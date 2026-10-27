package install

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shockalotti/membraid/assets"
)

// The plugin shipped v1-only and broke every OpenCode 2.x user with a "plugin
// must export a default definition" banner. These tests exist so that cannot
// happen quietly again.

func pluginAsset(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(assets.FS, "opencode/membraid.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPluginServesBothOpenCodeAPIs(t *testing.T) {
	src := pluginAsset(t)
	for _, want := range []string{
		"export default {",     // v2 reads this and nothing else
		`id: "membraid"`,       // required by the v2 definition schema
		"async function setup", // v2 entry point
		`hook("context"`,       // v2 replacement for the removed transform hook
		`server: Membraid`,     // v1 keeps its named-export entry point
		"experimental.chat.system.transform", // v1 hook map, still served
	} {
		if !strings.Contains(src, want) {
			t.Errorf("plugin template is missing %q - one of the two OpenCode APIs would break", want)
		}
	}
}

// The installer rewrites the first binary candidate with the real path. If the
// template's candidate list changes without updating the placeholder, the
// rewrite can miss and the plugin silently points at a path that does not
// exist. Exactly once is the only safe count.
func TestPluginPlaceholderAppearsExactlyOnce(t *testing.T) {
	if n := strings.Count(pluginAsset(t), opencodeBinPlaceholder); n != 1 {
		t.Fatalf("placeholder %q appears %d times in the plugin template, want exactly 1", opencodeBinPlaceholder, n)
	}
}

// Behaviour test: load the installed shape, register the v2 hook, fire it, and
// confirm a text part reaches the system array. The stub binary keeps the test
// hermetic - no vault, no real membraid.
func TestPluginV2HookInjectsDigestPart(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; the shape test still guards the export form")
	}

	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}

	stub := filepath.Join(dir, "membraid")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho DIGEST-STUB\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	plugin := filepath.Join(dir, "membraid.mjs")
	src, err := pointAt(opencodeBinPlaceholder, strconv.Quote(stub))(pluginAsset(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := filepath.Join(dir, "run.mjs")
	script := `
import plugin from "./membraid.mjs"
let captured = null
const ctx = {
  location: { directory: process.env.HOME },
  session: { hook: (name, cb) => { captured = { name, cb }; return Promise.resolve({ dispose() {} }) } },
}
await plugin.setup(ctx)
const input = { sessionID: "test-session", system: [] }
await captured.cb(input)
process.stdout.write(JSON.stringify({ hook: captured.name, system: input.system }))
`
	if err := os.WriteFile(runner, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, runner)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "MEMBRAID_BIN="+stub)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the plugin failed: %v\n%s", err, out)
	}

	var got struct {
		Hook   string `json:"hook"`
		System []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unexpected plugin output %q: %v", out, err)
	}
	if got.Hook != "context" {
		t.Errorf("v2 hook registered as %q, want %q", got.Hook, "context")
	}
	if len(got.System) != 1 || got.System[0].Type != "text" {
		t.Fatalf("want one text part in the system array, got %+v", got.System)
	}
	if !strings.HasPrefix(got.System[0].Text, "DIGEST-STUB") {
		t.Errorf("injected text does not carry the digest: %q", got.System[0].Text)
	}
	if !strings.Contains(got.System[0].Text, "Memory scan before you finish") {
		t.Error("the one-line write scan must ride the push")
	}
}
