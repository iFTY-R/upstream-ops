package storage

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// ChannelType 上游渠道类型。
type ChannelType string

const (
	ChannelTypeNewAPI  ChannelType = "newapi"
	ChannelTypeSub2API ChannelType = "sub2api"
)

type ShopPlatform string

const (
	ShopPlatformLDXP ShopPlatform = "ldxp"
)

type ShopScopeMode string

const (
	ShopScopeAll       ShopScopeMode = "all"
	ShopScopeFilters   ShopScopeMode = "filters"
	ShopScopeGoodsKeys ShopScopeMode = "goods_keys"
)

// CredentialMode 渠道凭据模式：
//   - password: 经典模式，存账号 + 密码，由 Connector 走完整登录流程
//   - token:    跳过登录，存用户已有的 cookie / access_token，直接构造 AuthSession
//
// token 模式不依赖打码 / 不会自动续期，token 失效时表现为 last_error 显示鉴权失败。
type CredentialMode string

const (
	CredentialModePassword CredentialMode = "password"
	CredentialModeToken    CredentialMode = "token"
)

// Channel 上游渠道账号。Password / Turnstile API key 等敏感字段都加密保存。
//
// 注意：会话凭据（access_token / refresh_token / cookie / csrf）单独存放在 AuthSession 表。
//
// CredentialMode + PasswordCipher 的语义重载：
//   - password 模式（默认）：Username + PasswordCipher 存账号密码，由 Connector.Login 用
//   - token    模式：PasswordCipher 存 JSON blob（NewAPI: {cookie,user_id} / Sub2API: {access_token,refresh_token}），
//     channel.Service 解析后直接构造 AuthSession，跳过 Login。Username 字段在 token 模式下保留
//     用户填写的备注（一般是邮箱），仅做展示。
//
// 复用 PasswordCipher 而不新增 TokenCipher 是为了让现有的 GORM 行 / 加密路径 / 迁移流程零变动。
type Channel struct {
	ID                     uint           `gorm:"primaryKey" json:"id"`
	Name                   string         `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Type                   ChannelType    `gorm:"size:32;not null;index" json:"type"`
	SiteURL                string         `gorm:"size:512;not null" json:"site_url"`
	Username               string         `gorm:"size:256;not null" json:"username"`
	SortOrder              int            `gorm:"not null;default:1" json:"sort_order"`
	PasswordCipher         string         `gorm:"size:4096;not null" json:"-"`
	CredentialMode         CredentialMode `gorm:"size:16;not null;default:'password'" json:"credential_mode"`
	LoginExtraParams       string         `gorm:"type:text" json:"login_extra_params"`
	TurnstileEnabled       bool           `gorm:"default:false" json:"turnstile_enabled"`
	IgnoreAnnouncements    bool           `gorm:"default:false" json:"ignore_announcements"`
	SubscriptionEnabled    bool           `gorm:"default:false" json:"subscription_enabled"`
	ProxyEnabled           bool           `gorm:"default:false" json:"proxy_enabled"`
	CaptchaConfigID        *uint          `json:"captcha_config_id,omitempty"`
	BalanceThreshold       float64        `gorm:"default:0" json:"balance_threshold"`
	RechargeMultiplier     *float64       `json:"recharge_multiplier,omitempty"`
	RechargeMultiplierMode string         `gorm:"size:16;not null;default:'divide'" json:"recharge_multiplier_mode"`
	MonitorEnabled         bool           `gorm:"default:true" json:"monitor_enabled"`

	// 最近一次采集结果（聚合视图，便于列表页直接展示）
	LastBalance   *float64   `json:"last_balance,omitempty"`
	LastBalanceAt *time.Time `json:"last_balance_at,omitempty"`
	TodayCost     *float64   `json:"today_cost,omitempty"`
	TotalCost     *float64   `json:"total_cost,omitempty"`
	LastError     string     `gorm:"type:text" json:"last_error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Channel) TableName() string { return "channels" }

// AuthSession 渠道登录后保存的凭据，按 ChannelID 一对一关联。
// *Cipher 字段都用 AES-GCM 加密；UserID 是上游账号 ID 字符串（非敏感），明文存放。
type AuthSession struct {
	ChannelID          uint       `gorm:"primaryKey" json:"channel_id"`
	UserID             string     `gorm:"size:64" json:"user_id,omitempty"`
	AccessTokenCipher  string     `gorm:"type:text" json:"-"`
	RefreshTokenCipher string     `gorm:"type:text" json:"-"`
	CookieCipher       string     `gorm:"type:text" json:"-"`
	CSRFTokenCipher    string     `gorm:"size:1024" json:"-"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (AuthSession) TableName() string { return "auth_sessions" }

// CaptchaProviderType 打码平台类型。
type CaptchaProviderType string

const (
	CaptchaCapSolver   CaptchaProviderType = "capsolver"
	CaptchaTwoCaptcha  CaptchaProviderType = "2captcha"
	CaptchaAntiCaptcha CaptchaProviderType = "anticaptcha"
	CaptchaYesCaptcha  CaptchaProviderType = "yescaptcha"
)

// CaptchaConfig 打码平台配置。APIKeyCipher 加密保存，Extra 存放各平台差异化 JSON。
type CaptchaConfig struct {
	ID           uint                `gorm:"primaryKey" json:"id"`
	Name         string              `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Type         CaptchaProviderType `gorm:"size:32;not null;index" json:"type"`
	APIKeyCipher string              `gorm:"size:1024" json:"-"`
	Endpoint     string              `gorm:"size:512" json:"endpoint,omitempty"`
	Extra        string              `gorm:"type:text" json:"extra,omitempty"`
	Enabled      bool                `gorm:"default:true" json:"enabled"`
	ProxyEnabled bool                `gorm:"default:false" json:"proxy_enabled"`
	LastBalance  *float64            `json:"last_balance,omitempty"`
	BalanceUnit  string              `gorm:"size:32" json:"balance_unit,omitempty"`
	BalanceAt    *time.Time          `json:"balance_at,omitempty"`
	BalanceError string              `gorm:"type:text" json:"balance_error,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

func (CaptchaConfig) TableName() string { return "captcha_configs" }

// RateSnapshot 渠道当前观察到的模型 / 分组倍率快照。upsert per (channel_id, model_name)。
// 实际的"变化历史"在 RateChangeLog；此表只保存当前状态。
type RateSnapshot struct {
	ID              uint    `gorm:"primaryKey" json:"id"`
	ChannelID       uint    `gorm:"not null;uniqueIndex:idx_rate_chan_model" json:"channel_id"`
	ModelName       string  `gorm:"size:256;not null;uniqueIndex:idx_rate_chan_model" json:"model_name"`
	Description     string  `gorm:"size:512" json:"description,omitempty"`
	Ratio           float64 `gorm:"not null" json:"ratio"`
	CompletionRatio float64 `json:"completion_ratio"`

	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

func (RateSnapshot) TableName() string { return "rate_snapshots" }

// RateChangeLog 倍率变化历史。每次扫描发现倍率数值或分组结构差异时写入一行。
type RateChangeLog struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	ChannelID          uint      `gorm:"not null;index" json:"channel_id"`
	ModelName          string    `gorm:"size:256;not null;index" json:"model_name"`
	ChangeType         string    `gorm:"size:32;not null;default:changed;index" json:"change_type"`
	OldRatio           *float64  `json:"old_ratio,omitempty"`
	NewRatio           float64   `gorm:"not null" json:"new_ratio"`
	OldCompletionRatio *float64  `json:"old_completion_ratio,omitempty"`
	NewCompletionRatio float64   `json:"new_completion_ratio"`
	ChangedAt          time.Time `gorm:"not null;index" json:"changed_at"`
}

func (RateChangeLog) TableName() string { return "rate_change_logs" }

// UpstreamAnnouncement 保存从上游渠道同步到的公告。
type UpstreamAnnouncement struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	ChannelID       uint       `gorm:"not null;uniqueIndex:idx_announcement_chan_source;index" json:"channel_id"`
	SourceKey       string     `gorm:"size:512;not null;uniqueIndex:idx_announcement_chan_source" json:"source_key"`
	Title           string     `gorm:"size:512" json:"title,omitempty"`
	Content         string     `gorm:"type:text;not null" json:"content"`
	Type            string     `gorm:"size:64" json:"type,omitempty"`
	Link            string     `gorm:"size:512" json:"link,omitempty"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty"`
	FirstSeenAt     time.Time  `gorm:"not null;index" json:"first_seen_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (UpstreamAnnouncement) TableName() string { return "upstream_announcements" }

// BalanceSnapshot 周期性余额采样，用于图表展示。
type BalanceSnapshot struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ChannelID uint      `gorm:"not null;index" json:"channel_id"`
	Balance   float64   `gorm:"not null" json:"balance"`
	SampledAt time.Time `gorm:"not null;index" json:"sampled_at"`
}

func (BalanceSnapshot) TableName() string { return "balance_snapshots" }

// CostSnapshot 周期性消费采样，用于图表展示。
type CostSnapshot struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ChannelID uint      `gorm:"not null;index" json:"channel_id"`
	TodayCost float64   `gorm:"not null" json:"today_cost"`
	SampledAt time.Time `gorm:"not null;index" json:"sampled_at"`
}

func (CostSnapshot) TableName() string { return "cost_snapshots" }

// NotificationChannelType 通知渠道类型。第一版至少 telegram，其它预留。
type NotificationChannelType string

const (
	NotifyTelegram    NotificationChannelType = "telegram"
	NotifyWebhook     NotificationChannelType = "webhook"
	NotifyEmail       NotificationChannelType = "email"
	NotifyWecom       NotificationChannelType = "wecom"
	NotifyDingTalk    NotificationChannelType = "dingtalk"
	NotifyFeishu      NotificationChannelType = "feishu"
	NotifyServerChan3 NotificationChannelType = "serverchan3"
)

// NotificationChannel 通知渠道配置。ConfigCipher 加密保存 JSON 配置（含 token / webhook url / 密码等）。
//
// Subscriptions 是 JSON 数组，记录该渠道关心的上游、事件和分组过滤；为空 / "[]" 表示订阅一切。
// 非敏感数据，明文保存，方便 Dispatcher 直接读取过滤而不解密。
type NotificationChannel struct {
	ID            uint                    `gorm:"primaryKey" json:"id"`
	Name          string                  `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Type          NotificationChannelType `gorm:"size:32;not null;index" json:"type"`
	ConfigCipher  string                  `gorm:"type:text;not null" json:"-"`
	Subscriptions string                  `gorm:"size:4096;not null;default:'[]'" json:"subscriptions"`
	Enabled       bool                    `gorm:"default:true" json:"enabled"`
	ProxyEnabled  bool                    `gorm:"default:false" json:"proxy_enabled"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

func (NotificationChannel) TableName() string { return "notification_channels" }

// NotificationEvent 系统内部触发的通知事件类型。
type NotificationEvent string

const (
	EventBalanceLow                  NotificationEvent = "balance_low"
	EventRateChanged                 NotificationEvent = "rate_changed"
	EventRateStructureChanged        NotificationEvent = "rate_structure_changed"
	EventRateAdded                   NotificationEvent = "rate_added"
	EventRateRemoved                 NotificationEvent = "rate_removed"
	EventAnnouncement                NotificationEvent = "announcement"
	EventLoginFailed                 NotificationEvent = "login_failed"
	EventCaptchaFailed               NotificationEvent = "captcha_failed"
	EventMonitorFailed               NotificationEvent = "monitor_failed"
	EventSubscriptionDailyLow        NotificationEvent = "subscription_daily_remaining_low"
	EventSubscriptionWeeklyLow       NotificationEvent = "subscription_weekly_remaining_low"
	EventSubscriptionMonthlyLow      NotificationEvent = "subscription_monthly_remaining_low"
	EventSubscriptionExpiring        NotificationEvent = "subscription_expiring"
	EventShopGoodsAdded              NotificationEvent = "shop_goods_added"
	EventShopGoodsRemoved            NotificationEvent = "shop_goods_removed"
	EventShopPriceChanged            NotificationEvent = "shop_price_changed"
	EventShopStockChanged            NotificationEvent = "shop_stock_changed"
	EventShopStockLow                NotificationEvent = "shop_stock_low"
	EventShopGoodsRestocked          NotificationEvent = "shop_goods_restocked"
	EventShopMonitorFailed           NotificationEvent = "shop_monitor_failed"
	EventAutoGroupSwitched           NotificationEvent = "auto_group_switched"
	EventAutoGroupUnavailable        NotificationEvent = "auto_group_unavailable"
	EventAutoGroupFailed             NotificationEvent = "auto_group_failed"
	EventAutoGroupCircuitOpened      NotificationEvent = "auto_group_circuit_opened"
	EventAutoGroupAllUnavailable     NotificationEvent = "auto_group_all_unavailable"
	EventAutoGroupRecovered          NotificationEvent = "auto_group_recovered"
	EventAutoGroupTargetUpdateFailed NotificationEvent = "auto_group_target_update_failed"
	EventAutoGroupProbeFailed        NotificationEvent = "auto_group_probe_failed"
	EventAutoGroupPolicyError        NotificationEvent = "auto_group_policy_error"
	EventPriceAILowestPriceDropped   NotificationEvent = "priceai_lowest_price_dropped"
	EventPriceAITargetPriceHit       NotificationEvent = "priceai_target_price_hit"
	EventPriceAIOutOfStock           NotificationEvent = "priceai_out_of_stock"
	EventPriceAIRestocked            NotificationEvent = "priceai_restocked"
	EventPriceAINewPublicLowestOffer NotificationEvent = "priceai_new_public_lowest_offer"
	EventPriceAIFeedStale            NotificationEvent = "priceai_feed_stale"
	EventPriceAISyncFailed           NotificationEvent = "priceai_sync_failed"
	EventPriceAISyncRecovered        NotificationEvent = "priceai_sync_recovered"
)

// NotificationLog 通知发送记录。
type NotificationLog struct {
	ID                uint              `gorm:"primaryKey" json:"id"`
	ChannelID         uint              `gorm:"not null;index" json:"channel_id"`
	UpstreamChannelID uint              `gorm:"not null;default:0;index" json:"upstream_channel_id,omitempty"`
	Event             NotificationEvent `gorm:"size:64;not null;index" json:"event"`
	Subject           string            `gorm:"size:512;not null" json:"subject"`
	Body              string            `gorm:"type:text" json:"body"`
	Success           bool              `gorm:"not null" json:"success"`
	ErrorMessage      string            `gorm:"type:text" json:"error_message,omitempty"`
	SentAt            time.Time         `gorm:"not null;index" json:"sent_at"`
}

func (NotificationLog) TableName() string { return "notification_logs" }

// NotificationCooldown 跨重启持久化的通知冷却记录。
//
// 业务键 (ChannelID, Event)：标记某渠道某类事件最近一次发送时间。
// Dispatcher 在发送 cooldown-aware 事件（如 balance_low）前查这张表，
// 命中且未过 cooldown 就跳过。
//
// 不和 NotificationLog 合并是因为：
//   - NotificationLog 是审计/历史日志（用户可见、可清理）
//   - NotificationCooldown 是去抖控制平面（仅最新一条、原子 upsert）
//
// ChannelID 这里指的是**上游渠道**（storage.Channel），不是通知渠道。
type NotificationCooldown struct {
	ChannelID  uint              `gorm:"primaryKey" json:"channel_id"`
	Event      NotificationEvent `gorm:"primaryKey;size:64" json:"event"`
	LastSentAt time.Time         `gorm:"not null" json:"last_sent_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

func (NotificationCooldown) TableName() string { return "notification_cooldowns" }

// MonitorJob 监控任务类型。
type MonitorJob string

const (
	MonitorJobLogin   MonitorJob = "login"
	MonitorJobBalance MonitorJob = "balance"
	MonitorJobRates   MonitorJob = "rates"
)

// MonitorLog 每次扫描 / 登录尝试的结果，便于诊断失败。
type MonitorLog struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	ChannelID    uint       `gorm:"not null;index" json:"channel_id"`
	Job          MonitorJob `gorm:"size:32;not null;index" json:"job"`
	Success      bool       `gorm:"not null" json:"success"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	DurationMS   int64      `json:"duration_ms"`
	StartedAt    time.Time  `gorm:"not null;index" json:"started_at"`
	FinishedAt   time.Time  `json:"finished_at"`
}

func (MonitorLog) TableName() string { return "monitor_logs" }

type ShopTarget struct {
	ID             uint         `gorm:"primaryKey" json:"id"`
	Name           string       `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Platform       ShopPlatform `gorm:"size:32;not null;index" json:"platform"`
	SiteURL        string       `gorm:"size:512;not null" json:"site_url"`
	BaseURL        string       `gorm:"size:512;not null" json:"base_url"`
	Token          string       `gorm:"size:128;not null;index" json:"token"`
	MonitorEnabled bool         `gorm:"default:true" json:"monitor_enabled"`
	// NotifyEnabled is retained for database/API compatibility with pre-global notification data.
	// Global watch rules select shop changes, then notification channel subscriptions deliver them.
	NotifyEnabled       bool          `gorm:"default:false" json:"notify_enabled"`
	ScopeMode           ShopScopeMode `gorm:"size:32;not null;default:'all'" json:"scope_mode"`
	GoodsTypesJSON      string        `gorm:"type:text" json:"goods_types_json"`
	CategoryIDsJSON     string        `gorm:"type:text" json:"category_ids_json"`
	CategoryNamesJSON   string        `gorm:"type:text" json:"category_names_json"`
	KeywordsJSON        string        `gorm:"type:text" json:"keywords_json"`
	GoodsKeysJSON       string        `gorm:"type:text" json:"goods_keys_json"`
	StockThreshold      int           `gorm:"default:0" json:"stock_threshold"`
	PriceChangeEnabled  bool          `gorm:"default:true" json:"price_change_enabled"`
	StockChangeEnabled  bool          `gorm:"default:true" json:"stock_change_enabled"`
	LowStockEnabled     bool          `gorm:"default:true" json:"low_stock_enabled"`
	RestockEnabled      bool          `gorm:"default:true" json:"restock_enabled"`
	NewGoodsEnabled     bool          `gorm:"default:true" json:"new_goods_enabled"`
	RemovedGoodsEnabled bool          `gorm:"default:true" json:"removed_goods_enabled"`
	ProxyEnabled        bool          `gorm:"default:false" json:"proxy_enabled"`
	SortOrder           int           `gorm:"not null;default:1" json:"sort_order"`
	GoodsSort           string        `gorm:"size:32;not null;default:'category'" json:"goods_sort"`
	LastSyncAt          *time.Time    `json:"last_sync_at,omitempty"`
	LastInfoAt          *time.Time    `json:"last_info_at,omitempty"`
	LastError           string        `gorm:"type:text" json:"last_error,omitempty"`
	LastShopName        string        `gorm:"size:256" json:"last_shop_name,omitempty"`
	LastGoodsCount      int           `gorm:"default:0" json:"last_goods_count"`
	LastLowStockGoods   int           `gorm:"default:0" json:"last_low_stock_goods"`
	LastChangedCount    int           `gorm:"default:0" json:"last_changed_count"`
	WatchRuleCount      int           `gorm:"-" json:"watch_rule_count"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

func (ShopTarget) TableName() string { return "shop_targets" }

type ShopWatchRule struct {
	// ShopWatchRule is retained for backwards-compatible reads and migrations. Runtime shop
	// notifications only consult global rules, then deliver through channel subscriptions.
	ID                  uint      `gorm:"primaryKey" json:"id"`
	TargetID            uint      `gorm:"not null;index" json:"target_id"`
	Name                string    `gorm:"size:128;not null" json:"name"`
	Enabled             bool      `gorm:"default:true;index" json:"enabled"`
	GoodsKeysJSON       string    `gorm:"type:text" json:"goods_keys_json"`
	CategoryIDsJSON     string    `gorm:"type:text" json:"category_ids_json"`
	CategoryNamesJSON   string    `gorm:"type:text" json:"category_names_json"`
	KeywordsJSON        string    `gorm:"type:text" json:"keywords_json"`
	ExcludeKeywordsJSON string    `gorm:"type:text" json:"exclude_keywords_json"`
	EventsJSON          string    `gorm:"type:text" json:"events_json"`
	StockThreshold      int       `gorm:"default:0" json:"stock_threshold"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (ShopWatchRule) TableName() string { return "shop_watch_rules" }

type ShopGoodsSnapshot struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	TargetID      uint       `gorm:"not null;uniqueIndex:idx_shop_goods_target_key;index" json:"target_id"`
	GoodsKey      string     `gorm:"size:128;not null;uniqueIndex:idx_shop_goods_target_key" json:"goods_key"`
	GoodsType     string     `gorm:"size:32;not null;index" json:"goods_type"`
	Name          string     `gorm:"size:512;not null" json:"name"`
	NameKey       string     `gorm:"size:512;not null;default:'';index" json:"-"`
	CategoryID    int64      `gorm:"index" json:"category_id"`
	CategoryName  string     `gorm:"size:256" json:"category_name"`
	Link          string     `gorm:"size:512" json:"link"`
	Price         float64    `gorm:"not null" json:"price"`
	MarketPrice   float64    `json:"market_price"`
	StockCount    int        `json:"stock_count"`
	LimitCount    int        `json:"limit_count"`
	SendOrder     int        `json:"send_order"`
	ContactFormat string     `gorm:"size:64" json:"contact_format"`
	RawJSON       string     `gorm:"type:text" json:"raw_json,omitempty"`
	FirstSeenAt   time.Time  `gorm:"not null;index" json:"first_seen_at"`
	LastSeenAt    time.Time  `gorm:"not null;index" json:"last_seen_at"`
	LastChangedAt *time.Time `json:"last_changed_at,omitempty"`
	RemovedAt     *time.Time `gorm:"index" json:"removed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (ShopGoodsSnapshot) TableName() string { return "shop_goods_snapshots" }

// ShopGoodsNameKey 返回同名分组使用的规范化键：名称去首尾空白并按 Unicode 规则小写，
// 空名称回退到商品键。键在写入侧由 Go 统一计算并持久化到 name_key 列，
// 避免分组查询依赖各数据库 LOWER/TRIM 的方言差异，也免去逐次表达式全表扫描。
func ShopGoodsNameKey(name, goodsKey string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(goodsKey))
	}
	return key
}

// BeforeSave 保证 name_key 始终与 name/goods_key 同步；Create 与 Save 都会经过这里。
func (s *ShopGoodsSnapshot) BeforeSave(*gorm.DB) error {
	s.NameKey = ShopGoodsNameKey(s.Name, s.GoodsKey)
	return nil
}

type ShopGoodsChangeEvent string

const (
	ShopChangeGoodsAdded     ShopGoodsChangeEvent = "goods_added"
	ShopChangeGoodsRemoved   ShopGoodsChangeEvent = "goods_removed"
	ShopChangePriceChanged   ShopGoodsChangeEvent = "price_changed"
	ShopChangeStockChanged   ShopGoodsChangeEvent = "stock_changed"
	ShopChangeStockLow       ShopGoodsChangeEvent = "stock_low"
	ShopChangeGoodsRestocked ShopGoodsChangeEvent = "goods_restocked"
	ShopChangeMonitorFailed  ShopGoodsChangeEvent = "monitor_failed"
)

type ShopGoodsChangeLog struct {
	ID        uint                 `gorm:"primaryKey" json:"id"`
	TargetID  uint                 `gorm:"not null;index" json:"target_id"`
	GoodsKey  string               `gorm:"size:128;index" json:"goods_key"`
	GoodsName string               `gorm:"size:512" json:"goods_name"`
	Event     ShopGoodsChangeEvent `gorm:"size:64;not null;index" json:"event"`
	OldValue  string               `gorm:"type:text" json:"old_value,omitempty"`
	NewValue  string               `gorm:"type:text" json:"new_value,omitempty"`
	Summary   string               `gorm:"type:text" json:"summary"`
	ChangedAt time.Time            `gorm:"not null;index" json:"changed_at"`
	CreatedAt time.Time            `json:"created_at"`
}

func (ShopGoodsChangeLog) TableName() string { return "shop_goods_change_logs" }

type ShopMonitorLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	TargetID     uint      `gorm:"not null;index" json:"target_id"`
	Success      bool      `gorm:"not null;index" json:"success"`
	ErrorMessage string    `gorm:"type:text" json:"error_message,omitempty"`
	GoodsCount   int       `json:"goods_count"`
	ChangedCount int       `json:"changed_count"`
	StartedAt    time.Time `gorm:"not null;index" json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	DurationMS   int64     `json:"duration_ms"`
	CreatedAt    time.Time `json:"created_at"`
}

func (ShopMonitorLog) TableName() string { return "shop_monitor_logs" }

type ShopSyncJobStatus string

const (
	ShopSyncJobQueued    ShopSyncJobStatus = "queued"
	ShopSyncJobRunning   ShopSyncJobStatus = "running"
	ShopSyncJobSucceeded ShopSyncJobStatus = "succeeded"
	ShopSyncJobFailed    ShopSyncJobStatus = "failed"
	ShopSyncJobTimedOut  ShopSyncJobStatus = "timed_out"
	ShopSyncJobSkipped   ShopSyncJobStatus = "skipped"
	ShopSyncJobCancelled ShopSyncJobStatus = "cancelled"
)

// ShopSyncJob keeps manual shop synchronization independent from the request
// that started it, so reverse-proxy timeouts cannot interrupt the work.
type ShopSyncJob struct {
	ID                uint              `gorm:"primaryKey" json:"id"`
	TargetID          uint              `gorm:"not null;index" json:"target_id"`
	Status            ShopSyncJobStatus `gorm:"size:32;not null;index" json:"status"`
	ErrorMessage      string            `gorm:"type:text" json:"error_message,omitempty"`
	GoodsCount        int               `json:"goods_count"`
	ChangedCount      int               `json:"changed_count"`
	EventsJSON        string            `gorm:"type:text" json:"events_json,omitempty"`
	StartedAt         *time.Time        `json:"started_at,omitempty"`
	FinishedAt        *time.Time        `json:"finished_at,omitempty"`
	DurationMS        int64             `json:"duration_ms"`
	RequestCount      int               `json:"request_count"`
	RequestDurationMS int64             `json:"request_duration_ms"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

func (ShopSyncJob) TableName() string { return "shop_sync_jobs" }

type ShopSyncBatchStatus string
type ShopSyncBatchSource string

const (
	ShopSyncBatchRunning    ShopSyncBatchStatus = "running"
	ShopSyncBatchSucceeded  ShopSyncBatchStatus = "succeeded"
	ShopSyncBatchPartial    ShopSyncBatchStatus = "partial"
	ShopSyncBatchFailed     ShopSyncBatchStatus = "failed"
	ShopSyncBatchCancelling ShopSyncBatchStatus = "cancelling"
	ShopSyncBatchCancelled  ShopSyncBatchStatus = "cancelled"

	ShopSyncBatchSourceManual ShopSyncBatchSource = "manual"
	ShopSyncBatchSourceCron   ShopSyncBatchSource = "cron"
)

// ShopSyncBatch records one "sync all" operation. Job IDs are stored
// separately from the aggregate counters so reused jobs can belong to a new batch.
type ShopSyncBatch struct {
	ID                uint                `gorm:"primaryKey" json:"id"`
	Status            ShopSyncBatchStatus `gorm:"size:32;not null;index" json:"status"`
	Source            ShopSyncBatchSource `gorm:"size:16;not null;default:manual;index" json:"source"`
	TotalCount        int                 `json:"total"`
	QueuedCount       int                 `json:"queued"`
	ReusedCount       int                 `json:"reused"`
	StartFailedCount  int                 `json:"start_failed"`
	SucceededCount    int                 `json:"succeeded"`
	FailedCount       int                 `json:"failed"`
	SkippedCount      int                 `json:"skipped"`
	CancelledCount    int                 `json:"cancelled"`
	JobIDsJSON        string              `gorm:"type:text" json:"-"`
	StartedAt         time.Time           `gorm:"not null;index" json:"started_at"`
	CancelRequestedAt *time.Time          `json:"cancel_requested_at,omitempty"`
	CancelledAt       *time.Time          `json:"cancelled_at,omitempty"`
	FinishedAt        *time.Time          `json:"finished_at,omitempty"`
	DurationMS        int64               `json:"duration_ms"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

func (ShopSyncBatch) TableName() string { return "shop_sync_batches" }

// ShopSyncBatchItem keeps the target snapshot and job association for every
// target selected by one sync-all operation.
type ShopSyncBatchItem struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	BatchID    uint      `gorm:"not null;uniqueIndex:idx_shop_sync_batch_target;index" json:"batch_id"`
	TargetID   uint      `gorm:"not null;uniqueIndex:idx_shop_sync_batch_target;index" json:"target_id"`
	TargetName string    `gorm:"size:128;not null" json:"target_name"`
	JobID      uint      `gorm:"index" json:"job_id,omitempty"`
	Reused     bool      `gorm:"not null;default:false" json:"reused"`
	StartError string    `gorm:"type:text" json:"start_error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (ShopSyncBatchItem) TableName() string { return "shop_sync_batch_items" }

// PriceAI public-board and catalog values are stored independently from shop
// monitoring. The records below describe only PriceAI's documented Feed and
// the narrowly scoped page-derived risk cache.
type PriceAIBoardKind string

const (
	PriceAIBoardDefault PriceAIBoardKind = "default"
	PriceAIBoardPreset  PriceAIBoardKind = "preset"
)

type PriceAIChangeEvent string

const (
	PriceAIChangeBaselineCreated       PriceAIChangeEvent = "baseline_created"
	PriceAIChangeCatalogProductMissing PriceAIChangeEvent = "catalog_product_missing"
	PriceAIChangeLowestPriceChanged    PriceAIChangeEvent = "lowest_price_changed"
	PriceAIChangeCurrencyChanged       PriceAIChangeEvent = "lowest_price_currency_changed"
	PriceAIChangeInStockCountChanged   PriceAIChangeEvent = "in_stock_count_changed"
	PriceAIChangeOfferCountChanged     PriceAIChangeEvent = "offer_count_changed"
	PriceAIChangePublicBoardChanged    PriceAIChangeEvent = "public_board_changed"
	PriceAIChangeTargetPriceHit        PriceAIChangeEvent = "target_price_hit"
	PriceAIChangeFeedBecameStale       PriceAIChangeEvent = "feed_became_stale"
	PriceAIChangeFeedRecovered         PriceAIChangeEvent = "feed_recovered"
	PriceAIChangeSyncFailed            PriceAIChangeEvent = "sync_failed"
	PriceAIChangeSyncRecovered         PriceAIChangeEvent = "sync_recovered"
)

type PriceAISyncJobKind string

const (
	PriceAISyncJobFeed PriceAISyncJobKind = "feed"
	PriceAISyncJobRisk PriceAISyncJobKind = "risk"
)

type PriceAIRiskScope string

const (
	PriceAIRiskScopeSource PriceAIRiskScope = "source"
	PriceAIRiskScopeOffer  PriceAIRiskScope = "offer"
)

// PriceAIFeedState keeps conditional request metadata and import health for
// one documented PriceAI Feed source.
type PriceAIFeedState struct {
	SourceKey           string     `gorm:"primaryKey;size:64" json:"source_key"`
	LatestURL           string     `gorm:"size:1024;not null" json:"latest_url"`
	SchemaURL           string     `gorm:"size:1024;not null" json:"schema_url"`
	ETag                string     `gorm:"column:etag;size:512" json:"etag,omitempty"`
	LastModified        string     `gorm:"size:512" json:"last_modified,omitempty"`
	SnapshotID          string     `gorm:"size:256" json:"snapshot_id,omitempty"`
	SnapshotURL         string     `gorm:"size:1024" json:"snapshot_url,omitempty"`
	SchemaVersion       string     `gorm:"size:64" json:"schema_version,omitempty"`
	GeneratedAt         *time.Time `json:"generated_at,omitempty"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	FeedStale           bool       `gorm:"not null;default:false" json:"feed_stale"`
	LastAttemptAt       *time.Time `gorm:"index" json:"last_attempt_at,omitempty"`
	LastSuccessAt       *time.Time `gorm:"index" json:"last_success_at,omitempty"`
	ConsecutiveFailures int        `gorm:"not null;default:0" json:"consecutive_failures"`
	LastError           string     `gorm:"type:text" json:"last_error,omitempty"`
	// MySQL 不允许 TEXT 列带 default，这里不设默认值；创建路径由 Go 显式写 "[]"，
	// 读取侧 decodeSeededSlugs 兼容空串。
	DefaultWatchSeededSlugsJSON string    `gorm:"type:text;not null" json:"default_watch_seeded_slugs_json"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

func (PriceAIFeedState) TableName() string { return "priceai_feed_state" }

// PriceAIProduct is the imported Feed catalog, not an assertion about the
// complete PriceAI marketplace.
type PriceAIProduct struct {
	ID                         uint       `gorm:"primaryKey" json:"id"`
	RemoteID                   string     `gorm:"size:256;not null;uniqueIndex:uq_priceai_products_remote_id" json:"remote_id"`
	Slug                       string     `gorm:"size:256;not null;uniqueIndex:uq_priceai_products_slug" json:"slug"`
	Name                       string     `gorm:"size:512;not null;index" json:"name"`
	Platform                   string     `gorm:"size:128;index" json:"platform,omitempty"`
	ProductType                string     `gorm:"size:128;index" json:"product_type,omitempty"`
	Spec                       string     `gorm:"type:text" json:"spec,omitempty"`
	Summary                    string     `gorm:"type:text" json:"summary,omitempty"`
	OfferCount                 int        `gorm:"not null;default:0" json:"offer_count"`
	InStockCount               int        `gorm:"not null;default:0" json:"in_stock_count"`
	LowestPrice                *float64   `json:"lowest_price,omitempty"`
	LowestPriceCurrency        *string    `gorm:"size:32" json:"lowest_price_currency,omitempty"`
	LatestSeenAt               time.Time  `gorm:"not null;index" json:"latest_seen_at"`
	ProductSnapshotGeneratedAt time.Time  `gorm:"not null;index" json:"product_snapshot_generated_at"`
	LastSnapshotID             string     `gorm:"size:256;not null;index" json:"last_snapshot_id"`
	FirstSeenAt                time.Time  `gorm:"not null;index" json:"first_seen_at"`
	LastSeenAt                 time.Time  `gorm:"not null;index" json:"last_seen_at"`
	MissingFromLatestAt        *time.Time `gorm:"index" json:"missing_from_latest_at,omitempty"`
	RawJSON                    string     `gorm:"type:text" json:"-"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

func (PriceAIProduct) TableName() string { return "priceai_products" }

// PriceAIWatchTarget is intentionally separate from ShopTarget so monitoring
// and notification preferences do not cross domains.
type PriceAIWatchTarget struct {
	ID                          uint       `gorm:"primaryKey" json:"id"`
	ProductID                   uint       `gorm:"not null;uniqueIndex:uq_priceai_watch_targets_product_id" json:"product_id"`
	MonitorEnabled              bool       `gorm:"not null;default:true" json:"monitor_enabled"`
	NotifyEnabled               bool       `gorm:"not null;default:false" json:"notify_enabled"`
	TargetPrice                 *float64   `json:"target_price,omitempty"`
	TargetPriceCurrency         *string    `gorm:"size:32" json:"target_price_currency,omitempty"`
	PriceDropPercent            *float64   `json:"price_drop_percent,omitempty"`
	NotificationCooldownMinutes int        `gorm:"not null;default:0" json:"notification_cooldown_minutes"`
	BaselineSnapshotID          string     `gorm:"size:256;not null" json:"baseline_snapshot_id"`
	LastNotifiedSnapshotID      *string    `gorm:"size:256" json:"last_notified_snapshot_id,omitempty"`
	LastNotifiedAt              *time.Time `json:"last_notified_at,omitempty"`
	CreatedAt                   time.Time  `json:"created_at"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

func (PriceAIWatchTarget) TableName() string { return "priceai_watch_targets" }

type PriceAIPreset struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ProductID      uint      `gorm:"not null;uniqueIndex:uq_priceai_presets_product_remote_id;index" json:"product_id"`
	RemoteID       string    `gorm:"size:256;not null;uniqueIndex:uq_priceai_presets_product_remote_id" json:"remote_id"`
	Label          string    `gorm:"size:512;not null" json:"label"`
	GroupName      string    `gorm:"size:256" json:"group_name,omitempty"`
	Description    string    `gorm:"type:text" json:"description,omitempty"`
	Total          int       `gorm:"not null;default:0" json:"total"`
	GeneratedAt    time.Time `gorm:"not null;index" json:"generated_at"`
	LastSnapshotID string    `gorm:"size:256;not null;index" json:"last_snapshot_id"`
	RawJSON        string    `gorm:"type:text" json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (PriceAIPreset) TableName() string { return "priceai_presets" }

type PriceAIOffer struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	ProductID       uint      `gorm:"not null;uniqueIndex:uq_priceai_offers_product_dedupe;index" json:"product_id"`
	RemoteID        string    `gorm:"size:256;index" json:"remote_id,omitempty"`
	DedupeKey       string    `gorm:"size:1024;not null;uniqueIndex:uq_priceai_offers_product_dedupe,length:700" json:"dedupe_key"`
	SourceID        string    `gorm:"size:256;index" json:"source_id,omitempty"`
	SourceName      string    `gorm:"size:512" json:"source_name,omitempty"`
	SourceStoreName string    `gorm:"size:512" json:"source_store_name,omitempty"`
	MerchantKey     string    `gorm:"size:1024;not null;index:,length:700" json:"merchant_key"`
	Title           string    `gorm:"type:text;not null" json:"title"`
	NormalizedTitle string    `gorm:"size:1024;not null;index:,length:700" json:"normalized_title"`
	Price           float64   `gorm:"not null" json:"price"`
	Currency        string    `gorm:"size:32" json:"currency,omitempty"`
	Status          string    `gorm:"size:128;index" json:"status,omitempty"`
	URL             string    `gorm:"size:2048;not null" json:"url"`
	LastSnapshotID  string    `gorm:"size:256;not null;index" json:"last_snapshot_id"`
	FirstSeenAt     time.Time `gorm:"not null;index" json:"first_seen_at"`
	LastSeenAt      time.Time `gorm:"not null;index" json:"last_seen_at"`
	RawJSON         string    `gorm:"type:text" json:"-"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (PriceAIOffer) TableName() string { return "priceai_offers" }

type PriceAIOfferRanking struct {
	ID               uint             `gorm:"primaryKey" json:"id"`
	ProductID        uint             `gorm:"not null;uniqueIndex:uq_priceai_offer_rankings_membership;index" json:"product_id"`
	OfferID          uint             `gorm:"not null;uniqueIndex:uq_priceai_offer_rankings_membership;index" json:"offer_id"`
	BoardKind        PriceAIBoardKind `gorm:"size:16;not null;uniqueIndex:uq_priceai_offer_rankings_membership" json:"board_kind"`
	PresetID         string           `gorm:"size:256;not null;uniqueIndex:uq_priceai_offer_rankings_membership" json:"preset_id,omitempty"`
	Rank             int              `gorm:"not null" json:"rank"`
	BoardGeneratedAt time.Time        `gorm:"not null;index" json:"board_generated_at"`
	LastSnapshotID   string           `gorm:"size:256;not null;index" json:"last_snapshot_id"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

func (PriceAIOfferRanking) TableName() string { return "priceai_offer_rankings" }

type PriceAISyncLog struct {
	ID                   uint               `gorm:"primaryKey" json:"id"`
	JobKind              PriceAISyncJobKind `gorm:"size:16;not null;index" json:"job_kind"`
	SnapshotID           string             `gorm:"size:256;index" json:"snapshot_id,omitempty"`
	Success              bool               `gorm:"not null" json:"success"`
	NotModified          bool               `gorm:"not null;default:false" json:"not_modified"`
	ProductsCount        int                `gorm:"not null;default:0" json:"products_count"`
	OffersCount          int                `gorm:"not null;default:0" json:"offers_count"`
	ChangedProductsCount int                `gorm:"not null;default:0" json:"changed_products_count"`
	ErrorMessage         string             `gorm:"type:text" json:"error_message,omitempty"`
	StartedAt            time.Time          `gorm:"not null;index" json:"started_at"`
	FinishedAt           time.Time          `gorm:"not null" json:"finished_at"`
	DurationMS           int64              `gorm:"not null;default:0" json:"duration_ms"`
	CreatedAt            time.Time          `json:"created_at"`
}

func (PriceAISyncLog) TableName() string { return "priceai_sync_logs" }

type PriceAIRiskFeedback struct {
	ID              uint             `gorm:"primaryKey" json:"id"`
	ProductID       uint             `gorm:"not null;uniqueIndex:uq_priceai_risk_feedback_subject;index" json:"product_id"`
	Scope           PriceAIRiskScope `gorm:"size:16;not null;uniqueIndex:uq_priceai_risk_feedback_subject" json:"scope"`
	SubjectRemoteID string           `gorm:"size:256;not null;uniqueIndex:uq_priceai_risk_feedback_subject" json:"subject_remote_id"`
	Status          string           `gorm:"size:128;index" json:"status,omitempty"`
	FeedbackCount   int              `gorm:"not null;default:0" json:"feedback_count"`
	ReasonsJSON     string           `gorm:"type:text" json:"reasons_json,omitempty"`
	SummariesJSON   string           `gorm:"type:text" json:"summaries_json,omitempty"`
	LatestAt        *time.Time       `json:"latest_at,omitempty"`
	PageURL         string           `gorm:"size:1024" json:"page_url,omitempty"`
	FetchedAt       *time.Time       `gorm:"index" json:"fetched_at,omitempty"`
	LastError       string           `gorm:"type:text" json:"last_error,omitempty"`
	RawJSON         string           `gorm:"type:text" json:"-"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

func (PriceAIRiskFeedback) TableName() string { return "priceai_risk_feedback" }

// PriceAILDXPTargetBinding is the exclusive ownership marker for automatic
// PriceAI-created exact LDXP targets. Manual targets must never gain a row.
type PriceAILDXPTargetBinding struct {
	ID           uint         `gorm:"primaryKey" json:"id"`
	Platform     ShopPlatform `gorm:"size:32;not null;uniqueIndex:uq_priceai_ldxp_target_identity" json:"platform"`
	BaseURL      string       `gorm:"size:512;not null;uniqueIndex:uq_priceai_ldxp_target_identity" json:"base_url"`
	Token        string       `gorm:"size:128;not null;uniqueIndex:uq_priceai_ldxp_target_identity" json:"token"`
	ShopTargetID uint         `gorm:"not null;uniqueIndex:uq_priceai_ldxp_target_shop_target" json:"shop_target_id"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

func (PriceAILDXPTargetBinding) TableName() string { return "priceai_ldxp_target_bindings" }

type SavedSearchConditionField string

const (
	SavedSearchConditionKeyword      SavedSearchConditionField = "keyword"
	SavedSearchConditionExclude      SavedSearchConditionField = "exclude_keyword"
	SavedSearchConditionCategoryName SavedSearchConditionField = "category_name"
)

var SavedSearchConditionFields = []SavedSearchConditionField{
	SavedSearchConditionKeyword,
	SavedSearchConditionExclude,
	SavedSearchConditionCategoryName,
}

type SavedSearchCondition struct {
	ID              uint                      `gorm:"primaryKey" json:"id"`
	Field           SavedSearchConditionField `gorm:"size:32;not null;uniqueIndex:idx_saved_search_conditions_field_value" json:"field"`
	Value           string                    `gorm:"size:512;not null" json:"value"`
	NormalizedValue string                    `gorm:"size:512;not null;uniqueIndex:idx_saved_search_conditions_field_value" json:"-"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}

func (SavedSearchCondition) TableName() string { return "saved_search_conditions" }

type AutoGroupPolicy struct {
	ID                            uint       `gorm:"primaryKey" json:"id"`
	ChannelID                     uint       `gorm:"not null;uniqueIndex:idx_auto_group_policy_channel_target;index" json:"channel_id"`
	Name                          string     `gorm:"size:128;not null" json:"name"`
	Enabled                       bool       `gorm:"not null;default:true;index" json:"enabled"`
	SortOrder                     int        `gorm:"not null;default:0;index" json:"sort_order"`
	NotifyEnabled                 bool       `gorm:"not null;default:true" json:"notify_enabled"`
	TargetKeyID                   int64      `gorm:"not null;default:0" json:"target_key_id"`
	TargetKeyName                 string     `gorm:"size:128;not null;default:'auto';uniqueIndex:idx_auto_group_policy_channel_target" json:"target_key_name"`
	ProbeKeyID                    int64      `gorm:"not null;default:0" json:"probe_key_id"`
	ProbeKeyName                  string     `gorm:"size:128;not null;default:'ops-probe-auto'" json:"probe_key_name"`
	ProbeModel                    string     `gorm:"size:128;not null;default:'gpt-5.4'" json:"probe_model"`
	ProbeTimeoutSeconds           int        `gorm:"not null;default:15" json:"probe_timeout_seconds"`
	ProbeSuccessCacheMinutes      int        `gorm:"not null;default:60" json:"probe_success_cache_minutes"`
	ProbeFailureRetryMinutes      int        `gorm:"not null;default:10" json:"probe_failure_retry_minutes"`
	ProbeMaxPerRun                int        `gorm:"not null;default:3" json:"probe_max_per_run"`
	IncludeGroupsJSON             string     `gorm:"type:text" json:"include_groups_json"`
	ExcludeGroupsJSON             string     `gorm:"type:text" json:"exclude_groups_json"`
	IncludeKeywordsJSON           string     `gorm:"type:text" json:"include_keywords_json"`
	ExcludeKeywordsJSON           string     `gorm:"type:text" json:"exclude_keywords_json"`
	MinRatio                      float64    `gorm:"not null;default:0" json:"min_ratio"`
	MaxRatio                      float64    `gorm:"not null;default:0" json:"max_ratio"`
	FailureThreshold              int        `gorm:"not null;default:2" json:"failure_threshold"`
	CircuitDurationMinutes        int        `gorm:"not null;default:30" json:"circuit_duration_minutes"`
	HalfOpenSuccessThreshold      int        `gorm:"not null;default:1" json:"half_open_success_threshold"`
	MinRatioImprovementPct        float64    `gorm:"not null;default:5" json:"min_ratio_improvement_pct"`
	SwitchCooldownMinutes         int        `gorm:"not null;default:30" json:"switch_cooldown_minutes"`
	ForceSwitchOnCurrentUnhealthy bool       `gorm:"not null;default:true" json:"force_switch_on_current_unhealthy"`
	KeepCurrentWhenNoAvailable    bool       `gorm:"not null;default:true" json:"keep_current_when_no_available"`
	CurrentGroupName              string     `gorm:"size:256" json:"current_group_name,omitempty"`
	CurrentGroupID                *int64     `json:"current_group_id,omitempty"`
	CurrentRatio                  float64    `gorm:"not null;default:0" json:"current_ratio"`
	LastStatus                    string     `gorm:"size:32;not null;default:'idle'" json:"last_status"`
	LastError                     string     `gorm:"type:text" json:"last_error,omitempty"`
	LastEvaluateAt                *time.Time `json:"last_evaluate_at,omitempty"`
	LastSwitchAt                  *time.Time `json:"last_switch_at,omitempty"`
	CreatedAt                     time.Time  `json:"created_at"`
	UpdatedAt                     time.Time  `json:"updated_at"`
}

func (AutoGroupPolicy) TableName() string { return "auto_group_policies" }

type AutoGroupCandidate struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	PolicyID           uint       `gorm:"not null;uniqueIndex:idx_auto_group_candidate;index" json:"policy_id"`
	GroupName          string     `gorm:"size:256;not null;uniqueIndex:idx_auto_group_candidate" json:"group_name"`
	GroupID            *int64     `json:"group_id,omitempty"`
	Description        string     `gorm:"type:text" json:"description,omitempty"`
	Ratio              float64    `gorm:"not null;default:0" json:"ratio"`
	Status             string     `gorm:"size:32;not null;default:'unknown'" json:"status"`
	Reason             string     `gorm:"type:text" json:"reason,omitempty"`
	FailureCount       int        `gorm:"not null;default:0" json:"failure_count"`
	SuccessCount       int        `gorm:"not null;default:0" json:"success_count"`
	CircuitOpenUntil   *time.Time `json:"circuit_open_until,omitempty"`
	CircuitOpenedAt    *time.Time `json:"circuit_opened_at,omitempty"`
	RecoveredAt        *time.Time `json:"recovered_at,omitempty"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	LastProbeAt        *time.Time `json:"last_probe_at,omitempty"`
	LastProbeSuccess   *bool      `json:"last_probe_success,omitempty"`
	LastProbeLatencyMS int64      `gorm:"not null;default:0" json:"last_probe_latency_ms"`
	LastErrorCode      string     `gorm:"size:64" json:"last_error_code,omitempty"`
	LastError          string     `gorm:"type:text" json:"last_error,omitempty"`
	ManualDisabled     bool       `gorm:"not null;default:false" json:"manual_disabled"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (AutoGroupCandidate) TableName() string { return "auto_group_candidates" }

type AutoGroupEvaluationLog struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	PolicyID         uint      `gorm:"not null;index" json:"policy_id"`
	ChannelID        uint      `gorm:"not null;index" json:"channel_id"`
	Success          bool      `gorm:"not null;index" json:"success"`
	Status           string    `gorm:"size:32;not null;index" json:"status"`
	TargetKeyID      int64     `gorm:"not null;default:0" json:"target_key_id"`
	TargetKeyName    string    `gorm:"size:128" json:"target_key_name,omitempty"`
	CurrentGroup     string    `gorm:"size:256" json:"current_group,omitempty"`
	SelectedGroup    string    `gorm:"size:256" json:"selected_group,omitempty"`
	SelectedRatio    float64   `gorm:"not null;default:0" json:"selected_ratio"`
	CandidateCount   int       `gorm:"not null;default:0" json:"candidate_count"`
	AvailableCount   int       `gorm:"not null;default:0" json:"available_count"`
	CircuitOpenCount int       `gorm:"not null;default:0" json:"circuit_open_count"`
	Action           string    `gorm:"size:64" json:"action,omitempty"`
	Message          string    `gorm:"type:text" json:"message,omitempty"`
	CreatedAt        time.Time `gorm:"not null;index" json:"created_at"`
}

func (AutoGroupEvaluationLog) TableName() string { return "auto_group_evaluation_logs" }

type AutoGroupSwitchLog struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	PolicyID      uint      `gorm:"not null;index" json:"policy_id"`
	ChannelID     uint      `gorm:"not null;index" json:"channel_id"`
	TargetKeyID   int64     `gorm:"not null;default:0" json:"target_key_id"`
	TargetKeyName string    `gorm:"size:128" json:"target_key_name,omitempty"`
	FromGroup     string    `gorm:"size:256" json:"from_group,omitempty"`
	ToGroup       string    `gorm:"size:256" json:"to_group,omitempty"`
	ToGroupID     *int64    `json:"to_group_id,omitempty"`
	ToRatio       float64   `gorm:"not null;default:0" json:"to_ratio"`
	Success       bool      `gorm:"not null;index" json:"success"`
	Reason        string    `gorm:"type:text" json:"reason,omitempty"`
	ErrorMessage  string    `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt     time.Time `gorm:"not null;index" json:"created_at"`
}

func (AutoGroupSwitchLog) TableName() string { return "auto_group_switch_logs" }
