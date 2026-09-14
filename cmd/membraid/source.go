package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/scope"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/wirelog"
)

// Knowledge locations are ordinary memories that say where knowledge lives
// outside membraid: a folder of specs, notes or docs that agents should read
// there, with their own tools. membraid records the folder and what it holds;
// it never reads or indexes the files (docs/V1-SCOPE.md explains why). Each is
// keyed source.<name>, so adding the same folder again replaces its entry.

type sourceInfo struct {
	index.Hit
	About  string `json:"about"`
	Folder string `json:"folder"`
	// Host is the machine the location was added on; the folder may not exist
	// on others.
	Host string `json:"host,omitempty"`
	// Exists reports whether the folder is there on this machine.
	Exists bool `json:"exists"`
}

// The folder may contain spaces, so it runs up to the machine note.
var sourceFolder = regexp.MustCompile(` Folder: (.+) \(on ([^)]*)\)\. Read it there`)

func sourceContent(about, folder, host string) string {
	about = strings.TrimRight(strings.TrimSpace(about), ".")
	return fmt.Sprintf("Knowledge location: %s. Folder: %s (on %s). Read it there with your own tools when the work touches it.", about, folder, host)
}

func parseSource(h index.Hit) sourceInfo {
	s := sourceInfo{Hit: h, About: h.Content}
	if m := sourceFolder.FindStringSubmatchIndex(h.Content); m != nil {
		s.Folder = h.Content[m[2]:m[3]]
		s.Host = h.Content[m[4]:m[5]]
		s.About = strings.TrimPrefix(h.Content[:m[0]], "Knowledge location: ")
		s.About = strings.TrimRight(s.About, ".")
	}
	if s.Folder != "" {
		if fi, err := os.Stat(expandHome(s.Folder)); err == nil && fi.IsDir() {
			s.Exists = true
		}
	}
	return s
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

func sourceKey(key, folder string) string {
	if key == "" {
		key = filepath.Base(expandHome(folder))
	}
	key = index.NormalizeKey(key)
	if !strings.HasPrefix(key, index.SourceKeyPrefix) {
		key = index.NormalizeKey(index.SourceKeyPrefix + key)
	}
	if key == strings.TrimSuffix(index.SourceKeyPrefix, ".") || key == index.SourceKeyPrefix {
		key = index.SourceKeyPrefix + "folder"
	}
	return key
}

// runSource is `membraid source add FOLDER --about TEXT | list | remove KEY`.
func runSource(v *vault.Vault, cfg config.Config, args []string, about, key, scopeFlag string, scopeGiven, jsonOut bool, source string) error {
	if len(args) == 0 {
		return errors.New(`source takes add, list or remove: membraid source add ~/Work/specs --about "API design specs"`)
	}
	return withIndex(v, cfg, func(ix *index.Index) error {
		switch args[0] {
		case "add":
			if len(args) < 2 || strings.TrimSpace(about) == "" {
				return errors.New(`source add needs a folder and what it holds: membraid source add ~/Work/specs --about "API design specs; read before changing endpoints"`)
			}
			folder, err := homeRelative(args[1])
			if err != nil {
				return err
			}
			if fi, err := os.Stat(expandHome(folder)); err != nil || !fi.IsDir() {
				return fmt.Errorf("there is no folder at %s on this machine", folder)
			}
			sc := scope.Resolve(scopeFlag)
			k := sourceKey(key, folder)
			res, err := ix.Write(index.Memory{
				Kind: index.KindProjectParam, Key: k, Scope: sc, Source: source,
				Content: sourceContent(about, folder, wirelog.SafeHost(cfg.HostName())),
			})
			if err != nil {
				return err
			}
			verb := "added"
			if len(res.Superseded) > 0 {
				verb = "updated"
			}
			fmt.Printf("%s knowledge location %s in %s: %s\n", verb, k, sc, folder)
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
				s.ScopeName = names[h.Scope]
				if s.ScopeName == "" {
					s.ScopeName = h.Scope
				}
				out = append(out, s)
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(out)
			}
			if len(out) == 0 {
				fmt.Println(`no knowledge locations yet: membraid source add FOLDER --about "what it holds"`)
			}
			for _, s := range out {
				note := ""
				if !s.Exists {
					note = "  (not on this machine)"
				}
				fmt.Printf("%-24s %-16s %s  -  %s%s\n", s.Key, s.ScopeName, s.Folder, s.About, note)
			}
			return nil

		case "remove":
			if len(args) < 2 {
				return errors.New("source remove needs the location's key, e.g. source.api.specs; membraid source list shows them")
			}
			k := sourceKey(args[1], "")
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
