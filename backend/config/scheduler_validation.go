package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ValidateSchedulerConfig enforces scheduler-wide business limits before a
// configuration is persisted or hot-reloaded.
func ValidateSchedulerConfig(cfg SchedulerConfig) error {
	if cfg.ShopManualCooldownMinutes < 0 {
		return fmt.Errorf("shop manual sync cooldown minutes must not be negative")
	}
	return ValidatePriceAIFeedCron(cfg.PriceAIFeedCron)
}

func ValidatePriceAIFeedCron(expression string) error {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil
	}
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(expression)
	if err != nil {
		return fmt.Errorf("invalid priceAI feed cron: %w", err)
	}
	anchor := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	first := schedule.Next(anchor)
	second := schedule.Next(first)
	if first.IsZero() || second.IsZero() || second.Sub(first) < time.Minute {
		return fmt.Errorf("priceAI feed cron must not run more than once per minute")
	}
	return nil
}
