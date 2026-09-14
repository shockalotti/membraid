package index

import (
	"testing"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// A correction names the memory it replaces. The old one leaves current memory
// with its successor recorded, the fuzzy match does not also run, and a
// rebuild agrees.
func TestExplicitReplacesSupersedesByID(t *testing.T) {
	dir := t.TempDir()
	ix := newIndexAt(t, dir, "a")
	old, _ := ix.Write(Memory{Kind: KindInsight, Content: "the staging db is on port 5432", Scope: "g1", Source: "x"})
	other, _ := ix.Write(Memory{Kind: KindInsight, Content: "the build needs make", Scope: "g1", Source: "x"})
	res, err := ix.Write(Memory{Kind: KindInsight, Content: "the staging db moved to port 6543", Scope: "g1", Source: "user", Replaces: []string{old.ID, "", old.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Superseded) != 1 || res.Superseded[0] != old.ID || res.Mode != wirelog.ModeExplicit {
		t.Fatalf("the named memory must be replaced, once: %+v", res)
	}
	var by string
	ix.db.QueryRow(`SELECT COALESCE(superseded_by, '') FROM memories WHERE id=?`, old.ID).Scan(&by)
	if by != res.ID {
		t.Errorf("the old memory must record what replaced it, got %q", by)
	}
	if r, _ := ix.Write(Memory{Kind: KindInsight, Content: "a correction naming a memory that is gone", Scope: "g1", Source: "user", Replaces: []string{old.ID}}); len(r.Superseded) != 0 {
		t.Errorf("a memory no longer current is not replaced again: %+v", r)
	}
	fresh := newIndexAt(t, dir, "fresh")
	fresh.ImportAll()
	if snapshot(t, fresh) != snapshot(t, ix) {
		t.Error("a rebuild must agree")
	}
	_ = other
}
