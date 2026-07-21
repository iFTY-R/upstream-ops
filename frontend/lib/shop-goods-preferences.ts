import type { ShopGoodsSort, ShopGoodsStatus } from "@/lib/api-types"

export type ShopGoodsStatusFilter = Exclude<ShopGoodsStatus, "in_stock">

const allShopGoodsPreferencesKey = "upstream-ops:shop-goods-preferences:v1"
const shopsGoodsPreferencesKey = "upstream-ops:shops-goods-preferences:v1"
const allShopGoodsSearchHistoryKey = "upstream-ops:shop-goods-search-history:v2"
const searchHistoryLimit = 30

const validStatuses = new Set<ShopGoodsStatusFilter>([
  "all",
  "active",
  "removed",
  "low_stock",
  "out_of_stock",
])

const validSorts = new Set<ShopGoodsSort>([
  "category",
  "stock_asc",
  "stock_desc",
  "price_asc",
  "price_desc",
  "last_seen_desc",
])

export interface AllShopGoodsPreferences {
  targetID: number | null
  status: ShopGoodsStatusFilter
  inStockOnly: boolean
  showGoodsKey: boolean
  sort: ShopGoodsSort
  keyword: string
  excludeKeyword: string
  categoryName: string
  pageSize: number
}

export type AllShopGoodsSearchHistoryField = "categoryName" | "keyword" | "excludeKeyword"

export interface AllShopGoodsSearchHistory {
  categoryName: string[]
  keyword: string[]
  excludeKeyword: string[]
}

export interface ShopsGoodsPreferences {
  selectedTargetID: number | null
  status: ShopGoodsStatusFilter
  inStockOnly: boolean
  keyword: string
  excludeKeyword: string
  categoryIDs: Record<string, number | null>
  sorts: Record<string, ShopGoodsSort>
}

const defaultAllShopGoodsPreferences: AllShopGoodsPreferences = {
  targetID: null,
  status: "all",
  inStockOnly: true,
  showGoodsKey: false,
  sort: "category",
  keyword: "",
  excludeKeyword: "",
  categoryName: "",
  pageSize: 50,
}

const defaultAllShopGoodsSearchHistory: AllShopGoodsSearchHistory = {
  categoryName: [],
  keyword: [],
  excludeKeyword: [],
}

const defaultShopsGoodsPreferences: ShopsGoodsPreferences = {
  selectedTargetID: null,
  status: "all",
  inStockOnly: true,
  keyword: "",
  excludeKeyword: "",
  categoryIDs: {},
  sorts: {},
}

function readObject(key: string): Record<string, unknown> | null {
  if (typeof window === "undefined") return null
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return null
    const value = JSON.parse(raw)
    return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null
  } catch {
    return null
  }
}

function writeObject(key: string, value: object) {
  if (typeof window === "undefined") return
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // A disabled or full browser storage must not block shop queries.
  }
}

function asTargetID(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : null
}

function asStatus(value: unknown): ShopGoodsStatusFilter {
  return typeof value === "string" && validStatuses.has(value as ShopGoodsStatusFilter)
    ? value as ShopGoodsStatusFilter
    : "all"
}

function asSort(value: unknown): ShopGoodsSort {
  return typeof value === "string" && validSorts.has(value as ShopGoodsSort)
    ? value as ShopGoodsSort
    : "category"
}

function asText(value: unknown): string {
  return typeof value === "string" ? value : ""
}

function asTextHistory(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const out: string[] = []
  const seen = new Set<string>()
  for (const item of value) {
    const text = asText(item).trim()
    if (!text) continue
    const key = text.toLocaleLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(text)
    if (out.length >= searchHistoryLimit) break
  }
  return out
}

function asPageSize(value: unknown): number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 10 && value <= 200
    ? value
    : defaultAllShopGoodsPreferences.pageSize
}

function asCategoryIDs(value: unknown): Record<string, number | null> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {}
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .filter(([targetID, categoryID]) => (
        asTargetID(Number(targetID)) !== null
        && (categoryID === null || (typeof categoryID === "number" && Number.isSafeInteger(categoryID) && categoryID >= 0))
      ))
      .map(([targetID, categoryID]) => [targetID, categoryID as number | null]),
  )
}

function asSorts(value: unknown): Record<string, ShopGoodsSort> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {}
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .filter(([targetID, sort]) => asTargetID(Number(targetID)) !== null && typeof sort === "string" && validSorts.has(sort as ShopGoodsSort))
      .map(([targetID, sort]) => [targetID, sort as ShopGoodsSort]),
  )
}

export function readAllShopGoodsPreferences(): AllShopGoodsPreferences {
  const value = readObject(allShopGoodsPreferencesKey)
  if (!value) return { ...defaultAllShopGoodsPreferences }
  return {
    targetID: asTargetID(value.targetID),
    status: asStatus(value.status),
    inStockOnly: typeof value.inStockOnly === "boolean" ? value.inStockOnly : defaultAllShopGoodsPreferences.inStockOnly,
    showGoodsKey: typeof value.showGoodsKey === "boolean" ? value.showGoodsKey : defaultAllShopGoodsPreferences.showGoodsKey,
    sort: asSort(value.sort),
    keyword: asText(value.keyword),
    excludeKeyword: asText(value.excludeKeyword),
    categoryName: asText(value.categoryName),
    pageSize: asPageSize(value.pageSize),
  }
}

export function writeAllShopGoodsPreferences(value: AllShopGoodsPreferences) {
  writeObject(allShopGoodsPreferencesKey, value)
}

export function readAllShopGoodsSearchHistory(): AllShopGoodsSearchHistory {
  const value = readObject(allShopGoodsSearchHistoryKey)
  if (!value) return { ...defaultAllShopGoodsSearchHistory }
  return {
    categoryName: asTextHistory(value.categoryName),
    keyword: asTextHistory(value.keyword),
    excludeKeyword: asTextHistory(value.excludeKeyword),
  }
}

export function rememberAllShopGoodsSearchHistory(
  field: AllShopGoodsSearchHistoryField,
  value: string,
): AllShopGoodsSearchHistory {
  return rememberAllShopGoodsSearchQuery({ [field]: value })
}

export function rememberAllShopGoodsSearchQuery(
  values: Partial<Record<AllShopGoodsSearchHistoryField, string | undefined>>,
): AllShopGoodsSearchHistory {
  let next = readAllShopGoodsSearchHistory()
  let changed = false
  for (const field of ["categoryName", "keyword", "excludeKeyword"] as const) {
    const text = values[field]?.trim()
    if (!text) continue
    const normalized = text.toLocaleLowerCase()
    next = {
      ...next,
      [field]: [
        text,
        ...next[field].filter((item) => item.toLocaleLowerCase() !== normalized),
      ].slice(0, searchHistoryLimit),
    }
    changed = true
  }
  if (changed) writeObject(allShopGoodsSearchHistoryKey, next)
  return next
}

export function readShopsGoodsPreferences(): ShopsGoodsPreferences {
  const value = readObject(shopsGoodsPreferencesKey)
  if (!value) return { ...defaultShopsGoodsPreferences }
  return {
    selectedTargetID: asTargetID(value.selectedTargetID),
    status: asStatus(value.status),
    inStockOnly: typeof value.inStockOnly === "boolean" ? value.inStockOnly : defaultShopsGoodsPreferences.inStockOnly,
    keyword: asText(value.keyword),
    excludeKeyword: asText(value.excludeKeyword),
    categoryIDs: asCategoryIDs(value.categoryIDs),
    sorts: asSorts(value.sorts),
  }
}

export function writeShopsGoodsPreferences(value: ShopsGoodsPreferences) {
  writeObject(shopsGoodsPreferencesKey, value)
}
