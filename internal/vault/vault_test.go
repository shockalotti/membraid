package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpVault(t *testing.T) *Vault {
	t.Helper()
	v := Open(filepath.Join(t.TempDir(), "vault"))
	if err := v.Init(); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestInitCreatesSkeletonAndRefusesTwice(t *testing.T) {
	v := tmpVault(t)
	for _, d := range append(Folders, HotDir) {
		if fi, err := os.Stat(filepath.Join(v.Root(), d)); err != nil || !fi.IsDir() {
			t.Errorf("missing folder %s", d)
		}
	}
	for _, f := range []string{"index.md", "log.md"} {
		if _, err := os.Stat(filepath.Join(v.Root(), f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	// Re-initialising over live memory is never what anyone meant.
	if err := v.Init(); err == nil {
		t.Error("second Init must refuse")
	}
}

// A hand-written note with no frontmatter is still a concept. Refusing it
// would punish exactly the hand-editing this store exists to allow.
func TestParseBareMarkdownIsAccepted(t *testing.T) {
	c, err := Parse("rules/never-force-push.md", []byte("Never force push to main.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "never force push" {
		t.Errorf("title from filename: %q", c.Title)
	}
	if c.Type != "rule" {
		t.Errorf("type from folder: %q", c.Type)
	}
	if c.Status != StatusStable {
		t.Errorf("a file a human put here is kept: %q", c.Status)
	}
	if c.Scope != ScopeShared {
		t.Errorf("default scope: %q", c.Scope)
	}
	if c.Created == "" {
		t.Error("created must be filled")
	}
	if c.Body != "Never force push to main." {
		t.Errorf("body: %q", c.Body)
	}
}

// Frontmatter wins over the folder: a file in decisions/ that says rule is a
// rule (SPEC §4.1, folders are advisory).
func TestFrontmatterBeatsFolder(t *testing.T) {
	raw := []byte("---\ntype: rule\ntitle: Keep history\nstatus: draft\nscope: mem-a3f7\nkey: memory.history\n---\nBody text.\n")
	c, err := Parse("decisions/keep-history.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != "rule" || c.Status != StatusDraft || c.Key != "memory.history" || c.Scope != "mem-a3f7" {
		t.Errorf("frontmatter not honoured: %+v", c)
	}
}

func TestRoundTripPreservesEverything(t *testing.T) {
	v := tmpVault(t)
	in := &Concept{
		Type: "preference", Title: "Prefers pnpm", Summary: "pnpm over npm",
		Status: StatusDraft, Scope: ScopeShared, Key: "pkg.manager",
		Author: "claude-code agent", Created: "2026-09-12",
		Tags: []string{"tooling"}, Body: "Uses `pnpm exec`, never `dlx`.",
		Path: "preferences/prefers-pnpm.md",
	}
	if err := v.Write(in); err != nil {
		t.Fatal(err)
	}
	out, err := v.Read(in.Path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.Key != in.Key || out.Status != in.Status ||
		out.Author != in.Author || out.Body != in.Body || out.Scope != in.Scope {
		t.Errorf("round trip lost data:\n in=%+v\nout=%+v", in, out)
	}
	if len(out.Tags) != 1 || out.Tags[0] != "tooling" {
		t.Errorf("tags lost: %v", out.Tags)
	}
}

// The file on disk must be something a person would be willing to edit.
func TestWrittenFileIsReadable(t *testing.T) {
	v := tmpVault(t)
	c := &Concept{Type: "rule", Title: "Never overwrite history", Created: "2026-09-01",
		Key: "memory.history", Body: "Facts supersede.", Path: "rules/never-overwrite-history.md"}
	if err := v.Write(c); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), c.Path))
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") || strings.Count(s, "---\n") < 2 {
		t.Errorf("not fenced frontmatter:\n%s", s)
	}
	if !strings.Contains(s, "key: memory.history") || !strings.HasSuffix(s, "Facts supersede.\n") {
		t.Errorf("unreadable output:\n%s", s)
	}
}

func TestListSkipsReservedAndHotDir(t *testing.T) {
	v := tmpVault(t)
	if err := v.Write(&Concept{Type: "fact", Title: "A", Created: "2026-09-12", Path: "facts/a.md"}); err != nil {
		t.Fatal(err)
	}
	// Wire log must never surface as a concept.
	if err := os.WriteFile(filepath.Join(v.HotPath(), "writes-2026-09.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "facts/a.md" {
		t.Fatalf("want only facts/a.md, got %v", paths(got))
	}
}

func paths(cs []*Concept) []string {
	var p []string
	for _, c := range cs {
		p = append(p, c.Path)
	}
	return p
}

// Filenames a person can guess and tab-complete: no timestamps, no suffixes.
func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Never overwrite memory history": "never-overwrite-memory-history",
		"  Prefers pnpm over npm!  ":     "prefers-pnpm-over-npm",
		"C++ / Rust interop":             "c-rust-interop",
		"":                               "untitled",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slug(strings.Repeat("word ", 40)); len(got) > 60 {
		t.Errorf("slug too long: %d", len(got))
	}
}

func TestAppendLogIsNewestFirst(t *testing.T) {
	v := tmpVault(t)
	if err := v.AppendLog("proposed rules/a.md"); err != nil {
		t.Fatal(err)
	}
	if err := v.AppendLog("archived facts/b.md"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "log.md"))
	s := string(raw)
	ai, bi := strings.Index(s, "archived"), strings.Index(s, "proposed")
	if ai < 0 || bi < 0 || ai > bi {
		t.Errorf("newest must be first:\n%s", s)
	}
}
