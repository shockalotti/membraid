package index

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// Distillation (SPEC §8) turns a subject agents keep coming back to into one
// readable concept file in the vault. The index finds the subjects and records
// which rows a concept covers; writing the file is the vault's job.

// SubjectRow is one write to a subject, current or superseded.
type SubjectRow struct {
	ID, Content, Source, At, SessionRef string
	Current                             bool
	Concept                             string // source_concept, empty if unlinked
	Until                               string // valid_to; empty while current
}

// Subject is a keyed subject with its whole history, newest first.
type Subject struct {
	Scope, Kind, Key string
	Rows             []SubjectRow
}

// Current returns the live row. Subjects only exist while one is live.
func (s Subject) Current() SubjectRow {
	for _, r := range s.Rows {
		if r.Current {
			return r
		}
	}
	return s.Rows[0]
}

// Sessions counts the distinct sessions that wrote the subject.
func (s Subject) Sessions() int {
	seen := map[string]bool{}
	for _, r := range s.Rows {
		if r.SessionRef != "" {
			seen[r.SessionRef] = true
		}
	}
	return len(seen)
}

// Subjects returns keyed subjects that qualify for a concept (SPEC §8 step 3):
// a live answer written at least twice, or written in at least two sessions.
// Tasks never distill: they are transient by definition.
func (ix *Index) Subjects() ([]Subject, error) {
	rows, err := ix.db.Query(`
		SELECT m.scope, m.kind, m.key, m.id, m.content, m.source, m.valid_from,
		       COALESCE(m.session_ref, ''), m.valid_to IS NULL, COALESCE(m.source_concept, '')
		  FROM memories m
		 WHERE m.key IS NOT NULL AND m.kind <> ?
		   AND EXISTS (SELECT 1 FROM memories c
		                WHERE c.scope = m.scope AND c.kind = m.kind AND c.key = m.key AND c.valid_to IS NULL)
		 ORDER BY m.scope, m.kind, m.key, m.valid_from DESC`, KindTaskState)
	if err != nil {
		return nil, fmt.Errorf("index: subjects: %w", err)
	}
	defer rows.Close()
	var all []Subject
	for rows.Next() {
		var scope, kind, key string
		var r SubjectRow
		if err := rows.Scan(&scope, &kind, &key, &r.ID, &r.Content, &r.Source, &r.At, &r.SessionRef, &r.Current, &r.Concept); err != nil {
			return nil, err
		}
		if n := len(all); n == 0 || all[n-1].Scope != scope || all[n-1].Kind != kind || all[n-1].Key != key {
			all = append(all, Subject{Scope: scope, Kind: kind, Key: key})
		}
		all[len(all)-1].Rows = append(all[len(all)-1].Rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Subject
	for _, s := range all {
		if len(s.Rows) >= 2 || s.Sessions() >= 2 {
			out = append(out, s)
		}
	}
	return out, nil
}

// LinkConcept records that rowIDs are covered by the concept file at path: a
// distill line in the wire log (so a rebuild and every other machine keep the
// link, SPEC §3.4), then source_concept on the rows. The file must already be
// written: file first, line second. Rows already linked to path are skipped,
// and nothing is logged when none are left.
func (ix *Index) LinkConcept(rowIDs []string, path string) (int, error) {
	if len(rowIDs) == 0 || path == "" {
		return 0, nil
	}
	args := []any{path}
	for _, id := range rowIDs {
		args = append(args, id)
	}
	rows, err := ix.db.Query(`SELECT id FROM memories
	                           WHERE COALESCE(source_concept, '') <> ? AND id IN (`+placeholders(len(rowIDs))+`)`, args...)
	if err != nil {
		return 0, err
	}
	var todo []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, id)
	}
	rows.Close()
	if len(todo) == 0 {
		return 0, nil
	}
	sort.Strings(todo)
	line := wirelog.DistillLine{Header: wirelog.NewHeader(wirelog.TypeDistill, ix.now()), Rows: todo, Concept: path}
	if ix.log != nil {
		if err := ix.log.Append(line); err != nil {
			return 0, fmt.Errorf("index: wire log: %w", err)
		}
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := applyDistill(tx, line); err != nil {
		return 0, err
	}
	return len(todo), tx.Commit()
}

func applyDistill(tx *sql.Tx, d wirelog.DistillLine) error {
	for _, id := range d.Rows {
		res, err := tx.Exec(`UPDATE memories SET source_concept=? WHERE id=?`, d.Concept, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if err := notePending(tx, id, pendingDistill, d.TS, d.Concept); err != nil {
				return err
			}
		}
	}
	return nil
}

// ConceptWritten remembers the exact bytes membraid last wrote to a concept
// file (as a hash), and WrittenConcept returns it. A file whose content no
// longer matches was edited by a person, and is never overwritten.
// Per-machine state: a fresh index treats existing files as the human's.
func (ix *Index) ConceptWritten(path, hash string) error {
	return ix.metaSet("concept_hash:"+path, hash)
}

func (ix *Index) WrittenConcept(path string) string {
	return ix.metaGet("concept_hash:" + path)
}

// RetiredSubjects returns keyed subjects that have a note but no live answer
// any more: every row was forgotten, finished or closed. Their note still shows
// the last answer as current, so distillation marks it no longer current
// instead of leaving it to mislead whoever reads the vault (SPEC 6.2, cold
// deprecation).
func (ix *Index) RetiredSubjects() ([]Subject, error) {
	rows, err := ix.db.Query(`
		SELECT m.scope, m.kind, m.key, m.id, m.content, m.source, m.valid_from,
		       COALESCE(m.session_ref, ''), COALESCE(m.valid_to, ''), COALESCE(m.source_concept, '')
		  FROM memories m
		 WHERE m.key IS NOT NULL AND m.kind <> ?
		   AND NOT EXISTS (SELECT 1 FROM memories c
		                    WHERE c.scope = m.scope AND c.kind = m.kind AND c.key = m.key AND c.valid_to IS NULL)
		   AND EXISTS (SELECT 1 FROM memories l
		                WHERE l.scope = m.scope AND l.kind = m.kind AND l.key = m.key AND COALESCE(l.source_concept, '') <> '')
		 ORDER BY m.scope, m.kind, m.key, m.valid_from DESC`, KindTaskState)
	if err != nil {
		return nil, fmt.Errorf("index: retired subjects: %w", err)
	}
	defer rows.Close()
	var out []Subject
	for rows.Next() {
		var scope, kind, key string
		var r SubjectRow
		if err := rows.Scan(&scope, &kind, &key, &r.ID, &r.Content, &r.Source, &r.At, &r.SessionRef, &r.Until, &r.Concept); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].Scope != scope || out[n-1].Kind != kind || out[n-1].Key != key {
			out = append(out, Subject{Scope: scope, Kind: kind, Key: key})
		}
		out[len(out)-1].Rows = append(out[len(out)-1].Rows, r)
	}
	return out, rows.Err()
}
