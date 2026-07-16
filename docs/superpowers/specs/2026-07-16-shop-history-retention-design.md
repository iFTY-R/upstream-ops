# Shop History Retention Design

## Goal

Bound SQLite growth caused by high-frequency shop history without removing the
current product snapshot used by shop and goods pages.

## Policy

- Keep `stock_changed` and `monitor_failed` events for 15 days.
- Keep all other shop change events for 90 days.
- Keep shop monitor logs for 30 days.
- Keep completed shop sync jobs for 30 days.
- Never delete queued or running sync jobs.
- Do not apply retention to `shop_goods_snapshots`.
- A retention value of `0` disables cleanup for that category.

## Integration

The existing daily retention cron owns these deletions. Storage repositories
provide cutoff-based delete methods, while the scheduler translates configured
day counts into cutoffs and logs each result independently. This preserves the
existing failure isolation: one table failing cleanup does not block the others.

The settings page exposes all four values and supplies defaults when loading an
older server response that does not include the new fields.

The same settings section provides an immediate cleanup command. It confirms
the exact cutoff dates derived from the current form values, serializes against
the scheduled cleanup, and reports deleted rows and per-category errors. It
does not run `VACUUM` or alter the active cron configuration.

## Verification

- Configuration tests lock the `15/90/30/30` defaults.
- Storage tests verify event-tier deletion and active-job preservation.
- Scheduler tests verify all shop retention categories are invoked.
- Existing backend tests, vet, frontend type checks, and production build remain
  required before release.
