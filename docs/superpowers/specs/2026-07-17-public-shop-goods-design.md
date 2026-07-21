# Public Shop Goods Design

## Goal

Allow anyone to open `/shop-goods` without signing in while preserving the
current authenticated management experience and keeping shop credentials and
operations private.

## Public Boundary

- Register dedicated read-only endpoints under `/api/public` outside the
  authenticated API group.
- Do not add existing `/api/shop-targets` or `/api/shop-goods` endpoints to the
  authentication whitelist.
- `/api/public/shop-targets` returns only `id`, `name`, `last_shop_name`, and
  `site_url`.
- `/api/public/shop-goods` keeps the existing page envelope and returns items
  containing only `id`, `target_id`, `goods_key`, `name`, `category_name`,
  `link`, `price`, `stock_count`, `limit_count`, `last_seen_at`, `removed_at`,
  `payment_channel_name`, `payment_quote_quantity`,
  `payment_original_amount`, `payment_fee`, `payment_fee_payer`,
  `payment_total_amount`, `payment_quoted_at`, `target_name`,
  `target_last_shop_name`, `target_site_url`, and `target_stock_threshold`.
- Never expose shop tokens, upstream base URLs, proxy settings, monitor flags,
  sync errors, raw upstream JSON, logs, retention data, or write operations.

## Frontend Behavior

The authentication bootstrap treats `/shop-goods` as a public entry point. If
there is no local Ops token and no Sub2API embedded-login payload, it sets the
session to anonymous without calling `/api/auth/me` and without registering a
global unauthorized handler for the public view. A local token or embedded
payload still uses the existing verification flow so signed-in users retain
the management experience. Invalid local tokens fall back to the public view
instead of the login page on this route.

The route renderer selects one of two explicit branches:

- Anonymous `/shop-goods` renders a standalone public layout backed only by
  `/api/public` queries. It retains the existing shop, status, category,
  keyword, stock, sorting, pagination, shop-page, and purchase-link behavior.
  The default filter remains "in stock".
- Authenticated `/shop-goods` renders the existing application providers,
  `AppShell`, protected queries, and management page.

Both views share the same goods rows. Goods and category names use up to two
lines before truncation. Price details use the quote rules below when a current
quote is available and otherwise fall back to the synchronized unit price and
minimum quantity. Apart from these shared display enhancements, the
authenticated management experience remains unchanged.

LDXP synchronization preserves the upstream channel order, filters to channels
whose status and custom status are enabled, and selects the first remaining
channel because that is the checkout page's default. It does not fall back to a
different channel when that quote fails. The synchronizer requests one quote
per fetched product using that product's key and normalized minimum purchase
quantity, defined as `max(limit_count, 1)`.
Quote collection follows the provider request interval and is deliberately
serial to avoid increasing upstream concurrency.

The quote fields are nullable and form one atomic group. A successful quote
stores the channel name, quoted quantity, original amount, fee, fee payer,
final payable amount, and quote time. `payment_fee_payer = 1` means the buyer
pays the fee; any other value is treated as not buyer-paid for display. Before
each full or single-product refresh, the in-memory goods item has no quote. A
channel-list or individual quote failure therefore saves all quote fields as
null and clears any previous quote instead of exposing it as current. Such
upstream quote failures do not fail goods synchronization. Context cancellation
and persistence failures remain hard errors.

Manual synchronization jobs allow up to 10 minutes because quote collection is
paced and serial. Jobs remain asynchronous and queryable while they run.

Currency equality uses a `0.005` tolerance. When a buyer-paid fee is present,
the price detail uses `×quantity + fee = payable`, including `×1`, only when
both `unit price × quantity = original amount` and
`original amount + fee = payable` hold within that tolerance. When the quote
has no buyer-paid fee, `×quantity = payable` is used only when both
`unit price × quantity = original amount` and `original amount = payable` hold.
If either equation is not valid because of discounts, rounding, or another
upstream adjustment, the UI uses non-equation text:
`×quantity · 含手续费 fee · 应付 payable` for buyer-paid fees or
`×quantity · 应付 payable` otherwise. A quantity of one with no buyer-paid fee
and no payable difference keeps the single-line unit price.
`payment_total_amount` is already the final payable amount and the frontend
must not add `payment_fee` to it again. A quote is displayed only when every
quote field is non-null and its quoted quantity still matches the synchronized
normalized minimum quantity.

Authenticated users keep the current application shell and protected queries,
including the single-product stock refresh action. Anonymous users never see or
invoke refresh, synchronization, configuration, or navigation to management
tools. Other routes continue to pass through `AuthGate` unchanged.

## Data Flow And Errors

Public handlers reuse repository filtering and pagination so public and
authenticated results have the same ordering semantics. They map storage models
to explicit response DTOs before serialization. Database failures return the
existing JSON error shape without exposing SQL or internal configuration.

The public frontend handles loading, empty, and request-failure states locally.
It does not mount `AppShell`, monitor providers, or any component that issues a
protected request. Public database failures are logged server-side and return a
generic JSON error rather than serializing repository or SQL details.

## Verification

- Backend tests prove public endpoints work without an authorization header.
- Response-shape tests prove sensitive target and snapshot fields are absent.
- Existing protected endpoints still return `401` without a token.
- Run Go tests and vet, frontend linting, TypeScript checking, and the production
  build. No frontend test dependency is added because this repository has no
  frontend test runner.
- Browser verification while signed out proves `/shop-goods` renders at desktop
  and mobile widths, its network traffic stays under `/api/public`, refresh is
  absent, and filters, sorting, pagination, shop links, and purchase links work.
- Browser verification proves long goods and category names are limited to two
  lines. Without a valid quote, normalized quantities greater than one show the
  computed unit-price total and a quantity of one remains single-line.
- Provider and monitor tests prove the default channel ID and minimum quantity
  are sent to channel-specific pricing, successful quotes are persisted, and a
  failed quote does not fail the containing shop synchronization.
- Tests cover goods-key association, upstream-order channel selection, serial
  quote calls, continuation after one quote failure, context cancellation,
  public nullable DTO fields, and atomic stale-quote clearing after channel,
  item, and single-product refresh failures.
- Browser verification proves buyer-paid fees render as
  `×quantity + fee = payable`, including the `×1` case, while unavailable or
  zero-fee single-item quotes do not add visual noise.
- Tests and browser fixtures cover raw `limit_count` values below one, equal to
  one, and greater than one, exact formula cases, and discounted or rounded
  quotes that must use non-equation fallback text.
- Browser verification while signed out proves another route still shows login.
- Browser verification while signed in proves `/shop-goods` retains the current
  application shell, protected queries, and stock-refresh action.
