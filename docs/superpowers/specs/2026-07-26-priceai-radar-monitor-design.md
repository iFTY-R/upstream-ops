# PriceAI Radar Monitor Design

Date: 2026-07-26

## Status

Accepted in conversation. This specification records the agreed design before implementation begins.

## Summary

Add a standalone `PriceAI 雷达` feature. It automatically imports PriceAI's documented public Price Radar Feed, keeps a local searchable product catalog, tracks selected products over time, and shows the currently published public quote boards grouped by normalized original offer title.

The initial watched products are `chatgpt-plus` and `chatgpt-team-business`. Their notifications are off by default.

The feature is separate from both upstream channel monitoring and `shopmonitor`. It may create an exact existing LDXP shop-monitor target from a stored PriceAI offer, but that is a narrow integration rather than a data-model merge.

## Goals

- Replace manual PriceAI page refreshes with automatic feed synchronization.
- Keep every product published by the Feed locally searchable and filterable by Feed-backed facets such as platform and product type.
- Watch selected products for price, availability, aggregate-count, public-board, stale-feed, and sync-health changes.
- Group the same original offer title within one PriceAI product and let users sort the visible merchant quotes by price or risk-related priority.
- Surface PriceAI product-page risk labels accurately, including their source and verification status.
- Let a stored LDXP `/item/{goodsKey}` offer create or extend an exact-product shop monitor without widening monitoring to a whole shop.
- Preserve working data when either the Feed or optional risk enrichment fails.

## Non-Goals

- Do not crawl PriceAI HTML or call undocumented internal `/api/` routes for price, stock, catalog, or quote data.
- Do not claim anonymous Feed data is a complete searchable database of all PriceAI offers or merchants.
- Do not perform arbitrary combined filters, price-range queries, raw-offer exports, or deep pagination against PriceAI; the public Feed intentionally does not provide them.
- Do not automate checkout, payment, order creation, or order queries.
- Do not label a merchant as safe, verified, fraudulent, or problematic from absence of a risk record.
- Do not reuse `shopmonitor` targets or notification flags as PriceAI watch targets.

## External Data Contract

### Authoritative Price Data

Price and public-board data use only PriceAI's documented public endpoints:

```text
Discovery: https://priceai.cc/.well-known/price-radar.json
Pointer:   https://data.priceai.cc/latest.json
Schema:    https://priceai.cc/price-radar-v1.schema.json
Docs:      https://priceai.cc/price-radar-api.md
```

The discovery document currently declares a 300-second refresh interval. The API documentation explicitly directs clients to use the Feed instead of PriceAI HTML pages or internal APIs.

The synchronization contract is:

1. Fetch `latest.json` with `If-None-Match` and `If-Modified-Since` when prior validators are available.
2. On `304 Not Modified`, update only attempt/log metadata. Do not change products or offers, and do not notify.
3. On `200`, validate the pointer and compare `snapshot_id` with the locally committed snapshot.
4. Download the immutable `snapshot_url` only when `snapshot_id` changed.
5. Validate the complete snapshot before opening the database transaction.
6. Atomically apply the snapshot, calculate watched-product changes, write the sync log, then dispatch notifications after commit.

The pointer and snapshot must be HTTPS and on the expected PriceAI data host. The snapshot ID in the downloaded object must equal the pointer snapshot ID. The importer must reject unsupported schema versions, duplicate product IDs or slugs, negative counts or prices, malformed timestamps, and malformed public offer URLs.

The exact remote-origin allowlist is intentionally fixed:

```text
Discovery and product pages: https://priceai.cc
Feed pointer and snapshots:  https://data.priceai.cc
```

The client rejects a redirect whose final URL is not HTTPS on the same allowed host for that request class. Risk enrichment and external product-page links use the canonical product URL `https://priceai.cc/products/{slug}`; `slug` is path-escaped and comes only from an imported product row.

### Coverage Boundary

The Feed gives a complete product catalog relative to that Feed, including aggregate fields such as `offer_count`, `in_stock_count`, `lowest_price`, and product snapshot time. It does **not** publish the full raw offer set.

For each product it publishes only:

- The default `top_offers` board, with at most five offers.
- Up to five existing exact single-tag `presets`, each with at most five offers.
- Aggregate counts for the larger PriceAI result set.

The UI must always describe quote search and grouping as `PriceAI 公开榜单报价`. Product search is complete only relative to the imported Feed catalog; merchant and quote search cover only the currently published boards. An absent quote must never be represented as unavailable, removed, or unknown in the full PriceAI marketplace.

### Risk Metadata Is A Separate, Page-Derived Source

The Feed schema has no risk-feedback field. PriceAI product pages can visibly expose risk metadata such as `riskFeedback`, including `scope`, `status`, count, reasons, summaries, and latest update time. This is useful, but it is not part of the documented Feed contract.

Risk enrichment therefore has the following rules:

- It fetches public product pages only to extract the narrow structured risk payload; it never calls undocumented PriceAI APIs.
- It is isolated from Feed synchronization, cached for six hours, and rate limited with bounded concurrency.
- A missing field, markup change, page timeout, or parser failure leaves the prior risk cache in place and does not fail or delay price synchronization.
- The UI labels the source as `PriceAI 页面风险标记` and shows the page extraction time.
- `status = user_report_pending_verification` displays exactly `用户反馈待核验` under the `PriceAI 商家风险` label.
- A source-scoped record applies only to a public offer with the same `source_id`; an offer-scoped record applies only to the same public offer ID. No name-only or host-only inference may create a risk badge.
- No risk record means only `未取得风险标记` or no badge. It never means safe, verified, or approved.

## Architecture

Add an isolated PriceAI domain rather than extending `backend/shopmonitor` or `backend/channel`:

```text
backend/priceai
backend/priceai/feed.go
backend/priceai/risk.go
backend/priceai/service.go
backend/api/priceai.go
backend/storage/priceai_*.go
frontend/app/priceai-page.tsx
```

Responsibilities:

- `priceai/feed.go`: fetch, conditionally cache, validate, and decode the documented pointer and immutable snapshot.
- `priceai/risk.go`: low-frequency page-derived risk extraction and strict association to stored public offers.
- `priceai/service.go`: transactional import, snapshot diffing, local catalog/query shaping, watch-target lifecycle, notification event creation, and one-click LDXP integration orchestration.
- `storage`: GORM models and repositories. Storage knows persistence, not remote HTTP or comparison decisions.
- `api`: authenticated request validation, pagination, and response shaping. It does not accept arbitrary remote PriceAI URLs for monitoring.
- `scheduler`: independent Feed and risk jobs with one in-process run lock per job kind.

Existing `shopmonitor` remains the owner of LDXP provider calls and exact-product scanning. PriceAI only invokes a narrowly defined service operation after it has resolved a stored, allowlisted offer into a trusted LDXP item.

## Storage Model

All new tables use the `priceai_` prefix. PriceAI retains current-state tables only; it does not store product history or durable aggregate-change records.

### `priceai_feed_state`

One logical row, keyed by source name, stores:

```text
source_key
latest_url
schema_url
etag
last_modified
snapshot_id
snapshot_url
schema_version
generated_at
published_at
feed_stale
last_attempt_at
last_success_at
consecutive_failures
last_error
default_watch_seeded_slugs_json
```

It prevents redundant immutable downloads, exposes source freshness, and supports stale/failure recovery decisions.

### `priceai_products`

Stores the latest product catalog snapshot:

```text
id
remote_id
slug
name
platform
product_type
spec
summary
offer_count
in_stock_count
lowest_price
lowest_price_currency
latest_seen_at
product_snapshot_generated_at
last_snapshot_id
first_seen_at
last_seen_at
missing_from_latest_at
raw_json
created_at
updated_at
```

Unique indexes: `remote_id` and `slug`.

`missing_from_latest_at` is set only after a valid full Feed commit no longer contains that product and is cleared when that product appears again in a later valid Feed. The row is not deleted. Such a catalog removal is recorded for audit but does not claim that a product is unavailable outside the Feed.

### `priceai_watch_targets`

PriceAI watch configuration is independent from shop targets:

```text
id
product_id
monitor_enabled
notify_enabled
target_price
target_price_currency
price_drop_percent
notification_cooldown_minutes
baseline_snapshot_id
last_notified_snapshot_id
last_notified_at
created_at
updated_at
```

`product_id` is unique. `target_price` and `target_price_currency` are both nullable but must be present together. The UI pre-fills the latest aggregate currency when known. A target-price comparison runs only when the target and current aggregate currencies match; an unknown or changed currency is surfaced as not comparable and never produces a false price alert. `price_drop_percent` is nullable; a price-drop event is logged regardless, while notification requires this threshold or a target-price hit.

After every successful Feed import, process the default slugs `chatgpt-plus` and `chatgpt-team-business` that are present in that valid catalog. For each slug not already recorded in `default_watch_seeded_slugs_json`, create a target only when one does not already exist, then record the slug as seeded. This lets a default product that first appears later be seeded once, while respecting a user who later deletes, disables, or changes an existing target. A newly created target stores the same committed snapshot as `baseline_snapshot_id`; changes before or in that snapshot do not notify. Seeded targets monitor automatically with notifications off and are never re-enabled or modified by later imports.

### `priceai_presets`

Stores each public preset board for a product:

```text
id
product_id
remote_id
label
group_name
description
total
generated_at
last_snapshot_id
raw_json
created_at
updated_at
```

Unique index: `(product_id, remote_id)`.

### `priceai_offers`

Stores the latest deduplicated public-board offers, not a claimed complete merchant catalog:

```text
id
product_id
remote_id
dedupe_key
source_id
source_name
source_store_name
merchant_key
title
normalized_title
price
currency
status
url
last_snapshot_id
first_seen_at
last_seen_at
raw_json
created_at
updated_at
```

The unique key is `(product_id, dedupe_key)`. `dedupe_key` is the remote offer ID when present, otherwise a canonicalized URL. `merchant_key` is `source_id` when present, otherwise canonical URL host plus normalized store name. It supports merchant counts and precise risk association but does not collapse distinct offers from the same merchant.

### `priceai_offer_rankings`

Preserves where an offer appeared:

```text
id
product_id
offer_id
board_kind
preset_id
rank
board_generated_at
last_snapshot_id
created_at
updated_at
```

Unique index: `(product_id, board_kind, preset_id, offer_id)`. `board_kind` is `default` or `preset`.

This table allows one offer shown in both the default board and a preset to appear only once in an `all public boards` grouping while retaining its board memberships and ranks.

### Sync Logs

`priceai_sync_logs` stores the five most recent Feed attempts and the five most recent risk-enrichment attempts for diagnostics:

```text
id
job_kind
snapshot_id
success
not_modified
products_count
offers_count
changed_products_count
error_message
started_at
finished_at
duration_ms
created_at
```

After a valid Feed import has calculated the prior board digest, it removes current-state preset, ranking, and offer rows that were not seen in the committed `snapshot_id`: rankings first, then offers with no remaining current ranking, then presets. The importer never presents those rows as current public boards or permits them to create a new LDXP target. Notifications are calculated from the prior current state before it is replaced, without persisting an audit trail.

### `priceai_risk_feedback`

Caches only the latest structured, page-derived risk payload:

```text
id
product_id
scope
subject_remote_id
status
feedback_count
reasons_json
summaries_json
latest_at
page_url
fetched_at
last_error
raw_json
created_at
updated_at
```

Unique index: `(product_id, scope, subject_remote_id)`. `subject_remote_id` is a source ID for `scope = source` and an offer ID for `scope = offer`.

## Feed Synchronization And Change Detection

### Import Algorithm

1. Acquire the Feed run lock. A concurrent manual or scheduled request reuses the active run rather than starting another download.
2. Fetch the pointer with conditional headers and a strict timeout/response-size limit.
3. For a changed pointer, validate the expected schema/version, PriceAI HTTPS host, snapshot ID, and immutable snapshot URL.
4. Fetch and validate the immutable snapshot in memory. Do not write partial product or offer data.
5. Begin one transaction.
6. Upsert products, presets, current public offers, and board memberships.
7. Mark prior products absent from a valid full catalog as missing while retaining their latest current row.
8. Compare each watched product with its prior current aggregate values and prior published-board digest.
9. Write the sync log and feed state in the same transaction.
10. Commit. Only then issue one aggregated notification digest for each affected watched product.

The first successful observation of a product creates a baseline. It sends no price, stock, or board-change notifications.

A newly created watch target also starts at its `baseline_snapshot_id`. Product changes are eligible for that target only from a later committed snapshot, so adding a target never treats an earlier snapshot as a new alert.

### Comparison Rules

- Persist `lowest_price_currency` from the public lowest offer when available. Compare price only when old and new values are non-null and have the same known persisted currency. A known-to-unknown, unknown-to-known, or changed currency writes `lowest_price_currency_changed`, but is not treated as a price drop or target-price hit.
- Record an aggregate availability change from `in_stock_count`; never fabricate per-offer stock because Feed offers only expose their published `status`.
- Record `lowest_price_changed` for both directions. Notifications are limited to a configured meaningful drop or a target-price hit.
- Record published-board changes from deduplicated offer identity, price, status, rank, and membership. Do not infer global merchant disappearance from a board absence.
- A newly published offer below the prior Feed lowest price is eligible for a high-signal notification when it is in the public board and the watch target permits notifications.
- A Feed `stale` transition, three consecutive Feed failures, and recovery each create a separate health event with cooldown protection.

### Current-State Storage

Current product, offer, preset, watch-target, and latest risk-feedback records are retained because they are required for the next comparison and active monitoring. Startup migration removes the retired `priceai_product_history` and `priceai_change_logs` tables. Sync logs have no scheduler retention setting: the repository enforces five records per job kind.

## Local Search, Classification, And Quote Grouping

### Product Catalog Search

`PriceAI 雷达` searches imported product fields locally. It supports:

- Free-text matching on product name, slug, specification, summary, and platform.
- Facets for platform and product type.
- Watch state, latest Feed presence, and availability filters.
- Sort by lowest price, aggregate in-stock count, product snapshot freshness, or name.

This is local catalog search. It is not a proxy to PriceAI's unavailable arbitrary offer search.

### Public Quote Search

Within a selected product, users can search the currently stored public-board offer title, merchant/source name, and store name. The quote panel labels its coverage as `公开榜单报价` and shows the product/board generation time.

The visible board selector is:

```text
Default Top 5
Each published preset
All public boards (deduplicated)
```

### Same-Title Grouping

Grouping is scoped to one PriceAI product and the selected public-board scope. It does not group across distinct PriceAI products.

1. De-duplicate identical public offers by remote offer ID, falling back to canonical URL.
2. Normalize the original offer title by trimming, Unicode-compatible lower-casing, collapsing whitespace, and normalizing full-width ASCII punctuation. Preserve the original title for display.
3. Group by `(product_id, normalized_title)`.
4. Show group title, merchant count, visible quote count, min/max public price, price spread, board memberships, and risk-badge count.
5. Keep each distinct offer row visible inside the group. The merchant key is used for count and risk association, not to silently merge different offers.

Default inner ordering is price ascending, then PriceAI board rank, then merchant name. Other supported local display sorts are:

```text
Price ascending
PriceAI published rank
Published status first
Risk labels first
Merchant name
```

The Feed has no per-offer timestamp. `freshness` can sort products and boards by their published generation timestamps, but the UI must not pretend it can rank individual merchant quotes by freshness.

For `board=preset:<id>`, `<id>` is the immutable remote preset ID from PriceAI, not the local database primary key. The API response exposes that remote ID as `preset_id` for stable frontend selection.

## Risk Display

Risk badges sit beside the merchant/offer row and remain visible regardless of local sort order.

For a matched record, show:

```text
PriceAI 商家风险
用户反馈待核验
反馈数量、原因摘要、最近反馈时间
PriceAI 页面风险标记 · 页面提取时间
```

Unknown future statuses show the raw status in a neutral `PriceAI 页面风险标记` presentation rather than being coerced to a stronger conclusion. The original PriceAI product page is always available as an external reference link.

## Notifications

PriceAI uses its own target-level `notify_enabled`, independent from shop-target flags. It may reuse the existing notification dispatcher and subscription delivery mechanisms, but receives distinct event names and optional PriceAI target filtering:

```text
priceai_lowest_price_dropped
priceai_target_price_hit
priceai_out_of_stock
priceai_restocked
priceai_new_public_lowest_offer
priceai_feed_stale
priceai_sync_failed
priceai_sync_recovered
```

Notification policy:

- Default targets are seeded once when their product first appears in a successful Feed and monitor automatically without notifications until explicitly enabled.
- Emit at most one digest per watched product and committed snapshot.
- Use the target cooldown for repeated price and availability signals.
- Send Feed failure only after three consecutive failures; send one recovery notification after a failed state recovers.
- Stale and recovered state transitions are edge-triggered, not sent every scheduled run.
- Include the product name, current Feed timestamp, prior/new aggregate values, and a link to the local PriceAI detail view. Do not state that a public board is the full marketplace.

## LDXP Exact-Product Monitoring

### Eligibility And Trust Boundary

Only a persisted PriceAI offer can trigger monitoring. The frontend submits the local stored offer ID; it never submits an arbitrary URL. The server loads the offer, validates an HTTPS LDXP item URL, and only accepts known hosts:

```text
pay.ldxp.cn
www.ldxp.cn
```

All other offer links remain ordinary external links. No arbitrary destination, redirect target, or user-provided URL is passed to `shopprovider`.

### URL Parsing Changes

`shopprovider.ParsedURL` must preserve `GoodsKey` for `/item/{goodsKey}` URLs while resolving the parent shop token. It currently resolves the item to a shop but discards the item key.

The parser must:

- Validate HTTPS and the LDXP host allowlist before calling item resolution.
- Preserve the exact path item key in `ParsedURL.GoodsKey`.
- Resolve the parent shop only through the existing LDXP item resolver.
- Fail closed if the item key, shop token, or canonical shop URL cannot be established.

### Target Semantics

Add an authenticated endpoint:

```text
POST /api/priceai/offers/:offerID/shop-target
```

It returns the resulting shop target and exact `goods_key`.

The optional request body is:

```json
{
  "shop_target_id": 123
}
```

Without `shop_target_id`, the endpoint reuses a prior PriceAI-created exact target for the same LDXP shop or creates a dedicated `scope_mode = goods_keys` target. It never silently broadens an existing whole-shop or filter target. With `shop_target_id`, it validates that the stored target resolves to the same platform, base URL, and shop token before appending the exact key.

When an explicit compatible shop target is selected, append the exact key to `goods_keys_json` idempotently. `goods_keys_json` becomes an additive exact-inclusion list for filter-scoped targets:

```text
all        -> all goods; explicit keys are already covered
filters    -> union of filter matches and exact included keys
goods_keys -> exact included keys only
```

The LDXP fetch path must use keyword search only as a candidate lookup and filter returned goods by exact `goods_key` before snapshotting. This prevents a similarly named product from becoming monitored.

## Backend API

All PriceAI routes are authenticated under the normal application API group and return the existing `{ "data": ... }` envelope.

```text
GET    /api/priceai/status
POST   /api/priceai/sync

GET    /api/priceai/products
GET    /api/priceai/products/:slug
GET    /api/priceai/products/:slug/offers

GET    /api/priceai/watch-targets
POST   /api/priceai/watch-targets
PUT    /api/priceai/watch-targets/:id
DELETE /api/priceai/watch-targets/:id

POST   /api/priceai/offers/:offerID/shop-target
POST   /api/priceai/risk-refresh
```

Key query parameters:

```text
GET /products?query=&platform=&product_type=&watch_state=&availability=&sort=&page=&page_size=
GET /products/:slug/offers?board=default|all|preset:<id>&query=&group_by=title&sort=&page=&page_size=
```

`POST /sync` coalesces with an active Feed run and returns its result rather than issuing parallel network requests. `POST /risk-refresh` is a manual best-effort enrichment trigger; it cannot refresh prices and returns queued/attempt status separately.

`POST /offers/:offerID/shop-target` rejects a missing stored offer, a non-LDXP URL, a disallowed host, a failed item resolution, or a target belonging to another shop. It has no URL field in its request body.

## Frontend

### Route And Navigation

Add a lazy-loaded authenticated route:

```tsx
<Route path="priceai" element={<PriceAIPage />} />
```

Add a `PriceAI 雷达` header navigation entry using an existing Lucide icon and the same responsive header pattern as other operational pages.

### Page Layout

The first screen is the operational view, not a landing page:

- Compact source-health strip: latest snapshot time, current snapshot ID, stale state, last success, and manual sync icon button.
- Catalog filters: text search, platform, product type, watch state, availability, and product sort.
- Product table/list: name, platform/type, aggregate lowest price, in-stock/offer counts, product freshness, watched state, and risk-data freshness.
- Selected product detail: current aggregate values, public-board selector, grouped quote table, and watch settings.
- Grouped quote table: original title, merchant/source, price/currency, published status, board membership, risk badges, and outbound source link.
- `精确监控` icon action appears only for stored offers that pass the LDXP eligibility check. A tooltip explains that it monitors this exact shop item.

The quote table permanently includes a compact coverage notice: `报价仅来自 PriceAI 当前公开 Top 5 / 预设榜单，不代表完整商家报价库。`

Risk display uses warning styling without making a safety verdict. Empty, loading, stale, failed Feed, failed risk enrichment, and no-public-board states each receive distinct, actionable presentation.

## Scheduling And Operations

Add two independent scheduler jobs:

```text
PriceAI Feed sync: every five minutes, with a fixed seconds offset
PriceAI risk enrichment: every six hours, bounded concurrency of one or two
```

Configuration belongs beside current scheduler settings, for example:

```yaml
scheduler:
  priceAIFeedCron: "23 */5 * * * *"
  priceAIRiskCron: "47 11 */6 * * *"
  priceAIConcurrency: 1
  priceAIRiskConcurrency: 1
```

The Feed job must never poll more often than once per minute. Risk work uses cached `fetched_at` and retry backoff so a failed page does not cause a tight retry loop. Both jobs are observable through `priceai_sync_logs`; the status endpoint exposes current freshness and last error.

## Error Handling

- Pointer `304` is a successful no-change sync.
- Pointer or snapshot validation failure writes a failed sync log and preserves all previously committed data.
- A network failure, non-2xx status, timeout, or oversized response increments the Feed failure counter but never marks catalog products missing.
- A valid snapshot with `stale = true` remains importable, but the stale state is prominent and can generate a transition notification.
- Risk extraction failures preserve the last valid risk cache, record a separate risk sync log, and never block Feed imports.
- A malformed one-click LDXP request fails before creating or mutating a shop target.
- Transaction or notification-dispatch failures are observable separately. Notifications are attempted only after commit, so an alert can never advertise rolled-back data.

## Security And Data Safety

- Use the fixed PriceAI allowlist `priceai.cc` for discovery/product pages and `data.priceai.cc` for pointer/snapshot data; use the fixed LDXP allowlist specified above for exact-item URLs.
- Apply context timeouts, response-size limits, redirect validation, and bounded concurrency to all remote reads.
- Treat Feed fields, risk summaries, merchant names, titles, and URLs as untrusted display data. Render text as text; do not inject remote HTML.
- Store raw JSON only for audit/debug and cap its persisted size. Do not store account credentials, customer contacts, checkout fields, or payment data.
- Open merchant and PriceAI links in a new tab with `rel="noopener noreferrer"`.
- Keep PriceAI price synchronization read-only. The LDXP integration is limited to monitoring configuration and the existing public product-list reads.

## Testing

### Backend

- Feed pointer conditional request: first fetch, `304`, unchanged snapshot ID, and changed snapshot ID.
- Snapshot contract validation: schema version, snapshot ID mismatch, duplicate products, negative values, malformed timestamps, invalid URLs, and no partial writes on failure.
- Atomic import and diffing: baseline, price up/down, aggregate-currency availability/change, target-price hit, aggregate stock transition, aggregate offer count change, stale/recovered transitions, catalog product missing/reappeared, and default product first appearing after earlier snapshots.
- Public-board rules: default/preset deduplication, board membership retention, title normalization, same-title grouping, price/rank/risk sorts, and no global-removal event from a missing board offer.
- Local product and public-quote search/facet/pagination behavior.
- Risk cache: six-hour freshness, parser failure retention, source/offer exact matching, pending-verification label mapping, and no risk badge from an unmatched record.
- Notification cooldown, one digest per snapshot, three-failure threshold, and recovery.
- API authentication, validation, and pagination for all PriceAI routes.
- LDXP integration: allowed item URLs, rejected hosts/protocols, preserved goods key, stored-offer lookup only, idempotent exact-target creation, and filter-target additive key behavior.
- Existing shop-monitor regression tests for `all`, `filters`, and `goods_keys` scopes, including exact-key filtering after keyword candidate lookup.

### Frontend

- Type check and production build.
- Product search/facet controls, catalog empty state, stale Feed state, and manual sync state.
- Quote coverage disclosure remains visible for default, preset, and all-board views.
- Same-title group expansion, price/risk/status sorts, and risk-source labels.
- Default seed targets show monitoring enabled but notifications disabled.
- Exact LDXP monitor action renders only for eligible stored offers and reports create/reuse/failure accurately.
- Responsive desktop/mobile check for filters, table overflow, detail panel, and header navigation.

Verification commands after implementation:

```bash
go test ./...
```

```bash
cd frontend
pnpm build
```

## Acceptance Criteria

- A running instance imports the documented Feed automatically every five minutes without manually refreshing PriceAI pages.
- `chatgpt-plus` and `chatgpt-team-business` are each idempotently seeded the first time they appear in a valid Feed, with notifications off.
- Users can search and classify every imported Feed product locally, while public quote search clearly states its bounded coverage.
- Same normalized offer titles are grouped within a product, and visible merchant quotes can be sorted by price and risk-related priority without losing distinct offers.
- PriceAI page-derived risk feedback is shown only when its scope identifier matches a stored public offer or source, and pending verification is labelled accurately.
- A failed Feed or risk refresh never erases prior good data or falsely marks products/offers unavailable.
- A qualifying stored LDXP offer can create or reuse an exact-product monitor; non-LDXP or arbitrary URLs cannot.
- Existing shop filter targets retain their filters when an exact PriceAI-derived item key is added.
- PriceAI notifications use their own opt-in setting, cooldown, and event types.

## Implementation Sequence

1. Add GORM models, repositories, migration coverage, current-board pruning, and sync-log caps.
2. Implement the documented Feed client, validator, importer, baseline/diff logic, and scheduler integration.
3. Add PriceAI API handlers, target settings, notifications, and backend tests.
4. Add the independent risk-enrichment adapter, strict association logic, cache behavior, and tests.
5. Extend LDXP item parsing and exact-key target semantics with regression tests.
6. Add frontend types, queries, route, navigation item, operational page, and responsive verification.
7. Run full backend/frontend verification and a manual Feed/risk/LDXP acceptance pass.
