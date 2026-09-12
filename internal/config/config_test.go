package config

import (
	"os"
	"testing"
	"time"
)

func withDir(t *testing.T) {
	t.Helper()
	t.Setenv("MEMBRAID_CONFIG_DIR", t.TempDir())
}

func TestMissingFileGivesWorkingDefaults(t *testing.T) {
	withDir(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c != Defaults() || !c.AutoSync || c.PushDelaySec != 60 || c.PullIntervalMin != 15 {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

// A file that sets one key keeps defaults for the rest, but an explicit false
// must win over a default true.
func TestPartialFileOverlaysDefaults(t *testing.T) {
	withDir(t)
	os.MkdirAll(Dir(), 0o700)
	os.WriteFile(Path(), []byte(`{"auto_sync": false}`), 0o600)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.AutoSync {
		t.Error("explicit false must override the default")
	}
	if c.PushDelaySec != 60 || c.PullIntervalMin != 15 {
		t.Errorf("unset keys must keep defaults: %+v", c)
	}
}

func TestSetValidatesAndPersists(t *testing.T) {
	withDir(t)
	c := Defaults()
	for _, bad := range [][2]string{{"auto_sync", "maybe"}, {"push_delay_sec", "-3"}, {"pull_interval_min", "x"}, {"nonsense", "1"}} {
		if err := c.Set(bad[0], bad[1]); err == nil {
			t.Errorf("Set(%q, %q) must fail", bad[0], bad[1])
		}
	}
	if err := c.Set("pull_interval_min", "5"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("push_delay_sec", "2"); err != nil {
		t.Fatal(err)
	}
	if c.PushDelaySec != 5 {
		t.Errorf("push delay must clamp to 5s so a busy agent cannot push every write: %d", c.PushDelaySec)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := Load()
	if got.PullIntervalMin != 5 {
		t.Errorf("setting did not persist: %+v", got)
	}
}

func TestStateRoundTripAndAge(t *testing.T) {
	withDir(t)
	if _, ok := LoadState().SinceLastSuccess(time.Now()); ok {
		t.Error("no state means no last success")
	}
	now := time.Now().UTC()
	s := State{LastSuccess: now.Add(-3 * time.Minute).Format(time.RFC3339Nano), Pushed: true}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	age, ok := LoadState().SinceLastSuccess(now)
	if !ok || age < 2*time.Minute || age > 4*time.Minute {
		t.Errorf("age wrong: %v %v", age, ok)
	}
}
