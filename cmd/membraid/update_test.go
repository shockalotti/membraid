package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shockalotti/membraid/internal/update"
)

// An installed v0.1.0 updates itself to a published v0.2.0: it finds the
// release, downloads its own platform's build, checks it, swaps it in, and
// afterwards reports the new version.
func TestUpdateReplacesTheInstalledBinary(t *testing.T) {
	dir := t.TempDir()
	build := func(ver, out string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+ver, "-o", out, ".")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", ver, err, b)
		}
	}
	installed := filepath.Join(dir, "bin", "membraid")
	os.MkdirAll(filepath.Dir(installed), 0o755)
	build("v0.1.0", installed)
	next := filepath.Join(dir, "next")
	build("v0.2.0", next)
	data, _ := os.ReadFile(next)

	name := update.AssetName(runtime.GOOS, runtime.GOARCH, false)
	sum := sha256.Sum256(data)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "assets": []map[string]string{
			{"name": name, "browser_download_url": srv.URL + "/dl/bin"},
			{"name": "checksums.txt", "browser_download_url": srv.URL + "/dl/sums"},
		}})
	})
	mux.HandleFunc("/dl/bin", func(w http.ResponseWriter, _ *http.Request) { w.Write(data) })
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + name + "\n"))
	})

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(installed, args...)
		cmd.Env = append(os.Environ(), "MEMBRAID_RELEASES_API="+srv.URL, "MEMBRAID_CONFIG_DIR="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("membraid %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	if out := run("update", "--check"); !strings.Contains(out, "v0.2.0 is available") {
		t.Errorf("check must report the newer release, got %q", out)
	}
	if out := run("update"); !strings.Contains(out, "membraid v0.2.0") {
		t.Errorf("update must report the new version, got %q", out)
	}
	if out := run("version"); !strings.HasPrefix(out, "membraid v0.2.0 ") {
		t.Errorf("the installed binary must now be v0.2.0, got %q", out)
	}
	if out := run("update"); !strings.Contains(out, "up to date") {
		t.Errorf("a second update must find nothing newer, got %q", out)
	}
}
