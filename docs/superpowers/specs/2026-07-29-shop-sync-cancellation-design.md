# 店铺商品同步停止功能设计

## 背景

商品总览页同时展示手动全量同步和定时同步批次。当前批次一旦启动，前端只能查看进度，后端也没有取消入口。遇到 LDxp 上游抖动、同步耗时过长或误操作时，用户无法停止正在进行的请求和尚未开始的店铺任务。

本设计增加批次级协作取消，并在设置页增加独立的店铺商品定时同步开关。停止当前定时批次不会关闭后续定时计划；只有设置开关控制未来是否创建定时批次。

## 目标

- 手动和定时全量同步都能从商品总览摘要区停止。
- 停止后立即取消进行中的网络请求，不再启动排队任务。
- 已完成结果保留；已经进入数据库事务的保存操作允许安全完成。
- 页面刷新后仍能看到“正在停止”或“已停止”状态及取消数量。
- 设置页可独立启用或关闭店铺商品定时同步，手动同步不受影响。

## 非目标

- 不暂停余额、倍率、PriceAI、自动分组或数据保留等其他定时任务。
- 不提供自动恢复未完成批次。
- 不通过终止进程、关闭整个调度器或回滚已完成店铺实现停止。
- 本功能不处理 LDxp `522` 重试策略。

## 交互设计

### 商品总览

“最近一次全量同步”摘要块只在批次状态为 `running` 或 `cancelling` 时显示停止操作：

- `running`：显示带停止图标的危险色“停止”按钮。
- 点击后弹出确认框，说明已完成数据会保留，进行中的请求将取消。
- 用户确认后调用取消接口。接口成功即显示 `cancelling`，按钮禁用并显示“正在停止”。
- 批次收敛后显示 `cancelled`，状态文案为“已停止”。
- 摘要明细增加“已停止 N”；详情列表中取消任务显示“已停止”。
- 停止接口失败时保留原运行状态，恢复按钮并显示错误提示。

确认框文案：

- 标题：`停止本次同步？`
- 描述：`将取消正在进行的网络请求，并停止尚未开始的店铺任务。已完成的同步结果会保留。`
- 确认按钮：`停止同步`

### 设置页

在现有调度设置区域、店铺 Cron 表达式附近增加 `启用店铺商品定时同步` 开关：

- 默认开启，兼容现有配置。
- 关闭后保存并应用配置，不再注册新的店铺商品 Cron。
- 当前已经运行的批次不受开关变化影响，需在商品总览页单独停止。
- 手动单店同步和手动全量同步始终可用。

## 状态模型

### 单店任务

现有状态保留，并新增终态 `cancelled`：

`queued -> running -> succeeded | failed | timed_out | skipped | cancelled`

取消规则：

- `queued` 任务直接变为 `cancelled`，不得进入 provider 调用。
- `running` 任务收到 context 取消。若尚在网络阶段，结束为 `cancelled`。
- 若任务已进入不可中断的数据库事务，事务完成后以实际结果收敛；成功结果不得被取消状态覆盖。
- 非取消错误仍按原规则记录为 `failed` 或 `timed_out`。

### 批次

批次新增 `cancelling` 和终态 `cancelled`：

`running -> succeeded | partial | failed | cancelling -> cancelled`

批次增加以下持久化字段：

- `cancelled`：已取消单店任务数量。
- `cancel_requested_at`：收到停止请求的时间。
- `cancelled_at`：所有任务完成状态收敛的时间。

只要用户成功请求停止，批次最终状态为 `cancelled`，即使停止前已有成功或失败任务。计数继续准确展示成功、失败、跳过和取消数量。

`running` 和 `cancelling` 是仅有的两个非终态批次状态。现有批次跟踪、刷新、最新批次查询、详情查询、清理条件和启动恢复逻辑必须同时把二者视为活动状态。`trackBatch` 在批次进入 `cancelling` 后继续调用 `refreshBatch`，直到所有任务进入终态并将批次收敛为 `cancelled`。

## 后端设计

### 取消注册表

`SyncJobRunner` 负责进程内取消能力：

- 单店任务在 `Start` 启动 goroutine 前创建包含 context、cancel 和取消标记的控制对象，并按 job ID 注册。`run` 必须复用同一个控制对象，不能在获得并发槽后另建独立 context。
- 获取并发槽时同时监听 job context。若任务在等待并发槽期间被取消，`run` 直接落库为 `cancelled`，不得调用 `MarkRunning` 或 provider。
- 获得并发槽后、调用 `MarkRunning` 前再次检查取消状态，关闭“拿到槽位”和“标记运行”之间的竞态窗口。
- 手动全量批次通过已有 job ID 列表找到并取消所属任务。
- 定时全量批次创建后生成批次 context，调度循环、provider 请求和等待过程都使用该 context。
- 任务或批次进入终态后立即删除取消函数，避免内存泄漏。
- 注册表只负责信号传递，数据库状态仍是页面展示和重启恢复的事实来源。

手动全量同步可能把已经运行的单店任务作为 `reused` job 加入新批次。任务一旦被列入批次，就明确接受该批次的取消控制；停止该批次会取消这些复用任务，即使它们早于批次创建。相关联的其他批次通过共享 job 的终态刷新各自结果。前端确认文案无需区分复用任务，但详情继续展示 `reused` 标记。

### 取消 API

新增：

`POST /api/shop-targets/sync-batches/:batch_id/cancel`

行为：

1. 查找批次，不存在返回 `404`。
2. 已是终态时幂等返回当前批次，不改变结果。
3. 原子写入 `cancelling` 和 `cancel_requested_at`。
4. 取消批次 context 及所有非终态 job context。
5. 将仍为 `queued` 的任务持久化为 `cancelled`。
6. 返回更新后的批次；后台在运行任务退出后刷新最终计数并写入 `cancelled`。

接口返回 `202 Accepted`，响应体包含更新后的批次。重复取消、取消已完成批次或完成与取消竞争时均返回数据库中的当前事实状态。

取消与自然完成并发时，数据库条件更新只允许非终态记录转换。已经写入成功的任务不会被后到的取消覆盖。

### 取消错误分类

runner 与 service 边界使用明确的取消判定，至少识别 `context.Canceled`，并允许包装后的取消错误通过 `errors.Is` 传播：

- `Service.Sync`、商品信息获取和商品分页获取发现取消时直接向上传播，不调用 `recordFailure` 或 `notifyFailure`。
- 取消不得更新店铺 `last_error`，不得写入失败 monitor log、`shop_monitor_failed` 事件或失败通知。
- 手动 `run` 和定时 `SyncAllScheduled.afterTarget` 都把取消映射为 job `cancelled`，不能映射为 `failed`、`timed_out` 或普通 `skipped`。
- 若取消到达时数据库事务已经成功提交，则以成功为准；只有实际因 context 取消退出的任务才标记为 `cancelled`。

### 定时开关

`SchedulerConfig` 增加：

```yaml
scheduler:
  shopEnabled: true
```

- 配置默认值为 `true`。
- 可选环境变量为 `SCHEDULER_SHOP_ENABLED`，遵循现有配置优先级。
- scheduler 只有在 `shopEnabled && shopCron != ""` 时注册店铺同步任务。
- 设置保存继续使用现有配置 API；应用配置继续通过 runtime manager 构造新 scheduler。

### 重启恢复

启动时扩展现有 `MarkInterrupted`：

- 遗留 `running` 任务按原有中断规则收敛。
- 遗留 `cancelling` 批次及其非终态任务统一收敛为 `cancelled`。
- 不自动重新执行被停止或中断的任务。

## 前端设计

- API 类型增加 job `cancelled`、batch `cancelling | cancelled`、`cancelled` 计数和取消时间字段；设置类型增加 `scheduler.shopEnabled`。
- 最新批次和详情查询在 `running` 或 `cancelling` 状态继续轮询，只有终态才停止。
- 新增取消 mutation，成功后立即刷新最新批次与批次详情。
- 使用现有 `useConfirm` 确认框和 toast 模式。
- 停止按钮固定尺寸，避免状态变化导致摘要布局跳动；移动端允许状态与按钮换行，不能覆盖标题或统计信息。
- 设置表单归一化缺失的 `shopEnabled` 为 `true`，避免旧配置在前端被误判为关闭。
- 手动全量同步的完成 toast 和统计逻辑单独识别 `cancelled`，显示“同步已停止”，不得把取消任务累加为失败。
- 设置页负责开关的归一化、渲染、保存和应用；关闭开关时 Cron 输入仍保留，便于重新开启后恢复原计划。

## 错误处理

- context 取消使用明确的取消分类，不写入失败通知，也不污染店铺 `last_error`。
- API 取消失败不做乐观终态更新。
- 批次已完成与取消请求并发时返回最终事实状态。
- runtime 配置应用失败时继续使用旧 scheduler，设置页保留现有待应用提示。
- 单个任务状态落库失败时记录结构化日志，并继续清理内存取消函数。

## 测试与验收

### 后端

- 取消排队任务后 provider 不会被调用。
- 任务在并发槽上等待时被取消，不会执行 `MarkRunning` 或 provider。
- 取消运行任务会中断阻塞中的 HTTP/context 操作。
- 已成功任务不会被取消覆盖。
- 重复取消和取消终态批次保持幂等。
- 手动批次和定时批次均可停止并正确汇总计数。
- 含复用 job 的手动批次被停止时，复用 job 同样取消，关联批次均能收敛。
- provider 获取期间取消不写入店铺 `last_error`、失败 monitor log 或失败通知。
- 重启能收敛 `cancelling` 批次。
- `trackBatch` 在 `cancelling` 状态持续刷新直至 `cancelled`。
- `shopEnabled=false` 时不注册店铺 Cron，其他 Cron 不受影响。
- 旧配置缺少 `shopEnabled` 时默认开启。

### 前端

- 仅活动批次显示停止按钮。
- 确认取消后才发送请求。
- `cancelling` 显示“正在停止”并禁用重复操作。
- `cancelled` 显示“已停止”和取消数量。
- 活动批次在 `running` 和 `cancelling` 状态持续轮询，终态停止轮询。
- 手动全量同步 toast 不把 `cancelled` 当作失败。
- 设置开关正确读取、保存和应用。
- 旧配置缺少 `shopEnabled` 时开关显示为开启。

### 完整验证

- `go test ./...`
- `pnpm test`
- `pnpm lint`
- `pnpm build`
- 桌面和移动视口截图检查摘要区、确认框及设置开关无溢出、遮挡或布局跳动。

## 风险与约束

- Go context 无法安全中断已经开始的数据库事务，因此停止语义是“尽快且数据安全”，不是强制终止。
- 取消注册表是进程内能力；进程重启依赖数据库恢复逻辑收敛状态。
- 手动全量同步当前先创建 job 再创建 batch，取消接口只能在 batch 响应返回后调用；API 返回前的短窗口由现有并发限制约束，不改变数据正确性。
