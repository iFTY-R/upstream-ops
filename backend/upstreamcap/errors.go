package upstreamcap

import (
	"errors"
	"fmt"
	"strings"
)

var errNilChannelService = errors.New("channel service is nil")

const (
	ErrCapabilityUnsupported   = "capability_unsupported"
	ErrAuthFailed              = "auth_failed"
	ErrSessionExpired          = "session_expired"
	ErrUpstreamUnreachable     = "upstream_unreachable"
	ErrUpstreamUnauthorized    = "upstream_unauthorized"
	ErrUpstreamForbidden       = "upstream_forbidden"
	ErrUpstreamRateLimited     = "upstream_rate_limited"
	ErrUpstreamBadResponse     = "upstream_bad_response"
	ErrUpstreamVersionMismatch = "upstream_version_mismatch"
	ErrInvalidChannelConfig    = "invalid_channel_config"
	ErrNotFound                = "not_found"
	ErrUnknown                 = "unknown"
)

type CapabilityError struct {
	Code       string
	Capability string
	ChannelID  uint
	Temporary  bool
	Cause      error
}

func (e *CapabilityError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.Code
	if e.Capability != "" {
		msg = e.Capability + ": " + msg
	}
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *CapabilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func Unsupported(channelID uint, capability string) error {
	return &CapabilityError{
		Code:       ErrCapabilityUnsupported,
		Capability: capability,
		ChannelID:  channelID,
		Temporary:  false,
		Cause:      fmt.Errorf("渠道不支持能力 %s", capability),
	}
}

func NormalizeError(channelID uint, capability string, err error) error {
	if err == nil {
		return nil
	}
	var capErr *CapabilityError
	if errors.As(err, &capErr) {
		return err
	}
	code, temporary := classifyError(err)
	return &CapabilityError{
		Code:       code,
		Capability: capability,
		ChannelID:  channelID,
		Temporary:  temporary,
		Cause:      err,
	}
}

func classifyError(err error) (string, bool) {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "login") || strings.Contains(msg, "登录失败"):
		return ErrAuthFailed, false
	case strings.Contains(msg, "not support") || strings.Contains(msg, "不支持"):
		return ErrCapabilityUnsupported, false
	case strings.Contains(msg, "token 已失效") || strings.Contains(msg, "session") && strings.Contains(msg, "expired"):
		return ErrSessionExpired, true
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "401"):
		return ErrUpstreamUnauthorized, false
	case strings.Contains(msg, "forbidden") || strings.Contains(msg, "403"):
		return ErrUpstreamForbidden, false
	case strings.Contains(msg, "rate limit") || strings.Contains(msg, "429"):
		return ErrUpstreamRateLimited, true
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") || strings.Contains(msg, "connection") || strings.Contains(msg, "connectex"):
		return ErrUpstreamUnreachable, true
	case strings.Contains(msg, "decode") || strings.Contains(msg, "json") || strings.Contains(msg, "bad response"):
		return ErrUpstreamBadResponse, true
	case strings.Contains(msg, "not found") || strings.Contains(msg, "找不到"):
		return ErrNotFound, false
	case strings.Contains(msg, "配置") || strings.Contains(msg, "credential") || strings.Contains(msg, "decrypt"):
		return ErrInvalidChannelConfig, false
	default:
		return ErrUnknown, true
	}
}
