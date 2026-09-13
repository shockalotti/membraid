package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReleases serves one release the way GitHub does: the API describes it,
// and each asset downloads from its own URL.
func fakeReleases(t *testing.T, tag string, files map[string]string) *Client {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}
	var assets []asset
	for name, body := range files {
		body := body
		assets = append(assets, asset{name, srv.URL + "/download/" + name})
		mux.HandleFunc("/download/"+name, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
	}
	release := func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": assets})
	}
	mux.HandleFunc("/releases/latest", release)
	mux.HandleFunc("/releases/tags/"+tag, release)
	return &Client{API: srv.URL, HTTP: srv.Client()}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestDownloadVerifiesTheChecksum(t *testing.T) {
	name := AssetName("linux", "amd64", false)
	good := "new binary"
	c := fakeReleases(t, "v0.5.0", map[string]string{
		name:            good,
		"checksums.txt": sha(good) + "  " + name + "\n" + sha("x") + "  membraid-darwin-arm64\n",
	})
	ctx := context.Background()
	r, err := c.Release(ctx, "")
	if err != nil || r.Tag != "v0.5.0" {
		t.Fatalf("latest release: %+v %v", r, err)
	}
	if data, err := c.Download(ctx, r, name); err != nil || string(data) != good {
		t.Fatalf("download: %q %v", data, err)
	}
	if _, err := c.Download(ctx, r, AssetName("freebsd", "riscv64", false)); err == nil || !strings.Contains(err.Error(), "no build for this machine") {
		t.Errorf("a platform with no build must say so, got %v", err)
	}

	tampered := fakeReleases(t, "v0.5.0", map[string]string{
		name:            "tampered",
		"checksums.txt": sha(good) + "  " + name + "\n",
	})
	r, _ = tampered.Release(ctx, "v0.5.0")
	if _, err := tampered.Download(ctx, r, name); err == nil || !strings.Contains(err.Error(), "does not match its checksum") {
		t.Errorf("a file that does not match its checksum must be refused, got %v", err)
	}

	unsigned := fakeReleases(t, "v0.5.0", map[string]string{name: good})
	r, _ = unsigned.Release(ctx, "")
	if _, err := unsigned.Download(ctx, r, name); err == nil {
		t.Error("a release without checksums must be refused")
	}
}

func TestAssetNames(t *testing.T) {
	for _, c := range []struct {
		goos, goarch string
		lite         bool
		want         string
	}{
		{"linux", "amd64", false, "membraid-linux-amd64"},
		{"linux", "arm64", true, "membraid-linux-arm64-lite"},
		{"windows", "amd64", false, "membraid-windows-amd64.exe"},
		{"darwin", "arm64", true, "membraid-darwin-arm64-lite"},
	} {
		if got := AssetName(c.goos, c.goarch, c.lite); got != c.want {
			t.Errorf("AssetName(%s, %s, %v) = %s, want %s", c.goos, c.goarch, c.lite, got, c.want)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		current, latest   string
		newer, comparable bool
	}{
		{"v0.4.0", "v0.4.1", true, true},
		{"v0.4.1", "v0.4.0", false, true},
		{"v0.4.0", "v0.4.0", false, true},
		{"v0.9.0", "v0.10.0", true, true},
		{"v0.4.0-rc.1", "v0.4.0", true, true},
		{"v1.0.0", "v0.9.9", false, true},
		{"dev", "v0.4.0", false, false},
		{"v0.4.0-0.20260913-abcdef", "v0.4.0", true, true},
	} {
		newer, comparable := Newer(c.current, c.latest)
		if newer != c.newer || comparable != c.comparable {
			t.Errorf("Newer(%s, %s) = %v, %v; want %v, %v", c.current, c.latest, newer, comparable, c.newer, c.comparable)
		}
	}
}

func TestReplaceSwapsTheFileAndKeepsItExecutable(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "membraid")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := Replace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	fi, _ := os.Stat(exe)
	if string(got) != "new" || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("want the new, executable file, got %q mode %v", got, fi.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Errorf("no temporary files may be left behind, got %d entries", len(entries))
	}
}
