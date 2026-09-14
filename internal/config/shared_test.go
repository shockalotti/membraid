package config

import (
	"testing"
	"time"
)

// Each machine writes its own file; the newest value of a key wins wherever it
// was set, so two machines never conflict and both end up agreeing.
func TestSharedSettingsFollowTheUser(t *testing.T) {
	hot := t.TempDir()
	t0 := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	if err := SetShared(hot, "laptop", "halflife_days", "45", t0); err != nil {
		t.Fatal(err)
	}
	if err := SetShared(hot, "minipc", "halflife_days", "20", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := SetShared(hot, "laptop", "frequency_boost", "1.5", t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := LoadShared(hot)
	if err != nil {
		t.Fatal(err)
	}
	if got["halflife_days"] != "20" || got["frequency_boost"] != "1.5" {
		t.Errorf("want the newest value per key across machines, got %v", got)
	}

	for key, bad := range map[string]string{"halflife_days": "0", "frequency_boost": "9", "digest_items": "x", "digest_shared_weight": "0"} {
		if err := SetShared(hot, "laptop", key, bad, t0); err == nil {
			t.Errorf("%s=%s must be refused", key, bad)
		}
	}
	if err := SetShared(hot, "laptop", "auto_sync", "true", t0); err == nil {
		t.Error("a machine setting is not a vault setting")
	}
}
