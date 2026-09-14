package index

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// A standing preference lives in shared, so a lookup from inside a project must
// find it, and a project's own answer on the same key as well.
func TestCurrentInScopesIncludesShared(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindPreference, Key: "pkg.manager", Content: "uses pnpm", Scope: ScopeShared, Source: "x"})
	ix.Write(Memory{Kind: KindPreference, Key: "pkg.manager", Content: "this repo uses bun", Scope: "g1", Source: "x"})

	for _, c := range []struct {
		scope string
		want  int
	}{{"g1", 2}, {"g2", 1}, {ScopeShared, 1}, {"*", 2}} {
		got, err := ix.CurrentInScopes(c.scope, KindPreference, "pkg.manager")
		if err != nil || len(got) != c.want {
			t.Errorf("lookup from %s: want %d answers, got %+v %v", c.scope, c.want, got, err)
		}
	}
}

// Quarantined memories are reachable only by asking for unscoped by name.
func TestUnscopedIsNeverAutoIncluded(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindInsight, Content: "orphan fact about railway", Scope: ScopeUnscoped, Source: "x"})
	ix.Write(Memory{Kind: KindInsight, Content: "shared fact about railway", Scope: ScopeShared, Source: "x"})

	for _, sc := range []string{"g1", ScopeShared} {
		hits, _ := ix.Search("railway", sc, 10)
		if len(hits) != 1 || hits[0].Scope != ScopeShared {
			t.Errorf("a search from %s must not see unscoped: %+v", sc, hits)
		}
	}
	if hits, _ := ix.Search("railway", ScopeUnscoped, 10); len(hits) != 1 || hits[0].Scope != ScopeUnscoped {
		t.Errorf("asking for unscoped by name must reach only the quarantine: %+v", hits)
	}
	if n := ix.UnscopedCount(); n != 1 {
		t.Errorf("want 1 unscoped memory counted, got %d", n)
	}
}

func setUserVersion(t *testing.T, path string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA user_version = ` + itoa(v)); err != nil {
		t.Fatal(err)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	s := ""
	for ; v > 0; v /= 10 {
		s = string(rune('0'+v%10)) + s
	}
	return s
}

// An index written by a newer membraid is refused rather than stamped back to
// this version; one too old to upgrade in place is set aside and rebuilt.
func TestSchemaVersionGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")
	ix, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix.Write(Memory{Kind: KindInsight, Content: "kept", Source: "x"})
	ix.Close()

	setUserVersion(t, path, schemaVersion+1)
	if _, err := Open(path, nil); !errors.Is(err, ErrNewerIndex) {
		t.Fatalf("a newer index must be refused, got %v", err)
	}

	setUserVersion(t, path, minUpgradeVersion-1)
	ix, err = Open(path, nil)
	if err != nil {
		t.Fatalf("an old index must be set aside and rebuilt, got %v", err)
	}
	defer ix.Close()
	if st, _ := ix.Stats(); st.Total != 0 {
		t.Errorf("the rebuilt index starts empty and fills from the log, got %+v", st)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "index.db.schema*.bak"))
	if len(backups) != 1 {
		t.Errorf("the old index must be kept beside it, got %v", backups)
	}
}
