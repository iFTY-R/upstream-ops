import { useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { toast } from "sonner"
import { ExternalLink, Filter, Loader2, PackageSearch, Plus, RefreshCw, Search, ShoppingCart, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { DataPagination } from "@/components/ui/data-pagination"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useLatestShopMonitorLog, useShopGoodsOverview, useShopGoodsTargetOptions } from "@/lib/queries"
import { money, relativeTime } from "@/lib/format"
import { useTriggerRefresh } from "@/lib/refresh-context"
import {
  readAllShopGoodsPreferences,
  readAllShopGoodsSearchHistory,
  rememberAllShopGoodsSearchQuery,
  type ShopGoodsStatusFilter,
  writeAllShopGoodsPreferences,
} from "@/lib/shop-goods-preferences"
import { cn } from "@/lib/utils"
import type {
  ShopGoodsListItem,
  ShopGoodsSort,
  ShopGoodsStatus,
  ShopMonitorLog,
  ShopRefreshGoodsResult,
  ShopSyncAllResult,
  ShopSyncJob,
  ShopSyncJobStartResult,
  ShopTarget,
} from "@/lib/api-types"

type GoodsStatusFilter = ShopGoodsStatusFilter

type AddShopForm = {
  name: string
  site_url: string
  base_url: string
  token: string
  stock_threshold: number
  notify_enabled: boolean
  proxy_enabled: boolean
}

const emptyAddShopForm: AddShopForm = {
  name: "",
  site_url: "",
  base_url: "",
  token: "",
  stock_threshold: 1,
  notify_enabled: false,
  proxy_enabled: false,
}

const statusLabels: Record<GoodsStatusFilter, string> = {
  all: "全部状态",
  active: "在线",
  removed: "已消失",
  low_stock: "低库存",
  out_of_stock: "零库存",
}

const sortLabels: Record<ShopGoodsSort, string> = {
  category: "店铺 / 分类 / 名称",
  stock_asc: "库存从低到高",
  stock_desc: "库存从高到低",
  price_asc: "价格从低到高",
  price_desc: "价格从高到低",
  last_seen_desc: "最近出现",
}

function shopName(row: ShopGoodsListItem) {
  return row.target_name?.trim() || row.target_last_shop_name?.trim() || `店铺 #${row.target_id}`
}

function normalizeTextFilter(value: string) {
  return value.trim()
}

function isActiveSyncJob(job: ShopSyncJob) {
  return job.status === "queued" || job.status === "running"
}

function durationText(ms?: number | null) {
  if (ms == null || !Number.isFinite(ms) || ms <= 0) return "—"
  if (ms < 1000) return `${Math.round(ms)} 毫秒`
  const totalSeconds = Math.max(1, Math.round(ms / 1000))
  if (totalSeconds < 60) return `${totalSeconds} 秒`
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return seconds > 0 ? `${minutes} 分 ${seconds} 秒` : `${minutes} 分钟`
}

function monitorLogStatusText(log: ShopMonitorLog | null) {
  if (!log) return "暂无同步记录"
  return log.success ? "同步成功" : "同步失败"
}

export default function ShopGoodsPage({ publicMode = false }: { publicMode?: boolean }) {
  const targets = useShopGoodsTargetOptions(publicMode)
  const latestSync = useLatestShopMonitorLog(!publicMode)
  const triggerRefresh = useTriggerRefresh()
  const [initialPreferences] = useState(readAllShopGoodsPreferences)
  const initialKeyword = normalizeTextFilter(initialPreferences.keyword)
  const initialExcludeKeyword = normalizeTextFilter(initialPreferences.excludeKeyword)
  const initialCategoryName = normalizeTextFilter(initialPreferences.categoryName)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(initialPreferences.pageSize)
  const [targetID, setTargetID] = useState<number | null>(initialPreferences.targetID)
  const [status, setStatus] = useState<GoodsStatusFilter>(initialPreferences.status)
  const [inStockOnly, setInStockOnly] = useState(initialPreferences.inStockOnly)
  const [showGoodsKey, setShowGoodsKey] = useState(initialPreferences.showGoodsKey)
  const [sort, setSort] = useState<ShopGoodsSort>(initialPreferences.sort)
  const [keyword, setKeyword] = useState(initialKeyword)
  const [excludeKeyword, setExcludeKeyword] = useState(initialExcludeKeyword)
  const [categoryName, setCategoryName] = useState(initialCategoryName)
  const [appliedKeyword, setAppliedKeyword] = useState(initialKeyword)
  const [appliedExcludeKeyword, setAppliedExcludeKeyword] = useState(initialExcludeKeyword)
  const [appliedCategoryName, setAppliedCategoryName] = useState(initialCategoryName)
  const [searchHistory, setSearchHistory] = useState(readAllShopGoodsSearchHistory)
  const [refreshingGoodsKey, setRefreshingGoodsKey] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [addShopOpen, setAddShopOpen] = useState(false)
  const [addShopForm, setAddShopForm] = useState<AddShopForm>(emptyAddShopForm)
  const [syncJobs, setSyncJobs] = useState<Record<number, ShopSyncJob>>({})
  const bulkSyncJobIDsRef = useRef<Set<number> | null>(null)

  const filters = useMemo(
    () => ({
      target_id: targetID ?? undefined,
      category_name: appliedCategoryName.trim() || undefined,
      status: inStockOnly ? "in_stock" as ShopGoodsStatus : status,
      keyword: appliedKeyword,
      exclude_keyword: appliedExcludeKeyword,
      sort,
    }),
    [appliedCategoryName, appliedExcludeKeyword, appliedKeyword, inStockOnly, sort, status, targetID],
  )
  const textFiltersDirty = normalizeTextFilter(keyword) !== appliedKeyword
    || normalizeTextFilter(excludeKeyword) !== appliedExcludeKeyword
    || normalizeTextFilter(categoryName) !== appliedCategoryName
  const goods = useShopGoodsOverview(page, pageSize, filters, true, publicMode)
  const rows = goods.data?.items ?? []
  const total = goods.data?.total ?? 0
  const pages = goods.data?.pages ?? 1
  const activeSyncJobs = useMemo(
    () => Object.values(syncJobs).filter(isActiveSyncJob),
    [syncJobs],
  )
  const activeSyncJobKey = activeSyncJobs.map((job) => `${job.id}:${job.status}`).join(",")
  const latestSyncTargetName = useMemo(() => {
    const log = latestSync.data
    if (!log) return ""
    const target = (targets.data ?? []).find((item) => item.id === log.target_id)
    return target?.name?.trim() || target?.last_shop_name?.trim() || (log.target_id ? `店铺 #${log.target_id}` : "")
  }, [latestSync.data, targets.data])
  const activeFilters = targetID !== null
    || status !== "all"
    || sort !== "category"
    || appliedKeyword.trim() !== ""
    || appliedExcludeKeyword.trim() !== ""
    || appliedCategoryName.trim() !== ""

  useEffect(() => {
    writeAllShopGoodsPreferences({
      targetID,
      status,
      inStockOnly,
      showGoodsKey,
      sort,
      keyword: appliedKeyword,
      excludeKeyword: appliedExcludeKeyword,
      categoryName: appliedCategoryName,
      pageSize,
    })
  }, [appliedCategoryName, appliedExcludeKeyword, appliedKeyword, inStockOnly, pageSize, showGoodsKey, sort, status, targetID])

  useEffect(() => {
    if (!goods.data || goods.error) return
    if (!filters.category_name?.trim() && !filters.keyword?.trim() && !filters.exclude_keyword?.trim()) return
    setSearchHistory(rememberAllShopGoodsSearchQuery({
      categoryName: filters.category_name,
      keyword: filters.keyword,
      excludeKeyword: filters.exclude_keyword,
    }))
  }, [filters.category_name, filters.exclude_keyword, filters.keyword, goods.data, goods.error])

  useEffect(() => {
    if (publicMode || activeSyncJobs.length === 0) return
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
        const bulkJobs = [...bulkIDs]
          .map((id) => Object.values(nextJobs).find((job) => job.id === id))
          .filter(Boolean) as ShopSyncJob[]
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
        if (isActiveSyncJob(job)) continue
        if (job.status === "succeeded") {
          toast.success(`同步完成：${job.goods_count} 个商品，${job.changed_count} 个变化`)
          refreshShopData()
        } else if (job.status === "failed" || job.status === "timed_out") {
          toast.error(job.error_message || "同步失败")
          refreshShopData()
        } else if (job.status === "skipped") {
          toast.message(job.error_message || "已有同步任务，已跳过")
          refreshShopData()
        }
      }
    }
    void poll()
    const timer = window.setInterval(() => void poll(), 2000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [activeSyncJobKey, publicMode])

  function resetPage(next: () => void) {
    next()
    setPage(1)
  }

  function changePageSize(nextPageSize: number) {
    setPageSize(nextPageSize)
    setPage(1)
  }

  function refreshShopData() {
    targets.refetch()
    goods.refetch()
    latestSync.refetch()
    triggerRefresh()
  }

  function openAddShop() {
    setAddShopForm(emptyAddShopForm)
    setAddShopOpen(true)
  }

  async function parseAddShopURL() {
    if (!addShopForm.site_url.trim()) {
      toast.error("请先填写店铺 URL")
      return
    }
    setBusy("parse-shop-url")
    try {
      const parsed = await apiFetch<{ platform: "ldxp"; site_url: string; base_url: string; token: string; name?: string; name_error?: string }>("/shop-targets/parse-url", {
        method: "POST",
        body: JSON.stringify({ site_url: addShopForm.site_url }),
      })
      setAddShopForm((form) => ({
        ...form,
        site_url: parsed.site_url || form.site_url,
        base_url: parsed.base_url,
        token: parsed.token,
        name: parsed.name || (form.name === parsed.token ? "" : form.name),
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

  async function startTargetSync(targetID: number) {
    const result = await apiFetch<ShopSyncJobStartResult>(`/shop-targets/${targetID}/sync`, { method: "POST" })
    setSyncJobs((current) => ({ ...current, [targetID]: result.job }))
    toast.success(result.reused ? "店铺已添加，同步任务仍在运行" : "店铺已添加，已开始自动同步")
  }

  async function saveAddShop() {
    if (!addShopForm.site_url.trim()) {
      toast.error("请填写店铺 URL")
      return
    }
    setBusy("save-shop")
    try {
      const saved = await apiFetch<ShopTarget>("/shop-targets", {
        method: "POST",
        body: JSON.stringify({
          name: addShopForm.name,
          site_url: addShopForm.site_url,
          platform: "ldxp",
          base_url: addShopForm.base_url,
          token: addShopForm.token,
          monitor_enabled: true,
          notify_enabled: addShopForm.notify_enabled,
          scope_mode: "all",
          goods_types: ["card"],
          category_ids: [],
          category_names: [],
          keywords: [],
          goods_keys: [],
          stock_threshold: addShopForm.stock_threshold,
          proxy_enabled: addShopForm.proxy_enabled,
          price_change_enabled: true,
          stock_change_enabled: true,
          low_stock_enabled: true,
          restock_enabled: true,
          new_goods_enabled: true,
          removed_goods_enabled: true,
          goods_sort: "category",
        }),
      })
      setAddShopOpen(false)
      setAddShopForm(emptyAddShopForm)
      targets.refetch()
      triggerRefresh()
      try {
        await startTargetSync(saved.id)
      } catch (err) {
        toast.warning(`店铺已添加，但自动同步失败：${err instanceof Error ? err.message : "未知错误"}`)
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "添加店铺失败")
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

  function applyTextFilters(nextValues?: Partial<{ categoryName: string; keyword: string; excludeKeyword: string }>) {
    const nextCategoryName = normalizeTextFilter(nextValues?.categoryName ?? categoryName)
    const nextKeyword = normalizeTextFilter(nextValues?.keyword ?? keyword)
    const nextExcludeKeyword = normalizeTextFilter(nextValues?.excludeKeyword ?? excludeKeyword)
    const changed = nextCategoryName !== appliedCategoryName
      || nextKeyword !== appliedKeyword
      || nextExcludeKeyword !== appliedExcludeKeyword
    setCategoryName(nextCategoryName)
    setKeyword(nextKeyword)
    setExcludeKeyword(nextExcludeKeyword)
    setAppliedCategoryName(nextCategoryName)
    setAppliedKeyword(nextKeyword)
    setAppliedExcludeKeyword(nextExcludeKeyword)
    setPage(1)
    if (!changed && page === 1) goods.refetch()
  }

  async function refreshGoodsStock(row: ShopGoodsListItem) {
    const busyKey = `${row.target_id}:${row.goods_key}`
    setRefreshingGoodsKey(busyKey)
    try {
      const result = await apiFetch<ShopRefreshGoodsResult>(
        `/shop-targets/${row.target_id}/goods/${encodeURIComponent(row.goods_key)}/refresh`,
        { method: "POST" },
      )
      if (goods.data) {
        goods.setData({
          ...goods.data,
          items: goods.data.items.map((item) =>
            item.target_id === row.target_id && item.goods_key === row.goods_key
              ? { ...item, ...result.snapshot }
              : item,
          ),
        })
      }
      goods.refetch()
      if (result.found) {
        toast.success(`库存已刷新：${result.snapshot.stock_count}`)
      } else {
        toast.warning("官方接口未找到该商品，已标记为消失")
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "刷新库存失败")
    } finally {
      setRefreshingGoodsKey(null)
    }
  }

  const summary = useMemo(() => {
    return {
      shops: targets.data?.length ?? 0,
      total,
      inStock: rows.filter((row) => !row.removed_at && row.stock_count > 0).length,
      low: rows.filter((row) => !row.removed_at && row.target_stock_threshold > 0 && row.stock_count <= row.target_stock_threshold).length,
    }
  }, [rows, targets.data?.length, total])

  return (
    <section className="space-y-4">
      <header className="space-y-3">
        <div className="flex flex-col gap-3 border-b border-border pb-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0 space-y-1">
            <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-[0.24em] text-muted-foreground">
              <PackageSearch className="size-4 text-blue-600" />
              {"Shop Goods"}
            </div>
            <h1 className="text-2xl font-semibold tracking-tight text-foreground">{"商品总览"}</h1>
            <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
              {"跨所有店铺查看商品快照，按店铺、分类、状态、关键词和库存/价格排序筛选。"}
            </p>
          </div>
          {!publicMode ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={syncAllTargets}
                disabled={busy === "sync-all" || (targets.data?.length ?? 0) === 0}
                className="gap-2"
              >
                {busy === "sync-all" ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                {"同步全部"}
              </Button>
              <Button type="button" onClick={openAddShop} className="gap-2">
                <Plus className="size-4" />
                {"添加店铺"}
              </Button>
            </div>
          ) : null}
        </div>

        <div className={cn(
          "grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-border bg-border",
          publicMode ? "lg:grid-cols-4" : "lg:grid-cols-5",
        )}>
          {!publicMode ? (
            <LatestSyncSummary
              log={latestSync.data}
              targetName={latestSyncTargetName}
              loading={latestSync.loading}
              activeCount={activeSyncJobs.length}
            />
          ) : null}
          <Summary label="店铺" value={summary.shops} />
          <Summary label="匹配商品" value={summary.total} />
          <Summary label="本页有库存" value={summary.inStock} />
          <Summary label="本页低库存" value={summary.low} warn={summary.low > 0} />
        </div>
      </header>

      <Card className="overflow-hidden">
        <div className="space-y-3 border-b border-border p-3">
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs font-medium text-muted-foreground">
            <div className="flex items-center gap-2">
              <Filter className="size-3.5" />
              <span>{"筛选和排序"}</span>
              {goods.loading ? <span>{"加载中..."}</span> : textFiltersDirty ? <span>{"有未应用搜索条件"}</span> : null}
            </div>
            <div className="inline-flex items-center gap-2">
              <Checkbox
                id="shop-goods-show-key"
                checked={showGoodsKey}
                onCheckedChange={(checked) => setShowGoodsKey(checked === true)}
              />
              <label htmlFor="shop-goods-show-key" className="cursor-pointer select-none transition hover:text-foreground">
                {"显示商品 Key"}
              </label>
            </div>
          </div>
          <div className="grid gap-2 md:grid-cols-[repeat(3,minmax(0,1fr))_2.5rem]">
            <ClearableInput
              value={categoryName}
              onChange={setCategoryName}
              onClear={() => applyTextFilters({ categoryName: "" })}
              onSubmit={applyTextFilters}
              placeholder="按分类名称筛选"
              history={searchHistory.categoryName}
            />
            <ClearableInput
              value={keyword}
              onChange={setKeyword}
              onClear={() => applyTextFilters({ keyword: "" })}
              onSubmit={applyTextFilters}
              placeholder="包含商品名或 Key"
              history={searchHistory.keyword}
            />
            <ClearableInput
              value={excludeKeyword}
              onChange={setExcludeKeyword}
              onClear={() => applyTextFilters({ excludeKeyword: "" })}
              onSubmit={applyTextFilters}
              placeholder="排除商品名或 Key"
              history={searchHistory.excludeKeyword}
            />
            <Button
              type="button"
              variant={textFiltersDirty ? "default" : "outline"}
              size="icon"
              className="justify-self-end md:justify-self-start"
              onClick={() => applyTextFilters()}
              aria-label="搜索"
              title="搜索"
            >
              <Search className="size-4" />
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Select value={targetID == null ? "all" : String(targetID)} onValueChange={(value) => resetPage(() => setTargetID(value === "all" ? null : Number(value)))}>
              <SelectTrigger className="w-full sm:w-44"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部店铺</SelectItem>
                {(targets.data ?? []).map((target) => (
                  <SelectItem key={target.id} value={String(target.id)}>
                    {target.name?.trim() || target.last_shop_name?.trim() || `店铺 #${target.id}`}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={status} onValueChange={(value) => resetPage(() => setStatus(value as GoodsStatusFilter))}>
              <SelectTrigger className="w-full sm:w-32"><SelectValue /></SelectTrigger>
              <SelectContent>
                {Object.entries(statusLabels).map(([value, label]) => (
                  <SelectItem key={value} value={value}>{label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={sort} onValueChange={(value) => resetPage(() => setSort(value as ShopGoodsSort))}>
              <SelectTrigger className="w-full sm:w-52"><SelectValue /></SelectTrigger>
              <SelectContent>
                {Object.entries(sortLabels).map(([value, label]) => (
                  <SelectItem key={value} value={value}>{label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              type="button"
              variant={inStockOnly ? "default" : "outline"}
              onClick={() => resetPage(() => setInStockOnly(!inStockOnly))}
            >
              {"有库存"}
            </Button>
          </div>
        </div>

        <div className="overflow-x-auto">
          <Table className="min-w-[1260px] table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[13%]">店铺</TableHead>
                <TableHead className="w-[29%]">商品</TableHead>
                <TableHead className="w-[18%]">分组 / 分类</TableHead>
                <TableHead className="w-[10%]">价格</TableHead>
                <TableHead className="w-[7%]">库存</TableHead>
                <TableHead className="w-[7%]">状态</TableHead>
                <TableHead className="w-[9%]">最近出现</TableHead>
                <TableHead className="w-[7%]">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {goods.error ? (
                <TableRow>
                  <TableCell colSpan={8} className="h-24 text-center text-sm text-destructive">
                    {`商品加载失败：${goods.error}`}
                  </TableCell>
                </TableRow>
              ) : rows.map((row) => (
                <GoodsRow
                  key={row.id}
                  row={row}
                  refreshing={refreshingGoodsKey === `${row.target_id}:${row.goods_key}`}
                  showGoodsKey={showGoodsKey}
                  onRefreshStock={publicMode ? undefined : refreshGoodsStock}
                />
              ))}
              {!goods.error && rows.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="h-24 text-center text-sm text-muted-foreground">
                    {activeFilters ? "当前筛选条件下没有商品。" : "暂无商品快照，先同步店铺。"}
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>
        <DataPagination
          page={goods.data?.page ?? page}
          pageSize={pageSize}
          pages={pages}
          total={total}
          disabled={goods.loading}
          onPageChange={setPage}
          onPageSizeChange={changePageSize}
        />
      </Card>

      {!publicMode ? (
        <AddShopDialog
          open={addShopOpen}
          form={addShopForm}
          busy={busy}
          onOpenChange={setAddShopOpen}
          onFormChange={setAddShopForm}
          onParseURL={parseAddShopURL}
          onSave={saveAddShop}
        />
      ) : null}
    </section>
  )
}

function GoodsRow({
  row,
  refreshing,
  showGoodsKey,
  onRefreshStock,
}: {
  row: ShopGoodsListItem
  refreshing: boolean
  showGoodsKey: boolean
  onRefreshStock?: (row: ShopGoodsListItem) => void
}) {
  const canBuy = !row.removed_at && row.stock_count > 0 && row.link
  const low = !row.removed_at && row.target_stock_threshold > 0 && row.stock_count <= row.target_stock_threshold
  return (
    <TableRow className={cn(row.removed_at && "opacity-50")}>
      <TableCell>
        <div className="min-w-0">
          <div className="truncate font-medium" title={shopName(row)}>{shopName(row)}</div>
          {row.target_site_url ? (
            <a href={row.target_site_url} target="_blank" rel="noreferrer" className="mt-1 inline-flex max-w-full items-center gap-1 truncate text-xs text-muted-foreground hover:text-foreground">
              店铺页 <ExternalLink className="size-3" />
            </a>
          ) : null}
        </div>
      </TableCell>
      <TableCell>
        <div className="min-w-0">
          <div className="line-clamp-2 whitespace-normal break-words font-medium leading-5" title={row.name}>{row.name}</div>
          {showGoodsKey ? (
            <div className="mt-1 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
              <span className="shrink-0">{row.goods_key}</span>
            </div>
          ) : null}
        </div>
      </TableCell>
      <TableCell>
        <div className="line-clamp-2 whitespace-normal break-words leading-5" title={row.category_name || undefined}>
          {row.category_name || "未分组"}
        </div>
      </TableCell>
      <TableCell>
        <div className="whitespace-nowrap tabular-nums">
          <div>{money(row.price)}</div>
          {row.limit_count > 1 ? (
            <div className="mt-0.5 text-xs text-muted-foreground">{`×${row.limit_count} = ${money(row.price * row.limit_count)}`}</div>
          ) : null}
        </div>
      </TableCell>
      <TableCell>
        {onRefreshStock ? (
          <button
            type="button"
            onClick={() => onRefreshStock(row)}
            disabled={refreshing}
            className={cn(
              "inline-flex items-center gap-1 rounded-md px-2 py-1 font-semibold tabular-nums transition hover:bg-muted disabled:cursor-wait disabled:opacity-70",
              low && "text-warning",
            )}
            title="点击刷新该商品库存"
          >
            {refreshing ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3 opacity-60" />}
            {row.stock_count}
          </button>
        ) : (
          <span className={cn("font-semibold tabular-nums", low && "text-warning")}>{row.stock_count}</span>
        )}
      </TableCell>
      <TableCell>{row.removed_at ? "已消失" : row.stock_count > 0 ? "有库存" : "零库存"}</TableCell>
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
}

function Summary({ label, value, warn }: { label: string; value: number; warn?: boolean }) {
  return (
    <div className={cn("min-w-0 bg-card p-3", warn && "bg-warning/5")}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums">{value}</div>
    </div>
  )
}

function LatestSyncSummary({
  log,
  targetName,
  loading,
  activeCount,
}: {
  log: ShopMonitorLog | null
  targetName: string
  loading: boolean
  activeCount: number
}) {
  return (
    <div className={cn(
      "min-w-0 bg-card p-3",
      log && !log.success && "bg-danger/5",
      activeCount > 0 && "bg-warning/5",
    )}>
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs text-muted-foreground">{"上一次同步"}</div>
        {activeCount > 0 ? (
          <span className="inline-flex items-center gap-1 rounded-full bg-warning/10 px-2 py-0.5 text-[10px] text-warning">
            <Loader2 className="size-3 animate-spin" />
            {`同步中 ${activeCount}`}
          </span>
        ) : (
          <span className={cn("rounded-full px-2 py-0.5 text-[10px]", log?.success === false ? "bg-danger/10 text-danger" : "bg-muted text-muted-foreground")}>
            {loading ? "加载中" : monitorLogStatusText(log)}
          </span>
        )}
      </div>
      <div className="mt-1 text-sm font-medium">
        {log ? relativeTime(log.finished_at || log.started_at) : loading ? "加载中..." : "暂无记录"}
      </div>
      <div className="mt-1 text-xs text-muted-foreground">
        {log ? `耗时 ${durationText(log.duration_ms)}${targetName ? ` · ${targetName}` : ""}` : "同步完成后会显示耗时"}
      </div>
      {log && !log.success && log.error_message ? (
        <div className="mt-1 line-clamp-2 text-xs text-danger">{log.error_message}</div>
      ) : null}
    </div>
  )
}

function AddShopDialog({
  open,
  form,
  busy,
  onOpenChange,
  onFormChange,
  onParseURL,
  onSave,
}: {
  open: boolean
  form: AddShopForm
  busy: string | null
  onOpenChange: (open: boolean) => void
  onFormChange: (form: AddShopForm) => void
  onParseURL: () => void
  onSave: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{"添加店铺"}</DialogTitle>
          <DialogDescription>
            {"支持直接粘贴 ldxp 店铺链接或商品链接。保存后会自动开始同步。"}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="店铺 URL" className="sm:col-span-2">
            <div className="flex gap-2">
              <Input
                value={form.site_url}
                onChange={(event) => onFormChange({ ...form, site_url: event.target.value })}
                placeholder="https://pay.ldxp.cn/shop/7FCVUA4X 或 https://www.ldxp.cn/item/9l814h"
              />
              <Button type="button" variant="outline" onClick={onParseURL} disabled={busy === "parse-shop-url"}>
                {busy === "parse-shop-url" ? <Loader2 className="size-4 animate-spin" /> : "解析"}
              </Button>
            </div>
          </Field>
          <Field label="名称">
            <Input
              value={form.name}
              onChange={(event) => onFormChange({ ...form, name: event.target.value })}
              placeholder="可留空，默认使用店铺名或 Token"
            />
          </Field>
          <Field label="低库存阈值">
            <Input
              type="number"
              value={form.stock_threshold}
              onChange={(event) => onFormChange({ ...form, stock_threshold: Number(event.target.value) || 0 })}
            />
          </Field>
          <Field label="Base URL">
            <Input
              value={form.base_url}
              onChange={(event) => onFormChange({ ...form, base_url: event.target.value })}
              placeholder="解析后自动填写"
            />
          </Field>
          <Field label="Token">
            <Input
              value={form.token}
              onChange={(event) => onFormChange({ ...form, token: event.target.value })}
              placeholder="解析后自动填写"
            />
          </Field>
        </div>
        <div className="grid gap-2 rounded-lg border border-border bg-muted/20 p-3 sm:grid-cols-2">
          <CheckRow
            id="shop-goods-add-notify"
            label="启用通知"
            checked={form.notify_enabled}
            onChange={(checked) => onFormChange({ ...form, notify_enabled: checked })}
          />
          <CheckRow
            id="shop-goods-add-proxy"
            label="使用代理"
            checked={form.proxy_enabled}
            onChange={(checked) => onFormChange({ ...form, proxy_enabled: checked })}
          />
        </div>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {"取消"}
          </Button>
          <Button type="button" onClick={onSave} disabled={busy === "save-shop" || busy === "parse-shop-url"}>
            {busy === "save-shop" ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
            {"保存并同步"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function Field({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

function CheckRow({
  id,
  label,
  checked,
  onChange,
}: {
  id: string
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} onCheckedChange={(value) => onChange(value === true)} />
      <label htmlFor={id} className="cursor-pointer select-none text-sm">
        {label}
      </label>
    </div>
  )
}

function ClearableInput({
  value,
  onChange,
  onClear,
  onSubmit,
  placeholder,
  history = [],
}: {
  value: string
  onChange: (value: string) => void
  onClear: () => void
  onSubmit: () => void
  placeholder: string
  history?: string[]
}) {
  const [open, setOpen] = useState(false)
  const showHistory = open && history.length > 0

  return (
    <Popover open={showHistory} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={value}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return
              event.preventDefault()
              setOpen(false)
              onSubmit()
            }}
            onFocus={() => setOpen(true)}
            onClick={() => setOpen(true)}
            className="pl-9 pr-10"
            placeholder={placeholder}
            autoComplete="off"
          />
          {value.trim() ? (
            <button
              type="button"
              onClick={() => {
                onClear()
                setOpen(false)
              }}
              className="absolute right-2 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
              aria-label="清除"
            >
              <X className="size-4" />
            </button>
          ) : null}
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={6}
        className="w-[var(--radix-popover-trigger-width)] p-1"
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        <div className="px-2 py-1.5 text-[11px] font-medium text-muted-foreground">最近查询</div>
        <div className="max-h-56 overflow-y-auto">
          {history.map((item, index) => (
            <button
              key={`${item}-${index}`}
              type="button"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                onChange(item)
                setOpen(false)
              }}
              className="block w-full truncate rounded-sm px-2 py-1.5 text-left text-sm transition hover:bg-accent hover:text-accent-foreground"
              title={item}
            >
              {item}
            </button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}
