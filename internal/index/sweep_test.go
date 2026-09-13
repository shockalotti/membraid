package index

import (
	"testing"
	"time"
)

// Sweep counts what has gone unused without deleting it, spares memories that
// were retrieved or are linked to a concept, and flags open tasks nobody has
// touched.
func TestSweepCountsStaleMemoriesAndTasks(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })

	old, _ := ix.Write(Memory{Kind: KindInsight, Content: "an old observation", Source: "x"})
	used, _ := ix.Write(Memory{Kind: KindInsight, Content: "an old but useful observation", Source: "x"})
	linked, _ := ix.Write(Memory{Kind: KindInsight, Content: "distilled into a concept", Source: "x"})
	task, _ := ix.Write(Memory{Kind: KindTaskState, Key: "task.migrate", Content: "halfway through the migration", Source: "x"})
	if _, err := ix.db.Exec(`UPDATE memories SET source_concept='facts/x.md' WHERE id=?`, linked.ID); err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(100 * 24 * time.Hour)
	ix.Touch([]string{used.ID})
	ix.Write(Memory{Kind: KindInsight, Content: "a fresh observation", Source: "x"})

	r, err := ix.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if r.Current != 5 {
		t.Errorf("want 5 current memories, got %d", r.Current)
	}
	// old and the task are stale; used was retrieved today, linked has a concept.
	if r.StaleRows != 2 {
		t.Errorf("want 2 stale memories (the old insight and the untouched task), got %d", r.StaleRows)
	}
	if r.StaleTasks != 1 {
		t.Errorf("want 1 stale task, got %d", r.StaleTasks)
	}
	if r.Checkpointed != 1 {
		t.Errorf("sweep must checkpoint the retrieval it saw, got %d rows", r.Checkpointed)
	}
	if hits, _ := ix.Search("old observation", "shared", 10); len(hits) == 0 {
		t.Error("sweep must not delete or hide anything")
	}

	stale, err := ix.StaleTasks("shared")
	if err != nil || len(stale) != 1 || stale[task.ID] < 99 {
		t.Errorf("want the migration task flagged at about 100 days, got %v %v", stale, err)
	}
	_ = old
}

func TestSweepIsWeeklyAndRemembersItsReport(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	if !ix.SweepDue() || ix.LastSweep() != nil {
		t.Fatal("an index that never swept is due, with no report")
	}
	ix.Write(Memory{Kind: KindInsight, Content: "something", Source: "x"})
	if _, err := ix.Sweep(); err != nil {
		t.Fatal(err)
	}
	if ix.SweepDue() {
		t.Error("a sweep just ran, so another is not due")
	}
	if r := ix.LastSweep(); r == nil || r.Current != 1 {
		t.Errorf("the last report must be kept, got %+v", r)
	}
	clock = clock.Add(SweepEvery)
	if !ix.SweepDue() {
		t.Error("a week later a sweep is due again")
	}
}

// A task someone keeps looking at is not stale, even if nobody rewrote it.
func TestRetrievingATaskKeepsItFresh(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	task, _ := ix.Write(Memory{Kind: KindTaskState, Key: "task.docs", Content: "writing the docs", Source: "x"})
	clock = clock.Add(20 * 24 * time.Hour)
	ix.Touch([]string{task.ID})
	if stale, _ := ix.StaleTasks("shared"); len(stale) != 0 {
		t.Errorf("a task retrieved today is not stale, got %v", stale)
	}
}
