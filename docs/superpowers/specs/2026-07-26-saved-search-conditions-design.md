# Saved Search Conditions Design

## Status

Accepted in conversation. This spec captures the agreed design before implementation.

## Goal

`shops` and `shop-goods` already keep local search history for shop goods filters. Add server-saved single search conditions so useful values can be reused across browsers or devices while keeping the existing local history behavior.

This feature is for individual input values, not full search combinations.

## In Scope

- Add server-saved values for three fields:
  - `keyword`: goods keyword search, shared by `shops` and `shop-goods`.
  - `exclude_keyword`: goods exclusion keyword search, shared by `shops` and `shop-goods`.
  - `category_name`: category name search, shown only on `shop-goods`.
- Show both cloud-saved values and local history values in the search input dropdown.
- Let users select either source to fill the input and immediately run the real query.
- Let logged-in users save the current single input value to the server.
- Let users remove local history values.
- Let logged-in users remove cloud-saved values.
- Keep cloud-saved values publicly readable, including when the visitor is not logged in.
- Keep normal search usable if cloud API calls fail.

## Out of Scope

- Saving complete filter presets or combinations.
- Per-user cloud condition isolation. The app currently uses a single-admin authentication model, so saved conditions are instance-global.
- Server-side search history generated automatically from every query. Cloud values are explicit saves only.
- Category search support on `shops`.

## Backend Design

Add a new GORM model, for example `SavedSearchCondition`, with:

- `id`
- `field`: one of `keyword`, `exclude_keyword`, `category_name`
- `value`: trimmed display value
- `normalized_value`: trimmed lower-case value for case-insensitive deduplication
- `created_at`
- `updated_at`

The table should have a unique index on `(field, normalized_value)` so duplicate saves cannot create duplicate rows. Saving a duplicate value updates the existing row's `value` and `updated_at`, which refreshes recency while preserving a single row.

Add a storage repository with:

- `List(fields []SavedSearchConditionField)`: returns values sorted by `updated_at DESC`, then `id DESC`.
- `Save(field, value)`: validates the field and non-empty trimmed value, then upserts by `(field, normalized_value)`.
- `Delete(id)`: deletes a single saved row.

Register the new model in `storage.AutoMigrate`.

## API Design

Use the existing public/authenticated split:

- `GET /api/public/search-conditions?fields=keyword,exclude_keyword,category_name`
  - Public.
  - Returns cloud values for requested valid fields.
  - If `fields` is omitted, return all valid fields.
  - Response shape:

```json
{
  "data": [
    {
      "id": 1,
      "field": "keyword",
      "value": "abc",
      "created_at": "2026-07-26T12:00:00Z",
      "updated_at": "2026-07-26T12:00:00Z"
    }
  ]
}
```

- `POST /api/search-conditions`
  - Authenticated mutation.
  - Body: `{ "field": "keyword", "value": "abc" }`.
  - Returns the saved row in `{ "data": { ... } }`.
- `DELETE /api/search-conditions/:id`
  - Authenticated mutation.
  - Deletes one cloud row and returns `{ "data": { "deleted": true } }`.

Mutation endpoints must require an authenticated subject from the auth middleware. If runtime auth is disabled or unavailable, they must not become public; missing `authSubject` returns `401`.

Cloud API failures should return normal JSON errors. The frontend will catch them and keep local search unaffected.

## Frontend Design

Extend the existing `SearchHistoryInput` instead of adding separate components. It should accept structured suggestion groups:

- Cloud saved conditions.
- Local history.

For each input:

- Fetch cloud values from the public endpoint.
- Merge cloud and local lists in the dropdown.
- If the same normalized value exists in both sources, hide the local row and show the cloud row.
- Show source labels so the user can distinguish "云端保存" from "本地历史".
- Selecting a row fills the input and immediately calls the same query handler used by current local-history selection.
- Keep current Enter search, clear button, and successful-query local-history recording.

Logged-in users should see a save action for the current trimmed input value when that normalized value is not already saved in the cloud for that field. Logged-out users should not see the save action, but can still see and select cloud rows.

Local row deletion removes only that field/value from localStorage. Cloud row deletion calls the authenticated delete API and removes the row from the dropdown after success. If cloud deletion fails, local rows and normal querying remain unaffected.

## Data Flow

1. Page loads local history from `localStorage` and cloud values from `GET /api/public/search-conditions`.
2. User types. Typing updates input state only; it does not save local or cloud history.
3. User presses Enter, clicks search, or selects a suggestion. The page applies filters and triggers the existing goods query.
4. After a successful real goods query, local history is updated as it is today.
5. User explicitly saves an input value. The frontend calls `POST /api/search-conditions`, refreshes cloud suggestions, and deduplicates local display against the cloud list.
6. User removes a suggestion. Local remove updates `localStorage`; cloud remove calls `DELETE /api/search-conditions/:id`.

## Error Handling

- Invalid fields return `400`.
- Empty values return `400`.
- Unauthenticated save/delete returns `401`.
- Duplicate cloud saves update recency and return the existing logical row.
- Public cloud fetch failures are swallowed on the frontend after an optional low-noise console/debug path; the dropdown still shows local history.
- Save/delete failures show a toast but do not change the active search filters.

## Testing

Backend:

- Auto-migration creates the saved condition table and unique index.
- Saving duplicate values with different casing produces one row and updates recency.
- Public list works without authentication.
- Save/delete require authentication and reject missing auth.
- Invalid field and empty value validation return `400`.

Frontend:

- Type check the new structured `SearchHistoryInput` props and page integrations.
- Build the frontend production bundle.
- Manually verify both pages:
  - cloud and local sections render separately,
  - duplicate local values are hidden when cloud exists,
  - selecting a suggestion immediately queries,
  - logged-out users can read/select cloud values but cannot save/delete cloud values,
  - local delete still works.
