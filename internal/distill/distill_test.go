package distill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/vaultsync"
)

func setup(t *testing.T) (*index.Index, *vault.Vault, func()) {
	t.Helper()
	dir := t.TempDir()
	v := vault.Open(filepath.Join(dir, "vault"))
	if err := v.Init(); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(dir, "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	clock := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	return ix, v, func() { clock = clock.Add(24 * time.Hour) }
}

func read(t *testing.T, v *vault.Vault, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v.Root(), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDistillWritesReadableConcepts(t *testing.T) {
	ix, v, nextDay := setup(t)
	ix.Write(index.Memory{Kind: index.KindProjectParam, Key: "deploy.target", Content: "deploys to fly.io", Scope: "g1", Source: "claude-code"})
	nextDay()
	ix.Write(index.Memory{Kind: index.KindProjectParam, Key: "deploy.target", Content: "deploys to railway", Scope: "g1", Source: "opencode"})
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "one.off", Content: "said once", Source: "x"})

	r, err := Run(ix, v, map[string]string{"g1": "memory-engine"})
	if err != nil {
		t.Fatal(err)
	}
	const path = "projects/memory-engine/deploy-target.md"
	if r.Created != 1 || len(r.Paths) != 1 || r.Paths[0] != path {
		t.Fatalf("want one new concept at %s, got %+v", path, r)
	}
	body := read(t, v, path)
	for _, want := range []string{
		"type: project", "title: Deploy target (memory-engine)", "status: draft", "scope: g1", "key: deploy.target",
		"author: opencode agent", "created: \"2026-03-01\"", "updated: \"2026-03-02\"",
		"deploys to railway\n", "## History", "2026-03-02, opencode: deploys to railway (current)", "2026-03-01, claude-code: deploys to fly.io",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("concept file missing %q:\n%s", want, body)
		}
	}
	c, err := v.Read(path)
	if err != nil || c.Key != "deploy.target" || c.Status != vault.StatusDraft {
		t.Errorf("the vault must parse the concept back: %+v %v", c, err)
	}

	again, _ := Run(ix, v, map[string]string{"g1": "memory-engine"})
	if again.Created+again.Updated != 0 || again.Linked != 0 {
		t.Errorf("a second pass with nothing new must change nothing, got %+v", again)
	}
}

// Once a person edits a concept, membraid never overwrites it, even when the
// subject changes, but still links new memories to it.
func TestDistillNeverOverwritesAPersonsEdit(t *testing.T) {
	ix, v, nextDay := setup(t)
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses pnpm", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses pnpm, never npm", Source: "x"})
	Run(ix, v, nil)
	const path = "preferences/pkg-manager.md"

	edited := strings.Replace(read(t, v, path), "status: draft", "status: stable", 1) + "\nMy own note.\n"
	os.WriteFile(filepath.Join(v.Root(), path), []byte(edited), 0o600)

	nextDay()
	w, _ := ix.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses bun now", Source: "x"})
	r, err := Run(ix, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 1 || r.Updated != 0 {
		t.Errorf("an edited file must be kept, not rewritten: %+v", r)
	}
	if read(t, v, path) != edited {
		t.Error("the person's edit was overwritten")
	}
	subjects, _ := ix.Subjects()
	for _, row := range subjects[0].Rows {
		if row.ID == w.ID && row.Concept != path {
			t.Errorf("the new memory must still be linked to the kept file, got %q", row.Concept)
		}
	}
}

// An unedited draft follows its subject.
func TestDistillUpdatesItsOwnDraft(t *testing.T) {
	ix, v, nextDay := setup(t)
	ix.Write(index.Memory{Kind: index.KindInsight, Key: "ci.flaky", Content: "e2e flakes on cold caches", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindInsight, Key: "ci.flaky", Content: "e2e flakes on cold caches; warm the cache first", Source: "x"})
	Run(ix, v, nil)
	nextDay()
	ix.Write(index.Memory{Kind: index.KindInsight, Key: "ci.flaky", Content: "fixed: caches are warmed in CI now", Source: "x"})
	r, _ := Run(ix, v, nil)
	if r.Updated != 1 {
		t.Fatalf("an unedited draft must be updated, got %+v", r)
	}
	if !strings.Contains(read(t, v, "facts/ci-flaky.md"), "fixed: caches are warmed in CI now (current)") {
		t.Error("the updated draft must show the new current answer")
	}
}

// A person may move or rename a concept; it is found by its frontmatter.
func TestDistillFindsAMovedConcept(t *testing.T) {
	ix, v, _ := setup(t)
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "editor.theme", Content: "dark", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "editor.theme", Content: "dark, high contrast", Source: "x"})
	Run(ix, v, nil)
	from := filepath.Join(v.Root(), "preferences/editor-theme.md")
	to := filepath.Join(v.Root(), "decisions/how-my-editor-looks.md")
	os.MkdirAll(filepath.Dir(to), 0o700)
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	r, err := Run(ix, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Created != 0 {
		t.Errorf("a moved concept must be reused, not duplicated: %+v", r)
	}
	if _, err := os.Stat(from); err == nil {
		t.Error("the old path must not be recreated")
	}
}

// Sync settles conflicts on notes distillation wrote by recognising this
// footer; the two must not drift apart.
func TestNotesCarryTheFooterSyncRecognises(t *testing.T) {
	ix, v, _ := setup(t)
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses pnpm", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses pnpm, never npm", Source: "x"})
	if _, err := Run(ix, v, nil); err != nil {
		t.Fatal(err)
	}
	if body := read(t, v, "preferences/pkg-manager.md"); !strings.Contains(body, vaultsync.DraftMarker) || !strings.Contains(body, "status: draft") {
		t.Errorf("a distilled note must carry the footer and draft status sync looks for:\n%s", body)
	}
}

// Ownership travels in the note, so a machine that did not write a note, or an
// index rebuilt from the log, still updates it.
func TestNotesStayMembraidsOnEveryMachine(t *testing.T) {
	ix, v, nextDay := setup(t)
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses npm", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses pnpm", Source: "x"})
	if r, _ := Run(ix, v, nil); r.Created != 1 {
		t.Fatalf("want a note, got %+v", r)
	}
	if !strings.Contains(read(t, v, "preferences/pkg-manager.md"), "\nmembraid: ") {
		t.Error("a note must carry its ownership line")
	}

	other, err := index.Open(filepath.Join(t.TempDir(), "other.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	nextDay()
	other.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses npm", Source: "x"})
	other.Write(index.Memory{Kind: index.KindPreference, Key: "pkg.manager", Content: "uses bun", Source: "y"})
	r, err := Run(other, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Updated != 1 || r.Kept != 0 {
		t.Errorf("another index must recognise membraid's note and update it: %+v", r)
	}
	if !strings.Contains(read(t, v, "preferences/pkg-manager.md"), "uses bun (current)") {
		t.Error("the note must show the other machine's current answer")
	}
}

// A note a person deleted stays deleted, even as its subject changes.
func TestDeletedNoteIsNotWrittenAgain(t *testing.T) {
	ix, v, nextDay := setup(t)
	ix.Write(index.Memory{Kind: index.KindInsight, Key: "ci.flaky", Content: "e2e flakes", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindInsight, Key: "ci.flaky", Content: "e2e flakes on cold caches", Source: "x"})
	Run(ix, v, nil)
	os.Remove(filepath.Join(v.Root(), "facts/ci-flaky.md"))
	nextDay()
	ix.Write(index.Memory{Kind: index.KindInsight, Key: "ci.flaky", Content: "fixed by warming caches", Source: "x"})
	r, err := Run(ix, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.LeftDeleted != 1 || r.Created != 0 {
		t.Errorf("a deleted note must not be written again: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(v.Root(), "facts/ci-flaky.md")); err == nil {
		t.Error("the deleted note came back")
	}
}

// When every memory on a subject is forgotten, its note stops claiming the
// last answer is current.
func TestForgottenSubjectsNoteIsMarkedNoLongerCurrent(t *testing.T) {
	ix, v, nextDay := setup(t)
	ix.Write(index.Memory{Kind: index.KindProjectParam, Key: "deploy.target", Content: "deploys to fly.io", Scope: "g1", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindProjectParam, Key: "deploy.target", Content: "deploys to railway", Scope: "g1", Source: "x"})
	names := map[string]string{"g1": "api"}
	Run(ix, v, names)
	nextDay()
	if _, err := ix.Forget("g1", "deploy.target", ""); err != nil {
		t.Fatal(err)
	}
	r, err := Run(ix, v, names)
	if err != nil {
		t.Fatal(err)
	}
	body := read(t, v, "projects/api/deploy-target.md")
	if r.Retired != 1 || !strings.Contains(body, "status: deprecated") || !strings.Contains(body, "No longer current") || strings.Contains(body, "(current)") {
		t.Errorf("the note must be marked no longer current: %+v\n%s", r, body)
	}
	if again, _ := Run(ix, v, names); again.Retired != 0 || again.Updated != 0 {
		t.Errorf("a second pass must change nothing: %+v", again)
	}
}

// A note whose frontmatter will not parse is skipped, and every other subject
// still distills.
func TestUnreadableNoteDoesNotStopDistillation(t *testing.T) {
	ix, v, _ := setup(t)
	os.WriteFile(filepath.Join(v.Root(), "facts", "hand-written.md"), []byte("---\ntype: fact\ntags: memory\n---\n\nMy own note.\n"), 0o600)
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "editor.theme", Content: "dark", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindPreference, Key: "editor.theme", Content: "dark, high contrast", Source: "x"})
	r, err := Run(ix, v, nil)
	if err != nil {
		t.Fatalf("an unreadable note must not stop distillation: %v", err)
	}
	if r.Created != 1 || len(r.Unreadable) != 1 || r.Unreadable[0] != "facts/hand-written.md" {
		t.Errorf("want the other subject written and the bad note named: %+v", r)
	}
	if got := read(t, v, "facts/hand-written.md"); !strings.Contains(got, "My own note.") {
		t.Error("the unreadable note must be left as it was")
	}
}

// Two projects whose folders share a name get a note each, and later passes do
// not flip one file between them.
func TestSameNamedProjectsGetTheirOwnNotes(t *testing.T) {
	ix, v, _ := setup(t)
	names := map[string]string{"g1": "api", "g2": "api"}
	for _, sc := range []string{"g1", "g2"} {
		ix.Write(index.Memory{Kind: index.KindProjectParam, Key: "deploy.target", Content: "first " + sc, Scope: sc, Source: "x"})
		ix.Write(index.Memory{Kind: index.KindProjectParam, Key: "deploy.target", Content: "deploys " + sc, Scope: sc, Source: "x"})
	}
	r, err := Run(ix, v, names)
	if err != nil {
		t.Fatal(err)
	}
	if r.Created != 2 || len(r.Paths) != 2 || r.Paths[0] == r.Paths[1] {
		t.Fatalf("each project must get its own note: %+v", r)
	}
	for _, p := range r.Paths {
		body := read(t, v, p)
		if strings.Contains(p, "g2") != strings.Contains(body, "deploys g2") {
			t.Errorf("%s holds the wrong project's answer:\n%s", p, body)
		}
	}
	if again, _ := Run(ix, v, names); again.Created+again.Updated != 0 {
		t.Errorf("a second pass must not churn the notes: %+v", again)
	}
}
