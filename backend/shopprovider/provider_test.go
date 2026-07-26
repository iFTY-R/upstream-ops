package shopprovider

import (
	"io"
	"net/http"
	"strings"
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
	if got.SiteURL != "https://pay.ldxp.cn/shop/7FCVUA4X" {
		t.Fatalf("site url = %q", got.SiteURL)
	}
	if got.Token != "7FCVUA4X" {
		t.Fatalf("token = %q", got.Token)
	}
}

func TestParseShopURLAcceptsLDXPItemURL(t *testing.T) {
	previousClient := ldxpItemHTTPClient
	ldxpItemHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost || r.URL.String() != "https://pay.ldxp.cn/shopApi/Shop/goodsInfo" {
				t.Fatalf("unexpected item resolver request: %s %s", r.Method, r.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"code":1,"msg":"success","data":{"user":{"nickname":"测试店铺","token":"ITEMSHOP","link":"https://example.invalid/shop/ITEMSHOP"}}}`)),
				Request:    r,
			}, nil
		})}
	}
	t.Cleanup(func() { ldxpItemHTTPClient = previousClient })

	got, err := ParseShopURL("https://pay.ldxp.cn/item/9l814h")
	if err != nil {
		t.Fatalf("parse item url: %v", err)
	}
	if got.Platform != storage.ShopPlatformLDXP {
		t.Fatalf("platform = %q", got.Platform)
	}
	if got.BaseURL != "https://pay.ldxp.cn" {
		t.Fatalf("base url = %q", got.BaseURL)
	}
	if got.SiteURL != "https://pay.ldxp.cn/shop/ITEMSHOP" {
		t.Fatalf("site url = %q", got.SiteURL)
	}
	if got.Token != "ITEMSHOP" {
		t.Fatalf("token = %q", got.Token)
	}
	if got.Name != "测试店铺" {
		t.Fatalf("name = %q", got.Name)
	}
	if got.GoodsKey != "9l814h" {
		t.Fatalf("goods key = %q", got.GoodsKey)
	}
}

func TestParseShopURLRejectsUntrustedLDXPItemURLBeforeRemoteRequest(t *testing.T) {
	if _, err := ParseShopURL("http://pay.ldxp.cn/item/9l814h"); err == nil {
		t.Fatal("HTTP item URL unexpectedly accepted")
	}
	if _, err := ParseShopURL("https://example.invalid/item/9l814h"); err == nil {
		t.Fatal("untrusted item host unexpectedly accepted")
	}
}

func TestParseShopURLRejectsUnsupportedPath(t *testing.T) {
	if _, err := ParseShopURL("https://pay.ldxp.cn/not-shop/7FCVUA4X"); err == nil {
		t.Fatal("expected unsupported path error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
