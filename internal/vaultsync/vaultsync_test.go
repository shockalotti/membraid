package vaultsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shockalotti/membraid/internal/vault"
)

// isolate git from the developer's global config, so the fallback identity
// path is exercised and nothing personal leaks into test repos.
func isolate(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// twoMachines builds a bare remote with one commit, and two clones of it.
func twoMachines(t *testing.T) (a, b string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	a, b = filepath.Join(root, "a"), filepath.Join(root, "b")
	exec.Command("git", "init", "-q", "--bare", "-b", "main", remote).Run()
	os.MkdirAll(seed, 0o755)
	sh(t, seed, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(seed, "index.md"), []byte("# memory\n"), 0o600)
	sh(t, seed, "add", "-A")
	sh(t, seed, "commit", "-q", "-m", "seed")
	sh(t, seed, "remote", "add", "origin", remote)
	sh(t, seed, "push", "-q", "-u", "origin", "main")
	for _, d := range []string{a, b} {
		if out, err := exec.Command("git", "clone", "-q", remote, d).CombinedOutput(); err != nil {
			t.Fatalf("clone: %v %s", err, out)
		}
	}
	return a, b
}

func run(t *testing.T, dir, host string) *Result {
	t.Helper()
	res, err := Run(context.Background(), dir, Options{Host: host})
	if err != nil {
		t.Fatalf("sync %s: %v", host, err)
	}
	return res
}

func TestWriteOnOneMachineArrivesOnTheOther(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	os.WriteFile(filepath.Join(a, "note.md"), []byte("from a\n"), 0o600)

	if r := run(t, a, "a"); !r.Committed || !r.Pushed {
		t.Fatalf("a should commit and push: %+v", r)
	}
	if r := run(t, b, "b"); !r.Pulled {
		t.Fatalf("b should pull: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(b, "note.md")); string(got) != "from a\n" {
		t.Errorf("b did not receive the note: %q", got)
	}
}

// The case per-host log files exist for: both machines write between syncs.
// Different files never conflict, whichever order the syncs happen in.
func TestBothMachinesWritingBetweenSyncsDoesNotConflict(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	os.MkdirAll(filepath.Join(a, ".hot"), 0o700)
	os.MkdirAll(filepath.Join(b, ".hot"), 0o700)
	os.WriteFile(filepath.Join(a, ".hot", "writes-2026-09-a.jsonl"), []byte("line from a\n"), 0o600)
	os.WriteFile(filepath.Join(b, ".hot", "writes-2026-09-b.jsonl"), []byte("line from b\n"), 0o600)

	run(t, a, "a")
	if r := run(t, b, "b"); !r.Pulled || !r.Pushed {
		t.Fatalf("b should rebase onto a and push: %+v", r)
	}
	run(t, a, "a")
	for _, dir := range []string{a, b} {
		for _, f := range []string{"writes-2026-09-a.jsonl", "writes-2026-09-b.jsonl"} {
			if _, err := os.Stat(filepath.Join(dir, ".hot", f)); err != nil {
				t.Errorf("%s missing %s", filepath.Base(dir), f)
			}
		}
	}
}

// When both machines really did change one file, sync stops, says which file,
// and leaves the repo clean with the local commit intact - never mid-rebase.
func TestRealConflictAbortsCleanly(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	os.WriteFile(filepath.Join(a, "index.md"), []byte("# edited on a\n"), 0o600)
	os.WriteFile(filepath.Join(b, "index.md"), []byte("# edited on b\n"), 0o600)
	run(t, a, "a")

	_, err := Run(context.Background(), b, Options{Host: "b"})
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0] != "index.md" {
		t.Fatalf("want a conflict naming index.md, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(b, ".git", "rebase-merge")); serr == nil {
		t.Error("repo left mid-rebase")
	}
	if got, _ := os.ReadFile(filepath.Join(b, "index.md")); string(got) != "# edited on b\n" {
		t.Errorf("local edit lost: %q", got)
	}
	if log := sh(t, b, "log", "--oneline", "-1"); !strings.Contains(log, "sync from b") {
		t.Errorf("local commit not intact: %s", log)
	}
}

func TestConcurrentSyncIsSkippedNotRaced(t *testing.T) {
	isolate(t)
	a, _ := twoMachines(t)
	unlock, ok, err := tryLock(filepath.Join(a, ".git", "membraid-sync.lock"))
	if err != nil || !ok {
		t.Fatalf("could not take lock: %v", err)
	}
	defer unlock()
	if r := run(t, a, "a"); r.Skipped == "" {
		t.Errorf("a second sync must skip while the lock is held: %+v", r)
	}
}

func TestNotARepoIsASkip(t *testing.T) {
	isolate(t)
	if r := run(t, t.TempDir(), "a"); r.Skipped == "" {
		t.Errorf("want skip, got %+v", r)
	}
}

func TestNoRemoteStillCommits(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "x.md"), []byte("x\n"), 0o600)
	r := run(t, dir, "solo")
	if !r.Committed || r.Remote || r.Pushed {
		t.Errorf("want a local commit only: %+v", r)
	}
}

func draftNote(body string) string {
	return "---\ntype: fact\ntitle: Deploy target\nstatus: draft\nkey: deploy.target\n---\n\n" + body +
		"\n\n" + DraftMarker + " Edit it freely: once you change this file, membraid will not overwrite it._\n"
}

func writeAndSync(t *testing.T, dir, host, file, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755)
	os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600)
	run(t, dir, host)
}

// Every machine adds log.md entries under the header, so two machines logging
// between syncs always collide. Sync keeps both machines' entries instead of
// stopping the second machine for good.
func TestLogConflictKeepsBothMachinesEntries(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	const header = "# Change log\n\nNewest first.\n"
	writeAndSync(t, a, "a", "log.md", header+"- 2026-09-13 init\n")
	run(t, b, "b")

	writeAndSync(t, a, "a", "log.md", header+"- 2026-09-14 sweep on a\n- 2026-09-13 init\n")
	os.WriteFile(filepath.Join(b, "log.md"), []byte(header+"- 2026-09-14 distill on b\n- 2026-09-13 init\n"), 0o600)
	if r := run(t, b, "b"); !r.Pulled || !r.Pushed {
		t.Fatalf("b must settle the log conflict and push: %+v", r)
	}
	run(t, a, "a")
	want := header + "- 2026-09-14 distill on b\n- 2026-09-14 sweep on a\n- 2026-09-13 init\n"
	for _, dir := range []string{a, b} {
		if got, _ := os.ReadFile(filepath.Join(dir, "log.md")); string(got) != want {
			t.Errorf("%s log.md:\n%s\nwant:\n%s", filepath.Base(dir), got, want)
		}
	}
}

// Two machines distilling the same subject before they have seen each other's
// memories write the note differently. Neither is a person's edit, so sync
// takes the version already pushed and lets the next distill pass rewrite it.
func TestUntouchedDraftConflictTakesTheRemoteVersion(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	const note = "facts/deploy-target.md"
	writeAndSync(t, a, "a", note, draftNote("deploys to railway"))
	run(t, b, "b")

	writeAndSync(t, a, "a", note, draftNote("deploys to fly.io"))
	os.WriteFile(filepath.Join(b, note), []byte(draftNote("deploys to render")), 0o600)
	if r := run(t, b, "b"); !r.Pulled {
		t.Fatalf("b must settle the draft conflict: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(b, note)); string(got) != draftNote("deploys to fly.io") {
		t.Errorf("want the remote's draft, got:\n%s", got)
	}
}

// A note a person changed is not membraid's to settle, and neither is a mix of
// settleable and unsettleable files: sync stops, names them, and leaves the
// repo clean with the local commit intact.
func TestConflictsMembraidCannotSettleStillStop(t *testing.T) {
	isolate(t)
	for _, c := range []struct {
		name  string
		setup func(a, b string)
		want  []string
	}{
		{"a person's edit", func(a, b string) {
			const note = "facts/deploy-target.md"
			writeAndSync(t, a, "a", note, draftNote("deploys to railway"))
			run(t, b, "b")
			writeAndSync(t, a, "a", note, draftNote("deploys to fly.io"))
			os.WriteFile(filepath.Join(b, note), []byte("---\ntype: fact\nstatus: stable\n---\n\nWe deploy to render. My note.\n"), 0o600)
		}, []string{"facts/deploy-target.md"}},
		{"log.md with another file", func(a, b string) {
			const header = "# Change log\n\nNewest first.\n"
			writeAndSync(t, a, "a", "log.md", header)
			run(t, b, "b")
			os.WriteFile(filepath.Join(a, "log.md"), []byte(header+"- on a\n"), 0o600)
			writeAndSync(t, a, "a", "index.md", "# edited on a\n")
			os.WriteFile(filepath.Join(b, "log.md"), []byte(header+"- on b\n"), 0o600)
			os.WriteFile(filepath.Join(b, "index.md"), []byte("# edited on b\n"), 0o600)
		}, []string{"index.md"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := twoMachines(t)
			c.setup(a, b)
			_, err := Run(context.Background(), b, Options{Host: "b"})
			var ce *ConflictError
			if !errors.As(err, &ce) {
				t.Fatalf("want a conflict, got %v", err)
			}
			for _, f := range c.want {
				if !strings.Contains(strings.Join(ce.Files, " "), f) {
					t.Errorf("conflict must name %s, got %v", f, ce.Files)
				}
			}
			for _, d := range []string{"rebase-merge", "rebase-apply"} {
				if _, serr := os.Stat(filepath.Join(b, ".git", d)); serr == nil {
					t.Error("the repo must not be left mid-rebase")
				}
			}
		})
	}
}

// A note membraid marked no longer current is not a draft, but it is still
// membraid's while its ownership line matches, so a conflict on it settles too.
func TestStampedNoteConflictSettles(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	const note = "facts/deploy-target.md"
	stamped := func(status, body string) string {
		return string(vault.Stamp([]byte("---\ntype: fact\ntitle: Deploy target\nstatus: " + status + "\nkey: deploy.target\n---\n" + body + "\n")))
	}
	writeAndSync(t, a, "a", note, stamped("draft", "deploys to railway"))
	run(t, b, "b")
	writeAndSync(t, a, "a", note, stamped("deprecated", "No longer current"))
	os.WriteFile(filepath.Join(b, note), []byte(stamped("draft", "deploys to fly.io")), 0o600)
	if r := run(t, b, "b"); !r.Pulled {
		t.Fatalf("b must settle a conflict between two untouched notes: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(b, note)); string(got) != stamped("deprecated", "No longer current") {
		t.Errorf("want the remote's note, got:\n%s", got)
	}
}
