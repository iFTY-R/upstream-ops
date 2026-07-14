package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ifty-r/upstream-ops/backend/storage"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Config struct {
	App           AppConfig           `mapstructure:"app" yaml:"app" json:"app"`
	Server        ServerConfig        `mapstructure:"server" yaml:"server" json:"server"`
	Database      DatabaseConfig      `mapstructure:"database" yaml:"database" json:"database"`
	Security      SecurityConfig      `mapstructure:"security" yaml:"security" json:"security"`
	Auth          AuthConfig          `mapstructure:"auth" yaml:"auth" json:"auth"`
	Scheduler     SchedulerConfig     `mapstructure:"scheduler" yaml:"scheduler" json:"scheduler"`
	Notifications NotificationsConfig `mapstructure:"notifications" yaml:"notifications" json:"notifications"`
	Proxy         ProxyConfig         `mapstructure:"proxy" yaml:"proxy" json:"proxy"`
	Upstream      UpstreamConfig      `mapstructure:"upstream" yaml:"upstream" json:"upstream"`
	Log           LogConfig           `mapstructure:"log" yaml:"log" json:"log"`
}

type AppConfig struct {
	Title              string `mapstructure:"title" yaml:"title" json:"title"`
	NotificationPrefix string `mapstructure:"notificationPrefix" yaml:"notificationPrefix" json:"notificationPrefix"`
}

type ServerConfig struct {
	Port           int      `mapstructure:"port" yaml:"port" json:"port"`
	Mode           string   `mapstructure:"mode" yaml:"mode" json:"mode"`
	TrustedProxies []string `mapstructure:"trustedProxies" yaml:"trustedProxies" json:"trustedProxies"`
	BaseURL        string   `mapstructure:"baseURL" yaml:"baseURL" json:"baseURL"`
}

type DatabaseConfig struct {
	Driver       string `mapstructure:"driver" yaml:"driver" json:"driver"`
	Path         string `mapstructure:"path" yaml:"path" json:"path"`
	Host         string `mapstructure:"host" yaml:"host" json:"host"`
	Port         int    `mapstructure:"port" yaml:"port" json:"port"`
	User         string `mapstructure:"user" yaml:"user" json:"user"`
	Password     string `mapstructure:"password" yaml:"password" json:"password"`
	Name         string `mapstructure:"name" yaml:"name" json:"name"`
	MaxOpenConns int    `mapstructure:"maxOpenConns" yaml:"maxOpenConns" json:"maxOpenConns"`
	MaxIdleConns int    `mapstructure:"maxIdleConns" yaml:"maxIdleConns" json:"maxIdleConns"`
}

func (d DatabaseConfig) ToStorageConfig() storage.DBConfig {
	return storage.DBConfig{
		Driver:       storage.DBDriver(d.Driver),
		Path:         d.Path,
		Host:         d.Host,
		Port:         d.Port,
		User:         d.User,
		Password:     d.Password,
		Name:         d.Name,
		MaxOpenConns: d.MaxOpenConns,
		MaxIdleConns: d.MaxIdleConns,
	}
}

type SecurityConfig struct {
	// AppSecret 主密钥，用于 AES-GCM。优先从 APP_SECRET 环境变量读取。
	AppSecret string `mapstructure:"appSecret" yaml:"appSecret" json:"appSecret"`
}

const DefaultAuthUsername = "admin"

// AuthConfig 后台单用户登录配置。
//
// Enabled = false 时整套鉴权被关掉：/api/* 全部免 token，前端检测后跳过登录页。
// 仅适合可信内网或已由反代鉴权保护的部署；默认开启鉴权以避免公共控制面裸露。
//
// Enabled=true 时 Username/Password 是写死的管理员凭据，TokenSecret 用于签发 HMAC token。
// 如果 TokenSecret 为空，会回退使用 Security.AppSecret，保证有合理默认。
type AuthConfig struct {
	Enabled         bool               `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Username        string             `mapstructure:"username" yaml:"username" json:"username"`
	Password        string             `mapstructure:"password" yaml:"password" json:"password"`
	TokenSecret     string             `mapstructure:"tokenSecret" yaml:"tokenSecret" json:"tokenSecret"`
	SessionTTLHours int                `mapstructure:"sessionTTLHours" yaml:"sessionTTLHours" json:"sessionTTLHours"`
	Sub2APIEmbed    Sub2APIEmbedConfig `mapstructure:"sub2apiEmbed" yaml:"sub2apiEmbed" json:"sub2apiEmbed"`
}

// Sub2APIEmbedConfig 控制从 Sub2API 自定义菜单 iframe 进入时的免登录换票。
//
// Sub2API 会把自己的登录 token 注入 URL；Ops 后端只在 exchange 接口内服务端校验该 token，
// 校验成功后签发 Ops 自己的后台 token，避免前端长期持有或透传第三方系统 token。
type Sub2APIEmbedConfig struct {
	Enabled        bool     `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	BaseURL        string   `mapstructure:"baseURL" yaml:"baseURL" json:"baseURL"`
	AllowedOrigins []string `mapstructure:"allowedOrigins" yaml:"allowedOrigins" json:"allowedOrigins"`
	RequireAdmin   bool     `mapstructure:"requireAdmin" yaml:"requireAdmin" json:"requireAdmin"`
}

type SchedulerConfig struct {
	BalanceCron string          `mapstructure:"balanceCron" yaml:"balanceCron" json:"balanceCron"`
	RateCron    string          `mapstructure:"rateCron" yaml:"rateCron" json:"rateCron"`
	ShopCron    string          `mapstructure:"shopCron" yaml:"shopCron" json:"shopCron"`
	Concurrency int             `mapstructure:"concurrency" yaml:"concurrency" json:"concurrency"`
	AutoGroup   AutoGroupConfig `mapstructure:"autoGroup" yaml:"autoGroup" json:"autoGroup"`
	Retention   RetentionConfig `mapstructure:"retention" yaml:"retention" json:"retention"`
}

type AutoGroupConfig struct {
	Enabled          bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Cron             string `mapstructure:"cron" yaml:"cron" json:"cron"`
	Concurrency      int    `mapstructure:"concurrency" yaml:"concurrency" json:"concurrency"`
	ProbeConcurrency int    `mapstructure:"probeConcurrency" yaml:"probeConcurrency" json:"probeConcurrency"`
}

// RetentionConfig 历史数据保留策略。
//
// 字段为 0 表示该表不清理，永久保留（默认 rate_change_logs 永远保留，是核心业务数据）。
// Cron 为空时不启动清理任务。
type RetentionConfig struct {
	Cron                 string `mapstructure:"cron" yaml:"cron" json:"cron"`
	MonitorLogsDays      int    `mapstructure:"monitorLogsDays" yaml:"monitorLogsDays" json:"monitorLogsDays"`
	BalanceSnapshotsDays int    `mapstructure:"balanceSnapshotsDays" yaml:"balanceSnapshotsDays" json:"balanceSnapshotsDays"`
	NotificationLogsDays int    `mapstructure:"notificationLogsDays" yaml:"notificationLogsDays" json:"notificationLogsDays"`
	AnnouncementsDays    int    `mapstructure:"announcementsDays" yaml:"announcementsDays" json:"announcementsDays"`
}

// NotificationsConfig 通知去抖策略。所有字段都是"少烦我"取向，默认不丢消息只合并。
//
//   - BatchRateChanges：同次扫描中将多个分组的变化合并成 1 条消息，避免上游一次大调价
//     瞬间发出 30+ 条通知刷屏。默认 true。
//   - MinChangePct：涨跌幅 < X% 的 rate_changed 跳过推送（仍会写入 rate_change_logs）。
//     0 = 全发，对应原始行为。
//   - BalanceLowCooldownMinutes：同一渠道的 balance_low 在 X 分钟内不重复推送。
//     0 = 不冷却（每次扫描发现仍 < 阈值都发）。冷却状态持久化在数据库的
//     notification_cooldowns 表，跨重启生效。
//   - SendMaxAttempts：单条通知发送失败时最多尝试次数（含首次）。
//     1 = 不重试。重试采用指数退避：1s / 2s / 4s …，上限 30s。
type NotificationsConfig struct {
	BatchRateChanges                         bool    `mapstructure:"batchRateChanges" yaml:"batchRateChanges" json:"batchRateChanges"`
	MinChangePct                             float64 `mapstructure:"minChangePct" yaml:"minChangePct" json:"minChangePct"`
	BalanceLowCooldownMinutes                int     `mapstructure:"balanceLowCooldownMinutes" yaml:"balanceLowCooldownMinutes" json:"balanceLowCooldownMinutes"`
	SubscriptionDailyRemainingThresholdPct   float64 `mapstructure:"subscriptionDailyRemainingThresholdPct" yaml:"subscriptionDailyRemainingThresholdPct" json:"subscriptionDailyRemainingThresholdPct"`
	SubscriptionWeeklyRemainingThresholdPct  float64 `mapstructure:"subscriptionWeeklyRemainingThresholdPct" yaml:"subscriptionWeeklyRemainingThresholdPct" json:"subscriptionWeeklyRemainingThresholdPct"`
	SubscriptionMonthlyRemainingThresholdPct float64 `mapstructure:"subscriptionMonthlyRemainingThresholdPct" yaml:"subscriptionMonthlyRemainingThresholdPct" json:"subscriptionMonthlyRemainingThresholdPct"`
	SubscriptionExpiryThresholdHours         int     `mapstructure:"subscriptionExpiryThresholdHours" yaml:"subscriptionExpiryThresholdHours" json:"subscriptionExpiryThresholdHours"`
	SubscriptionAlertCooldownMinutes         int     `mapstructure:"subscriptionAlertCooldownMinutes" yaml:"subscriptionAlertCooldownMinutes" json:"subscriptionAlertCooldownMinutes"`
	SendMaxAttempts                          int     `mapstructure:"sendMaxAttempts" yaml:"sendMaxAttempts" json:"sendMaxAttempts"`
}

type ProxyConfig struct {
	Enabled             bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	VersionCheckEnabled bool   `mapstructure:"versionCheckEnabled" yaml:"versionCheckEnabled" json:"versionCheckEnabled"`
	Protocol            string `mapstructure:"protocol" yaml:"protocol" json:"protocol"`
	Host                string `mapstructure:"host" yaml:"host" json:"host"`
	Port                int    `mapstructure:"port" yaml:"port" json:"port"`
	Username            string `mapstructure:"username" yaml:"username" json:"username"`
	Password            string `mapstructure:"password" yaml:"password" json:"password"`
}

const (
	DefaultUpstreamTimeoutSeconds = 30
	DefaultUpstreamUserAgent      = "upstream-ops/0.1"
)

type UpstreamConfig struct {
	TimeoutSeconds int    `mapstructure:"timeoutSeconds" yaml:"timeoutSeconds" json:"timeoutSeconds"`
	UserAgent      string `mapstructure:"userAgent" yaml:"userAgent" json:"userAgent"`
}

func (u UpstreamConfig) WithDefaults() UpstreamConfig {
	if u.TimeoutSeconds <= 0 {
		u.TimeoutSeconds = DefaultUpstreamTimeoutSeconds
	}
	if strings.TrimSpace(u.UserAgent) == "" {
		u.UserAgent = DefaultUpstreamUserAgent
	}
	return u
}

type LogConfig struct {
	Level  string `mapstructure:"level" yaml:"level" json:"level"`
	Format string `mapstructure:"format" yaml:"format" json:"format"`
}

// Load 读取 config.yaml（可选）+ APP_SECRET / * 环境变量覆盖。
//
// 关键映射：
//
//	APP_SECRET                       -> security.appSecret
//	DATABASE_DRIVER      -> database.driver
//	DATABASE_PATH        -> database.path
//	DATABASE_HOST        -> database.host
//	SERVER_PORT          -> server.port
//	SCHEDULER_BALANCECRON-> scheduler.balanceCron
//	SCHEDULER_SHOPCRON   -> scheduler.shopCron
func Load(path string) (*Config, error) {
	cfg, _, err := load(path, true)
	return cfg, err
}

func LoadWithPath(path string) (*Config, string, error) {
	return load(path, true)
}

func LoadFile(path string) (*Config, error) {
	cfg, _, err := load(path, false)
	return cfg, err
}

// BootstrapEnvAuthority 描述哪些启动期敏感字段当前由环境变量控制。
//
// 这些字段在启动时可由环境变量覆盖配置文件；运行时"应用配置"时也应继续遵守该优先级，
// 并且不应把环境变量值反写回配置文件。
type BootstrapEnvAuthority struct {
	AppSecret       bool
	AuthEnabled     bool
	AuthUsername    bool
	AuthPassword    bool
	AuthTokenSecret bool
}

func DetectBootstrapEnvAuthority() BootstrapEnvAuthority {
	_, appSecret := os.LookupEnv("APP_SECRET")
	_, authEnabled := os.LookupEnv("AUTH_ENABLED")
	_, authUsername := os.LookupEnv("ADMIN_USERNAME")
	_, authPassword := os.LookupEnv("ADMIN_PASSWORD")
	_, authTokenSecret := os.LookupEnv("AUTH_TOKEN_SECRET")
	return BootstrapEnvAuthority{
		AppSecret:       appSecret,
		AuthEnabled:     authEnabled,
		AuthUsername:    authUsername,
		AuthPassword:    authPassword,
		AuthTokenSecret: authTokenSecret,
	}
}

// LoadRuntimeFile 读取配置文件，并仅为启动期敏感字段叠加环境变量覆盖。
//
// 运行中的"应用配置"应以文件内容为主，但 APP_SECRET / AUTH_* 这类启动期入口
// 仍然必须遵守环境变量优先级，避免出现 UI 展示/热应用与实际启动配置不一致。
func LoadRuntimeFile(path string) (*Config, error) {
	cfg, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	if err := DetectBootstrapEnvAuthority().ApplyTo(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// PrepareForInitialSave 生成首次落盘的配置副本。
//
// 当服务通过环境变量启动且 config.yaml 尚不存在时，只写入可安全持久化的值；
// 启动期凭据/secret 继续由环境变量持有，避免把 bootstrap secret 反写到磁盘。
func PrepareForInitialSave(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	DetectBootstrapEnvAuthority().ScrubForInitialSave(&clone)
	return &clone
}

func (a BootstrapEnvAuthority) ApplyTo(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	if a.AppSecret {
		cfg.Security.AppSecret = os.Getenv("APP_SECRET")
	}
	if a.AuthEnabled {
		value, err := lookupEnvBool("AUTH_ENABLED")
		if err != nil {
			return err
		}
		cfg.Auth.Enabled = value
	}
	if a.AuthUsername {
		cfg.Auth.Username = os.Getenv("ADMIN_USERNAME")
	}
	if a.AuthPassword {
		cfg.Auth.Password = os.Getenv("ADMIN_PASSWORD")
	}
	if a.AuthTokenSecret {
		cfg.Auth.TokenSecret = os.Getenv("AUTH_TOKEN_SECRET")
	}
	return nil
}

func (a BootstrapEnvAuthority) ScrubForInitialSave(cfg *Config) {
	if cfg == nil {
		return
	}
	if a.AppSecret {
		cfg.Security.AppSecret = ""
	}
	if a.AuthEnabled {
		cfg.Auth.Enabled = false
	}
	if a.AuthUsername {
		cfg.Auth.Username = DefaultAuthUsername
	}
	if a.AuthPassword {
		cfg.Auth.Password = ""
	}
	if a.AuthTokenSecret {
		cfg.Auth.TokenSecret = ""
	}
}

func lookupEnvBool(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}
	return value, nil
}

func load(path string, withEnv bool) (*Config, string, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		for _, p := range configSearchPaths() {
			v.AddConfigPath(p)
		}
		v.AddConfigPath("/etc/upstream-ops")
	}

	setDefaults(v)

	if withEnv {
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
		v.AutomaticEnv()
		// APP_SECRET / ADMIN_USERNAME / ADMIN_PASSWORD / AUTH_ENABLED 是独立约定的环境变量名，不带前缀。
		_ = v.BindEnv("security.appSecret", "APP_SECRET")
		_ = v.BindEnv("auth.enabled", "AUTH_ENABLED")
		_ = v.BindEnv("auth.username", "ADMIN_USERNAME")
		_ = v.BindEnv("auth.password", "ADMIN_PASSWORD")
		_ = v.BindEnv("auth.tokenSecret", "AUTH_TOKEN_SECRET")
		_ = v.BindEnv("auth.sub2apiEmbed.enabled", "AUTH_SUB2API_EMBED_ENABLED", "AUTH_SUB2APIEMBED_ENABLED")
		_ = v.BindEnv("auth.sub2apiEmbed.baseURL", "AUTH_SUB2API_EMBED_BASEURL", "AUTH_SUB2APIEMBED_BASEURL")
		_ = v.BindEnv("auth.sub2apiEmbed.requireAdmin", "AUTH_SUB2API_EMBED_REQUIRE_ADMIN", "AUTH_SUB2APIEMBED_REQUIREADMIN")
		// Viper 坑：AutomaticEnv 只对已通过 SetDefault / BindEnv / 配置文件注册过的 key 生效；
		// 数据库的 user/password 没有合理的默认值（拒绝写"change-me"作默认），
		// 因此显式 BindEnv 以确保从环境变量读取。
		_ = v.BindEnv("database.driver", "DATABASE_DRIVER")
		_ = v.BindEnv("database.path", "DATABASE_PATH")
		_ = v.BindEnv("database.host", "DATABASE_HOST")
		_ = v.BindEnv("database.port", "DATABASE_PORT")
		_ = v.BindEnv("database.user", "DATABASE_USER")
		_ = v.BindEnv("database.password", "DATABASE_PASSWORD")
		_ = v.BindEnv("database.name", "DATABASE_NAME")
		_ = v.BindEnv("server.port", "SERVER_PORT")
		_ = v.BindEnv("server.mode", "SERVER_MODE")
		_ = v.BindEnv("scheduler.shopCron", "SCHEDULER_SHOPCRON")
		_ = v.BindEnv("log.level", "LOG_LEVEL")
	}

	if err := v.ReadInConfig(); err != nil {
		if path != "" {
			if !os.IsNotExist(err) {
				return nil, "", fmt.Errorf("read config: %w", err)
			}
		} else {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return nil, "", fmt.Errorf("read config: %w", err)
			}
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, "", fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.Upstream = cfg.Upstream.WithDefaults()
	return cfg, v.ConfigFileUsed(), nil
}

func Save(path string, cfg *Config) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func ResolvePath(requested, used string) string {
	if requested != "" {
		return requested
	}
	if used != "" {
		return used
	}
	for _, candidate := range configSearchPaths() {
		candidate = filepath.Join(candidate, "config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if wd, err := os.Getwd(); err == nil && filepath.Base(wd) == "backend" {
		return "../config.yaml"
	}
	return "config.yaml"
}

func configSearchPaths() []string {
	if wd, err := os.Getwd(); err == nil && filepath.Base(wd) == "backend" {
		return []string{"..", "."}
	}
	return []string{"."}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.title", "UpstreamOps")
	v.SetDefault("app.notificationPrefix", "[AI 聚合监控] ")

	v.SetDefault("server.port", 8418)
	v.SetDefault("server.mode", "debug")
	v.SetDefault("server.baseURL", "http://localhost:8418")

	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.path", "./data/upstream-ops.db")
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 3306)
	v.SetDefault("database.name", "upstreamops")
	v.SetDefault("database.maxOpenConns", 20)
	v.SetDefault("database.maxIdleConns", 5)

	// CLAUDE.md 默认建议：余额 15 分钟，倍率 30 分钟。
	v.SetDefault("scheduler.balanceCron", "37 */15 * * * *")
	v.SetDefault("scheduler.rateCron", "13 */30 * * * *")
	v.SetDefault("scheduler.shopCron", "41 */10 * * * *")
	v.SetDefault("scheduler.concurrency", 4)
	v.SetDefault("scheduler.autoGroup.enabled", false)
	v.SetDefault("scheduler.autoGroup.cron", "29 */5 * * * *")
	v.SetDefault("scheduler.autoGroup.concurrency", 2)
	v.SetDefault("scheduler.autoGroup.probeConcurrency", 1)

	// 历史清理：每天凌晨 3:17 跑一次（6 字段 cron 含秒），
	// monitor 30 天 / balance 90 天 / notify 90 天。rate_change_logs 不清理（业务核心数据）。
	v.SetDefault("scheduler.retention.cron", "0 17 3 * * *")
	v.SetDefault("scheduler.retention.monitorLogsDays", 30)
	v.SetDefault("scheduler.retention.balanceSnapshotsDays", 90)
	v.SetDefault("scheduler.retention.notificationLogsDays", 90)
	v.SetDefault("scheduler.retention.announcementsDays", 90)

	v.SetDefault("auth.enabled", true)
	v.SetDefault("auth.username", DefaultAuthUsername)
	v.SetDefault("auth.sessionTTLHours", 168) // 7 天
	v.SetDefault("auth.sub2apiEmbed.enabled", false)
	v.SetDefault("auth.sub2apiEmbed.baseURL", "")
	v.SetDefault("auth.sub2apiEmbed.allowedOrigins", []string{})
	v.SetDefault("auth.sub2apiEmbed.requireAdmin", true)

	// 通知去抖：默认开合并、不过滤涨跌幅、balance_low 1h 内不重复、失败重试 3 次。
	// 即"默认行为是合并刷屏 + 不重复 balance_low + 抗短时网络抖动"，不丢任何 rate_changed 事件。
	v.SetDefault("notifications.batchRateChanges", true)
	v.SetDefault("notifications.minChangePct", 0)
	v.SetDefault("notifications.balanceLowCooldownMinutes", 60)
	v.SetDefault("notifications.subscriptionDailyRemainingThresholdPct", 0)
	v.SetDefault("notifications.subscriptionWeeklyRemainingThresholdPct", 0)
	v.SetDefault("notifications.subscriptionMonthlyRemainingThresholdPct", 0)
	v.SetDefault("notifications.subscriptionExpiryThresholdHours", 0)
	v.SetDefault("notifications.subscriptionAlertCooldownMinutes", 1440)
	v.SetDefault("notifications.sendMaxAttempts", 3)

	v.SetDefault("proxy.protocol", "http")
	v.SetDefault("proxy.port", 0)
	v.SetDefault("proxy.enabled", false)
	v.SetDefault("proxy.versionCheckEnabled", false)

	v.SetDefault("upstream.timeoutSeconds", DefaultUpstreamTimeoutSeconds)
	v.SetDefault("upstream.userAgent", DefaultUpstreamUserAgent)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "text")
}
