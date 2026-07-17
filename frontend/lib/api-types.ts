/**
 * API response shapes for UpstreamOps backend.
 * Keep in sync with backend/storage/*.go and backend/api/*.go.
 */

export type ChannelType = "newapi" | "sub2api"
export type ShopPlatform = "ldxp"
export type ShopScopeMode = "all" | "filters" | "goods_keys"

export type CredentialMode = "password" | "token"

export type RechargeMultiplierMode = "divide" | "multiply"

export type NotificationChannelType =
  | "telegram"
  | "webhook"
  | "email"
  | "wecom"
  | "dingtalk"
  | "feishu"
  | "serverchan3"

export type CaptchaProviderType =
  | "capsolver"
  | "2captcha"
  | "anticaptcha"
  | "yescaptcha"

export type MonitorJob = "login" | "balance" | "rates"

export type NotificationEvent =
  | "balance_low"
  | "rate_changed"
  | "rate_structure_changed"
  | "rate_added"
  | "rate_removed"
  | "announcement"
  | "login_failed"
  | "captcha_failed"
  | "monitor_failed"
  | "subscription_daily_remaining_low"
  | "subscription_weekly_remaining_low"
  | "subscription_monthly_remaining_low"
  | "subscription_expiring"
  | "shop_goods_added"
  | "shop_goods_removed"
  | "shop_price_changed"
  | "shop_stock_changed"
  | "shop_stock_low"
  | "shop_goods_restocked"
  | "shop_monitor_failed"
  | "auto_group_switched"
  | "auto_group_unavailable"
  | "auto_group_failed"
  | "auto_group_circuit_opened"
  | "auto_group_all_unavailable"
  | "auto_group_recovered"
  | "auto_group_target_update_failed"
  | "auto_group_probe_failed"
  | "auto_group_policy_error"

export type ShopGoodsChangeEvent =
  | "goods_added"
  | "goods_removed"
  | "price_changed"
  | "stock_changed"
  | "stock_low"
  | "goods_restocked"
  | "monitor_failed"

export interface Channel {
  id: number
  name: string
  type: ChannelType
  site_url: string
  username: string
  sort_order: number
  user_id?: string
  credential_mode: CredentialMode
  login_extra_params: string
  turnstile_enabled: boolean
  ignore_announcements: boolean
  subscription_enabled: boolean
  proxy_enabled: boolean
  captcha_config_id?: number | null
  balance_threshold: number
  recharge_multiplier?: number | null
  recharge_multiplier_mode: RechargeMultiplierMode
  monitor_enabled: boolean
  last_balance?: number | null
  last_balance_at?: string | null
  today_cost?: number | null
  total_cost?: number | null
  last_error?: string
  created_at: string
  updated_at: string
}

export interface ChannelPage {
  items: Channel[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface ShopTarget {
  id: number
  name: string
  platform: ShopPlatform
  site_url: string
  base_url: string
  token: string
  monitor_enabled: boolean
  notify_enabled: boolean
  scope_mode: ShopScopeMode
  goods_types_json: string
  category_ids_json: string
  category_names_json: string
  keywords_json: string
  goods_keys_json: string
  stock_threshold: number
  price_change_enabled: boolean
  stock_change_enabled: boolean
  low_stock_enabled: boolean
  restock_enabled: boolean
  new_goods_enabled: boolean
  removed_goods_enabled: boolean
  proxy_enabled: boolean
  sort_order: number
  goods_sort: ShopGoodsSort
  last_sync_at?: string | null
  last_error?: string
  last_shop_name?: string
  last_goods_count: number
  last_low_stock_goods: number
  last_changed_count: number
  watch_rule_count: number
  created_at: string
  updated_at: string
}

export interface ShopGoodsTargetOption {
  id: number
  name: string
  last_shop_name?: string
  site_url: string
}

export interface ShopWatchRule {
  id: number
  target_id: number
  name: string
  enabled: boolean
  goods_keys_json: string
  category_ids_json: string
  category_names_json: string
  keywords_json: string
  events_json: string
  stock_threshold: number
  created_at: string
  updated_at: string
}

export interface ShopWatchRuleInput {
  name: string
  enabled: boolean
  goods_keys: string[]
  category_ids: number[]
  category_names: string[]
  keywords: string[]
  events: ShopGoodsChangeEvent[]
  stock_threshold: number
}

export interface ShopBulkNotificationInput {
  target_ids: number[]
  notify_enabled?: boolean
  upsert_rule: boolean
  replace_same_name: boolean
  rule: ShopWatchRuleInput
}

export interface ShopBulkNotificationResult {
  updated_targets: number
  created_rules: number
  updated_rules: number
  targets: ShopTarget[]
}

export interface ShopWatchRulePreview {
  total: number
  items: ShopGoodsSnapshot[]
}

export interface ShopGoodsSnapshot {
  id: number
  target_id: number
  goods_key: string
  goods_type: string
  name: string
  category_id: number
  category_name: string
  link: string
  price: number
  market_price: number
  stock_count: number
  limit_count: number
  send_order: number
  contact_format: string
  raw_json?: string
  first_seen_at: string
  last_seen_at: string
  last_changed_at?: string | null
  removed_at?: string | null
  created_at: string
  updated_at: string
}

export interface ShopGoodsWithTarget extends ShopGoodsSnapshot {
  target_name: string
  target_last_shop_name: string
  target_site_url: string
  target_monitor_enabled: boolean
  target_notify_enabled: boolean
  target_stock_threshold: number
}

/** Fields shared by the authenticated and anonymous goods overview. */
export interface ShopGoodsListItem {
  id: number
  target_id: number
  goods_key: string
  name: string
  category_name: string
  link: string
  price: number
  stock_count: number
  last_seen_at: string
  removed_at?: string | null
  target_name: string
  target_last_shop_name: string
  target_site_url: string
  target_stock_threshold: number
}

export type ShopGoodsStatus = "all" | "active" | "in_stock" | "removed" | "low_stock" | "out_of_stock"
export type ShopGoodsSort = "category" | "stock_asc" | "stock_desc" | "price_asc" | "price_desc" | "last_seen_desc"

export interface ShopSnapshotCategory {
  category_id: number
  category_name: string
  goods_count: number
  active_count: number
  removed_count: number
  low_stock_count: number
  out_of_stock_count: number
}

export interface ShopRefreshGoodsResult {
  snapshot: ShopGoodsSnapshot
  found: boolean
  changed: boolean
}

export interface ShopGoodsChangeLog {
  id: number
  target_id: number
  goods_key: string
  goods_name: string
  event: ShopGoodsChangeEvent
  old_value?: string
  new_value?: string
  summary: string
  changed_at: string
  created_at: string
}

export interface ShopMonitorLog {
  id: number
  target_id: number
  success: boolean
  error_message?: string
  goods_count: number
  changed_count: number
  started_at: string
  finished_at: string
  duration_ms: number
  created_at: string
}

export interface ShopCategory {
  id: number
  name: string
  image?: string
  goods_count: number
}

export interface ShopInfo {
  name: string
  link: string
  avatar?: string
  goods_count: number
  raw_json?: string
}

export interface ShopTestResult {
  info: ShopInfo
  categories: ShopCategory[]
}

export interface ShopSyncResult {
  goods_count: number
  changed_count: number
  events: Record<string, number>
}

export interface ShopSyncAllTargetResult {
  target_id: number
  name: string
  job?: ShopSyncJob
  reused?: boolean
  error?: string
}

export interface ShopSyncAllResult {
  total: number
  queued: number
  reused: number
  failed: number
  targets: ShopSyncAllTargetResult[]
}

export type ShopSyncJobStatus = "queued" | "running" | "succeeded" | "failed" | "timed_out" | "skipped"

export interface ShopSyncJob {
	id: number
	target_id: number
	status: ShopSyncJobStatus
	error_message?: string
	goods_count: number
	changed_count: number
	events_json?: string
	started_at?: string | null
	finished_at?: string | null
	duration_ms: number
	created_at: string
	updated_at: string
}

export interface ShopSyncJobStartResult {
	job: ShopSyncJob
	reused: boolean
}

export interface CaptchaConfig {
  id: number
  name: string
  type: CaptchaProviderType
  endpoint?: string
  extra?: string
  enabled: boolean
  proxy_enabled: boolean
  last_balance?: number | null
  balance_unit?: string
  balance_at?: string | null
  balance_error?: string
  created_at: string
  updated_at: string
}

export interface RateSnapshot {
  id: number
  channel_id: number
  model_name: string
  description?: string
  ratio: number
  completion_ratio: number
  first_seen_at: string
  last_seen_at: string
}

export interface RateChangeLog {
  id: number
  channel_id: number
  model_name: string
  change_type?: "changed" | "added" | "removed" | string
  old_ratio: number | null
  new_ratio: number
  old_completion_ratio?: number | null
  new_completion_ratio?: number
  changed_at: string
}

export interface RateChangeLogPage {
  items: RateChangeLog[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface BalanceSnapshot {
  id: number
  channel_id: number
  balance: number
  sampled_at: string
}

export interface NotificationSubscription {
  channel_ids: number[]
  mode: "all" | "groups"
  groups?: string[]
  events?: NotificationEvent[]
}

export interface NotificationChannel {
  id: number
  name: string
  type: NotificationChannelType
  enabled: boolean
  proxy_enabled: boolean
  subscriptions?: string
  created_at: string
  updated_at: string
}

export interface NotificationLog {
  id: number
  channel_id: number
  upstream_channel_id?: number
  channel_name?: string
  channel_type?: string
  event: NotificationEvent
  subject: string
  body: string
  success: boolean
  error_message?: string
  sent_at: string
}

export interface UpstreamAnnouncement {
  id: number
  channel_id: number
  source_key: string
  title?: string
  content: string
  type?: string
  link?: string
  published_at?: string | null
  source_updated_at?: string | null
  first_seen_at: string
}

export interface MonitorLog {
  id: number
  channel_id: number
  job: MonitorJob
  success: boolean
  error_message?: string
  duration_ms: number
  started_at: string
  finished_at: string
}

export interface DashboardLowest {
  channel_id: number
  name: string
  balance: number | null
}

export interface DashboardChannelStat {
  id: number
  name: string
  type: string
  monitor_enabled: boolean
  last_balance?: number | null
  today_cost?: number | null
  total_cost?: number | null
  last_error?: string
}

export interface DashboardSummary {
  total_channels: number
  active_channels: number
  failed_channels: number
  total_balance: number
  today_total_cost: number
  total_cost: number
  lowest_balance: DashboardLowest | null
  channels: DashboardChannelStat[]
  recent_rate_changes: RateChangeLog[]
}

export interface BalanceTrendPoint {
  day: string
  balance: number
}

export interface CostTrendPoint {
  day: string
  cost: number
}

export interface SystemAuthConfig {
  enabled: boolean
  username: string
  password: string
  tokenSecret: string
  sessionTTLHours: number
  sub2apiEmbed: Sub2APIEmbedConfig
}

export interface Sub2APIEmbedConfig {
  enabled: boolean
  baseURL: string
  allowedOrigins: string[]
  requireAdmin: boolean
}

export interface AppConfig {
  title: string
  notificationPrefix: string
}

export interface SystemSchedulerRetentionConfig {
  cron: string
  monitorLogsDays: number
  balanceSnapshotsDays: number
  notificationLogsDays: number
  announcementsDays: number
  shopHighFrequencyChangeLogsDays: number
  shopOtherChangeLogsDays: number
  shopMonitorLogsDays: number
  shopSyncJobsDays: number
}

export interface ShopRetentionResult {
  high_frequency_changes_deleted: number
  other_changes_deleted: number
  monitor_logs_deleted: number
  sync_jobs_deleted: number
  total_deleted: number
  errors?: Record<string, string>
}

export interface SystemSchedulerAutoGroupConfig {
  enabled: boolean
  cron: string
  concurrency: number
  probeConcurrency: number
}

export interface SystemSchedulerConfig {
  balanceCron: string
  rateCron: string
  shopCron: string
  concurrency: number
  autoGroup: SystemSchedulerAutoGroupConfig
  retention: SystemSchedulerRetentionConfig
}

export interface SystemNotificationsConfig {
  batchRateChanges: boolean
  minChangePct: number
  balanceLowCooldownMinutes: number
  subscriptionDailyRemainingThresholdPct: number
  subscriptionWeeklyRemainingThresholdPct: number
  subscriptionMonthlyRemainingThresholdPct: number
  subscriptionExpiryThresholdHours: number
  subscriptionAlertCooldownMinutes: number
  sendMaxAttempts: number
}

export interface SystemProxyConfig {
  enabled: boolean
  versionCheckEnabled: boolean
  protocol: "http" | "https" | "socks5"
  host: string
  port: number
  username: string
  password: string
}

export interface SystemUpstreamConfig {
  timeoutSeconds: number
  userAgent: string
  shopRequestIntervalMilliseconds: number
  shopInfoTTLHours: number
}

export interface SystemConfig {
  app: AppConfig
  auth: SystemAuthConfig
  scheduler: SystemSchedulerConfig
  notifications: SystemNotificationsConfig
  proxy: SystemProxyConfig
  upstream: SystemUpstreamConfig
}

export interface SystemConfigResponse {
  config_path: string
  config: SystemConfig
}

export interface AppVersion {
  name: string
  title: string
  version: string
  latest_version?: string
  update_available?: boolean
  repo_url?: string
  release_url?: string
  update_error?: string
}

export interface ApplyConfigResult {
  applied_sections: string[]
  message: string
}

export interface ChannelRedeemResult {
  message: string
  type: string
  value: number
  new_balance?: number
  new_concurrency?: number
  group_name?: string
  validity_days?: number
}

export type RechargePaymentMethod = "alipay" | "wxpay"
export type SubscriptionPaymentMethod =
  | "balance"
  | "alipay"
  | "wxpay"
  | "stripe"
  | "creem"
  | "waffo_pancake"
  | string

export interface ChannelRechargeMethod {
  type: RechargePaymentMethod
  name: string
  min_amount: number
  max_amount: number
}

export interface ChannelRechargeInfo {
  amount_label: string
  amount_step: number
  min_amount: number
  max_amount: number
  preset_amounts: number[]
  help_text?: string
  help_image_url?: string
  alipay_force_qrcode: boolean
  methods: ChannelRechargeMethod[]
}

export interface ChannelRechargeLaunch {
  mode: "qrcode" | "redirect" | "form" | "success"
  qr_code?: string
  pay_url?: string
  form_action?: string
  form_fields?: Record<string, string>
  expires_at?: string
}

export interface ChannelSubscriptionMethod {
  type: SubscriptionPaymentMethod
  name: string
}

export interface ChannelSubscriptionPlan {
  id: string
  name: string
  description?: string
  price: number
  currency?: string
  validity?: string
  group_name?: string
  quota?: number
  daily_limit_usd?: number | null
  weekly_limit_usd?: number | null
  monthly_limit_usd?: number | null
  features?: string[]
  payment_methods?: string[]
}

export interface ChannelSubscriptionInfo {
  plans: ChannelSubscriptionPlan[]
  methods: ChannelSubscriptionMethod[]
}

export type ChannelSubscriptionLaunch = ChannelRechargeLaunch

export interface ChannelSubscriptionUsageWindow {
  limit_usd: number
  used_usd: number
  remaining_usd: number
  remaining_percent: number
  used_percent: number
  window_start?: string | null
  resets_at?: string | null
  resets_in_seconds: number
}

export interface ChannelSubscriptionUsage {
  id: number
  group_id: number
  group_name: string
  status: string
  starts_at?: string | null
  expires_at?: string | null
  expires_in_days: number
  daily?: ChannelSubscriptionUsageWindow | null
  weekly?: ChannelSubscriptionUsageWindow | null
  monthly?: ChannelSubscriptionUsageWindow | null
}

export interface ChannelSubscriptionUsageInfo {
  items: ChannelSubscriptionUsage[]
}

export type ChannelAPIKeyStatus = "active" | "disabled" | "expired" | "quota_exhausted" | "unknown"

export interface ChannelAPIKey {
  id: number
  key: string
  name: string
  status: ChannelAPIKeyStatus | string
  group?: string
  group_name?: string
  group_description?: string
  group_ratio: number
  group_id?: number | null
  remain_amount?: number
  used_amount?: number
  quota: number
  quota_used: number
  unlimited_quota: boolean
  expired_time: number
  expires_at?: string | null
  created_at?: string | null
  updated_at?: string | null
  last_used_at?: string | null
  allow_ips?: string
  ip_whitelist?: string[]
  ip_blacklist?: string[]
  model_limits_enabled: boolean
  model_limits?: string
  cross_group_retry: boolean
  rate_limit_5h: number
  rate_limit_1d: number
  rate_limit_7d: number
  usage_5h: number
  usage_1d: number
  usage_7d: number
}

export interface ChannelAPIKeyPage {
  items: ChannelAPIKey[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface NotificationLogPage {
  items: NotificationLog[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface UpstreamAnnouncementPage {
  items: UpstreamAnnouncement[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface ChannelAPIKeyGroup {
  id?: number | null
  name: string
  description?: string
  ratio: number
}

export interface ChannelAPIKeyReveal {
  key: string
}

export interface AutoGroupPolicy {
  id: number
  channel_id: number
  name: string
  enabled: boolean
  sort_order: number
  notify_enabled: boolean
  target_key_id: number
  target_key_name: string
  probe_key_id: number
  probe_key_name: string
  probe_model: string
  probe_timeout_seconds: number
  probe_success_cache_minutes: number
  probe_failure_retry_minutes: number
  probe_max_per_run: number
  include_groups_json: string
  exclude_groups_json: string
  include_keywords_json: string
  exclude_keywords_json: string
  min_ratio: number
  max_ratio: number
  failure_threshold: number
  circuit_duration_minutes: number
  half_open_success_threshold: number
  min_ratio_improvement_pct: number
  switch_cooldown_minutes: number
  force_switch_on_current_unhealthy: boolean
  keep_current_when_no_available: boolean
  current_group_name?: string
  current_group_id?: number | null
  current_ratio: number
  last_status: AutoGroupStatus
  last_error?: string
  last_evaluate_at?: string | null
  last_switch_at?: string | null
  created_at: string
  updated_at: string
}

export type AutoGroupStatus =
  | "idle"
  | "ok"
  | "switched"
  | "unavailable"
  | "failed"
  | "disabled"
  | "kept"
  | "cooldown"
  | "probe_failed"

export type AutoGroupCandidateStatus =
  | "healthy"
  | "excluded"
  | "circuit_open"
  | "half_open"
  | "failed"
  | "unknown"

export type AutoGroupProbeErrorCode =
  | "0"
  | "1001"
  | "1002"
  | "1003"
  | "2001"
  | "2002"
  | "2003"
  | "2004"
  | "2101"
  | "3001"
  | "3002"
  | "3003"
  | "3004"
  | "3005"
  | "4001"
  | "4002"
  | "4003"
  | `${number}`

export interface AutoGroupCandidate {
  id: number
  policy_id: number
  group_name: string
  group_id?: number | null
  description?: string
  ratio: number
  status: AutoGroupCandidateStatus | string
  reason?: string
  failure_count: number
  success_count: number
  circuit_open_until?: string | null
  circuit_opened_at?: string | null
  recovered_at?: string | null
  last_probe_at?: string | null
  last_probe_success?: boolean | null
  last_probe_latency_ms: number
  last_error_code?: AutoGroupProbeErrorCode
  last_checked_at?: string | null
  last_error?: string
  manual_disabled: boolean
  created_at: string
  updated_at: string
}

export interface AutoGroupCandidateDecision {
  group_name: string
  group_id?: number | null
  description?: string
  ratio: number
  status: AutoGroupCandidateStatus | string
  reason?: string
  failure_count: number
  success_count: number
  circuit_open_until?: string | null
  circuit_opened_at?: string | null
  recovered_at?: string | null
  last_probe_at?: string | null
  last_probe_success?: boolean | null
  last_probe_latency_ms: number
  last_error_code?: AutoGroupProbeErrorCode
  last_error?: string
  manual_disabled: boolean
}

export interface AutoGroupPolicyView extends AutoGroupPolicy {
  channel?: Channel
  candidates?: AutoGroupCandidate[]
  latest_log?: AutoGroupEvaluationLog | null
}

export interface AutoGroupSummary {
  total_policies: number
  running_policies: number
  abnormal_policies: number
  circuit_groups: number
  today_switches: number
  no_available_policies: number
  manual_disabled_groups: number
}

export interface AutoGroupCapabilityItem {
  key: string
  label: string
  supported: boolean
  message?: string
}

export interface AutoGroupCapabilityMatrix {
  channel_id: number
  channel_type: ChannelType | string
  level: "full" | "suggest" | "observe" | "error" | string
  message?: string
  capabilities: AutoGroupCapabilityItem[]
}

export interface ProbeModelOption {
  id: string
  name?: string
  owned_by?: string
  source?: string
}

export interface AutoGroupProbeModelOptions {
  default_model: string
  items: ProbeModelOption[]
  warning?: string
}

export interface AutoGroupPolicyInput {
  channel_id: number
  name: string
  enabled: boolean
  notify_enabled: boolean
  target_key_id: number
  target_key_name: string
  probe_key_id: number
  probe_key_name: string
  probe_model: string
  probe_timeout_seconds: number
  probe_success_cache_minutes: number
  probe_failure_retry_minutes: number
  probe_max_per_run: number
  include_groups: string[]
  exclude_groups: string[]
  include_keywords: string[]
  exclude_keywords: string[]
  min_ratio: number
  max_ratio: number
  failure_threshold: number
  circuit_duration_minutes: number
  half_open_success_threshold: number
  min_ratio_improvement_pct: number
  switch_cooldown_minutes: number
  force_switch_on_current_unhealthy: boolean
  keep_current_when_no_available: boolean
}

export interface AutoGroupEvaluationLog {
  id: number
  policy_id: number
  channel_id: number
  success: boolean
  status: string
  target_key_id: number
  target_key_name?: string
  current_group?: string
  selected_group?: string
  selected_ratio: number
  candidate_count: number
  available_count: number
  circuit_open_count: number
  action?: string
  message?: string
  created_at: string
}

export interface AutoGroupSwitchLog {
  id: number
  policy_id: number
  channel_id: number
  target_key_id: number
  target_key_name?: string
  from_group?: string
  to_group?: string
  to_group_id?: number | null
  to_ratio: number
  success: boolean
  reason?: string
  error_message?: string
  created_at: string
}

export interface AutoGroupEvaluationResult {
  policy: AutoGroupPolicy
  channel: Channel
  target_key?: ChannelAPIKey
  selected?: AutoGroupCandidateDecision
  candidates: AutoGroupCandidateDecision[]
  evaluation_log: AutoGroupEvaluationLog
  switch_log?: AutoGroupSwitchLog
}

export type AutoGroupEvaluationLogPage = PageResult<AutoGroupEvaluationLog>
export type AutoGroupSwitchLogPage = PageResult<AutoGroupSwitchLog>
