package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Shared settings decide how memory is ranked. They follow the user, not the
// machine, so they live in the vault and sync with it: every machine orders the
// same memories the same way. Each machine writes only its own file,
// settings-<host>.json beside its log, so two machines changing settings
// between syncs never make a git conflict; the newest value of each key wins.
// The log reader only reads writes-*.jsonl, so older membraid versions never
// see these files.

// SharedKeys are the settings stored in the vault, with what each means.
var SharedKeys = map[string]string{
	"halflife_days":        "days for a write or a use to count half as much (whole number, 1 or more)",
	"frequency_boost":      "how much repeated use lifts a memory: 0 (use only keeps it fresh) to 5, default 1",
	"digest_items":         "how many known facts a session starts with (1 to 50)",
	"digest_shared_weight": "weight of shared memories against this project's in the digest (above 0, up to 1)",
}

// IsShared reports whether key is a vault setting.
func IsShared(key string) bool { _, ok := SharedKeys[key]; return ok }

type sharedValue struct {
	Value string `json:"value"`
	At    string `json:"at"`
}

// SharedPath is this machine's settings file in the vault's .hot directory.
func SharedPath(hotDir, host string) string {
	return filepath.Join(hotDir, "settings-"+host+".json")
}

// LoadShared merges every machine's settings file, newest value per key.
func LoadShared(hotDir string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(hotDir, "settings-*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	newest := map[string]sharedValue{}
	for _, f := range files {
		vals, err := readShared(f)
		if err != nil {
			return nil, err
		}
		for k, v := range vals {
			cur, ok := newest[k]
			if !ok || later(v.At, cur.At) {
				newest[k] = v
			}
		}
	}
	out := map[string]string{}
	for k, v := range newest {
		out[k] = v.Value
	}
	return out, nil
}

func later(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	if errA != nil {
		return false
	}
	return errB != nil || ta.After(tb)
}

func readShared(path string) (map[string]sharedValue, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]sharedValue{}, nil
	}
	if err != nil {
		return nil, err
	}
	vals := map[string]sharedValue{}
	if err := json.Unmarshal(raw, &vals); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return vals, nil
}

// ValidateShared checks a vault setting's value and returns it normalised.
func ValidateShared(key, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch key {
	case "halflife_days":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 3650 {
			return "", fmt.Errorf("halflife_days takes a whole number from 1 to 3650, got %q", value)
		}
		return strconv.Itoa(n), nil
	case "digest_items":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 50 {
			return "", fmt.Errorf("digest_items takes a whole number from 1 to 50, got %q", value)
		}
		return strconv.Itoa(n), nil
	case "frequency_boost":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 || f > 5 {
			return "", fmt.Errorf("frequency_boost takes a number from 0 to 5, got %q", value)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case "digest_shared_weight":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f <= 0 || f > 1 {
			return "", fmt.Errorf("digest_shared_weight takes a number above 0 and up to 1, got %q", value)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	}
	return "", fmt.Errorf("%q is not a vault setting", key)
}

// SetShared records a vault setting in this machine's file.
func SetShared(hotDir, host, key, value string, now time.Time) error {
	v, err := ValidateShared(key, value)
	if err != nil {
		return err
	}
	path := SharedPath(hotDir, host)
	vals, err := readShared(path)
	if err != nil {
		return err
	}
	vals[key] = sharedValue{Value: v, At: now.UTC().Format(time.RFC3339Nano)}
	buf, err := json.MarshalIndent(vals, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(hotDir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
