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
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { GlobalShopWatchRulesDrawer, type GlobalShopWatchSeed } from "@/components/monitor/shop-watch-rules-drawer"
import { SearchHistoryInput } from "@/components/search-history-input"
import { apiFetch } from "@/lib/api"
import { useGlobalShopWatchRules, useShopChangeLogs, useShopGoods, useShopMonitorLogs, useShopSnapshotCategories, useShopTargets } from "@/lib/queries"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { money, relativeTime } from "@/lib/format"
import {
  readAllShopGoodsSearchHistory,
  readShopsGoodsPreferences,
  rememberAllShopGoodsSearchQuery,
  type ShopGoodsStatusFilter,
  writeShopsGoodsPreferences,
} from "@/lib/shop-goods-preferences"
import { cn } from "@/lib/utils"
import type {
  ShopGoodsChangeEvent,
  ShopGoodsChangeLog,
  ShopGoodsSort,
  ShopGoodsSnapshot,
  ShopGoodsStatus,
  ShopMonitorLog,
  ShopRefreshGoodsResult,
  ShopScopeMode,
  ShopSnapshotCategory,
  ShopSyncAllResult,
  ShopSyncJob,
  ShopSyncJobStartResult,
  ShopTarget,
  ShopTestResult,
} from "@/lib/api-types"

type ShopForm = {
  name: string
  site_url: string
  platform: "ldxp"
  base_url: string
  token: string
  monitor_enabled: boolean
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
  sort_order: string
  goods_sort: ShopGoodsSort
}

const emptyForm: ShopForm = {
  name: "",
  site_url: "",
  platform: "ldxp",
  base_url: "",
  token: "",
  monitor_enabled: true,
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
  sort_order: "",
  goods_sort: "category",
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

type GoodsStatusFilter = ShopGoodsStatusFilter

function isActiveSyncJob(job: ShopSyncJob) {
  return job.status === "queued" || job.status === "running"
}

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

const emptyShopTargets: ShopTarget[] = []

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

function normalizeTextFilter(value: string) {
  return value.trim()
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
    sort_order: String(target.sort_order),
    goods_sort: target.goods_sort || "category",
  }
}

function shopDisplayName(target: ShopTarget | null) {
  if (!target) return ""
  return target.name?.trim() || target.last_shop_name?.trim() || `店铺 #${target.id}`
}

function clampListPosition(value: number, max: number) {
  if (!Number.isFinite(value)) return 1
  const normalized = Math.trunc(value)
  if (normalized < 1) return 1
  if (normalized > max) return max
  return normalized
}

function normalizeListPositionInput(value: string, max: number) {
  return String(clampListPosition(Number(value), max))
}

export default function ShopsPage() {
  const targets = useShopTargets()
  const refresh = useTriggerRefresh()
  const [initialGoodsPreferences] = useState(readShopsGoodsPreferences)
  const initialGoodsKeyword = normalizeTextFilter(initialGoodsPreferences.keyword)
  const initialGoodsExcludeKeyword = normalizeTextFilter(initialGoodsPreferences.excludeKeyword)
  const [selectedID, setSelectedID] = useState<number | null>(initialGoodsPreferences.selectedTargetID)
  const [editing, setEditing] = useState<ShopTarget | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<ShopForm>(emptyForm)
  const [busy, setBusy] = useState<string | null>(null)
  const [goodsPage, setGoodsPage] = useState(1)
  const [changesPage, setChangesPage] = useState(1)
  const [logsPage, setLogsPage] = useState(1)
  const [selectedCategoryID, setSelectedCategoryID] = useState<number | null>(() => {
    const targetID = initialGoodsPreferences.selectedTargetID
    return targetID == null ? null : initialGoodsPreferences.categoryIDs[String(targetID)] ?? null
  })
  const [goodsStatus, setGoodsStatus] = useState<GoodsStatusFilter>(initialGoodsPreferences.status)
  const [inStockOnly, setInStockOnly] = useState(initialGoodsPreferences.inStockOnly)
  const [goodsSort, setGoodsSort] = useState<ShopGoodsSort>(() => {
    const targetID = initialGoodsPreferences.selectedTargetID
    return targetID == null ? "category" : initialGoodsPreferences.sorts[String(targetID)] ?? "category"
  })
  const [goodsKeyword, setGoodsKeyword] = useState(initialGoodsKeyword)
  const [goodsExcludeKeyword, setGoodsExcludeKeyword] = useState(initialGoodsExcludeKeyword)
  const [appliedGoodsKeyword, setAppliedGoodsKeyword] = useState(initialGoodsKeyword)
  const [appliedGoodsExcludeKeyword, setAppliedGoodsExcludeKeyword] = useState(initialGoodsExcludeKeyword)
  const [goodsSearchHistory, setGoodsSearchHistory] = useState(readAllShopGoodsSearchHistory)
  const [categoryIDs, setCategoryIDs] = useState(initialGoodsPreferences.categoryIDs)
  const [sorts, setSorts] = useState(initialGoodsPreferences.sorts)
  const [highlightedGoodsKey, setHighlightedGoodsKey] = useState<string | null>(null)
  const [globalWatchRulesOpen, setGlobalWatchRulesOpen] = useState(false)
  const [globalWatchSeed, setGlobalWatchSeed] = useState<GlobalShopWatchSeed | null>(null)
  const [syncJobs, setSyncJobs] = useState<Record<number, ShopSyncJob>>({})
  const bulkSyncJobIDsRef = useRef<Set<number> | null>(null)
  const goodsRowRefs = useRef<Record<string, HTMLTableRowElement | null>>({})
  const pendingGoodsLookupRef = useRef<{ targetID: number; goodsKey: string } | null>(null)
  const shopListScrollRef = useRef<HTMLDivElement | null>(null)
  const shopListScrollTopRef = useRef(initialGoodsPreferences.shopListScrollTop)
  const shopListScrollSaveTimerRef = useRef<number | null>(null)
  const goodsFilters = useMemo(
    () => ({
      category_id: selectedCategoryID ?? undefined,
      status: inStockOnly ? "in_stock" as ShopGoodsStatus : goodsStatus,
      keyword: appliedGoodsKeyword,
      exclude_keyword: appliedGoodsExcludeKeyword,
      sort: goodsSort,
    }),
    [appliedGoodsExcludeKeyword, appliedGoodsKeyword, goodsSort, goodsStatus, inStockOnly, selectedCategoryID],
  )
  const goodsSearchDirty = normalizeTextFilter(goodsKeyword) !== appliedGoodsKeyword
    || normalizeTextFilter(goodsExcludeKeyword) !== appliedGoodsExcludeKeyword
  const goodsSearchActive = appliedGoodsKeyword.trim() !== "" || appliedGoodsExcludeKeyword.trim() !== ""
  const goods = useShopGoods(selectedID, goodsPage, 25, goodsFilters, true)
  const snapshotCategories = useShopSnapshotCategories(selectedID)
  const globalWatchRules = useGlobalShopWatchRules()
  const changes = useShopChangeLogs(selectedID, changesPage, 20)
  const monitorLogs = useShopMonitorLogs(selectedID, logsPage, 20)
  const shopList = targets.data ?? emptyShopTargets
  const selected = shopList.find((target) => target.id === selectedID) ?? null
  const selectedIndex = selected == null ? -1 : shopList.findIndex((target) => target.id === selected.id)
  const globalWatchTargetNames = useMemo(
    () => Object.fromEntries(shopList.map((target) => [target.id, shopDisplayName(target)])),
    [shopList],
  )
  const legacyWatchRuleCount = useMemo(
    () => shopList.reduce((total, target) => total + target.watch_rule_count, 0),
    [shopList],
  )
  const activeSyncJobs = useMemo(
    () => Object.values(syncJobs).filter((job) => job.status === "queued" || job.status === "running"),
    [syncJobs],
  )
  const activeSyncJobKey = activeSyncJobs.map((job) => `${job.id}:${job.status}`).join(",")

  function updateSelectedCategory(categoryID: number | null) {
    setSelectedCategoryID(categoryID)
    if (selectedID == null) return
    setCategoryIDs((current) => ({ ...current, [selectedID]: categoryID }))
  }

  function updateGoodsSort(sort: ShopGoodsSort) {
    setGoodsSort(sort)
    if (selectedID == null) return
    setSorts((current) => ({ ...current, [selectedID]: sort }))
  }

  useEffect(() => {
    writeShopsGoodsPreferences({
      selectedTargetID: selectedID,
      status: goodsStatus,
      inStockOnly,
      keyword: appliedGoodsKeyword,
      excludeKeyword: appliedGoodsExcludeKeyword,
      categoryIDs,
      sorts,
      shopListScrollTop: shopListScrollTopRef.current,
    })
  }, [appliedGoodsExcludeKeyword, appliedGoodsKeyword, categoryIDs, goodsStatus, inStockOnly, selectedID, sorts])

  useEffect(() => {
    if (!goods.data || goods.error) return
    if (!appliedGoodsKeyword.trim() && !appliedGoodsExcludeKeyword.trim()) return
    setGoodsSearchHistory(rememberAllShopGoodsSearchQuery({
      keyword: appliedGoodsKeyword,
      excludeKeyword: appliedGoodsExcludeKeyword,
    }))
  }, [appliedGoodsExcludeKeyword, appliedGoodsKeyword, goods.data, goods.error])

  function refreshShopData() {
    targets.refetch()
    goods.refetch()
    snapshotCategories.refetch()
    changes.refetch()
    monitorLogs.refetch()
    refresh()
  }

  useEffect(() => {
    if (selectedID == null || syncJobs[selectedID]) return
    let cancelled = false
    void apiFetch<ShopSyncJob>(`/shop-targets/${selectedID}/sync-jobs/latest`)
      .then((job) => {
        if (!cancelled && (job.status === "queued" || job.status === "running")) {
          setSyncJobs((current) => ({ ...current, [job.target_id]: job }))
        }
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [selectedID, syncJobs])

  useEffect(() => {
    if (activeSyncJobs.length === 0) return
    let cancelled = false
    const poll = async () => {
      let updates: ShopSyncJob[]
      try {
        updates = await apiFetch<ShopSyncJob[]>("/shop-targets/sync-jobs/status", {
          method: "POST",
          body: JSON.stringify({ job_ids: activeSyncJobs.map((job) => job.id) }),
        })
      } catch {
        return
      }
      if (cancelled) return
      const nextJobs = { ...syncJobs }
      for (const job of updates) {
        nextJobs[job.target_id] = job
      }
      setSyncJobs(nextJobs)

      const bulkIDs = bulkSyncJobIDsRef.current
      if (bulkIDs) {
        const bulkJobs = [...bulkIDs].map((id) => Object.values(nextJobs).find((job) => job.id === id)).filter(Boolean) as ShopSyncJob[]
        const bulkFinished = bulkJobs.length === bulkIDs.size && bulkJobs.every((job) => !isActiveSyncJob(job))
        if (bulkFinished) {
          const succeeded = bulkJobs.filter((job) => job.status === "succeeded").length
          const skipped = bulkJobs.filter((job) => job.status === "skipped").length
          const failed = bulkJobs.length - succeeded - skipped
          bulkSyncJobIDsRef.current = null
          refreshShopData()
          if (failed > 0 || skipped > 0) {
            toast.warning(`批量同步结束：成功 ${succeeded} 家，失败 ${failed} 家，跳过 ${skipped} 家`)
          } else {
            toast.success(`批量同步完成：${succeeded} 家店铺已更新`)
          }
        }
        return
      }

      for (const job of updates) {
        if (job.status === "succeeded") {
          toast.success(`同步完成：${job.goods_count} 个商品，${job.changed_count} 个变化`)
          refreshShopData()
        } else if (job.status === "failed" || job.status === "timed_out") {
          toast.error(job.error_message || "同步失败")
        } else if (job.status === "skipped") {
          toast.message(job.error_message || "已有同步任务，已跳过")
        }
      }
    }
    void poll()
    const timer = window.setInterval(() => void poll(), 2000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [activeSyncJobKey])

  useEffect(() => {
    if (shopList.length === 0 || (selectedID != null && shopList.some((target) => target.id === selectedID))) return
    setSelectedID(shopList[0].id)
  }, [selectedID, shopList])

  useEffect(() => {
    const list = shopListScrollRef.current
    if (!list) return
    if (Math.abs(list.scrollTop - shopListScrollTopRef.current) <= 1) return
    list.scrollTop = shopListScrollTopRef.current
  }, [shopList.length])

  useEffect(() => () => {
    if (shopListScrollSaveTimerRef.current != null) {
      window.clearTimeout(shopListScrollSaveTimerRef.current)
    }
    writeShopsGoodsPreferences({
      ...readShopsGoodsPreferences(),
      shopListScrollTop: shopListScrollTopRef.current,
    })
  }, [])

  useEffect(() => {
    const pendingLookup = pendingGoodsLookupRef.current
    const preserveLookup = pendingLookup?.targetID === selectedID
    setGoodsPage(1)
    setChangesPage(1)
    setLogsPage(1)
    const preferenceKey = selectedID == null ? "" : String(selectedID)
    setSelectedCategoryID(categoryIDs[preferenceKey] ?? null)
    setGoodsSort((sorts[preferenceKey] ?? selected?.goods_sort) || "category")
    if (preserveLookup) {
      setGoodsKeyword(pendingLookup.goodsKey)
      setAppliedGoodsKeyword(pendingLookup.goodsKey)
      setGoodsExcludeKeyword("")
      setAppliedGoodsExcludeKeyword("")
      pendingGoodsLookupRef.current = null
    }
  }, [categoryIDs, selectedID, selected?.goods_sort, sorts])

  useEffect(() => {
    setGoodsPage(1)
  }, [appliedGoodsExcludeKeyword, appliedGoodsKeyword, goodsSort, goodsStatus, inStockOnly, selectedCategoryID])

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

  const summary = useMemo(() => {
    return {
      total: shopList.length,
      enabled: shopList.filter((t) => t.monitor_enabled).length,
      goods: shopList.reduce((sum, t) => sum + (t.last_goods_count || 0), 0),
      low: shopList.reduce((sum, t) => sum + (t.last_low_stock_goods || 0), 0),
      failed: shopList.filter((t) => t.last_error).length,
      changed: shopList.reduce((sum, t) => sum + (t.last_changed_count || 0), 0),
    }
  }, [shopList])

  function openCreate() {
    setEditing(null)
    setForm({ ...emptyForm, sort_order: String(shopList.length + 1) })
    setFormOpen(true)
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
      const parsed = await apiFetch<{ platform: "ldxp"; site_url: string; base_url: string; token: string; name?: string; name_error?: string }>("/shop-targets/parse-url", {
        method: "POST",
        body: JSON.stringify({ site_url: form.site_url }),
      })
      setForm((f) => ({
        ...f,
        platform: parsed.platform,
        site_url: parsed.site_url || f.site_url,
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
      const maxListPosition = editing ? Math.max(shopList.length, 1) : shopList.length + 1
      const body = {
        ...form,
        sort_order: clampListPosition(Number(form.sort_order), maxListPosition),
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
      targets.refetch()
      refresh()
      if (editing) {
        toast.success("店铺已更新")
      } else {
        await startTargetSync(saved.id)
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "保存失败")
    } finally {
      setBusy(null)
    }
  }

  async function startTargetSync(targetID: number) {
    const result = await apiFetch<ShopSyncJobStartResult>(`/shop-targets/${targetID}/sync`, { method: "POST" })
    setSyncJobs((current) => ({ ...current, [targetID]: result.job }))
    toast.success(result.reused ? "店铺已添加，同步任务仍在运行" : "店铺已添加，已开始自动同步")
  }

  function applyGoodsSearch(nextValues?: Partial<{ keyword: string; excludeKeyword: string }>) {
    const nextKeyword = normalizeTextFilter(nextValues?.keyword ?? goodsKeyword)
    const nextExcludeKeyword = normalizeTextFilter(nextValues?.excludeKeyword ?? goodsExcludeKeyword)
    const changed = nextKeyword !== appliedGoodsKeyword || nextExcludeKeyword !== appliedGoodsExcludeKeyword
    setGoodsKeyword(nextKeyword)
    setGoodsExcludeKeyword(nextExcludeKeyword)
    setAppliedGoodsKeyword(nextKeyword)
    setAppliedGoodsExcludeKeyword(nextExcludeKeyword)
    setGoodsPage(1)
    if (changed) setHighlightedGoodsKey(null)
    if (!changed && goodsPage === 1) goods.refetch()
  }

  function handleGlobalWatchRulesChanged() {
    globalWatchRules.refetch()
  }

  function setGlobalWatchRulesDrawerOpen(open: boolean) {
    setGlobalWatchRulesOpen(open)
    if (!open) setGlobalWatchSeed(null)
  }

  function openGlobalWatchRules() {
    setGlobalWatchSeed(null)
    setGlobalWatchRulesOpen(true)
  }

  function watchGoodsGlobally(row: ShopGoodsSnapshot) {
    setGlobalWatchSeed({
      goods_key: row.goods_key,
      name: row.name,
      nonce: Date.now(),
    })
    setGlobalWatchRulesOpen(true)
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
      const result = await apiFetch<ShopSyncJobStartResult>(`/shop-targets/${target.id}/sync`, { method: "POST" })
      setSyncJobs((current) => ({ ...current, [target.id]: result.job }))
      toast.message(result.reused ? "同步任务仍在运行" : "已开始同步店铺")
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
      const jobs = result.targets.flatMap((item) => item.job ? [item.job] : [])
      if (jobs.length > 0) {
        setSyncJobs((current) => {
          const next = { ...current }
          for (const job of jobs) next[job.target_id] = job
          return next
        })
        bulkSyncJobIDsRef.current = new Set(jobs.map((job) => job.id))
      }
      if (result.failed > 0) {
        toast.warning(`已加入同步队列：新建 ${result.queued} 家，复用 ${result.reused} 家，入队失败 ${result.failed} 家`)
      } else {
        toast.message(`已加入同步队列：${result.queued} 家，复用运行中任务 ${result.reused} 家`)
      }
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
      pendingGoodsLookupRef.current = { targetID: row.target_id, goodsKey: key }
      setSelectedID(row.target_id)
    } else {
      setGoodsKeyword(key)
      setAppliedGoodsKeyword(key)
      setGoodsExcludeKeyword("")
      setAppliedGoodsExcludeKeyword("")
    }
    updateSelectedCategory(null)
    setGoodsStatus("all")
    setInStockOnly(false)
    setGoodsPage(1)
    setHighlightedGoodsKey(key)
    toast.info(`正在定位商品：${row.goods_name || key}`)
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
            <Button variant="outline" onClick={openGlobalWatchRules} className="gap-2">
              <Bell className="size-4" />
              {"全局关注规则"}
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

      <div className="grid min-w-0 gap-4 lg:grid-cols-[376px_minmax(0,1fr)]">
        <div className="min-w-0 space-y-3 lg:hidden">
          {shopList.length > 0 ? (
            <Card className="p-3">
              <Field label="选择店铺">
                <Select value={selectedID == null ? undefined : String(selectedID)} onValueChange={(value) => setSelectedID(Number(value))}>
                  <SelectTrigger>
                    <SelectValue placeholder="选择店铺" />
                  </SelectTrigger>
                  <SelectContent>
                    {shopList.map((target) => (
                      <SelectItem key={target.id} value={String(target.id)}>
                        {shopDisplayName(target)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </Card>
          ) : null}
          {selected ? (
            <ShopCard
              target={selected}
              active
              busy={busy}
              syncJob={syncJobs[selected.id]}
              canMoveUp={selectedIndex > 0}
              canMoveDown={selectedIndex >= 0 && selectedIndex < shopList.length - 1}
              onSelect={() => setSelectedID(selected.id)}
              onMoveUp={() => moveShopTarget(selected.id, -1)}
              onMoveDown={() => moveShopTarget(selected.id, 1)}
              onEdit={() => openEdit(selected)}
              onTest={() => testTarget(selected)}
              onSync={() => syncTarget(selected)}
              onDelete={() => deleteTarget(selected)}
            />
          ) : null}
          {!targets.loading && shopList.length === 0 ? (
            <Card className="border-dashed p-6 text-center text-sm text-muted-foreground">
              <PackageSearch className="mx-auto mb-2 size-8" />
              {"还没有店铺监控，添加一个店铺 URL 开始。"}
            </Card>
          ) : null}
        </div>

        <div className="hidden min-w-0 lg:block">
          <div className="sticky top-[calc(3.5rem+1.25rem)] self-start">
            <div
              ref={shopListScrollRef}
              onScroll={(event) => {
                shopListScrollTopRef.current = event.currentTarget.scrollTop
                if (shopListScrollSaveTimerRef.current != null) {
                  window.clearTimeout(shopListScrollSaveTimerRef.current)
                }
                shopListScrollSaveTimerRef.current = window.setTimeout(() => {
                  writeShopsGoodsPreferences({
                    ...readShopsGoodsPreferences(),
                    shopListScrollTop: shopListScrollTopRef.current,
                  })
                  shopListScrollSaveTimerRef.current = null
                }, 180)
              }}
              className="space-y-3 overflow-y-auto overscroll-contain pr-1 max-h-[calc(100dvh-3.5rem-1.25rem)]"
            >
              {shopList.map((target, index) => (
                <ShopCard
                  key={target.id}
                  target={target}
                  active={target.id === selectedID}
                  busy={busy}
                  syncJob={syncJobs[target.id]}
                  canMoveUp={index > 0}
                  canMoveDown={index < shopList.length - 1}
                  onSelect={() => setSelectedID(target.id)}
                  onMoveUp={() => moveShopTarget(target.id, -1)}
                  onMoveDown={() => moveShopTarget(target.id, 1)}
                  onEdit={() => openEdit(target)}
                  onTest={() => testTarget(target)}
                  onSync={() => syncTarget(target)}
                  onDelete={() => deleteTarget(target)}
                />
              ))}
              {!targets.loading && shopList.length === 0 ? (
                <Card className="border-dashed p-6 text-center text-sm text-muted-foreground">
                  <PackageSearch className="mx-auto mb-2 size-8" />
                  {"还没有店铺监控，添加一个店铺 URL 开始。"}
                </Card>
              ) : null}
            </div>
          </div>
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
            keyword={goodsKeyword}
            excludeKeyword={goodsExcludeKeyword}
            keywordHistory={goodsSearchHistory.keyword}
            excludeKeywordHistory={goodsSearchHistory.excludeKeyword}
            searchDirty={goodsSearchDirty}
            searchActive={goodsSearchActive}
            refreshingKey={busy?.startsWith("refresh-goods:") ? busy.slice("refresh-goods:".length) : null}
            highlightedGoodsKey={highlightedGoodsKey}
            rowRefs={goodsRowRefs}
            onCategory={updateSelectedCategory}
            onStatus={setGoodsStatus}
            onInStockOnly={setInStockOnly}
            onSort={updateGoodsSort}
            onKeyword={(keyword) => {
              setGoodsKeyword(keyword)
            }}
            onExcludeKeyword={setGoodsExcludeKeyword}
            onApplySearch={applyGoodsSearch}
            onRefreshGoods={refreshGoodsStock}
            onWatchGoods={watchGoodsGlobally}
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
                <Input value={form.site_url} onChange={(e) => setForm({ ...form, site_url: e.target.value })} placeholder="https://pay.ldxp.cn/shop/7FCVUA4X 或 https://www.ldxp.cn/item/9l814h" />
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
            <Field label="列表位置">
              <Input
                type="number"
                min="1"
                max={editing ? String(Math.max(shopList.length, 1)) : String(shopList.length + 1)}
                step="1"
                value={form.sort_order}
                onChange={(e) => setForm({ ...form, sort_order: e.target.value })}
                onBlur={() =>
                  setForm((current) => ({
                    ...current,
                    sort_order: normalizeListPositionInput(
                      current.sort_order,
                      editing ? Math.max(shopList.length, 1) : shopList.length + 1,
                    ),
                  }))
                }
              />
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
      <GlobalShopWatchRulesDrawer
        open={globalWatchRulesOpen}
        onOpenChange={setGlobalWatchRulesDrawerOpen}
        rules={globalWatchRules.data ?? []}
        loading={globalWatchRules.loading}
        seed={globalWatchSeed}
        targetNames={globalWatchTargetNames}
        legacyRuleCount={legacyWatchRuleCount}
        onRulesChanged={handleGlobalWatchRulesChanged}
      />
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
  syncJob?: ShopSyncJob
  canMoveUp: boolean
  canMoveDown: boolean
  onSelect: () => void
  onMoveUp: () => void
  onMoveDown: () => void
  onEdit: () => void
  onTest: () => void
  onSync: () => void
  onDelete: () => void
}) {
  const { target, active, busy } = props
  const displayName = shopDisplayName(target)
  const syncing = props.syncJob?.status === "queued" || props.syncJob?.status === "running"
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
        <div className="flex items-center gap-1">
          {syncing ? <span className="rounded-full bg-warning/10 px-2 py-0.5 text-[10px] text-warning">同步中</span> : null}
          <span className={cn("rounded-full px-2 py-0.5 text-[10px]", target.monitor_enabled ? "bg-success/10 text-success" : "bg-muted text-muted-foreground")}>
            {target.monitor_enabled ? "启用" : "暂停"}
          </span>
        </div>
      </div>
      <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
        <Mini label="商品" value={target.last_goods_count} />
        <Mini label="低库存" value={target.last_low_stock_goods} />
        <Mini label="变化" value={target.last_changed_count} />
      </div>
      {target.last_error ? <p className="mt-2 line-clamp-2 text-xs text-danger">{target.last_error}</p> : null}
      <div className="mt-3 flex flex-col gap-2">
        <span className="text-[11px] text-muted-foreground">上次同步 {relativeTime(target.last_sync_at)}</span>
        <div className="flex flex-nowrap justify-end gap-1" onClick={(e) => e.stopPropagation()}>
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
          <Button variant="outline" size="icon" className="size-7" onClick={props.onSync} disabled={busy === `sync:${target.id}` || syncing}>
            {busy === `sync:${target.id}` || syncing ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
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
  keyword,
  excludeKeyword,
  keywordHistory,
  excludeKeywordHistory,
  searchDirty,
  searchActive,
  refreshingKey,
  highlightedGoodsKey,
  rowRefs,
  onCategory,
  onStatus,
  onInStockOnly,
  onSort,
  onKeyword,
  onExcludeKeyword,
  onApplySearch,
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
  keyword: string
  excludeKeyword: string
  keywordHistory: string[]
  excludeKeywordHistory: string[]
  searchDirty: boolean
  searchActive: boolean
  refreshingKey: string | null
  highlightedGoodsKey: string | null
  rowRefs: React.MutableRefObject<Record<string, HTMLTableRowElement | null>>
  onCategory: (categoryID: number | null) => void
  onStatus: (status: GoodsStatusFilter) => void
  onInStockOnly: (enabled: boolean) => void
  onSort: (sort: ShopGoodsSort) => void
  onKeyword: (keyword: string) => void
  onExcludeKeyword: (keyword: string) => void
  onApplySearch: (nextValues?: Partial<{ keyword: string; excludeKeyword: string }>) => void
  onRefreshGoods: (row: ShopGoodsSnapshot) => void
  onWatchGoods: (row: ShopGoodsSnapshot) => void
  onPage: (page: number) => void
}) {
  const allCount = categories.reduce((sum, category) => sum + category.goods_count, 0)
  const activeFilters = selectedCategoryID !== null || status !== "all" || sort !== "category" || searchActive
  const selectedCategory = categories.find((category) => category.category_id === selectedCategoryID)
  const selectedCategoryName = selectedCategory ? categoryLabel(selectedCategory) : "全部分类"
  const displayName = shopDisplayName(target)

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
          {searchDirty ? <span>有未应用搜索条件</span> : null}
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
        <div className="grid min-w-0 gap-2 md:grid-cols-2 xl:grid-cols-[max-content_max-content_max-content_minmax(180px,1fr)_minmax(180px,1fr)_2.25rem]">
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
          <Select value={sort} onValueChange={(value) => onSort(value as ShopGoodsSort)}>
            <SelectTrigger className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {Object.entries(goodsSortLabels).map(([value, label]) => (
                <SelectItem key={value} value={value}>{label}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            type="button"
            variant={inStockOnly ? "default" : "outline"}
            onClick={() => onInStockOnly(!inStockOnly)}
            className="gap-2"
          >
            <CheckCircle2 className="size-4" />
            {"有库存"}
          </Button>
          <SearchHistoryInput
            value={keyword}
            onChange={onKeyword}
            onClear={() => onApplySearch({ keyword: "" })}
            onSubmit={() => onApplySearch()}
            onHistorySelect={(value) => onApplySearch({ keyword: value })}
            placeholder={`包含商品名或 Key（${selectedCategoryName}，空格/逗号多词）`}
            history={keywordHistory}
          />
          <SearchHistoryInput
            value={excludeKeyword}
            onChange={onExcludeKeyword}
            onClear={() => onApplySearch({ excludeKeyword: "" })}
            onSubmit={() => onApplySearch()}
            onHistorySelect={(value) => onApplySearch({ excludeKeyword: value })}
            placeholder="排除商品名或 Key（空格/逗号多词）"
            history={excludeKeywordHistory}
          />
          <Button
            type="button"
            variant={searchDirty ? "default" : "outline"}
            size="icon"
            className="justify-self-start"
            onClick={() => onApplySearch()}
            aria-label="搜索商品"
            title="搜索商品"
          >
            <Search className="size-4" />
          </Button>
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
                    <div className="flex min-w-0 items-center gap-1">
                      <Button
                        type="button"
                        size="icon"
                        variant="ghost"
                        className="size-7 shrink-0"
                        onClick={() => onWatchGoods(row)}
                        aria-label={`为 ${row.name || row.goods_key} 新建全局关注规则`}
                        title="新建全局关注规则"
                      >
                        <Star className="size-4" />
                      </Button>
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
