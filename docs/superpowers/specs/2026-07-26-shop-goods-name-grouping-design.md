# Shop Goods Name Grouping Design

## Goal

Add an optional `同商品名分组` view to `/shop-goods`. When enabled, pagination operates on product-name groups and each group can be expanded to compare every matching shop quote. The selected view is restored on the next visit using the existing shop-goods local preference mechanism.

## Non-Goals

- No fuzzy title matching, aliases, or manual merge rules.
- No database schema migration or durable grouping table.
- No cloud-synced UI preference.
- No change to the existing ungrouped response or view when grouping is disabled.
- No persistence of expanded/collapsed group rows.

## Name Identity

The backend derives a portable grouping key from each filtered current snapshot:

```text
COALESCE(NULLIF(LOWER(TRIM(goods_name)), ''), LOWER(TRIM(goods_key)))
```

This removes leading and trailing whitespace and makes Latin-letter case differences equivalent. It does not remove internal whitespace or punctuation and does not perform fuzzy matching. Empty names fall back to `goods_key` so unrelated unnamed products are not merged accidentally.

The displayed name is a stable non-empty name selected from the group, falling back to `goods_key` when necessary.

## API Contract

Both existing list endpoints accept an optional query parameter:

```text
GET /api/shop-goods?group_by=name
GET /api/public/shop-goods?group_by=name
```

An absent `group_by` preserves the current `PageResult<ShopGoodsListItem>` response. The only accepted non-empty value is `name`; unsupported values return `400`.

Grouped mode returns the normal page envelope with group items:

```json
{
  "data": {
    "items": [
      {
        "group_key": "chatgpt plus",
        "name": "ChatGPT Plus",
        "shop_count": 3,
        "quote_count": 3,
        "total_stock": 21,
        "min_price": 12.5,
        "max_price": 15,
        "latest_seen_at": "2026-07-26T12:00:00Z",
        "quotes": []
      }
    ],
    "total": 42,
    "page": 1,
    "page_size": 25,
    "pages": 2
  }
}
```

`total` and `pages` count groups, not quote rows. `quotes` contains every row in that group that satisfies the active filters.

## Query And Pagination

Grouped storage retrieval uses three bounded queries rather than one query per group:

1. Count distinct normalized group keys after applying the existing target, category, status, keyword, exclusion, and public-access predicates.
2. Fetch one page of group keys and aggregates using the requested sort.
3. Fetch all matching quote rows for those group keys in one query, then attach them to the ordered groups in memory.

This prevents groups from being split across pages and avoids N+1 queries. Existing page-size bounds remain in force. Public and authenticated endpoints continue to use their existing visibility rules before grouping.

## Sorting

The existing sort selector remains the single sorting control. In grouped mode its meaning is:

| Sort | Group order | Quote order inside group |
| --- | --- | --- |
| `category` | normalized product name ascending | shop order, category, goods key |
| `stock_asc` | total stock ascending | stock ascending |
| `stock_desc` | total stock descending | stock descending |
| `price_asc` | minimum price ascending | price ascending |
| `price_desc` | maximum price descending | price descending |
| `last_seen_desc` | latest seen time descending | last seen time descending |

Stable name, target, and goods-key tie breakers make pagination deterministic. Null-price handling follows the existing list behavior.

## Frontend Behavior

Add a compact checkbox labelled `同商品名分组` beside the existing binary display options. Changing it resets the page to 1 and clears the current expanded-group set.

Grouped results render as table groups, not nested cards:

- The parent row contains an expand icon, product name, shop/quote counts, price range, total stock, and latest update time.
- Groups start collapsed to keep the table compact.
- Expanding a group renders the existing quote-row fields and authenticated actions, including single-item stock refresh.
- Loading, error, and empty states reuse the current page states.
- The top count and pagination copy use `商品组` while grouped mode is enabled.
- The same behavior is available in public mode, while management actions remain hidden as they are today.

## Preference Persistence

Extend `AllShopGoodsPreferences` with:

```ts
groupByName: boolean
```

The default is `false`. Reading uses a boolean type guard so existing `v1` local-storage values remain valid without a key migration. The existing `writeAllShopGoodsPreferences` effect writes the option with the other filters and display preferences. Expanded rows are intentionally session-only.

## Error Handling

- Invalid `group_by` values return a clear `400` response.
- A grouped-query failure returns the existing API error envelope and does not fall back to an incomplete client-side grouping.
- Missing or malformed persisted preference values fall back to ungrouped mode.
- Refreshing a child quote updates/refetches the grouped page through the existing refresh path.

## Testing

Backend storage tests cover:

- whitespace and Latin-case normalization;
- empty-name fallback;
- group-level pagination without split groups;
- existing filters being applied before grouping;
- aggregate values and each sort mode;
- deterministic quote order.

API tests cover authenticated and public grouped responses, unchanged ungrouped responses, and invalid `group_by` rejection.

Frontend verification covers preference compatibility, option restoration, page reset, grouped empty/loading/error states, expansion, authenticated actions, public mode, TypeScript, and production build.

## Rollout

No data migration is required. Grouping is opt-in and defaults off, so the current shop-goods behavior remains unchanged until a user enables it.
