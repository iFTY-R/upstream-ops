package upstreamcap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeOpenAICompatibleSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok"}`))
	}))
	defer server.Close()

	result, err := probeOpenAICompatible(context.Background(), server.URL, "", "sk-test", ProbeRequest{Model: "gpt-4o-mini", Timeout: time.Second})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result == nil || !result.Success || result.Code != ProbeCodeOK {
		t.Fatalf("result = %#v, want success ok", result)
	}
}

func TestProbeOpenAICompatibleUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream unauthorized"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	result, err := probeOpenAICompatible(context.Background(), server.URL, "", "sk-test", ProbeRequest{Timeout: time.Second})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result == nil || result.Success || result.Code != ProbeCodeUpstreamUnauthorized {
		t.Fatalf("result = %#v, want upstream unauthorized failure", result)
	}
}

func TestProbeOpenAICompatibleProbeKeyUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"令牌无效"}}`))
	}))
	defer server.Close()

	result, err := probeOpenAICompatible(context.Background(), server.URL, "", "sk-test", ProbeRequest{Timeout: time.Second})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result == nil || result.Success || result.Code != ProbeCodeProbeKeyUnauthorized {
		t.Fatalf("result = %#v, want probe key unauthorized failure", result)
	}
}

func TestProbeOpenAICompatibleForbiddenCodes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "ip forbidden", body: `{"error":{"message":"您的 IP 不在令牌允许访问的列表中"}}`, want: ProbeCodeProbeKeyIPForbidden},
		{name: "group forbidden", body: `{"error":{"message":"无权访问 fast 分组"}}`, want: ProbeCodeProbeKeyGroupForbidden},
		{name: "model forbidden", body: `{"error":{"message":"This token has no access to model gpt-5.4"}}`, want: ProbeCodeProbeModelForbidden},
		{name: "upstream forbidden", body: `{"error":{"message":"upstream provider forbidden"}}`, want: ProbeCodeUpstreamForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			result, err := probeOpenAICompatible(context.Background(), server.URL, "", "sk-test", ProbeRequest{Timeout: time.Second})
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if result == nil || result.Success || result.Code != tt.want {
				t.Fatalf("result = %#v, want %s", result, tt.want)
			}
		})
	}
}

func TestProbeOpenAICompatibleErrorObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"bad upstream"}}`))
	}))
	defer server.Close()

	result, err := probeOpenAICompatible(context.Background(), server.URL, "", "sk-test", ProbeRequest{Timeout: time.Second})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result == nil || result.Success || result.Code != ProbeCodeUpstreamError {
		t.Fatalf("result = %#v, want upstream error failure", result)
	}
}

func TestProbeOpenAICompatibleRequiresAPIKey(t *testing.T) {
	_, err := probeOpenAICompatible(context.Background(), "https://example.invalid", "", "", ProbeRequest{})
	if err == nil {
		t.Fatalf("err = nil, want api key error")
	}
}
