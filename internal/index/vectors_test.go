package index

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const testModel = "test:all-MiniLM-L6-v2"

// readEmbeddings loads testdata/search/minilm-384.ebc: all-MiniLM-L6-v2
// vectors for memories.jsonl then queries.jsonl, in file order, as cached by
// the search evaluation. Header: "EBC1", doc count, query count, dimension.
func readEmbeddings(t *testing.T) (docs, queries [][]float32) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "search", "minilm-384.ebc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:4]) != "EBC1" {
		t.Fatal("not an embedding cache file")
	}
	nd := int(binary.LittleEndian.Uint32(b[4:]))
	nq := int(binary.LittleEndian.Uint32(b[8:]))
	dim := int(binary.LittleEndian.Uint32(b[12:]))
	off := 16
	read := func(n int) [][]float32 {
		out := make([][]float32, n)
		for i := range out {
			v := make([]float32, dim)
			for j := range v {
				v[j] = math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))
				off += 4
			}
			out[i] = v
		}
		return out
	}
	return read(nd), read(nq)
}

func vectorIndex(t *testing.T) (*Index, []searchQuery, [][]float32) {
	t.Helper()
	ix := newIndex(t)
	mems := readJSONL[searchMemory](t, "memories.jsonl")
	queries := readJSONL[searchQuery](t, "queries.jsonl")
	docVecs, queryVecs := readEmbeddings(t)
	if len(docVecs) != len(mems) || len(queryVecs) != len(queries) {
		t.Fatalf("embeddings do not match the test set: %d/%d docs, %d/%d queries", len(docVecs), len(mems), len(queryVecs), len(queries))
	}
	for i, m := range mems {
		if _, err := ix.Write(Memory{ID: m.ID, Kind: m.Kind, Key: m.Key, Scope: m.Scope, Content: m.Content, Source: "testdata"}); err != nil {
			t.Fatal(err)
		}
		if err := ix.PutVector(m.ID, testModel, docVecs[i]); err != nil {
			t.Fatal(err)
		}
	}
	return ix, queries, queryVecs
}

func recallAt5(t *testing.T, ix *Index, queries []searchQuery, vecs [][]float32) (map[string]float64, [][]string) {
	t.Helper()
	found, total := map[string]int{}, map[string]int{}
	var ranked [][]string
	for i, q := range queries {
		hits, err := ix.VectorSearch(vecs[i], testModel, "*", 5)
		if err != nil {
			t.Fatalf("%s: %v", q.ID, err)
		}
		ids := make([]string, len(hits))
		for j, h := range hits {
			ids[j] = h.ID
		}
		ranked = append(ranked, ids)
		total[q.Category]++
		total["overall"]++
		for _, id := range ids {
			if contains(q.Relevant, id) {
				found[q.Category]++
				found["overall"]++
				break
			}
		}
	}
	out := map[string]float64{}
	for c, n := range total {
		out[c] = float64(found[c]) / float64(n)
	}
	return out, ranked
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Vector search through the index must reproduce what the evaluation measured
// for all-MiniLM-L6-v2 (R@5 0.91 overall, 0.84 on paraphrase), and the SQL scan
// and the in-memory cache must rank identically.
func TestVectorSearchQuality(t *testing.T) {
	ix, queries, vecs := vectorIndex(t)

	sqlRecall, sqlRanked := recallAt5(t, ix, queries, vecs)
	for _, c := range []string{"overall", "paraphrase", "natural", "identifier", "distractor"} {
		t.Logf("vector recall@5 %-10s %.2f", c, sqlRecall[c])
	}
	for c, floor := range map[string]float64{"overall": 0.89, "paraphrase": 0.80} {
		if sqlRecall[c] < floor {
			t.Errorf("vector recall@5 for %s is %.2f, below %.2f", c, sqlRecall[c], floor)
		}
	}

	ix.EnableVectorCache()
	cacheRecall, cacheRanked := recallAt5(t, ix, queries, vecs)
	if !reflect.DeepEqual(sqlRanked, cacheRanked) {
		t.Errorf("the in-memory cache must rank exactly like the SQL scan (recall %.2f vs %.2f)", cacheRecall["overall"], sqlRecall["overall"])
	}
}

// Forgotten memories and other projects' memories must never surface, and the
// in-memory cache must notice a change made after it was loaded.
func TestVectorSearchRespectsScopeAndRetirement(t *testing.T) {
	ix, queries, vecs := vectorIndex(t)
	ix.EnableVectorCache()

	// q086: "Where does Oriel production deploy?", answered by m113.
	var qi int
	for i, q := range queries {
		if q.ID == "q086" {
			qi = i
		}
	}
	hits, err := ix.VectorSearch(vecs[qi], testModel, "oriel", 10)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search: %v %v", hits, err)
	}
	for _, h := range hits {
		if h.Scope != "oriel" && h.Scope != ScopeShared {
			t.Errorf("scope oriel returned a %s memory: %s", h.Scope, h.ID)
		}
	}
	// all-MiniLM ranks the right memory first for only 60% of distractor
	// queries, so require it among the results rather than at the top.
	inResults := func(id string) bool {
		for _, h := range hits {
			if h.ID == id {
				return true
			}
		}
		return false
	}
	if !inResults("m113") {
		t.Fatalf("expected m113 among the results before forgetting it, got %+v", hits)
	}

	if _, err := ix.Forget("oriel", "", "m113"); err != nil {
		t.Fatal(err)
	}
	hits, _ = ix.VectorSearch(vecs[qi], testModel, "oriel", 10)
	if inResults("m113") {
		t.Error("a forgotten memory must not come back from the in-memory cache")
	}
}

func TestMissingVectorsAndCoverage(t *testing.T) {
	ix := newIndex(t)
	a, _ := ix.Write(Memory{Kind: KindInsight, Content: "first", Source: "x"})
	b, _ := ix.Write(Memory{Kind: KindInsight, Content: "second", Source: "x"})
	if err := ix.PutVector(a.ID, testModel, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	pending, err := ix.MissingVectors(testModel, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != b.ID {
		t.Fatalf("want only the unembedded memory pending, got %+v %v", pending, err)
	}
	if other, _ := ix.MissingVectors("another:model", 10); len(other) != 2 {
		t.Errorf("a different model has no vectors yet, want 2 pending, got %d", len(other))
	}
	if have, total, _ := ix.VectorCoverage(testModel); have != 1 || total != 2 {
		t.Errorf("coverage: have %d of %d, want 1 of 2", have, total)
	}
	ix.Forget("shared", "", b.ID)
	if pending, _ := ix.MissingVectors(testModel, 10); len(pending) != 0 {
		t.Errorf("a forgotten memory needs no vector, got %+v", pending)
	}
}

func TestDropOtherVectors(t *testing.T) {
	ix := newIndex(t)
	w, _ := ix.Write(Memory{Kind: KindInsight, Content: "one fact", Source: "x"})
	ix.PutVector(w.ID, "old:model", []float32{1, 0})
	ix.PutVector(w.ID, testModel, []float32{0, 1})
	if n, err := ix.DropOtherVectors(testModel); err != nil || n != 1 {
		t.Fatalf("want 1 old vector removed, got %d %v", n, err)
	}
	if have, _, _ := ix.VectorCoverage(testModel); have != 1 {
		t.Error("the current model's vector must survive")
	}
	if have, _, _ := ix.VectorCoverage("old:model"); have != 0 {
		t.Error("the old model's vector must be gone")
	}
}
