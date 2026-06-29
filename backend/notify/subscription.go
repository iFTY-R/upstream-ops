package notify

import (
	"encoding/json"
	"strings"

	"github.com/bejix/upstream-ops/backend/storage"
)

// SubscriptionMode 倍率分组过滤维度。
//
//   - all    订阅该上游命中事件的所有分组倍率
//   - groups 仅订阅该上游 + 指定分组（model_name）的倍率相关事件；
//     非倍率事件仍命中（分组过滤仅对倍率事件起作用）
type SubscriptionMode string

const (
	SubscriptionModeAll    SubscriptionMode = "all"
	SubscriptionModeGroups SubscriptionMode = "groups"
)

// Subscription 通知渠道对单个上游的订阅规则。
type Subscription struct {
	ChannelID uint                        `json:"channel_id"`
	Mode      SubscriptionMode            `json:"mode"`
	Groups    []string                    `json:"groups,omitempty"`
	Events    []storage.NotificationEvent `json:"events,omitempty"`
}

// ParseSubscriptions 容错解析 JSON 数组；空串或解析失败均返回 nil（视为"订阅一切"）。
func ParseSubscriptions(raw string) ([]Subscription, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "null" {
		return nil, nil
	}
	var list []Subscription
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Matches 判断这条订阅是否覆盖当前消息：
//   - 上游 ID 必须一致
//   - Events 为空表示全部事件；非空时消息事件必须在 Events 中
//   - 倍率相关事件 + mode=groups 时，model_name 必须在 Groups 中
//   - 其它情况只要上游匹配即放行
func (s Subscription) Matches(msg Message) bool {
	if msg.ChannelID == 0 || msg.ChannelID != s.ChannelID {
		return false
	}
	if !s.matchesEvent(msg.Event) {
		return false
	}
	if !isRateEvent(msg.Event) || s.Mode != SubscriptionModeGroups {
		return true
	}
	for _, g := range s.Groups {
		if g == msg.ModelName {
			return true
		}
	}
	return false
}

func (s Subscription) matchesEvent(event storage.NotificationEvent) bool {
	if len(s.Events) == 0 {
		return true
	}
	for _, e := range s.Events {
		if e == event {
			return true
		}
	}
	return false
}

func isRateEvent(event storage.NotificationEvent) bool {
	return event == storage.EventRateChanged ||
		event == storage.EventRateStructureChanged ||
		event == storage.EventRateAdded ||
		event == storage.EventRateRemoved
}

// AnyMatch 任意一条订阅命中即视为该通知渠道关心此消息。
// 调用方应在 len(subs) > 0 时才调；空切片由调用方按"订阅一切"处理。
func AnyMatch(subs []Subscription, msg Message) bool {
	for i := range subs {
		if subs[i].Matches(msg) {
			return true
		}
	}
	return false
}
