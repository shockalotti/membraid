package index

import "testing"

func TestSimilarKeys(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"editor.theme", "theme", true},
		{"editor.theme", "theme.editor", true},
		{"editor.theme", "editortheme", true},
		{"editor.theme", "editor.themes", true},
		{"deploy.target", "deploy.targets", true},
		{"db.port", "redis.port", false},
		{"editor.theme", "editor.font", false},
		{"pkg.manager", "deploy.target", false},
		{"task.auth.fix", "task.auth.docs", false},
		{"editor.theme", "editor.theme", false},
	} {
		if got := similarKeys(c.a, c.b); got != c.want {
			t.Errorf("similarKeys(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Drift is visible: the vocabulary lists each key with its counts, pairs two
// live keys on one subject, and a write starting a new key hears about the
// existing one, in its project or in shared.
func TestKeyVocabularyShowsDrift(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "dark", Scope: ScopeShared, Source: "a"})
	ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "light", Scope: ScopeShared, Source: "b"})
	ix.Write(Memory{Kind: KindPreference, Key: "theme", Content: "light", Scope: ScopeShared, Source: "c"})
	ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "railway", Scope: "g1", Source: "a"})

	keys, err := ix.Keys("*")
	if err != nil || len(keys) != 3 {
		t.Fatalf("keys: %+v %v", keys, err)
	}
	for _, k := range keys {
		if k.Key == "editor.theme" && (k.Current != 1 || k.Total != 2) {
			t.Errorf("editor.theme has one current memory of two: %+v", k)
		}
	}
	if only, _ := ix.Keys("g1"); len(only) != 1 || only[0].Key != "deploy.target" {
		t.Errorf("a scope lists only its own keys: %+v", only)
	}

	drift, _ := ix.KeyDrift("*")
	if len(drift) != 1 || drift[0].A != "editor.theme" || drift[0].B != "theme" {
		t.Errorf("editor.theme and theme must be paired: %+v", drift)
	}

	ix.Write(Memory{Kind: KindPreference, Key: "editor.color.theme", Content: "solarized", Scope: "g1", Source: "a"})
	if got, _ := ix.SimilarKeys("g1", KindPreference, "color.theme"); len(got) != 2 || got[0] != "editor.color.theme" || got[1] != "theme" {
		t.Errorf("a new key in a project must hear about similar keys there and in shared, got %v", got)
	}
	if got, _ := ix.SimilarKeys(ScopeShared, KindPreference, "editor.themes"); len(got) != 1 || got[0] != "editor.theme" {
		t.Errorf("want editor.theme, got %v", got)
	}
	if got, _ := ix.SimilarKeys(ScopeShared, KindPreference, "theme"); got != nil {
		t.Errorf("a key already in use is not new, got %v", got)
	}
	if got, _ := ix.SimilarKeys("g1", KindProjectParam, "deploy.targets"); len(got) != 1 {
		t.Errorf("want deploy.target, got %v", got)
	}
}
