// Package update replaces the membraid binary with a published release.
//
// Releases are GitHub releases of shockalotti/membraid. Each carries one raw
// binary per platform, in a full build (with the built-in embedding model) and
// a lite one (-tags nobuiltin, smaller, Ollama only), plus checksums.txt in
// sha256sum format. Nothing is replaced unless the download matches its
// checksum, and the new file is moved into place in one rename, so a failed or
// interrupted update leaves the old binary working.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var errNotFound = errors.New("update: not found")

// DefaultAPI is the GitHub API base for membraid's releases.
const DefaultAPI = "https://api.github.com/repos/shockalotti/membraid"

// Release is one published version and its downloadable files by name.
type Release struct {
	Tag    string
	Assets map[string]string
}

// Client talks to the release API. Tests point API at a local server.
type Client struct {
	API  string
	HTTP *http.Client
}

func NewClient() *Client {
	api := DefaultAPI
	if v := os.Getenv("MEMBRAID_RELEASES_API"); v != "" {
		api = strings.TrimRight(v, "/")
	}
	return &Client{API: api, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// Release is the newest published release, or the one tagged tag when set.
func (c *Client) Release(ctx context.Context, tag string) (*Release, error) {
	url := c.API + "/releases/latest"
	if tag != "" {
		url = c.API + "/releases/tags/" + tag
	}
	body, err := c.get(ctx, url, "application/vnd.github+json")
	if errors.Is(err, errNotFound) {
		if tag != "" {
			return nil, fmt.Errorf("update: there is no release %s", tag)
		}
		return nil, errors.New("update: no membraid release has been published yet")
	}
	if err != nil {
		return nil, err
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("update: reading release: %w", err)
	}
	if raw.TagName == "" {
		return nil, errors.New("update: the release has no tag")
	}
	r := &Release{Tag: raw.TagName, Assets: map[string]string{}}
	for _, a := range raw.Assets {
		r.Assets[a.Name] = a.URL
	}
	return r, nil
}

// AssetName is the release file for a platform: membraid-linux-amd64,
// membraid-linux-amd64-lite, membraid-windows-amd64.exe.
func AssetName(goos, goarch string, lite bool) string {
	name := "membraid-" + goos + "-" + goarch
	if lite {
		name += "-lite"
	}
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Download fetches the named asset and returns it only if it matches the
// release's checksums.txt.
func (c *Client) Download(ctx context.Context, r *Release, name string) ([]byte, error) {
	url, ok := r.Assets[name]
	if !ok {
		return nil, fmt.Errorf("update: release %s has no build for this machine (%s)", r.Tag, name)
	}
	sumsURL, ok := r.Assets["checksums.txt"]
	if !ok {
		return nil, fmt.Errorf("update: release %s has no checksums.txt, so its files cannot be verified", r.Tag)
	}
	sums, err := c.get(ctx, sumsURL, "")
	if err != nil {
		return nil, err
	}
	want := checksumFor(string(sums), name)
	if want == "" {
		return nil, fmt.Errorf("update: checksums.txt in %s does not list %s", r.Tag, name)
	}
	data, err := c.get(ctx, url, "")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("update: %s does not match its checksum (got %s, want %s); nothing was replaced", name, got, want)
	}
	return data, nil
}

func checksumFor(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

func (c *Client) get(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", errNotFound, url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Newer reports whether latest is a later version than current. A current
// version that is not a release (a development build) is never newer or
// older: the caller decides, since comparing it means nothing.
func Newer(current, latest string) (newer, comparable bool) {
	c, okc := parse(current)
	l, okl := parse(latest)
	if !okc || !okl {
		return false, false
	}
	for i := 0; i < 3; i++ {
		if l.nums[i] != c.nums[i] {
			return l.nums[i] > c.nums[i], true
		}
	}
	// Same numbers: a release beats its own pre-release.
	return c.pre != "" && l.pre == "", true
}

type semver struct {
	nums [3]int
	pre  string
}

func parse(v string) (semver, bool) {
	var s semver
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		if v[i] == '-' {
			s.pre = v[i+1:]
		}
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return s, false
		}
		s.nums[i] = n
	}
	return s, true
}

// Replace puts data at exe. The new file is written beside it and renamed over
// it, so exe is either the old binary or the new one, never half of each.
// Running processes keep the file they started with. Windows will not rename
// over a running executable, so there the old one is moved aside first, to
// exe.old, which the next update removes.
func Replace(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exe); err == nil {
		mode = fi.Mode().Perm() | 0o100
	}
	tmp, err := os.CreateTemp(dir, ".membraid-update-*")
	if err != nil {
		return fmt.Errorf("update: cannot write next to %s: %w", exe, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(tmp.Name(), exe)
}
