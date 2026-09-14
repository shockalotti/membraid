package index

import (
	"encoding/json"
	"time"
)

// Sweep is the maintenance pass that keeps a store nobody curates healthy
// (SPEC §9). It deletes nothing. Memories that have gone unused sink through
// decay on their own; sweep counts them, flags open tasks nobody has touched,
// records the retrieval state, and leaves a report where a human will see it.
//
// SPEC §9 also archives concept files, expired drafts and deprecated concepts.
// Concepts do not exist yet, so those parts wait for distillation.

const (
	// SweepUnusedDays is how long a memory can go unwritten and unretrieved
	// before sweep counts it as stale (SPEC §15 sweep_unused_days).
	SweepUnusedDays = 90
	// StaleTaskDays is how long an open task can go untouched before it is
	// flagged as possibly finished or abandoned. Flagged, never hidden.
	StaleTaskDays = 14
	// SweepEvery is how often the scheduled sync runs a sweep (SPEC §15
	// sweep_every).
	SweepEvery = 7 * 24 * time.Hour
)

// SweepReport is what one pass found.
type SweepReport struct {
	At string `json:"at"`
	// Current memories, and how many are stale: unused for SweepUnusedDays and
	// not linked to a concept.
	Current    int `json:"current"`
	StaleRows  int `json:"stale_rows"`
	StaleTasks int `json:"stale_tasks"`
	// Checkpointed is how many retrieval times were written to the wire log.
	Checkpointed int `json:"checkpointed"`
	// Unscoped is how many current memories are quarantined with no project:
	// a growing number means an agent runs where no project resolves (SPEC 17).
	Unscoped int `json:"unscoped"`
}

// Sweep runs one pass. Maintenance never counts as retrieval (SPEC 7.2).
func (ix *Index) Sweep() (*SweepReport, error) {
	now := ix.now()
	r := &SweepReport{At: now.UTC().Format(time.RFC3339), Unscoped: ix.UnscopedCount()}
	rows, err := ix.db.Query(`SELECT kind, valid_from, last_retrieved, source_concept IS NOT NULL
	                            FROM memories WHERE valid_to IS NULL`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, validFrom string
		var retrieved *string
		var linked bool
		if err := rows.Scan(&kind, &validFrom, &retrieved, &linked); err != nil {
			rows.Close()
			return nil, err
		}
		r.Current++
		last := ""
		if retrieved != nil {
			last = *retrieved
		}
		unused := ix.unusedDays(validFrom, last)
		if kind == KindTaskState && unused >= StaleTaskDays {
			r.StaleTasks++
		}
		if !linked && unused >= SweepUnusedDays {
			r.StaleRows++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	n, err := ix.Checkpoint(0)
	if err != nil {
		return nil, err
	}
	r.Checkpointed = n
	buf, _ := json.Marshal(r)
	if err := ix.metaSet("last_sweep", string(buf)); err != nil {
		return nil, err
	}
	return r, nil
}

// LastSweep returns the most recent report, or nil if this index has never swept.
func (ix *Index) LastSweep() *SweepReport {
	raw := ix.metaGet("last_sweep")
	if raw == "" {
		return nil
	}
	var r SweepReport
	if json.Unmarshal([]byte(raw), &r) != nil {
		return nil
	}
	return &r
}

// SweepDue reports whether SweepEvery has passed since the last sweep.
func (ix *Index) SweepDue() bool {
	r := ix.LastSweep()
	if r == nil {
		return true
	}
	t, err := time.Parse(time.RFC3339, r.At)
	return err != nil || ix.now().Sub(t) >= SweepEvery
}

// StaleTasks returns open tasks in scope untouched for StaleTaskDays, mapped
// to how many days they have gone unused, so a digest or status can say so.
func (ix *Index) StaleTasks(scope string) (map[string]float64, error) {
	q := `SELECT id, valid_from, last_retrieved FROM memories WHERE valid_to IS NULL AND kind = ?`
	args := []any{KindTaskState}
	if scopes := effectiveScopes(scope); len(scopes) > 0 {
		q += ` AND scope IN (` + placeholders(len(scopes)) + `)`
		for _, s := range scopes {
			args = append(args, s)
		}
	}
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id, validFrom string
		var retrieved *string
		if err := rows.Scan(&id, &validFrom, &retrieved); err != nil {
			return nil, err
		}
		last := ""
		if retrieved != nil {
			last = *retrieved
		}
		if days := ix.unusedDays(validFrom, last); days >= StaleTaskDays {
			out[id] = days
		}
	}
	return out, rows.Err()
}
