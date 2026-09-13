package main

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shockalotti/membraid/internal/index"
)

// fakeEmbedder hashes words into a small bag-of-words vector: no model, no
// download, deterministic. Enough to tell vector search from keyword search.
type fakeEmbedder struct {
	calls   int
	texts   int
	failDoc bool
	failQry bool
}

func (f *fakeEmbedder) Name() string  { return "fake" }
func (f *fakeEmbedder) Model() string { return "fake:bow-64" }
func (f *fakeEmbedder) Dim() int      { return 64 }
func (f *fakeEmbedder) Close() error  { return nil }

func (f *fakeEmbedder) vec(s string) []float32 {
	v := make([]float32, 64)
	for _, w := range strings.Fields(strings.ToLower(s)) {
		h := fnv.New32a()
		h.Write([]byte(strings.Trim(w, ".,?!")))
		v[h.Sum32()%64]++
	}
	return v
}

func (f *fakeEmbedder) EmbedDocs(_ context.Context, texts []string) ([][]float32, error) {
	if f.failDoc {
		return nil, errors.New("ollama not reachable")
	}
	f.calls++
	f.texts += len(texts)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.vec(t)
	}
	return out, nil
}

func (f *fakeEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	if f.failQry {
		return nil, errors.New("ollama not reachable")
	}
	return f.vec(text), nil
}

func testIndex(t *testing.T) *index.Index {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func TestSearchIsKeywordWhenEmbeddingsAreOff(t *testing.T) {
	ix := testIndex(t)
	ix.Write(index.Memory{Kind: index.KindInsight, Content: "the build uses pnpm", Source: "x"})
	hits, mode, err := searchMemories(context.Background(), nil, ix, "pnpm", "shared", 5)
	if err != nil || mode != "keyword" || len(hits) != 1 {
		t.Fatalf("want one keyword hit, got %v %q %v", hits, mode, err)
	}
}

// With embeddings on, a search first embeds what is pending, so a memory
// written a moment ago is found by vector search.
func TestSearchEmbedsBacklogThenSearchesByVector(t *testing.T) {
	ix := testIndex(t)
	ix.Write(index.Memory{Kind: index.KindInsight, Content: "apples grow in the orchard", Source: "x"})
	ix.Write(index.Memory{Kind: index.KindInsight, Content: "bananas ripen on the counter", Source: "x"})
	e := &fakeEmbedder{}
	hits, mode, err := searchMemories(context.Background(), e, ix, "orchard apples", "shared", 1)
	if err != nil || mode != "vector" {
		t.Fatalf("want vector search, got %q %v", mode, err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "apples") {
		t.Errorf("want the apples memory first, got %+v", hits)
	}
	if e.texts != 2 {
		t.Errorf("want both memories embedded before searching, got %d", e.texts)
	}
}

// A stopped Ollama must not break search: it falls back to keywords.
func TestSearchFallsBackToKeywordsWhenEmbedderFails(t *testing.T) {
	for name, e := range map[string]*fakeEmbedder{
		"documents fail": {failDoc: true},
		"query fails":    {failQry: true},
	} {
		t.Run(name, func(t *testing.T) {
			ix := testIndex(t)
			ix.Write(index.Memory{Kind: index.KindInsight, Content: "deploys to railway", Source: "x"})
			hits, mode, err := searchMemories(context.Background(), e, ix, "railway", "shared", 5)
			if err != nil || mode != "keyword" || len(hits) != 1 {
				t.Fatalf("want a keyword fallback hit, got %v %q %v", hits, mode, err)
			}
		})
	}
}

func TestEmbedPendingBatchesAndStops(t *testing.T) {
	ix := testIndex(t)
	for i := 0; i < 70; i++ {
		ix.Write(index.Memory{Kind: index.KindInsight, Content: fmt.Sprintf("fact number %d", i), Source: "x"})
	}
	e := &fakeEmbedder{}
	n, err := embedPending(context.Background(), e, ix, 0)
	if err != nil || n != 70 {
		t.Fatalf("want 70 embedded, got %d %v", n, err)
	}
	if e.calls != 3 {
		t.Errorf("want batches of 32 (3 calls), got %d", e.calls)
	}
	if have, total, _ := ix.VectorCoverage(e.Model()); have != 70 || total != 70 {
		t.Errorf("coverage %d of %d", have, total)
	}
	if n, _ := embedPending(context.Background(), e, ix, 0); n != 0 {
		t.Errorf("nothing left to embed, but embedded %d", n)
	}
	ix.Write(index.Memory{Kind: index.KindInsight, Content: "one more", Source: "x"})
	if n, _ := embedPending(context.Background(), e, ix, 10); n != 1 {
		t.Errorf("want only the new memory embedded, got %d", n)
	}
}
