import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  Bell,
  CheckCircle2,
  ExternalLink,
  ListFilter,
  Loader2,
  PackageSearch,
  Pencil,
  Plus,
  Radar,
  RefreshCw,
  Search,
  ShoppingCart,
  Star,
  Store,
  Trash2,
  X,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ShopWatchRulesDrawer, type ShopWatchSeed } from "@/components/monitor/shop-watch-rules-drawer"
import { apiFetch } from "@/lib/api"
import { useShopChangeLogs, useShopGoods, useShopMonitorLogs, useShopSnapshotCategories, useShopTargets, useShopWatchRules } from "@/lib/queries"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { money, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  ShopGoodsChangeEvent,
  ShopGoodsChangeLog,
  ShopGoodsSort,
  ShopGoodsSnapshot,
  ShopGoodsStatus,
  ShopBulkNotificationInput,
  ShopBulkNotificationResult,
  ShopMonitorLog,
  ShopRefreshGoodsResult,
  ShopScopeMode,
  ShopSnapshotCategory,
  ShopSyncAllResult,
  ShopSyncResult,
  ShopTarget,
  ShopTestResult,
  ShopWatchRule,
  ShopWatchRuleInput,
} from "@/lib/api-types"

type ShopForm = {
  name: string
  site_url: string
  platform: "ldxp"
  base_url: string
  token: string
  monitor_enabled: boolean
  notify_enabled: boolean
  scope_mode: ShopScopeMode
  goods_types: string
  category_ids: string
  category_names: string
  keywords: string
  goods_keys: string
  stock_threshold: number
  proxy_enabled: boolean
  price_change_enabled: boolean
  stock_change_enabled: boolean
  low_stock_enabled: boolean
  restock_enabled: boolean
  new_goods_enabled: boolean
  removed_goods_enabled: boolean
  sort_order: number
  goods_sort: ShopGoodsSort
}

const emptyForm: ShopForm = {
  name: "",
  site_url: "",
  platform: "ldxp",
  base_url: "",
  token: "",
  monitor_enabled: true,
  notify_enabled: false,
  scope_mode: "all",
  goods_types: "card",
  category_ids: "",
  category_names: "",
  keywords: "",
  goods_keys: "",
  stock_threshold: 1,
  proxy_enabled: false,
  price_change_enabled: true,
  stock_change_enabled: true,
  low_stock_enabled: true,
  restock_enabled: true,
  new_goods_enabled: true,
  removed_goods_enabled: true,
  sort_order: 0,
  goods_sort: "category",
}

type BulkNotificationForm = {
  targetIDs: number[]
  notifyMode: "keep" | "on" | "off"
  upsertRule: boolean
  replaceSameName: boolean
  ruleName: string
  events: ShopGoodsChangeEvent[]
  stockThreshold: number
  categoryNames: string
  keywords: string
  goodsKeys: string
}

const eventLabels: Record<ShopGoodsChangeEvent, string> = {
  goods_added: "新增",
  goods_removed: "消失",
  price_changed: "价格",
  stock_changed: "库存",
  stock_low: "低库存",
  goods_restocked: "补货",
  monitor_failed: "失败",
}

type GoodsStatusFilter = Exclude<ShopGoodsStatus, "in_stock">

const goodsStatusLabels: Record<GoodsStatusFilter, string> = {
  all: "全部状态",
  active: "在线",
  removed: "已消失",
  low_stock: "低库存",
  out_of_stock: "零库存",
}

const goodsSortLabels: Record<ShopGoodsSort, string> = {
  category: "分类 / 名称",
  stock_asc: "库存从低到高",
  stock_desc: "库存从高到低",
  price_asc: "价格从低到高",
  price_desc: "价格从高到低",
  last_seen_desc: "最近出现",
}

function parseJSONList(raw: string): string[] {
  try {
    const value = JSON.parse(raw)
    return Array.isArray(value) ? value.map((item) => String(item)).filter(Boolean) : []
  } catch {
    return []
  }
}

function csv(raw: string): string[] {
  return raw.split(/[,\n]/).map((s) => s.trim()).filter(Boolean)
}

function csvNumbers(raw: string): number[] {
  return csv(raw).map(Number).filter((n) => Number.isFinite(n))
}

function formFromTarget(target: ShopTarget): ShopForm {
  return {
    ...emptyForm,
    name: target.name,
    site_url: target.site_url,
    platform: target.platform,
    base_url: target.base_url,
    token: target.token,
    monitor_enabled: target.monitor_enabled,
    notify_enabled: target.notify_enabled,
    scope_mode: target.scope_mode,
    goods_types: parseJSONList(target.goods_types_json).join(", "),
    category_ids: parseJSONList(target.category_ids_json).join(", "),
    category_names: parseJSONList(target.category_names_json).join(", "),
    keywords: parseJSONList(target.keywords_json).join(", "),
    goods_keys: parseJSONList(target.goods_keys_json).join(", "),
    stock_threshold: target.stock_threshold,
    proxy_enabled: target.proxy_enabled,
    price_change_enabled: target.price_change_enabled,
    stock_change_enabled: target.stock_change_enabled,
    low_stock_enabled: target.low_stock_enabled,
    restock_enabled: target.restock_enabled,
    new_goods_enabled: target.new_goods_enabled,
    removed_goods_enabled: target.removed_goods_enabled,
    sort_order: target.sort_order,
    goods_sort: target.goods_sort || "category",
  }
}

function shopTargetUpdateBody(target: ShopTarget, patch: Partial<ShopForm>) {
  return {
    name: target.name,
    site_url: target.site_url,
    platform: target.platform,
    base_url: target.base_url,
    token: target.token,
    monitor_enabled: target.monitor_enabled,
    notify_enabled: target.notify_enabled,
    scope_mode: target.scope_mode,
    goods_types: parseJSONList(target.goods_types_json),
    category_ids: parseJSONList(target.category_ids_json).map(Number).filter((value) => Number.isFinite(value)),
    category_names: parseJSONList(target.category_names_json),
    keywords: parseJSONList(target.keywords_json),
    goods_keys: parseJSONList(target.goods_keys_json),
    stock_threshold: target.stock_threshold,
    proxy_enabled: target.proxy_enabled,
    price_change_enabled: target.price_change_enabled,
    stock_change_enabled: target.stock_change_enabled,
    low_stock_enabled: target.low_stock_enabled,
    restock_enabled: target.restock_enabled,
    new_goods_enabled: target.new_goods_enabled,
    removed_goods_enabled: target.removed_goods_enabled,
    sort_order: target.sort_order,
    goods_sort: target.goods_sort || "category",
    ...patch,
  }
}

const defaultBulkNotificationForm: BulkNotificationForm = {
  targetIDs: [],
  notifyMode: "on",
  upsertRule: true,
  replaceSameName: true,
  ruleName: "批量关注规则",
  events: ["stock_changed", "stock_low", "goods_restocked"],
  stockThreshold: 1,
  categoryNames: "",
  keywords: "",
  goodsKeys: "",
}

function shopRulesMatchRow(rules: ShopWatchRule[], row: ShopGoodsSnapshot): boolean {
  return rules.some((rule) => {
    if (!rule.enabled) return false
    let hasCriteria = false
    const goodsKeys = parseJSONList(rule.goods_keys_json)
    if (goodsKeys.length > 0) {
      hasCriteria = true
      if (goodsKeys.some((key) => key.toLowerCase() === row.goods_key.toLowerCase())) return true
    }
    const categoryIDs = parseJSONList(rule.category_ids_json).map(Number).filter((value) => Number.isFinite(value))
    if (categoryIDs.length > 0) {
      hasCriteria = true
      if (categoryIDs.includes(row.category_id)) return true
    }
    const categoryNames = parseJSONList(rule.category_names_json)
    if (categoryNames.length > 0) {
      hasCriteria = true
      if (categoryNames.some((name) => name.toLowerCase() === row.category_name.toLowerCase())) return true
    }
    const keywords = parseJSONList(rule.keywords_json)
    if (keywords.length > 0) {
      hasCriteria = true
      const haystack = `${row.name} ${row.goods_key} ${row.category_name}`.toLowerCase()
      if (keywords.some((keyword) => haystack.includes(keyword.toLowerCase()))) return true
    }
    return !hasCriteria
  })
}

function shopDisplayName(target: ShopTarget | null) {
  if (!target) return ""
  return target.last_shop_name?.trim() || target.name
}

export default function ShopsPage() {
  const targets = useShopTargets()
  const refresh = useTriggerRefresh()
  const [selectedID, setSelectedID] = useState<number | null>(null)
  const [editing, setEditing] = useState<ShopTarget | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<ShopForm>(emptyForm)
  const [busy, setBusy] = useState<string | null>(null)
  const [goodsPage, setGoodsPage] = useState(1)
  const [changesPage, setChangesPage] = useState(1)
  const [logsPage, setLogsPage] = useState(1)
  const [selectedCategoryID, setSelectedCategoryID] = useState<number | null>(null)
  const [goodsStatus, setGoodsStatus] = useState<GoodsStatusFilter>("all")
  const [inStockOnly, setInStockOnly] = useState(false)
  const [goodsSort, setGoodsSort] = useState<ShopGoodsSort>("category")
  const [goodsKeyword, setGoodsKeyword] = useState("")
  const [highlightedGoodsKey, setHighlightedGoodsKey] = useState<string | null>(null)
  const [watchRulesOpen, setWatchRulesOpen] = useState(false)
  const [watchSeed, setWatchSeed] = useState<ShopWatchSeed | null>(null)
  const [bulkOpen, setBulkOpen] = useState(false)
  const [bulkForm, setBulkForm] = useState<BulkNotificationForm>(defaultBulkNotificationForm)
  const goodsRowRefs = useRef<Record<string, HTMLTableRowElement | null>>({})
  const goodsFilters = useMemo(
    () => ({
      category_id: selectedCategoryID ?? undefined,
      status: inStockOnly ? "in_stock" as ShopGoodsStatus : goodsStatus,
      keyword: goodsKeyword,
      sort: goodsSort,
    }),
    [goodsKeyword, goodsSort, goodsStatus, inStockOnly, selectedCategoryID],
  )
  const goods = useShopGoods(selectedID, goodsPage, 25, goodsFilters)
  const snapshotCategories = useShopSnapshotCategories(selectedID)
  const watchRules = useShopWatchRules(selectedID)
  const changes = useShopChangeLogs(selectedID, changesPage, 20)
  const monitorLogs = useShopMonitorLogs(selectedID, logsPage, 20)
  const selected = targets.data?.find((t) => t.id === selectedID) ?? null

  useEffect(() => {
    if (selectedID != null) return
    const first = targets.data?.[0]
    if (first) setSelectedID(first.id)
  }, [selectedID, targets.data])

  useEffect(() => {
    setGoodsPage(1)
    setChangesPage(1)
    setLogsPage(1)
    setSelectedCategoryID(null)
    setGoodsStatus("all")
    setInStockOnly(false)
    setGoodsSort(selected?.goods_sort || "category")
    setGoodsKeyword("")
  }, [selectedID, selected?.goods_sort])

  useEffect(() => {
    setGoodsPage(1)
  }, [goodsKeyword, goodsSort, goodsStatus, inStockOnly, selectedCategoryID])

  useEffect(() => {
    if (!highlightedGoodsKey) return
    const row = goodsRowRefs.current[highlightedGoodsKey]
    if (!row) return
    row.scrollIntoView({ behavior: "smooth", block: "center" })
    const timer = window.setTimeout(() => {
      setHighlightedGoodsKey((current) => (current === highlightedGoodsKey ? null : current))
    }, 4500)
    return () => window.clearTimeout(timer)
  }, [goods.data?.items, highlightedGoodsKey])

  const selectedWatchRules = useMemo(
    () => (watchRules.data ?? []).filter((rule) => rule.target_id === selectedID),
    [selectedID, watchRules.data],
  )
  const watchedGoodsKeys = useMemo(() => {
    const out = new Set<string>()
    for (const row of goods.data?.items ?? []) {
      if (shopRulesMatchRow(selectedWatchRules, row)) out.add(row.goods_key)
    }
    return out
  }, [goods.data?.items, selectedWatchRules])
  const summary = useMemo(() => {
    const list = targets.data ?? []
    return {
      total: list.length,
      enabled: list.filter((t) => t.monitor_enabled).length,
      goods: list.reduce((sum, t) => sum + (t.last_goods_count || 0), 0),
      low: list.reduce((sum, t) => sum + (t.last_low_stock_goods || 0), 0),
      failed: list.filter((t) => t.last_error).length,
      changed: list.reduce((sum, t) => sum + (t.last_changed_count || 0), 0),
    }
  }, [targets.data])

  function openCreate() {
    setEditing(null)
    setForm(emptyForm)
    setFormOpen(true)
  }

  function openBulkNotification() {
    const ids = (targets.data ?? []).map((target) => target.id)
    setBulkForm({ ...defaultBulkNotificationForm, targetIDs: ids })
    setBulkOpen(true)
  }

  function openEdit(target: ShopTarget) {
    setEditing(target)
    setForm(formFromTarget(target))
    setFormOpen(true)
  }

  async function parseURL() {
    if (!form.site_url.trim()) {
      toast.error("请先填写店铺 URL")
      return
    }
    setBusy("parse")
    try {
      const parsed = await apiFetch<{ platform: "ldxp"; base_url: string; token: string; name?: string; name_error?: string }>("/shop-targets/parse-url", {
        method: "POST",
        body: JSON.stringify({ site_url: form.site_url }),
      })
      setForm((f) => ({
        ...f,
        platform: parsed.platform,
        base_url: parsed.base_url,
        token: parsed.token,
        name: parsed.name || (f.name === parsed.token ? "" : f.name),
      }))
      if (parsed.name) {
        toast.success(`已解析店铺：${parsed.name}`)
      } else {
        toast.warning(parsed.name_error ? `URL 已解析，但店铺名获取失败：${parsed.name_error}` : "URL 已解析，但未获取到店铺名")
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "解析失败")
    } finally {
      setBusy(null)
    }
  }

  async function saveTarget() {
    setBusy("save")
    try {
      const body = {
        ...form,
        goods_types: csv(form.goods_types || "card"),
        category_ids: csvNumbers(form.category_ids),
        category_names: csv(form.category_names),
        keywords: csv(form.keywords),
        goods_keys: csv(form.goods_keys),
      }
      const path = editing ? `/shop-targets/${editing.id}` : "/shop-targets"
      const saved = await apiFetch<ShopTarget>(path, {
        method: editing ? "PUT" : "POST",
        body: JSON.stringify(body),
      })
      setSelectedID(saved.id)
      setFormOpen(false)
      toast.success(editing ? "店铺已更新" : "店铺已添加")
      targets.refetch()
      refresh()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "保存失败")
    } finally {
      setBusy(null)
    }
  }

  async function updateShopNotify(target: ShopTarget, enabled: boolean) {
    const next = await apiFetch<ShopTarget>(`/shop-targets/${target.id}`, {
      method: "PUT",
      body: JSON.stringify(shopTargetUpdateBody(target, { notify_enabled: enabled })),
    })
    targets.setData((targets.data ?? []).map((item) => (item.id === next.id ? { ...next, watch_rule_count: item.watch_rule_count } : item)))
    toast.success(enabled ? "店铺通知已开启" : "店铺通知已关闭")
    refresh()
  }

  async function saveBulkNotification() {
    const targetIDs = bulkForm.targetIDs.filter(Boolean)
    if (targetIDs.length === 0) {
      toast.error("请选择至少一个店铺")
      return
    }
    if (bulkForm.notifyMode === "keep" && !bulkForm.upsertRule) {
      toast.error("请选择要批量修改的通知配置")
      return
    }
    setBusy("bulk-notify")
    try {
      const rule: ShopWatchRuleInput = {
        name: bulkForm.ruleName.trim() || "批量关注规则",
        enabled: true,
        goods_keys: csv(bulkForm.goodsKeys),
        category_ids: [],
        category_names: csv(bulkForm.categoryNames),
        keywords: csv(bulkForm.keywords),
        events: bulkForm.events,
        stock_threshold: bulkForm.stockThreshold,
      }
      const body: ShopBulkNotificationInput = {
        target_ids: targetIDs,
        notify_enabled: bulkForm.notifyMode === "keep" ? undefined : bulkForm.notifyMode === "on",
        upsert_rule: bulkForm.upsertRule,
        replace_same_name: bulkForm.replaceSameName,
        rule,
      }
      const result = await apiFetch<ShopBulkNotificationResult>("/shop-targets/bulk-notification", {
        method: "POST",
        body: JSON.stringify(body),
      })
      targets.setData(result.targets)
      setBulkOpen(false)
      toast.success(`批量配置完成：店铺 ${result.updated_targets} 个，新增规则 ${result.created_rules} 条，更新规则 ${result.updated_rules} 条`)
      refresh()
      watchRules.refetch()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "批量配置通知失败")
    } finally {
      setBusy(null)
    }
  }

  async function saveGoodsSortAsDefault() {
    if (!selected || goodsSort === (selected.goods_sort || "category")) return
    setBusy(`goods-sort:${selected.id}`)
    try {
      const next = await apiFetch<ShopTarget>(`/shop-targets/${selected.id}`, {
        method: "PUT",
        body: JSON.stringify(shopTargetUpdateBody(selected, { goods_sort: goodsSort })),
      })
      targets.setData((targets.data ?? []).map((item) => (item.id === next.id ? { ...next, watch_rule_count: item.watch_rule_count } : item)))
      toast.success(`已保存默认排序：${goodsSortLabels[goodsSort]}`)
      refresh()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "保存默认排序失败")
    } finally {
      setBusy(null)
    }
  }

  function openWatchRules(target: ShopTarget) {
    setSelectedID(target.id)
    setWatchSeed(null)
    setWatchRulesOpen(true)
  }

  function watchGoods(row: ShopGoodsSnapshot) {
    setWatchSeed({ ...row, nonce: Date.now() })
    setWatchRulesOpen(true)
  }

  function handleWatchRulesChanged() {
    watchRules.refetch()
    targets.refetch()
  }

  async function testTarget(target: ShopTarget) {
    setBusy(`test:${target.id}`)
    try {
      const result = await apiFetch<ShopTestResult>(`/shop-targets/${target.id}/test`, { method: "POST" })
      toast.success(`连接正常：${result.info.name || shopDisplayName(target)}，分类 ${result.categories.length} 个`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "测试失败")
    } finally {
      setBusy(null)
    }
  }

  async function syncTarget(target: ShopTarget) {
    setBusy(`sync:${target.id}`)
    try {
      const result = await apiFetch<ShopSyncResult>(`/shop-targets/${target.id}/sync`, { method: "POST" })
      toast.success(`同步完成：${result.goods_count} 个商品，${result.changed_count} 个变化`)
      targets.refetch()
      goods.refetch()
      snapshotCategories.refetch()
      changes.refetch()
      monitorLogs.refetch()
      refresh()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "同步失败")
    } finally {
      setBusy(null)
    }
  }

  async function syncAllTargets() {
    setBusy("sync-all")
    try {
      const result = await apiFetch<ShopSyncAllResult>("/shop-targets/sync-all", { method: "POST" })
      if (result.failed > 0) {
        toast.warning(`同步完成：成功 ${result.success} 家，失败 ${result.failed} 家`)
      } else {
        toast.success(`同步完成：${result.success} 家店铺已更新`)
      }
      targets.refetch()
      goods.refetch()
      snapshotCategories.refetch()
      changes.refetch()
      monitorLogs.refetch()
      refresh()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "同步全部失败")
    } finally {
      setBusy(null)
    }
  }

  async function refreshGoodsStock(row: ShopGoodsSnapshot) {
    if (selectedID == null) return
    setBusy(`refresh-goods:${row.goods_key}`)
    try {
      const result = await apiFetch<ShopRefreshGoodsResult>(
        `/shop-targets/${selectedID}/goods/${encodeURIComponent(row.goods_key)}/refresh`,
        { method: "POST" },
      )
      if (goods.data) {
        goods.setData({
          ...goods.data,
          items: goods.data.items.map((item) => (item.goods_key === row.goods_key ? result.snapshot : item)),
        })
      } else {
        goods.refetch()
      }
      snapshotCategories.refetch()
      refresh()
      if (result.found) {
        toast.success(`库存已刷新：${result.snapshot.stock_count}`)
      } else {
        toast.warning("官方接口未找到该商品，已标记为消失")
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "刷新库存失败")
    } finally {
      setBusy(null)
    }
  }

  async function moveShopTarget(targetID: number, direction: -1 | 1) {
    const list = targets.data ?? []
    const index = list.findIndex((target) => target.id === targetID)
    const nextIndex = index + direction
    if (index < 0 || nextIndex < 0 || nextIndex >= list.length) return

    const reordered = [...list]
    const [item] = reordered.splice(index, 1)
    reordered.splice(nextIndex, 0, item)
    const ordered = reordered.map((target, itemIndex) => ({
      ...target,
      sort_order: itemIndex + 1,
    }))

    targets.setData(ordered)
    setBusy("reorder-shops")
    try {
      const saved = await apiFetch<ShopTarget[]>("/shop-targets/reorder", {
        method: "POST",
        body: JSON.stringify({
          items: ordered.map((target) => ({ id: target.id, sort_order: target.sort_order })),
        }),
      })
      targets.setData(saved)
      refresh()
    } catch (err) {
      targets.refetch()
      toast.error(err instanceof Error ? err.message : "排序保存失败")
    } finally {
      setBusy(null)
    }
  }

  async function deleteTarget(target: ShopTarget) {
    if (!window.confirm(`删除店铺监控「${shopDisplayName(target)}」？相关商品快照和变化日志也会删除。`)) return
    setBusy(`delete:${target.id}`)
    try {
      await apiFetch(`/shop-targets/${target.id}`, { method: "DELETE" })
      if (selectedID === target.id) setSelectedID(null)
      toast.success("店铺已删除")
      targets.refetch()
      refresh()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败")
    } finally {
      setBusy(null)
    }
  }

  function locateGoodsFromChange(row: ShopGoodsChangeLog) {
    const key = row.goods_key?.trim()
    if (!key) {
      toast.warning("这条变化日志没有商品 Key，无法定位商品")
      return
    }
    if (row.target_id && row.target_id !== selectedID) {
      setSelectedID(row.target_id)
    }
    setSelectedCategoryID(null)
    setGoodsStatus("all")
    setInStockOnly(false)
    setGoodsKeyword(key)
    setGoodsPage(1)
    setHighlightedGoodsKey(key)
    toast.info(`正在定位商品：${row.goods_name || key}`)
  }

  function clearGoodsSearch() {
    setGoodsKeyword("")
    setGoodsPage(1)
    setHighlightedGoodsKey(null)
  }

  return (
    <section className="space-y-4">
      <header className="overflow-hidden rounded-2xl border border-border bg-card">
        <div className="relative grid gap-4 p-4 sm:p-5 lg:grid-cols-[1.4fr_1fr]">
          <div className="absolute inset-0 bg-[radial-gradient(circle_at_20%_20%,rgba(20,184,166,0.16),transparent_30%),radial-gradient(circle_at_80%_0%,rgba(245,158,11,0.14),transparent_28%)]" />
          <div className="relative space-y-2">
            <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-[0.24em] text-muted-foreground">
              <Radar className="size-4 text-emerald-600" />
              {"Shop Radar"}
            </div>
            <h1 className="text-2xl font-semibold tracking-tight text-foreground">{"店铺监控"}</h1>
            <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
              {"监控链动小铺 / 鲸商城 Pro 店铺的商品库存、价格、补货、上架和消失状态；支持多店铺配置、定时采集、变化留痕和通知分发。"}
            </p>
          </div>
          <div className="relative flex flex-wrap items-end justify-start gap-2 lg:justify-end">
            <Button variant="outline" onClick={openBulkNotification} disabled={(targets.data?.length ?? 0) === 0 || busy === "bulk-notify"} className="gap-2">
              {busy === "bulk-notify" ? <Loader2 className="size-4 animate-spin" /> : <Bell className="size-4" />}
              {"批量通知"}
            </Button>
            <Button variant="outline" onClick={syncAllTargets} disabled={busy === "sync-all"} className="gap-2">
              {busy === "sync-all" ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
              {"同步全部"}
            </Button>
            <Button onClick={openCreate} className="gap-2">
              <Plus className="size-4" />
              {"添加店铺"}
            </Button>
          </div>
        </div>
      </header>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-6">
        <Summary label="店铺" value={summary.total} />
        <Summary label="启用" value={summary.enabled} />
        <Summary label="商品" value={summary.goods} />
        <Summary label="低库存" value={summary.low} warn={summary.low > 0} />
        <Summary label="最近变化" value={summary.changed} />
        <Summary label="失败" value={summary.failed} danger={summary.failed > 0} />
      </div>

      <div className="grid min-w-0 gap-4 lg:grid-cols-[360px_minmax(0,1fr)]">
        <div className="min-w-0 space-y-3">
          {(targets.data ?? []).map((target, index, list) => (
            <ShopCard
              key={target.id}
              target={target}
              active={target.id === selectedID}
              busy={busy}
              canMoveUp={index > 0}
              canMoveDown={index < list.length - 1}
              onSelect={() => setSelectedID(target.id)}
              onMoveUp={() => moveShopTarget(target.id, -1)}
              onMoveDown={() => moveShopTarget(target.id, 1)}
              onEdit={() => openEdit(target)}
              onTest={() => testTarget(target)}
              onSync={() => syncTarget(target)}
              onWatchRules={() => openWatchRules(target)}
              watchRuleCount={target.watch_rule_count}
              onDelete={() => deleteTarget(target)}
            />
          ))}
          {!targets.loading && (targets.data?.length ?? 0) === 0 ? (
            <Card className="border-dashed p-6 text-center text-sm text-muted-foreground">
              <PackageSearch className="mx-auto mb-2 size-8" />
              {"还没有店铺监控，添加一个店铺 URL 开始。"}
            </Card>
          ) : null}
        </div>

        <div className="min-w-0 space-y-4">
          <GoodsPanel
            target={selected}
            loading={goods.loading}
            categories={snapshotCategories.data ?? []}
            categoriesLoading={snapshotCategories.loading}
            rows={goods.data?.items ?? []}
            page={goods.data?.page ?? goodsPage}
            pages={goods.data?.pages ?? 1}
            total={goods.data?.total ?? 0}
            selectedCategoryID={selectedCategoryID}
            status={goodsStatus}
            inStockOnly={inStockOnly}
            sort={goodsSort}
            savingDefaultSort={selected ? busy === `goods-sort:${selected.id}` : false}
            keyword={goodsKeyword}
            refreshingKey={busy?.startsWith("refresh-goods:") ? busy.slice("refresh-goods:".length) : null}
            highlightedGoodsKey={highlightedGoodsKey}
            watchedGoodsKeys={watchedGoodsKeys}
            rowRefs={goodsRowRefs}
            onCategory={setSelectedCategoryID}
            onStatus={setGoodsStatus}
            onInStockOnly={setInStockOnly}
            onSort={setGoodsSort}
            onSaveDefaultSort={saveGoodsSortAsDefault}
            onKeyword={setGoodsKeyword}
            onClearSearch={clearGoodsSearch}
            onRefreshGoods={refreshGoodsStock}
            onWatchGoods={watchGoods}
            onPage={setGoodsPage}
          />
          <ChangePanel
            loading={changes.loading}
            rows={changes.data?.items ?? []}
            page={changes.data?.page ?? changesPage}
            pages={changes.data?.pages ?? 1}
            total={changes.data?.total ?? 0}
            onLocateGoods={locateGoodsFromChange}
            onPage={setChangesPage}
          />
          <MonitorLogPanel
            loading={monitorLogs.loading}
            rows={monitorLogs.data?.items ?? []}
            page={monitorLogs.data?.page ?? logsPage}
            pages={monitorLogs.data?.pages ?? 1}
            total={monitorLogs.data?.total ?? 0}
            onPage={setLogsPage}
          />
        </div>
      </div>

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="max-h-[calc(100vh-2rem)] max-w-3xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? "编辑店铺监控" : "添加店铺监控"}</DialogTitle>
            <DialogDescription>
              {"支持全店、分类/关键词、指定商品 key 三种范围。多个值用逗号或换行分隔。"}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="名称">
              <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="全网最低Team" />
            </Field>
            <Field label="店铺 URL">
              <div className="flex gap-2">
                <Input value={form.site_url} onChange={(e) => setForm({ ...form, site_url: e.target.value })} placeholder="https://pay.ldxp.cn/shop/7FCVUA4X" />
                <Button type="button" variant="outline" onClick={parseURL} disabled={busy === "parse"}>
                  {busy === "parse" ? <Loader2 className="size-4 animate-spin" /> : "解析"}
                </Button>
              </div>
            </Field>
            <Field label="Base URL">
              <Input value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} placeholder="https://pay.ldxp.cn" />
            </Field>
            <Field label="Token">
              <Input value={form.token} onChange={(e) => setForm({ ...form, token: e.target.value })} placeholder="7FCVUA4X" />
            </Field>
            <Field label="监控范围">
              <Select value={form.scope_mode} onValueChange={(value) => setForm({ ...form, scope_mode: value as ShopScopeMode })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全店</SelectItem>
                  <SelectItem value="filters">分类 / 关键词</SelectItem>
                  <SelectItem value="goods_keys">指定商品 Key</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="商品类型">
              <Input value={form.goods_types} onChange={(e) => setForm({ ...form, goods_types: e.target.value })} placeholder="card" />
            </Field>
            <Field label="分类 ID">
              <Input value={form.category_ids} onChange={(e) => setForm({ ...form, category_ids: e.target.value })} placeholder="112879, 112880" />
            </Field>
            <Field label="分类名称">
              <Input value={form.category_names} onChange={(e) => setForm({ ...form, category_names: e.target.value })} placeholder="K12, GPTpro" />
            </Field>
            <Field label="关键词">
              <Input value={form.keywords} onChange={(e) => setForm({ ...form, keywords: e.target.value })} placeholder="K12, GPTpro" />
            </Field>
            <Field label="商品 Key">
              <Input value={form.goods_keys} onChange={(e) => setForm({ ...form, goods_keys: e.target.value })} placeholder="96tin3, 7togvs" />
            </Field>
            <Field label="低库存阈值">
              <Input type="number" value={form.stock_threshold} onChange={(e) => setForm({ ...form, stock_threshold: Number(e.target.value) || 0 })} />
            </Field>
            <Field label="默认商品排序">
              <Select value={form.goods_sort} onValueChange={(value) => setForm({ ...form, goods_sort: value as ShopGoodsSort })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {Object.entries(goodsSortLabels).map(([value, label]) => (
                    <SelectItem key={value} value={value}>{label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
          <div className="grid gap-2 rounded-lg border border-border bg-muted/20 p-3 sm:grid-cols-3">
            <Check label="启用监控" checked={form.monitor_enabled} onChange={(v) => setForm({ ...form, monitor_enabled: v })} />
            <Check label="启用通知" checked={form.notify_enabled} onChange={(v) => setForm({ ...form, notify_enabled: v })} />
            <Check label="价格变化" checked={form.price_change_enabled} onChange={(v) => setForm({ ...form, price_change_enabled: v })} />
            <Check label="库存变化" checked={form.stock_change_enabled} onChange={(v) => setForm({ ...form, stock_change_enabled: v })} />
            <Check label="低库存" checked={form.low_stock_enabled} onChange={(v) => setForm({ ...form, low_stock_enabled: v })} />
            <Check label="补货" checked={form.restock_enabled} onChange={(v) => setForm({ ...form, restock_enabled: v })} />
            <Check label="新增商品" checked={form.new_goods_enabled} onChange={(v) => setForm({ ...form, new_goods_enabled: v })} />
            <Check label="商品消失" checked={form.removed_goods_enabled} onChange={(v) => setForm({ ...form, removed_goods_enabled: v })} />
            <Check label="使用代理" checked={form.proxy_enabled} onChange={(v) => setForm({ ...form, proxy_enabled: v })} />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setFormOpen(false)}>取消</Button>
            <Button onClick={saveTarget} disabled={busy === "save"}>
              {busy === "save" ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
              {"保存"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <ShopWatchRulesDrawer
        open={watchRulesOpen}
        onOpenChange={setWatchRulesOpen}
        target={selected}
        categories={snapshotCategories.data ?? []}
        rules={selectedWatchRules}
        loading={watchRules.loading}
        seed={watchSeed}
        onRulesChanged={handleWatchRulesChanged}
        onToggleNotify={async (enabled) => {
          if (!selected) return
          await updateShopNotify(selected, enabled)
        }}
      />
      <Dialog open={bulkOpen} onOpenChange={setBulkOpen}>
        <DialogContent className="max-h-[calc(100vh-2rem)] max-w-3xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{"批量通知配置"}</DialogTitle>
            <DialogDescription>
              {"一次性修改多个店铺的通知开关，并可批量创建或更新同名关注规则。"}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <Label className="text-xs text-muted-foreground">{"选择店铺"}</Label>
                <div className="flex gap-1">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setBulkForm((prev) => ({ ...prev, targetIDs: (targets.data ?? []).map((target) => target.id) }))}
                  >
                    {"全选"}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setBulkForm((prev) => ({ ...prev, targetIDs: [] }))}
                  >
                    {"清空"}
                  </Button>
                </div>
              </div>
              <div className="grid max-h-56 gap-2 overflow-y-auto rounded-lg border border-border p-2 sm:grid-cols-2">
                {(targets.data ?? []).map((target) => {
                  const checked = bulkForm.targetIDs.includes(target.id)
                  return (
                    <button
                      key={target.id}
                      type="button"
                      onClick={() =>
                        setBulkForm((prev) => ({
                          ...prev,
                          targetIDs: checked ? prev.targetIDs.filter((id) => id !== target.id) : [...prev.targetIDs, target.id],
                        }))
                      }
                      className={cn(
                        "rounded-md border px-3 py-2 text-left text-sm transition",
                        checked ? "border-emerald-500 bg-emerald-500/10 text-emerald-800" : "border-border hover:border-foreground/40",
                      )}
                    >
                      <div className="truncate font-medium">{shopDisplayName(target)}</div>
                      <div className="mt-0.5 text-[11px] text-muted-foreground">{target.notify_enabled ? "通知已开" : "通知关闭"} · 规则 {target.watch_rule_count ?? 0}</div>
                    </button>
                  )
                })}
              </div>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="店铺通知开关">
                <Select value={bulkForm.notifyMode} onValueChange={(value) => setBulkForm({ ...bulkForm, notifyMode: value as BulkNotificationForm["notifyMode"] })}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="on">全部开启</SelectItem>
                    <SelectItem value="off">全部关闭</SelectItem>
                    <SelectItem value="keep">保持不变</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="同名规则处理">
                <Select value={bulkForm.replaceSameName ? "replace" : "skip"} onValueChange={(value) => setBulkForm({ ...bulkForm, replaceSameName: value === "replace" })}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="replace">更新同名规则</SelectItem>
                    <SelectItem value="skip">已有同名则跳过</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>
            <div className="rounded-lg border border-border bg-muted/20 p-3">
              <Check label="批量创建/更新关注规则" checked={bulkForm.upsertRule} onChange={(v) => setBulkForm({ ...bulkForm, upsertRule: v })} />
              {bulkForm.upsertRule ? (
                <div className="mt-3 grid gap-3 sm:grid-cols-2">
                  <Field label="规则名称">
                    <Input value={bulkForm.ruleName} onChange={(e) => setBulkForm({ ...bulkForm, ruleName: e.target.value })} />
                  </Field>
                  <Field label="低库存阈值">
                    <Input type="number" value={bulkForm.stockThreshold} onChange={(e) => setBulkForm({ ...bulkForm, stockThreshold: Number(e.target.value) || 0 })} />
                  </Field>
                  <Field label="分类名称">
                    <Input value={bulkForm.categoryNames} onChange={(e) => setBulkForm({ ...bulkForm, categoryNames: e.target.value })} placeholder="分类名，逗号或换行分隔" />
                  </Field>
                  <Field label="商品 Key">
                    <Input value={bulkForm.goodsKeys} onChange={(e) => setBulkForm({ ...bulkForm, goodsKeys: e.target.value })} placeholder="商品 key，逗号或换行分隔" />
                  </Field>
                  <Field label="关键词">
                    <Input value={bulkForm.keywords} onChange={(e) => setBulkForm({ ...bulkForm, keywords: e.target.value })} placeholder="关键词，逗号或换行分隔" />
                  </Field>
                  <div className="space-y-1.5">
                    <Label className="text-xs text-muted-foreground">{"通知事件"}</Label>
                    <div className="flex flex-wrap gap-2">
                      {(Object.entries(eventLabels) as Array<[ShopGoodsChangeEvent, string]>).map(([event, label]) => {
                        const checked = bulkForm.events.includes(event)
                        return (
                          <button
                            key={event}
                            type="button"
                            onClick={() =>
                              setBulkForm((prev) => ({
                                ...prev,
                                events: checked ? prev.events.filter((item) => item !== event) : [...prev.events, event],
                              }))
                            }
                            className={cn(
                              "rounded-full border px-2.5 py-1 text-xs transition",
                              checked ? "border-emerald-500 bg-emerald-500/10 text-emerald-800" : "border-border text-muted-foreground hover:text-foreground",
                            )}
                          >
                            {label}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                </div>
              ) : null}
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setBulkOpen(false)}>取消</Button>
            <Button onClick={saveBulkNotification} disabled={busy === "bulk-notify"}>
              {busy === "bulk-notify" ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
              {"批量保存"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </section>
  )
}

function Summary({ label, value, warn, danger }: { label: string; value: number; warn?: boolean; danger?: boolean }) {
  return (
    <Card className={cn("p-3", warn && "border-warning/40 bg-warning/5", danger && "border-danger/40 bg-danger/5")}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
    </Card>
  )
}

function ShopCard(props: {
  target: ShopTarget
  active: boolean
  busy: string | null
  canMoveUp: boolean
  canMoveDown: boolean
  onSelect: () => void
  onMoveUp: () => void
  onMoveDown: () => void
  onEdit: () => void
  onTest: () => void
  onSync: () => void
  onWatchRules: () => void
  watchRuleCount?: number
  onDelete: () => void
}) {
  const { target, active, busy } = props
  const displayName = shopDisplayName(target)
  return (
    <Card className={cn("cursor-pointer p-3 transition hover:border-foreground/30", active && "border-foreground shadow-sm")} onClick={props.onSelect}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            {target.last_error ? <AlertTriangle className="size-4 text-danger" /> : <Store className="size-4 text-emerald-600" />}
            <h2 className="truncate text-sm font-semibold">{displayName}</h2>
          </div>
          <p className="mt-1 truncate text-xs text-muted-foreground">{target.site_url}</p>
        </div>
        <span className={cn("rounded-full px-2 py-0.5 text-[10px]", target.monitor_enabled ? "bg-success/10 text-success" : "bg-muted text-muted-foreground")}>
          {target.monitor_enabled ? "启用" : "暂停"}
        </span>
      </div>
      <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
        <Mini label="商品" value={target.last_goods_count} />
        <Mini label="低库存" value={target.last_low_stock_goods} />
        <Mini label="变化" value={target.last_changed_count} />
      </div>
      <div className="mt-2 flex items-center justify-between gap-2 rounded-md bg-muted/30 px-2 py-1.5 text-[11px] text-muted-foreground">
        <span className={cn("inline-flex items-center gap-1", target.notify_enabled && "text-emerald-700")}>
          <Bell className="size-3.5" />
          {target.notify_enabled ? "通知开启" : "通知关闭"}
        </span>
        <span>{`关注规则 ${props.watchRuleCount ?? 0}`}</span>
      </div>
      {target.last_error ? <p className="mt-2 line-clamp-2 text-xs text-danger">{target.last_error}</p> : null}
      <div className="mt-3 flex items-center justify-between gap-2">
        <span className="text-[11px] text-muted-foreground">上次同步 {relativeTime(target.last_sync_at)}</span>
        <div className="flex gap-1" onClick={(e) => e.stopPropagation()}>
          <Button asChild variant="outline" size="icon" className="size-7">
            <a href={target.site_url} target="_blank" rel="noreferrer" title="打开店铺">
              <ExternalLink className="size-3.5" />
            </a>
          </Button>
          <Button variant="outline" size="icon" className="size-7" onClick={props.onTest} disabled={busy === `test:${target.id}`}>
            {busy === `test:${target.id}` ? <Loader2 className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}
          </Button>
          <Button variant="outline" size="icon" className="size-7" onClick={props.onMoveUp} disabled={!props.canMoveUp || busy === "reorder-shops"}>
            <ArrowUp className="size-3.5" />
          </Button>
          <Button variant="outline" size="icon" className="size-7" onClick={props.onMoveDown} disabled={!props.canMoveDown || busy === "reorder-shops"}>
            <ArrowDown className="size-3.5" />
          </Button>
          <Button variant="outline" size="icon" className="size-7" onClick={props.onSync} disabled={busy === `sync:${target.id}`}>
            {busy === `sync:${target.id}` ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
          </Button>
          <Button variant="outline" size="icon" className={cn("size-7", target.notify_enabled && "border-emerald-500/40 text-emerald-700")} onClick={props.onWatchRules}>
            <Bell className="size-3.5" />
          </Button>
          <Button variant="outline" size="icon" className="size-7" onClick={props.onEdit}><Pencil className="size-3.5" /></Button>
          <Button variant="outline" size="icon" className="size-7" onClick={props.onDelete} disabled={busy === `delete:${target.id}`}><Trash2 className="size-3.5" /></Button>
        </div>
      </div>
    </Card>
  )
}

function Mini({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-md bg-muted/40 px-2 py-1">
      <div className="text-[10px] text-muted-foreground">{label}</div>
      <div className="font-semibold tabular-nums">{value}</div>
    </div>
  )
}

function GoodsPanel({
  target,
  loading,
  categories,
  categoriesLoading,
  rows,
  page,
  pages,
  total,
  selectedCategoryID,
  status,
  inStockOnly,
  sort,
  savingDefaultSort,
  keyword,
  refreshingKey,
  highlightedGoodsKey,
  watchedGoodsKeys,
  rowRefs,
  onCategory,
  onStatus,
  onInStockOnly,
  onSort,
  onSaveDefaultSort,
  onKeyword,
  onClearSearch,
  onRefreshGoods,
  onWatchGoods,
  onPage,
}: {
  target: ShopTarget | null
  loading: boolean
  categories: ShopSnapshotCategory[]
  categoriesLoading: boolean
  rows: ShopGoodsSnapshot[]
  page: number
  pages: number
  total: number
  selectedCategoryID: number | null
  status: GoodsStatusFilter
  inStockOnly: boolean
  sort: ShopGoodsSort
  savingDefaultSort: boolean
  keyword: string
  refreshingKey: string | null
  highlightedGoodsKey: string | null
  watchedGoodsKeys: Set<string>
  rowRefs: React.MutableRefObject<Record<string, HTMLTableRowElement | null>>
  onCategory: (categoryID: number | null) => void
  onStatus: (status: GoodsStatusFilter) => void
  onInStockOnly: (enabled: boolean) => void
  onSort: (sort: ShopGoodsSort) => void
  onSaveDefaultSort: () => void
  onKeyword: (keyword: string) => void
  onClearSearch: () => void
  onRefreshGoods: (row: ShopGoodsSnapshot) => void
  onWatchGoods: (row: ShopGoodsSnapshot) => void
  onPage: (page: number) => void
}) {
  const allCount = categories.reduce((sum, category) => sum + category.goods_count, 0)
  const activeFilters = selectedCategoryID !== null || status !== "all" || inStockOnly || sort !== "category" || keyword.trim() !== ""
  const selectedCategory = categories.find((category) => category.category_id === selectedCategoryID)
  const selectedCategoryName = selectedCategory ? categoryLabel(selectedCategory) : "全部分类"
  const displayName = shopDisplayName(target)
  const canClearSearch = keyword.trim() !== ""

  return (
    <Card className="min-w-0 overflow-hidden">
      <div className="flex items-center justify-between border-b border-border p-3">
        <div>
          <h2 className="text-sm font-semibold">商品快照</h2>
          <p className="text-xs text-muted-foreground">
            {target ? `${displayName} 当前筛选 ${total} 个，分类视图共 ${allCount || target.last_goods_count || 0} 个` : "选择一个店铺查看商品"}
          </p>
        </div>
        {loading ? <Loader2 className="size-4 animate-spin text-muted-foreground" /> : null}
      </div>
      <div className="space-y-3 border-b border-border bg-muted/10 p-3">
        <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <ListFilter className="size-3.5" />
          <span>按分类查看</span>
          {categoriesLoading ? <Loader2 className="size-3 animate-spin" /> : null}
        </div>
        <div className="flex min-w-0 flex-wrap gap-2">
          <CategoryButton
            active={selectedCategoryID === null}
            label="全部"
            count={allCount || target?.last_goods_count || 0}
            low={categories.reduce((sum, category) => sum + category.low_stock_count, 0)}
            removed={categories.reduce((sum, category) => sum + category.removed_count, 0)}
            onClick={() => onCategory(null)}
          />
          {categories.map((category) => (
            <CategoryButton
              key={`${category.category_id}:${category.category_name}`}
              active={selectedCategoryID === category.category_id}
              label={categoryLabel(category)}
              count={category.goods_count}
              low={category.low_stock_count}
              removed={category.removed_count}
              onClick={() => onCategory(category.category_id)}
            />
          ))}
          {!categoriesLoading && target && categories.length === 0 ? (
            <span className="rounded-full border border-dashed border-border px-3 py-1.5 text-xs text-muted-foreground">同步后会显示分类</span>
          ) : null}
        </div>
        <div className="grid min-w-0 gap-2 sm:grid-cols-[minmax(140px,180px)_minmax(220px,280px)_auto_minmax(220px,1fr)]">
          <Select value={status} onValueChange={(value) => onStatus(value as GoodsStatusFilter)}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {Object.entries(goodsStatusLabels).map(([value, label]) => (
                <SelectItem key={value} value={value}>{label}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex min-w-0 gap-2">
            <Select value={sort} onValueChange={(value) => onSort(value as ShopGoodsSort)}>
              <SelectTrigger className="min-w-0 flex-1">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(goodsSortLabels).map(([value, label]) => (
                  <SelectItem key={value} value={value}>{label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            {target && sort !== (target.goods_sort || "category") ? (
              <Button type="button" variant="outline" onClick={onSaveDefaultSort} disabled={savingDefaultSort} className="shrink-0">
                {savingDefaultSort ? <Loader2 className="mr-1 size-3 animate-spin" /> : null}
                {"设为默认"}
              </Button>
            ) : null}
          </div>
          <Button
            type="button"
            variant={inStockOnly ? "default" : "outline"}
            onClick={() => onInStockOnly(!inStockOnly)}
            className="gap-2"
          >
            <CheckCircle2 className="size-4" />
            {"有库存"}
          </Button>
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={keyword}
              onChange={(event) => onKeyword(event.target.value)}
              className="pl-9 pr-10"
              placeholder={`在 ${selectedCategoryName} 中搜索商品名、Key 或分类`}
            />
            {canClearSearch ? (
              <button
                type="button"
                onClick={onClearSearch}
                className="absolute right-2 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
                aria-label="清除搜索"
                title="清除搜索"
              >
                <X className="size-4" />
              </button>
            ) : null}
          </div>
        </div>
      </div>
      <div className="overflow-x-auto">
        <Table className="min-w-[980px] table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[38%]">商品</TableHead>
              <TableHead className="w-[13%]">分类</TableHead>
              <TableHead className="w-[10%]">价格</TableHead>
              <TableHead className="w-[10%]">库存</TableHead>
              <TableHead className="w-[9%]">状态</TableHead>
              <TableHead className="w-[11%]">最近出现</TableHead>
              <TableHead className="w-[9%]">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => {
              const refreshing = refreshingKey === row.goods_key
              const canBuy = !row.removed_at && row.stock_count > 0 && row.link
              const watched = watchedGoodsKeys.has(row.goods_key)
              return (
                <TableRow
                  key={row.id}
                  ref={(element) => {
                    rowRefs.current[row.goods_key] = element
                  }}
                  className={cn(
                    "transition-colors",
                    row.removed_at && "opacity-50",
                    highlightedGoodsKey === row.goods_key && "bg-warning/20 ring-1 ring-inset ring-warning/60",
                  )}
                >
                  <TableCell className="min-w-0">
                    <div className="flex min-w-0 items-center gap-2">
                      <button
                        type="button"
                        onClick={(event) => {
                          event.stopPropagation()
                          onWatchGoods(row)
                        }}
                        className={cn(
                          "shrink-0 rounded-md p-1 transition hover:bg-muted",
                          watched ? "text-amber-500" : "text-muted-foreground",
                        )}
                        title={watched ? "已被关注规则命中，点击编辑关注" : "重点关注该商品"}
                      >
                        <Star className={cn("size-4", watched && "fill-amber-400")} />
                      </button>
                      <div className="min-w-0 truncate font-medium" title={row.name}>{row.name}</div>
                    </div>
                    <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
                      <span className="shrink-0">{row.goods_key}</span>
                      {row.link ? <a href={row.link} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:text-foreground">官方页 <ExternalLink className="size-3" /></a> : null}
                    </div>
                  </TableCell>
                  <TableCell className="truncate" title={row.category_name || undefined}>{row.category_name || "-"}</TableCell>
                  <TableCell>{money(row.price)}</TableCell>
                  <TableCell>
                    <button
                      type="button"
                      onClick={() => onRefreshGoods(row)}
                      disabled={refreshing}
                      className={cn(
                        "inline-flex items-center gap-1 rounded-md px-2 py-1 font-semibold tabular-nums transition hover:bg-muted disabled:cursor-wait disabled:opacity-70",
                        row.stock_count <= 1 && "text-warning",
                      )}
                      title="点击刷新该商品库存"
                    >
                      {refreshing ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3 opacity-60" />}
                      {row.stock_count}
                    </button>
                  </TableCell>
                  <TableCell>{row.removed_at ? "已消失" : "在线"}</TableCell>
                  <TableCell>{relativeTime(row.last_seen_at)}</TableCell>
                  <TableCell>
                    {canBuy ? (
                      <Button asChild size="sm" variant="outline" className="h-7 gap-1 px-2">
                        <a href={row.link} target="_blank" rel="noreferrer">
                          <ShoppingCart className="size-3.5" />
                          购买
                        </a>
                      </Button>
                    ) : (
                      <Button size="sm" variant="outline" className="h-7 gap-1 px-2" disabled>
                        <ShoppingCart className="size-3.5" />
                        库存空
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="h-24 text-center text-sm text-muted-foreground">
                  {activeFilters ? "当前分类或筛选条件下没有商品。" : "暂无商品快照，先手动同步一次。"}
                </TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>
      <Pager page={page} pages={pages} onPage={onPage} />
    </Card>
  )
}

function categoryLabel(category: ShopSnapshotCategory) {
  return category.category_name || "未分类"
}

function CategoryButton({
  active,
  label,
  count,
  low,
  removed,
  onClick,
}: {
  active: boolean
  label: string
  count: number
  low: number
  removed: number
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "group min-w-28 max-w-48 shrink-0 rounded-xl border px-3 py-2 text-left transition",
        active ? "border-foreground bg-foreground text-background shadow-sm" : "border-border bg-card hover:border-foreground/40",
      )}
      title={label}
    >
      <div className="flex min-w-0 items-center gap-2">
        <span className="block min-w-0 flex-1 truncate text-xs font-medium">{label}</span>
        <span className={cn("rounded-full px-1.5 py-0.5 text-[10px] tabular-nums", active ? "bg-background/20" : "bg-muted")}>{count}</span>
      </div>
      <div className={cn("mt-1 flex min-w-0 gap-2 text-[10px]", active ? "text-background/75" : "text-muted-foreground")}>
        <span>低库存 {low}</span>
        <span>消失 {removed}</span>
      </div>
    </button>
  )
}

function ChangePanel({
  loading,
  rows,
  page,
  pages,
  total,
  onLocateGoods,
  onPage,
}: {
  loading: boolean
  rows: ShopGoodsChangeLog[]
  page: number
  pages: number
  total: number
  onLocateGoods: (row: ShopGoodsChangeLog) => void
  onPage: (page: number) => void
}) {
  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-border p-3">
        <div>
          <h2 className="text-sm font-semibold">变化日志</h2>
          <p className="text-xs text-muted-foreground">价格、库存、补货、上下架和失败记录，共 {total} 条。</p>
        </div>
        {loading ? <Loader2 className="size-4 animate-spin text-muted-foreground" /> : null}
      </div>
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>事件</TableHead>
              <TableHead>商品</TableHead>
              <TableHead>变化</TableHead>
              <TableHead>时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow
                key={row.id}
                className={cn("cursor-pointer transition-colors hover:bg-muted/60", !row.goods_key && "cursor-default")}
                onClick={() => row.goods_key && onLocateGoods(row)}
                title={row.goods_key ? "点击定位到商品快照" : undefined}
              >
                <TableCell><span className="rounded-full bg-muted px-2 py-0.5 text-xs">{eventLabels[row.event as ShopGoodsChangeEvent] ?? row.event}</span></TableCell>
                <TableCell className="min-w-52">{row.goods_name || row.goods_key || "-"}</TableCell>
                <TableCell className="min-w-80 text-sm">{row.summary || `${row.old_value ?? ""} -> ${row.new_value ?? ""}`}</TableCell>
                <TableCell>{relativeTime(row.changed_at)}</TableCell>
              </TableRow>
            ))}
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="h-24 text-center text-sm text-muted-foreground">暂无变化记录。</TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>
      <Pager page={page} pages={pages} onPage={onPage} />
    </Card>
  )
}

function MonitorLogPanel({
  loading,
  rows,
  page,
  pages,
  total,
  onPage,
}: {
  loading: boolean
  rows: ShopMonitorLog[]
  page: number
  pages: number
  total: number
  onPage: (page: number) => void
}) {
  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-border p-3">
        <div>
          <h2 className="text-sm font-semibold">运行日志</h2>
          <p className="text-xs text-muted-foreground">每次测试或同步的成功、失败、耗时和影响范围，共 {total} 条。</p>
        </div>
        {loading ? <Loader2 className="size-4 animate-spin text-muted-foreground" /> : null}
      </div>
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>结果</TableHead>
              <TableHead>商品</TableHead>
              <TableHead>变化</TableHead>
              <TableHead>耗时</TableHead>
              <TableHead>时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={row.id}>
                <TableCell>
                  <span className={cn("rounded-full px-2 py-0.5 text-xs", row.success ? "bg-success/10 text-success" : "bg-danger/10 text-danger")}>
                    {row.success ? "成功" : "失败"}
                  </span>
                  {!row.success && row.error_message ? <p className="mt-1 max-w-96 truncate text-xs text-danger">{row.error_message}</p> : null}
                </TableCell>
                <TableCell>{row.goods_count}</TableCell>
                <TableCell>{row.changed_count}</TableCell>
                <TableCell>{row.duration_ms}ms</TableCell>
                <TableCell>{relativeTime(row.started_at)}</TableCell>
              </TableRow>
            ))}
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="h-20 text-center text-sm text-muted-foreground">暂无运行日志。</TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>
      <Pager page={page} pages={pages} onPage={onPage} />
    </Card>
  )
}

function Pager({ page, pages, onPage }: { page: number; pages: number; onPage: (page: number) => void }) {
  if (pages <= 1) return null
  return (
    <div className="flex items-center justify-end gap-2 border-t border-border p-3 text-xs text-muted-foreground">
      <span>第 {page} / {pages} 页</span>
      <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)}>上一页</Button>
      <Button variant="outline" size="sm" disabled={page >= pages} onClick={() => onPage(page + 1)}>下一页</Button>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

function Check({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="size-4 accent-foreground" />
      {label}
    </label>
  )
}
