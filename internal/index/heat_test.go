package index

import (
	"math"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.02 }

func whyOf(t *testing.T, ix *Index, h Hit) *Why {
	t.Helper()
	whys, err := ix.explain([]Hit{h})
	if err != nil {
		t.Fatal(err)
	}
	return whys[h.ID]
}

// Showing up in search results is not use. A memory an agent reported using
// once outranks an equally relevant one that appeared in many searches.
func TestUseOutranksAppearance(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	used, _ := ix.Write(Memory{Kind: KindInsight, Content: "alpha deploys via railway", Source: "x"})
	seen, _ := ix.Write(Memory{Kind: KindInsight, Content: "bravo deploys via railway", Source: "x"})
	clock = clock.AddDate(0, 0, 90)

	for i := 0; i < 5; i++ {
		ix.Search("railway", "shared", 10)
		ix.Touch([]string{seen.ID})
	}
	if got, err := ix.MarkUsed([]string{used.ID}, "claude-code", 1); err != nil || len(got) != 1 {
		t.Fatalf("mark used: %v %v", got, err)
	}
	hits, err := ix.Search("railway", "shared", 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("search: %v %v", hits, err)
	}
	if hits[0].ID != used.ID || !near(hits[0].Why.Uses, 1) || hits[1].Why.Uses != 0 {
		t.Errorf("the used memory must lead, and appearances must add no use: %+v %+v", hits[0].Why, hits[1].Why)
	}
}

// A memory used again and again over weeks outranks one used once, today.
func TestRepeatedUseOutranksOneRecentUse(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	often, _ := ix.Write(Memory{Kind: KindInsight, Content: "alpha build needs the lockfile fresh", Source: "x"})
	once, _ := ix.Write(Memory{Kind: KindInsight, Content: "bravo build needs the lockfile fresh", Source: "x"})
	for i := 0; i < 6; i++ {
		clock = clock.AddDate(0, 0, 4)
		ix.MarkUsed([]string{often.ID}, "x", 1)
	}
	ix.MarkUsed([]string{once.ID}, "x", 1)

	hits, _ := ix.Search("build lockfile", "shared", 10)
	if len(hits) != 2 || hits[0].ID != often.ID {
		t.Fatalf("repeated use must win: %+v", hits)
	}
	if hits[0].Why.Heat <= hits[1].Why.Heat || hits[0].Why.Boost <= 1 {
		t.Errorf("heat and boost must reflect repetition: %+v vs %+v", hits[0].Why, hits[1].Why)
	}
}

// Heat belongs to the subject, so restating a keyed memory keeps it; and it
// halves every halflife.
func TestHeatSurvivesRestatementAndFades(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	first, _ := ix.Write(Memory{Kind: KindPreference, Key: "pkg.manager", Content: "uses npm", Source: "x"})
	ix.MarkUsed([]string{first.ID, first.ID}, "x", 1) // one use per call: duplicates in one call count once
	ix.MarkUsed([]string{first.ID}, "x", 1)
	ix.MarkUsed([]string{first.ID}, "x", 1)
	restated, _ := ix.Write(Memory{Kind: KindPreference, Key: "pkg.manager", Content: "uses pnpm", Source: "x"})

	h := Hit{ID: restated.ID, Kind: KindPreference, Key: "pkg.manager", Scope: ScopeShared}
	if w := whyOf(t, ix, h); !near(w.Uses, 3) || w.Writes != 2 || !near(w.Heat, 5) {
		t.Errorf("want 3 uses and 2 writes carried to the restated memory, got %+v", w)
	}
	clock = clock.AddDate(0, 0, 30)
	if w := whyOf(t, ix, h); !near(w.Uses, 1.5) || !near(w.Heat, 2.5) {
		t.Errorf("heat must halve after one halflife, got %+v", w)
	}
}

func TestRankingSettings(t *testing.T) {
	ix := newIndex(t)
	if got := ix.Ranking(); got != DefaultRanking() {
		t.Errorf("an index starts with the defaults, got %+v", got)
	}
	if b := ix.boost(4); !near(b, 1+math.Log(4)) {
		t.Errorf("default boost of heat 4 wrong: %v", b)
	}
	ix.SetRanking(Ranking{HalflifeDays: 30, FrequencyBoost: 0, DigestItems: 12, DigestSharedWeight: 0.7})
	if b := ix.boost(4); b != 1 {
		t.Errorf("frequency_boost 0 must stop repetition adding anything, got %v", b)
	}
	if b := ix.boost(0.5); b != 0.5 {
		t.Errorf("below one use, boost is the heat itself, got %v", b)
	}
	ix.SetRanking(Ranking{HalflifeDays: -3, FrequencyBoost: 99, DigestItems: 0, DigestSharedWeight: 7})
	if got := ix.Ranking(); got != DefaultRanking() {
		t.Errorf("out-of-range values must fall back to defaults, got %+v", got)
	}
}

// Each machine checkpoints its own heat. Another machine keeps the newest value
// per machine and sums them, so uses on both machines add up exactly once.
func TestHeatTravelsAcrossMachinesAndSums(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tick := func() time.Time { return clock }
	a := newIndexAt(t, dir, "a")
	a.SetClock(tick)
	w, _ := a.Write(Memory{Kind: KindInsight, Content: "the staging db is on port 5432", Source: "x"})
	b := newIndexAt(t, dir, "b")
	b.SetClock(tick)
	if _, err := b.ImportAll(); err != nil {
		t.Fatal(err)
	}

	a.MarkUsed([]string{w.ID}, "claude-code", 1)
	a.Checkpoint(0)
	clock = clock.Add(time.Minute)
	a.MarkUsed([]string{w.ID}, "claude-code", 1)
	a.Checkpoint(0)
	if got, err := b.MarkUsed([]string{w.ID}, "hermes", 1); err != nil || len(got) != 1 {
		t.Fatalf("b must record its use: %v %v", got, err)
	}
	if n, err := b.Checkpoint(0); err != nil || n == 0 {
		t.Fatalf("b must checkpoint its heat: %d %v", n, err)
	}

	h := Hit{ID: w.ID, Kind: KindInsight, Scope: ScopeShared}
	c := newIndexAt(t, dir, "c")
	c.SetClock(tick)
	if _, err := c.ImportAll(); err != nil {
		t.Fatal(err)
	}
	if why := whyOf(t, c, h); !near(why.Uses, 3) {
		t.Errorf("a third machine must see 2 uses from a and 1 from b, got %+v", why)
	}
	if _, err := a.ImportAll(); err != nil {
		t.Fatal(err)
	}
	if why := whyOf(t, a, h); !near(why.Uses, 3) {
		t.Errorf("a must add b's use to its own without counting its own twice, got %+v", why)
	}
	uses, _ := c.UsesBySource(7)
	if !near(uses["claude-code"], 2) || !near(uses["hermes"], 1) {
		t.Errorf("uses by agent must add up across machines: %v", uses)
	}
}

func TestMarkUsedResolvesDigestIDs(t *testing.T) {
	ix := newIndex(t)
	w, _ := ix.Write(Memory{Kind: KindInsight, Content: "flaky e2e on cold caches", Source: "x"})
	if got, err := ix.MarkUsed([]string{w.ID[:8]}, "x", 1); err != nil || len(got) != 1 || got[0].ID != w.ID {
		t.Errorf("an eight-character prefix must resolve: %v %v", got, err)
	}
	if got, _ := ix.MarkUsed([]string{"abc", "ffffffffffff"}, "x", 1); len(got) != 0 {
		t.Errorf("a too-short or unknown id must match nothing: %v", got)
	}
}

func TestRescopeMovesHeat(t *testing.T) {
	ix := newIndex(t)
	w, _ := ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: "railway", Scope: "p1", Source: "x"})
	ix.MarkUsed([]string{w.ID}, "x", 1)
	if _, err := ix.Rescope("p1", "g1"); err != nil {
		t.Fatal(err)
	}
	h := Hit{ID: w.ID, Kind: KindProjectParam, Key: "deploy.target", Scope: "g1"}
	if why := whyOf(t, ix, h); !near(why.Uses, 1) {
		t.Errorf("heat must move with its memories, got %+v", why)
	}
}

// Use tips close calls but cannot overturn a clearly better match: a memory
// used again and again still ranks below one that matches the query far better.
func TestUseCannotBeatAClearlyBetterMatch(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	strong, _ := ix.Write(Memory{Kind: KindInsight, Content: "staging deploys go to railway after the migration check passes", Source: "x"})
	weak, _ := ix.Write(Memory{Kind: KindInsight, Content: "the office wifi password rotates monthly; ask about railway tickets", Source: "x"})
	for i := 0; i < 30; i++ {
		clock = clock.Add(12 * time.Hour)
		ix.MarkUsed([]string{weak.ID}, "x", 1)
	}
	hits, err := ix.Search("staging deploys railway migration check", "shared", 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("search: %v %v", hits, err)
	}
	if hits[0].ID != strong.ID {
		t.Errorf("a clearly better match must lead however much the other is used: %+v %+v", hits[0].Why, hits[1].Why)
	}
	if f := hits[1].Why.UseFactor; f > useFactorMax || f <= 1 {
		t.Errorf("heavy use must lift within the bound, got use factor %v", f)
	}
}

// Memories agents already rely on cannot hold every digest slot: the newest
// memories keep a place.
func TestDigestKeepsRoomForNewMemories(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	var old []string
	for i := 0; i < 15; i++ {
		w, _ := ix.Write(Memory{Kind: KindProjectParam, Key: "setting.k" + string(rune('a'+i)), Content: "established value", Scope: "g1", Source: "x"})
		old = append(old, w.ID)
	}
	for d := 0; d < 20; d++ {
		clock = clock.Add(24 * time.Hour)
		ix.MarkUsed(old, "x", 1)
	}
	var fresh []string
	for i := 0; i < 3; i++ {
		clock = clock.Add(time.Hour)
		w, _ := ix.Write(Memory{Kind: KindInsight, Content: "new finding", Scope: "g1", Source: "x"})
		fresh = append(fresh, w.ID)
	}
	got, err := ix.Digest("g1", 12)
	if err != nil || len(got) != 12 {
		t.Fatalf("digest: %d %v", len(got), err)
	}
	in := map[string]bool{}
	for _, s := range got {
		in[s.ID] = true
		if s.Score > 0.9*digestBoostCap+1e-9 {
			t.Errorf("boost in the digest is capped, got score %v for %+v", s.Score, s.Hit)
		}
	}
	for _, id := range fresh {
		if !in[id] {
			t.Errorf("the newest memories must keep a place in the digest; missing %s", id)
		}
	}
}
