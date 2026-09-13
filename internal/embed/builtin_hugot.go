//go:build !nobuiltin

package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
	"github.com/knights-analytics/hugot/util/fileutil"
)

// builtinRepo is all-MiniLM-L6-v2: R@5 0.91 in the evaluation, 91 MB download,
// run by hugot's pure Go backend so the binary needs no CGO and no runtime.
const (
	builtinRepo  = "sentence-transformers/all-MiniLM-L6-v2"
	builtinOnnx  = "onnx/model.onnx"
	builtinName  = "all-MiniLM-L6-v2"
	builtinDim   = 384
	builtinBatch = 32
)

// Builtin embeds in-process. It downloads and loads the model on first use, not
// when constructed, so a command that never embeds pays nothing.
type Builtin struct {
	cacheDir string

	mu      sync.Mutex
	session *hugot.Session
	pipe    *pipelines.FeatureExtractionPipeline
}

// NewBuiltin returns the built-in provider. An empty cacheDir means
// DefaultCacheDir.
func NewBuiltin(cacheDir string) (Embedder, error) {
	if cacheDir == "" {
		cacheDir = DefaultCacheDir()
	}
	return &Builtin{cacheDir: cacheDir}, nil
}

func (b *Builtin) Name() string  { return "builtin" }
func (b *Builtin) Model() string { return "builtin:" + builtinName }
func (b *Builtin) Dim() int      { return builtinDim }

func (b *Builtin) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	var out [][]float32
	for i := 0; i < len(texts); i += builtinBatch {
		v, err := b.run(ctx, texts[i:min(i+builtinBatch, len(texts))])
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

func (b *Builtin) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	v, err := b.run(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

func (b *Builtin) run(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.loadLocked(ctx); err != nil {
		return nil, err
	}
	res, err := b.pipe.RunPipeline(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed: builtin model: %w", err)
	}
	if len(res.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: builtin model returned %d embeddings for %d inputs", len(res.Embeddings), len(texts))
	}
	for i := range res.Embeddings {
		res.Embeddings[i] = normalize(res.Embeddings[i])
	}
	return res.Embeddings, nil
}

func (b *Builtin) loadLocked(ctx context.Context) error {
	if b.pipe != nil {
		return nil
	}
	path, err := b.modelPath(ctx)
	if err != nil {
		return err
	}
	s, err := hugot.NewGoSession(ctx)
	if err != nil {
		return fmt.Errorf("embed: builtin session: %w", err)
	}
	pipe, err := hugot.NewPipeline(s, hugot.FeatureExtractionConfig{
		ModelPath:    path,
		Name:         builtinName,
		OnnxFilename: onnxFile(path),
		Options:      []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
	})
	if err != nil {
		s.Destroy()
		return fmt.Errorf("embed: builtin model %s: %w", path, err)
	}
	b.session, b.pipe = s, pipe
	return nil
}

// modelPath returns the local model directory, downloading it once if needed.
func (b *Builtin) modelPath(ctx context.Context) (string, error) {
	dir := filepath.Join(b.cacheDir, strings.ReplaceAll(builtinRepo, "/", "_"))
	if onnxFile(dir) != "" {
		return dir, nil
	}
	// hugot copies into <cacheDir>/<repo with / as _> without creating it, and
	// needs a filesystem bound to the context (nil means the OS).
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("embed: model cache: %w", err)
	}
	opts := hugot.NewDownloadOptions()
	opts.OnnxFilePath = builtinOnnx
	path, err := hugot.DownloadModel(fileutil.WithFileSystem(ctx, nil), builtinRepo, b.cacheDir, opts)
	if err != nil {
		if onnxFile(dir) != "" {
			return dir, nil // DownloadModel refuses to overwrite an existing download
		}
		return "", fmt.Errorf("embed: download %s: %w", builtinRepo, err)
	}
	return path, nil
}

// onnxFile names the model file inside dir, wherever the download put it, or ""
// if there is none yet.
func onnxFile(dir string) string {
	for _, name := range []string{"model.onnx", builtinOnnx} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.Size() > 0 {
			return name
		}
	}
	return ""
}

func (b *Builtin) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session != nil {
		err := b.session.Destroy()
		b.session, b.pipe = nil, nil
		return err
	}
	return nil
}
