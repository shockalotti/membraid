package index

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// retrievedFormat is fixed-width, so stored last_retrieved values also sort
// correctly as text. Comparisons still parse (see newerTime): values arrive
// from other machines' checkpoints, and RFC3339 text order is not time order
// when fractional seconds differ in length.
const retrievedFormat = "2006-01-02T15:04:05.000000000Z07:00"

const defaultHalflifeDays = 30

// SetHalflife sets how many days without retrieval halve a memory's rank
// (SPEC 7.2, halflife_days).
func (ix *Index) SetHalflife(days int) {
	if days < 1 {
		days = defaultHalflifeDays
	}
	ix.halflifeDays = float64(days)
}

func (ix *Index) halflife() float64 {
	if ix.halflifeDays <= 0 {
		return defaultHalflifeDays
	}
	return ix.halflifeDays
}

// decay is exp(-ln2 * unused / halflife): 1 for a memory used today, 0.5 after
// one halflife unused, 0.25 after two.
func (ix *Index) decay(unusedDays float64) float64 {
	return math.Exp(-math.Ln2 * unusedDays / ix.halflife())
}

// unusedDays counts from the later of the write and the last retrieval, floored
// at zero: a clock stepping backwards must not boost a row above one.
func (ix *Index) unusedDays(validFrom, lastRetrieved string) float64 {
	base, _ := time.Parse(time.RFC3339Nano, validFrom)
	if t, err := time.Parse(time.RFC3339Nano, lastRetrieved); err == nil && t.After(base) {
		base = t
	}
	if base.IsZero() {
		return 0
	}
	return math.Max(0, ix.now().Sub(base).Hours()/24)
}

func newerTime(a, b string) bool {
	ta, err := time.Parse(time.RFC3339Nano, a)
	if err != nil {
		return false
	}
	tb, err := time.Parse(time.RFC3339Nano, b)
	return err != nil || ta.After(tb)
}

// Touch records that memories were retrieved: returned to a caller by search,
// or looked up by key or id. Only intentional reads count (SPEC 7.2). The
// session digest, status output and maintenance never touch: whatever they
// show would otherwise keep itself fresh forever and never decay.
func (ix *Index) Touch(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{ix.now().UTC().Format(retrievedFormat)}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := ix.db.Exec(`UPDATE memories SET last_retrieved=? WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// Search runs FTS over current rows, with the scope predicate pushed into the
// query so a busy unrelated project cannot starve the results. bm25 picks a
// candidate pool wider than the result set, and decay reorders it: fusing only
// as many candidates as are returned would give decay nothing to reorder.
func (ix *Index) Search(q, scope string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	pool := limit * 5
	if pool < 50 {
		pool = 50
	}
	args := []any{ftsQuery(q)}
	sqlText := `
		SELECT m.id, m.kind, m.key, m.content, m.scope, m.source, m.valid_from,
		       m.last_retrieved, bm25(memories_fts)
		  FROM memories_fts f
		  JOIN memories m ON m.id = f.id
		 WHERE memories_fts MATCH ?
		   AND m.valid_to IS NULL`
	if scopes := effectiveScopes(scope); len(scopes) > 0 {
		sqlText += ` AND m.scope IN (` + placeholders(len(scopes)) + `)`
		for _, s := range scopes {
			args = append(args, s)
		}
	}
	sqlText += ` ORDER BY bm25(memories_fts) LIMIT ?`
	args = append(args, pool)

	rows, err := ix.db.Query(sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("index: query: %w", err)
	}
	defer rows.Close()
	type ranked struct {
		Hit
		score float64
	}
	var cands []ranked
	for rows.Next() {
		var h Hit
		var key, retrieved sql.NullString
		var bm25 float64
		if err := rows.Scan(&h.ID, &h.Kind, &key, &h.Content, &h.Scope, &h.Source, &h.At, &retrieved, &bm25); err != nil {
			return nil, err
		}
		h.Key = key.String
		// bm25 is negative in FTS5, more negative for a better match.
		relevance := math.Max(-bm25, 1e-9)
		cands = append(cands, ranked{h, relevance * ix.decay(ix.unusedDays(h.At, retrieved.String))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]Hit, len(cands))
	for i, c := range cands {
		out[i] = c.Hit
	}
	return out, nil
}

// applyCheckpoint restores retrieval state from a checkpoint line. SPEC 3.4
// calls a checkpoint a full snapshot that supersedes earlier ones, which held
// while one machine wrote the log. With one log per machine, a snapshot from
// one machine replacing another's would erase the other's retrievals, so
// replay keeps the newest time per row instead. The line format is unchanged.
// Concept entries are ignored until concepts exist.
func applyCheckpoint(tx *sql.Tx, c wirelog.CheckpointLine) error {
	for _, r := range c.Rows {
		var cur sql.NullString
		err := tx.QueryRow(`SELECT last_retrieved FROM memories WHERE id=?`, r.ID).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !cur.Valid || newerTime(r.LastRetrieved, cur.String) {
			if _, err := tx.Exec(`UPDATE memories SET last_retrieved=? WHERE id=?`, r.LastRetrieved, r.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Checkpoint appends this index's retrieval state to the wire log, so a rebuilt
// index, and every other machine, does not treat every memory as never used.
// It writes only when something was retrieved since the last checkpoint and at
// most once per minInterval, so an idle machine adds nothing to git. Returns
// the number of rows recorded, 0 when nothing was written.
func (ix *Index) Checkpoint(minInterval time.Duration) (int, error) {
	if ix.log == nil {
		return 0, nil
	}
	now := ix.now()
	last := ix.metaGet("last_checkpoint")
	if t, err := time.Parse(time.RFC3339Nano, last); err == nil && now.Sub(t) < minInterval {
		return 0, nil
	}
	rows, err := ix.db.Query(`SELECT id, last_retrieved FROM memories WHERE last_retrieved IS NOT NULL ORDER BY id`)
	if err != nil {
		return 0, err
	}
	var entries []wirelog.CheckpointRow
	newest := ""
	for rows.Next() {
		var r wirelog.CheckpointRow
		if err := rows.Scan(&r.ID, &r.LastRetrieved); err != nil {
			rows.Close()
			return 0, err
		}
		if newest == "" || newerTime(r.LastRetrieved, newest) {
			newest = r.LastRetrieved
		}
		entries = append(entries, r)
	}
	rows.Close()
	if len(entries) == 0 || (last != "" && !newerTime(newest, last)) {
		return 0, nil
	}
	line := wirelog.CheckpointLine{
		Header:   wirelog.NewHeader(wirelog.TypeCheckpoint, now),
		Rows:     entries,
		Concepts: []wirelog.CheckpointConcept{},
	}
	if err := ix.log.Append(line); err != nil {
		return 0, fmt.Errorf("index: wire log: %w", err)
	}
	return len(entries), ix.metaSet("last_checkpoint", now.UTC().Format(retrievedFormat))
}

func (ix *Index) metaGet(key string) string {
	var v string
	_ = ix.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return v
}

func (ix *Index) metaSet(key, value string) error {
	_, err := ix.db.Exec(`INSERT INTO meta (key, value) VALUES (?,?)
	                      ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// Scored is a digest candidate with the parts of its score, for --explain.
type Scored struct {
	Hit
	Score       float64 `json:"score"`
	Writes      int     `json:"writes"`
	UnusedDays  float64 `json:"unused_days"`
	KindWeight  float64 `json:"kind_weight"`
	ScopeWeight float64 `json:"scope_weight"`
	Decay       float64 `json:"decay"`
}

var digestKindWeight = map[string]float64{
	KindPreference:   1.0, // how the user wants things done applies in every session
	KindProjectParam: 0.9, // concrete values for the project in hand
	KindInsight:      0.6, // situational; search finds the rest
}

const (
	// digestSharedWeight ranks shared memories below this project's when
	// answering "what is known here".
	digestSharedWeight = 0.7
	// digestMinPreferences keeps standing preferences from being crowded out by
	// a burst of newer insights.
	digestMinPreferences = 3
)

// Digest picks the n memories a session should start with, by what is known
// rather than only by what is newest:
//
//	score = kind weight x scope weight x (1 + ln writes) x decay
//
// writes counts every write to the subject, history included: a subject
// restated across sessions has proven it matters. decay uses the later of the
// last write and the last retrieval. Up to three preferences are kept even
// when newer memories outscore them. Reading the digest does not touch
// retrieval state.
func (ix *Index) Digest(scope string, n int) ([]Scored, error) {
	if n <= 0 {
		return nil, nil
	}
	kinds := []string{KindPreference, KindProjectParam, KindInsight}
	args := []any{}
	q := `SELECT m.id, m.kind, m.key, m.content, m.scope, m.source, m.valid_from, m.last_retrieved,
	             CASE WHEN m.key IS NULL THEN 1 ELSE
	               (SELECT COUNT(*) FROM memories h WHERE h.scope=m.scope AND h.kind=m.kind AND h.key=m.key)
	             END
	        FROM memories m
	       WHERE m.valid_to IS NULL AND m.kind IN (` + placeholders(len(kinds)) + `)`
	for _, k := range kinds {
		args = append(args, k)
	}
	if scopes := effectiveScopes(scope); len(scopes) > 0 {
		q += ` AND m.scope IN (` + placeholders(len(scopes)) + `)`
		for _, s := range scopes {
			args = append(args, s)
		}
	}
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("index: digest: %w", err)
	}
	defer rows.Close()
	var all []Scored
	for rows.Next() {
		var s Scored
		var key, retrieved sql.NullString
		if err := rows.Scan(&s.ID, &s.Kind, &key, &s.Content, &s.Scope, &s.Source, &s.At, &retrieved, &s.Writes); err != nil {
			return nil, err
		}
		s.Key = key.String
		s.KindWeight = digestKindWeight[s.Kind]
		s.ScopeWeight = 1
		if s.Scope == ScopeShared && scope != ScopeShared && scope != "" {
			s.ScopeWeight = digestSharedWeight
		}
		s.UnusedDays = ix.unusedDays(s.At, retrieved.String)
		s.Decay = ix.decay(s.UnusedDays)
		s.Score = s.KindWeight * s.ScopeWeight * (1 + math.Log(float64(max(s.Writes, 1)))) * s.Decay
		all = append(all, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Newest first on equal score, so a fresh vault reads as it did before.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].At > all[j].At
	})
	if len(all) <= n {
		return all, nil
	}
	picked := append([]Scored(nil), all[:n]...)
	prefs := 0
	for _, s := range picked {
		if s.Kind == KindPreference {
			prefs++
		}
	}
	// Swap the lowest-scored non-preferences for the best preferences left out.
	for _, s := range all[n:] {
		if prefs >= digestMinPreferences {
			break
		}
		if s.Kind != KindPreference {
			continue
		}
		for i := len(picked) - 1; i >= 0; i-- {
			if picked[i].Kind != KindPreference {
				picked[i] = s
				prefs++
				break
			}
		}
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].Score > picked[j].Score })
	return picked, nil
}
