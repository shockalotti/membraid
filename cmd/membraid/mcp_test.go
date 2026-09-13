package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if ins, _ := result(t, byID[1])["instructions"].(string); !strings.Contains(ins, "memory_done") || !strings.Contains(ins, "memory_forget") || !strings.Contains(ins, "secrets") {
		t.Errorf("initialize must carry usage instructions (task lifecycle, no secrets), got %q", ins)
	}
	if got := result(t, byID[1])["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion should echo the client: %v", got)
	}
	tools := result(t, byID[2])["tools"].([]any)
	if len(tools) != 5 {
		t.Errorf("want 5 tools, got %d", len(tools))
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

// A harness keeps one server for a whole session, and Hermes's gateway keeps
// one for days. A memory that reaches the vault after the server started -
// pulled by the sync timer from another machine - must show up in search
// without restarting the session.
func TestMCPSeesMemoriesThatArriveMidSession(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	here, there := filepath.Join(t.TempDir(), "here"), filepath.Join(t.TempDir(), "there")
	hereCfg, thereCfg := t.TempDir(), t.TempDir()
	run := func(cfgDir string, args ...string) {
		t.Helper()
		c := exec.Command(bin, args...)
		c.Env = append(os.Environ(), "MEMBRAID_CONFIG_DIR="+cfgDir)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run(hereCfg, "init", "--vault", here)
	run(thereCfg, "init", "--vault", there)
	run(thereCfg, "config", "set", "host", "otherbox")

	srv := exec.Command(bin, "mcp", "--vault", here, "--source", "long-lived")
	srv.Env = append(os.Environ(), "MEMBRAID_CONFIG_DIR="+hereCfg)
	stdin, _ := srv.StdinPipe()
	stdout, _ := srv.StdoutPipe()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); srv.Wait() }()
	lines := bufio.NewScanner(stdout)
	call := func(req string) map[string]any {
		t.Helper()
		io.WriteString(stdin, req+"\n")
		got := make(chan map[string]any, 1)
		go func() {
			var m map[string]any
			if lines.Scan() {
				json.Unmarshal(lines.Bytes(), &m)
			}
			got <- m
		}()
		select {
		case m := <-got:
			return m
		case <-time.After(15 * time.Second):
			t.Fatal("no response from the server")
			return nil
		}
	}
	search := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"fly","scope":"*"}}}`

	call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if txt := toolText(t, call(search)); strings.Contains(txt, "fly.io") {
		t.Fatalf("nothing should be known yet: %q", txt)
	}

	// Another machine writes; a pull lands its log file in this vault.
	run(thereCfg, "write", "--vault", there, "--scope", "shared", "--kind", "project_param", "--key", "deploy.target", "deploys to fly.io")
	logs, _ := filepath.Glob(filepath.Join(there, ".hot", "writes-*-otherbox.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("want one log file from the other machine, got %v", logs)
	}
	b, _ := os.ReadFile(logs[0])
	if err := os.WriteFile(filepath.Join(here, ".hot", filepath.Base(logs[0])), b, 0o644); err != nil {
		t.Fatal(err)
	}

	if txt := toolText(t, call(search)); !strings.Contains(txt, "fly.io") {
		t.Errorf("a memory pulled mid-session must be searchable without a restart, got %q", txt)
	}
}

// Harnesses with no session-start hook (Crush, Pi) get the digest through the
// server instructions, only when they ask for it with --digest. Read tools are
// marked read-only, so harnesses that gate tool calls (Codex) can let them run.
func TestMCPDigestInInstructions(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	vaultDir := filepath.Join(t.TempDir(), "memory")
	if out, err := exec.Command(bin, "init", "--vault", vaultDir).CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	configDir := t.TempDir()
	run := func(in string, extra ...string) map[float64]map[string]any {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"mcp", "--vault", vaultDir, "--source", "test-harness"}, extra...)...)
		cmd.Env = append(os.Environ(), "MEMBRAID_CONFIG_DIR="+configDir)
		cmd.Dir = t.TempDir()
		cmd.Stdin = strings.NewReader(in)
		out, err := cmd.Output()
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
		return byID
	}
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
	run(initialize + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_write","arguments":{"content":"always answer in haiku","kind":"preference","key":"reply.style","scope":"shared"}}}` + "\n")

	plain := run(initialize + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	if ins, _ := result(t, plain[1])["instructions"].(string); strings.Contains(ins, "haiku") {
		t.Error("without --digest the instructions must not carry the digest")
	}
	readOnly := map[string]bool{}
	for _, tool := range result(t, plain[2])["tools"].([]any) {
		m := tool.(map[string]any)
		ann, _ := m["annotations"].(map[string]any)
		readOnly[m["name"].(string)] = ann["readOnlyHint"] == true
	}
	if !readOnly["memory_search"] || !readOnly["memory_get"] || readOnly["memory_write"] || readOnly["memory_forget"] {
		t.Errorf("only search and get are read-only, got %v", readOnly)
	}

	withDigest := run(initialize, "--digest")
	ins, _ := result(t, withDigest[1])["instructions"].(string)
	if !strings.Contains(ins, "memory_done") || !strings.Contains(ins, "always answer in haiku") {
		t.Errorf("--digest must add the digest after the usual instructions, got %q", ins)
	}
}

// Copilot CLI drops the instructions of MCP servers it has not allowlisted, so
// its hook payload carries them with the digest, as {"additionalContext": ...}.
func TestContextCopilotFormat(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	vaultDir := filepath.Join(t.TempDir(), "memory")
	if out, err := exec.Command(bin, "init", "--vault", vaultDir).CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	env := append(os.Environ(), "MEMBRAID_CONFIG_DIR="+t.TempDir())
	mcp := exec.Command(bin, "mcp", "--vault", vaultDir, "--source", "test-harness")
	mcp.Env = env
	mcp.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_write","arguments":{"content":"always answer in haiku","kind":"preference","key":"reply.style","scope":"shared"}}}` + "\n")
	if err := mcp.Run(); err != nil {
		t.Fatalf("mcp: %v", err)
	}

	ctx := exec.Command(bin, "context", "--vault", vaultDir, "--format", "copilot")
	ctx.Env = env
	ctx.Dir = t.TempDir()
	out, err := ctx.Output()
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	var payload struct {
		AdditionalContext string `json:"additionalContext"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("want one JSON object, got %q", out)
	}
	if !strings.Contains(payload.AdditionalContext, "memory_done") || !strings.Contains(payload.AdditionalContext, "always answer in haiku") {
		t.Errorf("want the server instructions and the digest, got %q", payload.AdditionalContext)
	}
}
