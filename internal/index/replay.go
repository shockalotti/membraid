package index

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// Import order (SPEC changelog v1.18).
//
// Most lines give the same index whatever order they arrive in: a keyed write
// is live only if it is the newest on its subject, a checkpoint keeps the
// newest time. A rescope does not. It moves whatever is in a scope when it is
// applied, so a rescope that arrives after writes newer than it, or a write
// older than a rescope already applied, would leave one machine's index
// different from another's, and from a rebuild. Import detects exactly those
// cases and replays the whole log in timestamp order instead, which is what a
// rebuild does. They are rare, since a rescope is a deliberate act. Replay
// keeps everything the log does not hold: embeddings, heat, retrieval times
// not checkpointed yet.
//
// Lines that name a memory this index does not have yet (a close, a distill, a
// checkpoint row) are kept and applied when its write arrives. Before, they did
// nothing and were never retried, and a machine whose clock is behind can stamp
// a close earlier than the write it closes, so even a rebuild lost it.

const metaReplay = "replay_pending"

// maxAliasHops bounds alias resolution. applyRescope never creates a cycle,
// so this only guards against a damaged index.
const maxAliasHops = 32

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// resolveScope follows rescopes: a project moved from one scope id to another
// keeps receiving what is written under its old id, which a machine that has
// not noticed the move still derives.
func resolveScope(q rowQuerier, scope string) string {
	if scope == "" || scope == ScopeShared || scope == ScopeUnscoped || scope == "*" {
		return scope
	}
	cur := scope
	for i := 0; i < maxAliasHops; i++ {
		var dst string
		if err := q.QueryRow(`SELECT dst FROM scope_aliases WHERE src=?`, cur).Scan(&dst); err != nil {
			return cur
		}
		cur = dst
	}
	return cur
}

// CanonicalScope is where memories for scope live now: scope itself, unless a
// rescope moved it.
func (ix *Index) CanonicalScope(scope string) string { return resolveScope(ix.db, scope) }

type logEntry struct {
	wirelog.Entry
	host string
}

type logPosition struct {
	pos  int64
	tail string
}

// tailBytes is how much of a file before a saved position is fingerprinted.
const tailBytes = 256

// tailHash fingerprints the bytes just before pos. If a person edits a line
// before pos, those bytes shift, and import rereads the file rather than
// resuming mid-line and skipping a record.
func tailHash(path string, pos int64) string {
	if pos <= 0 {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	n := int64(tailBytes)
	if pos < n {
		n = pos
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, pos-n); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:16])
}

// readLog decodes every file from where this index stopped, or from the start,
// in timestamp order across files.
func readLog(tx *sql.Tx, files []string, fromStart bool) ([]logEntry, map[string]logPosition, error) {
	var entries []logEntry
	positions := map[string]logPosition{}
	for _, f := range files {
		base := filepath.Base(f)
		var pos int64
		if !fromStart {
			if err := tx.QueryRow(`SELECT pos FROM log_offsets WHERE file=?`, base).Scan(&pos); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, nil, err
			}
			// A file shorter than the saved position was replaced: restored from
			// a backup, re-cloned. One whose bytes before the position changed was
			// edited. Either way, reread it; ids make that safe.
			if fi, err := os.Stat(f); err == nil && fi.Size() < pos {
				pos = 0
			}
			var want string
			_ = tx.QueryRow(`SELECT value FROM meta WHERE key=?`, "log_tail:"+base).Scan(&want)
			if pos > 0 && want != "" && tailHash(f, pos) != want {
				pos = 0
			}
		}
		es, next, err := wirelog.ReadFrom(f, pos)
		if err != nil {
			return nil, nil, fmt.Errorf("index: import %s: %w", base, err)
		}
		host := wirelog.HostOfFile(f)
		for _, e := range es {
			entries = append(entries, logEntry{e, host})
		}
		positions[base] = logPosition{pos: next, tail: tailHash(f, next)}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time().Before(entries[j].Time()) })
	return entries, positions, nil
}

// everyLogFile is every log file in the directories of files: a replay reads
// all of them, whichever ones this import was given.
func everyLogFile(files []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		all, err := wirelog.Files(filepath.Dir(f))
		if err != nil {
			return nil, err
		}
		for _, g := range append(all, f) {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// needsReplay reports whether applying entries on top of this index could give
// a different result from replaying the whole log in order.
func needsReplay(tx *sql.Tx, entries []logEntry) (bool, error) {
	applied := map[string]bool{}
	involved := map[string]bool{}
	var newestRescope time.Time
	rows, err := tx.Query(`SELECT ts, src, dst FROM rescopes`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var ts, src, dst string
		if err := rows.Scan(&ts, &src, &dst); err != nil {
			rows.Close()
			return false, err
		}
		applied[ts+"\x00"+src+"\x00"+dst] = true
		involved[src], involved[dst] = true, true
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil && t.After(newestRescope) {
			newestRescope = t
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, e := range entries {
		switch {
		case e.Write != nil:
			var scope string
			err := tx.QueryRow(`SELECT scope FROM memories WHERE id=?`, e.Write.ID).Scan(&scope)
			if err == nil {
				continue // already here: a reread
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}
			if involved[e.Write.Scope] && e.Time().Before(newestRescope) {
				return true, nil
			}
		case e.Rescope != nil:
			r := e.Rescope
			if r.From == r.To || applied[r.TS+"\x00"+r.From+"\x00"+r.To] {
				continue
			}
			newest, err := newestTouching(tx, r.From, r.To, newestRescope)
			if err != nil {
				return false, err
			}
			if e.Time().Before(newest) {
				return true, nil
			}
		case e.Close != nil:
			if newestRescope.IsZero() || !e.Time().Before(newestRescope) {
				continue
			}
			var scope string
			err := tx.QueryRow(`SELECT scope FROM memories WHERE id=? AND valid_to IS NULL`, e.Close.ID).Scan(&scope)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return false, err
			}
			if involved[scope] {
				return true, nil
			}
		}
	}
	return false, nil
}

// newestTouching is the newest change already applied to either scope of a
// rescope, or any rescope at all.
func newestTouching(tx *sql.Tx, from, to string, newest time.Time) (time.Time, error) {
	rows, err := tx.Query(`SELECT valid_from, COALESCE(valid_to, '') FROM memories WHERE scope IN (?, ?)`, from, to)
	if err != nil {
		return newest, err
	}
	defer rows.Close()
	for rows.Next() {
		var vf, vt string
		if err := rows.Scan(&vf, &vt); err != nil {
			return newest, err
		}
		for _, s := range []string{vf, vt} {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil && t.After(newest) {
				newest = t
			}
		}
	}
	return newest, rows.Err()
}

// resetForReplay empties what the log rebuilds, returning the number of
// memories before and each memory's retrieval time, which the log may not
// hold yet.
func resetForReplay(tx *sql.Tx) (int, map[string]string, error) {
	var before int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&before); err != nil {
		return 0, nil, err
	}
	retrieved := map[string]string{}
	rows, err := tx.Query(`SELECT id, last_retrieved FROM memories WHERE last_retrieved IS NOT NULL`)
	if err != nil {
		return 0, nil, err
	}
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return 0, nil, err
		}
		retrieved[id] = at
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	for _, table := range []string{"memories", "memories_fts", "rescopes", "scope_aliases", "pending_refs"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return 0, nil, err
		}
	}
	return before, retrieved, nil
}

// restoreRetrieved puts back retrieval times newer than the log's.
func restoreRetrieved(tx *sql.Tx, retrieved map[string]string) error {
	for id, at := range retrieved {
		var cur sql.NullString
		err := tx.QueryRow(`SELECT last_retrieved FROM memories WHERE id=?`, id).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !cur.Valid || newerTime(at, cur.String) {
			if _, err := tx.Exec(`UPDATE memories SET last_retrieved=? WHERE id=?`, at, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Pending line kinds: what a line said about a memory that was not here yet.
const (
	pendingClose     = "close"
	pendingDistill   = "distill"
	pendingRetrieved = "retrieved"
)

func notePending(tx *sql.Tx, id, kind, ts, value string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO pending_refs (id, kind, ts, value) VALUES (?,?,?,?)`, id, kind, ts, value)
	return err
}

// applyPending applies, to a memory just written, whatever lines named it
// before it arrived: a close, the newest note link, the newest retrieval.
func applyPending(tx *sql.Tx, id string) error {
	rows, err := tx.Query(`SELECT kind, ts, value FROM pending_refs WHERE id=?`, id)
	if err != nil {
		return err
	}
	type pending struct{ kind, ts, value string }
	var ps []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.kind, &p.ts, &p.value); err != nil {
			rows.Close()
			return err
		}
		ps = append(ps, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ps) == 0 {
		return err
	}
	sort.Slice(ps, func(i, j int) bool { return newerTime(ps[j].ts, ps[i].ts) })
	for _, p := range ps {
		var err error
		switch p.kind {
		case pendingClose:
			_, err = tx.Exec(`UPDATE memories SET valid_to=? WHERE id=? AND valid_to IS NULL`, p.ts, id)
		case pendingDistill:
			_, err = tx.Exec(`UPDATE memories SET source_concept=? WHERE id=?`, p.value, id)
		case pendingRetrieved:
			var cur sql.NullString
			if err = tx.QueryRow(`SELECT last_retrieved FROM memories WHERE id=?`, id).Scan(&cur); err == nil && (!cur.Valid || newerTime(p.ts, cur.String)) {
				_, err = tx.Exec(`UPDATE memories SET last_retrieved=? WHERE id=?`, p.ts, id)
			}
		}
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM pending_refs WHERE id=?`, id)
	return err
}

// applyScopes restores the project registry from a checkpoint. A name follows
// whichever machine saw the project most recently; a path is only ever this
// machine's own, since another machine's checkout path means nothing here.
func applyScopes(tx *sql.Tx, scopes []wirelog.CheckpointScope, host, self string) error {
	for _, s := range scopes {
		if s.Scope == "" || s.Scope == ScopeShared || s.Scope == ScopeUnscoped || s.Name == "" {
			continue
		}
		if resolveScope(tx, s.Scope) != s.Scope {
			continue // moved by a rescope: its registry entry went with it
		}
		var name, firstSeen, lastSeen string
		var path sql.NullString
		err := tx.QueryRow(`SELECT name, path, first_seen, last_seen FROM scopes WHERE scope=?`, s.Scope).Scan(&name, &path, &firstSeen, &lastSeen)
		if errors.Is(err, sql.ErrNoRows) {
			var p any
			if host == self && s.Path != "" {
				p = s.Path
			}
			if _, err := tx.Exec(`INSERT INTO scopes (scope, name, path, first_seen, last_seen) VALUES (?,?,?,?,?)`,
				s.Scope, s.Name, p, s.FirstSeen, s.LastSeen); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if newerTime(s.LastSeen, lastSeen) {
			name, lastSeen = s.Name, s.LastSeen
			if host == self && s.Path != "" {
				path = sql.NullString{String: s.Path, Valid: true}
			}
		}
		if newerTime(firstSeen, s.FirstSeen) {
			firstSeen = s.FirstSeen
		}
		if _, err := tx.Exec(`UPDATE scopes SET name=?, path=?, first_seen=?, last_seen=? WHERE scope=?`,
			name, path, firstSeen, lastSeen, s.Scope); err != nil {
			return err
		}
	}
	return nil
}

// scopeSnapshot is the registry entries whose name, path or day last seen
// changed since they were last logged, with the signature to record once the
// line is written. Once a day per project in use, at most.
func (ix *Index) scopeSnapshot() ([]wirelog.CheckpointScope, map[string]string, error) {
	rows, err := ix.db.Query(`SELECT scope, name, COALESCE(path, ''), first_seen, last_seen FROM scopes ORDER BY scope`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []wirelog.CheckpointScope
	sigs := map[string]string{}
	for rows.Next() {
		var s wirelog.CheckpointScope
		if err := rows.Scan(&s.Scope, &s.Name, &s.Path, &s.FirstSeen, &s.LastSeen); err != nil {
			return nil, nil, err
		}
		day := s.LastSeen
		if len(day) > 10 {
			day = day[:10]
		}
		sig := s.Name + "\x1f" + s.Path + "\x1f" + day
		if ix.metaGet("scope_logged:"+s.Scope) == sig {
			continue
		}
		out = append(out, s)
		sigs[s.Scope] = sig
	}
	return out, sigs, rows.Err()
}
