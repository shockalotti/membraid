// Package install sets membraid up inside each agent harness the user runs:
// the MCP server, the session digest, and the agent skill.
//
// Every step is idempotent - running install again changes nothing that is
// already right - and every JSON config it edits is backed up first. Harness
// configs belong to the user, not to membraid: steps add or migrate membraid's
// own entries and leave everything else as they found it.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/shockalotti/membraid/assets"
)

// ServerName is what every harness calls the MCP server. Not "memory": Hermes
// has a built-in tool named memory, and Claude Code and Grok have memory
// features of their own, so an agent shown two things called memory can write
// to the wrong one. Harnesses show the tools as membraid's.
const ServerName = "membraid"

// legacyName is the server name membraid used before the rename. An entry under
// it that runs membraid is migrated; anything else of that name is left alone.
const legacyName = "memory"

// Env is where install acts. Tests substitute Home, Run and LookPath.
type Env struct {
	Home     string
	Bin      string // absolute path every harness config will point at
	DryRun   bool
	Out      io.Writer
	Run      func(stdin, name string, args ...string) (string, error)
	LookPath func(string) (string, error)
	// OllamaUp reports whether a local Ollama server answers. Nil means no.
	OllamaUp func() bool
}

func DefaultEnv(bin string, out io.Writer) (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Env{Home: home, Bin: bin, Out: out, Run: runCommand, LookPath: exec.LookPath, OllamaUp: ollamaUp}, nil
}

// ollamaUp probes the local Ollama API with a short timeout, so install never
// waits on a server that is not there.
func ollamaUp() bool {
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get("http://localhost:11434/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func runCommand(stdin, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// Target is one thing install can set up.
type Target struct {
	ID     string
	Name   string
	Detect func(*Env) bool
	Notes  []string
	Steps  func(*Env) []Step
}

type Step struct {
	Desc  string
	Apply func(*Env) error
}

// Apply describes each step and, unless this is a dry run, performs it.
func Apply(e *Env, t Target) error {
	for _, s := range t.Steps(e) {
		fmt.Fprintf(e.Out, "  - %s\n", s.Desc)
		if e.DryRun {
			continue
		}
		if err := s.Apply(e); err != nil {
			return fmt.Errorf("%s: %w", s.Desc, err)
		}
	}
	return nil
}

func (e *Env) path(parts ...string) string {
	return filepath.Join(append([]string{e.Home}, parts...)...)
}

// has reports whether a harness is present: its binary on PATH, or its config
// directory in the home directory.
func (e *Env) has(binary string, dirs ...string) bool {
	if _, err := e.LookPath(binary); err == nil {
		return true
	}
	for _, d := range dirs {
		if fi, err := os.Stat(e.path(d)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// editJSON loads a JSON config, lets fn change it, and writes it back only if
// fn reports a change - after copying the original to <file>.membraid.bak.
//
// Numbers are kept as their literal text and HTML characters are not escaped:
// a plain decode would turn Claude Code's millisecond timestamps into 1.7e+12
// and every < in its stored history into <.
func editJSON(path string, fn func(doc map[string]any) bool) error {
	raw, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	doc := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return fmt.Errorf("%s is not plain JSON, so it was left untouched: %w", path, err)
		}
	}
	if !fn(doc) {
		return nil
	}
	mode := os.FileMode(0o600)
	if readErr == nil {
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(path+".membraid.bak", raw, 0o600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), mode)
}

// writeAsset copies an embedded file to dest, optionally rewriting it, and
// skips the write when dest already holds exactly that content.
func writeAsset(asset, dest string, rewrite func(string) (string, error)) error {
	b, err := fs.ReadFile(assets.FS, asset)
	if err != nil {
		return err
	}
	content := string(b)
	if rewrite != nil {
		if content, err = rewrite(content); err != nil {
			return err
		}
	}
	if cur, err := os.ReadFile(dest); err == nil && string(cur) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, []byte(content), 0o644)
}

// pointAt replaces an asset's default binary location with the installed one.
// It fails loudly if the placeholder is gone, because silently installing a
// plugin that looks for membraid in the wrong place is the worse outcome.
func pointAt(placeholder, value string) func(string) (string, error) {
	return func(s string) (string, error) {
		if !strings.Contains(s, placeholder) {
			return "", fmt.Errorf("asset no longer contains %q: installer and asset have drifted", placeholder)
		}
		return strings.ReplaceAll(s, placeholder, value), nil
	}
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// ours reports whether a config entry runs membraid.
func ours(entry any) bool {
	b, _ := json.Marshal(entry)
	s := string(b)
	return strings.Contains(s, "membraid") || strings.Contains(s, "memory-engine")
}

// migrateLegacy removes a pre-rename server entry, but only one that runs
// membraid: another tool's "memory" server is none of install's business.
func migrateLegacy(servers map[string]any) bool {
	if entry, ok := servers[legacyName]; ok && ours(entry) {
		delete(servers, legacyName)
		return true
	}
	return false
}

func skillStep(rel string) Step {
	return Step{
		Desc:  "agent skill at ~/" + filepath.ToSlash(rel),
		Apply: func(e *Env) error { return writeAsset("skill/SKILL.md", e.path(rel), nil) },
	}
}

func readYAMLMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}
