package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Drives the built binary the way a harness does: newline-delimited JSON-RPC
// on stdin, one JSON message per line on stdout, diagnostics on stderr.
func TestMCPStdioRoundTrip(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	vaultDir := filepath.Join(t.TempDir(), "memory")
	if out, err := exec.Command(bin, "init", "--vault", vaultDir).CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_write","arguments":{"content":"uses pnpm","kind":"preference","key":"pkg.manager","scope":"shared"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"memory_write","arguments":{"content":"uses bun","kind":"preference","key":"pkg.manager","scope":"shared"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"memory_get","arguments":{"key":"pkg.manager","scope":"shared"}}}`,
	}, "\n") + "\n"

	cmd := exec.Command(bin, "mcp", "--vault", vaultDir, "--source", "test-harness")
	// Never touch the developer's real settings or sync state.
	cmd.Env = append(os.Environ(), "MEMBRAID_CONFIG_DIR="+t.TempDir())
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.Output() // Output() keeps stderr separate: stdout must be pure protocol
	if err != nil {
		t.Fatalf("mcp: %v", err)
	}

	byID := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout must be pure JSON-RPC, got %q", line)
		}
		if id, ok := m["id"].(float64); ok {
			byID[id] = m
		}
	}

	// Five requests carry an id; the notification must draw no response at all.
	if len(byID) != 5 {
		t.Errorf("want 5 responses for the 5 id-bearing requests, got %d", len(byID))
	}
	if got := result(t, byID[1])["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion should echo the client: %v", got)
	}
	tools := result(t, byID[2])["tools"].([]any)
	if len(tools) != 4 {
		t.Errorf("want 4 tools, got %d", len(tools))
	}
	if txt := toolText(t, byID[4]); !strings.Contains(txt, "replaced 1 earlier answer") {
		t.Errorf("second write must supersede the first: %q", txt)
	}
	if txt := toolText(t, byID[5]); !strings.Contains(txt, "uses bun") || strings.Contains(txt, "pnpm") {
		t.Errorf("one live answer expected, got %q", txt)
	}
}

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", m)
	}
	return r
}

func toolText(t *testing.T, m map[string]any) string {
	t.Helper()
	c := result(t, m)["content"].([]any)
	return c[0].(map[string]any)["text"].(string)
}
