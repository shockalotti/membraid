package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ollamaBatch keeps each request small enough that one slow batch does not
// stall a long import.
const ollamaBatch = 32

// Ollama embeds through a local Ollama server. The model loads on demand and
// unloads after keep_alive, so an idle server costs little memory.
type Ollama struct {
	base, model string
	client      *http.Client

	mu  sync.Mutex
	dim int
}

// NewOllama returns a provider for model at base. An empty base means
// OLLAMA_HOST, or http://localhost:11434.
func NewOllama(base, model string) *Ollama {
	if base == "" {
		base = os.Getenv("OLLAMA_HOST")
	}
	return &Ollama{base: ollamaURL(base), model: model, client: &http.Client{Timeout: 5 * time.Minute}}
}

// ollamaURL accepts what OLLAMA_HOST may hold: empty, host, host:port, or a URL.
func ollamaURL(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "http://localhost:11434"
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return "http://localhost:11434"
	}
	h, port := u.Hostname(), u.Port()
	if h == "" || h == "0.0.0.0" {
		h = "localhost"
	}
	if port == "" {
		port = "11434"
	}
	u.Host = net.JoinHostPort(h, port)
	return strings.TrimRight(u.String(), "/")
}

func (o *Ollama) Name() string  { return "ollama" }
func (o *Ollama) Model() string { return "ollama:" + o.model }
func (o *Ollama) Close() error  { return nil }

func (o *Ollama) Dim() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.dim
}

// taskPrompts are the retrieval prompts from each model's official card. Ollama's
// /api/embed applies none of them, so they are prepended here.
//
//	embeddinggemma: https://huggingface.co/google/embeddinggemma-300m
//	qwen3-embedding: https://huggingface.co/Qwen/Qwen3-Embedding-0.6B (documents get no instruction)
//	nomic-embed-text: https://huggingface.co/nomic-ai/nomic-embed-text-v1.5
//	bge-m3 and all-minilm take no prompts.
func taskPrompts(model string) (query, doc string) {
	switch {
	case strings.HasPrefix(model, "embeddinggemma"):
		return "task: search result | query: ", "title: none | text: "
	case strings.HasPrefix(model, "qwen3-embedding"):
		return "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:", ""
	case strings.HasPrefix(model, "nomic-embed-text"):
		return "search_query: ", "search_document: "
	}
	return "", ""
}

func (o *Ollama) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	_, prompt := taskPrompts(o.model)
	var out [][]float32
	for i := 0; i < len(texts); i += ollamaBatch {
		v, err := o.embed(ctx, prefixed(prompt, texts[i:min(i+ollamaBatch, len(texts))]))
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

func (o *Ollama) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	prompt, _ := taskPrompts(o.model)
	v, err := o.embed(ctx, []string{prompt + text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

func prefixed(prompt string, texts []string) []string {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = prompt + t
	}
	return out
}

type embedRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	KeepAlive string   `json:"keep_alive"`
	Truncate  bool     `json:"truncate"`
}

func (o *Ollama) embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(embedRequest{Model: o.model, Input: texts, KeepAlive: "5m", Truncate: true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, o.unreachable(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if resp.StatusCode == http.StatusNotFound || strings.Contains(msg, "not found") {
			return nil, fmt.Errorf("embed: ollama does not have model %s; run: ollama pull %s", o.model, o.model)
		}
		return nil, fmt.Errorf("embed: ollama %s: %s", resp.Status, msg)
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("embed: ollama response: %w", err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: ollama returned %d embeddings for %d inputs", len(out.Embeddings), len(texts))
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, v := range out.Embeddings {
		if o.dim == 0 {
			o.dim = len(v)
		}
		if len(v) != o.dim {
			return nil, fmt.Errorf("embed: ollama returned a %d-dimension vector, expected %d", len(v), o.dim)
		}
		out.Embeddings[i] = normalize(v)
	}
	return out.Embeddings, nil
}

func (o *Ollama) unreachable(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("embed: ollama is not reachable at %s (is it running? start it with: ollama serve): %w", o.base, err)
}

// Available reports whether the server answers and has the model pulled. It
// waits at most 1.5 seconds, so callers can probe before choosing a provider.
func (o *Ollama) Available(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) != nil {
		return false
	}
	for _, m := range tags.Models {
		if sameModel(m.Name, o.model) || sameModel(m.Model, o.model) {
			return true
		}
	}
	return false
}

// sameModel compares Ollama tags, treating a bare name as name:latest.
func sameModel(a, b string) bool {
	norm := func(s string) string {
		if s != "" && !strings.Contains(s, ":") {
			return s + ":latest"
		}
		return s
	}
	return a != "" && norm(a) == norm(b)
}
