package shopprovider

import (
	"testing"

	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestParseShopURL(t *testing.T) {
	got, err := ParseShopURL("https://pay.ldxp.cn/shop/7FCVUA4X")
	if err != nil {
		t.Fatalf("parse shop url: %v", err)
	}
	if got.Platform != storage.ShopPlatformLDXP {
		t.Fatalf("platform = %q", got.Platform)
	}
	if got.BaseURL != "https://pay.ldxp.cn" {
		t.Fatalf("base url = %q", got.BaseURL)
	}
	if got.Token != "7FCVUA4X" {
		t.Fatalf("token = %q", got.Token)
	}
}

func TestParseShopURLRejectsUnsupportedPath(t *testing.T) {
	if _, err := ParseShopURL("https://pay.ldxp.cn/not-shop/7FCVUA4X"); err == nil {
		t.Fatal("expected unsupported path error")
	}
}
