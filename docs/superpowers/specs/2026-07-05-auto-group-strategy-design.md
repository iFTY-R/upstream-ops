# Auto Key 智能分组策略设计

Date: 2026-07-05

## 摘要

为 UpstreamOps 增加“Auto Key 智能分组策略”能力，用于在已经录入的 NewAPI / Sub2API 上游渠道中，为指定 API Key 自动选择当前最优分组。

该功能的核心目标不是二开某一个 Sub2API 上游，而是在 UpstreamOps 中作为跨上游的运维控制层：复用现有渠道配置、登录态、倍率采集、API Key 管理和通知体系，按策略自动把目标 API Key 切换到“可用且性价比更高”的分组。当低倍率分组不可用时，系统自动熔断并降级到下一个候选分组；当没有可用分组、切换失败、分组恢复时主动通知。

## 背景

当前使用场景中，用户会维护多个 Sub2API / NewAPI 上游。每个上游都有不同分组，不同分组倍率不同，且低倍率分组常常存在不稳定、维护、不可用、满载、限流等问题。

现在的人工流程是：

1. 登录每个上游后台。
2. 查看分组标题、描述、倍率。
3. 判断哪个低倍率分组可用。
4. 手动修改某个用于对外分发的 API Key，例如 `auto`。
5. 当该分组不可用时，再手动切换到另一个分组。

该流程重复、容易漏、响应慢，而且多个上游之间难以统一观察。UpstreamOps 已经具备集中监控多个上游、同步倍率、维护 API Key 和发送通知的基础能力，因此更适合作为该功能的实现位置。

## 设计结论

优先在 UpstreamOps 中实现，不二开 Sub2API。

原因：

- UpstreamOps 已经保存了各上游的基础地址、账号密码、Token/Cookie、代理配置和登录态，不应要求用户重复录入上游信息。
- UpstreamOps 已经有 Sub2API / NewAPI connector，包含倍率读取、API Key 列表、API Key 分组更新等统一能力。
- 该需求本质是跨上游运维调度，不是单个 Sub2API 的网关内核能力。
- 二开 Sub2API 会带来官方更新冲突、功能合并滞后和长期维护成本。
- 将能力放在 UpstreamOps 中，可以同时覆盖用户自建的聚合 Sub2API 网关、多个上游 Sub2API 和 NewAPI。

## 目标

- 基于现有渠道创建策略，不重复录入上游地址、账号、密码或 Token。
- 支持为指定渠道中的指定 API Key 创建自动分组策略。
- 默认推荐目标 API Key 名称为 `auto`。
- 支持探测 API Key，默认推荐名称为 `ops-probe-auto`，避免频繁切换生产 key 做探测。
- 支持候选分组范围限制，包括白名单、黑名单、关键词、倍率区间。
- 支持按倍率低优先选择分组。
- 支持分组不可用熔断、半开探测和恢复。
- 支持无可用分组、切换失败、自动降级、分组恢复等通知。
- 支持全局策略页面，也支持在渠道卡片或渠道详情中就地配置。
- 第一阶段完整支持 Sub2API，第二阶段支持 NewAPI。

## 非目标

- 不在第一阶段实现通用代理网关转发能力。
- 不修改用户真实请求流量路径。
- 不替代 Sub2API / NewAPI 的上游调度系统。
- 不直接在 UpstreamOps 中保存或解析用户业务请求内容。
- 不用生产 `auto` key 做周期性分组探测。
- 不在第一阶段做复杂机器学习评分。
- 不在第一阶段强依赖真实流量日志判断可用性。

## 术语

### 渠道

UpstreamOps 中已经配置的上游站点，类型为 `sub2api` 或 `newapi`。渠道包含 base URL、账号密码、Token/Cookie、代理、余额阈值、监控开关等信息。

### 目标 Key

需要被自动调整分组的上游 API Key。默认推荐名称为 `auto`。它是实际给下游系统或用户使用的 key。

### 探测 Key

专门用于可用性探测的 API Key。默认推荐名称为 `ops-probe-auto`。系统可以用它切换候选分组并发起轻量请求，以判断分组是否真实可用。

### 候选分组

策略允许参与自动选择的上游分组。候选分组来自现有渠道的 `ListAPIKeyGroups` 和倍率同步结果，不需要手动重新录入。

### 熔断

当某个分组连续失败或命中高权重错误时，暂时从候选列表中排除，等待冷却后进入半开探测。

## 信息架构

提供两个入口，但底层编辑同一份策略数据。

### 全局入口

新增菜单：

```text
智能分组策略
```

用于全局查看、筛选、批量管理所有渠道的 Auto Key 策略。

适合操作：

- 查看所有策略状态。
- 筛选异常策略。
- 批量暂停或恢复策略。
- 查看全部切换历史。
- 查看全部熔断分组。
- 快速定位没有可用分组的渠道。

### 渠道内入口

在渠道卡片或渠道详情中增加：

```text
智能分组
```

用于当前渠道的就地配置。

适合操作：

- 为当前渠道创建策略。
- 查看当前渠道目标 Key 的当前分组。
- 查看当前渠道候选分组健康状态。
- 立即评估当前渠道。
- 暂停当前渠道策略。
- 编辑当前渠道策略。

## 总览页交互

页面顶部展示统计卡：

```text
策略总数
运行中
异常策略
熔断分组
今日自动切换
无可用分组
```

策略表格字段：

```text
渠道
类型
目标 Key
探测 Key
当前分组
当前倍率
候选分组
熔断分组
策略状态
最近评估
最近切换
操作
```

策略状态建议：

```text
未配置
运行中
暂停
评估中
已降级
全部不可用
切换失败
配置错误
```

每行操作：

```text
查看
编辑
立即评估
暂停/恢复
切换记录
```

## 渠道内交互

渠道卡片只展示摘要，不塞复杂表单。

已配置时：

```text
智能分组：auto -> Claude 0.35x
状态：正常
候选：5，可用：3，熔断：2
```

未配置时：

```text
智能分组：未配置
[创建策略]
```

点击进入抽屉：

```text
Sub2API - A站
智能分组策略

当前状态：运行中
目标 Key：auto
探测 Key：ops-probe-auto
当前分组：Claude 0.35x
选择原因：0.28x 已熔断，0.35x 是当前可用最低倍率

[立即评估] [编辑策略] [暂停策略] [切换记录]
```

抽屉分区：

```text
状态摘要
当前选择原因
候选分组决策表
熔断状态
探测记录
切换记录
策略配置
通知配置
```

## 创建策略流程

创建策略应以向导形式呈现，降低配置错误。

### 第一步：选择现有渠道

从 UpstreamOps 已配置渠道中选择，不重新录入上游信息。

字段：

```text
渠道名称
渠道类型
基础地址
登录状态
最近倍率同步时间
API Key 管理能力
```

如果渠道登录失效，提示：

```text
该渠道登录状态不可用，请先在渠道卡片中测试登录或重新同步。
```

### 第二步：选择目标 API Key

系统通过 connector 拉取该渠道 API Key 列表。

默认搜索并推荐名称为 `auto` 的 key。

状态：

```text
已找到 auto key
未找到 auto key
API Key 列表读取失败
```

未找到时提供：

```text
[选择其他 Key] [创建 auto Key]
```

目标 Key 选择后展示：

```text
Key 名称
当前分组
当前倍率
状态
额度
过期时间
模型限制
```

### 第三步：选择探测 API Key

默认搜索并推荐名称为 `ops-probe-auto` 的 key。

未找到时提供：

```text
[选择已有 Key] [创建探测 Key]
```

探测 Key 建议配置：

```text
名称：ops-probe-auto
额度：较低
模型限制：仅允许探测模型
IP 限制：可选
状态：启用
```

提示文案：

```text
探测 Key 用于切换候选分组并发起轻量请求，不建议使用生产 auto key 做探测。
```

### 第四步：配置候选分组范围

支持三种模式。

#### 手动白名单

用户勾选允许参与自动选择的分组。

适合分组数量少、稳定范围明确的场景。

#### 规则筛选

按名称、描述、倍率筛选。

示例：

```text
名称包含：claude, sonnet, code
描述包含：稳定, 推荐
描述排除：不可用, 维护, 暂停, 满载, 限时, 测试
倍率范围：<= 0.8
```

#### 混合模式

先按规则筛选，再手动排除。

推荐作为默认模式。

### 第五步：配置排序规则

第一阶段建议提供：

```text
倍率低优先
稳定性优先
手动优先级优先
```

默认：

```text
可用分组中倍率低优先
```

排序细则：

```text
未熔断优先
探测成功优先
倍率低优先
成功率高优先
延迟低优先
最近切换少优先
```

### 第六步：配置熔断

默认值：

```text
连续失败 3 次后熔断
熔断 30 分钟
半开探测成功 2 次后恢复
半开探测失败后继续熔断 30 分钟
探测超时 15 秒
```

错误权重：

```text
401 / 403：高权重
余额不足：高权重
无可用账号：高权重
模型不存在：中权重，仅影响该探测模型
429：中权重
5xx：中权重
timeout：中权重
网络错误：中权重
```

### 第七步：配置通知

事件：

```text
自动切换成功
当前分组熔断
全部候选不可用
分组恢复可用
目标 Key 切换失败
探测 Key 创建失败
探测失败达到阈值
策略配置异常
```

默认通知：

```text
切换成功
全部不可用
目标 Key 切换失败
分组恢复
```

通知渠道复用现有通知系统，支持按事件过滤。

### 第八步：预览执行计划

保存前展示：

```text
当前会选择：Claude 0.35x
原因：
- Claude 0.28x 命中“连续失败 3 次”，熔断中
- Claude 0.35x 探测成功，倍率最低
- Claude 0.5x 探测成功，但倍率更高

保存后动作：
- 创建策略
- 不立即切换 / 立即评估并切换
```

保存按钮建议：

```text
[保存策略]
[保存并立即评估]
```

## 分组可用性判断

分组可用性分为四层，不能只依赖标题或描述。

### 第一层：静态规则

判断分组是否应该进入候选池。

条件：

```text
分组存在
倍率有效
在允许范围内
未命中排除关键词
未被手动禁用
不在熔断期
```

结果：

```text
候选
规则排除
手动禁用
熔断排除
未知
```

### 第二层：控制面探测

判断探测 Key 是否能切换到该分组。

操作：

```text
UpdateAPIKey(probe_key, group)
```

如果上游不允许该分组、分组 ID 无效、接口失败，则该分组不可进入可用池。

注意：

```text
不要用生产 auto key 做控制面探测。
```

### 第三层：数据面探测

使用探测 Key 发真实轻量请求，确认分组能实际调用模型。

探测端点按模型族配置：

```text
Anthropic Messages
OpenAI Chat Completions
Gemini GenerateContent
```

探测请求原则：

```text
max_tokens = 1
超时可配置
只发无敏感内容
错误体截断保存
不记录完整请求内容
```

示例：

```text
模型：claude-sonnet-4-5
端点：/v1/messages
max_tokens：1
timeout：15s
```

只调用 `/v1/models` 不足以证明分组可用，因为它可能不经过真实扣费和调度链路。

### 第四层：运行观测

基于历史表现调整健康状态。

指标：

```text
最近成功率
连续失败次数
最近延迟
最近错误类型
最近熔断次数
最近恢复次数
最近切换次数
```

第一阶段主要使用探测记录，后续可加入真实业务请求观测。

## 分组健康状态机

建议状态：

```text
unknown
healthy
suspect
open_circuit
half_open
manual_disabled
rule_excluded
```

状态转移：

```text
unknown -> healthy：探测成功
unknown -> suspect：探测失败
healthy -> suspect：单次中权重失败
healthy -> open_circuit：高权重失败或连续失败达到阈值
suspect -> healthy：后续探测成功
suspect -> open_circuit：连续失败达到阈值
open_circuit -> half_open：熔断冷却结束
half_open -> healthy：连续恢复探测成功
half_open -> open_circuit：半开探测失败
任意状态 -> manual_disabled：用户手动禁用
任意状态 -> rule_excluded：规则排除
manual_disabled -> unknown：用户手动恢复
rule_excluded -> unknown：规则变更后重新进入候选
```

## 选择算法

每次评估策略时：

1. 拉取现有渠道分组和倍率。
2. 拉取目标 Key 当前状态。
3. 拉取探测 Key 当前状态。
4. 生成候选分组列表。
5. 应用白名单、黑名单、关键词、倍率区间。
6. 排除手动禁用和熔断中分组。
7. 对候选分组执行必要探测。
8. 生成可用分组列表。
9. 按排序规则选择第一名。
10. 如果第一名与目标 Key 当前分组不同，执行切换。
11. 记录评估日志、探测日志和切换日志。
12. 根据事件触发通知。

伪逻辑：

```text
groups = loadGroups(channel)
targetKey = findTargetKey(channel, policy.target_key)
probeKey = findProbeKey(channel, policy.probe_key)

candidates = filterByPolicy(groups, policy.scope)
candidates = excludeManualDisabled(candidates)
candidates = excludeOpenCircuit(candidates)

for group in candidates:
    if shouldProbe(group):
        probeResult = probeGroup(channel, probeKey, group)
        updateHealth(group, probeResult)

available = candidates where health == healthy or half_open_success

if available is empty:
    notify no_available_groups
    keep current target group
    return

best = sort(available, policy.sort).first()

if targetKey.group != best.group:
    updateTargetKeyGroup(targetKey, best.group)
    notify switched
```

## 切换策略

目标 Key 切换必须保守。

建议规则：

```text
如果当前分组仍可用，且最佳分组只是轻微更便宜，可选择延迟切换。
如果当前分组不可用或熔断，立即降级。
如果最佳分组比当前分组低很多，允许切换。
如果短时间内切换次数过多，进入冷却。
```

配置项：

```text
min_ratio_improvement_pct
switch_cooldown_minutes
force_switch_on_current_unhealthy
keep_current_when_no_available
```

默认：

```text
当前分组可用时，只有更优倍率降低 >= 5% 才切换
当前分组不可用时，立即切换到可用最低倍率
无可用分组时保持原分组并通知
切换冷却 10 分钟
```

## 通知设计

通知复用现有通知渠道和订阅过滤。

新增事件建议：

```text
auto_group_switched
auto_group_current_circuit_opened
auto_group_all_unavailable
auto_group_recovered
auto_group_target_update_failed
auto_group_probe_failed
auto_group_policy_error
```

切换成功示例：

```text
[智能分组] 自动切换成功

渠道：Sub2API - A站
目标 Key：auto
从：Claude 0.28x
切到：Claude 0.35x
原因：原分组连续 3 次探测失败，已熔断 30 分钟
当前候选：5 个，可用 3 个，熔断 2 个
```

全部不可用示例：

```text
[智能分组] 无可用分组

渠道：Sub2API - A站
目标 Key：auto
候选分组：5 个
不可用原因：
- Claude 0.28x：熔断中，连续 3 次 timeout
- Claude 0.35x：401
- Claude 0.5x：无可用账号

系统已保持目标 Key 当前分组不变，请人工检查上游。
```

恢复示例：

```text
[智能分组] 分组恢复可用

渠道：Sub2API - A站
分组：Claude 0.28x
恢复原因：半开探测连续 2 次成功
当前动作：等待下次评估决定是否切回
```

## 数据模型建议

### `auto_group_policies`

存储策略主体。

```text
id
channel_id
name
enabled
target_key_id
target_key_name
probe_key_id
probe_key_name
candidate_mode
include_groups_json
exclude_groups_json
include_keywords_json
exclude_keywords_json
max_ratio
min_ratio
sort_mode
min_ratio_improvement_pct
switch_cooldown_minutes
failure_threshold
circuit_open_minutes
half_open_success_threshold
probe_timeout_seconds
probe_model
probe_endpoint_type
notify_enabled
last_evaluated_at
last_switched_at
last_status
last_error
created_at
updated_at
```

唯一约束建议：

```text
channel_id + target_key_id
```

如果部分上游无法稳定返回 key ID，则可降级为：

```text
channel_id + target_key_name
```

### `auto_group_candidates`

缓存每个策略下的分组状态。

```text
id
policy_id
channel_id
group_key
group_id
group_name
description
ratio
status
manual_disabled
failure_count
success_count
last_probe_at
last_probe_success
last_probe_latency_ms
last_error_code
last_error_message
circuit_opened_at
circuit_until
recovered_at
created_at
updated_at
```

### `auto_group_evaluation_logs`

记录每次评估结果。

```text
id
policy_id
channel_id
target_key_name
started_at
finished_at
success
selected_group_key
selected_group_name
selected_ratio
previous_group_key
previous_group_name
action
reason
candidate_count
available_count
circuit_open_count
error_message
created_at
```

`action` 建议：

```text
noop
switch
no_available
target_update_failed
policy_error
probe_only
```

### `auto_group_switch_logs`

记录真实切换。

```text
id
policy_id
channel_id
target_key_id
target_key_name
from_group_key
from_group_name
from_ratio
to_group_key
to_group_name
to_ratio
reason
success
error_message
created_at
```

## 后端模块建议

推荐新增：

```text
backend/autogroup
backend/api/auto_group_policies.go
backend/storage/auto_group_policies.go
```

职责：

```text
autogroup.Service
  EvaluatePolicy
  EvaluateAll
  ProbeGroup
  SwitchTargetKey
  BuildDecision
  DispatchNotifications
```

复用现有：

```text
backend/channel.Service
backend/connector.Connector
backend/notify.Dispatcher
backend/scheduler
backend/storage.Channel
backend/storage.RateSnapshot
```

Connector 需要的能力当前已经部分存在：

```text
ListAPIKeys
ListAPIKeyGroups
CreateAPIKey
UpdateAPIKey
GetRates
```

可能需要新增或扩展：

```text
ProbeAPIKeyRequest
```

如果不新增 connector 方法，也可以第一阶段在 autogroup 内按 channel type 调用标准模型接口，但长期建议抽象成 connector 能力。

## API 设计建议

```text
GET    /api/auto-group/policies
POST   /api/auto-group/policies
GET    /api/auto-group/policies/:id
PUT    /api/auto-group/policies/:id
DELETE /api/auto-group/policies/:id

POST   /api/auto-group/policies/:id/evaluate
POST   /api/auto-group/policies/:id/pause
POST   /api/auto-group/policies/:id/resume

GET    /api/auto-group/policies/:id/candidates
POST   /api/auto-group/policies/:id/candidates/:candidate_id/disable
POST   /api/auto-group/policies/:id/candidates/:candidate_id/enable
POST   /api/auto-group/policies/:id/candidates/:candidate_id/probe

GET    /api/auto-group/policies/:id/evaluation-logs
GET    /api/auto-group/policies/:id/switch-logs

GET    /api/channels/:id/auto-group-policy
POST   /api/channels/:id/auto-group-policy
```

渠道内入口可以调用同一套策略 API。

辅助 API：

```text
GET /api/channels/:id/api-keys?search=auto
GET /api/channels/:id/api-key-groups
POST /api/channels/:id/api-keys
```

这些能力当前已有相近实现，应优先复用。

## 前端页面建议

新增路由：

```text
/auto-groups
```

菜单名：

```text
智能分组
```

主要组件：

```text
AutoGroupPage
AutoGroupPolicyTable
AutoGroupPolicyDrawer
AutoGroupCandidateTable
AutoGroupDecisionPanel
AutoGroupSwitchLogs
AutoGroupProbeLogs
ChannelAutoGroupSummary
```

渠道卡片新增：

```text
智能分组状态摘要
策略按钮
```

## 决策表交互

候选分组表字段：

```text
分组
描述
倍率
规则命中
健康状态
探测结果
失败次数
熔断剩余
最近延迟
选择权重
操作
```

操作：

```text
立即探测
临时熔断
手动禁用
恢复
强制切换
查看错误
```

选择原因面板：

```text
当前选择：Claude 0.35x

为什么不是 Claude 0.28x：
- 连续 3 次探测失败
- 当前熔断剩余 18 分钟

为什么选择 Claude 0.35x：
- 探测成功
- 倍率为当前可用分组中最低
- 最近成功率 100%
```

## Scheduler 设计

新增配置：

```yaml
autoGroup:
  enabled: true
  cron: "29 */5 * * * *"
  concurrency: 2
  probeConcurrency: 2
```

默认评估周期建议：

```text
5 分钟
```

避免过于频繁地切换上游 Key。

每次调度：

1. 查询启用策略。
2. 按渠道并发限制执行。
3. 每个策略内部按候选分组探测并发限制执行。
4. 记录评估日志。
5. 触发必要通知。

## Sub2API 第一阶段支持

Sub2API 优先级最高。

原因：

- 用户主要痛点来自多个 Sub2API 上游。
- Sub2API 分组标题和描述通常直接标注倍率、可用性、维护状态。
- UpstreamOps 已经有 Sub2API 的 API Key 和分组管理能力。
- Sub2API 订阅用量和分组倍率已经在现有系统中有较多上下文。

第一阶段支持能力：

```text
读取 API Key
读取分组
读取倍率
更新 API Key 分组
创建探测 Key
静态规则过滤
控制面探测
数据面轻量探测
基础熔断
通知
手动立即评估
```

## NewAPI 第二阶段支持

NewAPI 支持应建立在统一能力抽象上。

如果 NewAPI connector 支持以下能力，则可以完整启用：

```text
ListAPIKeys
ListAPIKeyGroups
UpdateAPIKey
CreateAPIKey
Probe Request
```

如果某些 NewAPI 版本或魔改版缺能力，则 UI 显示：

```text
仅支持观测
支持半自动建议
支持完整自动切换
```

能力矩阵示例：

```text
渠道        读取分组  更新 Key 分组  创建探测 Key  数据面探测  自动切换
Sub2API A   支持      支持           支持          支持        支持
NewAPI B    支持      支持           不支持        支持        支持
NewAPI C    支持      不支持         不支持        支持        仅建议
```

## 错误处理

策略级错误：

```text
渠道不存在
渠道登录失效
目标 Key 不存在
探测 Key 不存在
分组列表读取失败
候选规则为空
无可用分组
目标 Key 更新失败
```

分组级错误：

```text
规则排除
手动禁用
控制面切换失败
数据面探测失败
熔断中
半开失败
```

数据面错误归类：

```text
auth_error
permission_denied
rate_limited
quota_exhausted
no_available_account
model_not_found
upstream_5xx
timeout
network_error
unknown
```

## 安全与成本控制

- 探测请求不能包含用户隐私内容。
- 探测响应只保存摘要，不保存完整内容。
- 探测错误体截断保存。
- 探测 Key 建议使用低额度和模型限制。
- 每个渠道限制探测并发。
- 每个策略限制切换频率。
- 无可用分组时默认保持目标 Key 当前分组，不自动禁用 Key。
- 强制切换需要二次确认。
- 删除策略不删除目标 Key 或探测 Key，只停止自动管理。

## 通知订阅复用

现有通知订阅支持事件过滤和渠道过滤。Auto Group 事件可作为新的事件类型加入。

建议增加事件分类：

```text
auto_group
```

订阅规则支持：

```text
指定渠道
指定事件
全部 Auto Group 事件
```

后续可扩展：

```text
指定策略 ID
指定目标 Key
只通知异常不通知成功切换
```

## 测试计划

### 后端单元测试

- 策略规则过滤。
- 分组关键词包含和排除。
- 倍率区间过滤。
- 熔断状态转移。
- 半开恢复。
- 选择算法。
- 当前分组可用时不频繁切换。
- 当前分组不可用时降级。
- 无可用分组时保持当前分组。
- 通知事件生成。

### Connector 测试

- Sub2API 读取 API Key。
- Sub2API 读取分组。
- Sub2API 更新 API Key 分组。
- Sub2API 创建探测 Key。
- NewAPI 能力检测。

### API 测试

- 创建策略。
- 更新策略。
- 删除策略。
- 立即评估。
- 暂停和恢复。
- 手动禁用候选分组。
- 查询评估日志。
- 查询切换日志。

### 前端测试

- 总览空状态。
- 渠道内未配置状态。
- 创建策略向导。
- 未找到 `auto` key。
- 未找到探测 key。
- 候选分组决策表。
- 立即评估 loading 和结果展示。
- 全部不可用状态。
- 熔断状态展示。

### 集成验证

```bash
go test ./...
```

```bash
cd frontend
pnpm exec tsc --noEmit
pnpm build
```

## 实施顺序

### 阶段 1：Sub2API 最小闭环

1. 新增存储表和模型。
2. 新增 Auto Group service。
3. 复用 Sub2API connector 的 API Key / 分组能力。
4. 实现策略 CRUD。
5. 实现立即评估。
6. 实现目标 Key 自动切换。
7. 实现基础熔断。
8. 实现通知。
9. 新增全局总览页。
10. 新增渠道卡片入口。

### 阶段 2：探测增强

1. 增加探测 Key 自动创建。
2. 增加数据面轻量探测。
3. 增加探测模型配置。
4. 增加半开恢复。
5. 增加切换冷却。
6. 增加选择原因面板。

### 阶段 3：NewAPI 支持

1. 补齐 NewAPI connector 能力检测。
2. 实现 NewAPI 策略评估。
3. 对不支持更新分组的 NewAPI 展示半自动建议。
4. 增加 NewAPI 数据面探测适配。

### 阶段 4：策略模板和批量管理

1. 策略模板。
2. 多渠道批量应用。
3. 批量暂停和恢复。
4. 批量立即评估。
5. 评分模型配置。

## 验收标准

第一阶段完成后应满足：

- 用户可以从现有渠道创建智能分组策略。
- 用户不需要重复录入上游地址、账号、密码或 Token。
- 系统可以找到或创建目标 `auto` key。
- 系统可以读取候选分组和倍率。
- 系统可以根据规则选择可用最低倍率分组。
- 系统可以把目标 Key 切换到选中分组。
- 当低倍率分组不可用时，系统可以熔断并降级。
- 当所有候选分组不可用时，系统不盲目切换，并发送通知。
- 用户可以在总览页和渠道内入口看到同一份策略状态。
- 用户可以查看为什么选择当前分组。

## 开放问题

- 探测请求默认模型如何按渠道自动推荐。
- 是否允许一个渠道配置多个目标 Key 策略。
- 是否需要为策略支持“只建议不自动切换”的 dry-run 模式。
- 是否需要从真实业务调用中导入失败信号。
- NewAPI 不同魔改版 API Key 分组接口差异如何能力检测。
- 是否需要在无可用分组时自动禁用目标 Key。默认建议不禁用，只通知。
- 是否需要支持“低倍率恢复后自动切回”。默认建议只在下次评估满足切换条件时切回，避免抖动。
