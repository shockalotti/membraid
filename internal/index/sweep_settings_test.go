package index

import (
	"testing"
	"time"
)

// Staleness thresholds come from the settings in force, and each report
// records the ones it used.
func TestSweepUsesTheStalenessSettings(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	ix.Write(Memory{Kind: KindInsight, Content: "a fact nobody uses", Scope: "g1", Source: "x"})
	ix.Write(Memory{Kind: KindTaskState, Key: "task.a", Content: "an open task", Scope: "g1", Source: "x"})
	clock = clock.Add(10 * 24 * time.Hour)

	r, err := ix.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if r.StaleRows != 0 || r.StaleTasks != 0 || r.UnusedDays != 90 || r.TaskDays != 14 {
		t.Errorf("at the defaults nothing is stale after 10 days: %+v", r)
	}
	if stale, _ := ix.StaleTasks("g1"); len(stale) != 0 {
		t.Errorf("no stale tasks at the default: %v", stale)
	}

	rk := DefaultRanking()
	rk.SweepUnusedDays, rk.StaleTaskDays = 7, 3
	ix.SetRanking(rk)
	if r, _ = ix.Sweep(); r.StaleRows != 2 || r.StaleTasks != 1 || r.UnusedDays != 7 || r.TaskDays != 3 {
		t.Errorf("with 7 and 3 days, both are stale: %+v", r)
	}
	if stale, _ := ix.StaleTasks("g1"); len(stale) != 1 {
		t.Errorf("the open task is stale at 3 days: %v", stale)
	}

	if ix.SweepDue(24 * time.Hour) {
		t.Error("a sweep just ran")
	}
	clock = clock.Add(2 * 24 * time.Hour)
	if !ix.SweepDue(24*time.Hour) || ix.SweepDue(7*24*time.Hour) {
		t.Error("due follows the cadence passed in")
	}
}
