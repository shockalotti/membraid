package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// Repair restores wire-log lines this machine's index holds but its log lost.
//
// Before v0.5.3, a long-running MCP server kept writing to a log file that a
// sync's rebase had replaced, so its writes and closes reached this index and
// nowhere else: no commit, no other machine, no rebuild. The index still has
// them. Repair rebuilds a scratch index from the log, compares, and appends a
// line for each memory the log never wrote and each close it never recorded,
// stamped with the original time so every machine applies it as it happened.
//
// A replay (import.go) does the same before it empties the index, since it
// rebuilds from the log and would otherwise drop the only copy.

// RepairReport lists what the log was missing.
type RepairReport struct {
	Writes []Hit    `json:"writes"`
	Closes []string `json:"closes"`
	// Applied is false for a dry run.
	Applied bool `json:"applied"`
}

type liveState struct {
	m            Memory
	validFrom    string
	validTo      string
	supersededBy string
}

// Repair finds lines the log is missing and, unless dryRun, appends them to
// this machine's log.
func (ix *Index) Repair(dryRun bool) (*RepairReport, error) {
	if ix.log == nil {
		return nil, fmt.Errorf("index: repair needs the wire log")
	}
	r, lines, err := ix.missingLines()
	if err != nil || dryRun || len(lines) == 0 {
		return r, err
	}
	if err := ix.appendLines(lines); err != nil {
		return r, err
	}
	r.Applied = true
	// The lines describe what this index already holds; import only records
	// how far it has read.
	_, err = ix.ImportAll()
	return r, err
}

func (ix *Index) appendLines(lines []any) error {
	for _, l := range lines {
		if err := ix.log.Append(l); err != nil {
			return fmt.Errorf("index: wire log: %w", err)
		}
	}
	return nil
}

// missingLines compares this index with one rebuilt from the log, and returns
// the write and close lines that would make the log hold everything here.
func (ix *Index) missingLines() (*RepairReport, []any, error) {
	r := &RepairReport{Writes: []Hit{}, Closes: []string{}}
	if ix.log == nil {
		return r, nil, nil
	}
	files, err := ix.log.Files()
	if err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp("", "membraid-repair-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	fromLog, err := Open(filepath.Join(tmp, "index.db"), nil)
	if err != nil {
		return nil, nil, err
	}
	defer fromLog.Close()
	if _, err := fromLog.ImportLog(files); err != nil {
		return nil, nil, err
	}

	live, err := memoryStates(ix.db)
	if err != nil {
		return nil, nil, err
	}
	logged, err := memoryStates(fromLog.db)
	if err != nil {
		return nil, nil, err
	}

	var writes []wirelog.WriteLine
	relogged := map[string]bool{}
	for id, s := range live {
		if _, ok := logged[id]; ok {
			continue
		}
		relogged[id] = true
		line := wirelog.WriteLine{
			Header: wirelog.Header{V: wirelog.CurrentVersion, T: wirelog.TypeWrite, TS: s.validFrom},
			ID:     id, Scope: s.m.Scope, Source: s.m.Source, Kind: s.m.Kind,
			Content: s.m.Content, Confidence: s.m.Confidence, SessionRef: s.m.SessionRef,
			Superseded: []wirelog.Superseded{},
		}
		if s.m.Key != "" {
			k, mode := s.m.Key, wirelog.ModeKey
			line.Key, line.SupersedeMode = &k, &mode
		}
		// Rows this write closed are named explicitly: an unkeyed or explicit
		// supersession cannot be derived again on replay.
		for other, o := range live {
			if o.supersededBy == id {
				line.Superseded = append(line.Superseded, wirelog.Superseded{ID: other, Key: o.m.Key})
			}
		}
		if s.m.Key == "" && len(line.Superseded) > 0 {
			mode := wirelog.ModeExplicit
			line.SupersedeMode = &mode
		}
		sort.Slice(line.Superseded, func(i, j int) bool { return line.Superseded[i].ID < line.Superseded[j].ID })
		writes = append(writes, line)
		r.Writes = append(r.Writes, Hit{ID: id, Kind: s.m.Kind, Key: s.m.Key, Content: s.m.Content, Scope: s.m.Scope, Source: s.m.Source, At: s.validFrom})
	}

	var closes []wirelog.CloseLine
	for id, s := range live {
		if s.validTo == "" {
			continue
		}
		if s.supersededBy != "" && (relogged[s.supersededBy] || logged[s.supersededBy].validFrom != "") {
			continue // closed by a write the log has, or is about to
		}
		if l, ok := logged[id]; ok && l.validTo != "" {
			continue
		}
		closes = append(closes, wirelog.CloseLine{
			Header: wirelog.Header{V: wirelog.CurrentVersion, T: wirelog.TypeClose, TS: s.validTo},
			ID:     id, Reason: "restored",
		})
		r.Closes = append(r.Closes, id)
	}

	sort.Slice(writes, func(i, j int) bool { return writes[i].TS < writes[j].TS })
	sort.Slice(r.Writes, func(i, j int) bool { return r.Writes[i].At < r.Writes[j].At })
	sort.Strings(r.Closes)
	var lines []any
	for _, w := range writes {
		lines = append(lines, w)
	}
	for _, c := range closes {
		lines = append(lines, c)
	}
	return r, lines, nil
}

func memoryStates(db *sql.DB) (map[string]liveState, error) {
	rows, err := db.Query(`SELECT id, kind, COALESCE(key, ''), content, scope, source, valid_from,
	                              COALESCE(valid_to, ''), COALESCE(superseded_by, ''), confidence, COALESCE(session_ref, '')
	                         FROM memories`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]liveState{}
	for rows.Next() {
		var s liveState
		var conf sql.NullFloat64
		if err := rows.Scan(&s.m.ID, &s.m.Kind, &s.m.Key, &s.m.Content, &s.m.Scope, &s.m.Source, &s.validFrom,
			&s.validTo, &s.supersededBy, &conf, &s.m.SessionRef); err != nil {
			return nil, err
		}
		if conf.Valid {
			c := conf.Float64
			s.m.Confidence = &c
		}
		if _, err := time.Parse(time.RFC3339Nano, s.validFrom); err != nil {
			continue
		}
		out[s.m.ID] = s
	}
	return out, rows.Err()
}
