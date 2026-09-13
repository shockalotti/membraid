//go:build nobuiltin

package embed

import "errors"

// NewBuiltin is unavailable in binaries built with -tags nobuiltin, which leave
// the in-process model out to keep the binary small. Ollama still works.
func NewBuiltin(cacheDir string) (Embedder, error) {
	return nil, errors.New("embed: this membraid was built without the built-in model (-tags nobuiltin); use the ollama provider")
}
