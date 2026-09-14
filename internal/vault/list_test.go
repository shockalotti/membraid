package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// A hand-edited note whose frontmatter will not parse is skipped and named,
// and every other note is still listed.
func TestListSkipsUnreadableNotes(t *testing.T) {
	v := Open(filepath.Join(t.TempDir(), "vault"))
	if err := v.Init(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(v.Root(), "facts", "good.md"), []byte("---\ntype: fact\ntitle: Good\n---\n\nFine.\n"), 0o600)
	os.WriteFile(filepath.Join(v.Root(), "facts", "bad.md"), []byte("---\ntype: fact\nrelated: [[other-note]]\n---\n\nObsidian link.\n"), 0o600)

	got, unreadable, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "facts/good.md" {
		t.Errorf("the readable note must still be listed, got %+v", got)
	}
	if len(unreadable) != 1 || unreadable[0] != "facts/bad.md" {
		t.Errorf("the unreadable note must be named, got %v", unreadable)
	}
}
