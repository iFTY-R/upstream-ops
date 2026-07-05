package upstreamcap

import (
	"context"

	"github.com/ifty-r/upstream-ops/backend/connector"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

const (
	CapBalance           = "balance"
	CapCosts             = "costs"
	CapRates             = "rates"
	CapAnnouncements     = "announcements"
	CapAPIKeys           = "api_keys"
	CapAPIKeyGroups      = "api_key_groups"
	CapAPIKeyCreate      = "api_key_create"
	CapAPIKeyUpdate      = "api_key_update"
	CapAPIKeyDelete      = "api_key_delete"
	CapAPIKeyReveal      = "api_key_reveal"
	CapRecharge          = "recharge"
	CapSubscription      = "subscription"
	CapSubscriptionUsage = "subscription_usage"
	CapOpenAIProbe       = "probe_openai_compatible"
)

type ObserveCapability interface {
	GetBalance(ctx context.Context, channelID uint) (*connector.BalanceResult, error)
	GetCosts(ctx context.Context, channelID uint) (*connector.CostResult, error)
	GetRates(ctx context.Context, channelID uint) ([]connector.RateResult, error)
	GetAnnouncements(ctx context.Context, channelID uint) ([]connector.AnnouncementResult, error)
}

type APIKeyCapability interface {
	ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error)
	CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error)
	UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error)
	DeleteAPIKey(ctx context.Context, channelID uint, keyID int64) error
	RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error)
}

type APIKeyControlCapability interface {
	ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error)
	CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error)
	UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error)
	RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error)
}

type GroupCapability interface {
	ListAPIKeyGroups(ctx context.Context, channelID uint) ([]connector.APIKeyGroup, error)
}

type RechargeCapability interface {
	GetRechargeInfo(ctx context.Context, channelID uint) (*connector.RechargeInfo, error)
	CreateRecharge(ctx context.Context, channelID uint, req connector.RechargeRequest) (*connector.RechargeLaunch, error)
}

type SubscriptionCapability interface {
	GetSubscriptionInfo(ctx context.Context, channelID uint) (*connector.SubscriptionInfo, error)
	CreateSubscription(ctx context.Context, channelID uint, req connector.SubscriptionRequest) (*connector.SubscriptionLaunch, error)
	GetSubscriptionUsage(ctx context.Context, channelID uint) (*connector.SubscriptionUsageInfo, error)
}

type ProbeCapability interface {
	ProbeOpenAICompatible(ctx context.Context, channelID uint, apiKey string, req ProbeRequest) (*ProbeResult, error)
}

type CapabilityLevel string

const (
	LevelFullControl CapabilityLevel = "full_control"
	LevelObserveOnly CapabilityLevel = "observe_only"
	LevelSuggestOnly CapabilityLevel = "suggest_only"
	LevelUnsupported CapabilityLevel = "unsupported"
	LevelDegraded    CapabilityLevel = "degraded"
)

type CapabilityMatrix struct {
	ChannelID    uint                `json:"channel_id"`
	ChannelType  storage.ChannelType `json:"channel_type"`
	Level        CapabilityLevel     `json:"level"`
	Message      string              `json:"message,omitempty"`
	Capabilities []CapabilityItem    `json:"capabilities"`
}

type CapabilityItem struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Supported bool   `json:"supported"`
	Required  bool   `json:"required"`
	Message   string `json:"message,omitempty"`
}
