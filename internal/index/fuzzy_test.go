package index

import (
	"testing"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// A memory restated without a key in nearly the same words replaces the
// earlier one. Pairs lexical similarity gets wrong stay two memories: dark
// mode and light mode, one changed digit, one changed word in a long sentence,
// and anything across scope, kind or a key.
func TestNearVerbatimRestatementWithoutAKeyReplacesTheOld(t *testing.T) {
	dir := t.TempDir()
	ix := newIndexAt(t, dir, "a")
	old, _ := ix.Write(Memory{Kind: KindInsight, Content: "The staging DB is on port 5432", Scope: "g1", Source: "x"})
	res, err := ix.Write(Memory{Kind: KindInsight, Content: "the staging db is on  port 5432.", Scope: "g1", Source: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Superseded) != 1 || res.Superseded[0] != old.ID || res.Mode != wirelog.ModeFuzzy {
		t.Fatalf("a restatement differing in case, spacing and punctuation must replace the old memory: %+v", res)
	}

	distinct := [][2]string{
		{"prefers dark mode", "prefers light mode"},
		{"the dev database listens on port 5432", "the dev database listens on port 5433"},
		{"the frontend repo deploys to vercel on every push to main", "the backend repo deploys to vercel on every push to main"},
	}
	for i, pair := range distinct {
		scope := "d" + string(rune('0'+i))
		ix.Write(Memory{Kind: KindInsight, Content: pair[0], Scope: scope, Source: "x"})
		if r, _ := ix.Write(Memory{Kind: KindInsight, Content: pair[1], Scope: scope, Source: "x"}); len(r.Superseded) != 0 {
			t.Errorf("%q and %q are two facts, got %+v", pair[0], pair[1], r)
		}
	}

	if r, _ := ix.Write(Memory{Kind: KindInsight, Content: "the staging db is on port 5432!", Scope: "g2", Source: "x"}); len(r.Superseded) != 0 {
		t.Errorf("another project's memory must not be replaced: %+v", r)
	}
	if r, _ := ix.Write(Memory{Kind: KindProjectParam, Content: "the staging db is on port 5432!", Scope: "g1", Source: "x"}); len(r.Superseded) != 0 {
		t.Errorf("another kind's memory must not be replaced: %+v", r)
	}
	ix.Write(Memory{Kind: KindInsight, Key: "staging.db.port", Content: "the staging database listens on 5433", Scope: "g1", Source: "x"})
	if r, _ := ix.Write(Memory{Kind: KindInsight, Content: "the staging database listens on 5433", Scope: "g1", Source: "x"}); len(r.Superseded) != 0 {
		t.Errorf("a keyed memory is only ever replaced by its key: %+v", r)
	}

	fresh := newIndexAt(t, dir, "fresh")
	fresh.ImportAll()
	if snapshot(t, fresh) != snapshot(t, ix) {
		t.Errorf("a rebuild must reach the same result from the logged outcome")
	}
}

// At 1, only the same words match, still ignoring case and punctuation.
func TestFuzzyThresholdOfOneMatchesOnlyTheSameWords(t *testing.T) {
	ix := newIndex(t)
	r := DefaultRanking()
	r.FuzzyThreshold = 1
	ix.SetRanking(r)
	ix.Write(Memory{Kind: KindInsight, Content: "The staging DB is on port 5432", Scope: "g1", Source: "x"})
	if res, _ := ix.Write(Memory{Kind: KindInsight, Content: "the staging database is on port 5432", Scope: "g1", Source: "x"}); len(res.Superseded) != 0 {
		t.Errorf("at 1 a different word is already different: %+v", res)
	}
	if res, _ := ix.Write(Memory{Kind: KindInsight, Content: "the  STAGING db is on port 5432.", Scope: "g1", Source: "x"}); len(res.Superseded) != 1 {
		t.Errorf("at 1 case, spacing and punctuation still match: %+v", res)
	}
}
