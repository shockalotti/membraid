package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/embed"
	"github.com/shockalotti/membraid/internal/index"
)

// Search by meaning is optional (docs/SEARCH-EVALUATION.md). With embeddings
// off, everything here reduces to keyword search. With them on, memories are
// embedded locally and search is vector-only, falling back to keyword search
// whenever the embedder cannot answer, so a stopped Ollama never breaks search.

// searchTimeout bounds embedding a query, including a cold model load.
const searchTimeout = 30 * time.Second

// newEmbedder builds the configured embedder, or nil when embeddings are off.
// A variable so tests can substitute a fake.
var newEmbedder = func(cfg config.Config) (embed.Embedder, error) {
	if !cfg.EmbeddingsOn() {
		return nil, nil
	}
	return embed.New(cfg.Embeddings, cfg.EmbedModel, embed.DefaultCacheDir())
}

// embedPending embeds current memories that have no vector for the embedder's
// model, newest first, in batches. max <= 0 means all of them. It returns how
// many were stored, even when it stops on an error.
func embedPending(ctx context.Context, e embed.Embedder, ix *index.Index, max int) (int, error) {
	const batch = 32
	done := 0
	for max <= 0 || done < max {
		n := batch
		if max > 0 && max-done < n {
			n = max - done
		}
		pending, err := ix.MissingVectors(e.Model(), n)
		if err != nil || len(pending) == 0 {
			return done, err
		}
		texts := make([]string, len(pending))
		for i, p := range pending {
			texts[i] = p.Content
		}
		vecs, err := e.EmbedDocs(ctx, texts)
		if err != nil {
			return done, err
		}
		if len(vecs) != len(pending) {
			return done, fmt.Errorf("embedder returned %d vectors for %d memories", len(vecs), len(pending))
		}
		for i, p := range pending {
			if err := ix.PutVector(p.ID, e.Model(), vecs[i]); err != nil {
				return done, err
			}
		}
		done += len(pending)
		if len(pending) < n {
			return done, nil
		}
	}
	return done, nil
}

// searchMemories is the one search path for the CLI and MCP. mode reports what
// ran: "vector", or "keyword" (embeddings off, nothing embedded yet, or the
// embedder unavailable, in which case a note goes to stderr).
func searchMemories(ctx context.Context, e embed.Embedder, ix *index.Index, q, sc string, limit int) ([]index.Hit, string, error) {
	keyword := func() ([]index.Hit, string, error) {
		hits, err := ix.Search(q, sc, limit)
		return hits, "keyword", err
	}
	if e == nil {
		return keyword()
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	// A memory written moments ago should be findable now, not after the next
	// background pass: embed a small backlog first.
	if _, err := embedPending(ctx, e, ix, 64); err != nil {
		fmt.Fprintf(os.Stderr, "membraid: embeddings unavailable (%v); used keyword search\n", err)
		return keyword()
	}
	have, total, err := ix.VectorCoverage(e.Model())
	if err != nil || have == 0 {
		return keyword()
	}
	qv, err := e.EmbedQuery(ctx, q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "membraid: embeddings unavailable (%v); used keyword search\n", err)
		return keyword()
	}
	hits, err := ix.VectorSearch(qv, e.Model(), sc, limit)
	if err != nil || have >= total {
		return hits, "vector", err
	}
	// Some memories have no vector yet (a large import, a slow embedder), and
	// vector search cannot see them. Keyword matches among those come after
	// the vector results rather than staying invisible until embedding catches up.
	return withUnembedded(ix, e.Model(), hits, q, sc, limit)
}

func withUnembedded(ix *index.Index, model string, hits []index.Hit, q, sc string, limit int) ([]index.Hit, string, error) {
	kw, err := ix.Search(q, sc, limit)
	if err != nil || len(kw) == 0 {
		return hits, "vector", nil
	}
	ids := make([]string, len(kw))
	for i, h := range kw {
		ids[i] = h.ID
	}
	missing, err := ix.Unembedded(ids, model)
	if err != nil {
		return hits, "vector", nil
	}
	seen := map[string]bool{}
	for _, h := range hits {
		seen[h.ID] = true
	}
	added := false
	for _, h := range kw {
		if missing[h.ID] && !seen[h.ID] {
			hits = append(hits, h)
			added = true
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if added {
		return hits, "vector+keyword", nil
	}
	return hits, "vector", nil
}

// embedAfterSync embeds any backlog after a CLI or scheduled sync, so memories
// written by plain CLI calls or pulled from another machine become searchable
// by meaning without waiting for an agent session. Bounded, and never fails the
// sync: a stopped Ollama only postpones it.
func embedAfterSync(cfg config.Config, ix *index.Index, quiet bool) {
	e, err := newEmbedder(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "membraid: embeddings:", err)
		return
	}
	if e == nil {
		return
	}
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	n, err := embedPending(ctx, e, ix, 500)
	if err != nil {
		fmt.Fprintf(os.Stderr, "membraid: embedded %d memories, then: %v\n", n, err)
		return
	}
	if n > 0 && !quiet {
		fmt.Printf("embedded %d memor%s\n", n, map[bool]string{true: "y", false: "ies"}[n == 1])
	}
}

// searchStatus describes how search runs on this machine, for status output.
func searchStatus(cfg config.Config, ix *index.Index) map[string]any {
	out := map[string]any{"mode": "keyword"}
	e, err := newEmbedder(cfg)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	if e == nil {
		return out
	}
	defer e.Close()
	have, total, _ := ix.VectorCoverage(e.Model())
	out["mode"], out["model"], out["embedded"], out["current"] = "vector", e.Model(), have, total
	return out
}

// lockedEmbedder serialises calls to an embedder shared by goroutines: an MCP
// server embeds in the background while it answers searches, and whether the
// built-in model is safe to call concurrently is not established.
type lockedEmbedder struct {
	mu sync.Mutex
	e  embed.Embedder
}

func (l *lockedEmbedder) Name() string  { return l.e.Name() }
func (l *lockedEmbedder) Model() string { return l.e.Model() }
func (l *lockedEmbedder) Dim() int      { return l.e.Dim() }
func (l *lockedEmbedder) Close() error  { return l.e.Close() }

func (l *lockedEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.e.EmbedDocs(ctx, texts)
}

func (l *lockedEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.e.EmbedQuery(ctx, text)
}
