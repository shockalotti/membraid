package config

import "testing"

// Maintenance cadences are this machine's; ranking and staleness thresholds
// are the vault's, and halflife is no longer a local setting.
func TestCadenceSettings(t *testing.T) {
	withDir(t)
	c := Defaults()
	if c.DistillEveryMin != 30 || c.SweepEveryDays != 7 {
		t.Fatalf("defaults: %+v", c)
	}
	if err := c.Set("distill_every_min", "2"); err != nil || c.DistillEveryMin != 5 {
		t.Errorf("distillation more often than every 5 minutes must clamp to 5: %v %d", err, c.DistillEveryMin)
	}
	if err := c.Set("sweep_every_days", "0"); err == nil {
		t.Error("sweep_every_days 0 must be refused")
	}
	if c.MetricsEveryDays != 7 {
		t.Errorf("metrics_every_days default must be 7, got %d", c.MetricsEveryDays)
	}
	if err := c.Set("metrics_every_days", "0"); err != nil || c.MetricsEveryDays != 0 {
		t.Errorf("metrics_every_days 0 must be allowed (off): %v %d", err, c.MetricsEveryDays)
	}
	if err := c.Set("metrics_every_days", "9"); err != nil || c.MetricsEveryDays != 9 {
		t.Errorf("metrics_every_days 9 must be allowed: %v %d", err, c.MetricsEveryDays)
	}
	if err := c.Set("metrics_every_days", "-1"); err == nil {
		t.Error("metrics_every_days -1 must be refused")
	}
	if err := c.Set("metrics_every_days", "900"); err != nil || c.MetricsEveryDays != 7 {
		t.Errorf("metrics_every_days 900 must clamp back to the default 7: %v %d", err, c.MetricsEveryDays)
	}
	if err := c.Set("halflife_days", "10"); err == nil {
		t.Error("halflife_days is a vault setting, not this machine's")
	}
	for key, bad := range map[string]string{"sweep_unused_days": "3", "stale_task_days": "400"} {
		if _, err := ValidateShared(key, bad); err == nil {
			t.Errorf("%s=%s must be refused", key, bad)
		}
	}
	if v, err := ValidateShared("stale_task_days", " 21 "); err != nil || v != "21" {
		t.Errorf("stale_task_days 21: %q %v", v, err)
	}
}
