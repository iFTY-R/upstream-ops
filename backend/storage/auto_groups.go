package storage

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AutoGroups struct{ db *gorm.DB }

func NewAutoGroups(db *gorm.DB) *AutoGroups { return &AutoGroups{db: db} }

func (r *AutoGroups) CreatePolicy(p *AutoGroupPolicy) error {
	return r.db.Create(p).Error
}

func (r *AutoGroups) UpdatePolicy(p *AutoGroupPolicy) error {
	return r.db.Save(p).Error
}

func (r *AutoGroups) DeletePolicy(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{
			&AutoGroupCandidate{},
			&AutoGroupEvaluationLog{},
			&AutoGroupSwitchLog{},
		} {
			if err := tx.Where("policy_id = ?", id).Delete(model).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&AutoGroupPolicy{}, id).Error
	})
}

func (r *AutoGroups) FindPolicy(id uint) (*AutoGroupPolicy, error) {
	var p AutoGroupPolicy
	if err := r.db.First(&p, id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *AutoGroups) FindPolicyByChannel(channelID uint) (*AutoGroupPolicy, error) {
	var p AutoGroupPolicy
	if err := r.db.Where("channel_id = ?", channelID).
		Order("CASE WHEN target_key_name = 'auto' THEN 0 ELSE 1 END").
		Order("id ASC").
		First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *AutoGroups) FindPolicyByChannelTarget(channelID uint, targetKeyName string) (*AutoGroupPolicy, error) {
	var p AutoGroupPolicy
	if err := r.db.Where("channel_id = ? AND target_key_name = ?", channelID, targetKeyName).
		First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *AutoGroups) ListPoliciesByChannel(channelID uint) ([]AutoGroupPolicy, error) {
	var list []AutoGroupPolicy
	if err := r.db.Where("channel_id = ?", channelID).
		Order("CASE WHEN sort_order > 0 THEN 0 ELSE 1 END").
		Order("sort_order ASC").
		Order("CASE WHEN sort_order > 0 THEN 0 WHEN target_key_name = 'auto' THEN 0 ELSE 1 END").
		Order("target_key_name ASC").
		Order("updated_at DESC").
		Order("id ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *AutoGroups) ListPolicies() ([]AutoGroupPolicy, error) {
	var list []AutoGroupPolicy
	if err := r.db.Scopes(autoGroupPolicyOrder).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *AutoGroups) ListEnabledPolicies() ([]AutoGroupPolicy, error) {
	var list []AutoGroupPolicy
	if err := r.db.Where("enabled = ?", true).Scopes(autoGroupPolicyOrder).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *AutoGroups) ReorderPolicies(ids []uint) error {
	if len(ids) == 0 {
		return errors.New("排序列表不能为空")
	}
	seen := map[uint]bool{}
	for _, id := range ids {
		if id == 0 {
			return errors.New("排序列表包含无效策略 ID")
		}
		if seen[id] {
			return fmt.Errorf("排序列表包含重复策略 ID：%d", id)
		}
		seen[id] = true
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&AutoGroupPolicy{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return fmt.Errorf("排序列表包含不存在的策略")
		}
		for i, id := range ids {
			if err := tx.Model(&AutoGroupPolicy{}).Where("id = ?", id).Update("sort_order", i+1).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func autoGroupPolicyOrder(db *gorm.DB) *gorm.DB {
	return db.
		Order("CASE WHEN sort_order > 0 THEN 0 ELSE 1 END").
		Order("sort_order ASC").
		Order("updated_at DESC").
		Order("id ASC")
}

func (r *AutoGroups) UpsertCandidate(c *AutoGroupCandidate) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "policy_id"}, {Name: "group_name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"group_id",
			"description",
			"ratio",
			"status",
			"reason",
			"failure_count",
			"success_count",
			"circuit_open_until",
			"circuit_opened_at",
			"recovered_at",
			"last_checked_at",
			"last_probe_at",
			"last_probe_success",
			"last_probe_latency_ms",
			"last_error_code",
			"last_error",
			"manual_disabled",
			"updated_at",
		}),
	}).Create(c).Error
}

func (r *AutoGroups) FindCandidate(policyID uint, groupName string) (*AutoGroupCandidate, error) {
	var c AutoGroupCandidate
	err := r.db.Where("policy_id = ? AND group_name = ?", policyID, groupName).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *AutoGroups) FindCandidateByID(policyID, candidateID uint) (*AutoGroupCandidate, error) {
	var c AutoGroupCandidate
	err := r.db.Where("policy_id = ? AND id = ?", policyID, candidateID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *AutoGroups) ListCandidates(policyID uint) ([]AutoGroupCandidate, error) {
	var list []AutoGroupCandidate
	if err := r.db.Where("policy_id = ?", policyID).
		Order("ratio ASC").Order("group_name ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *AutoGroups) SetPolicyEnabled(id uint, enabled bool) error {
	return r.db.Model(&AutoGroupPolicy{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"enabled":     enabled,
			"last_status": map[bool]string{true: "idle", false: "disabled"}[enabled],
		}).Error
}

func (r *AutoGroups) SetCandidateManualDisabled(policyID, candidateID uint, disabled bool) error {
	now := time.Now()
	status := "unknown"
	reason := "已恢复，等待下次评估"
	if disabled {
		status = "excluded"
		reason = "已手动停用"
	}
	return r.db.Model(&AutoGroupCandidate{}).
		Where("policy_id = ? AND id = ?", policyID, candidateID).
		Updates(map[string]any{
			"manual_disabled": disabled,
			"status":          status,
			"reason":          reason,
			"last_checked_at": &now,
		}).Error
}

func (r *AutoGroups) OpenCandidateCircuit(policyID, candidateID uint, openFor time.Duration, reason string) error {
	now := time.Now()
	until := now.Add(openFor)
	if reason == "" {
		reason = "人工临时熔断"
	}
	return r.db.Model(&AutoGroupCandidate{}).
		Where("policy_id = ? AND id = ?", policyID, candidateID).
		Updates(map[string]any{
			"status":                "circuit_open",
			"reason":                reason,
			"circuit_opened_at":     &now,
			"circuit_open_until":    &until,
			"last_checked_at":       &now,
			"last_probe_success":    false,
			"last_error_code":       "manual_circuit",
			"last_error":            reason,
			"last_probe_latency_ms": 0,
		}).Error
}

func (r *AutoGroups) MarkCandidateFailure(policyID uint, groupName string, threshold int, openFor time.Duration, errMsg string) error {
	now := time.Now()
	c, err := r.FindCandidate(policyID, groupName)
	if err != nil {
		return err
	}
	if c == nil {
		c = &AutoGroupCandidate{PolicyID: policyID, GroupName: groupName}
	}
	c.FailureCount++
	c.SuccessCount = 0
	c.Status = "failed"
	c.Reason = "切换失败"
	c.LastError = errMsg
	c.LastCheckedAt = &now
	if threshold <= 0 {
		threshold = 2
	}
	if c.FailureCount >= threshold && openFor > 0 {
		until := now.Add(openFor)
		c.Status = "circuit_open"
		c.CircuitOpenUntil = &until
		c.CircuitOpenedAt = &now
	}
	return r.UpsertCandidate(c)
}

func (r *AutoGroups) MarkCandidateProbeFailure(policyID uint, groupName string, threshold int, openFor time.Duration, code, errMsg string, latencyMS int64) (*AutoGroupCandidate, error) {
	now := time.Now()
	c, err := r.FindCandidate(policyID, groupName)
	if err != nil {
		return nil, err
	}
	if c == nil {
		c = &AutoGroupCandidate{PolicyID: policyID, GroupName: groupName}
	}
	probeOK := false
	c.FailureCount++
	c.SuccessCount = 0
	c.Status = "failed"
	c.Reason = "探测失败"
	c.LastErrorCode = code
	c.LastError = errMsg
	c.LastCheckedAt = &now
	c.LastProbeAt = &now
	c.LastProbeSuccess = &probeOK
	c.LastProbeLatencyMS = latencyMS
	if threshold <= 0 {
		threshold = 2
	}
	if c.FailureCount >= threshold && openFor > 0 {
		until := now.Add(openFor)
		c.Status = "circuit_open"
		c.Reason = "探测失败，已熔断"
		c.CircuitOpenUntil = &until
		c.CircuitOpenedAt = &now
	}
	if err := r.UpsertCandidate(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (r *AutoGroups) MarkCandidateProbeSuccess(policyID uint, groupName string, threshold int, latencyMS int64) (*AutoGroupCandidate, bool, error) {
	now := time.Now()
	c, err := r.FindCandidate(policyID, groupName)
	if err != nil {
		return nil, false, err
	}
	if c == nil {
		c = &AutoGroupCandidate{PolicyID: policyID, GroupName: groupName}
	}
	probeOK := true
	if threshold <= 0 {
		threshold = 1
	}
	wasCircuit := c.CircuitOpenUntil != nil || c.Status == "circuit_open" || c.Status == "half_open"
	c.SuccessCount++
	c.FailureCount = 0
	c.LastErrorCode = ""
	c.LastError = ""
	c.LastCheckedAt = &now
	c.LastProbeAt = &now
	c.LastProbeSuccess = &probeOK
	c.LastProbeLatencyMS = latencyMS
	recovered := false
	if c.SuccessCount >= threshold {
		c.Status = "healthy"
		c.Reason = "探测通过"
		if wasCircuit {
			recovered = true
			c.RecoveredAt = &now
		}
		c.CircuitOpenUntil = nil
	} else {
		c.Status = "half_open"
		c.Reason = "半开探测通过，等待连续成功"
	}
	if err := r.UpsertCandidate(c); err != nil {
		return nil, false, err
	}
	return c, recovered, nil
}

func (r *AutoGroups) ResetCandidateFailure(policyID uint, groupName string) error {
	now := time.Now()
	return r.db.Model(&AutoGroupCandidate{}).
		Where("policy_id = ? AND group_name = ?", policyID, groupName).
		Updates(map[string]any{
			"failure_count":      0,
			"success_count":      0,
			"circuit_open_until": nil,
			"last_error_code":    "",
			"last_error":         "",
			"last_checked_at":    &now,
		}).Error
}

func (r *AutoGroups) AppendEvaluationLog(log *AutoGroupEvaluationLog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	return r.db.Create(log).Error
}

func (r *AutoGroups) AppendSwitchLog(log *AutoGroupSwitchLog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	return r.db.Create(log).Error
}

func (r *AutoGroups) ListEvaluationLogs(policyID uint, page, pageSize int) ([]AutoGroupEvaluationLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := r.db.Model(&AutoGroupEvaluationLog{})
	if policyID != 0 {
		q = q.Where("policy_id = ?", policyID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []AutoGroupEvaluationLog
	if err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *AutoGroups) ListSwitchLogs(policyID uint, page, pageSize int) ([]AutoGroupSwitchLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := r.db.Model(&AutoGroupSwitchLog{})
	if policyID != 0 {
		q = q.Where("policy_id = ?", policyID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []AutoGroupSwitchLog
	if err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
