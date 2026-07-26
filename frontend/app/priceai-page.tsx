import { useDeferredValue, useEffect, useState } from "react"
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  Eye,
  Link2,
  Loader2,
  Radar,
  RefreshCw,
  Search,
  ShieldAlert,
  SlidersHorizontal,
  Target,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiFetch } from "@/lib/api"
import type {
  PriceAIProductListItem,
  PriceAIQuote,
  PriceAIQuoteGroup,
  PriceAIQuoteSort,
  PriceAIRiskFeedback,
  PriceAIShopTargetResult,
  PriceAIWatchTarget,
} from "@/lib/api-types"
import { dateTime, decimal, relativeTime } from "@/lib/format"
import {
  type PriceAIProductFilters,
  usePriceAIOffers,
  usePriceAIProduct,
  usePriceAIProducts,
  usePriceAIStatus,
} from "@/lib/queries"
import { cn } from "@/lib/utils"

const PAGE_SIZE = 20

function priceText(value?: number | null, currency?: string | null) {
  if (value == null || !Number.isFinite(value)) return "—"
  return `${currency?.trim() || ""} ${decimal(value, 4)}`.trim()
}

function riskSummary(record: PriceAIRiskFeedback) {
  try {
    const values = JSON.parse(record.reasons_json || record.summaries_json || "[]")
    if (Array.isArray(values)) return values.filter((value): value is string => typeof value === "string").slice(0, 2)
  } catch {
    // Displaying the source status is still useful when a future payload changes shape.
  }
  return []
}

function sourceHealthTone(stale: boolean, failures: number) {
  if (stale || failures >= 3) return "border-warning/35 bg-warning/10"
  return "border-border bg-card"
}

export default function PriceAIPage() {
  const [page, setPage] = useState(1)
  const [filters, setFilters] = useState<PriceAIProductFilters>({
    watchState: "all",
    availability: "all",
    sort: "latest_seen_desc",
  })
  const [knownPlatforms, setKnownPlatforms] = useState<string[]>([])
  const [knownProductTypes, setKnownProductTypes] = useState<string[]>([])
  const [search, setSearch] = useState("")
  const deferredSearch = useDeferredValue(search)
  const [selectedSlug, setSelectedSlug] = useState<string | null>(null)
  const [board, setBoard] = useState("default")
  const [quoteSearch, setQuoteSearch] = useState("")
  const deferredQuoteSearch = useDeferredValue(quoteSearch)
  const [quoteSort, setQuoteSort] = useState<PriceAIQuoteSort>("price_asc")
  const [expandedGroups, setExpandedGroups] = useState<string[]>([])
  const [syncing, setSyncing] = useState(false)
  const [refreshingRisk, setRefreshingRisk] = useState(false)
  const [savingTarget, setSavingTarget] = useState(false)
  const [targetPrice, setTargetPrice] = useState("")
  const [targetCurrency, setTargetCurrency] = useState("")

  const activeFilters = { ...filters, query: deferredSearch }
  const status = usePriceAIStatus()
  const products = usePriceAIProducts(page, PAGE_SIZE, activeFilters)
  const detail = usePriceAIProduct(selectedSlug)
  const offers = usePriceAIOffers(selectedSlug, board, deferredQuoteSearch, quoteSort)

  useEffect(() => {
    setPage(1)
  }, [deferredSearch, filters.platform, filters.productType, filters.watchState, filters.availability, filters.sort])

  useEffect(() => {
    const target = detail.data?.watch_target
    setTargetPrice(target?.target_price == null ? "" : String(target.target_price))
    setTargetCurrency(target?.target_price_currency ?? detail.data?.product.lowest_price_currency ?? "")
    setBoard("default")
    setQuoteSearch("")
    setExpandedGroups([])
  }, [detail.data?.product.slug, detail.data?.watch_target?.id])

  const items = products.data?.items ?? []
  const platforms = Array.from(
    new Set(items.map((item) => item.platform).filter((value): value is string => Boolean(value))),
  ).sort()
  const productTypes = Array.from(
    new Set(items.map((item) => item.product_type).filter((value): value is string => Boolean(value))),
  ).sort()
  const platformKey = platforms.join("\u0000")
  const productTypeKey = productTypes.join("\u0000")
  const platformFacetValues = mergeFacetValues(knownPlatforms, platforms)
  const productTypeFacetValues = mergeFacetValues(knownProductTypes, productTypes)
  const selectedProduct = detail.data?.product
  const selectedTarget = detail.data?.watch_target

  useEffect(() => {
    if (!platformKey) return
    setKnownPlatforms((current) => mergeFacetValues(current, platformKey.split("\u0000")))
  }, [platformKey])

  useEffect(() => {
    if (!productTypeKey) return
    setKnownProductTypes((current) => mergeFacetValues(current, productTypeKey.split("\u0000")))
  }, [productTypeKey])

  async function runSync() {
    setSyncing(true)
    try {
      await apiFetch("/priceai/sync", { method: "POST" })
      toast.success("PriceAI Feed 同步完成")
      status.refetch()
      products.refetch()
      detail.refetch()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "同步失败")
    } finally {
      setSyncing(false)
    }
  }

  async function runRiskRefresh() {
    setRefreshingRisk(true)
    try {
      await apiFetch("/priceai/risk-refresh", { method: "POST" })
      toast.success("风险标记刷新已完成")
      status.refetch()
      products.refetch()
      detail.refetch()
      offers.refetch()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "风险标记刷新失败")
    } finally {
      setRefreshingRisk(false)
    }
  }

  async function saveWatchTarget(patch: Record<string, unknown>, creating = false) {
    if (!selectedProduct) return
    setSavingTarget(true)
    try {
      if (creating) {
        await apiFetch("/priceai/watch-targets", {
          method: "POST",
          body: JSON.stringify({ product_id: selectedProduct.id, monitor_enabled: true, notify_enabled: false, ...patch }),
        })
      } else if (selectedTarget) {
        await apiFetch(`/priceai/watch-targets/${selectedTarget.id}`, {
          method: "PUT",
          body: JSON.stringify(patch),
        })
      }
      toast.success(creating ? "已加入 PriceAI 监控" : "监控设置已更新")
      detail.refetch()
      products.refetch()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存监控设置失败")
    } finally {
      setSavingTarget(false)
    }
  }

  async function removeWatchTarget() {
    if (!selectedTarget) return
    setSavingTarget(true)
    try {
      await apiFetch(`/priceai/watch-targets/${selectedTarget.id}`, { method: "DELETE" })
      toast.success("已停止 PriceAI 监控")
      detail.refetch()
      products.refetch()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "停止监控失败")
    } finally {
      setSavingTarget(false)
    }
  }

  async function saveTargetPrice() {
    if (!selectedTarget) return
    const value = Number(targetPrice)
    if (!Number.isFinite(value) || value < 0 || !targetCurrency.trim()) {
      toast.error("请输入有效的目标价与币种")
      return
    }
    await saveWatchTarget({ target_price: value, target_price_currency: targetCurrency.trim() })
  }

  function toggleExpanded(group: PriceAIQuoteGroup) {
    const key = group.normalized_title || group.title
    setExpandedGroups((current) => current.includes(key) ? current.filter((item) => item !== key) : [...current, key])
  }

  return (
    <div className="space-y-4">
      <section className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <Radar className="size-5 text-sky-600" />
            <h2 className="text-lg font-semibold text-foreground">PriceAI 雷达</h2>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">导入 PriceAI 公开 Price Radar Feed，监控本地目录与公开榜单变化。</p>
        </div>
        <div className="flex items-center gap-2">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="outline" size="icon" onClick={runRiskRefresh} disabled={refreshingRisk} aria-label="刷新风险标记">
                <ShieldAlert className={cn("size-4", refreshingRisk && "animate-pulse")} />
              </Button>
            </TooltipTrigger>
            <TooltipContent>刷新 PriceAI 页面风险标记，不会刷新价格</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button size="icon" onClick={runSync} disabled={syncing} aria-label="立即同步 PriceAI Feed">
                <RefreshCw className={cn("size-4", syncing && "animate-spin")} />
              </Button>
            </TooltipTrigger>
            <TooltipContent>立即同步 PriceAI Feed</TooltipContent>
          </Tooltip>
        </div>
      </section>

      <SourceHealth status={status.data} loading={status.loading} error={status.error} />

      <Card>
        <CardHeader className="gap-3 pb-3">
          <CardTitle className="flex items-center gap-2 text-sm"><SlidersHorizontal className="size-4" />目录筛选</CardTitle>
          <div className="space-y-3">
            <div className="relative max-w-xl">
              <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="名称、规格、平台" className="pl-8" />
            </div>
            <div className="grid gap-x-5 gap-y-3 xl:grid-cols-3">
              <FilterCardGroup label="平台" value={filters.platform ?? "all"} onValueChange={(platform) => setFilters((current) => ({ ...current, platform }))} options={facetOptions("全部平台", platformFacetValues, filters.platform)} />
              <FilterCardGroup label="类型" value={filters.productType ?? "all"} onValueChange={(productType) => setFilters((current) => ({ ...current, productType }))} options={facetOptions("全部类型", productTypeFacetValues, filters.productType)} />
              <FilterCardGroup label="监控状态" value={filters.watchState ?? "all"} onValueChange={(watchState) => setFilters((current) => ({ ...current, watchState: watchState as PriceAIProductFilters["watchState"] }))} options={[{ value: "all", label: "全部" }, { value: "watched", label: "已监控" }, { value: "unwatched", label: "未监控" }]} />
              <FilterCardGroup label="可用性" value={filters.availability ?? "all"} onValueChange={(availability) => setFilters((current) => ({ ...current, availability: availability as PriceAIProductFilters["availability"] }))} options={[{ value: "all", label: "全部" }, { value: "in_stock", label: "有库存" }, { value: "out_of_stock", label: "无库存" }]} />
              <FilterCardGroup label="排序" value={filters.sort ?? "latest_seen_desc"} onValueChange={(sort) => setFilters((current) => ({ ...current, sort: sort as PriceAIProductFilters["sort"] }))} options={[{ value: "latest_seen_desc", label: "最新快照" }, { value: "lowest_price_asc", label: "最低价 ↑" }, { value: "lowest_price_desc", label: "最低价 ↓" }, { value: "in_stock_desc", label: "库存优先" }, { value: "name_asc", label: "名称" }]} />
              <label className="flex min-h-9 cursor-pointer items-center gap-2 self-end rounded-md border border-border px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground">
                <Switch checked={Boolean(filters.includeMissing)} onCheckedChange={(includeMissing) => setFilters((current) => ({ ...current, includeMissing }))} />
                <span>包含最新 Feed 中缺失的商品</span>
              </label>
            </div>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          <CatalogTable items={items} loading={products.loading} error={products.error} selectedSlug={selectedSlug} onSelect={setSelectedSlug} />
          <Pager page={products.data?.page ?? page} pages={products.data?.pages ?? 1} total={products.data?.total ?? 0} onPageChange={setPage} />
        </CardContent>
      </Card>

      {selectedSlug ? (
        <Card>
          <CardHeader className="gap-2 border-b border-border pb-4">
            <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-start">
              <div className="min-w-0">
                <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                  {detail.data?.product.name ?? selectedSlug}
                  {detail.data?.product.platform ? <Badge variant="outline">{detail.data.product.platform}</Badge> : null}
                  {detail.data?.product.product_type ? <Badge variant="secondary">{detail.data.product.product_type}</Badge> : null}
                </CardTitle>
                {detail.data?.product.spec ? <p className="mt-1 text-sm text-muted-foreground">{detail.data.product.spec}</p> : null}
              </div>
              {detail.data?.source_product_url ? <Button asChild variant="outline" size="sm" className="shrink-0"><a href={detail.data.source_product_url} target="_blank" rel="noopener noreferrer"><ExternalLink className="size-3.5" />PriceAI 页面</a></Button> : null}
            </div>
          </CardHeader>
          <CardContent className="space-y-5 pt-5">
            {detail.error ? <InlineError message={detail.error} /> : null}
            {detail.loading && !detail.data ? <LoadingLine /> : null}
            {detail.data ? (
              <>
                <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(18rem,0.72fr)]">
                  <AggregatePanel product={detail.data.product} />
                  <WatchTargetPanel target={selectedTarget} price={targetPrice} currency={targetCurrency} saving={savingTarget} onCreate={() => saveWatchTarget({}, true)} onRemove={removeWatchTarget} onToggleMonitor={(monitor_enabled) => saveWatchTarget({ monitor_enabled })} onToggleNotify={(notify_enabled) => saveWatchTarget({ notify_enabled })} onPriceChange={setTargetPrice} onCurrencyChange={setTargetCurrency} onSavePrice={saveTargetPrice} />
                </div>

                <section className="space-y-3">
                  <div className="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
                    <div>
                      <h3 className="text-sm font-semibold">公开榜单报价</h3>
                      <p className="mt-1 text-xs text-muted-foreground">{offers.data?.coverage ?? detail.data.coverage}</p>
                    </div>
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <Select value={board} onValueChange={setBoard}>
                        <SelectTrigger className="w-full sm:w-44"><SelectValue /></SelectTrigger>
                        <SelectContent><SelectItem value="default">默认 Top 5</SelectItem><SelectItem value="all">所有公开榜单</SelectItem>{detail.data.presets.map((preset) => <SelectItem key={preset.remote_id} value={`preset:${preset.remote_id}`}>{preset.label}</SelectItem>)}</SelectContent>
                      </Select>
                      <Select value={quoteSort} onValueChange={(value) => setQuoteSort(value as PriceAIQuoteSort)}>
                        <SelectTrigger className="w-full sm:w-36"><SelectValue /></SelectTrigger>
                        <SelectContent><SelectItem value="price_asc">价格升序</SelectItem><SelectItem value="price_desc">价格降序</SelectItem><SelectItem value="rank_asc">榜单排名</SelectItem><SelectItem value="risk_first">风险标记优先</SelectItem></SelectContent>
                      </Select>
                      <div className="relative"><Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" /><Input value={quoteSearch} onChange={(event) => setQuoteSearch(event.target.value)} placeholder="报价或商家" className="pl-8" /></div>
                    </div>
                  </div>
                  <QuoteGroups groups={offers.data?.items ?? []} loading={offers.loading} error={offers.error} expandedGroups={expandedGroups} onToggle={toggleExpanded} />
                </section>
              </>
            ) : null}
          </CardContent>
        </Card>
      ) : (
        <Card className="border-dashed"><CardContent className="flex min-h-36 flex-col items-center justify-center gap-2 text-center"><Radar className="size-6 text-muted-foreground" /><p className="text-sm font-medium">选择一个目录商品以查看公开报价与监控状态</p><p className="text-xs text-muted-foreground">当前目录来自已同步的 PriceAI Feed。</p></CardContent></Card>
      )}
    </div>
  )
}

function SourceHealth({ status, loading, error }: { status: ReturnType<typeof usePriceAIStatus>["data"]; loading: boolean; error: string | null }) {
  if (error) return <InlineError message={error} />
  if (loading && !status) return <LoadingLine />
  if (!status) return null
  const state = status.state
  return (
    <div className={cn("grid gap-3 rounded-lg border p-3 sm:grid-cols-2 xl:grid-cols-5", sourceHealthTone(state.feed_stale, state.consecutive_failures))}>
      <HealthItem label="当前快照" value={state.snapshot_id || "尚未导入"} detail={state.generated_at ? `生成于 ${dateTime(state.generated_at)}` : undefined} />
      <HealthItem label="最后成功" value={relativeTime(state.last_success_at)} detail={state.last_success_at ? dateTime(state.last_success_at) : undefined} />
      <HealthItem label="Feed 状态" value={state.feed_stale ? "标记为过期" : "正常"} warning={state.feed_stale} detail={state.last_error || undefined} />
      <HealthItem label="连续失败" value={String(state.consecutive_failures)} warning={state.consecutive_failures >= 3} detail={status.feed_log?.error_message || undefined} />
      <HealthItem label="风险刷新" value={status.risk_log?.success ? relativeTime(status.risk_log.finished_at) : "尚无成功记录"} detail={status.risk_log?.error_message || undefined} />
    </div>
  )
}

function HealthItem({ label, value, detail, warning = false }: { label: string; value: string; detail?: string; warning?: boolean }) {
  return <div className="min-w-0"><p className="text-[11px] text-muted-foreground">{label}</p><p className={cn("truncate text-sm font-semibold", warning && "text-warning")}>{value}</p>{detail ? <p className="truncate text-[11px] text-muted-foreground" title={detail}>{detail}</p> : null}</div>
}

type FilterCardOption = { value: string; label: string }

function mergeFacetValues(current: string[], values: string[]) {
  const next = Array.from(new Set([...current, ...values])).sort((a, b) => a.localeCompare(b, "zh-CN"))
  return next.length === current.length && next.every((value, index) => value === current[index]) ? current : next
}

function facetOptions(allLabel: string, values: string[], selected?: string) {
  const options = new Set(values)
  if (selected && selected !== "all") options.add(selected)
  return [{ value: "all", label: allLabel }, ...Array.from(options).sort((a, b) => a.localeCompare(b, "zh-CN")).map((value) => ({ value, label: value }))]
}

function FilterCardGroup({ label, value, options, onValueChange }: { label: string; value: string; options: FilterCardOption[]; onValueChange: (value: string) => void }) {
  return (
    <fieldset className="min-w-0">
      <legend className="mb-1.5 text-xs font-medium text-muted-foreground">{label}</legend>
      <RadioGroup value={value} onValueChange={onValueChange} aria-label={label} className="flex flex-wrap gap-1.5">
        {options.map((option) => {
          const selected = option.value === value
          return (
            <label key={option.value} className={cn(
              "flex min-h-9 cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm transition-colors",
              selected
                ? "border-primary bg-primary/10 text-foreground shadow-xs"
                : "border-border bg-background text-muted-foreground hover:border-foreground/25 hover:bg-muted/50 hover:text-foreground",
            )}>
              <RadioGroupItem value={option.value} className="size-3.5" />
              <span className="max-w-full truncate">{option.label}</span>
            </label>
          )
        })}
      </RadioGroup>
    </fieldset>
  )
}

function CatalogTable({ items, loading, error, selectedSlug, onSelect }: { items: PriceAIProductListItem[]; loading: boolean; error: string | null; selectedSlug: string | null; onSelect: (slug: string) => void }) {
  if (error) return <div className="p-4"><InlineError message={error} /></div>
  if (loading && items.length === 0) return <div className="p-4"><LoadingLine /></div>
  if (items.length === 0) return <div className="flex min-h-40 flex-col items-center justify-center gap-2 p-4 text-center"><Search className="size-5 text-muted-foreground" /><p className="text-sm font-medium">没有匹配的 Feed 商品</p><p className="text-xs text-muted-foreground">调整本地目录筛选，或先执行一次 PriceAI Feed 同步。</p></div>
  return <div className="overflow-x-auto"><Table className="min-w-215"><TableHeader><TableRow><TableHead>商品</TableHead><TableHead>最低公开价</TableHead><TableHead>库存 / 报价</TableHead><TableHead>快照</TableHead><TableHead>监控</TableHead><TableHead>风险数据</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id} data-state={selectedSlug === item.slug ? "selected" : undefined} className="cursor-pointer" onClick={() => onSelect(item.slug)}><TableCell><div className="font-medium">{item.name}</div><div className="mt-1 flex gap-1"><Badge variant="outline">{item.platform || "未分类"}</Badge>{item.product_type ? <Badge variant="secondary">{item.product_type}</Badge> : null}{item.missing_from_latest_at ? <Badge className="bg-warning/15 text-warning hover:bg-warning/15" variant="outline">已缺失</Badge> : null}</div></TableCell><TableCell className="font-medium">{priceText(item.lowest_price, item.lowest_price_currency)}</TableCell><TableCell>{item.in_stock_count} / {item.offer_count}</TableCell><TableCell><span title={dateTime(item.product_snapshot_generated_at)}>{relativeTime(item.product_snapshot_generated_at)}</span></TableCell><TableCell>{item.watched ? <Badge className="border-success/30 bg-success/10 text-success" variant="outline">已监控</Badge> : <span className="text-muted-foreground">未监控</span>}</TableCell><TableCell>{item.risk_fetched_at ? <span title={dateTime(item.risk_fetched_at)}>{relativeTime(item.risk_fetched_at)}</span> : <span className="text-muted-foreground">尚未提取</span>}</TableCell></TableRow>)}</TableBody></Table></div>
}

function Pager({ page, pages, total, onPageChange }: { page: number; pages: number; total: number; onPageChange: (page: number) => void }) {
  return <div className="flex items-center justify-between border-t border-border px-4 py-3 text-xs text-muted-foreground"><span>共 {total} 项，第 {page}/{pages} 页</span><div className="flex gap-2"><Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPageChange(page - 1)}>上一页</Button><Button variant="outline" size="sm" disabled={page >= pages} onClick={() => onPageChange(page + 1)}>下一页</Button></div></div>
}

function AggregatePanel({ product }: { product: NonNullable<ReturnType<typeof usePriceAIProduct>["data"]>["product"] }) {
  return <section className="rounded-lg border border-border p-4"><h3 className="text-sm font-semibold">当前聚合</h3><div className="mt-3 grid grid-cols-3 gap-3"><Metric label="最低公开价" value={priceText(product.lowest_price, product.lowest_price_currency)} /><Metric label="有库存报价" value={String(product.in_stock_count)} /><Metric label="公开报价数" value={String(product.offer_count)} /></div><p className="mt-3 text-xs text-muted-foreground">商品快照 {dateTime(product.product_snapshot_generated_at)}，仅反映 PriceAI 已发布的 Feed 聚合值。</p></section>
}

function Metric({ label, value }: { label: string; value: string }) { return <div><p className="text-[11px] text-muted-foreground">{label}</p><p className="mt-1 truncate text-sm font-semibold">{value}</p></div> }

function WatchTargetPanel({ target, price, currency, saving, onCreate, onRemove, onToggleMonitor, onToggleNotify, onPriceChange, onCurrencyChange, onSavePrice }: { target?: PriceAIWatchTarget | null; price: string; currency: string; saving: boolean; onCreate: () => void; onRemove: () => void; onToggleMonitor: (value: boolean) => void; onToggleNotify: (value: boolean) => void; onPriceChange: (value: string) => void; onCurrencyChange: (value: string) => void; onSavePrice: () => void }) {
  return <section className="rounded-lg border border-border p-4"><div className="flex items-center justify-between gap-3"><div><h3 className="flex items-center gap-2 text-sm font-semibold"><Target className="size-4 text-sky-600" />监控目标</h3><p className="mt-1 text-xs text-muted-foreground">通知独立于店铺监控，默认关闭。</p></div>{target ? <Button variant="ghost" size="icon" className="text-muted-foreground hover:text-destructive" disabled={saving} onClick={onRemove} aria-label="停止监控"><Trash2 className="size-4" /></Button> : null}</div>{target ? <div className="mt-4 space-y-3"><ToggleRow label="启用价格与库存监控" description="根据当前快照计算降价、目标价和库存状态。" checked={target.monitor_enabled} disabled={saving} onCheckedChange={onToggleMonitor} /><ToggleRow label="发送 PriceAI 通知" description="价格下降、目标价、库存和 Feed 健康变化" checked={target.notify_enabled} disabled={saving} onCheckedChange={onToggleNotify} /><div className="space-y-2 border-t border-border pt-3"><Label className="text-xs">目标公开价</Label><div className="flex gap-2"><Input type="number" min="0" step="any" value={price} onChange={(event) => onPriceChange(event.target.value)} placeholder="可选" /><Input value={currency} onChange={(event) => onCurrencyChange(event.target.value)} placeholder="币种" className="w-24" /><Button size="sm" variant="outline" disabled={saving || !price} onClick={onSavePrice}>保存</Button></div>{target.target_price != null ? <p className="text-[11px] text-muted-foreground">当前阈值：{priceText(target.target_price, target.target_price_currency)}</p> : null}</div></div> : <div className="mt-4 rounded-md border border-dashed border-border p-3"><p className="text-sm font-medium">尚未监控此商品</p><p className="mt-1 text-xs text-muted-foreground">建立目标会以当前快照作为基线，不会补发历史通知。</p><Button className="mt-3" size="sm" onClick={onCreate} disabled={saving}><Eye className="size-3.5" />开始监控</Button></div>}</section>
}

function ToggleRow({ label, description, checked, disabled, onCheckedChange }: { label: string; description: string; checked: boolean; disabled: boolean; onCheckedChange: (value: boolean) => void }) { return <div className="flex items-center justify-between gap-3"><div><p className="text-sm font-medium">{label}</p><p className="text-[11px] text-muted-foreground">{description}</p></div><Switch checked={checked} disabled={disabled} onCheckedChange={onCheckedChange} /></div> }

function QuoteGroups({ groups, loading, error, expandedGroups, onToggle }: { groups: PriceAIQuoteGroup[]; loading: boolean; error: string | null; expandedGroups: string[]; onToggle: (group: PriceAIQuoteGroup) => void }) {
  if (error) return <InlineError message={error} />
  if (loading && groups.length === 0) return <LoadingLine />
  if (groups.length === 0) return <div className="rounded-lg border border-dashed border-border p-5 text-center text-sm text-muted-foreground">当前公开榜单没有可显示的报价。</div>
  return <div className="overflow-x-auto rounded-lg border border-border"><Table className="min-w-230"><TableHeader><TableRow><TableHead>原始报价标题</TableHead><TableHead>公开价格区间</TableHead><TableHead>报价 / 商家</TableHead><TableHead>风险标记</TableHead><TableHead>公开榜单</TableHead><TableHead className="w-20">操作</TableHead></TableRow></TableHeader><TableBody>{groups.map((group) => { const key = group.normalized_title || group.title; const expanded = expandedGroups.includes(key); return <QuoteGroupRows key={key} group={group} expanded={expanded} onToggle={() => onToggle(group)} /> })}</TableBody></Table></div>
}

function QuoteGroupRows({ group, expanded, onToggle }: { group: PriceAIQuoteGroup; expanded: boolean; onToggle: () => void }) {
  const first = group.quotes[0]
  return <><TableRow className="bg-muted/25"><TableCell><button type="button" onClick={onToggle} className="flex max-w-115 items-center gap-2 text-left font-medium hover:underline">{expanded ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}<span className="truncate">{group.title}</span></button></TableCell><TableCell>{priceText(group.min_price, group.currency)}{group.price_spread != null ? <p className="mt-1 text-[11px] text-muted-foreground">价差 {priceText(group.price_spread, group.currency)}</p> : null}</TableCell><TableCell>{group.visible_quote_count} 条 / {group.merchant_count} 商家</TableCell><TableCell>{group.risk_badge_count > 0 ? <Badge className="border-warning/30 bg-warning/10 text-warning" variant="outline">{group.risk_badge_count} 条反馈</Badge> : <span className="text-muted-foreground">无匹配标记</span>}</TableCell><TableCell>{first ? <Memberships memberships={first.memberships} /> : "—"}</TableCell><TableCell><Button variant="ghost" size="sm" onClick={onToggle}>{expanded ? "收起" : "展开"}</Button></TableCell></TableRow>{expanded ? group.quotes.map((quote) => <QuoteRow key={quote.id} quote={quote} />) : null}</>
}

function QuoteRow({ quote }: { quote: PriceAIQuote }) {
  const [monitoring, setMonitoring] = useState(false)
  async function createExactMonitor() {
    setMonitoring(true)
    try {
      const result = await apiFetch<PriceAIShopTargetResult>(`/priceai/offers/${quote.id}/shop-target`, { method: "POST" })
      if (result.created) {
        toast.success("已创建精确 LDXP 商品监控")
      } else if (result.already_included) {
        toast.message("该精确 LDXP 商品已在现有监控中")
      } else if (result.reused) {
        toast.success("已复用现有店铺监控并添加精确商品")
      } else {
        toast.success("已更新精确 LDXP 商品监控")
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建精确监控失败")
    } finally {
      setMonitoring(false)
    }
  }
  return <TableRow><TableCell className="pl-10"><div className="font-medium">{quote.title}</div><div className="mt-1 text-xs text-muted-foreground">{quote.source_store_name || quote.source_name || quote.merchant_key || "未知商家"}</div></TableCell><TableCell className="font-medium">{priceText(quote.price, quote.currency)}</TableCell><TableCell><Badge variant="outline">{quote.status || "已发布"}</Badge></TableCell><TableCell><RiskBadges feedback={quote.risk_feedback ?? []} /></TableCell><TableCell><Memberships memberships={quote.memberships} /></TableCell><TableCell><div className="flex items-center gap-1"><Tooltip><TooltipTrigger asChild><Button asChild variant="ghost" size="icon" aria-label="打开报价来源"><a href={quote.url} target="_blank" rel="noopener noreferrer"><ExternalLink className="size-3.5" /></a></Button></TooltipTrigger><TooltipContent>打开报价来源</TooltipContent></Tooltip>{quote.ldxp_eligible ? <Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" disabled={monitoring} onClick={createExactMonitor} aria-label="精确监控此 LDXP 商品">{monitoring ? <Loader2 className="size-3.5 animate-spin" /> : <Link2 className="size-3.5" />}</Button></TooltipTrigger><TooltipContent>精确监控此 LDXP 店铺商品</TooltipContent></Tooltip> : null}</div></TableCell></TableRow>
}

function Memberships({ memberships }: { memberships: PriceAIQuote["memberships"] }) { return <div className="flex max-w-44 flex-wrap gap-1">{memberships.map((item) => <Badge key={`${item.board_kind}-${item.preset_id}-${item.rank}`} variant="outline">{item.board_kind === "default" ? `Top ${item.rank}` : `预设 #${item.rank}`}</Badge>)}</div> }

function RiskBadges({ feedback }: { feedback: PriceAIRiskFeedback[] }) { if (feedback.length === 0) return <span className="text-xs text-muted-foreground">无匹配标记</span>; return <div className="space-y-1">{feedback.map((record) => <Tooltip key={`${record.scope}-${record.subject_remote_id}`}><TooltipTrigger asChild><Badge className="max-w-42 border-warning/30 bg-warning/10 text-warning" variant="outline"><AlertTriangle className="size-3" />PriceAI 商家风险</Badge></TooltipTrigger><TooltipContent className="max-w-xs"><p>用户反馈待核验 · {record.feedback_count} 条</p><p>PriceAI 页面风险标记 · {relativeTime(record.fetched_at)}</p>{riskSummary(record).length > 0 ? <p className="mt-1">{riskSummary(record).join("；")}</p> : null}</TooltipContent></Tooltip>)}</div> }

function InlineError({ message }: { message: string }) { return <div className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive"><AlertTriangle className="size-4 shrink-0" />{message}</div> }
function LoadingLine() { return <div className="flex items-center gap-2 rounded-lg border border-dashed border-border px-3 py-3 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />加载中…</div> }
