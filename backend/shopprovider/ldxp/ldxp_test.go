package ldxp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/shopprovider"
)

func TestClientReadsInfoCategoriesGoodsAndPrice(t *testing.T) {
	mux := http.NewServeMux()
	var priceRequest map[string]any
	mux.HandleFunc("/shopApi/Shop/info", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]any{
			"nickname":    "测试店铺",
			"link":        "https://example.invalid/shop/TOKEN",
			"avatar":      "https://example.invalid/a.png",
			"goods_count": 2,
		})
	})
	mux.HandleFunc("/shopApi/Shop/categoryList", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, []map[string]any{
			{"id": 10, "name": "GPT", "image": "", "goods_count": 2},
		})
	})
	mux.HandleFunc("/shopApi/Shop/goodsList", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]any{
			"total": 1,
			"list": []map[string]any{
				{
					"goods_key":      "abc",
					"goods_type":     "card",
					"name":           "GPT Pro",
					"link":           "https://example.invalid/goods/abc",
					"price":          1.23,
					"market_price":   2.34,
					"contact_format": "text",
					"category":       map[string]any{"id": 10, "name": "GPT"},
					"extend":         map[string]any{"stock_count": 8, "limit_count": 1, "send_order": 0},
				},
			},
		})
	})
	mux.HandleFunc("/shopApi/Shop/getUserChannel", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, []map[string]any{
			{"id": 6, "name": "停用渠道", "show_name": "停用", "status": 0, "custom_status": 1, "rate": 1},
			{"id": 7, "name": "支付宝电脑收款", "show_name": "支付宝", "status": 1, "custom_status": 1, "rate": 3},
			{"id": 8, "name": "微信收款", "show_name": "微信", "status": 1, "custom_status": 1, "rate": 2},
		})
	})
	mux.HandleFunc("/shopApi/Shop/getGoodsPrice", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&priceRequest); err != nil {
			t.Fatalf("decode price request: %v", err)
		}
		writeEnvelope(t, w, map[string]any{
			"original_amount": 2.46,
			"total_amount":    2.53,
			"fee":             0.07,
			"fee_payer":       1,
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New()
	target := shopprovider.Target{BaseURL: server.URL, Token: "TOKEN"}
	info, err := client.Info(context.Background(), target)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Name != "测试店铺" || info.GoodsCount != 2 {
		t.Fatalf("info = %+v", info)
	}
	categories, err := client.Categories(context.Background(), target, shopprovider.CategoryRequest{GoodsType: "card"})
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	if len(categories) != 1 || categories[0].Name != "GPT" {
		t.Fatalf("categories = %+v", categories)
	}
	goods, err := client.Goods(context.Background(), target, shopprovider.GoodsRequest{GoodsType: "card"})
	if err != nil {
		t.Fatalf("goods: %v", err)
	}
	if goods.Total != 1 || len(goods.List) != 1 || goods.List[0].GoodsKey != "abc" || goods.List[0].StockCount != 8 {
		t.Fatalf("goods = %+v", goods)
	}
	channels, err := client.PaymentChannels(context.Background(), target)
	if err != nil {
		t.Fatalf("payment channels: %v", err)
	}
	if len(channels) != 2 || channels[0].ID != 7 || channels[0].DisplayName != "支付宝" || channels[1].ID != 8 {
		t.Fatalf("payment channels = %+v", channels)
	}
	price, err := client.Price(context.Background(), target, shopprovider.PriceRequest{GoodsKey: "abc", Quantity: 2, ChannelID: 7})
	if err != nil {
		t.Fatalf("price: %v", err)
	}
	if price.OriginalAmount != 2.46 || price.TotalAmount != 2.53 || price.Fee != 0.07 || price.FeePayer != 1 {
		t.Fatalf("price = %+v", price)
	}
	if priceRequest["goods_key"] != "abc" || priceRequest["quantity"] != float64(2) || priceRequest["channel_id"] != float64(7) {
		t.Fatalf("price request = %#v", priceRequest)
	}
}

func TestClientRetriesACWSCV2Challenge(t *testing.T) {
	const arg1 = "82AD28C760AEED5D15E41628E2A744590DAAA028"
	value, ok := acwSCV2Value(arg1)
	if !ok {
		t.Fatal("compute acw_sc__v2 value")
	}
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/shopApi/Shop/info", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><script>var arg1='" + arg1 + "';document.cookie='acw_sc__v2='+v</script></html>"))
			return
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "acw_sc__v2="+value) {
			t.Fatalf("cookie = %q, want acw_sc__v2=%s", got, value)
		}
		writeEnvelope(t, w, map[string]any{
			"nickname":    "挑战后店铺",
			"link":        "https://example.invalid/shop/TOKEN",
			"goods_count": 1,
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New()
	info, err := client.Info(context.Background(), shopprovider.Target{BaseURL: server.URL, Token: "TOKEN"})
	if err != nil {
		t.Fatalf("info after challenge: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if info.Name != "挑战后店铺" {
		t.Fatalf("info = %+v", info)
	}
}

func TestClientEstablishesSessionBeforeAPIRequest(t *testing.T) {
	var infoCalls, warmUpCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/shop/TOKEN", func(w http.ResponseWriter, r *http.Request) {
		warmUpCalls++
		if r.Header.Get("Sec-Fetch-Mode") != "navigate" || r.Header.Get("Sec-Fetch-Dest") != "document" {
			t.Fatalf("navigation headers are missing: %+v", r.Header)
		}
		http.SetCookie(w, &http.Cookie{Name: "shop_session", Value: "ready", Path: "/"})
		_, _ = w.Write([]byte("<html>shop</html>"))
	})
	mux.HandleFunc("/shopApi/Shop/info", func(w http.ResponseWriter, r *http.Request) {
		infoCalls++
		if r.Header.Get("User-Agent") == legacyOpsUserAgent {
			t.Fatal("ldxp request should not use the legacy Ops user agent")
		}
		if r.Header.Get("Accept-Language") == "" || r.Header.Get("Origin") != "http://"+r.Host {
			t.Fatalf("browser headers are missing: %+v", r.Header)
		}
		if r.Referer() != "http://"+r.Host+"/shop/TOKEN" {
			t.Fatalf("referer = %q", r.Referer())
		}
		if r.Header.Get("Sec-Fetch-Mode") != "cors" || r.Header.Get("Sec-Fetch-Site") != "same-origin" {
			t.Fatalf("fetch headers are missing: %+v", r.Header)
		}
		if _, err := r.Cookie("shop_session"); err != nil {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><title>403 Forbidden</title><p>Denied by http_bot_simple</p></html>"))
			return
		}
		writeEnvelope(t, w, map[string]any{"nickname": "会话恢复店铺", "goods_count": 1})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New()
	info, err := client.Info(context.Background(), shopprovider.Target{
		BaseURL: server.URL,
		SiteURL: server.URL + "/shop/TOKEN",
		Token:   "TOKEN",
	})
	if err != nil {
		t.Fatalf("info after session warm up: %v", err)
	}
	if info.Name != "会话恢复店铺" || infoCalls != 1 || warmUpCalls != 1 {
		t.Fatalf("info = %+v, info calls = %d, warm-up calls = %d", info, infoCalls, warmUpCalls)
	}
}

func TestClientWarmsOriginOnceAcrossShopURLs(t *testing.T) {
	var warmUpCalls, infoCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/shop/", func(w http.ResponseWriter, r *http.Request) {
		warmUpCalls++
		http.SetCookie(w, &http.Cookie{Name: "shop_session", Value: "ready", Path: "/"})
		_, _ = w.Write([]byte("<html>shop</html>"))
	})
	mux.HandleFunc("/shopApi/Shop/info", func(w http.ResponseWriter, r *http.Request) {
		infoCalls++
		if _, err := r.Cookie("shop_session"); err != nil {
			t.Fatalf("shared origin session cookie missing: %v", err)
		}
		writeEnvelope(t, w, map[string]any{"nickname": "shared"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New()
	for _, token := range []string{"A", "B"} {
		if _, err := client.Info(context.Background(), shopprovider.Target{
			BaseURL: server.URL,
			SiteURL: server.URL + "/shop/" + token,
			Token:   token,
		}); err != nil {
			t.Fatalf("info %s: %v", token, err)
		}
	}
	if warmUpCalls != 1 || infoCalls != 2 {
		t.Fatalf("warm-up calls = %d, info calls = %d; want 1 and 2", warmUpCalls, infoCalls)
	}
}

func TestClientSpacesRequestsWithinSharedSession(t *testing.T) {
	var requestTimes []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestTimes = append(requestTimes, time.Now())
		writeEnvelope(t, w, map[string]any{"nickname": "paced"})
	}))
	defer server.Close()

	client := New()
	client.SetHTTPConfig(shopprovider.HTTPConfig{RequestInterval: 50 * time.Millisecond})
	for _, token := range []string{"A", "B"} {
		if _, err := client.Info(context.Background(), shopprovider.Target{BaseURL: server.URL, Token: token}); err != nil {
			t.Fatalf("info %s: %v", token, err)
		}
	}
	if len(requestTimes) != 2 {
		t.Fatalf("request times = %d, want 2", len(requestTimes))
	}
	if spacing := requestTimes[1].Sub(requestTimes[0]); spacing < 40*time.Millisecond {
		t.Fatalf("request spacing = %s, want at least 40ms", spacing)
	}
}

func TestClientNegotiatesHTTP2ForTLSRequests(t *testing.T) {
	var protocolMajor int
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocolMajor = r.ProtoMajor
		writeEnvelope(t, w, map[string]any{"nickname": "HTTP/1.1 店铺"})
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client := New()
	client.http.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- test server certificate.
	if _, err := client.Info(context.Background(), shopprovider.Target{BaseURL: server.URL, Token: "TOKEN"}); err != nil {
		t.Fatalf("info: %v", err)
	}
	if protocolMajor != 2 {
		t.Fatalf("protocol major = %d, want 2", protocolMajor)
	}
}

func TestClientReportsHTMLBlockPageClearly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/shopApi/Shop/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><p>Denied by http_bot_simple</p></html>"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	_, err := New().Info(context.Background(), shopprovider.Target{BaseURL: server.URL, Token: "TOKEN"})
	if err == nil {
		t.Fatal("expected HTML block page error")
	}
	message := err.Error()
	if !strings.Contains(message, "upstream returned HTML") || !strings.Contains(message, "http 200") || !strings.Contains(message, "ESA rejected the request as automated") {
		t.Fatalf("error = %q", message)
	}
	var blocked *shopprovider.UpstreamBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error type = %T, want *shopprovider.UpstreamBlockedError", err)
	}
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code": 1,
		"msg":  "success",
		"data": data,
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}
}
