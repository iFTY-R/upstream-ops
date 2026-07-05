package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"github.com/ifty-r/upstream-ops/backend/upstreamcap"
)

type upstreamCapStub struct {
	matrix *upstreamcap.CapabilityMatrix
	id     uint
}

func (s *upstreamCapStub) Matrix(ctx context.Context, channelID uint) (*upstreamcap.CapabilityMatrix, error) {
	s.id = channelID
	return s.matrix, nil
}

func TestChannelCapabilitiesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := &upstreamCapStub{matrix: &upstreamcap.CapabilityMatrix{
		ChannelID:   7,
		ChannelType: storage.ChannelTypeNewAPI,
		Level:       upstreamcap.LevelFullControl,
		Capabilities: []upstreamcap.CapabilityItem{
			{Key: upstreamcap.CapAPIKeys, Supported: true, Required: true},
			{Key: upstreamcap.CapSubscription, Supported: false, Message: "NewAPI 不支持订阅购买"},
		},
	}}
	router := gin.New()
	registerChannels(router.Group("/api"), &Deps{UpstreamCap: stub})

	req := httptest.NewRequest(http.MethodGet, "/api/channels/7/capabilities", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if stub.id != 7 {
		t.Fatalf("channel id = %d, want 7", stub.id)
	}
	var resp struct {
		Data upstreamcap.CapabilityMatrix `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Level != upstreamcap.LevelFullControl || resp.Data.ChannelType != storage.ChannelTypeNewAPI {
		t.Fatalf("matrix = %#v", resp.Data)
	}
}

func TestChannelCapabilitiesEndpointRequiresService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	registerChannels(router.Group("/api"), &Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/channels/1/capabilities", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
}
