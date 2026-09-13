package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/shockalotti/membraid/internal/embed"
	"github.com/shockalotti/membraid/internal/update"
)

// version is set at release build time: -ldflags "-X main.version=v0.4.0".
var version = ""

// currentVersion is the release this binary was built from: the release build's
// stamp, else the module version go install recorded, else "dev" for a build
// from a working tree.
func currentVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func buildKind() string {
	if embed.BuiltinIncluded {
		return "full"
	}
	return "lite"
}

func runVersion(jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"version": currentVersion(), "os": runtime.GOOS, "arch": runtime.GOARCH, "build": buildKind(),
		})
	}
	fmt.Printf("membraid %s (%s/%s, %s build)\n", currentVersion(), runtime.GOOS, runtime.GOARCH, buildKind())
	return nil
}

// runUpdate replaces this binary with a published release. check only reports;
// tag installs that release instead of the latest, and is how a development
// build, which cannot be compared with anything, is replaced deliberately.
func runUpdate(check bool, tag string, jsonOut bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := update.NewClient()
	rel, err := client.Release(ctx, tag)
	if err != nil {
		return err
	}
	current := currentVersion()
	newer, comparable := update.Newer(current, rel.Tag)

	if check {
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"current": current, "latest": rel.Tag, "newer": newer, "comparable": comparable,
			})
		}
		switch {
		case !comparable:
			fmt.Printf("membraid %s is a development build; the latest release is %s\n", current, rel.Tag)
		case newer:
			fmt.Printf("membraid %s is available (this is %s): membraid update\n", rel.Tag, current)
		default:
			fmt.Printf("membraid %s is up to date\n", current)
		}
		return nil
	}

	if tag == "" {
		if !comparable {
			return fmt.Errorf("this membraid is a development build (%s), so there is no telling whether %s is newer; to replace it with that release anyway: membraid update --version %s", current, rel.Tag, rel.Tag)
		}
		if !newer {
			fmt.Printf("membraid %s is up to date\n", current)
			return nil
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if strings.Contains(exe, "go-build") {
		return errors.New("update replaces an installed binary, not one run by go run")
	}
	name := update.AssetName(runtime.GOOS, runtime.GOARCH, !embed.BuiltinIncluded)
	fmt.Printf("downloading %s %s...\n", rel.Tag, name)
	data, err := client.Download(ctx, rel, name)
	if err != nil {
		return err
	}
	if err := update.Replace(exe, data); err != nil {
		return err
	}
	out, err := exec.Command(exe, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary at %s does not run: %v: %s", exe, err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("updated %s: %s", exe, out)
	fmt.Println("Harnesses keep running the old version until they restart; the sync timer picks up the new one on its next run.")
	return nil
}
