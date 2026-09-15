package main

import (
	"fmt"
	"strconv"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/embed"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/wirelog"
)

// nonEmpty is s as a one-element list, or nil when s is empty.
func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// printConfig shows every setting in effect, defaults included, grouped by
// where it is kept: this machine's config file, or the vault, which every
// machine shares.
func printConfig(v *vault.Vault, cfg config.Config) {
	rank := loadRanking(v, cfg)
	embeddings := cfg.Embeddings
	if embeddings == "" {
		embeddings = "off"
	}
	model := cfg.EmbedModel
	if model == "" {
		model = embed.DefaultOllamaModel
	}
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

	fmt.Printf("this machine (%s)\n", config.Path())
	for _, row := range [][2]string{
		{"auto_sync", strconv.FormatBool(cfg.AutoSync)},
		{"push_delay_sec", strconv.Itoa(cfg.PushDelaySec)},
		{"pull_interval_min", strconv.Itoa(cfg.PullIntervalMin)},
		{"distill_every_min", strconv.Itoa(cfg.DistillEveryMin)},
		{"sweep_every_days", strconv.Itoa(cfg.SweepEveryDays)},
		{"metrics_every_days", strconv.Itoa(cfg.MetricsEveryDays)},
		{"embeddings", embeddings},
		{"embed_model", model},
		{"host", wirelog.SafeHost(cfg.HostName())},
	} {
		fmt.Printf("  %-26s %-30s %s\n", row[0], row[1], config.LocalKeys[row[0]])
	}

	fmt.Printf("\nevery machine (in the vault, %s)\n", v.HotPath())
	for _, row := range [][2]string{
		{"halflife_days", num(rank.HalflifeDays)},
		{"frequency_boost", num(rank.FrequencyBoost)},
		{"digest_items", strconv.Itoa(rank.DigestItems)},
		{"digest_shared_weight", num(rank.DigestSharedWeight)},
		{"fuzzy_supersede_threshold", num(rank.FuzzyThreshold)},
		{"sweep_unused_days", strconv.Itoa(rank.SweepUnusedDays)},
		{"stale_task_days", strconv.Itoa(rank.StaleTaskDays)},
	} {
		fmt.Printf("  %-26s %-30s %s\n", row[0], row[1], config.SharedKeys[row[0]])
	}
	fmt.Println("\nchange one: membraid config set KEY VALUE")
}
