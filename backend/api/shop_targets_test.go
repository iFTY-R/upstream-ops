package api

import (
	"testing"

	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestBuildShopTargetPreservesNotifyEnabledWhenOmitted(t *testing.T) {
	current := &storage.ShopTarget{
		Name:          "shop",
		Platform:      storage.ShopPlatformLDXP,
		SiteURL:       "https://pay.ldxp.cn/shop/TOKEN",
		BaseURL:       "https://pay.ldxp.cn",
		Token:         "TOKEN",
		NotifyEnabled: true,
	}
	next, err := buildShopTarget(shopTargetInput{
		Name:     current.Name,
		Platform: current.Platform,
		SiteURL:  current.SiteURL,
		BaseURL:  current.BaseURL,
		Token:    current.Token,
	}, current)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	if !next.NotifyEnabled {
		t.Fatalf("notify_enabled = false, want preserved true")
	}
}

func TestBuildShopTargetDefaultsNotifyEnabledForCreate(t *testing.T) {
	next, err := buildShopTarget(shopTargetInput{
		Name:     "shop",
		Platform: storage.ShopPlatformLDXP,
		SiteURL:  "https://pay.ldxp.cn/shop/TOKEN",
		BaseURL:  "https://pay.ldxp.cn",
		Token:    "TOKEN",
	}, nil)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	if next.NotifyEnabled {
		t.Fatalf("notify_enabled = true, want default false")
	}
}
