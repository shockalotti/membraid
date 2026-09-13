package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeOllama answers /api/embed with [3,4,0] per input (so normalisation is
// visible) and /api/tags with two pulled models. It records every embed call.
type fakeOllama struct {
	mu    sync.Mutex
	calls [][]string
}

func (f *fakeOllama) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{
				{"name": "embeddinggemma:300m-qat-q4_0"}, {"name": "all-minilm:latest"},
			}})
		case "/api/embed":
			var req embedRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.Model == "missing" {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": `model "missing" not found, try pulling it first`})
				return
			}
			if req.KeepAlive != "5m" || !req.Truncate {
				t.Errorf("want keep_alive 5m and truncate, got %q %v", req.KeepAlive, req.Truncate)
			}
			f.mu.Lock()
			f.calls = append(f.calls, req.Input)
			f.mu.Unlock()
			out := make([][]float32, len(req.Input))
			for i := range out {
				out[i] = []float32{3, 4, 0}
			}
			json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOllamaAppliesPromptsBatchesAndNormalises(t *testing.T) {
	f := &fakeOllama{}
	srv := f.server(t)
	o := NewOllama(srv.URL, "embeddinggemma:300m-qat-q4_0")
	if o.Dim() != 0 {
		t.Errorf("dim must be unknown before the first call, got %d", o.Dim())
	}

	docs := make([]string, 70)
	for i := range docs {
		docs[i] = "memory"
	}
	vs, err := o.EmbedDocs(context.Background(), docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 70 || len(f.calls) != 3 || len(f.calls[0]) != 32 || len(f.calls[2]) != 6 {
		t.Errorf("want 70 vectors in batches of 32, 32, 6; got %d vectors, %d calls", len(vs), len(f.calls))
	}
	if f.calls[0][0] != "title: none | text: memory" {
		t.Errorf("document prompt not applied: %q", f.calls[0][0])
	}
	if math.Abs(float64(vs[0][0])-0.6) > 1e-6 || math.Abs(float64(vs[0][1])-0.8) > 1e-6 {
		t.Errorf("vectors must be L2-normalised, got %v", vs[0])
	}
	if o.Dim() != 3 {
		t.Errorf("dim must be discovered from the first response, got %d", o.Dim())
	}

	if _, err := o.EmbedQuery(context.Background(), "where does it deploy"); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[len(f.calls)-1][0]; got != "task: search result | query: where does it deploy" {
		t.Errorf("query prompt not applied: %q", got)
	}
	if o.Model() != "ollama:embeddinggemma:300m-qat-q4_0" || o.Name() != "ollama" {
		t.Errorf("identity wrong: %s %s", o.Name(), o.Model())
	}
}

func TestOllamaPromptsPerModel(t *testing.T) {
	for _, tc := range []struct{ model, query, doc string }{
		{"nomic-embed-text", "search_query: q", "search_document: d"},
		{"qwen3-embedding:0.6b", "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:q", "d"},
		{"all-minilm", "q", "d"},
	} {
		f := &fakeOllama{}
		o := NewOllama(f.server(t).URL, tc.model)
		o.EmbedQuery(context.Background(), "q")
		o.EmbedDocs(context.Background(), []string{"d"})
		if f.calls[0][0] != tc.query || f.calls[1][0] != tc.doc {
			t.Errorf("%s: got query %q doc %q", tc.model, f.calls[0][0], f.calls[1][0])
		}
	}
}

func TestOllamaErrorsSayWhatToDo(t *testing.T) {
	f := &fakeOllama{}
	_, err := NewOllama(f.server(t).URL, "missing").EmbedQuery(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "ollama pull missing") {
		t.Errorf("a missing model must say how to pull it, got %v", err)
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	_, err = NewOllama(url, DefaultOllamaModel).EmbedQuery(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "not reachable") || !strings.Contains(err.Error(), "ollama serve") {
		t.Errorf("a stopped server must say so, got %v", err)
	}
}

func TestOllamaAvailable(t *testing.T) {
	f := &fakeOllama{}
	url := f.server(t).URL
	ctx := context.Background()
	if !NewOllama(url, "embeddinggemma:300m-qat-q4_0").Available(ctx) {
		t.Error("a pulled model must be available")
	}
	if !NewOllama(url, "all-minilm").Available(ctx) {
		t.Error("a bare name must match name:latest")
	}
	if NewOllama(url, "qwen3-embedding:0.6b").Available(ctx) {
		t.Error("a model that is not pulled must not be available")
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	if NewOllama(deadURL, DefaultOllamaModel).Available(ctx) {
		t.Error("a stopped server must not be available")
	}
}

func TestOllamaURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "http://localhost:11434",
		"0.0.0.0:11434":     "http://localhost:11434",
		"myhost":            "http://myhost:11434",
		"127.0.0.1:9999":    "http://127.0.0.1:9999",
		"https://gpu:8443/": "https://gpu:8443",
	} {
		if got := ollamaURL(in); got != want {
			t.Errorf("ollamaURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewRejectsUnknownProvider(t *testing.T) {
	if _, err := New("openai", "", ""); err == nil {
		t.Error("only local providers exist")
	}
	e, err := New("ollama", "", "")
	if err != nil || e.Model() != "ollama:"+DefaultOllamaModel {
		t.Errorf("ollama with no model must use the default, got %v %v", e, err)
	}
}
