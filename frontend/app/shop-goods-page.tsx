import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { ExternalLink, Filter, Loader2, PackageSearch, RefreshCw, Search, ShoppingCart, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { DataPagination } from "@/components/ui/data-pagination"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useDebouncedValue } from "@/hooks/use-debounced-value"
import { apiFetch } from "@/lib/api"
import { useShopGoodsOverview, useShopGoodsTargetOptions } from "@/lib/queries"
import { money, relativeTime } from "@/lib/format"
import {
  readAllShopGoodsPreferences,
  type ShopGoodsStatusFilter,
  writeAllShopGoodsPreferences,
} from "@/lib/shop-goods-preferences"
import { cn } from "@/lib/utils"
import type { ShopGoodsListItem, ShopGoodsSort, ShopGoodsStatus, ShopRefreshGoodsResult } from "@/lib/api-types"

type GoodsStatusFilter = ShopGoodsStatusFilter

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

export default function ShopGoodsPage({ publicMode = false }: { publicMode?: boolean }) {
  const targets = useShopGoodsTargetOptions(publicMode)
  const [initialPreferences] = useState(readAllShopGoodsPreferences)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(initialPreferences.pageSize)
  const [targetID, setTargetID] = useState<number | null>(initialPreferences.targetID)
  const [status, setStatus] = useState<GoodsStatusFilter>(initialPreferences.status)
  const [inStockOnly, setInStockOnly] = useState(initialPreferences.inStockOnly)
  const [sort, setSort] = useState<ShopGoodsSort>(initialPreferences.sort)
  const [keyword, setKeyword] = useState(initialPreferences.keyword)
  const [categoryName, setCategoryName] = useState(initialPreferences.categoryName)
  const debouncedKeyword = useDebouncedValue(keyword)
  const debouncedCategoryName = useDebouncedValue(categoryName)
  const [refreshingGoodsKey, setRefreshingGoodsKey] = useState<string | null>(null)

  const filters = useMemo(
    () => ({
      target_id: targetID ?? undefined,
      category_name: debouncedCategoryName.trim() || undefined,
      status: inStockOnly ? "in_stock" as ShopGoodsStatus : status,
      keyword: debouncedKeyword,
      sort,
    }),
    [debouncedCategoryName, debouncedKeyword, inStockOnly, sort, status, targetID],
  )
  const goodsSearchPending = keyword !== debouncedKeyword || categoryName !== debouncedCategoryName
  const goods = useShopGoodsOverview(page, pageSize, filters, !goodsSearchPending, publicMode)
  const rows = goods.data?.items ?? []
  const total = goods.data?.total ?? 0
  const pages = goods.data?.pages ?? 1
  const activeFilters = targetID !== null || status !== "all" || sort !== "category" || keyword.trim() !== "" || categoryName.trim() !== ""

  useEffect(() => {
    writeAllShopGoodsPreferences({ targetID, status, inStockOnly, sort, keyword, categoryName, pageSize })
  }, [categoryName, inStockOnly, keyword, pageSize, sort, status, targetID])

  function resetPage(next: () => void) {
    next()
    setPage(1)
  }

  function changePageSize(nextPageSize: number) {
    setPageSize(nextPageSize)
    setPage(1)
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
      <header className="overflow-hidden rounded-2xl border border-border bg-card">
        <div className="relative grid gap-4 p-4 sm:p-5 lg:grid-cols-[1.4fr_1fr]">
          <div className="absolute inset-0 bg-[radial-gradient(circle_at_18%_20%,rgba(59,130,246,0.14),transparent_30%),radial-gradient(circle_at_86%_0%,rgba(16,185,129,0.14),transparent_28%)]" />
          <div className="relative space-y-2">
            <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-[0.24em] text-muted-foreground">
              <PackageSearch className="size-4 text-blue-600" />
              {"Shop Goods"}
            </div>
            <h1 className="text-2xl font-semibold tracking-tight text-foreground">{"商品总览"}</h1>
            <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
              {"跨所有店铺查看商品快照，按店铺、分类、状态、关键词和库存/价格排序筛选。"}
            </p>
          </div>
          <div className="relative grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-2">
            <Summary label="店铺" value={summary.shops} />
            <Summary label="匹配商品" value={summary.total} />
            <Summary label="本页有库存" value={summary.inStock} />
            <Summary label="本页低库存" value={summary.low} warn={summary.low > 0} />
          </div>
        </div>
      </header>

      <Card className="overflow-hidden">
        <div className="space-y-3 border-b border-border p-3">
          <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
            <Filter className="size-3.5" />
            <span>{"筛选和排序"}</span>
            {goods.loading || goodsSearchPending ? <span>{"加载中..."}</span> : null}
          </div>
          <div className="grid gap-2 md:grid-cols-[minmax(160px,220px)_minmax(140px,180px)_minmax(140px,180px)_auto_minmax(180px,1fr)_minmax(180px,1fr)]">
            <Select value={targetID == null ? "all" : String(targetID)} onValueChange={(value) => resetPage(() => setTargetID(value === "all" ? null : Number(value)))}>
              <SelectTrigger><SelectValue /></SelectTrigger>
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
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {Object.entries(statusLabels).map(([value, label]) => (
                  <SelectItem key={value} value={value}>{label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={sort} onValueChange={(value) => resetPage(() => setSort(value as ShopGoodsSort))}>
              <SelectTrigger><SelectValue /></SelectTrigger>
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
            <ClearableInput
              value={categoryName}
              onChange={(value) => resetPage(() => setCategoryName(value))}
              onClear={() => resetPage(() => setCategoryName(""))}
              placeholder="按分类名称筛选"
            />
            <ClearableInput
              value={keyword}
              onChange={(value) => resetPage(() => setKeyword(value))}
              onClear={() => resetPage(() => setKeyword(""))}
              placeholder="搜索商品名或 Key"
            />
          </div>
        </div>

        <div className="overflow-x-auto">
          <Table className="min-w-[1260px] table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[17%]">店铺</TableHead>
                <TableHead className="w-[27%]">商品</TableHead>
                <TableHead className="w-[15%]">分组 / 分类</TableHead>
                <TableHead className="w-[8%]">价格</TableHead>
                <TableHead className="w-[8%]">库存</TableHead>
                <TableHead className="w-[8%]">状态</TableHead>
                <TableHead className="w-[10%]">最近出现</TableHead>
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
    </section>
  )
}

function GoodsRow({
  row,
  refreshing,
  onRefreshStock,
}: {
  row: ShopGoodsListItem
  refreshing: boolean
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
          <div className="truncate font-medium" title={row.name}>{row.name}</div>
          <div className="mt-1 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
            <span className="shrink-0">{row.goods_key}</span>
          </div>
        </div>
      </TableCell>
      <TableCell className="truncate" title={row.category_name || undefined}>{row.category_name || "未分组"}</TableCell>
      <TableCell>{money(row.price)}</TableCell>
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
    <div className={cn("rounded-lg border border-border bg-background/80 p-3", warn && "border-warning/40 bg-warning/5")}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums">{value}</div>
    </div>
  )
}

function ClearableInput({
  value,
  onChange,
  onClear,
  placeholder,
}: {
  value: string
  onChange: (value: string) => void
  onClear: () => void
  placeholder: string
}) {
  return (
    <div className="relative">
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input value={value} onChange={(event) => onChange(event.target.value)} className="pl-9 pr-10" placeholder={placeholder} />
      {value.trim() ? (
        <button
          type="button"
          onClick={onClear}
          className="absolute right-2 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
          aria-label="清除"
        >
          <X className="size-4" />
        </button>
      ) : null}
    </div>
  )
}
