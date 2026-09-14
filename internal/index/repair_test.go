package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dropLines removes every log line containing any of ids, as a write into a
// replaced log file did: the index has them, the log never did.
func dropLines(t *testing.T, dir string, ids ...string) {
	t.Helper()
	for _, f := range hostFiles(t, dir) {
		raw, _ := os.ReadFile(f)
		var kept []string
		for _, line := range strings.SplitAfter(string(raw), "\n") {
			drop := false
			for _, id := range ids {
				if strings.Contains(line, `"id":"`+id+`"`) {
					drop = true
				}
			}
			if !drop {
				kept = append(kept, line)
			}
		}
		os.WriteFile(f, []byte(strings.Join(kept, "")), 0o600)
	}
}

// A keyed correction, an unkeyed memory and a forget that only this index
// holds go back into the log, and a rebuild from it then matches this index.
func TestRepairRestoresLinesTheLogLost(t *testing.T) {
	dir := t.TempDir()
	ix := newIndexAt(t, dir, "omarchy")
	clock := time.Date(2026, 9, 14, 0, 16, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	ix.Write(Memory{Kind: KindProjectParam, Key: "membraid.ranking.design", Content: "first design", Scope: ScopeShared, Source: "claude-code"})
	kept, _ := ix.Write(Memory{Kind: KindInsight, Content: "a fact that was logged", Scope: ScopeShared, Source: "claude-code"})
	clock = clock.Add(time.Hour)
	lostKeyed, _ := ix.Write(Memory{Kind: KindProjectParam, Key: "membraid.ranking.design", Content: "revised design", Scope: ScopeShared, Source: "claude-code"})
	lostPlain, _ := ix.Write(Memory{Kind: KindInsight, Content: "crush, pi and codex are set up", Scope: ScopeShared, Source: "claude-code"})
	clock = clock.Add(time.Hour)
	ix.Forget("", "", kept.ID)
	dropLines(t, dir, lostKeyed.ID, lostPlain.ID, kept.ID+`","reason`)
	// dropLines matched the close by its id; put the write back if it went too.
	if fresh := rebuiltSnapshot(t, dir); !strings.Contains(fresh, kept.ID) {
		t.Fatal("test setup: the logged write must still be in the log")
	}

	report, err := ix.Repair(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Writes) != 2 || len(report.Closes) != 1 || report.Applied {
		t.Fatalf("a dry run finds two writes and one close and changes nothing: %+v", report)
	}
	if report, err = ix.Repair(false); err != nil || !report.Applied {
		t.Fatalf("repair: %+v %v", report, err)
	}
	if got, want := rebuiltSnapshot(t, dir), snapshot(t, ix); got != want {
		t.Errorf("after repair a rebuild must match this index\nrebuilt:\n%s\nlive:\n%s", got, want)
	}
	if again, _ := ix.Repair(true); len(again.Writes) != 0 || len(again.Closes) != 0 {
		t.Errorf("a second repair finds nothing: %+v", again)
	}
}

// Upgrading replays the log, which empties the index first. A memory whose log
// line was lost must survive that, and end up in the log.
func TestReplayKeepsWhatOnlyTheIndexHeld(t *testing.T) {
	dir := t.TempDir()
	ix := newIndexAt(t, dir, "omarchy")
	ix.Write(Memory{Kind: KindProjectParam, Key: "membraid.ranking.design", Content: "first design", Scope: ScopeShared, Source: "claude-code"})
	lost, _ := ix.Write(Memory{Kind: KindProjectParam, Key: "membraid.ranking.design", Content: "revised design", Scope: ScopeShared, Source: "claude-code"})
	dropLines(t, dir, lost.ID)
	before := snapshot(t, ix)
	path := filepath.Join(dir, "index-omarchy.db")
	ix.metaSet(metaReplay, "1")

	if _, err := ix.ImportAll(); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, ix); got != before {
		t.Errorf("a replay must not drop a memory only the index held\nafter:\n%s\nbefore:\n%s", got, before)
	}
	if got := rebuiltSnapshot(t, dir); got != before {
		t.Errorf("the memory must be back in the log\nrebuilt:\n%s\nlive:\n%s", got, before)
	}
	if cur, _ := ix.Current(ScopeShared, KindProjectParam, "membraid.ranking.design"); cur == nil || cur.Content != "revised design" {
		t.Errorf("the revised design must stay current: %+v (%s)", cur, path)
	}
}

func rebuiltSnapshot(t *testing.T, dir string) string {
	t.Helper()
	files := hostFiles(t, dir)
	var all []string
	for _, f := range files {
		all = append(all, f)
	}
	fresh, err := Open(filepath.Join(t.TempDir(), "fresh.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := fresh.ImportLog(all); err != nil {
		t.Fatal(err)
	}
	return snapshot(t, fresh)
}
