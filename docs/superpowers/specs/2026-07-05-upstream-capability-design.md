# 上游能力模块封装设计

Date: 2026-07-05

## 摘要

将 UpstreamOps 现有的 Sub2API / NewAPI 基础能力抽象为独立的“上游能力模块”，以渠道类型作为适配入口，以能力接口作为复用边界。

该模块不替代现有 `connector`，而是在 `channel.Service` 和业务模块之间增加一层稳定的能力门面。智能分组、渠道监控、API Key 管理、订阅购买、后续自动化功能都通过这层能力门面调用上游，不再各自拼接“渠道解密、登录态、connector 调用、能力判断、错误归一化”逻辑。

## 背景

当前项目已经支持 `sub2api` 和 `newapi` 两类渠道，并具备以下能力：

- 登录和登录态复用。
- 余额、消费、倍率、公告同步。
- API Key 列表、创建、更新、删除、明文读取。
- 分组列表和分组倍率。
- Sub2API 的充值、订阅购买和订阅用量。
- 智能分组对 API Key、分组和数据面探测的组合调用。

现有实现的问题不是“没有能力”，而是能力边界不够清晰：

- `connector.Connector` 是一个大接口，所有渠道都必须实现完整方法，即使部分渠道天然不支持。
- 业务模块要知道哪些能力在哪个 connector 上可靠可用。
- 智能分组、渠道管理、购买、后续功能容易重复处理登录、代理、会话刷新、能力降级、错误分类。
- NewAPI 不同版本或魔改版能力差异较大，硬编码在业务逻辑中会越来越难维护。

## 设计目标

- 按渠道类型保留 Sub2API / NewAPI 适配，不抹平上游差异。
- 把基础能力封装为可组合接口，业务模块只依赖自己需要的能力。
- 复用现有渠道配置，不重复录入上游地址、账号、密码、Token、代理。
- 统一会话准备、代理配置、HTTP 超时、错误归一化和能力检测。
- 支持能力矩阵，让 UI 和业务逻辑知道当前渠道是“完整控制”“仅观测”还是“不支持”。
- 为后续新增 OneAPI、VoAPI、自建聚合网关等渠道留扩展点。

## 非目标

- 不重写 Sub2API / NewAPI connector 的所有实现。
- 不改变现有渠道表和认证数据模型。
- 不强制一次性删除 `channel.Service` 内部 connector 调用；它仍负责渠道解析、会话和认证。
- 不把不同渠道强行伪装成完全一致，能力缺失必须显式表达。
- 不引入新的外部依赖。

## 推荐方案

采用“能力门面 + 能力接口 + 渠道类型适配器”的方案。

```text
业务模块
  -> upstreamcap.Service
    -> channel.Service 准备渠道、会话、代理、HTTP 配置
      -> connector.For(channel.Type)
        -> sub2api.Client / newapi.Client
```

模块建议：

```text
backend/upstreamcap
  service.go
  capabilities.go
  errors.go
  probe.go
  matrix.go
```

命名说明：

- `connector` 继续表示“具体上游 HTTP API 客户端”。
- `channel` 继续表示“本地渠道配置和会话管理”。
- `upstreamcap` 表示“业务可复用的上游能力门面”。

## 能力分组

### 基础观测能力

用于渠道监控、首页状态和基础同步。

```go
type ObserveCapability interface {
    GetBalance(ctx context.Context, channelID uint) (*connector.BalanceResult, error)
    GetCosts(ctx context.Context, channelID uint) (*connector.CostResult, error)
    GetRates(ctx context.Context, channelID uint) ([]connector.RateResult, error)
    GetAnnouncements(ctx context.Context, channelID uint) ([]connector.AnnouncementResult, error)
}
```

### API Key 能力

用于 API Key 管理、智能分组和后续批量治理。

```go
type APIKeyCapability interface {
    ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error)
    CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error)
    UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error)
    DeleteAPIKey(ctx context.Context, channelID uint, keyID int64) error
    RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error)
}
```

### 分组能力

用于智能分组、倍率展示、分组健康检查。

```go
type GroupCapability interface {
    ListAPIKeyGroups(ctx context.Context, channelID uint) ([]connector.APIKeyGroup, error)
}
```

### 探测能力

用于智能分组和后续上游健康检查。

```go
type ProbeCapability interface {
    ProbeOpenAICompatible(ctx context.Context, channelID uint, apiKey string, req ProbeRequest) (*ProbeResult, error)
}
```

`ProbeRequest` 建议包含：

```text
model
timeout
max_tokens
endpoint_type
group_name
```

第一阶段可以先支持 OpenAI Chat Completions 兼容探测；后续按模型族扩展 Anthropic、Gemini。

### 购买能力

Sub2API 支持更完整；NewAPI 当前支持充值能力，但订阅购买和订阅用量应显式返回能力不支持。

```go
type RechargeCapability interface {
    GetRechargeInfo(ctx context.Context, channelID uint) (*connector.RechargeInfo, error)
    CreateRecharge(ctx context.Context, channelID uint, req connector.RechargeRequest) (*connector.RechargeLaunch, error)
}

type SubscriptionCapability interface {
    GetSubscriptionInfo(ctx context.Context, channelID uint) (*connector.SubscriptionInfo, error)
    CreateSubscription(ctx context.Context, channelID uint, req connector.SubscriptionRequest) (*connector.SubscriptionLaunch, error)
    GetSubscriptionUsage(ctx context.Context, channelID uint) (*connector.SubscriptionUsageInfo, error)
}
```

## 能力矩阵

每个渠道应能返回当前能力矩阵。

```go
type CapabilityMatrix struct {
    ChannelID    uint
    ChannelType  storage.ChannelType
    Level        CapabilityLevel
    Capabilities []CapabilityItem
}

type CapabilityItem struct {
    Key       string
    Supported bool
    Required  bool
    Message   string
}
```

建议等级：

```text
full_control     支持完整读写和自动化
observe_only     只支持观测，不支持写操作
suggest_only     可生成建议，但不能自动执行
unsupported      关键能力不可用
degraded         临时异常，例如登录失效或接口失败
```

`level` 表示核心自动化能力的可用等级；非核心能力缺失仍通过 `CapabilityItem` 明细表达。例如 NewAPI 可以具备智能分组所需的完整核心能力，但不支持订阅购买。

智能分组需要的最低能力：

```text
api_keys
api_key_groups
api_key_update
api_key_reveal
probe_openai_compatible
```

订阅购买需要的最低能力：

```text
subscription_info
create_subscription
recharge_info 或 payment_launch
```

## 错误模型

新增统一错误类型，避免上层靠字符串判断。

```go
type CapabilityError struct {
    Code       string
    Capability string
    ChannelID  uint
    Temporary  bool
    Cause      error
}
```

建议错误码：

```text
capability_unsupported
auth_failed
session_expired
upstream_unreachable
upstream_unauthorized
upstream_forbidden
upstream_rate_limited
upstream_bad_response
upstream_version_mismatch
invalid_channel_config
not_found
```

处理原则：

- 能力缺失返回 `capability_unsupported`，不是普通 HTTP 错误。
- 登录失效、网络超时、上游 5xx 标记为 temporary。
- 401/403 需要区分渠道登录失效和 API Key 数据面鉴权失败。
- 上层通知和 UI 展示使用错误码生成稳定文案。

## 数据流

### 业务调用 API Key 能力

```text
autogroup.Service
  -> upstreamcap.Service.ListAPIKeys(channelID)
    -> prepare(channelID)
      -> channel.Service 读取渠道
      -> 解密账号/Token
      -> 获取或刷新 AuthSession
      -> connector.For(channel.Type)
      -> 应用代理和 HTTP 配置
    -> connector.ListAPIKeys(...)
    -> 归一化错误
```

### 业务调用探测能力

```text
autogroup.Service
  -> upstreamcap.Service.ProbeOpenAICompatible(channelID, apiKey, req)
    -> 读取渠道 base URL、代理、超时配置
    -> 发起低成本数据面请求
    -> 截断错误体
    -> 返回 ProbeResult
```

探测不应散落在智能分组内部长期维护，应该成为可复用能力。

## 与现有代码的关系

当前已有可复用基础：

- `backend/connector/connector.go` 已定义统一数据结构和 connector 注册表。
- `backend/channel/service.go` 已处理渠道解密、会话复用、代理和 HTTP 配置。
- `backend/autogroup/service.go` 当前直接依赖一个局部 `ChannelService` 接口。
- `backend/channel/service.go` 已经提供 API Key 管理统一入口。

第一阶段不删除现有 connector 方法。建议新增 `backend/upstreamcap`，内部复用 `channel.Service`，并逐步把业务模块迁移过去。

## 迁移策略

### 阶段 1：抽能力门面，不改行为

1. 新增 `backend/upstreamcap`。
2. 将 `channel.Service` 的 API Key、分组、余额、倍率等公开方法包装成能力门面。
3. 增加能力矩阵和统一错误类型。
4. 保持现有 API 路由和前端不变。
5. 补充 Sub2API / NewAPI 能力矩阵测试。

验收标准：

- 原有 `go test ./...` 通过。
- 智能分组不改行为。
- 能力矩阵可准确区分 Sub2API / NewAPI 支持项。

### 阶段 2：迁移智能分组

1. 将 `autogroup.Service` 的 `ChannelService` 依赖替换为能力接口组合。
2. 将数据面探测移入 `upstreamcap.ProbeCapability`。
3. 智能分组只表达策略和状态机，不再关心上游会话与具体 connector。

验收标准：

- 智能分组现有测试全部通过。
- 候选选择、探测、熔断、切换行为不回退。
- NewAPI 探测 Key 回填和额度恢复行为不回退。

### 阶段 3：迁移渠道管理和购买

1. API Key 管理页面改用 `upstreamcap.APIKeyCapability`。
2. 订阅购买改用 `SubscriptionCapability`。
3. UI 根据能力矩阵隐藏或降级不支持操作。

验收标准：

- NewAPI 不支持订阅购买时显示明确原因。
- Sub2API 购买、用量、API Key 管理行为不回退。

### 阶段 4：新增渠道类型

新增上游时只需要：

1. 实现 connector。
2. 注册渠道类型。
3. 在能力矩阵中声明支持项。
4. 补充该渠道的能力测试。

业务模块不应新增渠道类型分支。

## 高可用要求

- 同一渠道的写操作需要串行化，避免同时修改同一个上游 API Key。
- 能力门面不能吞掉关键状态错误。
- 外部不可逆操作成功后，本地后置日志失败只能告警，不能把真实成功误报为失败。
- 探测必须有超时、并发限制、缓存和单轮预算。
- 调度任务应避免重入和堆积。
- 能力矩阵检测失败应允许降级展示，不应导致整个页面不可用。

## 测试计划

### 单元测试

- Sub2API 能力矩阵。
- NewAPI 能力矩阵。
- 能力缺失错误归一化。
- 登录失效和刷新失败错误归一化。
- API Key 能力包装。
- 分组能力包装。
- 探测错误分类。

### 集成测试

- 智能分组通过能力门面完成评估。
- NewAPI 探测 Key 创建后 ID 回查不回退。
- Sub2API API Key 分组更新不回退。
- 订阅购买在 Sub2API 可用，在 NewAPI 明确不可用。

### 回归验证

```bash
go test ./...
go test -race ./backend/autogroup ./backend/channel ./backend/connector/newapi ./backend/connector/sub2api
cd frontend
pnpm exec tsc --noEmit
pnpm build
```

## 开放问题

- 能力矩阵是否每次实时检测，还是使用短缓存。
- NewAPI 不同魔改版的 API Key 分组更新差异是否需要版本指纹。
- 数据面探测是否先只支持 OpenAI Chat Completions，还是同时设计 Anthropic / Gemini。
- 是否需要把能力矩阵结果落库用于首页快速展示。
- 是否需要暴露 `/api/channels/:id/capabilities` 作为前端统一入口。

## 结论

建议实施该封装。

它不会推翻现有 connector 和 channel 代码，而是把现有能力整理成稳定复用层。这样智能分组、店铺监控以外的后续功能可以组合能力接口开发，减少重复逻辑，并让 Sub2API / NewAPI 的差异通过能力矩阵显式表达。

## 当前落地状态

已完成：

- 新增 `backend/upstreamcap` 能力门面。
- 暴露 `/api/channels/:id/capabilities` 能力矩阵接口。
- 智能分组已通过能力门面复用 API Key、分组、切组、明文 Key 和 OpenAI 兼容探测能力。
- 渠道 API Key 管理、分组读取、充值、订阅购买和订阅用量接口已通过能力门面调用。
- 普通渠道监控的余额、消费、倍率、公告同步和订阅用量告警已通过能力门面调用。

仍保留在 `channel.Service` 的内容：

- 渠道创建、编辑、删除、登录测试、兑换码等“本地渠道管理”职责。
- connector 实现和登录态管理本身。
