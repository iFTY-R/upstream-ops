# Shop Monitor Design

Date: 2026-07-04

## Summary

Add a dedicated "店铺监控" feature for monitoring product state in card-selling shop pages such as `https://pay.ldxp.cn/shop/7FCVUA4X`.

The first implementation targets the 链动小铺 / 鲸商城 Pro public shop protocol. The design keeps a small provider abstraction so later card-shop platforms can be added without rewriting storage, API, notifications, or frontend page structure.

## Goals

- Add a standalone frontend menu entry and page for shop monitoring.
- Support multiple monitored shops, not just `7FCVUA4X`.
- Support three monitoring scopes per shop:
  - Whole shop.
  - Category and keyword filters.
  - Explicit product keys.
- Monitor product price, stock, category, product existence, and product metadata changes.
- Detect and log product added, product removed, price changed, stock changed, low stock, restocked, and monitor failure events.
- Reuse the existing notification dispatch system.
- Avoid order creation, payment, or order-query workflows in the first version.

## Non-Goals

- Do not create orders or call payment endpoints.
- Do not automate checkout, payment, or order polling.
- Do not build a fully generic arbitrary-webpage monitoring engine in the first version.
- Do not merge shop monitoring into the existing NewAPI/Sub2API channel model.
- Do not require seller account credentials; the first version only consumes public shop APIs.

## Target Protocol

The `ldxp` provider calls public JSON endpoints exposed by the shop frontend:

```text
POST /shopApi/Shop/info
POST /shopApi/Shop/categoryList
POST /shopApi/Shop/goodsList
POST /shopApi/Shop/getGoodsPrice
```

For `https://pay.ldxp.cn/shop/7FCVUA4X`, the parsed fields are:

```text
base_url = https://pay.ldxp.cn
token    = 7FCVUA4X
platform = ldxp
```

Observed monitorable product fields include:

```text
goods_key
goods_type
name
link
price
market_price
category.id
category.name
extend.stock_count
extend.limit_count
extend.send_order
contact_format
```

## Architecture

Add independent modules instead of extending `backend/channel`, because the current channel model is account-centric and designed around NewAPI/Sub2API authentication, balance, rates, subscriptions, and upstream admin operations.

Recommended package layout:

```text
backend/shopprovider
backend/shopprovider/ldxp
backend/shopmonitor
backend/api/shop_targets.go
backend/storage/shop_targets.go
backend/storage/shop_goods.go
```

Provider interface:

```go
type Provider interface {
    Info(ctx context.Context, target Target) (*ShopInfo, error)
    Categories(ctx context.Context, target Target, req CategoryRequest) ([]Category, error)
    Goods(ctx context.Context, target Target, req GoodsRequest) (*GoodsPage, error)
    Price(ctx context.Context, target Target, req PriceRequest) (*PriceResult, error)
}
```

The monitor service owns scheduling, snapshot comparison, change logging, and notification dispatch. Providers should only translate remote platform APIs into normalized shop/product structures.

## Storage

### `shop_targets`

Stores monitored shop configuration.

```text
id
name
platform
site_url
base_url
token
monitor_enabled
scope_mode
goods_types_json
category_ids_json
category_names_json
keywords_json
goods_keys_json
stock_threshold
price_change_enabled
stock_change_enabled
low_stock_enabled
restock_enabled
new_goods_enabled
removed_goods_enabled
proxy_enabled
sort_order
last_sync_at
last_error
created_at
updated_at
```

`scope_mode` values:

```text
all
filters
goods_keys
```

Use JSON strings for list fields to stay consistent with existing lightweight storage patterns and to avoid additional join tables in the first version.

### `shop_goods_snapshots`

Stores the latest known state per product.

```text
id
target_id
goods_key
goods_type
name
category_id
category_name
link
price
market_price
stock_count
limit_count
send_order
contact_format
raw_json
first_seen_at
last_seen_at
last_changed_at
removed_at
created_at
updated_at
```

Unique index:

```text
target_id + goods_key
```

`removed_at` is nullable. A missing product marks `removed_at` instead of deleting the row, so the UI can show disappeared goods and later detect restocking or reappearance.

### `shop_goods_change_logs`

Stores durable change history.

```text
id
target_id
goods_key
goods_name
event
old_value
new_value
summary
changed_at
created_at
```

Event values:

```text
goods_added
goods_removed
price_changed
stock_changed
stock_low
goods_restocked
monitor_failed
```

`old_value` and `new_value` are strings to support simple scalar changes and compact JSON snippets without schema churn.

### `shop_monitor_logs`

Stores sync attempts.

```text
id
target_id
success
error_message
goods_count
changed_count
started_at
finished_at
duration_ms
created_at
```

## Backend API

Add route registration under `/api/shop-targets`.

```text
GET    /api/shop-targets
POST   /api/shop-targets
GET    /api/shop-targets/:id
PUT    /api/shop-targets/:id
DELETE /api/shop-targets/:id

POST   /api/shop-targets/parse-url
POST   /api/shop-targets/:id/test
POST   /api/shop-targets/:id/sync
POST   /api/shop-targets/sync-all

GET    /api/shop-targets/:id/categories
GET    /api/shop-targets/:id/goods
GET    /api/shop-targets/:id/change-logs
GET    /api/shop-targets/:id/monitor-logs
```

`parse-url` request:

```json
{
  "site_url": "https://pay.ldxp.cn/shop/7FCVUA4X"
}
```

`parse-url` response:

```json
{
  "platform": "ldxp",
  "base_url": "https://pay.ldxp.cn",
  "token": "7FCVUA4X"
}
```

`test` should call `Info` and `Categories`, returning normalized shop metadata and category list without writing snapshots.

`sync` should run the full snapshot diff for one target and return counts:

```json
{
  "goods_count": 15,
  "changed_count": 3,
  "events": {
    "price_changed": 1,
    "stock_changed": 2
  }
}
```

## Sync Algorithm

For one target:

1. Validate `platform`, `base_url`, and `token`.
2. Call `Info` to ensure the shop is reachable.
3. Decide goods types. Default to `["card"]` if not configured.
4. Load categories for each goods type.
5. Build scan requests from `scope_mode`.
6. Fetch goods pages until all pages are exhausted.
7. De-duplicate by `goods_key`.
8. Load existing snapshots for the target.
9. Insert snapshots for new goods and emit `goods_added` if this is not the first sync.
10. Compare existing goods with fetched goods.
11. Emit `price_changed` when price changes.
12. Emit `stock_changed` when stock changes.
13. Emit `goods_restocked` when stock changes from `0` to `>0` or when a removed product reappears.
14. Emit `stock_low` only when the product crosses from above threshold to at-or-below threshold, or on first detection after product creation if configured.
15. Mark previously existing but missing goods as removed and emit `goods_removed`.
16. Save monitor log.
17. Update target `last_sync_at` and `last_error`.
18. Dispatch a single aggregated notification for the scan.

First sync behavior:

- Create baseline snapshots.
- Do not send `goods_added` for every existing product.
- Send monitor failure notifications if the first sync fails.

## Scope Semantics

### Whole Shop

Scan all configured `goods_types`. For each type, request `category_id=0` where supported, because observed `ldxp` behavior returns all goods for a type when `category_id=0`.

### Category And Keyword Filters

Scan configured category IDs directly. If keywords are configured, issue keyword searches with `category_id=0`. Merge and de-duplicate results by `goods_key`.

### Explicit Product Keys

The `ldxp` public product list is category/search based. For explicit product keys, first use cached snapshots when possible. If a key is not cached, scan all configured goods types and categories until found. Later implementation can add item-detail endpoint support if discovered.

## Notifications

Add shop-specific event constants to storage and notification filtering:

```text
shop_goods_added
shop_goods_removed
shop_price_changed
shop_stock_changed
shop_stock_low
shop_goods_restocked
shop_monitor_failed
```

The dispatcher should aggregate all changes from one scan into one message per notification channel.

Example message:

```text
[店铺监控] 全网最低Team 商品变化

新增商品: 1
价格变化: 1
库存变化: 2
低库存: 1

- GPT pro20x 成品保首登: 库存 0，低于阈值 1
- 100个 team k12子号: 库存 2 -> 1
- 中转站充值100额度: 价格 100 -> 95
```

Notification subscription filtering should support shop events through the existing `events` list. For the first version, `channel_ids` can continue to refer to upstream channel IDs for old events; shop events can ignore channel filters or use a new optional `shop_target_ids` field in subscription JSON. The less invasive first step is to deliver shop events to channels whose subscription is empty or whose `events` contains the shop event.

## Frontend

Add route:

```tsx
<Route path="shops" element={<ShopMonitorPage />} />
```

Add a header menu item named `店铺监控`.

Page sections:

- Summary cards:
  - Total shops.
  - Enabled shops.
  - Total goods.
  - Low-stock goods.
  - Recent changes.
  - Failed shops.
- Shop target list:
  - Name.
  - Platform.
  - Token.
  - Scope.
  - Enabled state.
  - Last sync time.
  - Last error.
  - Actions: test, sync, edit, enable/disable, delete.
- Product snapshot table:
  - Product name.
  - Category.
  - Price.
  - Stock.
  - Product key.
  - Last seen.
  - Link.
- Change log table:
  - Event.
  - Product.
  - Old value.
  - New value.
  - Summary.
  - Time.

Dialogs:

- Add/edit shop target.
- Scope configuration.
- Category picker after successful test.
- Manual sync result.

Add frontend types alongside existing API types:

```ts
type ShopPlatform = "ldxp"
type ShopScopeMode = "all" | "filters" | "goods_keys"
type ShopChangeEvent =
  | "goods_added"
  | "goods_removed"
  | "price_changed"
  | "stock_changed"
  | "stock_low"
  | "goods_restocked"
  | "monitor_failed"
```

## Scheduling

Reuse the existing scheduler style, but keep shop sync separate from balance/rate sync.

Config additions:

```yaml
shopMonitor:
  enabled: true
  cron: "41 */10 * * * *"
  concurrency: 2
```

If the first implementation needs to stay smaller, run shop sync as part of the existing balance cron with a separate service call. The preferred design is a separate cron because shop monitoring cadence and failure semantics differ from upstream balance/rate monitoring.

## Error Handling

- Remote non-2xx responses become monitor failures.
- API envelope `code != 1` becomes monitor failure with the remote `msg`.
- A single category fetch failure should fail that target's scan to avoid partial snapshots marking goods as removed incorrectly.
- On scan failure, do not mark missing goods as removed.
- Keep previous snapshots when a scan fails.
- Write `last_error` and a failed `shop_monitor_logs` row.

## Security And Safety

- Do not store customer contact data.
- Do not store payment URLs.
- Do not call `/shopApi/Pay/order`.
- Do not call `/shopApi/Pay/query` in this feature.
- Treat product descriptions as untrusted HTML. Store raw JSON for audit/debug, but render descriptions only sanitized or not at all in the first UI.
- Limit request concurrency per shop to avoid hammering public shop APIs.

## Testing Plan

Backend unit tests:

- URL parsing:
  - Valid `https://pay.ldxp.cn/shop/7FCVUA4X`.
  - Trailing slash.
  - Unsupported URL.
- LDXP provider:
  - Decode `info`.
  - Decode `categoryList`.
  - Decode paged `goodsList`.
  - Handle `code != 1`.
- Diff logic:
  - First sync creates baseline without change spam.
  - New product.
  - Removed product.
  - Removed product reappears.
  - Price change.
  - Stock change.
  - Low-stock threshold crossing.
  - Failed scan does not mark removals.
- API:
  - CRUD.
  - Test target.
  - Manual sync.
  - Goods pagination.
  - Change-log pagination.

Frontend checks:

- TypeScript build.
- Add/edit dialog validation.
- URL parse flow.
- Scope mode switching.
- Empty states for no shops, no goods, no change logs.
- Error display for failed shop sync.

Verification commands:

```bash
go test ./...
```

```bash
cd frontend
pnpm build
```

## Implementation Sequence

1. Add storage models and repositories.
2. Add `shopprovider` interface and `ldxp` implementation.
3. Add `shopmonitor` sync and diff service.
4. Add API handlers and route registration.
5. Add scheduler integration.
6. Add notification event constants and aggregated shop notifications.
7. Add frontend types and API helpers.
8. Add `/shops` page and header navigation entry.
9. Add tests and run backend/frontend verification.

## Open Decisions

- Whether shop notification subscription filters need explicit `shop_target_ids` in the first version.
- Whether `getGoodsPrice` should be called for every product every scan. The first version can rely on `goodsList.price`; use `getGoodsPrice` only when discounts/coupons are later needed.
- Whether explicit product-key scope should require a category hint for faster scanning.
