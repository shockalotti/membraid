package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shockalotti/membraid/internal/wirelog"
)

func newIndexAt(t *testing.T, dir, host string) *Index {
	t.Helper()
	lg, err := wirelog.Open(filepath.Join(dir, ".hot"), host)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := Open(filepath.Join(dir, "index-"+host+".db"), lg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close(); lg.Close() })
	return ix
}

func newIndex(t *testing.T) *Index { return newIndexAt(t, t.TempDir(), "test") }

func TestSupersessionSpansHarnesses(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "prefers dark theme", Scope: "shared", Source: "claude-code"})
	res, err := ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "prefers light theme", Scope: "shared", Source: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Superseded) != 1 {
		t.Fatalf("the older row must be closed, got %d closures", len(res.Superseded))
	}
	cur, _ := ix.Current("shared", KindPreference, "editor.theme")
	if cur == nil || cur.Content != "prefers light theme" || cur.Source != "opencode" {
		t.Errorf("wrong live answer: %+v", cur)
	}
	if hist, _ := ix.History("shared", KindPreference, "editor.theme"); len(hist) != 2 {
		t.Fatalf("history must keep the old row: %d", len(hist))
	}
}

func TestKeyNormalizationUnifiesSpellings(t *testing.T) {
	for _, k := range []string{"Editor_Theme", "editor..theme", "editor . theme", "EDITOR/THEME"} {
		if got := NormalizeKey(k); got != "editor.theme" {
			t.Errorf("NormalizeKey(%q) = %q", k, got)
		}
	}
}

func TestDifferentKindsAreDifferentSubjects(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindPreference, Key: "deploy.target", Content: "prefers railway", Source: "a"})
	if res, _ := ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "is railway", Source: "a"}); len(res.Superseded) != 0 {
		t.Errorf("different kinds must not supersede, got %v", res.Superseded)
	}
}

func TestSearchScopeIsolation(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindProjectParam, Content: "deploy target is railway", Scope: "proj-a", Source: "x"})
	ix.Write(Memory{Kind: KindProjectParam, Content: "deploy target is fly", Scope: "proj-b", Source: "x"})
	ix.Write(Memory{Kind: KindPreference, Content: "deploy with zero downtime", Scope: "shared", Source: "x"})
	hits, err := ix.Search("deploy", "proj-a", 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("want own scope + shared, got %d: %v", len(hits), err)
	}
	if all, _ := ix.Search("deploy", "*", 10); len(all) != 3 {
		t.Errorf("* must see everything, got %d", len(all))
	}
}

func TestRejectsBadInput(t *testing.T) {
	ix := newIndex(t)
	if _, err := ix.Write(Memory{Kind: "nonsense", Content: "x", Source: "a"}); err == nil {
		t.Error("unknown kind must be refused")
	}
	if _, err := ix.Write(Memory{Kind: KindInsight, Content: "x", Scope: "*", Source: "a"}); err == nil {
		t.Error("* must not be writable as a scope")
	}
}

// A fresh index built from the log alone must hold what the original held -
// that is what cloning the vault onto another machine relies on.
func TestFreshIndexRebuildsFromLog(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	a.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "dark", Scope: "shared", Source: "claude-code"})
	a.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "light", Scope: "shared", Source: "opencode"})
	a.Write(Memory{Kind: KindInsight, Content: "tests flake under load", Scope: "shared", Source: "grok"})

	fresh := newIndexAt(t, dir, "fresh")
	n, err := fresh.ImportAll()
	if err != nil || n != 3 {
		t.Fatalf("want 3 imported, got %d: %v", n, err)
	}
	cur, _ := fresh.Current("shared", KindPreference, "editor.theme")
	if cur == nil || cur.Content != "light" {
		t.Errorf("rebuilt index has the wrong live answer: %+v", cur)
	}
	if again, _ := fresh.ImportAll(); again != 0 {
		t.Errorf("import must be idempotent, second run imported %d", again)
	}
}

// Two machines both wrote deploy.target while offline. After sync each imports
// the other's line. They must converge on the same live answer - the newest -
// whichever line each one saw first.
func TestCrossMachineMergeConvergesInEitherOrder(t *testing.T) {
	logs := t.TempDir()
	older := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	k := "deploy.target"
	write := func(host string, at time.Time, content string) string {
		lg, _ := wirelog.Open(logs, host)
		defer lg.Close()
		if err := lg.Append(wirelog.WriteLine{Header: wirelog.NewHeader(wirelog.TypeWrite, at),
			ID: NewID(), Key: &k, Scope: "g1", Source: host, Kind: KindProjectParam,
			Content: content, Superseded: []wirelog.Superseded{}}); err != nil {
			t.Fatal(err)
		}
		files, _ := wirelog.Files(logs)
		for _, f := range files {
			if filepath.Base(f) == "writes-"+at.Format("2006-01")+"-"+host+".jsonl" {
				return f
			}
		}
		t.Fatal("file not found")
		return ""
	}
	fileA := write("windows", older, "deploys to railway")
	fileB := write("omarchy", newer, "deploys to fly.io")

	for name, order := range map[string][]string{"older first": {fileA, fileB}, "newer first": {fileB, fileA}} {
		ix, err := Open(filepath.Join(t.TempDir(), "i.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range order { // one file per import: the order lines really arrive in
			if _, err := ix.ImportLog([]string{f}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		cur, _ := ix.Current("g1", KindProjectParam, k)
		if cur == nil || cur.Content != "deploys to fly.io" {
			t.Errorf("%s: want the newest answer live, got %+v", name, cur)
		}
		if hist, _ := ix.History("g1", KindProjectParam, k); len(hist) != 2 {
			t.Errorf("%s: the older answer must be kept as history, got %d rows", name, len(hist))
		}
		ix.Close()
	}
}

// The stuck-task bug: an unkeyed task_state could never leave "where you left
// off". It can now be closed by id, and a keyed one by key.
func TestDoneClosesTasksByIDAndByKey(t *testing.T) {
	ix := newIndex(t)
	unkeyed, _ := ix.Write(Memory{Kind: KindTaskState, Content: "wiring the omarchy widget", Scope: "g1", Source: "claude-code"})
	ix.Write(Memory{Kind: KindTaskState, Key: "task.sync", Content: "building sync", Scope: "g1", Source: "claude-code"})

	if closed, err := ix.Done("g1", "", unkeyed.ID); err != nil || len(closed) != 1 {
		t.Fatalf("close by id: %v %v", closed, err)
	}
	if closed, err := ix.Done("g1", "Task_Sync", ""); err != nil || len(closed) != 1 {
		t.Fatalf("close by key, spelled differently: %v %v", closed, err)
	}
	if open, _ := ix.Recent("g1", []string{KindTaskState}, 10); len(open) != 0 {
		t.Errorf("no task should remain open, got %v", open)
	}
	if _, err := ix.Done("g1", "task.sync", ""); err != ErrNothingToClose {
		t.Errorf("closing twice must report nothing to close, got %v", err)
	}
}

func TestDoneRefusesNonTasks(t *testing.T) {
	ix := newIndex(t)
	pref, _ := ix.Write(Memory{Kind: KindPreference, Content: "prefers pnpm", Source: "a"})
	if _, err := ix.Done("shared", "", pref.ID); err == nil {
		t.Error("a preference is superseded, never marked done")
	}
}

// Done is logged, so a finished task stays finished on the other machine too.
func TestDoneSurvivesRebuild(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	w, _ := a.Write(Memory{Kind: KindTaskState, Content: "half-done thing", Scope: "g1", Source: "x"})
	a.Done("g1", "", w.ID)
	fresh := newIndexAt(t, dir, "fresh")
	fresh.ImportAll()
	if open, _ := fresh.Recent("g1", []string{KindTaskState}, 10); len(open) != 0 {
		t.Errorf("the task reopened on rebuild: %v", open)
	}
}

// Forget retires a memory of any kind that has nothing to replace it. It is a
// close, not a delete: the entry leaves search and the digest but stays in
// history.
func TestForgetRetiresAnyKindByIDAndKey(t *testing.T) {
	ix := newIndex(t)
	stray, _ := ix.Write(Memory{Kind: KindInsight, Content: "cross-machine smoke test entry", Source: "cli"})
	ix.Write(Memory{Kind: KindProjectParam, Key: "ci.runner", Content: "uses buildkite", Scope: "g1", Source: "x"})

	if got, err := ix.Forget("shared", "", stray.ID); err != nil || len(got) != 1 {
		t.Fatalf("forget by id: %v %v", got, err)
	}
	if got, err := ix.Forget("g1", "CI_Runner", ""); err != nil || len(got) != 1 {
		t.Fatalf("forget by key, spelled differently: %v %v", got, err)
	}
	if hits, _ := ix.Search("smoke buildkite", "*", 10); len(hits) != 0 {
		t.Errorf("forgotten memories must leave search: %v", hits)
	}
	if hist, _ := ix.History("g1", KindProjectParam, "ci.runner"); len(hist) != 1 {
		t.Errorf("forgetting must keep the entry in history, got %d rows", len(hist))
	}
	if _, err := ix.Forget("g1", "ci.runner", ""); err != ErrNothingToForget {
		t.Errorf("forgetting twice must report nothing to forget, got %v", err)
	}
	if _, err := ix.Forget("g1", "", ""); err == nil {
		t.Error("forget with neither key nor id must fail")
	}
}

// Forget is logged, so a forgotten memory stays forgotten on the other machine.
func TestForgetSurvivesRebuild(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	w, _ := a.Write(Memory{Kind: KindPreference, Content: "prefers a tool since removed", Source: "x"})
	a.Forget("shared", "", w.ID)
	fresh := newIndexAt(t, dir, "fresh")
	fresh.ImportAll()
	if hits, _ := fresh.Search("removed", "*", 10); len(hits) != 0 {
		t.Errorf("the memory came back on rebuild: %v", hits)
	}
}

// A crash, or a full disk, can leave half a line at the end of a log file.
// The next write, from a new process, must not glue itself onto that fragment.
// If it did, the file would hold a corrupt line in the middle, which replay
// refuses (SPEC §5.3), and nothing this machine wrote after the crash would
// ever import anywhere again: not on a rebuild, not on another machine.
func TestTornTailDoesNotPoisonLaterWrites(t *testing.T) {
	dir := t.TempDir()
	before := newIndexAt(t, dir, "a")
	if _, err := before.Write(Memory{Kind: KindInsight, Content: "written before the crash", Source: "x"}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".hot", "writes-*-a.jsonl"))
	if len(files) != 1 {
		t.Fatalf("want one log file, got %v", files)
	}
	f, err := os.OpenFile(files[0], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"v":1,"t":"write","ts":"2026-09-13T10:00:00Z","id":"torn`)
	f.Close()

	after := newIndexAt(t, dir, "a")
	if _, err := after.Write(Memory{Kind: KindInsight, Content: "written after the crash", Source: "x"}); err != nil {
		t.Fatal(err)
	}

	fresh := newIndexAt(t, dir, "fresh")
	if _, err := fresh.ImportAll(); err != nil {
		t.Fatalf("replay must survive a crash fragment: %v", err)
	}
	hits, _ := fresh.Search("crash", "*", 10)
	got := map[string]bool{}
	for _, h := range hits {
		got[h.Content] = true
	}
	for _, want := range []string{"written before the crash", "written after the crash"} {
		if !got[want] {
			t.Errorf("want %q back after a rebuild, got %v", want, got)
		}
	}
}

// Rescope is logged, so a moved project stays moved on every machine. Before
// this, rescope only touched the local index and a clone put the memories
// straight back in the old scope.
func TestRescopeSurvivesRebuild(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	a.Write(Memory{Kind: KindProjectParam, Key: "db.engine", Content: "postgres", Scope: "pold", Source: "x"})
	if n, err := a.Rescope("pold", "gnew"); err != nil || n != 1 {
		t.Fatalf("rescope: %d %v", n, err)
	}
	fresh := newIndexAt(t, dir, "fresh")
	fresh.ImportAll()
	if cur, _ := fresh.Current("gnew", KindProjectParam, "db.engine"); cur == nil {
		t.Error("rebuilt index lost the rescope")
	}
}

// Moving a live answer into a scope that already has one on the same subject
// must not leave two live answers: the older is closed.
func TestRescopeCollisionKeepsNewer(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	ix.Write(Memory{Kind: KindProjectParam, Key: "db.engine", Content: "mysql", Scope: "gnew", Source: "x"})
	clock = clock.Add(time.Hour)
	ix.Write(Memory{Kind: KindProjectParam, Key: "db.engine", Content: "postgres", Scope: "pold", Source: "x"})
	if _, err := ix.Rescope("pold", "gnew"); err != nil {
		t.Fatalf("rescope must not violate the unique index: %v", err)
	}
	if cur, _ := ix.Current("gnew", KindProjectParam, "db.engine"); cur == nil || cur.Content != "postgres" {
		t.Errorf("the newer answer must win: %+v", cur)
	}
}

func TestImportRereadsAReplacedFile(t *testing.T) {
	dir := t.TempDir()
	a := newIndexAt(t, dir, "a")
	a.Write(Memory{Kind: KindInsight, Content: "one", Scope: "shared", Source: "x"})
	fresh := newIndexAt(t, dir, "fresh")
	fresh.ImportAll()
	// Simulate a file replaced by a shorter copy (restored backup).
	files, _ := wirelog.Files(filepath.Join(dir, ".hot"))
	fresh.db.Exec(`UPDATE log_offsets SET pos = 999999`)
	if _, err := fresh.ImportAll(); err != nil {
		t.Fatalf("import after a replaced file: %v (%v)", err, files)
	}
	if s, _ := fresh.Stats(); s.Current != 1 {
		t.Errorf("want 1 row, got %d", s.Current)
	}
	_ = os.Remove
}

func TestIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		id := NewID()
		if seen[id] {
			t.Fatalf("duplicate id after %d", i)
		}
		seen[id] = true
	}
}

func TestScopeNamesResolveIdsForDisplay(t *testing.T) {
	ix := newIndex(t)
	ix.TouchScope("g0c59d778", "memory-engine", "/p/memory-engine")
	names, err := ix.ScopeNames()
	if err != nil {
		t.Fatal(err)
	}
	if names["g0c59d778"] != "memory-engine" || names["shared"] != "shared" {
		t.Errorf("unexpected names: %v", names)
	}
}
