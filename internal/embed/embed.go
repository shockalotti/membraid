// Package embed turns text into vectors for membraid's optional vector search.
//
// Two providers, both local: Ollama running a dedicated embedding model, or a
// small model built into the binary. Nothing here sends memory anywhere but
// this machine (docs/SEARCH-EVALUATION.md).
package embed

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// Embedder produces L2-normalised vectors, so cosine similarity is a dot
// product. Documents and queries are separate calls because some models expect
// different task prompts for each.
type Embedder interface {
	// Name is the provider kind: "ollama" or "builtin".
	Name() string
	// Model identifies the exact model, stored with every vector so a change of
	// model is detected and the store re-embedded rather than mixing spaces.
	Model() string
	// Dim is the vector length, or 0 until the first embedding is produced.
	Dim() int
	EmbedDocs(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	Close() error
}

// DefaultOllamaModel scored best in the evaluation: R@5 0.96, 297 MB of RAM
// when loaded, 239 MB download.
const DefaultOllamaModel = "embeddinggemma:300m-qat-q4_0"

// New builds a provider by kind. model is only used by "ollama" (empty means
// DefaultOllamaModel); cacheDir only by "builtin" (empty means DefaultCacheDir).
func New(kind, model, cacheDir string) (Embedder, error) {
	switch kind {
	case "ollama":
		if model == "" {
			model = DefaultOllamaModel
		}
		return NewOllama("", model), nil
	case "builtin":
		return NewBuiltin(cacheDir)
	default:
		return nil, fmt.Errorf("embed: unknown provider %q (want ollama or builtin)", kind)
	}
}

// DefaultCacheDir is where downloaded models live: outside the vault, because
// models are large, per machine, and never synced.
func DefaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "membraid", "models")
}

func normalize(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	n := math.Sqrt(s)
	out := make([]float32, len(v))
	if n == 0 {
		return out
	}
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}
