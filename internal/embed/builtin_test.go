//go:build !nobuiltin

package embed

import (
	"context"
	"os"
	"testing"
)

func dotf(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// The built-in model downloads about 90 MB on first use, so this runs only when
// asked: MEMBRAID_TEST_BUILTIN=1, with MEMBRAID_TEST_MODELS pointing at an
// existing download to skip it.
func TestBuiltinEmbedsMeaning(t *testing.T) {
	if os.Getenv("MEMBRAID_TEST_BUILTIN") != "1" {
		t.Skip("set MEMBRAID_TEST_BUILTIN=1 to run (downloads about 90 MB)")
	}
	e, err := NewBuiltin(os.Getenv("MEMBRAID_TEST_MODELS"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.Model() != "builtin:all-MiniLM-L6-v2" {
		t.Errorf("model identity: %s", e.Model())
	}
	ctx := context.Background()
	docs, err := e.EmbedDocs(ctx, []string{
		"Production deploys to Railway from the main branch.",
		"The team prefers tabs over spaces in Go files.",
	})
	if err != nil {
		t.Fatal(err)
	}
	q, err := e.EmbedQuery(ctx, "where is the web app hosted")
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 384 || len(docs[0]) != 384 || e.Dim() != 384 {
		t.Fatalf("want 384 dimensions, got query %d doc %d dim %d", len(q), len(docs[0]), e.Dim())
	}
	if near, far := dotf(q, docs[0]), dotf(q, docs[1]); near <= far {
		t.Errorf("hosting question should be nearer the deploy memory: %.3f vs %.3f", near, far)
	}
}
