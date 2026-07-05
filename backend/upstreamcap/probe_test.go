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
	if result == nil || !result.Success || result.Code != "ok" {
		t.Fatalf("result = %#v, want success ok", result)
	}
}

func TestProbeOpenAICompatibleUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	result, err := probeOpenAICompatible(context.Background(), server.URL, "", "sk-test", ProbeRequest{Timeout: time.Second})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result == nil || result.Success || result.Code != "unauthorized" {
		t.Fatalf("result = %#v, want unauthorized failure", result)
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
	if result == nil || result.Success || result.Code != "upstream_error" {
		t.Fatalf("result = %#v, want upstream error failure", result)
	}
}

func TestProbeOpenAICompatibleRequiresAPIKey(t *testing.T) {
	_, err := probeOpenAICompatible(context.Background(), "https://example.invalid", "", "", ProbeRequest{})
	if err == nil {
		t.Fatalf("err = nil, want api key error")
	}
}
