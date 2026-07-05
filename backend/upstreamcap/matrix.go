package upstreamcap

import (
	"context"

	"github.com/ifty-r/upstream-ops/backend/storage"
)

func (s *Service) Matrix(ctx context.Context, channelID uint) (*CapabilityMatrix, error) {
	if s == nil || s.channels == nil {
		return nil, NormalizeError(channelID, "matrix", errNilChannelService)
	}
	ch, err := s.channels.Channels.FindByID(channelID)
	if err != nil {
		return nil, NormalizeError(channelID, "matrix", err)
	}
	items := capabilityItemsFor(ch.Type)
	matrix := &CapabilityMatrix{
		ChannelID:    ch.ID,
		ChannelType:  ch.Type,
		Level:        levelForItems(items),
		Capabilities: items,
	}
	switch {
	case matrix.Level == LevelUnsupported:
		matrix.Message = "该渠道类型暂未声明可复用能力"
	case matrix.Level == LevelFullControl && hasUnsupportedOptional(items):
		matrix.Message = "核心自动化能力完整，部分非核心能力不支持"
	}
	return matrix, nil
}

func capabilityItemsFor(t storage.ChannelType) []CapabilityItem {
	common := []CapabilityItem{
		{Key: CapBalance, Label: "读取余额", Supported: true},
		{Key: CapCosts, Label: "读取消费", Supported: true},
		{Key: CapRates, Label: "读取倍率", Supported: true},
		{Key: CapAnnouncements, Label: "读取公告", Supported: true},
		{Key: CapAPIKeys, Label: "读取 API Key", Supported: true, Required: true},
		{Key: CapAPIKeyGroups, Label: "读取分组", Supported: true, Required: true},
		{Key: CapAPIKeyCreate, Label: "创建 API Key", Supported: true},
		{Key: CapAPIKeyUpdate, Label: "更新 API Key", Supported: true, Required: true},
		{Key: CapAPIKeyDelete, Label: "删除 API Key", Supported: true},
		{Key: CapAPIKeyReveal, Label: "读取明文 Key", Supported: true, Required: true},
		{Key: CapOpenAIProbe, Label: "OpenAI 兼容探测", Supported: true, Required: true},
	}
	switch t {
	case storage.ChannelTypeSub2API:
		return append(common,
			CapabilityItem{Key: CapRecharge, Label: "充值", Supported: true},
			CapabilityItem{Key: CapSubscription, Label: "订阅购买", Supported: true},
			CapabilityItem{Key: CapSubscriptionUsage, Label: "订阅用量", Supported: true},
		)
	case storage.ChannelTypeNewAPI:
		return append(common,
			CapabilityItem{Key: CapRecharge, Label: "充值", Supported: true},
			CapabilityItem{Key: CapSubscription, Label: "订阅购买", Supported: false, Message: "NewAPI 不支持订阅购买"},
			CapabilityItem{Key: CapSubscriptionUsage, Label: "订阅用量", Supported: false, Message: "NewAPI 不支持订阅用量"},
		)
	default:
		return []CapabilityItem{
			{Key: CapBalance, Label: "读取余额", Supported: false, Message: "未知渠道类型"},
			{Key: CapAPIKeys, Label: "读取 API Key", Supported: false, Required: true, Message: "未知渠道类型"},
		}
	}
}

func supportsCapability(t storage.ChannelType, capability string) bool {
	for _, item := range capabilityItemsFor(t) {
		if item.Key == capability {
			return item.Supported
		}
	}
	return false
}

func levelForItems(items []CapabilityItem) CapabilityLevel {
	if len(items) == 0 {
		return LevelUnsupported
	}
	requiredOK := true
	anySupported := false
	anyRequired := false
	for _, item := range items {
		if item.Supported {
			anySupported = true
		}
		if item.Required {
			anyRequired = true
		}
		if item.Required && !item.Supported {
			requiredOK = false
		}
	}
	switch {
	case anyRequired && requiredOK:
		return LevelFullControl
	case requiredOK:
		return LevelSuggestOnly
	case anySupported:
		return LevelObserveOnly
	default:
		return LevelUnsupported
	}
}

func hasUnsupportedOptional(items []CapabilityItem) bool {
	for _, item := range items {
		if !item.Required && !item.Supported {
			return true
		}
	}
	return false
}
