package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/scope"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/wirelog"
)

// Knowledge locations are ordinary memories that say where knowledge lives
// outside membraid, and who can reach it: a folder on one machine, a git repo
// any machine can clone, or a web page that may need a login or only answer on
// the user's private network. membraid records the location; it never reads,
// clones, fetches or probes it (docs/V1-SCOPE.md explains why). Each is keyed
// source.<name>, so adding the same place again replaces its entry.
//
// The memory reads as plain text for agents and ends with a [source ...] tail
// that the CLI and the widget parse back.

type location struct {
	// Type is folder, git or web.
	Type string `json:"type"`
	// Path is a folder, relative to home when under it.
	Path string `json:"path,omitempty"`
	// Repo is a git remote, with any credentials removed.
	Repo string `json:"repo,omitempty"`
	// Subdir is the folder inside the repo the location points at.
	Subdir string `json:"subdir,omitempty"`
	// Checkout is where the repo was checked out on Host.
	Checkout string `json:"checkout,omitempty"`
	// URL is a web page.
	URL string `json:"url,omitempty"`
	// Service names a known service behind a URL, such as Notion.
	Service string `json:"service,omitempty"`
	// Login is set when the page needs an account; the credential is never stored.
	Login bool `json:"login,omitempty"`
	// Private is set for addresses only reachable on a private network.
	Private bool `json:"private,omitempty"`
	// Host is the machine a folder or checkout was added on.
	Host string `json:"host,omitempty"`
}

type sourceInfo struct {
	index.Hit
	location
	About string `json:"about"`
	// Here reports whether the folder or checkout exists on this machine.
	// Always false for web pages, which membraid never checks.
	Here bool `json:"here"`
	// Open is what opening the location here means: a local folder, or a web
	// address (a repo's page when it is not checked out here).
	Open string `json:"open,omitempty"`
}

var gitHosts = map[string]bool{"github.com": true, "gitlab.com": true, "codeberg.org": true, "bitbucket.org": true}

// webServices are services recognised from a URL's host, so an agent knows
// which account or tool a page needs. A line each; order matters for suffixes.
var webServices = []struct {
	host, name string
	login      bool
}{
	{"notion.site", "Notion", false}, // pages published to the web
	{"notion.so", "Notion", true},
	{"docs.google.com", "Google Drive", true},
	{"drive.google.com", "Google Drive", true},
}

func hostIs(host, domain string) bool { return host == domain || strings.HasSuffix(host, "."+domain) }

// privateHost reports addresses that are clearly not on the public internet,
// judged from the address alone: nothing is looked up or contacted.
func privateHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		_, cgnat, _ := net.ParseCIDR("100.64.0.0/10") // Tailscale's addresses
		return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)
	}
	if !strings.Contains(host, ".") {
		return true // a bare machine name
	}
	for _, suffix := range []string{".ts.net", ".local", ".lan", ".internal", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// cleanRemote removes credentials from an http(s) git remote: a token in a
// remote URL must never reach memory, which syncs to git.
func cleanRemote(r string) string {
	if u, err := url.Parse(r); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		u.User = nil
		return strings.TrimSuffix(u.String(), "/")
	}
	return r
}

// gitCheckout finds the repo a folder belongs to and its remote, preferring
// origin. Empty when the folder is not in a repo with a remote.
func gitCheckout(dir string) (top, remote string) {
	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	if top = run("rev-parse", "--show-toplevel"); top == "" {
		return "", ""
	}
	if remote = run("remote", "get-url", "origin"); remote == "" {
		if names := strings.Fields(run("remote")); len(names) > 0 {
			remote = run("remote", "get-url", names[0])
		}
	}
	return top, remote
}

// detectLocation works out what target is: a web address, a git repo address,
// or a local folder, which becomes its repo when it lives in one.
func detectLocation(target string, login bool, host string) (location, error) {
	t := strings.TrimSpace(target)
	switch {
	case strings.HasPrefix(t, "git@") || strings.HasPrefix(t, "ssh://") || strings.HasPrefix(t, "git://"):
		return location{Type: "git", Repo: cleanRemote(t)}, nil
	case strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://"):
		u, err := url.Parse(t)
		if err != nil || u.Hostname() == "" {
			return location{}, fmt.Errorf("%q is not a web address", t)
		}
		u.User = nil
		h := strings.ToLower(u.Hostname())
		segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
		if strings.HasSuffix(u.Path, ".git") || (gitHosts[h] && len(segs) == 2 && u.RawQuery == "" && u.Fragment == "") {
			return location{Type: "git", Repo: strings.TrimSuffix(u.String(), "/")}, nil
		}
		loc := location{Type: "web", URL: u.String(), Login: login, Private: privateHost(h)}
		for _, s := range webServices {
			if hostIs(h, s.host) {
				loc.Service, loc.Login = s.name, login || s.login
				break
			}
		}
		return loc, nil
	}
	folder, err := homeRelative(t)
	if err != nil {
		return location{}, err
	}
	dir := expandHome(folder)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return location{}, fmt.Errorf("there is no folder at %s on this machine", folder)
	}
	if top, remote := gitCheckout(dir); remote != "" {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			resolved = dir
		}
		sub, err := filepath.Rel(top, resolved)
		if err != nil || sub == "." || strings.HasPrefix(sub, "..") {
			sub = ""
		}
		checkout, _ := homeRelative(top)
		return location{Type: "git", Repo: cleanRemote(remote), Subdir: filepath.ToSlash(sub), Checkout: checkout, Host: host}, nil
	}
	return location{Type: "folder", Path: folder, Host: host}, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// homeRelative writes a folder under the home directory as ~/..., so the same
// location reads the same on machines whose home paths differ.
func homeRelative(p string) (string, error) {
	abs, err := filepath.Abs(expandHome(p))
	if err != nil {
		return "", err
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "~", nil
			}
			return "~/" + filepath.ToSlash(rel), nil
		}
	}
	return abs, nil
}

func repoName(repo string) string {
	r := strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
	if i := strings.LastIndexAny(r, "/:"); i >= 0 {
		r = r[i+1:]
	}
	return r
}

// locationKey is source.<name>: a folder's name, a repo's name (and folder),
// or a page's host and last path part. A given key is used as it is.
func locationKey(key string, loc location) string {
	if key == "" {
		switch loc.Type {
		case "folder":
			key = filepath.Base(expandHome(loc.Path))
		case "git":
			key = repoName(loc.Repo)
			if loc.Subdir != "" {
				key += "." + path.Base(loc.Subdir)
			}
		case "web":
			if u, err := url.Parse(loc.URL); err == nil {
				key = strings.TrimPrefix(u.Hostname(), "www.")
				if last := path.Base(strings.Trim(u.Path, "/")); last != "." && last != "" && last != "/" {
					key += "." + last
				}
			}
		}
	}
	key = index.NormalizeKey(key)
	if !strings.HasPrefix(key, index.SourceKeyPrefix) {
		key = index.NormalizeKey(index.SourceKeyPrefix + key)
	}
	if len(key) > 60 {
		key = strings.TrimRight(key[:60], ".")
	}
	if key == strings.TrimSuffix(index.SourceKeyPrefix, ".") {
		key = index.SourceKeyPrefix + "location"
	}
	return key
}

// locationContent is what agents read: what is there, where, and what to do
// if this machine cannot reach it, then the [source ...] tail.
func locationContent(about string, loc location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Knowledge location: %s. ", strings.TrimRight(strings.TrimSpace(about), "."))
	switch loc.Type {
	case "folder":
		fmt.Fprintf(&b, "Folder %s, only on %s: read it there with your own tools; on another machine, tell the user it is on %s.", loc.Path, loc.Host, loc.Host)
	case "git":
		fmt.Fprintf(&b, "Git repo %s", loc.Repo)
		if loc.Subdir != "" {
			fmt.Fprintf(&b, ", folder %s/", loc.Subdir)
		}
		if loc.Checkout != "" {
			fmt.Fprintf(&b, " (checked out at %s on %s)", loc.Checkout, loc.Host)
		}
		b.WriteString(": read it in a local checkout, or clone it; a private repo needs access the user has.")
	case "web":
		fmt.Fprintf(&b, "Web page %s", loc.URL)
		switch {
		case loc.Private:
			b.WriteString(", only reachable on the user's private network")
			if loc.Login {
				b.WriteString(" and needs a login")
			}
			b.WriteString(": open it if this machine can reach it, otherwise tell the user.")
		case loc.Login && loc.Service != "":
			fmt.Fprintf(&b, ", needs %s access: use a %s tool if you have one, otherwise ask the user.", loc.Service, loc.Service)
		case loc.Login:
			b.WriteString(", needs a login: use it only if you already have access, otherwise ask the user.")
		default:
			b.WriteString(", public: read it when the work touches it.")
		}
	}
	b.WriteString(" " + locationTail(loc))
	return b.String()
}

func locationTail(loc location) string {
	parts := []string{"type=" + quote(loc.Type)}
	add := func(name, value string) {
		if value != "" {
			parts = append(parts, name+"="+quote(value))
		}
	}
	add("path", loc.Path)
	add("repo", loc.Repo)
	add("subdir", loc.Subdir)
	add("checkout", loc.Checkout)
	add("url", loc.URL)
	add("service", loc.Service)
	if loc.Login {
		add("login", "true")
	}
	if loc.Private {
		add("private", "true")
	}
	add("host", loc.Host)
	return "[source " + strings.Join(parts, " ") + "]"
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, "%22") + `"` }

var (
	sourceTail = regexp.MustCompile(`\[source ([^\]]*)\]\s*$`)
	tailAttr   = regexp.MustCompile(`(\w+)="([^"]*)"`)
	// The first format, before types: a folder, possibly with spaces, then the machine.
	legacyFolder = regexp.MustCompile(` Folder: (.+) \(on ([^)]*)\)\. Read it there`)
	typeMarker   = map[string]string{"folder": ". Folder ", "git": ". Git repo ", "web": ". Web page "}
)

func parseSource(h index.Hit) sourceInfo {
	s := sourceInfo{Hit: h, About: h.Content}
	if m := sourceTail.FindStringSubmatchIndex(h.Content); m != nil {
		for _, a := range tailAttr.FindAllStringSubmatch(h.Content[m[2]:m[3]], -1) {
			v := strings.ReplaceAll(a[2], "%22", `"`)
			switch a[1] {
			case "type":
				s.Type = v
			case "path":
				s.Path = v
			case "repo":
				s.Repo = v
			case "subdir":
				s.Subdir = v
			case "checkout":
				s.Checkout = v
			case "url":
				s.URL = v
			case "service":
				s.Service = v
			case "login":
				s.Login = v == "true"
			case "private":
				s.Private = v == "true"
			case "host":
				s.Host = v
			}
		}
		if marker, ok := typeMarker[s.Type]; ok {
			if i := strings.LastIndex(h.Content[:m[0]], marker); i >= 0 {
				s.About = strings.TrimPrefix(h.Content[:i], "Knowledge location: ")
			}
		}
	} else if m := legacyFolder.FindStringSubmatchIndex(h.Content); m != nil {
		s.Type, s.Path, s.Host = "folder", h.Content[m[2]:m[3]], h.Content[m[4]:m[5]]
		s.About = strings.TrimRight(strings.TrimPrefix(h.Content[:m[0]], "Knowledge location: "), ".")
	}
	isDir := func(p string) bool {
		fi, err := os.Stat(expandHome(p))
		return p != "" && err == nil && fi.IsDir()
	}
	switch s.Type {
	case "folder":
		if s.Here = isDir(s.Path); s.Here {
			s.Open = expandHome(s.Path)
		}
	case "git":
		if s.Here = isDir(s.Checkout); s.Here {
			s.Open = filepath.Join(expandHome(s.Checkout), filepath.FromSlash(s.Subdir))
		} else {
			s.Open = repoPage(s.Repo, s.Subdir)
		}
	case "web":
		s.Open = s.URL
	}
	return s
}

// repoPage is a repo's web page, when its remote names a known git host.
func repoPage(repo, subdir string) string {
	r := strings.TrimSuffix(repo, ".git")
	if strings.HasPrefix(r, "git@") {
		if i := strings.Index(r, ":"); i > 0 {
			r = "https://" + strings.TrimPrefix(r[:i], "git@") + "/" + r[i+1:]
		}
	}
	u, err := url.Parse(r)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !gitHosts[strings.ToLower(u.Hostname())] {
		return ""
	}
	return strings.TrimSuffix(u.String(), "/")
}

func (s sourceInfo) where() string {
	switch s.Type {
	case "folder":
		note := ""
		if !s.Here {
			note = " (only on " + s.Host + ")"
		}
		return "folder " + s.Path + note
	case "git":
		w := "git " + s.Repo
		if s.Subdir != "" {
			w += " /" + s.Subdir
		}
		if s.Here {
			w += " (checked out here)"
		}
		return w
	case "web":
		w := "web " + s.URL
		switch {
		case s.Private:
			w += " (private network)"
		case s.Login && s.Service != "":
			w += " (needs " + s.Service + ")"
		case s.Login:
			w += " (needs a login)"
		default:
			w += " (public)"
		}
		return w
	}
	return ""
}

// runSource is `membraid source add TARGET --about TEXT [--login] | list | remove KEY`.
func runSource(v *vault.Vault, cfg config.Config, args []string, about, key, scopeFlag string, scopeGiven, login, jsonOut bool, source string) error {
	if len(args) == 0 {
		return errors.New(`source takes add, list or remove: membraid source add ~/Work/specs --about "API design specs"`)
	}
	return withIndex(v, cfg, func(ix *index.Index) error {
		switch args[0] {
		case "add":
			if len(args) < 2 || strings.TrimSpace(about) == "" {
				return errors.New(`source add needs a folder, git repo or web address, and what it holds: membraid source add https://github.com/you/api --about "API design specs; read before changing endpoints"`)
			}
			loc, err := detectLocation(args[1], login, wirelog.SafeHost(cfg.HostName()))
			if err != nil {
				return err
			}
			sc := scope.Resolve(scopeFlag)
			k := locationKey(key, loc)
			res, err := ix.Write(index.Memory{
				Kind: index.KindProjectParam, Key: k, Scope: sc, Source: source,
				Content: locationContent(about, loc),
			})
			if err != nil {
				return err
			}
			verb := "added"
			if len(res.Superseded) > 0 {
				verb = "updated"
			}
			info := parseSource(index.Hit{Content: locationContent(about, loc)})
			fmt.Printf("%s knowledge location %s in %s: %s\n", verb, k, sc, info.where())
			return nil

		case "list":
			sc := "*"
			if scopeGiven {
				sc = scope.Resolve(scopeFlag)
			}
			hits, err := ix.Sources(sc)
			if err != nil {
				return err
			}
			names, _ := ix.ScopeNames()
			out := make([]sourceInfo, 0, len(hits))
			for _, h := range hits {
				s := parseSource(h)
				if s.ScopeName = names[h.Scope]; s.ScopeName == "" {
					s.ScopeName = h.Scope
				}
				out = append(out, s)
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(out)
			}
			if len(out) == 0 {
				fmt.Println(`no knowledge locations yet: membraid source add FOLDER|REPO|URL --about "what it holds"`)
			}
			for _, s := range out {
				fmt.Printf("%-28s %-16s %s  -  %s\n", s.Key, s.ScopeName, s.where(), s.About)
			}
			return nil

		case "remove":
			if len(args) < 2 {
				return errors.New("source remove needs the location's key, e.g. source.api.specs; membraid source list shows them")
			}
			k := locationKey(args[1], location{})
			gone, err := ix.Forget(scope.Resolve(scopeFlag), k, "")
			if errors.Is(err, index.ErrNothingToForget) {
				return fmt.Errorf("no knowledge location %s in this scope; membraid source list shows them (pass --scope)", k)
			}
			if err != nil {
				return err
			}
			fmt.Printf("removed %s (%d, still in history)\n", k, len(gone))
			return nil
		}
		return fmt.Errorf("source takes add, list or remove, not %q", args[0])
	})
}
