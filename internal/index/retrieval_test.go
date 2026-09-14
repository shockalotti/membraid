package index

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func lastRetrieved(t *testing.T, ix *Index, id string) string {
	t.Helper()
	var v sql.NullString
	if err := ix.db.QueryRow(`SELECT last_retrieved FROM memories WHERE id=?`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v.String
}

// Two memories match a query equally well. The one an agent used today must
// outrank the one nobody has used in three halflives.
func TestSearchRanksUnusedMemoriesLower(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	ix.Write(Memory{Kind: KindInsight, Content: "alpha deploys via railway", Source: "x"})
	used, _ := ix.Write(Memory{Kind: KindInsight, Content: "bravo deploys via railway", Source: "x"})

	clock = clock.Add(90 * 24 * time.Hour)
	if _, err := ix.MarkUsed([]string{used.ID}, "x", 1); err != nil {
		t.Fatal(err)
	}
	hits, err := ix.Search("railway deploys", "shared", 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("search: %v %v", hits, err)
	}
	if hits[0].ID != used.ID {
		t.Errorf("a memory retrieved today must outrank an equally relevant one unused for 90 days: %+v", hits)
	}
}

// Only intentional reads count as retrieval. Whatever the digest or a listing
// shows would otherwise keep itself fresh forever and never decay.
func TestOnlyIntentionalReadsRecordRetrieval(t *testing.T) {
	ix := newIndex(t)
	w, _ := ix.Write(Memory{Kind: KindPreference, Key: "editor.theme", Content: "prefers dark theme", Source: "x"})
	ix.Recent("shared", nil, 10)
	ix.Digest("shared", 10)
	ix.Search("dark theme", "shared", 10)
	if got := lastRetrieved(t, ix, w.ID); got != "" {
		t.Errorf("listing, digest and a search nobody acted on must not record retrieval, got %s", got)
	}
	if err := ix.Touch([]string{w.ID}); err != nil {
		t.Fatal(err)
	}
	if lastRetrieved(t, ix, w.ID) == "" {
		t.Error("touch must record retrieval")
	}
}

// Retrieval state travels in checkpoint lines, so a rebuilt index and every
// other machine agree on what has been used. With one log per machine, replay
// keeps the newest time per memory rather than letting one machine's snapshot
// erase another's.
func TestCheckpointCarriesRetrievalAcrossMachines(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := newIndexAt(t, dir, "a")
	a.SetClock(func() time.Time { return clock })
	w, _ := a.Write(Memory{Kind: KindInsight, Content: "shared fact about the build", Source: "x"})
	if n, _ := a.Checkpoint(0); n != 0 {
		t.Error("nothing retrieved must write no checkpoint")
	}

	clock = clock.Add(time.Hour)
	a.Touch([]string{w.ID})
	if n, err := a.Checkpoint(time.Hour); err != nil || n != 1 {
		t.Fatalf("checkpoint on a: %d %v", n, err)
	}
	if n, _ := a.Checkpoint(time.Hour); n != 0 {
		t.Error("a second checkpoint within the interval must write nothing")
	}

	b := newIndexAt(t, dir, "b")
	b.SetClock(func() time.Time { return clock })
	if _, err := b.ImportAll(); err != nil {
		t.Fatal(err)
	}
	if lastRetrieved(t, b, w.ID) == "" {
		t.Fatal("machine b must learn a's retrieval from the checkpoint")
	}
	clock = clock.Add(48 * time.Hour)
	b.Touch([]string{w.ID})
	newest := lastRetrieved(t, b, w.ID)
	if n, err := b.Checkpoint(time.Hour); err != nil || n != 1 {
		t.Fatalf("checkpoint on b: %d %v", n, err)
	}

	fresh := newIndexAt(t, dir, "fresh")
	if _, err := fresh.ImportAll(); err != nil {
		t.Fatal(err)
	}
	if got := lastRetrieved(t, fresh, w.ID); got != newest {
		t.Errorf("replay must keep the newest retrieval across machines: got %q, want %q", got, newest)
	}
}

// A subject restated across sessions has proven it matters, and outranks a
// burst of one-off observations written after it.
func TestDigestLeadsWithWhatIsKnownNotWhatIsNewest(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	for i := 0; i < 3; i++ {
		ix.Write(Memory{Kind: KindProjectParam, Key: "deploy.target", Content: fmt.Sprintf("deploys to railway (restated %d)", i), Scope: "g1", Source: "x"})
		clock = clock.Add(time.Hour)
	}
	clock = clock.Add(24 * time.Hour)
	for i := 0; i < 20; i++ {
		ix.Write(Memory{Kind: KindInsight, Content: fmt.Sprintf("passing observation %d", i), Scope: "g1", Source: "x"})
	}

	got, err := ix.Digest("g1", 12)
	if err != nil || len(got) != 12 {
		t.Fatalf("digest: %d %v", len(got), err)
	}
	if got[0].Key != "deploy.target" || got[0].Writes != 3 {
		t.Errorf("the restated subject should lead the digest, got %+v", got[0])
	}
}

// Standing preferences apply in every session, so a digest keeps room for them
// even when a project's facts all outscore them.
func TestDigestKeepsRoomForPreferences(t *testing.T) {
	ix := newIndex(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ix.SetClock(func() time.Time { return clock })
	ix.Write(Memory{Kind: KindPreference, Key: "pkg.manager", Content: "uses pnpm, never npm", Scope: "shared", Source: "x"})
	clock = clock.Add(60 * 24 * time.Hour)
	for k := 0; k < 15; k++ {
		for i := 0; i < 3; i++ {
			ix.Write(Memory{Kind: KindProjectParam, Key: fmt.Sprintf("setting.n%d", k), Content: fmt.Sprintf("value %d restated %d", k, i), Scope: "g1", Source: "x"})
		}
	}

	got, err := ix.Digest("g1", 12)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range got {
		if s.Key == "pkg.manager" {
			found = true
		}
	}
	if !found {
		t.Errorf("a standing preference must keep its place in the digest, got %d items with none", len(got))
	}
	if len(got) != 12 {
		t.Errorf("want 12 items, got %d", len(got))
	}
}

// A checkpoint carries only what changed since the last one, so its size
// follows activity rather than the store. Replaying a run of them on another
// machine still arrives at every retrieval and every use.
func TestCheckpointsCarryOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tick := func() time.Time { return clock }
	a := newIndexAt(t, dir, "a")
	a.SetClock(tick)
	var ids []string
	for i := 0; i < 20; i++ {
		w, _ := a.Write(Memory{Kind: KindInsight, Content: fmt.Sprintf("fact %d", i), Source: "x"})
		ids = append(ids, w.ID)
	}
	a.Touch(ids)
	a.MarkUsed(ids[:5], "x", 1)
	clock = clock.Add(time.Minute)
	if n, err := a.Checkpoint(0); err != nil || n != 25 {
		t.Fatalf("the first checkpoint carries everything: %d %v", n, err)
	}

	clock = clock.Add(time.Minute)
	a.Touch(ids[7:8])
	a.MarkUsed(ids[7:8], "x", 1)
	clock = clock.Add(time.Minute)
	if n, err := a.Checkpoint(0); err != nil || n != 2 {
		t.Fatalf("a later checkpoint carries only the changed row and heat: %d %v", n, err)
	}
	clock = clock.Add(time.Minute)
	if n, _ := a.Checkpoint(0); n != 0 {
		t.Errorf("nothing changed, nothing written, got %d", n)
	}

	b := newIndexAt(t, dir, "b")
	b.SetClock(tick)
	if _, err := b.ImportAll(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if lastRetrieved(t, b, id) == "" {
			t.Errorf("b must learn every retrieval from the run of checkpoints, missing %s", id)
		}
	}
	whys, _ := b.explain([]Hit{{ID: ids[0], Kind: KindInsight, Scope: ScopeShared}, {ID: ids[7], Kind: KindInsight, Scope: ScopeShared}})
	if whys[ids[0]].Uses < 0.9 || whys[ids[7]].Uses < 0.9 {
		t.Errorf("b must learn uses from both checkpoints: %+v %+v", whys[ids[0]], whys[ids[7]])
	}
}
