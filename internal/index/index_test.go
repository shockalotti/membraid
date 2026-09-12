package index

import (
	"path/filepath"
	"testing"

	"github.com/wynne/memory-engine/internal/wirelog"
)

func newIndex(t *testing.T) *Index {
	t.Helper()
	dir := t.TempDir()
	lg, err := wirelog.Open(filepath.Join(dir, ".hot"))
	if err != nil {
		t.Fatal(err)
	}
	ix, err := Open(filepath.Join(dir, "index.db"), lg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close(); lg.Close() })
	return ix
}

// The headline behaviour: a fact learned in one harness is current in all of
// them. Claude Code says dark, OpenCode later says light, and there is exactly
// one live answer afterwards - not two competing ones.
func TestSupersessionSpansHarnesses(t *testing.T) {
	ix := newIndex(t)
	if _, err := ix.Write(Memory{Kind: KindPreference, Key: "editor.theme",
		Content: "prefers dark theme", Scope: "shared", Source: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	res, err := ix.Write(Memory{Kind: KindPreference, Key: "editor.theme",
		Content: "prefers light theme", Scope: "shared", Source: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Superseded) != 1 {
		t.Fatalf("the older row must be closed, got %d closures", len(res.Superseded))
	}
	cur, err := ix.Current("shared", KindPreference, "editor.theme")
	if err != nil || cur == nil {
		t.Fatalf("no current row: %v", err)
	}
	if cur.Content != "prefers light theme" || cur.Source != "opencode" {
		t.Errorf("wrong live answer: %+v", cur)
	}
	hist, err := ix.History("shared", KindPreference, "editor.theme")
	if err != nil || len(hist) != 2 {
		t.Fatalf("history must keep the old row: %d %v", len(hist), err)
	}
}

// Different agents spell keys differently. If normalization fails the shared
// brain fragments into one subject per punctuation habit.
func TestKeyNormalizationUnifiesSpellings(t *testing.T) {
	for _, k := range []string{"Editor_Theme", "editor..theme", "editor . theme", "EDITOR/THEME"} {
		if got := NormalizeKey(k); got != "editor.theme" {
			t.Errorf("NormalizeKey(%q) = %q", k, got)
		}
	}
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindPreference, Key: "Editor_Theme", Content: "dark", Source: "a"})
	res, _ := ix.Write(Memory{Kind: KindPreference, Key: "editor..theme", Content: "light", Source: "b"})
	if len(res.Superseded) != 1 {
		t.Errorf("differently-spelled keys must be one subject, got %d closures", len(res.Superseded))
	}
}

// kind is part of identity: a preference and an insight under one key are two
// subjects and must not close each other.
func TestDifferentKindsAreDifferentSubjects(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindPreference, Key: "deploy.target", Content: "prefers railway", Source: "a"})
	res, _ := ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "is railway", Source: "a"})
	if len(res.Superseded) != 0 {
		t.Errorf("different kinds must not supersede, got %v", res.Superseded)
	}
}

// Unkeyed writes accumulate: without a key there is no subject to supersede.
func TestUnkeyedWritesDoNotSupersede(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindInsight, Content: "build fails on stale lockfile", Source: "a"})
	res, _ := ix.Write(Memory{Kind: KindInsight, Content: "tests flake under load", Source: "a"})
	if len(res.Superseded) != 0 {
		t.Errorf("unkeyed writes have no subject: %v", res.Superseded)
	}
}

// A project query sees its own scope plus shared, so cross-project knowledge
// surfaces without being asked for - and another project's rows do not.
func TestSearchScopeIsolation(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindProjectParam, Content: "deploy target is railway", Scope: "proj-a", Source: "x"})
	ix.Write(Memory{Kind: KindProjectParam, Content: "deploy target is fly", Scope: "proj-b", Source: "x"})
	ix.Write(Memory{Kind: KindPreference, Content: "deploy with zero downtime", Scope: "shared", Source: "x"})

	hits, err := ix.Search("deploy", "proj-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want own scope + shared, got %d: %v", len(hits), hits)
	}
	for _, h := range hits {
		if h.Scope == "proj-b" {
			t.Errorf("leaked another project's memory: %+v", h)
		}
	}
	if all, _ := ix.Search("deploy", "*", 10); len(all) != 3 {
		t.Errorf("* must see everything, got %d", len(all))
	}
}

func TestSupersededRowsLeaveSearch(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "deploys to heroku", Source: "a"})
	ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "deploys to railway", Source: "a"})
	hits, _ := ix.Search("deploys", "shared", 10)
	if len(hits) != 1 || hits[0].Content != "deploys to railway" {
		t.Errorf("search must show one live answer, got %v", hits)
	}
}

func TestRejectsBadInput(t *testing.T) {
	ix := newIndex(t)
	if _, err := ix.Write(Memory{Kind: "nonsense", Content: "x", Source: "a"}); err == nil {
		t.Error("unknown kind must be refused")
	}
	if _, err := ix.Write(Memory{Kind: KindInsight, Content: "  ", Source: "a"}); err == nil {
		t.Error("empty content must be refused")
	}
	if _, err := ix.Write(Memory{Kind: KindInsight, Content: "x", Scope: "*", Source: "a"}); err == nil {
		t.Error("* must not be writable as a scope")
	}
}

// Every write must reach the log before the index, so a rebuild never loses a
// row the index had.
func TestWriteReachesTheWireLog(t *testing.T) {
	dir := t.TempDir()
	lg, _ := wirelog.Open(filepath.Join(dir, ".hot"))
	ix, _ := Open(filepath.Join(dir, "index.db"), lg)
	defer func() { ix.Close(); lg.Close() }()

	ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "dark", Source: "claude-code"})
	ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "light", Source: "opencode"})

	files, _ := lg.Files()
	if len(files) != 1 {
		t.Fatalf("want one month file, got %v", files)
	}
	var c countingHandler
	if err := wirelog.Replay(files[0], &c); err != nil {
		t.Fatal(err)
	}
	if c.writes != 2 {
		t.Fatalf("want 2 logged writes, got %d", c.writes)
	}
	if len(c.last.Superseded) != 1 {
		t.Errorf("the log must record what the write closed: %+v", c.last.Superseded)
	}
}

type countingHandler struct {
	writes int
	last   wirelog.WriteLine
}

func (c *countingHandler) Write(l wirelog.WriteLine) error { c.writes++; c.last = l; return nil }
func (c *countingHandler) Distill(wirelog.DistillLine) error       { return nil }
func (c *countingHandler) Checkpoint(wirelog.CheckpointLine) error { return nil }
