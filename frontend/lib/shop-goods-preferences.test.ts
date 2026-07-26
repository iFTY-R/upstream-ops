import { beforeEach, describe, expect, it } from "vitest"

import {
  forgetAllShopGoodsSearchHistory,
  readAllShopGoodsPreferences,
  readAllShopGoodsSearchHistory,
  readShopsGoodsPreferences,
  rememberAllShopGoodsSearchQuery,
  writeAllShopGoodsPreferences,
  type AllShopGoodsPreferences,
} from "./shop-goods-preferences"

const ALL_GOODS_KEY = "upstream-ops:shop-goods-preferences:v1"
const HISTORY_KEY = "upstream-ops:shop-goods-search-history:v2"

class MemoryStorage {
  private map = new Map<string, string>()

  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key)! : null
  }

  setItem(key: string, value: string) {
    this.map.set(key, String(value))
  }

  removeItem(key: string) {
    this.map.delete(key)
  }

  clear() {
    this.map.clear()
  }
}

const storage = new MemoryStorage()

beforeEach(() => {
  storage.clear()
  ;(globalThis as Record<string, unknown>).window = { localStorage: storage }
})

describe("readAllShopGoodsPreferences", () => {
  it("returns defaults when nothing is stored", () => {
    expect(readAllShopGoodsPreferences()).toEqual({
      targetID: null,
      status: "all",
      inStockOnly: true,
      showGoodsKey: false,
      groupByName: false,
      sort: "category",
      keyword: "",
      excludeKeyword: "",
      categoryName: "",
      pageSize: 50,
    })
  })

  it("round-trips a full preference object", () => {
    const value: AllShopGoodsPreferences = {
      targetID: 7,
      status: "low_stock",
      inStockOnly: false,
      showGoodsKey: true,
      groupByName: true,
      sort: "price_asc",
      keyword: "plus",
      excludeKeyword: "team",
      categoryName: "GPT",
      pageSize: 100,
    }
    writeAllShopGoodsPreferences(value)
    expect(readAllShopGoodsPreferences()).toEqual(value)
  })

  it("keeps legacy payloads without groupByName readable", () => {
    // v0.0.60 之前保存的对象没有 groupByName 字段，读取时必须回退为关闭。
    storage.setItem(
      ALL_GOODS_KEY,
      JSON.stringify({ targetID: 3, status: "active", sort: "stock_desc", pageSize: 20 }),
    )
    const prefs = readAllShopGoodsPreferences()
    expect(prefs.groupByName).toBe(false)
    expect(prefs.targetID).toBe(3)
    expect(prefs.status).toBe("active")
    expect(prefs.sort).toBe("stock_desc")
    expect(prefs.pageSize).toBe(20)
  })

  it("sanitizes invalid stored values back to defaults", () => {
    storage.setItem(
      ALL_GOODS_KEY,
      JSON.stringify({
        targetID: -1,
        status: "bogus",
        groupByName: "yes",
        sort: 42,
        keyword: 9,
        pageSize: 5000,
      }),
    )
    const prefs = readAllShopGoodsPreferences()
    expect(prefs.targetID).toBeNull()
    expect(prefs.status).toBe("all")
    expect(prefs.groupByName).toBe(false)
    expect(prefs.sort).toBe("category")
    expect(prefs.keyword).toBe("")
    expect(prefs.pageSize).toBe(50)
  })

  it("survives corrupted JSON", () => {
    storage.setItem(ALL_GOODS_KEY, "{not json")
    expect(readAllShopGoodsPreferences().status).toBe("all")
  })

  it("returns defaults when window is unavailable (SSR)", () => {
    delete (globalThis as Record<string, unknown>).window
    expect(readAllShopGoodsPreferences().pageSize).toBe(50)
  })
})

describe("search history", () => {
  it("dedupes case-insensitively and keeps most recent first", () => {
    rememberAllShopGoodsSearchQuery({ keyword: "ChatGPT" })
    rememberAllShopGoodsSearchQuery({ keyword: "claude" })
    rememberAllShopGoodsSearchQuery({ keyword: "chatgpt" })
    expect(readAllShopGoodsSearchHistory().keyword).toEqual(["chatgpt", "claude"])
  })

  it("caps each field at 30 entries", () => {
    for (let i = 0; i < 40; i++) {
      rememberAllShopGoodsSearchQuery({ keyword: `term-${i}` })
    }
    const history = readAllShopGoodsSearchHistory()
    expect(history.keyword).toHaveLength(30)
    expect(history.keyword[0]).toBe("term-39")
  })

  it("ignores blank input and forgets entries case-insensitively", () => {
    rememberAllShopGoodsSearchQuery({ keyword: "  " })
    expect(readAllShopGoodsSearchHistory().keyword).toEqual([])

    rememberAllShopGoodsSearchQuery({ keyword: "Plus", categoryName: "GPT" })
    forgetAllShopGoodsSearchHistory("keyword", "pLUS")
    const history = readAllShopGoodsSearchHistory()
    expect(history.keyword).toEqual([])
    expect(history.categoryName).toEqual(["GPT"])
  })

  it("drops malformed stored history", () => {
    storage.setItem(HISTORY_KEY, JSON.stringify({ keyword: ["a", 3, "", "A", "b"] }))
    expect(readAllShopGoodsSearchHistory().keyword).toEqual(["a", "b"])
  })
})

describe("readShopsGoodsPreferences", () => {
  it("filters malformed per-target maps", () => {
    storage.setItem(
      "upstream-ops:shops-goods-preferences:v1",
      JSON.stringify({
        selectedTargetID: 2,
        categoryIDs: { "1": 5, "0": 9, bad: 1, "2": null },
        sorts: { "1": "price_desc", "2": "nope" },
        goodsPageSize: 25,
      }),
    )
    const prefs = readShopsGoodsPreferences()
    expect(prefs.selectedTargetID).toBe(2)
    expect(prefs.categoryIDs).toEqual({ "1": 5, "2": null })
    expect(prefs.sorts).toEqual({ "1": "price_desc" })
    expect(prefs.goodsPageSize).toBe(25)
  })
})
