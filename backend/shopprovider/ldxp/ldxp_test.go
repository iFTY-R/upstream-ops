package ldxp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ifty-r/upstream-ops/backend/shopprovider"
)

func TestClientReadsInfoCategoriesGoodsAndPrice(t *testing.T) {
	mux := http.NewServeMux()
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
	mux.HandleFunc("/shopApi/Shop/getGoodsPrice", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]any{"original_amount": 2.46, "total_amount": 2.46})
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
	price, err := client.Price(context.Background(), target, shopprovider.PriceRequest{GoodsKey: "abc", Quantity: 2})
	if err != nil {
		t.Fatalf("price: %v", err)
	}
	if price.TotalAmount != 2.46 {
		t.Fatalf("price = %+v", price)
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
