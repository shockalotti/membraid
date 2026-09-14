package wirelog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A sync's rebase replaces the log file with a new one while an MCP server
// holds the old one open. Its next line must land in the file at the path, not
// in the deleted file, where no commit would ever see it.
func TestAppendAfterTheFileIsReplacedWritesToTheNewFile(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, "omarchy")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	now := time.Now().UTC()
	if err := l.Append(CloseLine{Header: NewHeader(TypeClose, now), ID: "before"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, l.monthFile(now))
	first, _ := os.ReadFile(path)

	// What git checkout does: write the new content to a new file, then put it
	// in place of the old one.
	tmp := path + ".new"
	if err := os.WriteFile(tmp, first, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}

	if err := l.Append(CloseLine{Header: NewHeader(TypeClose, now), ID: "after"}); err != nil {
		t.Fatal(err)
	}
	es, _, err := ReadFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[1].Close == nil || es[1].Close.ID != "after" {
		t.Fatalf("the line written after the file was replaced must be in the file at the path, got %d entries", len(es))
	}

	// And when the file was removed outright, the next line recreates it.
	os.Remove(path)
	if err := l.Append(CloseLine{Header: NewHeader(TypeClose, now), ID: "recreated"}); err != nil {
		t.Fatal(err)
	}
	if es, _, _ := ReadFrom(path, 0); len(es) != 1 || es[0].Close.ID != "recreated" {
		t.Errorf("a removed log file must be recreated by the next line, got %+v", es)
	}
}
