package autogroup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ifty-r/upstream-ops/backend/connector"
	"github.com/ifty-r/upstream-ops/backend/notify"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

type ChannelService interface {
	ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error)
	ListAPIKeyGroups(ctx context.Context, channelID uint) ([]connector.APIKeyGroup, error)
	CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error)
	UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error)
	RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error)
}

type Service struct {
	Repo       *storage.AutoGroups
	Channels   *storage.Channels
	Rates      *storage.Rates
	ChannelSvc ChannelService
	Dispatcher *notify.Dispatcher
	Log        *slog.Logger
}

func NewService(repo *storage.AutoGroups, channels *storage.Channels, rates *storage.Rates, channelSvc ChannelService, dispatcher *notify.Dispatcher, log *slog.Logger) *Service {
	return &Service{
		Repo:       repo,
		Channels:   channels,
		Rates:      rates,
		ChannelSvc: channelSvc,
		Dispatcher: dispatcher,
		Log:        log,
	}
}

type PolicyInput struct {
	ChannelID                     uint     `json:"channel_id"`
	Name                          string   `json:"name"`
	Enabled                       bool     `json:"enabled"`
	NotifyEnabled                 bool     `json:"notify_enabled"`
	TargetKeyID                   int64    `json:"target_key_id"`
	TargetKeyName                 string   `json:"target_key_name"`
	ProbeKeyID                    int64    `json:"probe_key_id"`
	ProbeKeyName                  string   `json:"probe_key_name"`
	ProbeModel                    string   `json:"probe_model"`
	ProbeTimeoutSeconds           int      `json:"probe_timeout_seconds"`
	ProbeSuccessCacheMinutes      int      `json:"probe_success_cache_minutes"`
	ProbeFailureRetryMinutes      int      `json:"probe_failure_retry_minutes"`
	ProbeMaxPerRun                int      `json:"probe_max_per_run"`
	IncludeGroups                 []string `json:"include_groups"`
	ExcludeGroups                 []string `json:"exclude_groups"`
	IncludeKeywords               []string `json:"include_keywords"`
	ExcludeKeywords               []string `json:"exclude_keywords"`
	MinRatio                      float64  `json:"min_ratio"`
	MaxRatio                      float64  `json:"max_ratio"`
	FailureThreshold              int      `json:"failure_threshold"`
	CircuitDurationMinutes        int      `json:"circuit_duration_minutes"`
	HalfOpenSuccessThreshold      int      `json:"half_open_success_threshold"`
	MinRatioImprovementPct        float64  `json:"min_ratio_improvement_pct"`
	SwitchCooldownMinutes         int      `json:"switch_cooldown_minutes"`
	ForceSwitchOnCurrentUnhealthy *bool    `json:"force_switch_on_current_unhealthy"`
	KeepCurrentWhenNoAvailable    *bool    `json:"keep_current_when_no_available"`
}

type PolicyView struct {
	storage.AutoGroupPolicy
	Channel    *storage.Channel                `json:"channel,omitempty"`
	Candidates []storage.AutoGroupCandidate    `json:"candidates,omitempty"`
	LatestLog  *storage.AutoGroupEvaluationLog `json:"latest_log,omitempty"`
}

type EvaluationResult struct {
	Policy        storage.AutoGroupPolicy        `json:"policy"`
	Channel       storage.Channel                `json:"channel"`
	TargetKey     *connector.APIKey              `json:"target_key,omitempty"`
	Selected      *CandidateDecision             `json:"selected,omitempty"`
	Candidates    []CandidateDecision            `json:"candidates"`
	EvaluationLog storage.AutoGroupEvaluationLog `json:"evaluation_log"`
	SwitchLog     *storage.AutoGroupSwitchLog    `json:"switch_log,omitempty"`
}

type Summary struct {
	TotalPolicies        int `json:"total_policies"`
	RunningPolicies      int `json:"running_policies"`
	AbnormalPolicies     int `json:"abnormal_policies"`
	CircuitGroups        int `json:"circuit_groups"`
	TodaySwitches        int `json:"today_switches"`
	NoAvailablePolicies  int `json:"no_available_policies"`
	ManualDisabledGroups int `json:"manual_disabled_groups"`
}

type CapabilityItem struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Supported bool   `json:"supported"`
	Message   string `json:"message,omitempty"`
}

type CapabilityMatrix struct {
	ChannelID    uint             `json:"channel_id"`
	ChannelType  string           `json:"channel_type"`
	Level        string           `json:"level"`
	Message      string           `json:"message,omitempty"`
	Capabilities []CapabilityItem `json:"capabilities"`
}

type CandidateDecision struct {
	GroupName          string     `json:"group_name"`
	GroupID            *int64     `json:"group_id,omitempty"`
	Description        string     `json:"description,omitempty"`
	Ratio              float64    `json:"ratio"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason,omitempty"`
	FailureCount       int        `json:"failure_count"`
	SuccessCount       int        `json:"success_count"`
	CircuitOpenUntil   *time.Time `json:"circuit_open_until,omitempty"`
	CircuitOpenedAt    *time.Time `json:"circuit_opened_at,omitempty"`
	RecoveredAt        *time.Time `json:"recovered_at,omitempty"`
	LastProbeAt        *time.Time `json:"last_probe_at,omitempty"`
	LastProbeSuccess   *bool      `json:"last_probe_success,omitempty"`
	LastProbeLatencyMS int64      `json:"last_probe_latency_ms"`
	LastErrorCode      string     `json:"last_error_code,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	ManualDisabled     bool       `json:"manual_disabled"`
}

type probeKey struct {
	ID    int64
	Name  string
	Value string
}

type probeResult struct {
	Success   bool
	Code      string
	Message   string
	LatencyMS int64
}

func (s *Service) ListPolicies() ([]PolicyView, error) {
	policies, err := s.Repo.ListPolicies()
	if err != nil {
		return nil, err
	}
	out := make([]PolicyView, 0, len(policies))
	for _, p := range policies {
		view := s.policyView(p)
		out = append(out, view)
	}
	return out, nil
}

func (s *Service) ReorderPolicies(ids []uint) ([]PolicyView, error) {
	if err := s.Repo.ReorderPolicies(ids); err != nil {
		return nil, err
	}
	return s.ListPolicies()
}

func (s *Service) GetPolicy(id uint) (*PolicyView, error) {
	p, err := s.Repo.FindPolicy(id)
	if err != nil {
		return nil, err
	}
	view := s.policyView(*p)
	return &view, nil
}

func (s *Service) GetPolicyByChannel(channelID uint) (*PolicyView, error) {
	p, err := s.Repo.FindPolicyByChannel(channelID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetPolicy(p.ID)
}

func (s *Service) ListPoliciesByChannel(channelID uint) ([]PolicyView, error) {
	policies, err := s.Repo.ListPoliciesByChannel(channelID)
	if err != nil {
		return nil, err
	}
	out := make([]PolicyView, 0, len(policies))
	for _, p := range policies {
		out = append(out, s.policyView(p))
	}
	return out, nil
}

func (s *Service) GetPolicyByChannelTarget(channelID uint, targetKeyName string) (*PolicyView, error) {
	p, err := s.Repo.FindPolicyByChannelTarget(channelID, emptyAs(strings.TrimSpace(targetKeyName), "auto"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetPolicy(p.ID)
}

func (s *Service) policyView(p storage.AutoGroupPolicy) PolicyView {
	view := PolicyView{AutoGroupPolicy: p}
	if s.Channels != nil {
		if ch, err := s.Channels.FindByID(p.ChannelID); err == nil {
			view.Channel = ch
		}
	}
	if candidates, err := s.Repo.ListCandidates(p.ID); err == nil {
		view.Candidates = candidates
	}
	if logs, _, err := s.Repo.ListEvaluationLogs(p.ID, 1, 1); err == nil && len(logs) > 0 {
		view.LatestLog = &logs[0]
	}
	return view
}

func (s *Service) DetectCapabilities(ctx context.Context, channelID uint) (*CapabilityMatrix, error) {
	ch, err := s.Channels.FindByID(channelID)
	if err != nil {
		return nil, fmt.Errorf("渠道不存在：%w", err)
	}
	matrix := &CapabilityMatrix{
		ChannelID:   ch.ID,
		ChannelType: string(ch.Type),
		Level:       "observe",
	}
	keyItemsOK := false
	groupItemsOK := false
	keyMsg := ""
	groupMsg := ""
	if s.ChannelSvc == nil {
		keyMsg = "渠道服务未初始化"
		groupMsg = "渠道服务未初始化"
	} else {
		if _, err := s.ChannelSvc.ListAPIKeys(ctx, channelID, connector.APIKeyQuery{Page: 1, PageSize: 1}); err != nil {
			keyMsg = err.Error()
		} else {
			keyItemsOK = true
		}
		if groups, err := s.ChannelSvc.ListAPIKeyGroups(ctx, channelID); err != nil {
			groupMsg = err.Error()
		} else {
			groupItemsOK = true
			if len(groups) == 0 {
				groupMsg = "上游当前没有返回可用分组"
			}
		}
	}
	rateOK := false
	if s.Rates != nil {
		if rates, err := s.Rates.ListByChannel(channelID); err == nil && len(rates) > 0 {
			rateOK = true
		}
	}
	fullControl := keyItemsOK && groupItemsOK
	dataProbe := fullControl
	if fullControl {
		matrix.Level = "full"
		matrix.Message = "已确认可读取 Key 和分组，支持自动创建探测 Key、探测候选分组并切换目标 Key。"
	} else if rateOK {
		matrix.Level = "suggest"
		matrix.Message = "已存在倍率数据，但 Key 或分组控制面检测失败，只能辅助观察，不能可靠自动切换。"
	} else {
		matrix.Level = "error"
		matrix.Message = "无法确认 Key 和分组能力，请先检查渠道登录状态或同步倍率。"
	}
	matrix.Capabilities = []CapabilityItem{
		{Key: "list_api_keys", Label: "读取 API Key", Supported: keyItemsOK, Message: keyMsg},
		{Key: "list_groups", Label: "读取分组", Supported: groupItemsOK, Message: groupMsg},
		{Key: "create_probe_key", Label: "创建探测 Key", Supported: fullControl, Message: supportMessage(fullControl, keyMsg, groupMsg)},
		{Key: "update_key_group", Label: "修改 Key 分组", Supported: fullControl, Message: supportMessage(fullControl, keyMsg, groupMsg)},
		{Key: "data_probe", Label: "数据面探测", Supported: dataProbe, Message: supportMessage(dataProbe, keyMsg, groupMsg)},
		{Key: "auto_switch", Label: "自动切换", Supported: fullControl, Message: supportMessage(fullControl, keyMsg, groupMsg)},
	}
	return matrix, nil
}

func supportMessage(ok bool, parts ...string) string {
	if ok {
		return ""
	}
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			return part
		}
	}
	return "依赖能力未通过检测"
}

func (s *Service) Summary() (*Summary, error) {
	policies, err := s.Repo.ListPolicies()
	if err != nil {
		return nil, err
	}
	summary := &Summary{TotalPolicies: len(policies)}
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, p := range policies {
		if p.Enabled {
			summary.RunningPolicies++
		}
		switch p.LastStatus {
		case "failed", "probe_failed":
			summary.AbnormalPolicies++
		case "unavailable":
			summary.AbnormalPolicies++
			summary.NoAvailablePolicies++
		}
		if p.LastSwitchAt != nil && !p.LastSwitchAt.Before(todayStart) {
			summary.TodaySwitches++
		}
		candidates, err := s.Repo.ListCandidates(p.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range candidates {
			if c.Status == "circuit_open" {
				summary.CircuitGroups++
			}
			if c.ManualDisabled {
				summary.ManualDisabledGroups++
			}
		}
	}
	return summary, nil
}

func (s *Service) CreatePolicy(in PolicyInput) (*PolicyView, error) {
	p, err := policyFromInput(nil, in)
	if err != nil {
		return nil, err
	}
	if _, err := s.Channels.FindByID(p.ChannelID); err != nil {
		return nil, fmt.Errorf("渠道不存在：%w", err)
	}
	if err := s.Repo.CreatePolicy(p); err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("该渠道已存在目标 Key %s 的智能分组策略", p.TargetKeyName)
		}
		return nil, err
	}
	return s.GetPolicy(p.ID)
}

func (s *Service) UpdatePolicy(id uint, in PolicyInput) (*PolicyView, error) {
	current, err := s.Repo.FindPolicy(id)
	if err != nil {
		return nil, err
	}
	p, err := policyFromInput(current, in)
	if err != nil {
		return nil, err
	}
	if _, err := s.Channels.FindByID(p.ChannelID); err != nil {
		return nil, fmt.Errorf("渠道不存在：%w", err)
	}
	if err := s.Repo.UpdatePolicy(p); err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("该渠道已存在目标 Key %s 的智能分组策略", p.TargetKeyName)
		}
		return nil, err
	}
	return s.GetPolicy(p.ID)
}

func (s *Service) DeletePolicy(id uint) error {
	return s.Repo.DeletePolicy(id)
}

func (s *Service) SetPolicyEnabled(id uint, enabled bool) (*PolicyView, error) {
	if err := s.Repo.SetPolicyEnabled(id, enabled); err != nil {
		return nil, err
	}
	return s.GetPolicy(id)
}

func (s *Service) EvaluateAllEnabled(ctx context.Context) {
	s.EvaluateAllEnabledWithConcurrency(ctx, 1)
}

func (s *Service) EvaluateAllEnabledWithConcurrency(ctx context.Context, concurrency int) {
	policies, err := s.Repo.ListEnabledPolicies()
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("list auto group policies failed", "err", err)
		}
		return
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	jobs := make(chan storage.AutoGroupPolicy)
	var wg sync.WaitGroup
	var channelLocks sync.Map
	lockForChannel := func(channelID uint) *sync.Mutex {
		lock, _ := channelLocks.LoadOrStore(channelID, &sync.Mutex{})
		return lock.(*sync.Mutex)
	}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				lock := lockForChannel(p.ChannelID)
				lock.Lock()
				_, err := s.EvaluatePolicy(ctx, p.ID)
				lock.Unlock()
				if err != nil && s.Log != nil {
					s.Log.Warn("evaluate auto group policy failed", "policy_id", p.ID, "err", err)
				}
			}
		}()
	}
	for i := range policies {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- policies[i]:
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *Service) EvaluatePolicy(ctx context.Context, policyID uint) (*EvaluationResult, error) {
	p, err := s.Repo.FindPolicy(policyID)
	if err != nil {
		return nil, err
	}
	ch, err := s.Channels.FindByID(p.ChannelID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	result := &EvaluationResult{Policy: *p, Channel: *ch}

	if !p.Enabled {
		log := s.buildEvalLog(p, nil, nil, true, "disabled", "disabled", "策略未启用", 0, 0, 0, 0)
		_ = s.Repo.AppendEvaluationLog(log)
		result.EvaluationLog = *log
		return result, nil
	}

	target, err := s.findTargetKey(ctx, p)
	if err != nil {
		return s.failEvaluation(ctx, result, p, ch, "failed", err, storage.EventAutoGroupPolicyError)
	}
	result.TargetKey = target

	probe, err := s.ensureProbeKey(ctx, p)
	if err != nil {
		return s.failEvaluation(ctx, result, p, ch, "probe_failed", err, storage.EventAutoGroupProbeFailed)
	}

	candidates, err := s.evaluateCandidates(ctx, p, ch, probe, target)
	if err != nil {
		return s.failEvaluation(ctx, result, p, ch, "failed", err, storage.EventAutoGroupPolicyError)
	}
	result.Candidates = candidates
	healthy := healthyCandidates(candidates)
	availableCount := len(healthy)
	circuitCount := countCandidatesByStatus(candidates, "circuit_open")
	currentGroup := currentKeyGroup(target)
	current := findCandidateByGroup(candidates, target, currentGroup)
	if len(healthy) == 0 {
		msg := "没有匹配且可用的候选分组"
		if p.KeepCurrentWhenNoAvailable {
			msg += "，保持当前目标 key 分组不变"
		}
		targetForStatus := keyWithCandidateGroup(target, current)
		log := s.buildEvalLog(p, targetForStatus, nil, false, "unavailable", "all_unavailable", msg, len(candidates), availableCount, circuitCount, 0)
		_ = s.updatePolicyStatus(p, "unavailable", msg, targetForStatus, currentRatio(current, target), nil, &now)
		_ = s.Repo.AppendEvaluationLog(log)
		result.Policy = *p
		result.EvaluationLog = *log
		s.dispatch(ctx, p, ch, storage.EventAutoGroupAllUnavailable, "", "智能分组无可用候选", msg, nil)
		return result, nil
	}

	selected := healthy[0]
	result.Selected = &selected
	currentHealthy := current != nil && current.Status == "healthy"
	if sameGroup(target, selected) {
		msg := "当前目标 key 已在最优分组"
		targetForStatus := keyWithCandidateGroup(target, &selected)
		log := s.buildEvalLog(p, targetForStatus, &selected, true, "ok", "keep_current", msg, len(candidates), availableCount, circuitCount, selected.Ratio)
		_ = s.updatePolicyStatus(p, "ok", "", targetForStatus, selected.Ratio, nil, &now)
		_ = s.Repo.AppendEvaluationLog(log)
		result.Policy = *p
		result.EvaluationLog = *log
		return result, nil
	}

	if currentHealthy {
		improvement := ratioImprovementPct(current.Ratio, selected.Ratio)
		minImprovement := p.MinRatioImprovementPct
		if minImprovement > 0 && improvement < minImprovement {
			msg := fmt.Sprintf("当前分组可用，切换到 %s 的倍率收益 %.2f%% 低于阈值 %.2f%%，保持不变", selected.GroupName, improvement, minImprovement)
			targetForStatus := keyWithCandidateGroup(target, current)
			log := s.buildEvalLog(p, targetForStatus, &selected, true, "kept", "min_improvement_not_met", msg, len(candidates), availableCount, circuitCount, selected.Ratio)
			_ = s.updatePolicyStatus(p, "kept", "", targetForStatus, current.Ratio, nil, &now)
			_ = s.Repo.AppendEvaluationLog(log)
			result.Policy = *p
			result.EvaluationLog = *log
			return result, nil
		}
	}

	if !shouldIgnoreSwitchCooldown(p, currentHealthy) {
		if remaining := switchCooldownRemaining(p, now); remaining > 0 {
			msg := fmt.Sprintf("切换冷却中，剩余约 %s，保持当前分组", formatDurationCN(remaining))
			targetForStatus := keyWithCandidateGroup(target, current)
			log := s.buildEvalLog(p, targetForStatus, &selected, true, "cooldown", "switch_cooldown", msg, len(candidates), availableCount, circuitCount, selected.Ratio)
			_ = s.updatePolicyStatus(p, "cooldown", "", targetForStatus, currentRatio(current, target), nil, &now)
			_ = s.Repo.AppendEvaluationLog(log)
			result.Policy = *p
			result.EvaluationLog = *log
			return result, nil
		}
	}

	selected = s.ensureSwitchProbe(ctx, p, ch, probe, selected, now)
	result.Selected = &selected
	if selected.Status != "healthy" {
		msg := fmt.Sprintf("切换前探测 %s 未通过：%s", selected.GroupName, emptyAs(selected.LastError, selected.Reason))
		targetForStatus := keyWithCandidateGroup(target, current)
		log := s.buildEvalLog(p, targetForStatus, &selected, false, "probe_failed", "pre_switch_probe_failed", msg, len(candidates), availableCount, circuitCount, selected.Ratio)
		_ = s.updatePolicyStatus(p, "probe_failed", msg, targetForStatus, currentRatio(current, target), nil, &now)
		_ = s.Repo.AppendEvaluationLog(log)
		result.Policy = *p
		result.EvaluationLog = *log
		s.dispatch(ctx, p, ch, storage.EventAutoGroupProbeFailed, selected.GroupName, "智能分组切换前探测失败", msg, map[string]any{"group": selected.GroupName})
		return result, nil
	}

	updated, switchLog, err := s.switchTargetKey(ctx, p, ch, target, selected, currentGroup)
	result.SwitchLog = switchLog
	if err != nil {
		_ = s.Repo.MarkCandidateFailure(p.ID, selected.GroupName, p.FailureThreshold, circuitDuration(p), err.Error())
		msg := fmt.Sprintf("切换到 %s 失败：%v", selected.GroupName, err)
		targetForStatus := keyWithCandidateGroup(target, current)
		log := s.buildEvalLog(p, targetForStatus, &selected, false, "failed", "target_update_failed", msg, len(candidates), availableCount, circuitCount, selected.Ratio)
		_ = s.updatePolicyStatus(p, "failed", msg, targetForStatus, currentRatio(current, target), nil, &now)
		_ = s.Repo.AppendEvaluationLog(log)
		result.Policy = *p
		result.EvaluationLog = *log
		s.dispatch(ctx, p, ch, storage.EventAutoGroupTargetUpdateFailed, selected.GroupName, "智能分组切换失败", msg, map[string]any{
			"from_group": currentGroup,
			"to_group":   selected.GroupName,
		})
		return result, err
	}

	_ = s.Repo.ResetCandidateFailure(p.ID, selected.GroupName)
	updated = keyWithCandidateGroup(updated, &selected)
	msg := fmt.Sprintf("已从 %s 切换到 %s", emptyAs(currentGroup, "未识别分组"), selected.GroupName)
	log := s.buildEvalLog(p, updated, &selected, true, "switched", "switch", msg, len(candidates), availableCount, circuitCount, selected.Ratio)
	_ = s.updatePolicyStatus(p, "switched", "", updated, selected.Ratio, &now, &now)
	_ = s.Repo.AppendEvaluationLog(log)
	result.Policy = *p
	result.TargetKey = updated
	result.EvaluationLog = *log
	s.dispatch(ctx, p, ch, storage.EventAutoGroupSwitched, selected.GroupName, "智能分组已切换", msg, map[string]any{
		"from_group": currentGroup,
		"to_group":   selected.GroupName,
		"ratio":      selected.Ratio,
	})
	return result, nil
}

func (s *Service) SetCandidateManualDisabled(policyID, candidateID uint, disabled bool) (*storage.AutoGroupCandidate, error) {
	c, err := s.Repo.FindCandidateByID(policyID, candidateID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("找不到候选分组：%d", candidateID)
	}
	if err := s.Repo.SetCandidateManualDisabled(policyID, candidateID, disabled); err != nil {
		return nil, err
	}
	return s.Repo.FindCandidateByID(policyID, candidateID)
}

func (s *Service) OpenCandidateCircuit(policyID, candidateID uint) (*storage.AutoGroupCandidate, error) {
	p, err := s.Repo.FindPolicy(policyID)
	if err != nil {
		return nil, err
	}
	c, err := s.Repo.FindCandidateByID(policyID, candidateID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("找不到候选分组：%d", candidateID)
	}
	if err := s.Repo.OpenCandidateCircuit(policyID, candidateID, circuitDuration(p), "人工临时熔断"); err != nil {
		return nil, err
	}
	return s.Repo.FindCandidateByID(policyID, candidateID)
}

func (s *Service) ProbeCandidate(ctx context.Context, policyID, candidateID uint) (*storage.AutoGroupCandidate, error) {
	p, err := s.Repo.FindPolicy(policyID)
	if err != nil {
		return nil, err
	}
	ch, err := s.Channels.FindByID(p.ChannelID)
	if err != nil {
		return nil, err
	}
	stored, err := s.Repo.FindCandidateByID(policyID, candidateID)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("找不到候选分组：%d", candidateID)
	}
	if stored.ManualDisabled {
		return nil, fmt.Errorf("候选分组已手动停用，请先恢复后再探测")
	}
	probe, err := s.ensureProbeKey(ctx, p)
	if err != nil {
		return nil, err
	}
	decision := candidateFromStored(*stored)
	decision.Status, decision.Reason = classifyCandidate(p, decision, time.Now())
	if decision.Status == "excluded" || decision.Status == "circuit_open" {
		return stored, nil
	}
	decision = s.probeCandidate(ctx, p, ch, probe, decision)
	return s.Repo.FindCandidate(policyID, decision.GroupName)
}

func (s *Service) ensureSwitchProbe(ctx context.Context, p *storage.AutoGroupPolicy, ch *storage.Channel, probe *probeKey, selected CandidateDecision, now time.Time) CandidateDecision {
	if selected.LastProbeAt != nil && selected.LastProbeSuccess != nil && *selected.LastProbeSuccess && now.Sub(*selected.LastProbeAt) <= time.Minute {
		return selected
	}
	return s.probeCandidate(ctx, p, ch, probe, selected)
}

func (s *Service) ForceSwitchCandidate(ctx context.Context, policyID, candidateID uint) (*EvaluationResult, error) {
	p, err := s.Repo.FindPolicy(policyID)
	if err != nil {
		return nil, err
	}
	ch, err := s.Channels.FindByID(p.ChannelID)
	if err != nil {
		return nil, err
	}
	stored, err := s.Repo.FindCandidateByID(policyID, candidateID)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("找不到候选分组：%d", candidateID)
	}
	if stored.ManualDisabled {
		return nil, fmt.Errorf("候选分组已手动停用")
	}
	switch stored.Status {
	case "", "unknown", "healthy", "half_open":
	default:
		return nil, fmt.Errorf("候选分组当前状态为 %s，不允许强制切换", stored.Status)
	}
	target, err := s.findTargetKey(ctx, p)
	if err != nil {
		result := &EvaluationResult{Policy: *p, Channel: *ch}
		return s.failEvaluation(ctx, result, p, ch, "failed", err, storage.EventAutoGroupPolicyError)
	}
	selected := candidateFromStored(*stored)
	now := time.Now()
	updated, switchLog, err := s.switchTargetKey(ctx, p, ch, target, selected, currentKeyGroup(target))
	result := &EvaluationResult{
		Policy:    *p,
		Channel:   *ch,
		TargetKey: target,
		Selected:  &selected,
		SwitchLog: switchLog,
	}
	if err != nil {
		msg := fmt.Sprintf("强制切换到 %s 失败：%v", selected.GroupName, err)
		log := s.buildEvalLog(p, target, &selected, false, "failed", "force_switch_failed", msg, 1, 0, 0, selected.Ratio)
		_ = s.updatePolicyStatus(p, "failed", msg, target, selected.Ratio, nil, &now)
		_ = s.Repo.AppendEvaluationLog(log)
		result.Policy = *p
		result.EvaluationLog = *log
		return result, err
	}
	updated = keyWithCandidateGroup(updated, &selected)
	msg := fmt.Sprintf("已人工强制切换到 %s", selected.GroupName)
	log := s.buildEvalLog(p, updated, &selected, true, "switched", "force_switch", msg, 1, 1, 0, selected.Ratio)
	_ = s.updatePolicyStatus(p, "switched", "", updated, selected.Ratio, &now, &now)
	_ = s.Repo.AppendEvaluationLog(log)
	result.Policy = *p
	result.TargetKey = updated
	result.EvaluationLog = *log
	s.dispatch(ctx, p, ch, storage.EventAutoGroupSwitched, selected.GroupName, "智能分组已强制切换", msg, map[string]any{
		"from_group": currentKeyGroup(target),
		"to_group":   selected.GroupName,
		"ratio":      selected.Ratio,
		"manual":     true,
	})
	return result, nil
}

func (s *Service) failEvaluation(ctx context.Context, result *EvaluationResult, p *storage.AutoGroupPolicy, ch *storage.Channel, status string, err error, event storage.NotificationEvent) (*EvaluationResult, error) {
	now := time.Now()
	msg := err.Error()
	log := s.buildEvalLog(p, nil, nil, false, status, string(event), msg, 0, 0, 0, 0)
	_ = s.updatePolicyStatus(p, status, msg, nil, 0, nil, &now)
	_ = s.Repo.AppendEvaluationLog(log)
	result.Policy = *p
	result.EvaluationLog = *log
	s.dispatch(ctx, p, ch, event, "", "智能分组评估失败", msg, nil)
	return result, err
}

func (s *Service) evaluateCandidates(ctx context.Context, p *storage.AutoGroupPolicy, ch *storage.Channel, probe *probeKey, target *connector.APIKey) ([]CandidateDecision, error) {
	groups, err := s.ChannelSvc.ListAPIKeyGroups(ctx, p.ChannelID)
	if err != nil {
		return nil, fmt.Errorf("读取上游分组失败：%w", err)
	}
	rates, _ := s.Rates.ListByChannel(p.ChannelID)
	byName := make(map[string]CandidateDecision)
	for _, r := range rates {
		name := strings.TrimSpace(r.ModelName)
		if name == "" {
			continue
		}
		byName[strings.ToLower(name)] = CandidateDecision{
			GroupName:   name,
			Description: r.Description,
			Ratio:       r.Ratio,
			Status:      "unknown",
		}
	}
	for _, g := range groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		c := byName[key]
		c.GroupName = name
		c.GroupID = g.ID
		if strings.TrimSpace(g.Description) != "" {
			c.Description = g.Description
		}
		if g.Ratio > 0 {
			c.Ratio = g.Ratio
		}
		c.Status = "unknown"
		byName[key] = c
	}

	out := make([]CandidateDecision, 0, len(byName))
	now := time.Now()
	currentGroup := currentKeyGroup(target)
	for _, c := range byName {
		prev, err := s.Repo.FindCandidate(p.ID, c.GroupName)
		if err != nil {
			return nil, err
		}
		if prev != nil {
			c.FailureCount = prev.FailureCount
			c.SuccessCount = prev.SuccessCount
			c.CircuitOpenUntil = prev.CircuitOpenUntil
			c.CircuitOpenedAt = prev.CircuitOpenedAt
			c.RecoveredAt = prev.RecoveredAt
			c.LastProbeAt = prev.LastProbeAt
			c.LastProbeSuccess = prev.LastProbeSuccess
			c.LastProbeLatencyMS = prev.LastProbeLatencyMS
			c.LastErrorCode = prev.LastErrorCode
			c.LastError = prev.LastError
			c.ManualDisabled = prev.ManualDisabled
		}
		c.Status, c.Reason = classifyCandidate(p, c, now)
		c = applyProbeCache(p, c, now)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi := probePriority(out[i], target, currentGroup)
		pj := probePriority(out[j], target, currentGroup)
		if pi != pj {
			return pi < pj
		}
		if out[i].Ratio != out[j].Ratio {
			return out[i].Ratio < out[j].Ratio
		}
		return out[i].GroupName < out[j].GroupName
	})
	probesUsed := 0
	probeBudget := probeMaxPerRun(p)
	for i := range out {
		if !needsProbe(out[i]) {
			continue
		}
		if probesUsed >= probeBudget {
			out[i].Status = "unknown"
			out[i].Reason = "超过单轮探测上限，等待下次评估"
			continue
		}
		out[i] = s.probeCandidate(ctx, p, ch, probe, out[i])
		probesUsed++
	}
	for _, c := range out {
		checkedAt := now
		_ = s.Repo.UpsertCandidate(&storage.AutoGroupCandidate{
			PolicyID:           p.ID,
			GroupName:          c.GroupName,
			GroupID:            c.GroupID,
			Description:        c.Description,
			Ratio:              c.Ratio,
			Status:             c.Status,
			Reason:             c.Reason,
			FailureCount:       c.FailureCount,
			SuccessCount:       c.SuccessCount,
			CircuitOpenUntil:   c.CircuitOpenUntil,
			CircuitOpenedAt:    c.CircuitOpenedAt,
			RecoveredAt:        c.RecoveredAt,
			LastCheckedAt:      &checkedAt,
			LastProbeAt:        c.LastProbeAt,
			LastProbeSuccess:   c.LastProbeSuccess,
			LastProbeLatencyMS: c.LastProbeLatencyMS,
			LastErrorCode:      c.LastErrorCode,
			LastError:          c.LastError,
			ManualDisabled:     c.ManualDisabled,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Status == "healthy" && out[j].Status != "healthy" {
			return true
		}
		if out[i].Status != "healthy" && out[j].Status == "healthy" {
			return false
		}
		if out[i].Ratio != out[j].Ratio {
			return out[i].Ratio < out[j].Ratio
		}
		return out[i].GroupName < out[j].GroupName
	})
	return out, nil
}

func (s *Service) findTargetKey(ctx context.Context, p *storage.AutoGroupPolicy) (*connector.APIKey, error) {
	search := strings.TrimSpace(p.TargetKeyName)
	if p.TargetKeyID == 0 && search == "" {
		search = "auto"
	}
	page, err := s.ChannelSvc.ListAPIKeys(ctx, p.ChannelID, connector.APIKeyQuery{
		Page:     1,
		PageSize: 100,
		Search:   search,
	})
	if err != nil {
		return nil, fmt.Errorf("读取目标 API Key 失败：%w", err)
	}
	key := selectTargetKey(page.Items, p.TargetKeyID, search)
	if key != nil {
		return key, nil
	}
	if search != "" {
		page, err = s.ChannelSvc.ListAPIKeys(ctx, p.ChannelID, connector.APIKeyQuery{Page: 1, PageSize: 100})
		if err != nil {
			return nil, fmt.Errorf("读取 API Key 列表失败：%w", err)
		}
		key = selectTargetKey(page.Items, p.TargetKeyID, search)
	}
	if key == nil {
		if p.TargetKeyID > 0 {
			return nil, fmt.Errorf("找不到目标 API Key：ID %d", p.TargetKeyID)
		}
		key, err = s.createTargetKey(ctx, p, search)
		if err != nil {
			return nil, fmt.Errorf("找不到目标 API Key：%s，自动创建失败：%w", search, err)
		}
	}
	return key, nil
}

func (s *Service) createTargetKey(ctx context.Context, p *storage.AutoGroupPolicy, name string) (*connector.APIKey, error) {
	name = emptyAs(strings.TrimSpace(name), "auto")
	unlimited := true
	req := connector.APIKeyCreateRequest{
		Name:           name,
		UnlimitedQuota: &unlimited,
	}
	if groups, err := s.ChannelSvc.ListAPIKeyGroups(ctx, p.ChannelID); err == nil && len(groups) > 0 {
		sort.SliceStable(groups, func(i, j int) bool {
			if groups[i].Ratio != groups[j].Ratio {
				return groups[i].Ratio < groups[j].Ratio
			}
			return groups[i].Name < groups[j].Name
		})
		req.Group = groups[0].Name
		req.GroupID = groups[0].ID
	}
	created, err := s.ChannelSvc.CreateAPIKey(ctx, p.ChannelID, req)
	if err != nil {
		return nil, err
	}
	if created == nil || created.ID <= 0 {
		return nil, fmt.Errorf("上游返回的目标 key 无效")
	}
	p.TargetKeyID = created.ID
	p.TargetKeyName = emptyAs(created.Name, name)
	_ = s.Repo.UpdatePolicy(p)
	return created, nil
}

func (s *Service) ensureProbeKey(ctx context.Context, p *storage.AutoGroupPolicy) (*probeKey, error) {
	name := emptyAs(strings.TrimSpace(p.ProbeKeyName), "ops-probe-auto")
	var key *connector.APIKey
	if p.ProbeKeyID > 0 {
		page, err := s.ChannelSvc.ListAPIKeys(ctx, p.ChannelID, connector.APIKeyQuery{Page: 1, PageSize: 100})
		if err != nil {
			return nil, fmt.Errorf("读取探测 API Key 失败：%w", err)
		}
		key = selectTargetKey(page.Items, p.ProbeKeyID, "")
	}
	if key == nil {
		page, err := s.ChannelSvc.ListAPIKeys(ctx, p.ChannelID, connector.APIKeyQuery{Page: 1, PageSize: 100, Search: name})
		if err != nil {
			return nil, fmt.Errorf("读取探测 API Key 失败：%w", err)
		}
		key = selectTargetKey(page.Items, 0, name)
	}
	if key == nil {
		unlimited := false
		quota := 10.0
		limitEnabled := true
		req := connector.APIKeyCreateRequest{
			Name:               name,
			Quota:              &quota,
			UnlimitedQuota:     &unlimited,
			ModelLimitsEnabled: &limitEnabled,
			ModelLimits:        emptyAs(strings.TrimSpace(p.ProbeModel), "gpt-4o-mini"),
		}
		if groups, err := s.ChannelSvc.ListAPIKeyGroups(ctx, p.ChannelID); err == nil && len(groups) > 0 {
			sort.SliceStable(groups, func(i, j int) bool {
				if groups[i].Ratio != groups[j].Ratio {
					return groups[i].Ratio < groups[j].Ratio
				}
				return groups[i].Name < groups[j].Name
			})
			req.Group = groups[0].Name
			req.GroupID = groups[0].ID
		}
		created, err := s.ChannelSvc.CreateAPIKey(ctx, p.ChannelID, req)
		if err != nil {
			return nil, fmt.Errorf("创建探测 API Key %s 失败：%w", name, err)
		}
		key = created
	}
	if key == nil {
		return nil, fmt.Errorf("探测 API Key %s 创建后返回为空", name)
	}
	if key.ID <= 0 {
		return nil, fmt.Errorf("探测 API Key %s 缺少有效 ID", name)
	}
	if p.ProbeKeyID != key.ID || p.ProbeKeyName != key.Name {
		p.ProbeKeyID = key.ID
		p.ProbeKeyName = emptyAs(key.Name, name)
		_ = s.Repo.UpdatePolicy(p)
	}
	raw, err := s.ChannelSvc.RevealAPIKey(ctx, p.ChannelID, key.ID)
	if err != nil {
		return nil, fmt.Errorf("读取探测 API Key 明文失败：%w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("探测 API Key %s 明文为空", name)
	}
	return &probeKey{ID: key.ID, Name: emptyAs(key.Name, name), Value: strings.TrimSpace(raw)}, nil
}

func (s *Service) probeCandidate(ctx context.Context, p *storage.AutoGroupPolicy, ch *storage.Channel, probe *probeKey, c CandidateDecision) CandidateDecision {
	if probe == nil {
		c.Status = "failed"
		c.Reason = "缺少探测 key"
		return c
	}
	if err := s.moveProbeKey(ctx, p, probe.ID, c); err != nil {
		c.Status = "failed"
		c.Reason = "探测 key 切组失败"
		c.LastError = err.Error()
		c.LastErrorCode = "probe_key_update_failed"
		failed, _ := s.Repo.MarkCandidateProbeFailure(p.ID, c.GroupName, p.FailureThreshold, circuitDuration(p), c.LastErrorCode, err.Error(), 0)
		if failed != nil {
			applyStoredCandidate(&c, failed)
		}
		s.dispatch(ctx, p, ch, storage.EventAutoGroupProbeFailed, c.GroupName, "智能分组探测失败", err.Error(), map[string]any{"group": c.GroupName})
		if c.Status == "circuit_open" {
			s.dispatch(ctx, p, ch, storage.EventAutoGroupCircuitOpened, c.GroupName, "智能分组已熔断", fmt.Sprintf("%s 探测失败达到阈值，已熔断", c.GroupName), nil)
		}
		return c
	}
	res := s.probeOpenAI(ctx, ch, p, probe.Value)
	if !res.Success {
		c.Status = "failed"
		c.Reason = "探测失败"
		c.LastError = res.Message
		c.LastErrorCode = res.Code
		c.LastProbeLatencyMS = res.LatencyMS
		failed, _ := s.Repo.MarkCandidateProbeFailure(p.ID, c.GroupName, p.FailureThreshold, circuitDuration(p), res.Code, res.Message, res.LatencyMS)
		if failed != nil {
			applyStoredCandidate(&c, failed)
		}
		s.dispatch(ctx, p, ch, storage.EventAutoGroupProbeFailed, c.GroupName, "智能分组探测失败", fmt.Sprintf("%s：%s", c.GroupName, res.Message), map[string]any{
			"group": c.GroupName,
			"code":  res.Code,
		})
		if c.Status == "circuit_open" {
			s.dispatch(ctx, p, ch, storage.EventAutoGroupCircuitOpened, c.GroupName, "智能分组已熔断", fmt.Sprintf("%s 探测失败达到阈值，已熔断", c.GroupName), nil)
		}
		return c
	}
	updated, recovered, err := s.Repo.MarkCandidateProbeSuccess(p.ID, c.GroupName, p.HalfOpenSuccessThreshold, res.LatencyMS)
	if err == nil && updated != nil {
		applyStoredCandidate(&c, updated)
	}
	if recovered {
		s.dispatch(ctx, p, ch, storage.EventAutoGroupRecovered, c.GroupName, "智能分组候选已恢复", fmt.Sprintf("%s 半开探测通过，已恢复可用", c.GroupName), nil)
	}
	if c.Status == "" || c.Status == "unknown" {
		c.Status = "healthy"
		c.Reason = "探测通过"
	}
	return c
}

func (s *Service) moveProbeKey(ctx context.Context, p *storage.AutoGroupPolicy, probeID int64, c CandidateDecision) error {
	req := connector.APIKeyUpdateRequest{}
	if c.GroupID != nil {
		req.GroupID = c.GroupID
	} else {
		group := c.GroupName
		req.Group = &group
	}
	_, err := s.ChannelSvc.UpdateAPIKey(ctx, p.ChannelID, probeID, req)
	if err != nil {
		return fmt.Errorf("切换探测 key 到 %s 失败：%w", c.GroupName, err)
	}
	return nil
}

func (s *Service) probeOpenAI(ctx context.Context, ch *storage.Channel, p *storage.AutoGroupPolicy, apiKey string) probeResult {
	timeout := time.Duration(p.ProbeTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	model := emptyAs(strings.TrimSpace(p.ProbeModel), "gpt-4o-mini")
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
		"max_tokens": 1,
		"stream":     false,
	})
	url := strings.TrimRight(ch.SiteURL, "/") + "/v1/chat/completions"
	started := time.Now()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return probeResult{Code: "request_build_failed", Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return probeResult{Code: probeErrorCode(err), Message: err.Error(), LatencyMS: latency}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return probeResult{Code: httpProbeCode(resp.StatusCode, raw), Message: probeHTTPMessage(resp.StatusCode, raw), LatencyMS: latency}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return probeResult{Code: "invalid_json", Message: "探测接口返回非 JSON 响应", LatencyMS: latency}
	}
	if len(decoded) == 0 {
		return probeResult{Code: "empty_response", Message: "探测接口返回空 JSON", LatencyMS: latency}
	}
	return probeResult{Success: true, Code: "ok", Message: "探测通过", LatencyMS: latency}
}

func (s *Service) switchTargetKey(ctx context.Context, p *storage.AutoGroupPolicy, ch *storage.Channel, target *connector.APIKey, selected CandidateDecision, fromGroup string) (*connector.APIKey, *storage.AutoGroupSwitchLog, error) {
	req := connector.APIKeyUpdateRequest{}
	if selected.GroupID != nil {
		req.GroupID = selected.GroupID
	} else {
		group := selected.GroupName
		req.Group = &group
	}
	updated, err := s.ChannelSvc.UpdateAPIKey(ctx, p.ChannelID, target.ID, req)
	log := &storage.AutoGroupSwitchLog{
		PolicyID:      p.ID,
		ChannelID:     p.ChannelID,
		TargetKeyID:   target.ID,
		TargetKeyName: target.Name,
		FromGroup:     fromGroup,
		ToGroup:       selected.GroupName,
		ToGroupID:     selected.GroupID,
		ToRatio:       selected.Ratio,
		Success:       err == nil,
		Reason:        "选择倍率最低且未被规则排除的健康分组",
	}
	if err != nil {
		log.ErrorMessage = err.Error()
	}
	_ = s.Repo.AppendSwitchLog(log)
	return updated, log, err
}

func (s *Service) buildEvalLog(p *storage.AutoGroupPolicy, target *connector.APIKey, selected *CandidateDecision, success bool, status, action, msg string, count, availableCount, circuitCount int, ratio float64) *storage.AutoGroupEvaluationLog {
	log := &storage.AutoGroupEvaluationLog{
		PolicyID:         p.ID,
		ChannelID:        p.ChannelID,
		Success:          success,
		Status:           status,
		CandidateCount:   count,
		AvailableCount:   availableCount,
		CircuitOpenCount: circuitCount,
		Action:           action,
		Message:          msg,
		SelectedRatio:    ratio,
	}
	if target != nil {
		log.TargetKeyID = target.ID
		log.TargetKeyName = target.Name
		log.CurrentGroup = currentKeyGroup(target)
	}
	if selected != nil {
		log.SelectedGroup = selected.GroupName
		log.SelectedRatio = selected.Ratio
	}
	return log
}

func (s *Service) updatePolicyStatus(p *storage.AutoGroupPolicy, status, lastErr string, key *connector.APIKey, ratio float64, switchAt, evalAt *time.Time) error {
	p.LastStatus = status
	p.LastError = lastErr
	if evalAt != nil {
		p.LastEvaluateAt = evalAt
	}
	if switchAt != nil {
		p.LastSwitchAt = switchAt
	}
	if key != nil {
		p.TargetKeyID = key.ID
		p.TargetKeyName = emptyAs(key.Name, p.TargetKeyName)
		p.CurrentGroupName = currentKeyGroup(key)
		p.CurrentGroupID = key.GroupID
		p.CurrentRatio = ratio
	}
	return s.Repo.UpdatePolicy(p)
}

func (s *Service) dispatch(ctx context.Context, p *storage.AutoGroupPolicy, ch *storage.Channel, event storage.NotificationEvent, modelName, subject, body string, extra map[string]any) {
	if !p.NotifyEnabled || s.Dispatcher == nil || ch == nil {
		return
	}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["policy_id"] = p.ID
	extra["policy_name"] = p.Name
	extra["channel_name"] = ch.Name
	msg := notify.Message{
		Event:     event,
		ChannelID: ch.ID,
		ModelName: modelName,
		Subject:   fmt.Sprintf("%s：%s", ch.Name, subject),
		Body:      body,
		Extra:     extra,
	}
	if err := s.Dispatcher.Dispatch(ctx, msg); err != nil && s.Log != nil {
		s.Log.Warn("dispatch auto group notification failed", "policy_id", p.ID, "err", err)
	}
}

func policyFromInput(current *storage.AutoGroupPolicy, in PolicyInput) (*storage.AutoGroupPolicy, error) {
	p := &storage.AutoGroupPolicy{}
	if current != nil {
		*p = *current
	}
	if in.ChannelID != 0 {
		p.ChannelID = in.ChannelID
	}
	if p.ChannelID == 0 {
		return nil, errors.New("请选择渠道")
	}
	p.Name = strings.TrimSpace(in.Name)
	if p.Name == "" {
		p.Name = "智能分组策略"
	}
	p.Enabled = in.Enabled
	p.NotifyEnabled = in.NotifyEnabled
	p.TargetKeyID = in.TargetKeyID
	p.TargetKeyName = emptyAs(strings.TrimSpace(in.TargetKeyName), "auto")
	p.ProbeKeyID = in.ProbeKeyID
	p.ProbeKeyName = emptyAs(strings.TrimSpace(in.ProbeKeyName), "ops-probe-auto")
	p.ProbeModel = emptyAs(strings.TrimSpace(in.ProbeModel), "gpt-4o-mini")
	p.ProbeTimeoutSeconds = in.ProbeTimeoutSeconds
	if p.ProbeTimeoutSeconds <= 0 {
		p.ProbeTimeoutSeconds = 15
	}
	p.ProbeSuccessCacheMinutes = in.ProbeSuccessCacheMinutes
	if p.ProbeSuccessCacheMinutes <= 0 {
		p.ProbeSuccessCacheMinutes = 60
	}
	p.ProbeFailureRetryMinutes = in.ProbeFailureRetryMinutes
	if p.ProbeFailureRetryMinutes <= 0 {
		p.ProbeFailureRetryMinutes = 10
	}
	p.ProbeMaxPerRun = in.ProbeMaxPerRun
	if p.ProbeMaxPerRun <= 0 {
		p.ProbeMaxPerRun = 3
	}
	p.MinRatio = in.MinRatio
	p.MaxRatio = in.MaxRatio
	p.FailureThreshold = in.FailureThreshold
	if p.FailureThreshold <= 0 {
		p.FailureThreshold = 2
	}
	p.CircuitDurationMinutes = in.CircuitDurationMinutes
	if p.CircuitDurationMinutes <= 0 {
		p.CircuitDurationMinutes = 30
	}
	p.HalfOpenSuccessThreshold = in.HalfOpenSuccessThreshold
	if p.HalfOpenSuccessThreshold <= 0 {
		p.HalfOpenSuccessThreshold = 1
	}
	p.MinRatioImprovementPct = in.MinRatioImprovementPct
	if p.MinRatioImprovementPct < 0 {
		p.MinRatioImprovementPct = 0
	}
	p.SwitchCooldownMinutes = in.SwitchCooldownMinutes
	if p.SwitchCooldownMinutes < 0 {
		p.SwitchCooldownMinutes = 30
	}
	if in.ForceSwitchOnCurrentUnhealthy != nil {
		p.ForceSwitchOnCurrentUnhealthy = *in.ForceSwitchOnCurrentUnhealthy
	} else if current == nil {
		p.ForceSwitchOnCurrentUnhealthy = true
	}
	if in.KeepCurrentWhenNoAvailable != nil {
		p.KeepCurrentWhenNoAvailable = *in.KeepCurrentWhenNoAvailable
	} else if current == nil {
		p.KeepCurrentWhenNoAvailable = true
	}
	var err error
	if p.IncludeGroupsJSON, err = marshalStrings(in.IncludeGroups); err != nil {
		return nil, err
	}
	if p.ExcludeGroupsJSON, err = marshalStrings(in.ExcludeGroups); err != nil {
		return nil, err
	}
	if p.IncludeKeywordsJSON, err = marshalStrings(in.IncludeKeywords); err != nil {
		return nil, err
	}
	if p.ExcludeKeywordsJSON, err = marshalStrings(in.ExcludeKeywords); err != nil {
		return nil, err
	}
	return p, nil
}

func classifyCandidate(p *storage.AutoGroupPolicy, c CandidateDecision, now time.Time) (string, string) {
	if c.ManualDisabled {
		return "excluded", "已手动停用"
	}
	if c.Ratio <= 0 {
		return "excluded", "缺少有效倍率"
	}
	if p.MinRatio > 0 && c.Ratio < p.MinRatio {
		return "excluded", "低于最小倍率"
	}
	if p.MaxRatio > 0 && c.Ratio > p.MaxRatio {
		return "excluded", "高于最大倍率"
	}
	includeGroups := stringSet(mustUnmarshalStrings(p.IncludeGroupsJSON))
	if len(includeGroups) > 0 && !includeGroups[strings.ToLower(c.GroupName)] {
		return "excluded", "不在允许分组范围"
	}
	excludeGroups := stringSet(mustUnmarshalStrings(p.ExcludeGroupsJSON))
	if excludeGroups[strings.ToLower(c.GroupName)] {
		return "excluded", "命中排除分组"
	}
	text := strings.ToLower(c.GroupName + "\n" + c.Description)
	includeKeywords := normalizeStrings(mustUnmarshalStrings(p.IncludeKeywordsJSON))
	if len(includeKeywords) > 0 && !containsAny(text, includeKeywords) {
		return "excluded", "未命中包含关键词"
	}
	excludeKeywords := normalizeStrings(mustUnmarshalStrings(p.ExcludeKeywordsJSON))
	if containsAny(text, excludeKeywords) {
		return "excluded", "命中排除关键词"
	}
	if c.CircuitOpenUntil != nil && c.CircuitOpenUntil.After(now) {
		return "circuit_open", "熔断中"
	}
	if c.CircuitOpenUntil != nil && !c.CircuitOpenUntil.After(now) {
		return "half_open", "熔断到期，进入半开探测"
	}
	return "healthy", "可用"
}

func healthyCandidates(list []CandidateDecision) []CandidateDecision {
	out := make([]CandidateDecision, 0, len(list))
	for _, c := range list {
		if c.Status == "healthy" {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Ratio != out[j].Ratio {
			return out[i].Ratio < out[j].Ratio
		}
		return out[i].GroupName < out[j].GroupName
	})
	return out
}

func countCandidatesByStatus(list []CandidateDecision, status string) int {
	count := 0
	for _, c := range list {
		if c.Status == status {
			count++
		}
	}
	return count
}

func applyProbeCache(p *storage.AutoGroupPolicy, c CandidateDecision, now time.Time) CandidateDecision {
	if c.Status == "half_open" {
		return c
	}
	if c.Status != "healthy" {
		return c
	}
	if c.LastProbeAt == nil {
		c.Status = "probe_pending"
		c.Reason = "等待首次探测"
		return c
	}
	if c.LastProbeSuccess != nil && *c.LastProbeSuccess {
		if now.Sub(*c.LastProbeAt) < probeSuccessCacheDuration(p) {
			c.Reason = "使用最近探测成功缓存"
			return c
		}
		c.Status = "probe_pending"
		c.Reason = "探测成功缓存已过期"
		return c
	}
	if now.Sub(*c.LastProbeAt) < probeFailureRetryDuration(p) {
		c.Status = "failed"
		c.Reason = "失败重试间隔内"
		return c
	}
	c.Status = "probe_pending"
	c.Reason = "失败重试间隔已到"
	return c
}

func needsProbe(c CandidateDecision) bool {
	return c.Status == "probe_pending" || c.Status == "half_open"
}

func probePriority(c CandidateDecision, target *connector.APIKey, currentGroup string) int {
	if !needsProbe(c) {
		return 100
	}
	if target != nil {
		if target.GroupID != nil && c.GroupID != nil && *target.GroupID == *c.GroupID {
			return 0
		}
		if currentGroup != "" && strings.EqualFold(currentGroup, c.GroupName) {
			return 0
		}
	}
	if c.Status == "half_open" {
		return 1
	}
	if c.LastProbeAt == nil {
		return 2
	}
	if c.LastProbeSuccess != nil && !*c.LastProbeSuccess {
		return 3
	}
	return 4
}

func probeSuccessCacheDuration(p *storage.AutoGroupPolicy) time.Duration {
	minutes := p.ProbeSuccessCacheMinutes
	if minutes <= 0 {
		minutes = 60
	}
	return time.Duration(minutes) * time.Minute
}

func probeFailureRetryDuration(p *storage.AutoGroupPolicy) time.Duration {
	minutes := p.ProbeFailureRetryMinutes
	if minutes <= 0 {
		minutes = 10
	}
	return time.Duration(minutes) * time.Minute
}

func probeMaxPerRun(p *storage.AutoGroupPolicy) int {
	if p.ProbeMaxPerRun <= 0 {
		return 3
	}
	return p.ProbeMaxPerRun
}

func findCandidateByGroup(list []CandidateDecision, key *connector.APIKey, groupName string) *CandidateDecision {
	for i := range list {
		if key != nil && key.GroupID != nil && list[i].GroupID != nil && *key.GroupID == *list[i].GroupID {
			return &list[i]
		}
		if strings.EqualFold(list[i].GroupName, groupName) {
			return &list[i]
		}
	}
	return nil
}

func ratioImprovementPct(current, selected float64) float64 {
	if current <= 0 || selected <= 0 || selected >= current {
		return 0
	}
	return (current - selected) / current * 100
}

func currentRatio(current *CandidateDecision, key *connector.APIKey) float64 {
	if current != nil && current.Ratio > 0 {
		return current.Ratio
	}
	if key != nil && key.GroupRatio > 0 {
		return key.GroupRatio
	}
	return 0
}

func shouldIgnoreSwitchCooldown(p *storage.AutoGroupPolicy, currentHealthy bool) bool {
	return !currentHealthy && p.ForceSwitchOnCurrentUnhealthy
}

func switchCooldownRemaining(p *storage.AutoGroupPolicy, now time.Time) time.Duration {
	if p.LastSwitchAt == nil {
		return 0
	}
	minutes := p.SwitchCooldownMinutes
	if minutes <= 0 {
		return 0
	}
	until := p.LastSwitchAt.Add(time.Duration(minutes) * time.Minute)
	if until.After(now) {
		return until.Sub(now)
	}
	return 0
}

func formatDurationCN(d time.Duration) string {
	if d <= 0 {
		return "0 分钟"
	}
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	if minutes < 60 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", minutes/60, minutes%60)
}

func applyStoredCandidate(c *CandidateDecision, stored *storage.AutoGroupCandidate) {
	c.Status = stored.Status
	c.Reason = stored.Reason
	c.FailureCount = stored.FailureCount
	c.SuccessCount = stored.SuccessCount
	c.CircuitOpenUntil = stored.CircuitOpenUntil
	c.CircuitOpenedAt = stored.CircuitOpenedAt
	c.RecoveredAt = stored.RecoveredAt
	c.LastProbeAt = stored.LastProbeAt
	c.LastProbeSuccess = stored.LastProbeSuccess
	c.LastProbeLatencyMS = stored.LastProbeLatencyMS
	c.LastErrorCode = stored.LastErrorCode
	c.LastError = stored.LastError
	c.ManualDisabled = stored.ManualDisabled
}

func candidateFromStored(stored storage.AutoGroupCandidate) CandidateDecision {
	return CandidateDecision{
		GroupName:          stored.GroupName,
		GroupID:            stored.GroupID,
		Description:        stored.Description,
		Ratio:              stored.Ratio,
		Status:             stored.Status,
		Reason:             stored.Reason,
		FailureCount:       stored.FailureCount,
		SuccessCount:       stored.SuccessCount,
		CircuitOpenUntil:   stored.CircuitOpenUntil,
		CircuitOpenedAt:    stored.CircuitOpenedAt,
		RecoveredAt:        stored.RecoveredAt,
		LastProbeAt:        stored.LastProbeAt,
		LastProbeSuccess:   stored.LastProbeSuccess,
		LastProbeLatencyMS: stored.LastProbeLatencyMS,
		LastErrorCode:      stored.LastErrorCode,
		LastError:          stored.LastError,
		ManualDisabled:     stored.ManualDisabled,
	}
}

func probeErrorCode(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout") {
		return "timeout"
	}
	if strings.Contains(msg, "connection") {
		return "connection_error"
	}
	return "http_error"
}

func httpProbeCode(status int, body []byte) string {
	text := strings.ToLower(string(body))
	switch status {
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusTooManyRequests:
		return "rate_limited"
	}
	if strings.Contains(text, "quota") || strings.Contains(text, "余额") || strings.Contains(text, "额度") {
		return "quota_exhausted"
	}
	if strings.Contains(text, "model") || strings.Contains(text, "模型") {
		return "model_unavailable"
	}
	return fmt.Sprintf("http_%d", status)
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

func sameGroup(key *connector.APIKey, selected CandidateDecision) bool {
	if key == nil {
		return false
	}
	if key.GroupID != nil && selected.GroupID != nil && *key.GroupID == *selected.GroupID {
		return true
	}
	return strings.EqualFold(currentKeyGroup(key), selected.GroupName)
}

func currentKeyGroup(key *connector.APIKey) string {
	if key == nil {
		return ""
	}
	if strings.TrimSpace(key.GroupName) != "" {
		return strings.TrimSpace(key.GroupName)
	}
	return strings.TrimSpace(key.Group)
}

func keyWithCandidateGroup(key *connector.APIKey, candidate *CandidateDecision) *connector.APIKey {
	if key == nil || candidate == nil || strings.TrimSpace(candidate.GroupName) == "" {
		return key
	}
	if strings.TrimSpace(currentKeyGroup(key)) != "" {
		return key
	}
	if key.GroupID != nil && candidate.GroupID != nil && *key.GroupID != *candidate.GroupID {
		return key
	}
	key.GroupName = candidate.GroupName
	key.Group = candidate.GroupName
	if key.GroupID == nil && candidate.GroupID != nil {
		key.GroupID = candidate.GroupID
	}
	if key.GroupRatio <= 0 && candidate.Ratio > 0 {
		key.GroupRatio = candidate.Ratio
	}
	return key
}

func selectTargetKey(items []connector.APIKey, id int64, name string) *connector.APIKey {
	name = strings.TrimSpace(name)
	var contains *connector.APIKey
	for i := range items {
		k := &items[i]
		if id > 0 && k.ID == id {
			return k
		}
		if name == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k.Name), name) {
			return k
		}
		if contains == nil && strings.Contains(strings.ToLower(k.Name), strings.ToLower(name)) {
			contains = k
		}
	}
	return contains
}

func circuitDuration(p *storage.AutoGroupPolicy) time.Duration {
	minutes := p.CircuitDurationMinutes
	if minutes <= 0 {
		minutes = 30
	}
	return time.Duration(minutes) * time.Minute
}

func marshalStrings(values []string) (string, error) {
	clean := normalizeStrings(values)
	if len(clean) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(clean)
	return string(raw), err
}

func mustUnmarshalStrings(raw string) []string {
	var values []string
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func normalizeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(strings.ToLower(v))
		if v != "" {
			out[v] = true
		}
	}
	return out
}

func containsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
