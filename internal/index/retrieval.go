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
	var cands []Hit
	var relevance []float64
	for rows.Next() {
		var h Hit
		var key, retrieved sql.NullString
		var bm25 float64
		if err := rows.Scan(&h.ID, &h.Kind, &key, &h.Content, &h.Scope, &h.Source, &h.At, &retrieved, &bm25); err != nil {
			return nil, err
		}
		h.Key = key.String
		// bm25 is negative in FTS5, more negative for a better match.
		cands = append(cands, h)
		relevance = append(relevance, math.Max(-bm25, 1e-9))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return ix.rankByUse(cands, relevance, limit)
}

// rankByUse scores candidates as relevance x boost(heat) and returns the best
// limit, each with its Why. Ties keep the order the candidates came in.
func (ix *Index) rankByUse(cands []Hit, relevance []float64, limit int) ([]Hit, error) {
	whys, err := ix.explain(cands)
	if err != nil {
		return nil, err
	}
	// Relevance leads and use tips close calls. Relevance is taken relative to
	// the best match, and use can move a score only within a bounded band, so a
	// heavily used memory cannot outrank a clearly better match. Multiplying raw
	// relevance by boost could: cosine similarities sit close together, and a
	// boost of 4 outweighed the difference between a poor match and a good one.
	best := 1e-9
	for _, r := range relevance {
		best = math.Max(best, r)
	}
	for i := range cands {
		w := whys[cands[i].ID]
		// A negative cosine would invert the ordering; nothing that far off matters.
		w.Relevance = math.Max(relevance[i], 1e-9)
		w.UseFactor = useFactor(w.Boost)
		w.Score = w.Relevance / best * w.UseFactor
		cands[i].Why = w
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Why.Score > cands[j].Why.Score })
	if len(cands) > limit {
		cands = cands[:limit]
	}
	return cands, nil
}

// applyCheckpoint restores retrieval state from a checkpoint line. SPEC 3.4
// calls a checkpoint a full snapshot that supersedes earlier ones, which held
// while one machine wrote the log. With one log per machine, a snapshot from
// one machine replacing another's would erase the other's retrievals, so
// replay keeps the newest time per row instead. The line format is unchanged.
// Concept entries are ignored: the concept mirror is not filled (V1-SCOPE).
func applyCheckpoint(tx *sql.Tx, c wirelog.CheckpointLine) error {
	for _, r := range c.Rows {
		var cur sql.NullString
		err := tx.QueryRow(`SELECT last_retrieved FROM memories WHERE id=?`, r.ID).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			if err := notePending(tx, r.ID, pendingRetrieved, r.LastRetrieved, ""); err != nil {
				return err
			}
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
	// Only what changed since the last checkpoint. Replay keeps the newest
	// value per row and per machine and subject, so a run of these lines
	// rebuilds exactly what full snapshots would, while each line stays the
	// size of the activity since the last one rather than of every memory ever
	// used: the log is never pruned. An index with no checkpoint yet (new, or
	// rebuilt) writes everything once. A time imported from another machine's
	// line can be sent on again; that echo scales with activity, not the store.
	heat, uses, err := ix.heatSnapshot(last)
	if err != nil {
		return 0, err
	}
	rows, err := ix.db.Query(`SELECT id, last_retrieved FROM memories WHERE last_retrieved IS NOT NULL ORDER BY id`)
	if err != nil {
		return 0, err
	}
	var entries []wirelog.CheckpointRow
	for rows.Next() {
		var r wirelog.CheckpointRow
		if err := rows.Scan(&r.ID, &r.LastRetrieved); err != nil {
			rows.Close()
			return 0, err
		}
		if last == "" || newerTime(r.LastRetrieved, last) {
			entries = append(entries, r)
		}
	}
	rows.Close()
	scopes, scopeSigs, err := ix.scopeSnapshot()
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 && len(heat) == 0 && len(scopes) == 0 {
		return 0, nil
	}
	if entries == nil {
		entries = []wirelog.CheckpointRow{}
	}
	line := wirelog.CheckpointLine{
		Header:   wirelog.NewHeader(wirelog.TypeCheckpoint, now),
		Rows:     entries,
		Concepts: []wirelog.CheckpointConcept{},
		Heat:     heat,
		Uses:     uses,
		Scopes:   scopes,
	}
	if err := ix.log.Append(line); err != nil {
		return 0, fmt.Errorf("index: wire log: %w", err)
	}
	for sc, sig := range scopeSigs {
		if err := ix.metaSet("scope_logged:"+sc, sig); err != nil {
			return 0, err
		}
	}
	return len(entries) + len(heat), ix.metaSet("last_checkpoint", now.UTC().Format(retrievedFormat))
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
	Uses        float64 `json:"uses"`
	Heat        float64 `json:"heat"`
	Boost       float64 `json:"boost"`
	KindWeight  float64 `json:"kind_weight"`
	ScopeWeight float64 `json:"scope_weight"`
}

var digestKindWeight = map[string]float64{
	KindPreference:   1.0, // how the user wants things done applies in every session
	KindProjectParam: 0.9, // concrete values for the project in hand
	KindInsight:      0.6, // situational; search finds the rest
}

// digestMinPreferences keeps standing preferences from being crowded out by a
// burst of newer insights.
const digestMinPreferences = 3

// The digest is what agents follow, and following a memory is using it, so a
// memory in the digest gains heat and stays there. Two limits keep that loop
// from closing: boost counts only up to digestBoostCap in the digest, and the
// digestFreshSlots newest memories always get a place.
const (
	digestBoostCap   = 2.0
	digestFreshSlots = 3
)

// Digest picks the n memories a session should start with, by what is known
// rather than only by what is newest:
//
//	score = kind weight x scope weight x boost(heat)
//
// Heat counts every write to the subject, history included, and every use an
// agent reported, each fading with the halflife: a subject restated across
// sessions or used again and again has proven it matters. Up to three
// preferences are kept even when newer memories outscore them. Reading the
// digest is not use.
func (ix *Index) Digest(scope string, n int) ([]Scored, error) {
	if n <= 0 {
		return nil, nil
	}
	kinds := []string{KindPreference, KindProjectParam, KindInsight}
	args := []any{}
	q := `SELECT m.id, m.kind, m.key, m.content, m.scope, m.source, m.valid_from
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
	var hits []Hit
	for rows.Next() {
		var h Hit
		var key sql.NullString
		if err := rows.Scan(&h.ID, &h.Kind, &key, &h.Content, &h.Scope, &h.Source, &h.At); err != nil {
			return nil, err
		}
		h.Key = key.String
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	whys, err := ix.explain(hits)
	if err != nil {
		return nil, err
	}
	for _, h := range hits {
		w := whys[h.ID]
		s := Scored{Hit: h, Writes: w.Writes, Uses: w.Uses, Heat: w.Heat, Boost: w.Boost}
		s.KindWeight = digestKindWeight[s.Kind]
		s.ScopeWeight = 1
		if s.Scope == ScopeShared && scope != ScopeShared && scope != "" {
			s.ScopeWeight = ix.Ranking().DigestSharedWeight
		}
		s.Score = s.KindWeight * s.ScopeWeight * math.Min(s.Boost, digestBoostCap)
		all = append(all, s)
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
	// Keep room for what is new: the newest memories get a slot each, taken from
	// the lowest-scored memories that are neither preferences nor new.
	newest := append([]Scored(nil), all...)
	sort.SliceStable(newest, func(i, j int) bool { return newest[i].At > newest[j].At })
	inPicked := map[string]bool{}
	for _, s := range picked {
		inPicked[s.ID] = true
	}
	protected := map[string]bool{}
	for _, s := range newest {
		if len(protected) >= digestFreshSlots || len(protected) >= len(picked) {
			break
		}
		if inPicked[s.ID] {
			protected[s.ID] = true
			continue
		}
		sort.SliceStable(picked, func(i, j int) bool { return picked[i].Score > picked[j].Score })
		for i := len(picked) - 1; i >= 0; i-- {
			if picked[i].Kind != KindPreference && !protected[picked[i].ID] {
				delete(inPicked, picked[i].ID)
				picked[i] = s
				inPicked[s.ID] = true
				protected[s.ID] = true
				break
			}
		}
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].Score > picked[j].Score })
	return picked, nil
}
