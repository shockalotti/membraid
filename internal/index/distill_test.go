package index

import (
	"testing"
	"time"
)

// A subject qualifies when its answer has been written twice, or written in
// two sessions. One-off facts and tasks never do.
func TestSubjectsQualify(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	step := func() { clock = clock.Add(time.Hour) }

	ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "deploys to fly.io", Scope: "g1", Source: "claude-code"})
	step()
	ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "deploys to railway", Scope: "g1", Source: "opencode"})
	step()
	ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "prefers dark theme", Source: "x", SessionRef: "s-1"})
	step()
	ix.Write(Memory{Kind: KindInsight, Key: "ci.flaky", Content: "the e2e suite flakes on cold caches", Scope: "g1", Source: "x"})
	step()
	ix.Write(Memory{Kind: KindTaskState, Key: "task.migrate", Content: "step 1 done", Scope: "g1", Source: "x"})
	step()
	ix.Write(Memory{Kind: KindTaskState, Key: "task.migrate", Content: "step 2 done", Scope: "g1", Source: "x"})

	subjects, err := ix.Subjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 || subjects[0].Key != "deploy.target" {
		t.Fatalf("want only deploy.target to qualify, got %+v", subjects)
	}
	s := subjects[0]
	if len(s.Rows) != 2 || s.Current().Content != "deploys to railway" || !s.Rows[0].Current {
		t.Errorf("want both rows, newest (current) first: %+v", s.Rows)
	}

	// A second session restating a single fact also qualifies it.
	step()
	ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "prefers dark theme, high contrast", Source: "y", SessionRef: "s-2"})
	subjects, _ = ix.Subjects()
	if len(subjects) != 2 {
		t.Errorf("a subject written in two sessions must qualify, got %d subjects", len(subjects))
	}
}

// The link between rows and their concept file survives a rebuild, and linking
// again is a no-op rather than another log line.
func TestLinkConceptIsLoggedAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	w1, _ := a.Write(Memory{Kind: KindPreference, Key: "pkg.manager", Content: "uses pnpm", Source: "x"})
	w2, _ := a.Write(Memory{Kind: KindPreference, Key: "pkg.manager", Content: "uses pnpm, never npm", Source: "x"})
	ids := []string{w1.ID, w2.ID}

	if n, err := a.LinkConcept(ids, "preferences/pkg-manager.md"); err != nil || n != 2 {
		t.Fatalf("link: %d %v", n, err)
	}
	if n, _ := a.LinkConcept(ids, "preferences/pkg-manager.md"); n != 0 {
		t.Errorf("linking again must do nothing, linked %d", n)
	}

	fresh := newIndexAt(t, dir, "fresh")
	if _, err := fresh.ImportAll(); err != nil {
		t.Fatal(err)
	}
	subjects, _ := fresh.Subjects()
	if len(subjects) != 1 {
		t.Fatalf("want the subject after rebuild, got %+v", subjects)
	}
	for _, r := range subjects[0].Rows {
		if r.Concept != "preferences/pkg-manager.md" {
			t.Errorf("row %s lost its concept link on rebuild: %q", r.ID, r.Concept)
		}
	}
	r, _ := fresh.Sweep()
	if r.StaleRows != 0 {
		t.Errorf("linked rows are never stale for sweep, got %d", r.StaleRows)
	}
}

func TestConceptWrittenHash(t *testing.T) {
	ix := newIndex(t)
	if ix.WrittenConcept("facts/x.md") != "" {
		t.Error("nothing written yet")
	}
	ix.ConceptWritten("facts/x.md", "abc")
	if ix.WrittenConcept("facts/x.md") != "abc" {
		t.Error("the written hash must be remembered")
	}
}
