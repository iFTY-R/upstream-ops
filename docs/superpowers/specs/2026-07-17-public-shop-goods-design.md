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
  `link`, `price`, `stock_count`, `last_seen_at`, `removed_at`, `target_name`,
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
  `AppShell`, protected queries, and management page unchanged.

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
- Browser verification while signed out proves another route still shows login.
- Browser verification while signed in proves `/shop-goods` retains the current
  application shell, protected queries, and stock-refresh action.
