package upstreamcap

import (
	"context"
	"errors"
	"strings"

	"github.com/ifty-r/upstream-ops/backend/channel"
	"github.com/ifty-r/upstream-ops/backend/connector"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

type Service struct {
	channels *channel.Service
}

func NewService(channels *channel.Service) *Service {
	return &Service{channels: channels}
}

func (s *Service) GetBalance(ctx context.Context, channelID uint) (*connector.BalanceResult, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapBalance)
	if err != nil {
		return nil, err
	}
	out, err := conn.GetBalance(ctx, resolved, session)
	return out, NormalizeError(c.ID, CapBalance, err)
}

func (s *Service) GetCosts(ctx context.Context, channelID uint) (*connector.CostResult, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapCosts)
	if err != nil {
		return nil, err
	}
	out, err := conn.GetCosts(ctx, resolved, session)
	return out, NormalizeError(c.ID, CapCosts, err)
}

func (s *Service) GetRates(ctx context.Context, channelID uint) ([]connector.RateResult, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapRates)
	if err != nil {
		return nil, err
	}
	out, err := conn.GetRates(ctx, resolved, session)
	return out, NormalizeError(c.ID, CapRates, err)
}

func (s *Service) GetAnnouncements(ctx context.Context, channelID uint) ([]connector.AnnouncementResult, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapAnnouncements)
	if err != nil {
		return nil, err
	}
	out, err := conn.GetAnnouncements(ctx, resolved, session)
	return out, NormalizeError(c.ID, CapAnnouncements, err)
}

func (s *Service) ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapAPIKeys)
	if err != nil {
		return nil, err
	}
	page, err := conn.ListAPIKeys(ctx, resolved, session, query)
	return page, s.normalizeAndClear(c.ID, CapAPIKeys, err)
}

func (s *Service) ListAPIKeyGroups(ctx context.Context, channelID uint) ([]connector.APIKeyGroup, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapAPIKeyGroups)
	if err != nil {
		return nil, err
	}
	groups, err := conn.ListAPIKeyGroups(ctx, resolved, session)
	return groups, s.normalizeAndClear(c.ID, CapAPIKeyGroups, err)
}

func (s *Service) ListModels(ctx context.Context, channelID uint) ([]connector.ModelOption, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapModels)
	if err != nil {
		return nil, err
	}
	models, err := conn.ListModels(ctx, resolved, session)
	return models, s.normalizeAndClear(c.ID, CapModels, err)
}

func (s *Service) CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapAPIKeyCreate)
	if err != nil {
		return nil, err
	}
	key, err := conn.CreateAPIKey(ctx, resolved, session, req)
	return key, s.normalizeAndClear(c.ID, CapAPIKeyCreate, err)
}

func (s *Service) UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapAPIKeyUpdate)
	if err != nil {
		return nil, err
	}
	key, err := conn.UpdateAPIKey(ctx, resolved, session, keyID, req)
	return key, s.normalizeAndClear(c.ID, CapAPIKeyUpdate, err)
}

func (s *Service) DeleteAPIKey(ctx context.Context, channelID uint, keyID int64) error {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapAPIKeyDelete)
	if err != nil {
		return err
	}
	err = conn.DeleteAPIKey(ctx, resolved, session, keyID)
	return s.normalizeAndClear(c.ID, CapAPIKeyDelete, err)
}

func (s *Service) RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapAPIKeyReveal)
	if err != nil {
		return "", err
	}
	key, err := conn.RevealAPIKey(ctx, resolved, session, keyID)
	return key, s.normalizeAndClear(c.ID, CapAPIKeyReveal, err)
}

func (s *Service) GetRechargeInfo(ctx context.Context, channelID uint) (*connector.RechargeInfo, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapRecharge)
	if err != nil {
		return nil, err
	}
	info, err := conn.GetRechargeInfo(ctx, resolved, session)
	return info, s.normalizeAndClear(c.ID, CapRecharge, err)
}

func (s *Service) CreateRecharge(ctx context.Context, channelID uint, req connector.RechargeRequest) (*connector.RechargeLaunch, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapRecharge)
	if err != nil {
		return nil, err
	}
	launch, err := conn.CreateRecharge(ctx, resolved, session, req)
	return launch, s.normalizeAndClear(c.ID, CapRecharge, err)
}

func (s *Service) GetSubscriptionInfo(ctx context.Context, channelID uint) (*connector.SubscriptionInfo, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapSubscription)
	if err != nil {
		return nil, err
	}
	info, err := conn.GetSubscriptionInfo(ctx, resolved, session)
	return info, s.normalizeAndClear(c.ID, CapSubscription, err)
}

func (s *Service) CreateSubscription(ctx context.Context, channelID uint, req connector.SubscriptionRequest) (*connector.SubscriptionLaunch, error) {
	req.PlanID = strings.TrimSpace(req.PlanID)
	req.PaymentMethod = strings.TrimSpace(req.PaymentMethod)
	if req.PlanID == "" {
		return nil, NormalizeError(channelID, CapSubscription, errors.New("请选择订阅套餐"))
	}
	if req.PaymentMethod == "" {
		return nil, NormalizeError(channelID, CapSubscription, errors.New("请选择支付方式"))
	}
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapSubscription)
	if err != nil {
		return nil, err
	}
	launch, err := conn.CreateSubscription(ctx, resolved, session, req)
	return launch, s.normalizeAndClear(c.ID, CapSubscription, err)
}

func (s *Service) GetSubscriptionUsage(ctx context.Context, channelID uint) (*connector.SubscriptionUsageInfo, error) {
	c, resolved, conn, session, err := s.prepare(ctx, channelID, CapSubscriptionUsage)
	if err != nil {
		return nil, err
	}
	info, err := conn.GetSubscriptionUsage(ctx, resolved, session)
	return info, s.normalizeAndClear(c.ID, CapSubscriptionUsage, err)
}

func (s *Service) prepare(ctx context.Context, channelID uint, capability string) (*storage.Channel, *connector.Channel, connector.Connector, *connector.AuthSession, error) {
	if s == nil || s.channels == nil {
		return nil, nil, nil, nil, NormalizeError(channelID, capability, errNilChannelService)
	}
	c, err := s.channels.Channels.FindByID(channelID)
	if err != nil {
		return nil, nil, nil, nil, NormalizeError(channelID, capability, err)
	}
	if !supportsCapability(c.Type, capability) {
		return nil, nil, nil, nil, Unsupported(c.ID, capability)
	}
	resolved, err := s.channels.Resolve(ctx, c)
	if err != nil {
		return nil, nil, nil, nil, NormalizeError(channelID, capability, err)
	}
	conn, err := connector.For(connector.ChannelType(c.Type))
	if err != nil {
		return nil, nil, nil, nil, NormalizeError(channelID, capability, err)
	}
	s.channels.ApplyHTTPConfig(conn)
	s.channels.ApplyProxy(conn, resolved)
	session, err := s.channels.EnsureSession(ctx, c, resolved, conn)
	if err != nil {
		return nil, nil, nil, nil, NormalizeError(channelID, capability, err)
	}
	return c, resolved, conn, session, nil
}

func (s *Service) normalizeAndClear(channelID uint, capability string, err error) error {
	if err != nil {
		return NormalizeError(channelID, capability, err)
	}
	_ = s.channels.Channels.SetLastError(channelID, "")
	return nil
}
