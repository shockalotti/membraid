package index

import (
	"errors"
	"testing"
)

// Characters that are not separators are removed before separators collapse,
// so no leftover run of dots splits one subject in two.
func TestKeyNormalizationRemovesJunkBeforeCollapsing(t *testing.T) {
	for in, want := range map[string]string{
		"editor.!.theme": "editor.theme",
		"Editor_Theme":   "editor.theme",
		"editor . theme": "editor.theme",
		"deploy/target!": "deploy.target",
		"pkg:manager":    "pkgmanager",
		"!!!":            "",
	} {
		if got := NormalizeKey(in); got != want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", in, got, want)
		}
	}
	ix := newIndex(t)
	if _, err := ix.Write(Memory{Kind: KindPreference, Key: "!!!", Content: "x", Source: "t"}); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("a key with nothing left must be refused, not written unkeyed: %v", err)
	}
}
