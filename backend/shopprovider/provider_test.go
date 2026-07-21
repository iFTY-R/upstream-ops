package shopprovider

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
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
    mux := http.NewServeMux()
    mux.HandleFunc("/shopApi/Shop/goodsInfo", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            t.Fatalf("method = %s", r.Method)
        }
        _ = json.NewEncoder(w).Encode(map[string]any{
            "code": 1,
            "msg":  "success",
            "data": map[string]any{
                "user": map[string]any{
                    "nickname": "测试店铺",
                    "token":    "ITEMSHOP",
                    "link":     "https://example.invalid/shop/ITEMSHOP",
                },
            },
        })
    })
    server := httptest.NewServer(mux)
    defer server.Close()

    got, err := ParseShopURL(server.URL + "/item/9l814h")
    if err != nil {
        t.Fatalf("parse item url: %v", err)
    }
    if got.Platform != storage.ShopPlatformLDXP {
        t.Fatalf("platform = %q", got.Platform)
    }
    if got.BaseURL != server.URL {
        t.Fatalf("base url = %q", got.BaseURL)
    }
    if got.SiteURL != "https://example.invalid/shop/ITEMSHOP" {
        t.Fatalf("site url = %q", got.SiteURL)
    }
    if got.Token != "ITEMSHOP" {
        t.Fatalf("token = %q", got.Token)
    }
    if got.Name != "测试店铺" {
        t.Fatalf("name = %q", got.Name)
    }
}

func TestParseShopURLRejectsUnsupportedPath(t *testing.T) {
    if _, err := ParseShopURL("https://pay.ldxp.cn/not-shop/7FCVUA4X"); err == nil {
        t.Fatal("expected unsupported path error")
    }
}
