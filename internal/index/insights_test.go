package index

import (
	"path/filepath"
	"testing"
	"time"
)

func insightsIndex(t *testing.T) (*Index, *time.Time) {
	t.Helper()
	ix, err := Open(filepath.Join(t.TempDir(), "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	clock := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	return ix, &clock
}

func TestProjects(t *testing.T) {
	ix, clock := insightsIndex(t)
	dir := t.TempDir()
	ix.TouchScope("g1", "membraid", dir)
	ix.TouchScope("p2", "gone", filepath.Join(dir, "moved-away"))
	ix.TouchScope("p3", "tmp", dir)
	ix.Write(Memory{Kind: KindPreference, Content: "uses pnpm", Scope: ScopeShared, Source: "claude-code"})
	ix.Write(Memory{Kind: KindTaskState, Key: "task.a", Content: "half done", Scope: "g1", Source: "opencode"})
	*clock = clock.Add(time.Hour)
	ix.Write(Memory{Kind: KindInsight, Content: "flaky test", Scope: "g1", Source: "claude-code"})
	ix.Write(Memory{Kind: KindInsight, Content: "old note", Scope: "p2", Source: "grok"})

	ps, err := ix.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 4 || ps[0].Scope != ScopeShared {
		t.Fatalf("want shared first and four projects, got %+v", ps)
	}
	byScope := map[string]ProjectInfo{}
	for _, p := range ps {
		byScope[p.Scope] = p
	}
	g := byScope["g1"]
	if g.Name != "membraid" || g.Memories != 2 || g.OpenTasks != 1 || g.Missing || len(g.Sources) != 2 || g.Sources[0] != "claude-code" {
		t.Errorf("g1 wrong: %+v", g)
	}
	if !byScope["p2"].Missing {
		t.Error("a project whose folder is gone must say so")
	}
	if byScope["p3"].Memories != 0 {
		t.Errorf("an empty project has no memories: %+v", byScope["p3"])
	}

	n, err := ix.PruneScopes()
	if err != nil || n != 1 {
		t.Fatalf("want the one empty project pruned, got %d %v", n, err)
	}
	ps, _ = ix.Projects()
	if len(ps) != 3 {
		t.Errorf("want three projects after pruning, got %+v", ps)
	}
}

func TestInsights(t *testing.T) {
	ix, clock := insightsIndex(t)
	*clock = clock.AddDate(0, 0, -10)
	ix.Write(Memory{Kind: KindInsight, Content: "long ago", Source: "grok"})
	*clock = clock.AddDate(0, 0, 9)
	w, _ := ix.Write(Memory{Kind: KindPreference, Key: "pkg", Content: "npm", Source: "claude-code"})
	*clock = clock.AddDate(0, 0, 1)
	ix.Write(Memory{Kind: KindPreference, Key: "pkg", Content: "pnpm", Source: "opencode"})
	ix.Write(Memory{Kind: KindInsight, Content: "used one", Source: "opencode"})
	hits, _ := ix.Search("used", "*", 5)
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	ix.Touch(ids)

	in, err := ix.Insights(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.WritesByDay) != 7 || in.WritesByDay[6].Count != 2 || in.WritesByDay[5].Count != 1 {
		t.Errorf("writes by day wrong: %+v", in.WritesByDay)
	}
	if in.WritesBySource["opencode"] != 2 || in.WritesBySource["claude-code"] != 1 || in.WritesBySource["grok"] != 0 {
		t.Errorf("writes by source must count the window, replaced statements included: %+v", in.WritesBySource)
	}
	if in.Current != 3 || in.NeverUsed != 2 || len(in.RecentlyUsed) != 1 || in.RecentlyUsed[0].Content != "used one" {
		t.Errorf("usage wrong: %+v", in)
	}
	_ = w
}

func TestBrowseFiltersAndDoesNotTouch(t *testing.T) {
	ix, _ := insightsIndex(t)
	ix.Write(Memory{Kind: KindInsight, Content: "a", Scope: "g1", Source: "grok"})
	ix.Write(Memory{Kind: KindPreference, Content: "b", Scope: ScopeShared, Source: "grok"})
	ix.Write(Memory{Kind: KindInsight, Content: "c", Scope: "g1", Source: "opencode"})

	all, _ := ix.Browse("*", "", "", 0)
	if len(all) != 3 {
		t.Errorf("want every memory, got %d", len(all))
	}
	only, _ := ix.Browse("g1", "insight", "grok", 0)
	if len(only) != 1 || only[0].Content != "a" {
		t.Errorf("filters wrong: %+v", only)
	}
	in, _ := ix.Insights(7)
	if in.NeverUsed != 3 {
		t.Error("browsing must not count as retrieval")
	}
	none, _ := ix.Browse("nope", "", "", 0)
	if none == nil {
		t.Error("an empty result is an empty list, not null, for JSON readers")
	}
}
