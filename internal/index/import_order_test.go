package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// snapshot is what must agree between machines: every memory's scope, whether
// it is current, and the note it is linked to.
func snapshot(t *testing.T, ix *Index) string {
	t.Helper()
	rows, err := ix.db.Query(`SELECT id, scope, valid_to IS NULL, COALESCE(source_concept, '') FROM memories ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, scope, concept string
		var live bool
		if err := rows.Scan(&id, &scope, &live, &concept); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %s live=%v %s\n", id, scope, live, concept)
	}
	return b.String()
}

func hostFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := wirelog.Files(filepath.Join(dir, ".hot"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[wirelog.HostOfFile(f)] = f
	}
	return out
}

func permutations(s []string) [][]string {
	if len(s) <= 1 {
		return [][]string{s}
	}
	var out [][]string
	for i := range s {
		rest := append(append([]string{}, s[:i]...), s[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]string{s[i]}, p...))
		}
	}
	return out
}

// Three machines' logs, whose lines interleave in time: writes to a project,
// a rescope of it and a rescope back, a close and a note link. Whatever order
// the files arrive in, one at a time as syncs really deliver them, every
// machine must end with the index a rebuild from the whole log gives.
func TestImportOrderDoesNotChangeTheResult(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	at := func(h int) func() time.Time {
		return func() time.Time { return base.Add(time.Duration(h) * time.Hour) }
	}

	a := newIndexAt(t, dir, "a")
	a.SetClock(at(1))
	a.Write(Memory{Kind: KindProjectParam, Key: "db.engine", Content: "mysql", Scope: "g1", Source: "x"})
	a.SetClock(at(3))
	unkeyed, _ := a.Write(Memory{Kind: KindInsight, Content: "the build needs make", Scope: "g1", Source: "x"})
	a.SetClock(at(5))
	a.Write(Memory{Kind: KindProjectParam, Key: "db.engine", Content: "postgres", Scope: "g1", Source: "x"})
	a.SetClock(at(9))
	late, _ := a.Write(Memory{Kind: KindInsight, Content: "written under the old id after the move back", Scope: "g1", Source: "x"})

	b := newIndexAt(t, dir, "b")
	b.SetClock(at(2))
	b.Write(Memory{Kind: KindProjectParam, Key: "db.engine", Content: "sqlite", Scope: "g2", Source: "y"})
	b.SetClock(at(4))
	if _, err := b.Rescope("g1", "g2"); err != nil {
		t.Fatal(err)
	}

	c := newIndexAt(t, dir, "c")
	c.SetClock(at(6))
	if _, err := c.closeIDs([]string{unkeyed.ID}, "forgotten"); err != nil {
		t.Fatal(err)
	}
	c.SetClock(at(7))
	if _, err := c.Rescope("g2", "g3"); err != nil {
		t.Fatal(err)
	}
	c.SetClock(at(8))
	line := wirelog.DistillLine{Header: wirelog.NewHeader(wirelog.TypeDistill, at(8)()), Rows: []string{late.ID}, Concept: "facts/late.md"}
	if err := c.log.Append(line); err != nil {
		t.Fatal(err)
	}

	files := hostFiles(t, dir)
	all := []string{files["a"], files["b"], files["c"]}
	rebuilt, err := Open(filepath.Join(t.TempDir(), "rebuilt.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	if _, err := rebuilt.ImportLog(all); err != nil {
		t.Fatal(err)
	}
	want := snapshot(t, rebuilt)
	if cur, _ := rebuilt.Current("g3", KindProjectParam, "db.engine"); cur == nil || cur.Content != "postgres" {
		t.Errorf("the newest answer must be live where the project ended up: %+v", cur)
	}
	if !strings.Contains(want, late.ID+" g3 live=true facts/late.md") {
		t.Errorf("a write under an old id follows the project, and its note link applies:\n%s", want)
	}

	for _, order := range permutations([]string{"a", "b", "c"}) {
		ix, err := Open(filepath.Join(t.TempDir(), "i.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range order {
			if _, err := ix.ImportLog([]string{files[h]}); err != nil {
				t.Fatalf("%v: %v", order, err)
			}
		}
		// Every file again: a second machine's next sync sees them all.
		if _, err := ix.ImportLog(all); err != nil {
			t.Fatal(err)
		}
		if got := snapshot(t, ix); got != want {
			t.Errorf("files arriving %v gave a different index\ngot:\n%s\nwant:\n%s", order, got, want)
		}
		ix.Close()
	}
}

// A checkout that still derives a project's old id writes where the project
// went, and a rescope back into the old id brings it into use again.
func TestWritesUnderAnOldIDFollowTheProject(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	ix.Write(Memory{Kind: KindInsight, Content: "before the move", Scope: "pold", Source: "x"})
	clock = clock.Add(time.Hour)
	ix.Rescope("pold", "gnew")
	clock = clock.Add(time.Hour)
	res, err := ix.Write(Memory{Kind: KindInsight, Content: "after the move", Scope: "pold", Source: "x"})
	if err != nil || res.Scope != "gnew" {
		t.Fatalf("a write under the old id must land in the new one: %+v %v", res, err)
	}
	if got := ix.CanonicalScope("pold"); got != "gnew" {
		t.Errorf("pold must resolve to gnew, got %s", got)
	}
	clock = clock.Add(time.Hour)
	ix.Rescope("gnew", "pold")
	if got := ix.CanonicalScope("pold"); got != "pold" {
		t.Errorf("moving back must bring pold into use again, got %s", got)
	}
	if got := ix.CanonicalScope("gnew"); got != "pold" {
		t.Errorf("gnew must now resolve to pold, got %s", got)
	}
}

// A close whose memory had not arrived yet used to do nothing, forever.
func TestCloseArrivingBeforeItsWriteStillApplies(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	w, _ := a.Write(Memory{Kind: KindTaskState, Key: "task.fix", Content: "fix the flaky test", Scope: "g1", Source: "x"})
	b := newIndexAt(t, dir, "b")
	b.ImportAll()
	b.SetClock(func() time.Time { return time.Now().Add(time.Minute) })
	if _, err := b.Done("g1", "task.fix", ""); err != nil {
		t.Fatal(err)
	}
	files := hostFiles(t, dir)
	ix, err := Open(filepath.Join(t.TempDir(), "i.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	ix.ImportLog([]string{files["b"]})
	ix.ImportLog([]string{files["a"]})
	if cur, _ := ix.Current("g1", KindTaskState, "task.fix"); cur != nil {
		t.Errorf("the task was marked done on b, so it must not be open: %+v (id %s)", cur, w.ID)
	}
}

// A hand edit that shortens an earlier line moves every later line. Import
// used to resume at the old byte position, in the middle of a line, and lose
// the record there.
func TestEditedLogIsReadAgain(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	a.Write(Memory{Kind: KindInsight, Content: "first memory with a long sentence to trim", Scope: "shared", Source: "x"})
	a.Write(Memory{Kind: KindInsight, Content: "second memory", Scope: "shared", Source: "x"})
	reader := newIndexAt(t, dir, "reader")
	reader.ImportAll()

	third, _ := a.Write(Memory{Kind: KindInsight, Content: "third memory, written after the reader stopped", Scope: "shared", Source: "x"})
	f := hostFiles(t, dir)["a"]
	raw, _ := os.ReadFile(f)
	edited := strings.Replace(string(raw), " with a long sentence to trim", "", 1)
	if err := os.WriteFile(f, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ImportAll(); err != nil {
		t.Fatal(err)
	}
	var n int
	reader.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE id=?`, third.ID).Scan(&n)
	if n != 1 {
		t.Error("the record after an edited line must still be imported")
	}
}

// A rebuilt index knows what each project is called and how often each agent
// used memory today, not just the count at the day's first checkpoint.
func TestRebuildKeepsProjectNamesAndUseCounts(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	w, _ := a.Write(Memory{Kind: KindInsight, Content: "a fact", Scope: "g1", Source: "x"})
	a.TouchScope("g1", "membraid", "/src/membraid")
	a.MarkUsed([]string{w.ID}, "claude-code", 1)
	if _, err := a.Checkpoint(0); err != nil {
		t.Fatal(err)
	}
	a.MarkUsed([]string{w.ID}, "claude-code", 1)
	if _, err := a.Checkpoint(0); err != nil {
		t.Fatal(err)
	}

	lg, err := wirelog.Open(filepath.Join(dir, ".hot"), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	rebuilt, err := Open(filepath.Join(t.TempDir(), "rebuilt.db"), lg)
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	if _, err := rebuilt.ImportAll(); err != nil {
		t.Fatal(err)
	}
	names, _ := rebuilt.ScopeNames()
	if names["g1"] != "membraid" {
		t.Errorf("the rebuilt registry must name g1, got %q", names["g1"])
	}
	scopes, _ := rebuilt.Scopes()
	if len(scopes) != 1 || scopes[0].Path != "/src/membraid" {
		t.Errorf("this machine's own checkout path must come back: %+v", scopes)
	}
	if uses, _ := rebuilt.UsesBySource(1); uses["claude-code"] != 2 {
		t.Errorf("want 2 uses after a rebuild, got %v", uses)
	}

	other := newIndexAt(t, dir, "other")
	other.ImportAll()
	if sc, _ := other.Scopes(); len(sc) != 1 || sc[0].Path != "" {
		t.Errorf("another machine learns the name but not the path: %+v", sc)
	}
}

// An index from before rescopes were recorded replays the log once on
// upgrade, so a project moved back then still takes writes under its old id.
func TestUpgradeReplaysToRecordRescopes(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	a.Write(Memory{Kind: KindInsight, Content: "before the move", Scope: "pold", Source: "x"})
	a.Rescope("pold", "gnew")
	path := filepath.Join(dir, "index-a.db")
	a.db.Exec(`DELETE FROM rescopes`)
	a.db.Exec(`DELETE FROM scope_aliases`)
	a.db.Exec(`UPDATE memories SET last_retrieved='2026-09-01T00:00:00Z'`)
	a.Close()
	setUserVersion(t, path, 4)

	lg, _ := wirelog.Open(filepath.Join(dir, ".hot"), "a")
	defer lg.Close()
	ix, err := Open(path, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if _, err := ix.ImportAll(); err != nil {
		t.Fatal(err)
	}
	if got := ix.CanonicalScope("pold"); got != "gnew" {
		t.Errorf("the upgrade must record the old rescope, pold resolves to %s", got)
	}
	var retrieved string
	ix.db.QueryRow(`SELECT COALESCE(last_retrieved, '') FROM memories`).Scan(&retrieved)
	if retrieved == "" {
		t.Error("replay must keep retrieval times the log does not hold")
	}
	if ix.metaGet(metaReplay) != "" {
		t.Error("the replay must run once")
	}
}
