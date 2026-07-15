import type { ShopGoodsSort, ShopGoodsStatus } from "@/lib/api-types"

export type ShopGoodsStatusFilter = Exclude<ShopGoodsStatus, "in_stock">

const allShopGoodsPreferencesKey = "upstream-ops:shop-goods-preferences:v1"
const shopsGoodsPreferencesKey = "upstream-ops:shops-goods-preferences:v1"

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
  sort: ShopGoodsSort
  keyword: string
  categoryName: string
  pageSize: number
}

export interface ShopsGoodsPreferences {
  selectedTargetID: number | null
  status: ShopGoodsStatusFilter
  inStockOnly: boolean
  keyword: string
  categoryIDs: Record<string, number | null>
  sorts: Record<string, ShopGoodsSort>
}

const defaultAllShopGoodsPreferences: AllShopGoodsPreferences = {
  targetID: null,
  status: "all",
  inStockOnly: true,
  sort: "category",
  keyword: "",
  categoryName: "",
  pageSize: 50,
}

const defaultShopsGoodsPreferences: ShopsGoodsPreferences = {
  selectedTargetID: null,
  status: "all",
  inStockOnly: true,
  keyword: "",
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
    sort: asSort(value.sort),
    keyword: asText(value.keyword),
    categoryName: asText(value.categoryName),
    pageSize: asPageSize(value.pageSize),
  }
}

export function writeAllShopGoodsPreferences(value: AllShopGoodsPreferences) {
  writeObject(allShopGoodsPreferencesKey, value)
}

export function readShopsGoodsPreferences(): ShopsGoodsPreferences {
  const value = readObject(shopsGoodsPreferencesKey)
  if (!value) return { ...defaultShopsGoodsPreferences }
  return {
    selectedTargetID: asTargetID(value.selectedTargetID),
    status: asStatus(value.status),
    inStockOnly: typeof value.inStockOnly === "boolean" ? value.inStockOnly : defaultShopsGoodsPreferences.inStockOnly,
    keyword: asText(value.keyword),
    categoryIDs: asCategoryIDs(value.categoryIDs),
    sorts: asSorts(value.sorts),
  }
}

export function writeShopsGoodsPreferences(value: ShopsGoodsPreferences) {
  writeObject(shopsGoodsPreferencesKey, value)
}
