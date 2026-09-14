package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shockalotti/membraid/internal/index"
)

func TestDetectLocationAddresses(t *testing.T) {
	for _, c := range []struct {
		target  string
		login   bool
		want    location
		comment string
	}{
		{"https://github.com/you/api", false, location{Type: "git", Repo: "https://github.com/you/api"}, "a repo page is the repo"},
		{"https://github.com/you/api/tree/main/docs", false, location{Type: "web", URL: "https://github.com/you/api/tree/main/docs"}, "a deeper GitHub page is a page"},
		{"git@github.com:you/api.git", false, location{Type: "git", Repo: "git@github.com:you/api.git"}, ""},
		{"https://user:s3cret@gitlab.example.com/g/r.git", false, location{Type: "git", Repo: "https://gitlab.example.com/g/r.git"}, "credentials never reach memory"},
		{"https://www.notion.so/Team-Runbook-abc123", false, location{Type: "web", URL: "https://www.notion.so/Team-Runbook-abc123", Service: "Notion", Login: true}, ""},
		{"https://acme.notion.site/Public-Page", false, location{Type: "web", URL: "https://acme.notion.site/Public-Page", Service: "Notion"}, "published Notion pages are public"},
		{"https://docs.google.com/document/d/xyz/edit", false, location{Type: "web", URL: "https://docs.google.com/document/d/xyz/edit", Service: "Google Drive", Login: true}, ""},
		{"http://wynneclaw1:8080/wiki", false, location{Type: "web", URL: "http://wynneclaw1:8080/wiki", Private: true}, "a bare machine name"},
		{"https://box.tail1234.ts.net/", false, location{Type: "web", URL: "https://box.tail1234.ts.net/", Private: true}, ""},
		{"http://192.168.1.5/notes", false, location{Type: "web", URL: "http://192.168.1.5/notes", Private: true}, ""},
		{"http://100.101.2.3/", false, location{Type: "web", URL: "http://100.101.2.3/", Private: true}, "Tailscale addresses"},
		{"https://docs.example.com/api", false, location{Type: "web", URL: "https://docs.example.com/api"}, ""},
		{"https://intranet.example.com/wiki", true, location{Type: "web", URL: "https://intranet.example.com/wiki", Login: true}, ""},
	} {
		got, err := detectLocation(c.target, c.login, "omarchy")
		if err != nil || got != c.want {
			t.Errorf("%s (%s): got %+v %v, want %+v", c.target, c.comment, got, err, c.want)
		}
	}
	if _, err := detectLocation("https://", false, "h"); err == nil {
		t.Error("an address without a host must be refused")
	}
}

func TestDetectLocationFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	plain := filepath.Join(home, "Notes")
	os.MkdirAll(plain, 0o755)
	if got, err := detectLocation(plain, false, "omarchy"); err != nil || got != (location{Type: "folder", Path: "~/Notes", Host: "omarchy"}) {
		t.Errorf("a plain folder: %+v %v", got, err)
	}
	if _, err := detectLocation(filepath.Join(home, "missing"), false, "omarchy"); err == nil {
		t.Error("a folder that is not there must be refused")
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := filepath.Join(home, "Projects", "api")
	docs := filepath.Join(repo, "docs")
	os.MkdirAll(docs, 0o755)
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://me:tok3n@github.com/you/api.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	got, err := detectLocation(docs, false, "omarchy")
	want := location{Type: "git", Repo: "https://github.com/you/api.git", Subdir: "docs", Checkout: "~/Projects/api", Host: "omarchy"}
	if err != nil || got != want {
		t.Errorf("a folder in a repo must be recorded as the repo: got %+v %v, want %+v", got, err, want)
	}
	if k := locationKey("", got); k != "source.api.docs" {
		t.Errorf("key for a repo folder: %s", k)
	}
}

// What is written reads back into the same location, and the text tells an
// agent elsewhere what to do.
func TestLocationContentRoundTrip(t *testing.T) {
	for _, c := range []struct {
		loc  location
		says string
	}{
		{location{Type: "folder", Path: "~/Work/API Specs", Host: "omarchy"}, "only on omarchy"},
		{location{Type: "git", Repo: "git@github.com:you/api.git", Subdir: "docs", Checkout: "~/Projects/api", Host: "omarchy"}, "or clone it"},
		{location{Type: "web", URL: "https://www.notion.so/Runbook", Service: "Notion", Login: true}, "use a Notion tool if you have one, otherwise ask the user"},
		{location{Type: "web", URL: "http://wynneclaw1:8080/", Private: true, Login: true}, "only reachable on the user's private network and needs a login"},
		{location{Type: "web", URL: "https://docs.example.com/a?b=\"c\"", Login: false}, "public"},
	} {
		content := locationContent("Specs. Folder notes, read first.", c.loc)
		if !strings.Contains(content, c.says) {
			t.Errorf("%s content must say %q: %s", c.loc.Type, c.says, content)
		}
		got := parseSource(index.Hit{Content: content})
		if got.location != c.loc || got.About != "Specs. Folder notes, read first" {
			t.Errorf("round trip of %+v: got %+v about %q\n%s", c.loc, got.location, got.About, content)
		}
	}

	legacy := parseSource(index.Hit{Content: "Knowledge location: API specs. Folder: ~/Work/API Specs (on omarchy). Read it there with your own tools when the work touches it."})
	if legacy.Type != "folder" || legacy.Path != "~/Work/API Specs" || legacy.Host != "omarchy" || legacy.About != "API specs" {
		t.Errorf("a location in the first format must still read: %+v", legacy)
	}

	if p := repoPage("git@github.com:you/api.git", "docs"); p != "https://github.com/you/api" {
		t.Errorf("repo page: %s", p)
	}
	if p := repoPage("git@example.internal:x/y.git", ""); p != "" {
		t.Errorf("an unknown git host has no page to open: %s", p)
	}
}

func TestLocationKeys(t *testing.T) {
	for _, c := range []struct {
		loc  location
		want string
	}{
		{location{Type: "folder", Path: "~/Work/API Specs"}, "source.api.specs"},
		{location{Type: "git", Repo: "https://github.com/you/membraid.git"}, "source.membraid"},
		{location{Type: "web", URL: "https://www.notion.so/Runbook"}, "source.notion.so.runbook"},
	} {
		if got := locationKey("", c.loc); got != c.want {
			t.Errorf("key for %+v: got %s, want %s", c.loc, got, c.want)
		}
	}
	long := locationKey("", location{Type: "web", URL: "https://docs.example.com/" + strings.Repeat("very-long-page-name-", 6)})
	if len(long) > 60 || strings.HasSuffix(long, ".") {
		t.Errorf("keys are kept short: %s", long)
	}
}
