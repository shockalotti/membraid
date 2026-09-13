// Package config holds per-machine settings and sync state.
//
// Both live OUTSIDE the vault, deliberately. The vault is synced between
// machines; whether this machine auto-syncs, what it is called, and when it
// last synced are facts about this machine, and syncing them would have every
// machine overwrite every other's.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// AutoSync commits and pushes shortly after writes, pulls when an agent
	// session starts, and lets the scheduled timer pull periodically.
	AutoSync bool `json:"auto_sync"`
	// PushDelaySec waits for writes to go quiet before pushing, so a burst of
	// agent writes becomes one commit rather than twenty.
	PushDelaySec int `json:"push_delay_sec"`
	// PullIntervalMin is how stale a scheduled pull may get. The timer fires
	// more often than this and skips when the last sync is recent enough.
	PullIntervalMin int `json:"pull_interval_min"`
	// Host names this machine's wire-log file. Empty means the hostname.
	Host string `json:"host,omitempty"`
	// HalflifeDays is how many days without being retrieved it takes for a
	// memory's search rank to halve (SPEC 7.2). Retrieval resets the clock.
	HalflifeDays int `json:"halflife_days"`
	// Embeddings turns on search by meaning: "off" (the default, also when
	// empty), "ollama" or "builtin". Vectors stay in this machine's index.
	Embeddings string `json:"embeddings,omitempty"`
	// EmbedModel is the Ollama model tag; empty means the default. The builtin
	// provider has one model and ignores it.
	EmbedModel string `json:"embed_model,omitempty"`
}

// EmbeddingsOn reports whether search by meaning is configured.
func (c Config) EmbeddingsOn() bool { return c.Embeddings != "" && c.Embeddings != "off" }

func Defaults() Config {
	return Config{AutoSync: true, PushDelaySec: 60, PullIntervalMin: 15, HalflifeDays: 30}
}

// Dir is MEMBRAID_CONFIG_DIR, or the platform config dir: ~/.config/membraid on
// Linux, %AppData%\membraid on Windows.
func Dir() string {
	if d := os.Getenv("MEMBRAID_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ".membraid-config"
	}
	return filepath.Join(base, "membraid")
}

func Path() string      { return filepath.Join(Dir(), "config.json") }
func StatePath() string { return filepath.Join(Dir(), "state.json") }

// Load returns defaults overlaid with whatever the file sets. A missing file is
// not an error: the defaults are a working configuration.
func Load() (Config, error) {
	c := Defaults()
	raw, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Defaults(), fmt.Errorf("config: %s: %w", Path(), err)
	}
	c.clamp()
	return c, nil
}

func (c *Config) clamp() {
	if c.PushDelaySec < 5 {
		c.PushDelaySec = 5
	}
	if c.PullIntervalMin < 1 {
		c.PullIntervalMin = 1
	}
	if c.HalflifeDays < 1 {
		c.HalflifeDays = 30
	}
}

func (c Config) Save() error {
	c.clamp()
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), append(buf, '\n'), 0o600)
}

// Set parses and applies one setting by its JSON name.
func (c *Config) Set(key, value string) error {
	switch key {
	case "auto_sync":
		b, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("auto_sync takes true or false, got %q", value)
		}
		c.AutoSync = b
	case "push_delay_sec", "pull_interval_min", "halflife_days":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 {
			return fmt.Errorf("%s takes a positive whole number, got %q", key, value)
		}
		switch key {
		case "push_delay_sec":
			c.PushDelaySec = n
		case "pull_interval_min":
			c.PullIntervalMin = n
		default:
			c.HalflifeDays = n
		}
	case "host":
		c.Host = strings.TrimSpace(value)
	case "embeddings":
		switch v := strings.ToLower(strings.TrimSpace(value)); v {
		case "off", "ollama", "builtin":
			c.Embeddings = v
		default:
			return fmt.Errorf("embeddings takes off, ollama or builtin, got %q", value)
		}
	case "embed_model":
		c.EmbedModel = strings.TrimSpace(value)
	default:
		return fmt.Errorf("unknown setting %q (auto_sync, push_delay_sec, pull_interval_min, halflife_days, host, embeddings, embed_model)", key)
	}
	c.clamp()
	return nil
}

// HostName is the configured host or the machine's hostname. Callers sanitise.
func (c Config) HostName() string {
	if c.Host != "" {
		return c.Host
	}
	h, err := os.Hostname()
	if err != nil {
		return "local"
	}
	return h
}

// State records how the last sync went, for status output and the bar widget.
type State struct {
	LastAttempt string `json:"last_attempt,omitempty"`
	LastSuccess string `json:"last_success,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	LastSkipped string `json:"last_skipped,omitempty"`
	Pushed      bool   `json:"pushed,omitempty"`
	Pulled      bool   `json:"pulled,omitempty"`
	Imported    int    `json:"imported,omitempty"`
}

func LoadState() State {
	var s State
	if raw, err := os.ReadFile(StatePath()); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func (s State) Save() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(StatePath(), append(buf, '\n'), 0o600)
}

// SinceLastSuccess reports how long ago the last successful sync was, and
// false if there has never been one.
func (s State) SinceLastSuccess(now time.Time) (time.Duration, bool) {
	t, err := time.Parse(time.RFC3339Nano, s.LastSuccess)
	if err != nil {
		return 0, false
	}
	return now.Sub(t), true
}
