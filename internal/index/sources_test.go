package index

import "testing"

func TestSourcesListsKnowledgeLocations(t *testing.T) {
	ix := newIndex(t)
	ix.Write(Memory{Kind: KindProjectParam, Key: "source.api.specs", Content: "old", Scope: "g1", Source: "user"})
	ix.Write(Memory{Kind: KindProjectParam, Key: "source.api.specs", Content: "specs", Scope: "g1", Source: "user"})
	ix.Write(Memory{Kind: KindProjectParam, Key: "source.notes", Content: "notes", Scope: ScopeShared, Source: "user"})
	ix.Write(Memory{Kind: KindProjectParam, Key: "sourcery.config", Content: "not a location", Scope: "g1", Source: "x"})
	ix.Write(Memory{Kind: KindInsight, Content: "unkeyed", Scope: "g1", Source: "x"})

	all, err := ix.Sources("*")
	if err != nil || len(all) != 2 {
		t.Fatalf("want the two current locations, got %v %v", all, err)
	}
	g1, _ := ix.Sources("g1")
	if len(g1) != 1 || g1[0].Content != "specs" {
		t.Errorf("want only g1's current location, got %+v", g1)
	}
	none, _ := ix.Sources("p9")
	if none == nil || len(none) != 0 {
		t.Errorf("no locations is an empty list, got %v", none)
	}
}
