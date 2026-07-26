# PriceAI Radar Implementation Plan

Status: ready for implementation after plan review

Approved design: `docs/superpowers/specs/2026-07-26-priceai-radar-monitor-design.md`

## Requirements Summary

Implement a standalone `PriceAI Radar` domain that:

- Synchronizes the documented PriceAI public Price Radar Feed every five minutes without HTML or internal API crawling for price data.
- Persists the complete Feed product catalog and only the public default/preset quote boards.
- Supports local product search/facets and grouped public-board quote views, without implying full PriceAI merchant or offer search.
- Watches selected products, seeds `chatgpt-plus` and `chatgpt-team-business` exactly once when they first appear, and keeps notifications opt-in.
- Enriches risk feedback from public PriceAI product pages independently every six hours without blocking price synchronization.
- Creates/reuses exact LDXP item monitoring only from a persisted, allowlisted PriceAI offer.
- Preserves prior good data after any Feed, risk-page, or LDXP failure.

## Existing Integration Map

- GORM models and notification event constants live in `backend/storage/model.go:215-449`; models are registered centrally by `AutoMigrate` in `backend/storage/storage.go:194-228`.
- The server creates repositories/services, scheduler, runtime manager, and API dependencies in `cmd/server/main.go:94-208`.
- Authenticated API wiring is centralized in `backend/api/api.go:71-137`; new handlers should follow the grouped `shop-targets` route style in `backend/api/shop_targets.go:19-58`.
- Scheduler cron registration, no-overlap policy, and retention live in `backend/scheduler/scheduler.go:20-130` and `backend/scheduler/scheduler.go:172-325`. Scheduler configuration is in `backend/config/config.go:100-130` and runtime reloading rebuilds it in `backend/runtimeconfig/runtime.go:131-202`.
- Notification subscriptions are JSON rules in `backend/notify/subscription.go:22-139`; dispatch currently uses `Message.ChannelID` and event matching in `backend/notify/dispatcher.go:97-109` and `backend/notify/dispatcher.go:263-290`.
- Existing LDXP item URL parsing resolves a shop but drops the item key in `backend/shopprovider/provider.go:158-257`. Existing `filters` and `goods_keys` scope handling is mutually exclusive in `backend/shopmonitor/service.go:605-700`.
- The frontend uses lazy routes in `frontend/src/main.tsx:17-42`, header navigation in `frontend/components/monitor/monitor-header.tsx:38-49`, document titles in `frontend/lib/document-title.ts:1-15`, shared API types in `frontend/lib/api-types.ts`, and `useApi` query hooks in `frontend/lib/queries.ts:42-130`.

## Guardrails

- Do not add third-party dependencies. Use the Go standard library, GORM, current Gin patterns, and existing frontend components.
- Price data must come only from the PriceAI discovery/pointer/immutable snapshot contract. Do not call undocumented PriceAI endpoints.
- Risk extraction is page-derived, low-frequency, strict-identifier matched, and best effort.
- Current public boards are not a complete offer dataset. Keep this limitation in API fields and UI copy.
- Never accept a browser-supplied URL for LDXP monitoring. Resolve the stored local offer server-side.
- Preserve unrelated worktree changes. The plan artifact stays under `.omx/plans/` and is not part of the product implementation commit.

## Testable Acceptance Criteria

1. A successful Feed sync uses conditional pointer requests, does not download an immutable snapshot when `snapshot_id` is unchanged, and commits all current-state changes atomically.
2. A malformed pointer/snapshot, cross-host redirect, invalid schema/version, duplicate product, or malformed offer leaves the prior committed catalog untouched.
3. The local catalog can filter all imported Feed products by text, platform, product type, watch state, and availability; public quote search is labelled as public-board-only.
4. Quotes are deduplicated by offer ID/canonical URL, grouped only within a product by normalized original title, and retain distinct offers from the same merchant.
5. Default/preset board rows absent from a later valid snapshot are removed from current views and cannot be used to create a new LDXP target.
6. Currency changes or missing aggregate currency produce an audit event but never a false price-drop or target-price alert.
7. Each default product is seeded once on its first valid appearance, records its creation snapshot as baseline, and starts with notifications disabled.
8. Page-derived risk feedback displays `PriceAI 商家风险` and `用户反馈待核验` only when the source or offer ID matches exactly; missing risk data is never a safety verdict.
9. Only `https://pay.ldxp.cn/item/{goodsKey}` and `https://www.ldxp.cn/item/{goodsKey}` stored offers can create exact monitoring; arbitrary URLs and non-LDXP offers are rejected.
10. Adding an exact key to a filter-scoped shop target retains filter results and additionally monitors that exact key, without accepting fuzzy keyword matches.
11. Feed/risk schedules, retention, settings save/apply, API types, and frontend configuration remain consistent after runtime reload.
12. `go test ./...` and `pnpm build` pass; manual verification demonstrates one Feed import, one grouped quote view, one risk display state, and one exact LDXP target create/reuse flow.

## Implementation Steps

### 1. Establish PriceAI Storage Contracts And Repositories

Files to add:

- `backend/storage/priceai.go`
- `backend/storage/priceai_test.go`

Files to modify:

- `backend/storage/model.go`
- `backend/storage/storage.go`
- `backend/storage/storage_test.go`

Actions:

1. Define the approved model set with explicit table names, indexes, JSON fields, and timestamps:
   - `PriceAIFeedState`
   - `PriceAIProduct`
   - `PriceAIWatchTarget`
   - `PriceAIPreset`
   - `PriceAIOffer`
   - `PriceAIOfferRanking`
   - `PriceAIProductHistory`
   - `PriceAIChangeLog`
   - `PriceAISyncLog`
   - `PriceAIRiskFeedback`
   - `PriceAILDXPTargetBinding`
2. Persist `lowest_price_currency` with product/history records and `target_price_currency` with watch targets. Keep nullable price/currency values distinguishable from zero.
3. Add `baseline_snapshot_id` to watch targets and `default_watch_seeded_slugs_json` to Feed state so target seeding is deterministic and does not recreate a user-deleted target.
4. Add unique indexes that match the design: product remote ID/slug, one target per product, preset `(product_id, remote_id)`, offer `(product_id, dedupe_key)`, ranking membership, history `(product_id, snapshot_id)`, risk `(product_id, scope, subject_remote_id)`, and the PriceAI-managed LDXP binding's `(platform, base_url, token)` plus `shop_target_id`.
5. Make `PriceAILDXPTargetBinding` the sole ownership marker for an automatically created exact LDXP target. It stores the PriceAI-created `shop_target_id` and the normalized target shop identity; manual shop targets never receive this binding.
6. Create repository methods for transaction-scoped import, current-state list/query pagination, watched-target CRUD, history/change/sync-log writes, risk cache reads/writes, current-board pruning, retention deletion, and binding lookup/create/delete. Follow the atomic repository pattern already used by `ShopTargets.Transaction` in `backend/storage/shop_targets.go:93-99`.
7. Register all models in `AutoMigrate` alongside the existing monitoring models in `backend/storage/storage.go:202-228`.

Tests:

- Migration creates all PriceAI tables and unique indexes.
- Repository upserts preserve unique logical records and transaction rollback leaves no partial snapshot.
- Current board pruning removes old rankings before offers/presets and never deletes product history.
- Retention deletes only eligible history/change/sync rows and retains current product/offer/target/risk state.
- The PriceAI LDXP binding is unique per normalized shop identity, cannot point at two targets, and is removed when its target is deleted.

Exit condition: the storage layer can express every current-state, historical, baseline, and risk-cache invariant without remote HTTP or notification code.

### 2. Build The Documented Feed Client And Transactional Import Service

Files to add:

- `backend/priceai/feed.go`
- `backend/priceai/feed_test.go`
- `backend/priceai/service.go`
- `backend/priceai/service_test.go`
- `backend/priceai/types.go`

Files to modify:

- `backend/storage/priceai.go`

Actions:

1. Define constants for the documented discovery, pointer, schema, snapshot host, and canonical product-page hosts. Use a dedicated HTTP client with context timeout, response-size ceiling, and redirect validation that rejects host/scheme changes.
2. Fetch discovery only for contract validation; fetch `latest.json` using stored `ETag` and `Last-Modified`. Treat `304` as a successful no-change run.
3. On a changed pointer, validate schema version, immutable HTTPS snapshot URL, expected host, and snapshot ID before downloading. Decode snapshots with typed Go structs; validate required data in code instead of adding a JSON-schema dependency.
4. Reject invalid timestamps, negative counts/prices, duplicate product remote IDs/slugs, duplicate board identities, malformed offer URLs, and a snapshot ID mismatch before opening the write transaction.
5. In one transaction, upsert products/presets/offers/rankings, clear `missing_from_latest_at` for reappearing products, mark catalog products missing only after a valid full snapshot, prune stale current boards, write history/logs/state, and seed default targets when their slugs first appear.
6. Calculate diffs against the prior committed snapshot: aggregate price/currency, stock count, offer count, stale transition, and deduplicated public-board membership/rank/price/status. Record board changes as published-board changes only.
7. Keep notification construction as data returned from the transaction. Dispatch only after successful commit, never during the transaction.
8. Provide a single-flight/coalesced `Sync(ctx)` entry point used by scheduler and manual API sync. Enforce the documented one-minute minimum between pointer network attempts from persisted Feed state, even when an invalid configuration or repeated manual request tries to trigger more often. Preserve current data and record a failed sync log on any fetch/validation/import error.

Tests:

- `httptest` pointer/snapshot fixtures for first sync, conditional `304`, unchanged ID, changed ID, cross-host redirect, bad schema/version, malformed data, and response limit/timeout behavior.
- Baseline, seed-later, reappearance, price up/down, currency state change, target-price match/mismatch, availability/offer count, stale/recovery, and three-failure/recovery scenarios.
- Quote deduplication/group inputs and stale-board pruning do not imply global offer removal.
- Failed runs preserve old current-state rows and do not dispatch a notification candidate.
- Concurrent/manual requests and a sub-minute attempted cadence do not issue a second pointer network request.

Exit condition: service tests prove correct Feed lifecycle and durable local snapshots with no external network dependency.

### 3. Integrate Notifications, Scheduler, Runtime Settings, And Retention

Files to modify:

- `backend/storage/model.go`
- `backend/notify/notifier.go`
- `backend/notify/subscription.go`
- `backend/notify/subscription_test.go`
- `backend/notify/dispatcher.go`
- `backend/config/config.go`
- `backend/config/config_test.go`
- `backend/scheduler/scheduler.go`
- `backend/scheduler/scheduler_test.go`
- `backend/runtimeconfig/runtime.go`
- `backend/runtimeconfig/runtime_test.go`
- `backend/api/settings.go`
- `backend/api/settings_test.go`
- `cmd/server/main.go`

Actions:

1. Add the distinct PriceAI notification event constants from the approved design. Keep existing shop events unchanged (`backend/storage/model.go:230-262`).
2. Extend `notify.Message` with an optional PriceAI target identity and extend subscription JSON with optional `priceai_target_ids`. Apply that filter only to PriceAI events; retain existing event/channel/group semantics for every old message.
3. Ensure PriceAI messages do not exploit `ChannelID == 0` to bypass a custom subscription's event filtering. Empty subscriptions remain the existing subscribe-all behavior.
4. Use `PriceAIWatchTarget` baseline/cooldown fields for product-level deduplication. Do not repurpose `NotificationCooldown`, whose key is documented as an upstream channel ID in `backend/storage/model.go:279-297`.
5. Add `priceAIFeedCron`, `priceAIRiskCron`, `priceAIConcurrency`, `priceAIRiskConcurrency`, and PriceAI history/change/sync retention fields to `SchedulerConfig`/`RetentionConfig`. Validate that a configured Feed cron cannot schedule more frequently than once per minute during config load/startup as well as settings save/apply, bind environment variables, and set staggered six-field cron defaults in the same style as `ShopCron` (`backend/config/config.go:100-130` and `backend/config/config.go:462-482`).
6. Inject `*priceai.Service` and PriceAI repository dependencies into `scheduler.New`, register independent Feed/risk jobs, apply bounded concurrency and single-flight behavior, and add retention cleanup. The existing scheduler already uses `cron.WithSeconds` plus `SkipIfStillRunning` in `backend/scheduler/scheduler.go:54-87`.
7. Wire repositories, service, scheduler factory, runtime manager, and API `Deps` in `cmd/server/main.go:99-208`. Update runtime config reload so the replacement scheduler gets the same PriceAI service instance.
8. Extend settings GET/save/apply types and tests; add scheduler/retention controls to the current Settings page rather than creating a parallel configuration surface.

Tests:

- Legacy subscription JSON retains prior behavior; PriceAI target filters and event-only filters match exactly, including `ChannelID == 0` messages with `priceai_target_ids`.
- New scheduler cron configuration validates, starts the two jobs only when configured, and runs PriceAI retention independently of shop retention.
- Config load/startup and settings save/apply reject a PriceAI Feed cron more frequent than once per minute; the service-level minimum-attempt guard still protects direct/manual callers.
- Runtime reload rebuilds a scheduler with PriceAI jobs and preserves the existing service/repository dependencies.
- Settings round trip retains every new cron/concurrency/retention field, including zero retention values.

Exit condition: scheduled and manual services use the same singleton, notification subscriptions are precise, and config reload cannot silently disable PriceAI jobs.

### 4. Add Isolated Page-Derived Risk Enrichment

Files to add:

- `backend/priceai/risk.go`
- `backend/priceai/risk_test.go`

Files to modify:

- `backend/priceai/service.go`
- `backend/storage/priceai.go`

Actions:

1. Build canonical risk URLs only as `https://priceai.cc/products/{escaped imported slug}`. Do not accept caller-provided product URLs.
2. Fetch risk pages with the same HTTPS host/redirect/size/timeout policy, but on a separate low-frequency runner.
3. Extract only the structured `riskFeedback` payload from a stable hydration/structured-data boundary; do not infer risks from text or scrape price/quote fields from HTML.
4. Cache source- and offer-scoped feedback with `fetched_at`, raw audited payload, error state, reason/summaries, count, status, and latest time. Keep prior valid cache if parsing/fetching fails.
5. Match a source record only to the same `source_id` and an offer record only to the same remote offer ID. Map `user_report_pending_verification` to the exact display state required by the spec; unknown statuses remain neutral raw status.
6. Make `RefreshRisk(ctx)` independently coalesced and callable by the six-hour scheduler/manual API path. It must never block Feed import or mutate Feed freshness/error state.

Tests:

- Correct structured extraction, absent field, malformed payload, stale cache, fetch failure retention, and host/redirect rejection.
- Exact source/offer ID matching, pending-verification mapping, unknown status rendering data, and no badge for an unmatched record.
- Feed synchronization remains successful when risk refresh fails.

Exit condition: risk state is useful and auditable but fully degradable.

### 5. Extend LDXP Parsing And Exact-Inclusion Scope Semantics

Files to modify:

- `backend/shopprovider/provider.go`
- `backend/shopprovider/provider_test.go`
- `backend/shopmonitor/service.go`
- `backend/shopmonitor/service_test.go`
- `backend/storage/shop_targets.go`
- `backend/storage/priceai.go`
- `backend/storage/storage_test.go`
- `backend/api/shop_targets.go`
- `backend/api/shop_targets_test.go`

Actions:

1. Add `GoodsKey` to `shopprovider.ParsedURL`. For `/item/{goodsKey}`, validate HTTPS plus the exact `pay.ldxp.cn`/`www.ldxp.cn` host allowlist before resolving the parent shop, preserve the path key, and fail closed on missing key/token/canonical shop.
2. Use `PriceAILDXPTargetBinding` to find a reusable dedicated target by normalized `(platform, base_url, token)`. Create the binding in the same transaction as the new target, recover safely from a unique-identity race by reloading it, and remove it from `ShopTargets.Delete` with the other dependent records.
3. Add an atomic identity validation helper for an explicitly selected shop target. This path may append a key to a manual target only after exact platform/base URL/token comparison; it must not create a PriceAI-managed binding for that target.
4. Refactor `fetchGoods`/`buildGoodsRequests` so `goods_keys` remains exact-only, while `filters` scans the union of normal filter requests and exact-key candidate requests. Do not apply one global exact-key filter to all filter results.
5. Represent an exact candidate request with the expected goods key and discard returned records whose `GoodsKey` differs. Preserve existing whole-shop behavior.
6. Keep `RefreshGoodsByKey` exact-match behavior and add regression cases covering filters plus explicit key, multiple explicit keys, no fuzzy result, and source failure.
7. Preserve existing target CRUD and ordering semantics; do not modify `NotifyEnabled` behavior as part of this integration.

Tests:

- Existing shop URL cases still pass; allowed LDXP item URLs preserve `GoodsKey`; unsupported protocol/host/path fails before remote request.
- Filter-only, goods-key-only, all, and filters-plus-explicit-key result sets are correct.
- Explicit candidates never monitor a similarly named but different key.
- Automatic reuse selects only a valid PriceAI binding; it never selects a manual target with the same shop identity.
- Explicit manual target selection validates identity, appends idempotently, and never gains a PriceAI binding.
- Deleting a PriceAI-created shop target removes its binding, and the next automatic request creates a fresh target-plus-binding rather than reusing a manual target; atomic shop target reuse/update rolls back on a conflict/failure.

Exit condition: precise monitoring is mechanically exact before any PriceAI endpoint exposes it.

### 6. Expose Authenticated PriceAI APIs And One-Click Target Creation

Files to add:

- `backend/api/priceai.go`
- `backend/api/priceai_test.go`

Files to modify:

- `backend/api/api.go`
- `cmd/server/main.go`
- `backend/storage/priceai.go`
- `backend/priceai/service.go`

Actions:

1. Extend `api.Deps` with PriceAI repositories/service and register `/api/priceai` under the existing authenticated group (`backend/api/api.go:116-137`).
2. Implement status, coalesced manual sync, catalog pagination/facets, product detail, grouped public-board offers, history, change logs, watch-target CRUD, and manual risk-refresh endpoints from the approved contract.
3. Validate pagination, sort enums, `board=default|all|preset:<remote-id>`, target price/currency pairs, and product slug existence server-side. Return the standard `{ "data": ... }` envelope.
4. Build grouped offer DTOs server-side so the frontend receives original title, normalized group, visible count, merchant count, min/max/spread, board memberships, risk matches, and stale/freshness metadata without inventing full-market coverage.
5. Implement `POST /api/priceai/offers/:offerID/shop-target` with optional `{ "shop_target_id": number }`. Load the local current offer by internal ID; reject stale/non-LDXP/malformed/disallowed URLs before calling the parser.
6. With no target ID, resolve only the `PriceAILDXPTargetBinding` for the same shop identity, then reuse that dedicated target or create target-plus-binding atomically. With a target ID, validate platform/base URL/token identity and append the exact key idempotently without changing its scope mode or filters; do not treat that manual target as automatically reusable later.
7. Return a clear create/reuse/update result and never accept a URL field from the client.

Tests:

- Auth, dependency-unavailable, validation, pagination, remote preset ID, and `{data}` envelope coverage.
- Catalog/quote scope labels and absence of stale board offers in API results.
- Manual sync reuse under concurrent requests and manual risk refresh isolation.
- Stored-offer-only LDXP endpoint, allowed hosts, target mismatch rejection, bound-target-only automatic reuse, selected manual target append, deleted-binding recovery, and idempotency.

Exit condition: every UI action has an authenticated, bounded server contract and no API can be used as an arbitrary fetch/SSRF path.

### 7. Build The PriceAI Operational Frontend And Settings Extensions

Files to add:

- `frontend/app/priceai-page.tsx`

Files to modify:

- `frontend/src/main.tsx`
- `frontend/components/monitor/monitor-header.tsx`
- `frontend/lib/document-title.ts`
- `frontend/lib/api-types.ts`
- `frontend/lib/queries.ts`
- `frontend/lib/api.ts` only if a shared request helper needs a typed extension
- `frontend/components/monitor/notification-form-dialog.tsx`
- `frontend/app/settings-page.tsx`

Actions:

1. Add TypeScript contracts for Feed status, product/catalog pages, grouped quote rows, board/preset identifiers, history, changes, risk feedback, watch targets, sync result, and LDXP target-creation result.
2. Add `useApi`-based hooks in the existing query style; build URLs only from typed filters and encoded route values.
3. Add lazy `/priceai` route, `PriceAI Radar` navigation icon/button, and document title entry using the existing operational screen patterns in `frontend/src/main.tsx:17-42` and `frontend/components/monitor/monitor-header.tsx:38-49`.
4. Implement the first operational screen with source-health status, manual sync, local product search/facets, watcher controls, price/history changes, board selector, grouped title table, sort controls, board coverage notice, risk labels/freshness, and safe external links.
5. Show an exact-monitor action only for API-declared eligible offers. Send only the stored offer ID and optional existing target ID; report created/reused/appended outcomes without constructing a shop URL client-side.
6. Extend notification event selection with a PriceAI event group and optional PriceAI target selection while preserving existing subscriptions. Extend Settings scheduler/retention form defaults and fields for PriceAI jobs, matching the current scheduler controls at `frontend/app/settings-page.tsx:760-965`.
7. Ensure responsive behavior: filters wrap, quote data scrolls predictably, action icons have tooltips, no risk state reads as a safety verdict, and the public-board coverage limitation remains visible.

Tests and checks:

- Type-check all new API types/query hooks and build the production bundle.
- Manual desktop/mobile checks for empty/loading/error/stale/risk-failure states, grouping/sorts, seed target defaults, and exact monitor result states.
- Verify custom notification subscriptions do not lose existing event selections when PriceAI fields are absent.

Exit condition: the page is usable as a standalone radar screen and accurately communicates Feed/risk/quote limitations.

### 8. Run Integrated Verification And Migration Safety Checks

Files to modify only if gaps are found:

- Relevant `*_test.go` files from steps 1-7

Actions:

1. Run focused package tests after each step, then the full backend suite:

```bash
go test ./backend/storage ./backend/priceai ./backend/notify ./backend/shopprovider ./backend/shopmonitor ./backend/api ./backend/config ./backend/runtimeconfig ./backend/scheduler
go test ./...
```

2. Build the frontend:

```bash
cd frontend
pnpm build
```

3. Start a local instance and use controlled Feed/risk fixtures first. Verify normal boot/migration, runtime settings apply, scheduled job registration, API auth, and notification subscription matching.
4. Perform one live documented Feed smoke check only after fixture coverage passes. Confirm source timestamps/coverage language, not third-party offer availability.
5. Exercise the LDXP endpoint with a stored eligible fixture/record and verify it creates/reuses an exact key target; then verify a rejected host cannot create or mutate anything.
6. Review database counts after repeated syncs to confirm immutable snapshot retries do not duplicate products/offers/history, and current-board pruning does not remove audit history.

Exit condition: all tests/builds pass, no known migration/data-loss issue remains, and manual checks cover the user-visible critical path.

## Dependency Order

```text
1 Storage contracts
  -> 2 Feed import service
  -> 3 Scheduler/config/notifications
  -> 4 Risk enrichment
  -> 5 LDXP exact semantics
  -> 6 Authenticated API
  -> 7 Frontend/settings
  -> 8 Integrated verification
```

Steps 4 and 5 can be developed in parallel after Step 2, but both must be complete before the API/frontend integration step. Step 3 must land before final scheduler/config verification.

## Risks And Mitigations

| Risk | Mitigation | Evidence |
| --- | --- | --- |
| PriceAI changes page markup | Parse only the narrow structured risk payload, keep cached data, and isolate it from Feed sync. | Design spec, risk step 4 |
| Feed data is mistaken for all offers | Enforce API/UI public-board naming and remove stale current boards without claiming global removal. | Design spec, steps 2/6/7 |
| Partial snapshot corrupts current state | Validate before one transaction and dispatch only after commit. | `backend/storage/shop_targets.go:93-99` transaction precedent |
| PriceAI notification leaks through unrelated subscriptions | Add target identity plus event filtering instead of relying on `ChannelID == 0`. | `backend/notify/subscription.go:69-91` |
| One-click monitoring broadens shop scope | Server loads stored offer; LDXP parser preserves exact key; filter scope becomes a union with exact candidate filtering. | `backend/shopmonitor/service.go:605-700` |
| Runtime reload loses new jobs | Extend scheduler factory wiring and runtime reload tests. | `backend/runtimeconfig/runtime.go:131-202` |
| History tables grow indefinitely | Add explicit retention fields and cleanup alongside existing scheduler retention. | `backend/config/config.go:116-130` |

## Execution Notes

- Keep commits scoped by vertical slice where tests pass independently: storage/feed, scheduler/notification, risk/LDXP, API/frontend, final verification.
- Do not include `.omx/` artifacts in product commits.
- No implementation starts from this plan until execution is explicitly requested.
