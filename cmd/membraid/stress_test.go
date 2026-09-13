package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// command is machine.run without the test failure, for use off the test
// goroutine where t.Fatal is not allowed.
func (m machine) command(args ...string) *exec.Cmd {
	cmd := exec.Command(m.bin, args...)
	cmd.Env = append(os.Environ(), "MEMBRAID_VAULT="+m.vault, "MEMBRAID_CONFIG_DIR="+m.cfg,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	return cmd
}

// mcpClient drives one long-lived `membraid mcp` process the way a harness
// does: one request at a time over stdin, one response line on stdout.
type mcpClient struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Scanner
	stderr *bytes.Buffer
	id     int
}

func startMCP(m machine, source string) (*mcpClient, error) {
	cmd := m.command("mcp", "--source", source)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &mcpClient{cmd: cmd, in: in, out: bufio.NewScanner(out), stderr: &bytes.Buffer{}}
	c.out.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if _, err := c.call("initialize", map[string]any{"protocolVersion": "2025-06-18"}); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *mcpClient) call(method string, params any) (map[string]any, error) {
	c.id++
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	if _, err := c.in.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("%s: write: %v", method, err)
	}
	if !c.out.Scan() {
		return nil, fmt.Errorf("%s: server closed its output; stderr:\n%s", method, c.stderr.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(c.out.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("%s: stdout is not JSON-RPC: %q", method, c.out.Text())
	}
	if e, ok := resp["error"]; ok {
		return nil, fmt.Errorf("%s: %v", method, e)
	}
	return resp, nil
}

func (c *mcpClient) tool(name string, args map[string]any) (string, error) {
	resp, err := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	res, _ := resp["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return "", fmt.Errorf("%s: no content in %v", name, resp)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if isErr, _ := res["isError"].(bool); isErr {
		return "", fmt.Errorf("%s: %s", name, text)
	}
	return text, nil
}

// close ends the session. The server flushes pending writes with a sync on
// the way out, so closing four at once is itself a sync race.
func (c *mcpClient) close() error {
	c.in.Close()
	return c.cmd.Wait()
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func sortedLines(s string) string {
	l := nonEmptyLines(s)
	sort.Strings(l)
	return strings.Join(l, "\n")
}

// The design assumes nothing about how many agents are open. In practice one
// machine runs Claude Code, OpenCode and two Hermes services at once, plus CLI
// calls and the sync timer, all against one vault. This drives that shape much
// harder than daily use and checks that nothing is lost, torn or duplicated:
// long-lived MCP servers and short CLI processes write concurrently, half of
// every writer's writes race on one key, searches run throughout, and syncs
// overlap the lot. Then the index is rebuilt from the log alone, and a second
// machine clones the remote; both must agree with the original exactly.
func TestStressManyClientsOneVault(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test: run without -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	a := machine{t, bin, filepath.Join(root, "a", "vault"), filepath.Join(root, "a", "cfg")}
	a.run("init")
	a.run("config", "set", "host", "stressbox")
	a.run("config", "set", "push_delay_sec", "1")
	git(t, a.vault, "init", "-q", "-b", "main")
	git(t, a.vault, "add", "-A")
	git(t, a.vault, "commit", "-q", "-m", "vault")
	git(t, a.vault, "remote", "add", "origin", remote)
	git(t, a.vault, "push", "-q", "-u", "origin", "main")

	const mcpWriters, cliWriters, perWriter = 4, 4, 50
	const sameKey = "stress.same"
	memory := func(w, n int) (content, key string) {
		if n%2 == 0 {
			return fmt.Sprintf("same answer from writer %d write %d", w, n), sameKey
		}
		return fmt.Sprintf("note from writer %d write %d", w, n), ""
	}

	errs := make(chan error, 4096)
	stop := make(chan struct{})
	var writers, background sync.WaitGroup

	for w := 0; w < mcpWriters; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			c, err := startMCP(a, fmt.Sprintf("mcp-writer-%d", w))
			if err != nil {
				errs <- fmt.Errorf("mcp writer %d: start: %v", w, err)
				return
			}
			for n := 0; n < perWriter; n++ {
				content, key := memory(w, n)
				args := map[string]any{"content": content, "kind": "insight", "scope": "shared"}
				if key != "" {
					args["key"] = key
				}
				if _, err := c.tool("memory_write", args); err != nil {
					errs <- fmt.Errorf("mcp writer %d write %d: %v", w, n, err)
				}
			}
			if err := c.close(); err != nil {
				errs <- fmt.Errorf("mcp writer %d: exit: %v; stderr:\n%s", w, err, c.stderr.String())
			}
		}(w)
	}
	for w := mcpWriters; w < mcpWriters+cliWriters; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for n := 0; n < perWriter; n++ {
				content, key := memory(w, n)
				args := []string{"write", content, "--kind", "insight", "--scope", "shared", "--source", fmt.Sprintf("cli-writer-%d", w)}
				if key != "" {
					args = append(args, "--key", key)
				}
				if out, err := a.command(args...).CombinedOutput(); err != nil {
					errs <- fmt.Errorf("cli writer %d write %d: %v: %s", w, n, err, out)
				}
			}
		}(w)
	}
	for s := 0; s < 2; s++ {
		background.Add(1)
		go func(s int) {
			defer background.Done()
			c, err := startMCP(a, fmt.Sprintf("mcp-searcher-%d", s))
			if err != nil {
				errs <- fmt.Errorf("searcher %d: start: %v", s, err)
				return
			}
			defer c.close()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := c.tool("memory_search", map[string]any{"query": "note", "scope": "shared", "limit": 5}); err != nil {
					errs <- fmt.Errorf("searcher %d: %v", s, err)
					return
				}
			}
		}(s)
	}
	background.Add(1)
	go func() {
		defer background.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if out, err := a.command("sync", "--json").CombinedOutput(); err != nil {
				errs <- fmt.Errorf("sync during writes: %v: %s", err, out)
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() { writers.Wait(); close(stop); background.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		t.Fatal("clients did not finish within 5 minutes: something is deadlocked or starved")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	// Settle: one sync that actually runs, not one skipped because an exiting
	// server still holds the lock.
	settled := false
	for i := 0; i < 20 && !settled; i++ {
		out := a.run("sync", "--json")
		settled = !strings.Contains(out, `"skipped"`)
		if !settled {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if !settled {
		t.Fatal("sync never got the lock after the clients exited")
	}

	total := (mcpWriters + cliWriters) * perWriter
	half := total / 2

	// Every line whole, every write logged exactly once.
	logs, _ := filepath.Glob(filepath.Join(a.vault, ".hot", "writes-*.jsonl"))
	writes := 0
	for _, f := range logs {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range nonEmptyLines(string(b)) {
			if !json.Valid([]byte(line)) {
				t.Fatalf("%s line %d is torn or interleaved: %q", filepath.Base(f), i+1, line)
			}
			if strings.Contains(line, `"t":"write"`) {
				writes++
			}
		}
	}
	if writes != total {
		t.Errorf("want %d write lines in the log, got %d", total, writes)
	}

	notes := a.run("search", "note", "--scope", "shared", "-n", "10000")
	history := a.run("history", sameKey, "--scope", "shared")
	if n := len(nonEmptyLines(notes)); n != half {
		t.Errorf("want %d current unkeyed memories, got %d", half, n)
	}
	if n := len(nonEmptyLines(history)); n != half {
		t.Errorf("want %d rows of history for %s, got %d", half, sameKey, n)
	}
	if n := strings.Count(history, "->"); n != 1 {
		t.Errorf("%d writers racing on one key must leave exactly one current answer, got %d", mcpWriters+cliWriters, n)
	}

	// The log alone must rebuild the same index.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(filepath.Join(a.vault, ".hot", "index.db"+suffix))
	}
	if got := a.run("search", "note", "--scope", "shared", "-n", "10000"); sortedLines(got) != sortedLines(notes) {
		t.Error("an index rebuilt from the log disagrees with the live index about unkeyed memories")
	}
	if got := a.run("history", sameKey, "--scope", "shared"); got != history {
		t.Errorf("an index rebuilt from the log disagrees about %s:\nlive:\n%s\nrebuilt:\n%s", sameKey, history, got)
	}

	// And everything reached the remote: a fresh machine sees the same brain.
	b := machine{t, bin, filepath.Join(root, "b", "vault"), filepath.Join(root, "b", "cfg")}
	if out, err := exec.Command("git", "clone", "-q", remote, b.vault).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	b.run("config", "set", "host", "otherbox")
	if got := b.run("search", "note", "--scope", "shared", "-n", "10000"); sortedLines(got) != sortedLines(notes) {
		t.Errorf("a clone of the remote is missing memories: has %d of %d", len(nonEmptyLines(got)), half)
	}
	if got := b.run("history", sameKey, "--scope", "shared"); got != history {
		t.Errorf("a clone of the remote disagrees about %s", sameKey)
	}
}
