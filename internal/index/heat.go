package index

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shockalotti/membraid/internal/wirelog"
)

const defaultHalflifeDays = 30

// Ranking orders memories by relevance times how much they have been used. It
// is the user's, not the machine's: callers load it from the vault's shared
// settings, so every machine ranks the same memories the same way.
type Ranking struct {
	// HalflifeDays is how long a write's or a use's weight takes to halve.
	HalflifeDays float64 `json:"halflife_days"`
	// FrequencyBoost is how strongly heat above one use lifts a memory:
	// boost = 1 + FrequencyBoost x ln(heat). 0 means use only keeps a memory
	// fresh and repetition adds nothing.
	FrequencyBoost float64 `json:"frequency_boost"`
	// DigestItems is how many known memories a session digest carries.
	DigestItems int `json:"digest_items"`
	// DigestSharedWeight scales shared memories against a project's own in its
	// digest.
	DigestSharedWeight float64 `json:"digest_shared_weight"`
}

func DefaultRanking() Ranking {
	return Ranking{HalflifeDays: defaultHalflifeDays, FrequencyBoost: 1, DigestItems: 12, DigestSharedWeight: 0.7}
}

// Clamped replaces any value outside its meaningful range with the default.
func (r Ranking) Clamped() Ranking {
	d := DefaultRanking()
	if r.HalflifeDays < 1 || r.HalflifeDays > 3650 || math.IsNaN(r.HalflifeDays) {
		r.HalflifeDays = d.HalflifeDays
	}
	if r.FrequencyBoost < 0 || r.FrequencyBoost > 5 || math.IsNaN(r.FrequencyBoost) {
		r.FrequencyBoost = d.FrequencyBoost
	}
	if r.DigestItems < 1 || r.DigestItems > 50 {
		r.DigestItems = d.DigestItems
	}
	if r.DigestSharedWeight <= 0 || r.DigestSharedWeight > 1 || math.IsNaN(r.DigestSharedWeight) {
		r.DigestSharedWeight = d.DigestSharedWeight
	}
	return r
}

func (ix *Index) SetRanking(r Ranking) { ix.rank = r.Clamped() }

// Ranking is the ranking in force: the defaults until SetRanking is called.
func (ix *Index) Ranking() Ranking {
	if ix.rank == (Ranking{}) {
		return DefaultRanking()
	}
	return ix.rank
}

// decay is exp(-ln2 * days / halflife): what is left of a write or a use made
// days ago. 1 today, 0.5 after one halflife, 0.25 after two.
func (ix *Index) decay(days float64) float64 {
	return math.Exp(-math.Ln2 * math.Max(0, days) / ix.Ranking().HalflifeDays)
}

// decayAt is what is left now of a write or a use made at t.
func (ix *Index) decayAt(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return ix.decay(ix.now().Sub(t).Hours() / 24)
}

// boost is how much use lifts a memory: its heat up to one use, then growing
// only logarithmically, so a heavily used memory gets a lift but cannot outrank
// a clearly more relevant one.
func (ix *Index) boost(heat float64) float64 {
	if heat <= 1 {
		return heat
	}
	return 1 + ix.Ranking().FrequencyBoost*math.Log(heat)
}

// Why is how a memory earned its place.
//
//	heat  = sum over writes and uses of 0.5 ^ (days since / halflife)
//	boost = heat when heat <= 1, else 1 + frequency_boost x ln(heat)
//	score = relevance x boost
type Why struct {
	Relevance float64 `json:"relevance"`
	// Writes is how many times the subject was written, history included.
	Writes int `json:"writes"`
	// Uses is the part of heat that comes from reported uses, on every machine.
	Uses     float64 `json:"uses"`
	Heat     float64 `json:"heat"`
	Boost    float64 `json:"boost"`
	LastUsed string  `json:"last_used,omitempty"`
	Score    float64 `json:"score"`
}

const subjectSep = "\x1f"

// subjectFor names what heat attaches to. A keyed memory's subject is its
// scope, kind and key, so restating it keeps its heat; an unkeyed memory is
// its own subject.
func subjectFor(scope, kind, key, id string) string {
	if key != "" {
		return "k" + subjectSep + scope + subjectSep + kind + subjectSep + key
	}
	return "i" + subjectSep + id
}

// explain computes each hit's use: every write to its subject counts as a use,
// plus every use recorded on any machine, each fading on the halflife. Keyed
// by hit id, with an entry for every hit; Relevance and Score are the caller's.
func (ix *Index) explain(hits []Hit) (map[string]*Why, error) {
	out := map[string]*Why{}
	if len(hits) == 0 {
		return out, nil
	}
	type acc struct {
		writes   int
		writeH   float64
		useH     float64
		lastUsed time.Time
	}
	bySubject := map[string]*acc{}
	subjectOfHit := map[string]string{}
	var keyed []Hit
	var unkeyed []any
	for _, h := range hits {
		s := subjectFor(h.Scope, h.Kind, h.Key, h.ID)
		subjectOfHit[h.ID] = s
		if _, seen := bySubject[s]; seen {
			continue
		}
		bySubject[s] = &acc{}
		if h.Key != "" {
			keyed = append(keyed, h)
		} else {
			unkeyed = append(unkeyed, h.ID)
		}
	}
	record := func(subject, at string, weight float64, isWrite bool) {
		a := bySubject[subject]
		t, err := time.Parse(time.RFC3339Nano, at)
		if a == nil || err != nil {
			return
		}
		if isWrite {
			a.writes++
			a.writeH += weight * ix.decayAt(t)
		} else {
			a.useH += weight * ix.decayAt(t)
		}
		if t.After(a.lastUsed) {
			a.lastUsed = t
		}
	}

	// Writes, history included: a subject restated has been used again.
	for start := 0; start < len(keyed); start += 200 {
		batch := keyed[start:min(start+200, len(keyed))]
		vals := make([]string, len(batch))
		args := make([]any, 0, 3*len(batch))
		for i, h := range batch {
			vals[i] = "(?,?,?)"
			args = append(args, h.Scope, h.Kind, h.Key)
		}
		rows, err := ix.db.Query(`SELECT scope, kind, key, valid_from FROM memories
			WHERE key IS NOT NULL AND (scope, kind, key) IN (VALUES `+strings.Join(vals, ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("index: heat: %w", err)
		}
		for rows.Next() {
			var scope, kind, key, at string
			if err := rows.Scan(&scope, &kind, &key, &at); err != nil {
				rows.Close()
				return nil, err
			}
			record(subjectFor(scope, kind, key, ""), at, 1, true)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if len(unkeyed) > 0 {
		rows, err := ix.db.Query(`SELECT id, valid_from FROM memories WHERE id IN (`+placeholders(len(unkeyed))+`)`, unkeyed...)
		if err != nil {
			return nil, fmt.Errorf("index: heat: %w", err)
		}
		for rows.Next() {
			var id, at string
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return nil, err
			}
			record(subjectFor("", "", "", id), at, 1, true)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Reported uses: this machine's, and each other machine's latest.
	subjects := make([]any, 0, len(bySubject))
	for s := range bySubject {
		subjects = append(subjects, s)
	}
	for _, table := range []string{"heat_local", "heat_remote"} {
		rows, err := ix.db.Query(`SELECT subject, heat, at FROM `+table+` WHERE subject IN (`+placeholders(len(subjects))+`)`, subjects...)
		if err != nil {
			return nil, fmt.Errorf("index: heat: %w", err)
		}
		for rows.Next() {
			var s, at string
			var h float64
			if err := rows.Scan(&s, &h, &at); err != nil {
				rows.Close()
				return nil, err
			}
			record(s, at, h, false)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	for id, s := range subjectOfHit {
		a := bySubject[s]
		w := &Why{Writes: a.writes, Uses: a.useH, Heat: a.writeH + a.useH}
		w.Boost = ix.boost(w.Heat)
		if !a.lastUsed.IsZero() {
			w.LastUsed = a.lastUsed.UTC().Format(time.RFC3339)
		}
		out[id] = w
	}
	return out, nil
}

var ErrAmbiguousID = errors.New("index: id prefix matches more than one memory")

// resolveID finds a current memory by id, or by an id prefix of at least six
// characters, which is how the digest names memories without a key.
func resolveID(q querier, ref string) (*Hit, error) {
	ref = strings.TrimSpace(ref)
	if len(ref) < 6 {
		return nil, nil
	}
	rows, err := q.Query(`SELECT id, kind, COALESCE(key,''), content, scope, source, valid_from FROM memories
		WHERE valid_to IS NULL AND (id = ? OR id LIKE ? || '%') ORDER BY id = ? DESC LIMIT 2`, ref, ref, ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Kind, &h.Key, &h.Content, &h.Scope, &h.Source, &h.At); err != nil {
			return nil, err
		}
		found = append(found, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch {
	case len(found) == 0:
		return nil, nil
	case found[0].ID == ref || len(found) == 1:
		return &found[0], nil
	}
	return nil, fmt.Errorf("%w: %s", ErrAmbiguousID, ref)
}

// MarkUsed records that memories were used: an agent said a memory changed
// what it did (memory_used), or asked for exactly that memory (memory_get).
// Each use adds weight to its subject's heat on this machine. Appearing in a
// search result or the digest never does. refs are ids or id prefixes; the
// memories found are returned, so callers can say what was not.
func (ix *Index) MarkUsed(refs []string, source string, weight float64) ([]Hit, error) {
	if weight <= 0 {
		weight = 1
	}
	if source == "" {
		source = "unknown"
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := ix.now().UTC()
	nowText := now.Format(retrievedFormat)
	day := now.Format("2006-01-02")
	host := ix.selfHost()
	seen := map[string]bool{}
	var used []Hit
	for _, ref := range refs {
		h, err := resolveID(tx, ref)
		if err != nil {
			return nil, err
		}
		if h == nil || seen[h.ID] {
			continue
		}
		seen[h.ID] = true
		subject := subjectFor(h.Scope, h.Kind, h.Key, h.ID)
		var old float64
		var at string
		err = tx.QueryRow(`SELECT heat, at FROM heat_local WHERE subject=?`, subject).Scan(&old, &at)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		heat := weight
		if t, perr := time.Parse(time.RFC3339Nano, at); perr == nil {
			heat += old * ix.decayAt(t)
		}
		if _, err := tx.Exec(`INSERT INTO heat_local (subject, heat, at) VALUES (?,?,?)
			ON CONFLICT(subject) DO UPDATE SET heat=excluded.heat, at=excluded.at`, subject, heat, nowText); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO use_counts (host, day, source, n) VALUES (?,?,?,?)
			ON CONFLICT(host, day, source) DO UPDATE SET n = n + excluded.n`, host, day, source, weight); err != nil {
			return nil, err
		}
		used = append(used, *h)
	}
	return used, tx.Commit()
}

func (ix *Index) selfHost() string {
	if ix.log == nil {
		return ""
	}
	return ix.log.Host()
}

// heatSnapshot is this machine's heat updated after since (all of it when since
// is empty), and its last two weeks of use counts, for a checkpoint line. Use
// counts are always sent whole: they are a few rows, and a reader replaces a
// machine's counts with its newest line.
func (ix *Index) heatSnapshot(since string) ([]wirelog.CheckpointHeat, []wirelog.CheckpointUse, error) {
	rows, err := ix.db.Query(`SELECT subject, heat, at FROM heat_local ORDER BY subject`)
	if err != nil {
		return nil, nil, err
	}
	var heat []wirelog.CheckpointHeat
	for rows.Next() {
		var s, at string
		var h float64
		if err := rows.Scan(&s, &h, &at); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if since != "" && !newerTime(at, since) {
			continue
		}
		parts := strings.Split(s, subjectSep)
		entry := wirelog.CheckpointHeat{Heat: h, At: at}
		switch {
		case len(parts) == 4 && parts[0] == "k":
			entry.Scope, entry.Kind, entry.Key = parts[1], parts[2], parts[3]
		case len(parts) == 2 && parts[0] == "i":
			entry.ID = parts[1]
		default:
			continue
		}
		heat = append(heat, entry)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	fortnight := ix.now().UTC().AddDate(0, 0, -14).Format("2006-01-02")
	urows, err := ix.db.Query(`SELECT day, source, n FROM use_counts WHERE host=? AND day >= ? ORDER BY day, source`, ix.selfHost(), fortnight)
	if err != nil {
		return nil, nil, err
	}
	defer urows.Close()
	var uses []wirelog.CheckpointUse
	for urows.Next() {
		var u wirelog.CheckpointUse
		if err := urows.Scan(&u.Day, &u.Source, &u.N); err != nil {
			return nil, nil, err
		}
		uses = append(uses, u)
	}
	return heat, uses, urows.Err()
}

// applyHeat restores heat and use counts from a checkpoint written by host.
// This machine's own lines refill heat_local after a rebuild. Another
// machine's heat is kept per machine, newest wins, and summed at ranking time:
// that is exact, because heat fades in proportion, so each machine's value can
// be faded to now separately.
func applyHeat(tx *sql.Tx, c wirelog.CheckpointLine, host, self string) error {
	local := host == self
	for _, h := range c.Heat {
		if h.Key == "" && h.ID == "" {
			continue
		}
		subject := subjectFor(h.Scope, h.Kind, NormalizeKey(h.Key), h.ID)
		var cur string
		var err error
		if local {
			err = tx.QueryRow(`SELECT at FROM heat_local WHERE subject=?`, subject).Scan(&cur)
		} else {
			err = tx.QueryRow(`SELECT at FROM heat_remote WHERE subject=? AND host=?`, subject, host).Scan(&cur)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if cur != "" && !newerTime(h.At, cur) {
			continue
		}
		if local {
			_, err = tx.Exec(`INSERT INTO heat_local (subject, heat, at) VALUES (?,?,?)
				ON CONFLICT(subject) DO UPDATE SET heat=excluded.heat, at=excluded.at`, subject, h.Heat, h.At)
		} else {
			_, err = tx.Exec(`INSERT INTO heat_remote (subject, host, heat, at) VALUES (?,?,?,?)
				ON CONFLICT(subject, host) DO UPDATE SET heat=excluded.heat, at=excluded.at`, subject, host, h.Heat, h.At)
		}
		if err != nil {
			return err
		}
	}
	if len(c.Uses) == 0 {
		return nil
	}
	if local {
		for _, u := range c.Uses {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO use_counts (host, day, source, n) VALUES (?,?,?,?)`, host, u.Day, u.Source, u.N); err != nil {
				return err
			}
		}
		return nil
	}
	metaKey := "uses_ts:" + host
	var last string
	if err := tx.QueryRow(`SELECT value FROM meta WHERE key=?`, metaKey).Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if last != "" && !newerTime(c.TS, last) {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM use_counts WHERE host=?`, host); err != nil {
		return err
	}
	for _, u := range c.Uses {
		if _, err := tx.Exec(`INSERT INTO use_counts (host, day, source, n) VALUES (?,?,?,?)
			ON CONFLICT(host, day, source) DO UPDATE SET n=excluded.n`, host, u.Day, u.Source, u.N); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, metaKey, c.TS)
	return err
}

// rescopeHeat moves keyed subjects' heat with their memories. Where the target
// scope already has heat for the same subject, the target's stands and the
// moved heat is dropped.
func rescopeHeat(tx *sql.Tx, from, to string) error {
	prefix := "k" + subjectSep + from + subjectSep
	for _, table := range []string{"heat_local", "heat_remote"} {
		rows, err := tx.Query(`SELECT DISTINCT subject FROM `+table+` WHERE substr(subject, 1, ?) = ?`, len(prefix), prefix)
		if err != nil {
			return err
		}
		var subjects []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				rows.Close()
				return err
			}
			subjects = append(subjects, s)
		}
		rows.Close()
		for _, s := range subjects {
			moved := "k" + subjectSep + to + subjectSep + strings.TrimPrefix(s, prefix)
			if _, err := tx.Exec(`UPDATE OR IGNORE `+table+` SET subject=? WHERE subject=?`, moved, s); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE subject=?`, s); err != nil {
				return err
			}
		}
	}
	return nil
}

// UsesBySource is reported uses per agent over the last days days, summed over
// every machine.
func (ix *Index) UsesBySource(days int) (map[string]float64, error) {
	since := ix.now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	rows, err := ix.db.Query(`SELECT source, SUM(n) FROM use_counts WHERE day >= ? GROUP BY source`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var s string
		var n float64
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}
