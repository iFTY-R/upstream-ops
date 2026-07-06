package upstreamcap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ProbeRequest struct {
	Model        string        `json:"model"`
	Timeout      time.Duration `json:"timeout"`
	MaxTokens    int           `json:"max_tokens"`
	EndpointType string        `json:"endpoint_type"`
	GroupName    string        `json:"group_name,omitempty"`
}

type ProbeResult struct {
	Success   bool   `json:"success"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}

const (
	ProbeCodeOK                     = "0"
	ProbeCodeTimeout                = "1001"
	ProbeCodeConnectionError        = "1002"
	ProbeCodeHTTPError              = "1003"
	ProbeCodeProbeKeyUnauthorized   = "2001"
	ProbeCodeProbeKeyIPForbidden    = "2002"
	ProbeCodeProbeKeyGroupForbidden = "2003"
	ProbeCodeProbeModelForbidden    = "2004"
	ProbeCodeUpstreamUnauthorized   = "3001"
	ProbeCodeUpstreamForbidden      = "3002"
	ProbeCodeRateLimited            = "3003"
	ProbeCodeQuotaExhausted         = "3004"
	ProbeCodeModelUnavailable       = "3005"
	ProbeCodeEmptyResponse          = "4001"
	ProbeCodeInvalidJSON            = "4002"
	ProbeCodeUpstreamError          = "4003"
)

func (s *Service) ProbeOpenAICompatible(ctx context.Context, channelID uint, apiKey string, req ProbeRequest) (*ProbeResult, error) {
	if s == nil || s.channels == nil {
		return nil, NormalizeError(channelID, CapOpenAIProbe, errNilChannelService)
	}
	ch, err := s.channels.Channels.FindByID(channelID)
	if err != nil {
		return nil, NormalizeError(channelID, CapOpenAIProbe, err)
	}
	if !supportsCapability(ch.Type, CapOpenAIProbe) {
		return nil, Unsupported(ch.ID, CapOpenAIProbe)
	}
	resolved, err := s.channels.Resolve(ctx, ch)
	if err != nil {
		return nil, NormalizeError(channelID, CapOpenAIProbe, err)
	}
	result, err := probeOpenAICompatible(ctx, resolved.SiteURL, resolved.ProxyURL, apiKey, req)
	return result, NormalizeError(channelID, CapOpenAIProbe, err)
}

func probeOpenAICompatible(ctx context.Context, siteURL, proxyURL, apiKey string, req ProbeRequest) (*ProbeResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("api key is required")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
		"max_tokens": maxTokens,
		"stream":     false,
	})
	if err != nil {
		return nil, err
	}

	client, err := probeHTTPClient(timeout, proxyURL)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(siteURL, "/") + "/v1/chat/completions"
	started := time.Now()
	httpReq, err := http.NewRequestWithContext(probeCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := client.Do(httpReq)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return &ProbeResult{Success: false, Code: probeTransportCode(err), Message: err.Error(), LatencyMS: latency}, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ProbeResult{Success: false, Code: probeHTTPCode(resp.StatusCode, raw), Message: probeHTTPMessage(resp.StatusCode, raw), LatencyMS: latency}, nil
	}
	if len(raw) == 0 {
		return &ProbeResult{Success: false, Code: ProbeCodeEmptyResponse, Message: "探测接口返回空响应", LatencyMS: latency}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return &ProbeResult{Success: false, Code: ProbeCodeInvalidJSON, Message: "探测接口返回非 JSON 响应", LatencyMS: latency}, nil
	}
	if decoded["error"] != nil {
		return &ProbeResult{Success: false, Code: ProbeCodeUpstreamError, Message: probeErrorMessage(decoded["error"]), LatencyMS: latency}, nil
	}
	return &ProbeResult{Success: true, Code: ProbeCodeOK, Message: "探测通过", LatencyMS: latency}, nil
}

func probeHTTPClient(timeout time.Duration, proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

func probeTransportCode(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout") {
		return ProbeCodeTimeout
	}
	if strings.Contains(msg, "connection") || strings.Contains(msg, "connectex") {
		return ProbeCodeConnectionError
	}
	return ProbeCodeHTTPError
}

func probeHTTPCode(status int, body []byte) string {
	text := strings.ToLower(string(body))
	switch status {
	case http.StatusUnauthorized:
		if isProbeKeyUnauthorizedBody(text) {
			return ProbeCodeProbeKeyUnauthorized
		}
		return ProbeCodeUpstreamUnauthorized
	case http.StatusForbidden:
		return probeForbiddenCode(text)
	case http.StatusTooManyRequests:
		return ProbeCodeRateLimited
	}
	if strings.Contains(text, "quota") || strings.Contains(text, "余额") || strings.Contains(text, "额度") {
		return ProbeCodeQuotaExhausted
	}
	if strings.Contains(text, "model") || strings.Contains(text, "模型") {
		return ProbeCodeModelUnavailable
	}
	return fmt.Sprintf("http_%d", status)
}

func probeForbiddenCode(text string) string {
	switch {
	case strings.Contains(text, "ip 不在") ||
		strings.Contains(text, "ip不在") ||
		strings.Contains(text, "client ip") ||
		strings.Contains(text, "allow"):
		return ProbeCodeProbeKeyIPForbidden
	case strings.Contains(text, "无权访问") ||
		strings.Contains(text, "group access denied") ||
		strings.Contains(text, "分组") && strings.Contains(text, "弃用"):
		return ProbeCodeProbeKeyGroupForbidden
	case strings.Contains(text, "no access to model") ||
		strings.Contains(text, "token model") ||
		strings.Contains(text, "model forbidden") ||
		strings.Contains(text, "模型"):
		return ProbeCodeProbeModelForbidden
	default:
		return ProbeCodeUpstreamForbidden
	}
}

func isProbeKeyUnauthorizedBody(text string) bool {
	return strings.Contains(text, "token invalid") ||
		strings.Contains(text, "invalid token") ||
		strings.Contains(text, "invalid_api_key") ||
		strings.Contains(text, "令牌无效") ||
		strings.Contains(text, "令牌已过期") ||
		strings.Contains(text, "令牌额度已用尽") ||
		strings.Contains(text, "tokenstatus")
}

func probeHTTPMessage(status int, body []byte) string {
	msg := strings.TrimSpace(string(body))
	if len([]rune(msg)) > 240 {
		msg = string([]rune(msg)[:240])
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Sprintf("探测接口返回 HTTP %d：%s", status, msg)
}

func probeErrorMessage(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "探测接口返回错误对象"
	}
	msg := strings.TrimSpace(string(raw))
	if len([]rune(msg)) > 240 {
		msg = string([]rune(msg)[:240])
	}
	if msg == "" || msg == "null" {
		return "探测接口返回错误对象"
	}
	return "探测接口返回错误对象：" + msg
}
