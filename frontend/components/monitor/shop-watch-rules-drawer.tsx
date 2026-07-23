import { useEffect, useMemo, useRef, useState } from "react"
import { AlertTriangle, Bell, Edit3, Loader2, Plus, Save, Search, Star, Trash2, X } from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type {
  ShopGoodsChangeEvent,
  ShopGoodsSnapshot,
  ShopWatchRule,
  ShopWatchRuleInput,
  ShopWatchRulePreview,
} from "@/lib/api-types"

type RuleForm = {
  name: string
  enabled: boolean
  goodsKeys: string
  categoryIDs: string
  categoryNames: string
  keywords: string
  excludeKeywords: string
  events: ShopGoodsChangeEvent[]
  stockThreshold: number
}

export type GlobalShopWatchSeed = Pick<ShopGoodsSnapshot, "goods_key" | "name"> & {
  nonce: number
}

const defaultEvents: ShopGoodsChangeEvent[] = ["stock_changed", "stock_low", "goods_restocked"]

const emptyRuleForm: RuleForm = {
  name: "",
  enabled: true,
  goodsKeys: "",
  categoryIDs: "",
  categoryNames: "",
  keywords: "",
  excludeKeywords: "",
  events: defaultEvents,
  stockThreshold: 1,
}

const eventOptions: Array<{ value: ShopGoodsChangeEvent; label: string; tone: string }> = [
  { value: "goods_restocked", label: "补货", tone: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700" },
  { value: "stock_low", label: "低库存", tone: "border-amber-500/30 bg-amber-500/10 text-amber-700" },
  { value: "stock_changed", label: "库存变化", tone: "border-sky-500/30 bg-sky-500/10 text-sky-700" },
  { value: "price_changed", label: "价格变化", tone: "border-blue-500/30 bg-blue-500/10 text-blue-700" },
  { value: "goods_added", label: "新增", tone: "border-teal-500/30 bg-teal-500/10 text-teal-700" },
  { value: "goods_removed", label: "消失", tone: "border-orange-500/30 bg-orange-500/10 text-orange-700" },
  { value: "monitor_failed", label: "失败", tone: "border-red-500/30 bg-red-500/10 text-red-700" },
]

export function GlobalShopWatchRulesDrawer({
  open,
  onOpenChange,
  rules,
  loading,
  seed,
  targetNames,
  legacyRuleCount,
  onRulesChanged,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  rules: ShopWatchRule[]
  loading: boolean
  seed: GlobalShopWatchSeed | null
  targetNames: Record<number, string>
  legacyRuleCount: number
  onRulesChanged: () => void
}) {
  const [editing, setEditing] = useState<ShopWatchRule | null>(null)
  const [form, setForm] = useState<RuleForm>(emptyRuleForm)
  const [saving, setSaving] = useState(false)
  const [preview, setPreview] = useState<ShopWatchRulePreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const activeOpenRef = useRef(false)

  useEffect(() => {
    if (!open) {
      activeOpenRef.current = false
      return
    }
    if (activeOpenRef.current) return
    activeOpenRef.current = true
    setEditing(null)
    setForm(emptyRuleForm)
    setPreview(null)
    setPreviewLoading(false)
  }, [open])

  useEffect(() => {
    if (!open || !seed) return
    setEditing(null)
    setForm({
      ...emptyRuleForm,
      name: `全局关注 ${seed.name || seed.goods_key}`,
      goodsKeys: seed.goods_key,
    })
  }, [open, seed?.nonce])

  useEffect(() => {
    if (!open) {
      setPreview(null)
      setPreviewLoading(false)
      return
    }
    const controller = new AbortController()
    let active = true
    const timer = window.setTimeout(() => {
      setPreviewLoading(true)
      apiFetch<ShopWatchRulePreview>("/shop-watch-rules/preview", {
        method: "POST",
        body: JSON.stringify(formToInput(form)),
        signal: controller.signal,
      })
        .then((nextPreview) => {
          if (active) setPreview(nextPreview)
        })
        .catch(() => {
          if (active && !controller.signal.aborted) setPreview(null)
        })
        .finally(() => {
          if (active) setPreviewLoading(false)
        })
    }, 250)
    return () => {
      active = false
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [form, open])

  const activeRules = rules.filter((rule) => rule.enabled)
  const totalExplicitGoods = useMemo(
    () => new Set(rules.flatMap((rule) => parseJSONList(rule.goods_keys_json))).size,
    [rules],
  )

  function editRule(rule: ShopWatchRule) {
    setEditing(rule)
    setForm(formFromRule(rule))
  }

  function resetForm() {
    setEditing(null)
    setForm(emptyRuleForm)
  }

  async function saveRule() {
    setSaving(true)
    try {
      const input = formToInput(form)
      const path = editing
        ? `/shop-watch-rules/${editing.id}`
        : "/shop-watch-rules"
      await apiFetch<ShopWatchRule>(path, {
        method: editing ? "PUT" : "POST",
        body: JSON.stringify(input),
      })
      toast.success(editing ? "关注规则已更新" : "关注规则已创建")
      resetForm()
      onRulesChanged()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "保存关注规则失败")
    } finally {
      setSaving(false)
    }
  }

  async function deleteRule(rule: ShopWatchRule) {
    if (!window.confirm(`删除关注规则「${rule.name}」？`)) return
    setSaving(true)
    try {
      await apiFetch(`/shop-watch-rules/${rule.id}`, { method: "DELETE" })
      if (editing?.id === rule.id) resetForm()
      toast.success("关注规则已删除")
      onRulesChanged()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除关注规则失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="h-[100dvh] w-full gap-0 overflow-hidden p-0 sm:max-w-3xl">
        <SheetHeader className="shrink-0 border-b border-border bg-[radial-gradient(circle_at_20%_0%,rgba(16,185,129,0.16),transparent_28%),linear-gradient(135deg,rgba(15,23,42,0.04),transparent)] p-4 sm:p-5">
          <div className="flex flex-col gap-3 pr-8 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
            <div>
              <SheetTitle className="flex items-center gap-2 text-lg">
                <Bell className="size-5 text-emerald-600" />
                {"全局关注规则"}
              </SheetTitle>
              <SheetDescription className="mt-1">
                {"规则应用于所有店铺；同步落库后命中规则才会进入全局通知分发。通知渠道和事件订阅在通知渠道页面配置。"}
              </SheetDescription>
            </div>
          </div>
          <div className="mt-4 grid grid-cols-3 gap-2">
            <Metric label="启用规则" value={activeRules.length} />
            <Metric label="规则总数" value={rules.length} />
            <Metric label="精确商品" value={totalExplicitGoods} />
          </div>
          {legacyRuleCount > 0 ? (
            <div className="mt-3 flex items-start gap-2 border-t border-warning/30 pt-3 text-xs text-muted-foreground">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />
              <span>{`检测到 ${legacyRuleCount} 条历史店铺关注规则，数据会保留，但不再参与推送。请按需要在这里创建全局规则。`}</span>
            </div>
          ) : null}
        </SheetHeader>

        <ScrollArea className="min-h-0 flex-1">
          <div className="grid gap-4 p-3 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:p-4 lg:grid-cols-[minmax(240px,0.8fr)_minmax(0,1.2fr)]">
            <div className="order-2 space-y-3 lg:order-1">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold">规则列表</h3>
                <Button size="sm" variant="outline" onClick={resetForm} className="h-8 gap-1.5">
                  <Plus className="size-3.5" />
                  {"新建"}
                </Button>
              </div>
              {loading ? (
                <Card className="flex h-24 items-center justify-center text-sm text-muted-foreground">
                  <Loader2 className="mr-2 size-4 animate-spin" />
                  {"加载规则中"}
                </Card>
              ) : null}
              {!loading && rules.length === 0 ? (
                <Card className="border-dashed p-4 text-sm text-muted-foreground">
                  {"还没有全局关注规则。新建后会对所有店铺的同步变化生效。"}
                </Card>
              ) : null}
              {rules.map((rule) => (
                <RuleCard
                  key={rule.id}
                  rule={rule}
                  active={editing?.id === rule.id}
                  onEdit={() => editRule(rule)}
                  onDelete={() => deleteRule(rule)}
                />
              ))}
            </div>

            <div className="order-1 space-y-4 lg:order-2">
              <Card className="overflow-hidden">
                <div className="border-b border-border p-4">
                  <h3 className="text-sm font-semibold">{editing ? "编辑规则" : "新建规则"}</h3>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {"精确商品、分类和关键词可以同时使用，命中任意条件就会通知。条件全空表示全店关注。"}
                  </p>
                </div>
                <div className="space-y-4 p-4">
                  <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
                    <Field label="规则名称">
                      <Input
                        value={form.name}
                        onChange={(event) => setForm({ ...form, name: event.target.value })}
                        placeholder="热门套餐补货提醒"
                      />
                    </Field>
                    <label className="flex items-end gap-2 pb-2 text-sm">
                      <Switch checked={form.enabled} onCheckedChange={(enabled) => setForm({ ...form, enabled })} />
                      {"启用"}
                    </label>
                  </div>

                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="精确商品 Key">
                      <Textarea
                        value={form.goodsKeys}
                        onChange={(event) => setForm({ ...form, goodsKeys: event.target.value })}
                        placeholder="96tin3, 7togvs"
                        className="min-h-20"
                      />
                    </Field>
                    <Field label="包含关键词">
                      <Textarea
                        value={form.keywords}
                        onChange={(event) => setForm({ ...form, keywords: event.target.value })}
                        placeholder="Claude, 月卡, Team"
                        className="min-h-20"
                      />
                    </Field>
                    <Field label="分类 ID">
                      <Textarea
                        value={form.categoryIDs}
                        onChange={(event) => setForm({ ...form, categoryIDs: event.target.value })}
                        placeholder="112879, 112880"
                        className="min-h-20"
                      />
                    </Field>
                    <Field label="分类名称">
                      <Textarea
                        value={form.categoryNames}
                        onChange={(event) => setForm({ ...form, categoryNames: event.target.value })}
                        placeholder="K12, GPTpro"
                        className="min-h-20"
                      />
                    </Field>
                  </div>

                  <Field label="排除关键词">
                    <Textarea
                      value={form.excludeKeywords}
                      onChange={(event) => setForm({ ...form, excludeKeywords: event.target.value })}
                      placeholder="赠品, 失效, 测试"
                      className="min-h-20"
                    />
                  </Field>

                  <Field label="通知事件">
                    <div className="flex flex-wrap gap-2">
                      {eventOptions.map((option) => {
                        const checked = form.events.includes(option.value)
                        return (
                          <button
                            key={option.value}
                            type="button"
                            onClick={() => setForm({ ...form, events: toggleEvent(form.events, option.value) })}
                            className={cn(
                              "rounded-full border px-3 py-1.5 text-xs font-medium transition",
                              checked ? option.tone : "border-border bg-muted/30 text-muted-foreground hover:text-foreground",
                            )}
                          >
                            {option.label}
                          </button>
                        )
                      })}
                    </div>
                  </Field>

                  <div className="grid gap-3 sm:grid-cols-[180px_1fr]">
                    <Field label="规则低库存阈值">
                      <Input
                        type="number"
                        min={0}
                        value={form.stockThreshold}
                        onChange={(event) => setForm({ ...form, stockThreshold: Number(event.target.value) || 0 })}
                      />
                    </Field>
                    <div className="rounded-xl border border-border bg-muted/20 p-3">
                      <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
                        <Search className="size-3.5" />
                        {"命中预览"}
                        {previewLoading ? <Loader2 className="size-3 animate-spin" /> : null}
                      </div>
                      <p className="mt-1 text-sm">
                        {"当前规则会命中 "}
                        <span className="font-semibold tabular-nums">{preview?.total ?? 0}</span>
                        {" 个商品"}
                      </p>
                      <div className="mt-2 flex flex-wrap gap-1.5">
                        {(preview?.items ?? []).slice(0, 10).map((item) => {
                          const targetName = targetNames[item.target_id] || `店铺 #${item.target_id}`
                          const label = `${targetName} · ${item.name || item.goods_key}`
                          return (
                            <Badge key={`${item.target_id}:${item.goods_key}`} variant="secondary" className="max-w-full truncate" title={label}>
                              {label}
                            </Badge>
                          )
                        })}
                        {preview && preview.total > preview.items.length ? (
                          <Badge variant="outline">+{preview.total - preview.items.length}</Badge>
                        ) : null}
                      </div>
                    </div>
                  </div>

                </div>
                <div className="sticky bottom-0 z-10 flex flex-col-reverse gap-2 border-t border-border bg-card/95 p-3 shadow-[0_-12px_24px_rgba(15,23,42,0.08)] backdrop-blur sm:flex-row sm:justify-end sm:p-4">
                  {editing ? (
                    <Button variant="outline" onClick={resetForm} disabled={saving} className="w-full sm:w-auto">
                      <X className="mr-2 size-4" />
                      {"取消编辑"}
                    </Button>
                  ) : null}
                  <Button onClick={saveRule} disabled={saving} className="w-full gap-2 sm:w-auto">
                    {saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}
                    {editing ? "保存规则" : "创建规则"}
                  </Button>
                </div>
              </Card>
            </div>
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}

function RuleCard({
  rule,
  active,
  onEdit,
  onDelete,
}: {
  rule: ShopWatchRule
  active: boolean
  onEdit: () => void
  onDelete: () => void
}) {
  const keys = parseJSONList(rule.goods_keys_json)
  const keywords = parseJSONList(rule.keywords_json)
  const excludeKeywords = parseJSONList(rule.exclude_keywords_json)
  const categories = [...parseJSONList(rule.category_names_json), ...parseJSONList(rule.category_ids_json)]
  const events = parseJSONList(rule.events_json)
  return (
    <Card className={cn("p-3 transition", active && "border-foreground shadow-sm", !rule.enabled && "opacity-60")}>
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Star className={cn("size-4", rule.enabled ? "fill-amber-400 text-amber-500" : "text-muted-foreground")} />
            <h4 className="truncate text-sm font-semibold">{rule.name}</h4>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {keys.length > 0 ? `精确商品 ${keys.length} 个` : "未指定精确商品"}
            {" · "}
            {keywords.length > 0 ? `关键词 ${keywords.length} 个` : "无关键词"}
            {excludeKeywords.length > 0 ? ` · 排除 ${excludeKeywords.length} 个` : ""}
          </p>
        </div>
        <div className="flex gap-1">
          <Button size="icon" variant="outline" className="size-7" onClick={onEdit}>
            <Edit3 className="size-3.5" />
          </Button>
          <Button size="icon" variant="outline" className="size-7" onClick={onDelete}>
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {events.length === 0 ? <Badge variant="secondary">全部事件</Badge> : events.map((event) => <Badge key={event} variant="secondary">{eventLabel(event)}</Badge>)}
        {categories.length > 0 ? <Badge variant="outline">分类 {categories.length}</Badge> : null}
        {excludeKeywords.slice(0, 2).map((keyword) => <Badge key={keyword} variant="outline" title={keyword}>{`排除 ${keyword}`}</Badge>)}
        {excludeKeywords.length > 2 ? <Badge variant="outline" title={excludeKeywords.join("、")}>{`+${excludeKeywords.length - 2}`}</Badge> : null}
        {rule.stock_threshold > 0 ? <Badge variant="outline">{"阈值 <= "}{rule.stock_threshold}</Badge> : null}
      </div>
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-xl border border-border bg-background/75 px-3 py-2">
      <div className="text-[10px] text-muted-foreground">{label}</div>
      <div className="text-lg font-semibold tabular-nums">{value}</div>
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

function formFromRule(rule: ShopWatchRule): RuleForm {
  return {
    name: rule.name,
    enabled: rule.enabled,
    goodsKeys: parseJSONList(rule.goods_keys_json).join(", "),
    categoryIDs: parseJSONList(rule.category_ids_json).join(", "),
    categoryNames: parseJSONList(rule.category_names_json).join(", "),
    keywords: parseJSONList(rule.keywords_json).join(", "),
    excludeKeywords: parseJSONList(rule.exclude_keywords_json).join(", "),
    events: parseJSONList(rule.events_json) as ShopGoodsChangeEvent[],
    stockThreshold: rule.stock_threshold,
  }
}

function formToInput(form: RuleForm): ShopWatchRuleInput {
  return {
    name: form.name.trim() || "未命名关注规则",
    enabled: form.enabled,
    goods_keys: csv(form.goodsKeys),
    category_ids: csv(form.categoryIDs).map(Number).filter((value) => Number.isFinite(value)),
    category_names: csv(form.categoryNames),
    keywords: csv(form.keywords),
    exclude_keywords: csv(form.excludeKeywords),
    events: form.events,
    stock_threshold: form.stockThreshold,
  }
}

function csv(raw: string): string[] {
  return raw.split(/[,\n]/).map((item) => item.trim()).filter(Boolean)
}

function parseJSONList(raw: string): string[] {
  try {
    const value = JSON.parse(raw)
    if (!Array.isArray(value)) return []
    return value.map((item) => String(item)).filter(Boolean)
  } catch {
    return []
  }
}

function toggleEvent(events: ShopGoodsChangeEvent[], event: ShopGoodsChangeEvent) {
  return events.includes(event) ? events.filter((item) => item !== event) : [...events, event]
}

function eventLabel(event: string) {
  return eventOptions.find((option) => option.value === event)?.label ?? event
}
