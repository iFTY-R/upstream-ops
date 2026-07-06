"use client"

import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react"
import { useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import {
  AlertTriangle,
  ArrowDown,
  ArrowRightLeft,
  ArrowUp,
  ChevronsUpDown,
  CheckCircle2,
  CircleDashed,
  Loader2,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Save,
  Settings2,
  Trash2,
  XCircle,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { useAutoGroupCapabilities, useAutoGroupPolicies, useAutoGroupSummary, useChannels } from "@/lib/queries"
import { apiFetch } from "@/lib/api"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { channelTypeLabel, formatRatio, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  AutoGroupCandidate,
  AutoGroupCapabilityMatrix,
  AutoGroupEvaluationLogPage,
  AutoGroupEvaluationResult,
  AutoGroupProbeModelOptions,
  AutoGroupPolicyInput,
  AutoGroupPolicyView,
  AutoGroupSummary,
  AutoGroupSwitchLogPage,
  Channel,
  ChannelAPIKey,
  ChannelAPIKeyGroup,
  ChannelAPIKeyPage,
  ProbeModelOption,
} from "@/lib/api-types"

interface FormState {
  id: number | null
  channel_id: string
  name: string
  enabled: boolean
  notify_enabled: boolean
  target_key_id: string
  target_key_name: string
  probe_key_id: string
  probe_key_name: string
  probe_model: string
  probe_timeout_seconds: string
  probe_success_cache_minutes: string
  probe_failure_retry_minutes: string
  probe_max_per_run: string
  include_groups: string[]
  exclude_groups: string[]
  include_keywords: string
  exclude_keywords: string
  min_ratio: string
  max_ratio: string
  failure_threshold: string
  circuit_duration_minutes: string
  half_open_success_threshold: string
  min_ratio_improvement_pct: string
  switch_cooldown_minutes: string
  force_switch_on_current_unhealthy: boolean
  keep_current_when_no_available: boolean
}

type CandidateAction = "disable" | "enable" | "probe" | "circuit" | "force-switch"

const wizardSteps = [
  { key: "channel", title: "渠道能力", desc: "选择已有上游并确认控制面能力" },
  { key: "target", title: "目标 Key", desc: "选择要自动切换分组的 API Key" },
  { key: "probe", title: "探测配置", desc: "配置探测 Key、模型和超时" },
  { key: "scope", title: "候选范围", desc: "限制分组、倍率和关键词" },
  { key: "guard", title: "熔断通知", desc: "设置降级、冷却和通知行为" },
  { key: "review", title: "预览保存", desc: "确认策略后写入配置" },
] as const

const statusMeta = {
  idle: { label: "未评估", cls: "bg-muted text-muted-foreground", icon: CircleDashed },
  ok: { label: "已是最优", cls: "bg-success/10 text-success ring-success/20", icon: CheckCircle2 },
  switched: { label: "已切换", cls: "bg-brand/10 text-brand ring-brand/20", icon: ArrowRightLeft },
  unavailable: { label: "无可用组", cls: "bg-warning/10 text-warning ring-warning/20", icon: AlertTriangle },
  failed: { label: "评估失败", cls: "bg-danger/10 text-danger ring-danger/20", icon: XCircle },
  disabled: { label: "已停用", cls: "bg-muted text-muted-foreground", icon: CircleDashed },
  kept: { label: "保持当前", cls: "bg-success/10 text-success ring-success/20", icon: CheckCircle2 },
  cooldown: { label: "冷却中", cls: "bg-warning/10 text-warning ring-warning/20", icon: AlertTriangle },
  probe_failed: { label: "探测失败", cls: "bg-danger/10 text-danger ring-danger/20", icon: XCircle },
}

const defaultProbeModel = "gpt-5.4"

function emptyForm(channelID = ""): FormState {
  return {
    id: null,
    channel_id: channelID,
    name: "Auto Key 智能分组",
    enabled: true,
    notify_enabled: true,
    target_key_id: "0",
    target_key_name: "auto",
    probe_key_id: "0",
    probe_key_name: "ops-probe-auto",
    probe_model: defaultProbeModel,
    probe_timeout_seconds: "15",
    probe_success_cache_minutes: "60",
    probe_failure_retry_minutes: "10",
    probe_max_per_run: "3",
    include_groups: [],
    exclude_groups: [],
    include_keywords: "",
    exclude_keywords: "不可用,维护,禁用,下架,不稳定,暂停",
    min_ratio: "",
    max_ratio: "",
    failure_threshold: "2",
    circuit_duration_minutes: "30",
    half_open_success_threshold: "1",
    min_ratio_improvement_pct: "5",
    switch_cooldown_minutes: "30",
    force_switch_on_current_unhealthy: true,
    keep_current_when_no_available: true,
  }
}

function formFromPolicy(p: AutoGroupPolicyView): FormState {
  return {
    id: p.id,
    channel_id: String(p.channel_id),
    name: p.name || "Auto Key 智能分组",
    enabled: p.enabled,
    notify_enabled: p.notify_enabled,
    target_key_id: String(p.target_key_id || 0),
    target_key_name: p.target_key_name || "auto",
    probe_key_id: String(p.probe_key_id || 0),
    probe_key_name: p.probe_key_name || "ops-probe-auto",
    probe_model: p.probe_model || defaultProbeModel,
    probe_timeout_seconds: String(p.probe_timeout_seconds || 15),
    probe_success_cache_minutes: String(p.probe_success_cache_minutes || 60),
    probe_failure_retry_minutes: String(p.probe_failure_retry_minutes || 10),
    probe_max_per_run: String(p.probe_max_per_run || 3),
    include_groups: parseJSONList(p.include_groups_json),
    exclude_groups: parseJSONList(p.exclude_groups_json),
    include_keywords: parseJSONList(p.include_keywords_json).join(", "),
    exclude_keywords: parseJSONList(p.exclude_keywords_json).join(", "),
    min_ratio: p.min_ratio > 0 ? String(p.min_ratio) : "",
    max_ratio: p.max_ratio > 0 ? String(p.max_ratio) : "",
    failure_threshold: String(p.failure_threshold || 2),
    circuit_duration_minutes: String(p.circuit_duration_minutes || 30),
    half_open_success_threshold: String(p.half_open_success_threshold || 1),
    min_ratio_improvement_pct: String(p.min_ratio_improvement_pct ?? 5),
    switch_cooldown_minutes: String(p.switch_cooldown_minutes || 30),
    force_switch_on_current_unhealthy: p.force_switch_on_current_unhealthy ?? true,
    keep_current_when_no_available: p.keep_current_when_no_available ?? true,
  }
}

export default function AutoGroupsPage() {
  const [searchParams] = useSearchParams()
  const requestedChannelID = searchParams.get("channel_id") ?? ""
  const channels = useChannels()
  const policies = useAutoGroupPolicies()
  const summary = useAutoGroupSummary()
  const refresh = useTriggerRefresh()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [form, setForm] = useState<FormState>(() => emptyForm(requestedChannelID))
  const [selectedID, setSelectedID] = useState<number | "new">("new")
  const [wizardStep, setWizardStep] = useState(0)
  const [saving, setSaving] = useState(false)
  const [evaluating, setEvaluating] = useState(false)
  const [keys, setKeys] = useState<ChannelAPIKey[]>([])
  const [keysLoading, setKeysLoading] = useState(false)
  const [groups, setGroups] = useState<ChannelAPIKeyGroup[]>([])
  const [groupsLoading, setGroupsLoading] = useState(false)
  const [probeModels, setProbeModels] = useState<ProbeModelOption[]>([{ id: defaultProbeModel, source: "default" }])
  const [probeModelsLoading, setProbeModelsLoading] = useState(false)
  const [probeModelsWarning, setProbeModelsWarning] = useState("")
  const [logs, setLogs] = useState<AutoGroupEvaluationLogPage | null>(null)
  const [switchLogs, setSwitchLogs] = useState<AutoGroupSwitchLogPage | null>(null)
  const [logsBump, setLogsBump] = useState(0)
  const [actionBusy, setActionBusy] = useState<string | null>(null)
  const [appliedChannelParam, setAppliedChannelParam] = useState<string | null>(null)
  const [reorderingID, setReorderingID] = useState<number | null>(null)

  const list = policies.data ?? []
  const selectedPolicy = selectedID === "new" ? null : list.find((p) => p.id === selectedID) ?? null
  const selectedChannel = channels.data?.find((c) => String(c.id) === form.channel_id) ?? null
  const channelID = Number(form.channel_id) || null
  const capabilities = useAutoGroupCapabilities(channelID)
  const currentCandidates = selectedPolicy?.candidates ?? []

  useEffect(() => {
    if (!policies.data) return
    if (requestedChannelID) {
      if (appliedChannelParam === requestedChannelID) return
      const existing = policies.data.find((p) => String(p.channel_id) === requestedChannelID)
      if (existing) {
        setSelectedID(existing.id)
        setForm(formFromPolicy(existing))
        setWizardStep(5)
      } else {
        setSelectedID("new")
        setForm(emptyForm(requestedChannelID))
        setWizardStep(0)
      }
      setAppliedChannelParam(requestedChannelID)
      return
    }
    setAppliedChannelParam(null)
  }, [appliedChannelParam, policies.data, requestedChannelID])

  useEffect(() => {
    const channelID = Number(form.channel_id)
    if (!channelID) {
      setKeys([])
      setGroups([])
      setProbeModels([{ id: defaultProbeModel, source: "default" }])
      setProbeModelsWarning("")
      return
    }
    let cancelled = false
    setKeysLoading(true)
    apiFetch<ChannelAPIKeyPage>(`/channels/${channelID}/api-keys?page=1&page_size=100`)
      .then((res) => {
        if (!cancelled) setKeys(res.items ?? [])
      })
      .catch((e: Error) => {
        if (!cancelled) toast.error(e.message || "读取 API Key 失败")
      })
      .finally(() => {
        if (!cancelled) setKeysLoading(false)
      })

    setGroupsLoading(true)
    apiFetch<ChannelAPIKeyGroup[]>(`/channels/${channelID}/api-keys/groups`)
      .then((res) => {
        if (!cancelled) setGroups(res ?? [])
      })
      .catch((e: Error) => {
        if (!cancelled) toast.error(e.message || "读取分组失败")
      })
      .finally(() => {
        if (!cancelled) setGroupsLoading(false)
      })

    setProbeModelsLoading(true)
    apiFetch<AutoGroupProbeModelOptions>(`/auto-groups/probe-models/${channelID}`)
      .then((res) => {
        if (cancelled) return
        setProbeModels(normalizeProbeModelOptions(res.items, res.default_model || defaultProbeModel, form.probe_model))
        setProbeModelsWarning(res.warning || "")
      })
      .catch((e: Error) => {
        if (cancelled) return
        setProbeModels(normalizeProbeModelOptions([], defaultProbeModel, form.probe_model))
        setProbeModelsWarning(e.message || "读取上游模型列表失败，已使用默认模型")
      })
      .finally(() => {
        if (!cancelled) setProbeModelsLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [form.channel_id])

  useEffect(() => {
    if (!selectedPolicy) {
      setLogs(null)
      setSwitchLogs(null)
      return
    }
    let cancelled = false
    apiFetch<AutoGroupEvaluationLogPage>(`/auto-groups/${selectedPolicy.id}/evaluation-logs?page=1&page_size=12`)
      .then((res) => {
        if (!cancelled) setLogs(res)
      })
      .catch(() => {
        if (!cancelled) setLogs(null)
      })
    apiFetch<AutoGroupSwitchLogPage>(`/auto-groups/${selectedPolicy.id}/switch-logs?page=1&page_size=8`)
      .then((res) => {
        if (!cancelled) setSwitchLogs(res)
      })
      .catch(() => {
        if (!cancelled) setSwitchLogs(null)
      })
    return () => {
      cancelled = true
    }
  }, [selectedPolicy?.id, logsBump])

  const keyOptions = useMemo(() => {
    return keys.slice().sort((a, b) => {
      if (a.name.toLowerCase() === "auto") return -1
      if (b.name.toLowerCase() === "auto") return 1
      return a.name.localeCompare(b.name, "zh-CN")
    })
  }, [keys])

  const probeModelOptions = useMemo(
    () => normalizeProbeModelOptions(probeModels, defaultProbeModel, form.probe_model),
    [form.probe_model, probeModels],
  )

  function selectPolicy(p: AutoGroupPolicyView) {
    setSelectedID(p.id)
    setForm(formFromPolicy(p))
    setWizardStep(5)
  }

  function startCreate(channel?: Channel) {
    setSelectedID("new")
    setForm(emptyForm(channel ? String(channel.id) : requestedChannelID))
    setWizardStep(0)
    setLogs(null)
    setSwitchLogs(null)
  }

  function patchForm(patch: Partial<FormState>) {
    setForm((prev) => ({ ...prev, ...patch }))
  }

  function refreshAutoGroupData() {
    refresh()
    policies.refetch()
    summary.refetch()
  }

  async function movePolicy(policyID: number, direction: "up" | "down") {
    const index = list.findIndex((p) => p.id === policyID)
    if (index < 0) return
    const nextIndex = direction === "up" ? index - 1 : index + 1
    if (nextIndex < 0 || nextIndex >= list.length) return
    const next = list.slice()
    const [item] = next.splice(index, 1)
    next.splice(nextIndex, 0, item)
    setReorderingID(policyID)
    try {
      await apiFetch<AutoGroupPolicyView[]>("/auto-groups/reorder", {
        method: "POST",
        body: JSON.stringify({ ids: next.map((p) => p.id) }),
      })
      toast.success("策略排序已保存")
      refreshAutoGroupData()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "保存排序失败")
    } finally {
      setReorderingID(null)
    }
  }

  function canAdvanceStep() {
    if (wizardStep === 0) return !!form.channel_id
    if (wizardStep === 1) return !!form.target_key_name.trim() || numberOrZero(form.target_key_id) > 0
    return true
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setSaving(true)
    try {
      const body = buildInput(form)
      const path = form.id ? `/auto-groups/${form.id}` : "/auto-groups"
      const method = form.id ? "PUT" : "POST"
      const saved = await apiFetch<AutoGroupPolicyView>(path, {
        method,
        body: JSON.stringify(body),
      })
      setSelectedID(saved.id)
      setForm(formFromPolicy(saved))
      toast.success("智能分组策略已保存")
      refreshAutoGroupData()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "保存失败")
    } finally {
      setSaving(false)
    }
  }

  async function evaluateNow() {
    if (!form.id) {
      toast.error("请先保存策略")
      return
    }
    setEvaluating(true)
    try {
      const res = await apiFetch<AutoGroupEvaluationResult>(`/auto-groups/${form.id}/evaluate`, { method: "POST" })
      toast.success(res.evaluation_log.message || "评估完成")
      refreshAutoGroupData()
      setLogs(null)
      setSwitchLogs(null)
      setLogsBump((v) => v + 1)
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "评估失败")
      refreshAutoGroupData()
    } finally {
      setEvaluating(false)
    }
  }

  async function deleteCurrent() {
    if (!form.id) return
    const ok = await confirm({
      title: `删除策略 ${form.name}？`,
      description: "删除后会同时清理候选熔断状态和历史评估/切换日志。",
      confirmLabel: "删除",
      destructive: true,
    })
    if (!ok) return
    try {
      await apiFetch(`/auto-groups/${form.id}`, { method: "DELETE" })
      toast.success("策略已删除")
      startCreate()
      refreshAutoGroupData()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "删除失败")
    }
  }

  async function togglePolicyEnabled(enabled: boolean) {
    if (!form.id) return
    const key = enabled ? "resume-policy" : "pause-policy"
    setActionBusy(key)
    try {
      const saved = await apiFetch<AutoGroupPolicyView>(`/auto-groups/${form.id}/${enabled ? "resume" : "pause"}`, { method: "POST" })
      setForm(formFromPolicy(saved))
      toast.success(enabled ? "策略已恢复" : "策略已暂停")
      refreshAutoGroupData()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "操作失败")
    } finally {
      setActionBusy(null)
    }
  }

  async function runCandidateAction(candidate: AutoGroupCandidate, action: CandidateAction) {
    if (!form.id) return
    const busyKey = `${action}-${candidate.id}`
    if (action === "force-switch") {
      const ok = await confirm({
        title: `强制切换到 ${candidate.group_name}？`,
        description: "该操作会直接修改目标 API Key 的分组，绕过当前排序和冷却规则。",
        confirmLabel: "强制切换",
      })
      if (!ok) return
    }
    setActionBusy(busyKey)
    try {
      const res = await apiFetch<AutoGroupCandidate | AutoGroupEvaluationResult>(
        `/auto-groups/${form.id}/candidates/${candidate.id}/${action}`,
        { method: "POST" },
      )
      if (action === "force-switch") {
        toast.success((res as AutoGroupEvaluationResult).evaluation_log?.message || "已强制切换")
        setLogsBump((v) => v + 1)
      } else {
        toast.success(candidateActionLabel(action))
      }
      refreshAutoGroupData()
    } catch (e) {
      const err = e as Error
      toast.error(err.message || "候选操作失败")
    } finally {
      setActionBusy(null)
    }
  }

  return (
    <section className="space-y-4">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-lg font-semibold text-foreground">{"智能分组"}</h1>
          <p className="mt-1 max-w-4xl text-xs leading-5 text-muted-foreground">
            {"复用已配置的上游渠道，给指定 API Key 自动选择低倍率且未被规则排除的分组。倍率采集后会自动评估，也可以在这里手动触发。"}
          </p>
        </div>
        <Button size="sm" className="gap-1.5" onClick={() => startCreate()}>
          <Plus className="size-3.5" />
          {"新增策略"}
        </Button>
      </header>

      <SummaryCards summary={summary.data} loading={summary.loading} />

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base">{"策略列表"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {policies.loading ? (
              <p className="text-xs text-muted-foreground">{"加载中..."}</p>
            ) : list.length === 0 ? (
              <div className="rounded-lg border border-dashed border-border px-4 py-8 text-center">
                <p className="text-sm text-muted-foreground">{"还没有智能分组策略"}</p>
                <Button size="sm" variant="outline" className="mt-3 gap-1.5" onClick={() => startCreate()}>
                  <Plus className="size-3.5" />
                  {"创建第一个策略"}
                </Button>
              </div>
            ) : (
              <ScrollArea className="max-h-[calc(100vh-220px)] pr-1">
                <div className="space-y-2">
                  {list.map((p, index) => (
                    <PolicyCard
                      key={p.id}
                      policy={p}
                      selected={selectedID === p.id}
                      reordering={reorderingID === p.id}
                      canMoveUp={index > 0}
                      canMoveDown={index < list.length - 1}
                      onClick={() => selectPolicy(p)}
                      onMoveUp={() => movePolicy(p.id, "up")}
                      onMoveDown={() => movePolicy(p.id, "down")}
                    />
                  ))}
                </div>
              </ScrollArea>
            )}
          </CardContent>
        </Card>

        <div className="space-y-4">
          <Card className="border border-border shadow-none">
            <CardHeader className="flex flex-col gap-2 pb-2 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <CardTitle className="text-base">{form.id ? "编辑策略" : "新增策略"}</CardTitle>
                <p className="mt-1 text-xs text-muted-foreground">
                  {selectedChannel
                    ? `${selectedChannel.name} · ${channelTypeLabel(selectedChannel.type)}`
                    : "请选择一个已配置的上游渠道"}
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" size="sm" className="gap-1.5" onClick={evaluateNow} disabled={!form.id || evaluating}>
                  {evaluating ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
                  {"立即评估"}
                </Button>
                {form.id ? (
                  <Button
                    variant="outline"
                    size="sm"
                    className="gap-1.5"
                    onClick={() => togglePolicyEnabled(!form.enabled)}
                    disabled={actionBusy === "pause-policy" || actionBusy === "resume-policy"}
                  >
                    {form.enabled ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
                    {form.enabled ? "暂停" : "恢复"}
                  </Button>
                ) : null}
                {form.id ? (
                  <Button variant="outline" size="sm" className="gap-1.5 text-danger hover:text-danger" onClick={deleteCurrent}>
                    <Trash2 className="size-3.5" />
                    {"删除"}
                  </Button>
                ) : null}
              </div>
            </CardHeader>
            <CardContent>
              <form onSubmit={handleSubmit} className="space-y-5">
                <WizardStepper step={wizardStep} onStepChange={setWizardStep} />

                {wizardStep === 0 ? (
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    <Field label="策略名称">
                      <Input value={form.name} onChange={(e) => patchForm({ name: e.target.value })} />
                    </Field>
                    <Field label="上游渠道">
                      <Select
                        value={form.channel_id}
                        onValueChange={(v) => patchForm({ channel_id: v, include_groups: [], exclude_groups: [] })}
                      >
                        <SelectTrigger>
                          <SelectValue placeholder="选择渠道" />
                        </SelectTrigger>
                        <SelectContent>
                          {(channels.data ?? []).map((c) => (
                            <SelectItem key={c.id} value={String(c.id)}>
                              {c.name} · {channelTypeLabel(c.type)}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>
                    <CapabilityPanel
                      loading={capabilities.loading}
                      matrix={capabilities.data}
                      error={capabilities.error}
                    />
                  </div>
                ) : null}

                {wizardStep === 1 ? (
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    <Field label="目标 API Key">
                      <Select
                        value={form.target_key_id}
                        onValueChange={(v) => {
                          const key = keyOptions.find((item) => String(item.id) === v)
                          patchForm({ target_key_id: v, target_key_name: key?.name ?? form.target_key_name })
                        }}
                        disabled={!form.channel_id || keysLoading}
                      >
                        <SelectTrigger>
                          <SelectValue placeholder={keysLoading ? "加载 key..." : "选择目标 key"} />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="0">按名称查找：{form.target_key_name || "auto"}</SelectItem>
                          {keyOptions.map((key) => (
                            <SelectItem key={key.id} value={String(key.id)}>
                              {key.name} · {currentKeyGroup(key) || "未识别分组"}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label="目标 Key 名称兜底">
                      <Input
                        value={form.target_key_name}
                        onChange={(e) => patchForm({ target_key_name: e.target.value })}
                        placeholder="auto"
                      />
                    </Field>
                    <div className="rounded-lg border border-border bg-muted/20 p-3 text-xs leading-5 text-muted-foreground md:col-span-2">
                      {"可以为同一个上游创建多个目标 Key 策略，例如 auto、auto-fast、team-a。相同渠道 + 相同目标 Key 名称只能存在一条策略。"}
                    </div>
                  </div>
                ) : null}

                {wizardStep === 2 ? (
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    <Field label="探测 Key 名称">
                      <Input
                        value={form.probe_key_name}
                        onChange={(e) => patchForm({ probe_key_name: e.target.value })}
                        placeholder="ops-probe-auto"
                      />
                    </Field>
                    <Field label="探测模型">
                      <ProbeModelSelect
                        value={form.probe_model}
                        options={probeModelOptions}
                        loading={probeModelsLoading}
                        warning={probeModelsWarning}
                        onChange={(probe_model) => patchForm({ probe_model })}
                      />
                    </Field>
                    <Field label="探测超时(秒)">
                      <Input value={form.probe_timeout_seconds} onChange={(e) => patchForm({ probe_timeout_seconds: e.target.value })} />
                    </Field>
                    <Field label="半开成功次数">
                      <Input value={form.half_open_success_threshold} onChange={(e) => patchForm({ half_open_success_threshold: e.target.value })} />
                    </Field>
                    <Field label="成功缓存(分钟)">
                      <Input value={form.probe_success_cache_minutes} onChange={(e) => patchForm({ probe_success_cache_minutes: e.target.value })} />
                    </Field>
                    <Field label="失败重试(分钟)">
                      <Input value={form.probe_failure_retry_minutes} onChange={(e) => patchForm({ probe_failure_retry_minutes: e.target.value })} />
                    </Field>
                    <Field label="单轮最多探测">
                      <Input value={form.probe_max_per_run} onChange={(e) => patchForm({ probe_max_per_run: e.target.value })} />
                    </Field>
                    <div className="rounded-lg border border-border bg-muted/20 p-3 text-xs leading-5 text-muted-foreground">
                      {"成功缓存内不会重复发起真实请求；切换前会复用本轮刚成功的探测，否则会再次校验目标分组。"}
                    </div>
                  </div>
                ) : null}

                {wizardStep === 3 ? (
                  <div className="space-y-3">
                    <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
                      <GroupPicker
                        title="允许分组范围"
                        hint="留空表示所有分组都可以参与排序"
                        groups={groups}
                        selected={form.include_groups}
                        otherSelected={form.exclude_groups}
                        loading={groupsLoading}
                        tone="include"
                        onChange={(values) => patchForm({ include_groups: values })}
                      />
                      <GroupPicker
                        title="选择排除分组"
                        hint="这里只是可选分组列表，勾选后才会排除"
                        groups={groups}
                        selected={form.exclude_groups}
                        otherSelected={form.include_groups}
                        loading={groupsLoading}
                        tone="exclude"
                        onChange={(values) => patchForm({ exclude_groups: values })}
                      />
                    </div>
                    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                      <Field label="最小倍率">
                        <Input value={form.min_ratio} onChange={(e) => patchForm({ min_ratio: e.target.value })} placeholder="不限" />
                      </Field>
                      <Field label="最大倍率">
                        <Input value={form.max_ratio} onChange={(e) => patchForm({ max_ratio: e.target.value })} placeholder="不限" />
                      </Field>
                      <Field label="包含关键词">
                        <Input
                          value={form.include_keywords}
                          onChange={(e) => patchForm({ include_keywords: e.target.value })}
                          placeholder="逗号分隔；留空不限制"
                        />
                      </Field>
                      <Field label="排除关键词">
                        <Input
                          value={form.exclude_keywords}
                          onChange={(e) => patchForm({ exclude_keywords: e.target.value })}
                          placeholder="不可用,维护,禁用..."
                        />
                      </Field>
                    </div>
                  </div>
                ) : null}

                {wizardStep === 4 ? (
                  <div className="space-y-3">
                    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                      <Field label="失败阈值">
                        <Input value={form.failure_threshold} onChange={(e) => patchForm({ failure_threshold: e.target.value })} />
                      </Field>
                      <Field label="熔断分钟">
                        <Input value={form.circuit_duration_minutes} onChange={(e) => patchForm({ circuit_duration_minutes: e.target.value })} />
                      </Field>
                      <Field label="最小收益(%)">
                        <Input value={form.min_ratio_improvement_pct} onChange={(e) => patchForm({ min_ratio_improvement_pct: e.target.value })} />
                      </Field>
                      <Field label="切换冷却(分钟)">
                        <Input value={form.switch_cooldown_minutes} onChange={(e) => patchForm({ switch_cooldown_minutes: e.target.value })} />
                      </Field>
                    </div>
                    <div className="grid grid-cols-1 gap-2 rounded-lg border border-border p-3 sm:grid-cols-2">
                      <ToggleRow
                        title="启用策略"
                        desc="关闭后后台调度和手动评估都不会切换 key"
                        checked={form.enabled}
                        onChange={(v) => patchForm({ enabled: v })}
                      />
                      <ToggleRow
                        title="异常/切换通知"
                        desc="复用通知渠道订阅规则，可按渠道、事件、分组过滤"
                        checked={form.notify_enabled}
                        onChange={(v) => patchForm({ notify_enabled: v })}
                      />
                      <ToggleRow
                        title="当前不可用时强制降级"
                        desc="当前分组探测失败时忽略收益阈值和冷却，直接切到可用候选"
                        checked={form.force_switch_on_current_unhealthy}
                        onChange={(v) => patchForm({ force_switch_on_current_unhealthy: v })}
                      />
                      <ToggleRow
                        title="无可用组时保持当前"
                        desc="所有候选不可用时不清空或乱切目标 key，只通知人工处理"
                        checked={form.keep_current_when_no_available}
                        onChange={(v) => patchForm({ keep_current_when_no_available: v })}
                      />
                    </div>
                  </div>
                ) : null}

                {wizardStep === 5 ? (
                  <ReviewPanel
                    form={form}
                    channel={selectedChannel}
                    capabilities={capabilities.data}
                    groupsCount={groups.length}
                  />
                ) : null}

                <div className="flex flex-col gap-2 border-t border-border pt-4 sm:flex-row sm:items-center sm:justify-between">
                  <p className="text-xs text-muted-foreground">
                    {wizardStep === wizardSteps.length - 1
                      ? "保存后会按调度配置自动评估；手动评估会立即读取上游 key 和分组。"
                      : wizardSteps[wizardStep]?.desc}
                  </p>
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                    <Button
                      type="button"
                      variant="outline"
                      disabled={wizardStep === 0}
                      onClick={() => setWizardStep((v) => Math.max(0, v - 1))}
                    >
                      {"上一步"}
                    </Button>
                    {wizardStep < wizardSteps.length - 1 ? (
                      <Button
                        type="button"
                        disabled={!canAdvanceStep()}
                        onClick={() => setWizardStep((v) => Math.min(wizardSteps.length - 1, v + 1))}
                      >
                        {"下一步"}
                      </Button>
                    ) : (
                      <Button type="submit" className="gap-1.5" disabled={saving}>
                        {saving ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
                        {saving ? "保存中" : "保存策略"}
                      </Button>
                    )}
                  </div>
                </div>
              </form>
            </CardContent>
          </Card>

          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <DecisionPanel policy={selectedPolicy} candidates={currentCandidates} />
            <CandidatesPanel candidates={currentCandidates} actionBusy={actionBusy} onAction={runCandidateAction} />
            <LogsPanel logs={logs} switchLogs={switchLogs} />
          </div>
        </div>
      </div>
      {confirmDialog}
    </section>
  )
}

function PolicyCard({
  policy,
  selected,
  reordering,
  canMoveUp,
  canMoveDown,
  onClick,
  onMoveUp,
  onMoveDown,
}: {
  policy: AutoGroupPolicyView
  selected: boolean
  reordering: boolean
  canMoveUp: boolean
  canMoveDown: boolean
  onClick: () => void
  onMoveUp: () => void
  onMoveDown: () => void
}) {
  const key = policy.enabled ? policy.last_status : "disabled"
  const meta = statusMeta[key as keyof typeof statusMeta] ?? statusMeta.idle
  const Icon = meta.icon
  const healthy = (policy.candidates ?? []).filter((item) => item.status === "healthy").length
  const circuit = (policy.candidates ?? []).filter((item) => item.status === "circuit_open").length
  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg border transition-colors",
        selected ? "border-primary bg-primary/5" : "border-border hover:bg-muted/40",
      )}
    >
      <button type="button" onClick={onClick} className="w-full p-3 text-left">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold text-foreground">{policy.name}</p>
            <p className="mt-0.5 truncate text-xs text-muted-foreground">
              {policy.channel?.name ?? `渠道 #${policy.channel_id}`} · key {policy.target_key_name || policy.target_key_id}
            </p>
          </div>
          <span className={cn("inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] ring-1 ring-inset", meta.cls)}>
            <Icon className="size-3" />
            {meta.label}
          </span>
        </div>
        <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
          <MiniStat label="当前组" value={policy.current_group_name || "—"} />
          <MiniStat label="可用/熔断" value={`${healthy}/${circuit}`} />
          <MiniStat label="上次评估" value={relativeTime(policy.last_evaluate_at)} />
        </div>
        {policy.last_error ? (
          <p className="mt-2 line-clamp-2 text-[11px] leading-4 text-danger">{policy.last_error}</p>
        ) : null}
      </button>
      <div className="flex items-center justify-between gap-2 border-t border-border/70 bg-muted/20 px-2 py-1.5">
        <span className="text-[10px] text-muted-foreground">排序 {policy.sort_order > 0 ? policy.sort_order : "未保存"}</span>
        <div className="flex items-center gap-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-[11px]"
            disabled={reordering || !canMoveUp}
            onClick={onMoveUp}
          >
            {reordering ? <Loader2 className="mr-1 size-3 animate-spin" /> : <ArrowUp className="mr-1 size-3" />}
            {"上移"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-[11px]"
            disabled={reordering || !canMoveDown}
            onClick={onMoveDown}
          >
            {reordering ? <Loader2 className="mr-1 size-3 animate-spin" /> : <ArrowDown className="mr-1 size-3" />}
            {"下移"}
          </Button>
        </div>
      </div>
    </div>
  )
}

function MiniStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded border border-border bg-background px-2 py-1.5">
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className="truncate font-medium text-foreground">{value}</p>
    </div>
  )
}

function SummaryCards({ summary, loading }: { summary: AutoGroupSummary | null; loading: boolean }) {
  const items = [
    { label: "策略总数", value: summary?.total_policies ?? 0 },
    { label: "运行中", value: summary?.running_policies ?? 0 },
    { label: "异常策略", value: summary?.abnormal_policies ?? 0, danger: true },
    { label: "熔断分组", value: summary?.circuit_groups ?? 0, warning: true },
    { label: "今日切换", value: summary?.today_switches ?? 0 },
    { label: "无可用组", value: summary?.no_available_policies ?? 0, danger: true },
    { label: "手动停用", value: summary?.manual_disabled_groups ?? 0 },
  ]
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-7">
      {items.map((item) => (
        <div
          key={item.label}
          className={cn(
            "rounded-lg border border-border bg-card px-3 py-2 shadow-none",
            item.danger && "border-danger/30 bg-danger/5",
            item.warning && "border-warning/30 bg-warning/5",
          )}
        >
          <p className="text-[11px] text-muted-foreground">{item.label}</p>
          <p className="mt-1 text-lg font-semibold tabular-nums text-foreground">{loading ? "..." : item.value}</p>
        </div>
      ))}
    </div>
  )
}

function WizardStepper({ step, onStepChange }: { step: number; onStepChange: (step: number) => void }) {
  return (
    <div className="rounded-lg border border-border bg-muted/20 p-2">
      <div className="grid grid-cols-2 gap-2 md:grid-cols-3 xl:grid-cols-6">
        {wizardSteps.map((item, index) => (
          <button
            key={item.key}
            type="button"
            onClick={() => onStepChange(index)}
            className={cn(
              "rounded-md px-3 py-2 text-left transition-colors",
              step === index ? "bg-background shadow-sm ring-1 ring-border" : "hover:bg-background/70",
            )}
          >
            <span className="text-[10px] text-muted-foreground">{String(index + 1).padStart(2, "0")}</span>
            <span className="mt-0.5 block text-xs font-medium text-foreground">{item.title}</span>
          </button>
        ))}
      </div>
    </div>
  )
}

function CapabilityPanel({
  loading,
  matrix,
  error,
}: {
  loading: boolean
  matrix: AutoGroupCapabilityMatrix | null
  error: string | null
}) {
  const levelClass =
    matrix?.level === "full"
      ? "bg-success/10 text-success ring-success/20"
      : matrix?.level === "suggest"
        ? "bg-warning/10 text-warning ring-warning/20"
        : matrix?.level === "error"
          ? "bg-danger/10 text-danger ring-danger/20"
          : "bg-muted text-muted-foreground ring-border"
  const levelLabel =
    matrix?.level === "full"
      ? "完整支持"
      : matrix?.level === "suggest"
        ? "建议模式"
        : matrix?.level === "error"
          ? "检测失败"
          : "观察模式"
  return (
    <div className="rounded-lg border border-border bg-background/80 p-3 md:col-span-2">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div>
          <p className="text-sm font-medium text-foreground">{"能力矩阵"}</p>
          <p className="mt-1 text-[11px] leading-5 text-muted-foreground">
            {"只复用已配置渠道的地址和账号信息，不需要重新录入上游。检测不会创建或修改真实 API Key。"}
          </p>
        </div>
        <span className={cn("inline-flex rounded px-2 py-1 text-[11px] ring-1 ring-inset", levelClass)}>
          {loading ? "检测中" : error ? "检测失败" : levelLabel}
        </span>
      </div>
      {loading ? (
        <p className="text-xs text-muted-foreground">{"正在读取上游 Key 和分组能力..."}</p>
      ) : error ? (
        <p className="text-xs text-danger">{error}</p>
      ) : !matrix ? (
        <p className="text-xs text-muted-foreground">{"选择渠道后显示 sub2api / newapi 能力检测结果。"}</p>
      ) : (
        <div className="space-y-3">
          <p className="text-xs leading-5 text-muted-foreground">{matrix.message || "已完成能力检测。"}</p>
          <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            {matrix.capabilities.map((item) => (
              <div key={item.key} className="rounded-md border border-border px-3 py-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-medium text-foreground">{item.label}</span>
                  <Badge variant={item.supported ? "secondary" : "outline"}>
                    {item.supported ? "支持" : "不可用"}
                  </Badge>
                </div>
                {item.message ? <p className="mt-1 line-clamp-2 text-[11px] text-muted-foreground">{item.message}</p> : null}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function ReviewPanel({
  form,
  channel,
  capabilities,
  groupsCount,
}: {
  form: FormState
  channel: Channel | null
  capabilities: AutoGroupCapabilityMatrix | null
  groupsCount: number
}) {
  return (
    <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
      <ReviewItem title="上游渠道" value={channel ? `${channel.name} · ${channelTypeLabel(channel.type)}` : "未选择"} />
      <ReviewItem title="能力级别" value={capabilityLevelLabel(capabilities?.level)} />
      <ReviewItem title="目标 Key" value={form.target_key_name || form.target_key_id || "auto"} />
      <ReviewItem title="探测 Key" value={form.probe_key_name || "ops-probe-auto"} />
      <ReviewItem title="允许分组" value={form.include_groups.length > 0 ? `${form.include_groups.length} 个` : `全部 ${groupsCount || ""}`} />
      <ReviewItem title="排除分组" value={form.exclude_groups.length > 0 ? `${form.exclude_groups.length} 个` : "未指定"} />
      <ReviewItem title="倍率范围" value={`${form.min_ratio || "不限"} - ${form.max_ratio || "不限"}`} />
      <ReviewItem title="通知" value={form.notify_enabled ? "启用异常/切换通知" : "不通知"} />
      <div className="rounded-lg border border-border bg-muted/20 p-3 text-xs leading-5 text-muted-foreground md:col-span-2 xl:col-span-4">
        {"保存后，系统会按倍率、关键词、手动停用、熔断状态和实时探测结果选择最低倍率健康分组；如果没有可用分组，会保持当前分组并按通知规则提醒。"}
      </div>
    </div>
  )
}

function ReviewItem({ title, value }: { title: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-background px-3 py-2">
      <p className="text-[11px] text-muted-foreground">{title}</p>
      <p className="mt-1 truncate text-sm font-medium text-foreground">{value}</p>
    </div>
  )
}

function capabilityLevelLabel(level?: string) {
  if (level === "full") return "完整支持"
  if (level === "suggest") return "建议模式"
  if (level === "error") return "检测失败"
  return "待检测"
}

function ProbeModelSelect({
  value,
  options,
  loading,
  warning,
  onChange,
}: {
  value: string
  options: ProbeModelOption[]
  loading: boolean
  warning: string
  onChange: (value: string) => void
}) {
  const [open, setOpen] = useState(false)
  const selected = options.find((item) => item.id === value)
  return (
    <div className="space-y-2">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="outline"
            role="combobox"
            aria-expanded={open}
            className="w-full justify-between"
            disabled={loading && options.length === 0}
          >
            <span className="truncate text-left">
              {loading ? "加载模型..." : selected?.id || value || defaultProbeModel}
            </span>
            <ChevronsUpDown className="ml-2 size-3.5 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-[var(--radix-popover-trigger-width)] p-0" align="start">
          <Command>
            <CommandInput placeholder="搜索模型..." />
            <CommandList>
              <CommandEmpty>{"没有匹配的模型。"}</CommandEmpty>
              <CommandGroup>
                {options.map((item) => (
                  <CommandItem
                    key={item.id}
                    value={item.id}
                    onSelect={() => {
                      onChange(item.id)
                      setOpen(false)
                    }}
                  >
                    <CheckCircle2 className={cn("size-3.5", value === item.id ? "opacity-100" : "opacity-0")} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-medium">{item.id}</span>
                      {item.name || item.owned_by ? (
                        <span className="block truncate text-[11px] text-muted-foreground">
                          {[item.name && item.name !== item.id ? item.name : "", item.owned_by].filter(Boolean).join(" · ")}
                        </span>
                      ) : null}
                    </span>
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {warning ? <p className="text-[11px] leading-4 text-warning">{warning}</p> : null}
    </div>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

function ToggleRow({ title, desc, checked, onChange }: { title: string; desc: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-md bg-muted/20 px-3 py-2">
      <div>
        <p className="text-sm font-medium">{title}</p>
        <p className="text-[11px] text-muted-foreground">{desc}</p>
      </div>
      <Switch checked={checked} onCheckedChange={onChange} />
    </div>
  )
}

function GroupPicker({
  title,
  hint,
  groups,
  selected,
  otherSelected,
  loading,
  tone,
  onChange,
}: {
  title: string
  hint: string
  groups: ChannelAPIKeyGroup[]
  selected: string[]
  otherSelected: string[]
  loading: boolean
  tone: "include" | "exclude"
  onChange: (values: string[]) => void
}) {
  function toggle(name: string, checked: boolean) {
    onChange(checked ? Array.from(new Set([...selected, name])) : selected.filter((item) => item !== name))
  }
  const visibleGroups = useMemo(() => {
    return groups.slice().sort((a, b) => {
      const aSelected = selected.includes(a.name)
      const bSelected = selected.includes(b.name)
      if (aSelected !== bSelected) return aSelected ? -1 : 1
      return a.name.localeCompare(b.name, "zh-CN")
    })
  }, [groups, selected])
  const toneCls = tone === "include" ? "border-success/30 bg-success/5" : "border-warning/30 bg-warning/5"
  const selectedLabel = tone === "include" ? "已允许" : "已排除"
  return (
    <div className={cn("rounded-lg border p-3", selected.length > 0 ? toneCls : "border-border")}>
      <div className="mb-2 flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium">{title}</p>
          <p className="text-[11px] text-muted-foreground">{hint}</p>
          <p className="mt-1 text-[11px] text-muted-foreground">
            {`${selectedLabel} ${selected.length} 个 / 可选 ${groups.length} 个，只有勾选的分组会生效。`}
          </p>
        </div>
        {selected.length > 0 ? (
          <Button type="button" variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => onChange([])}>
            {"清空"}
          </Button>
        ) : null}
      </div>
      {loading ? (
        <p className="text-xs text-muted-foreground">{"读取分组中..."}</p>
      ) : groups.length === 0 ? (
        <p className="text-xs text-muted-foreground">{"暂无上游分组，先同步倍率或检查渠道登录"}</p>
      ) : (
        <ScrollArea className="h-48 pr-2">
          <div className="space-y-1.5">
            {visibleGroups.map((g) => {
              const checked = selected.includes(g.name)
              const disabled = otherSelected.includes(g.name) && !checked
              return (
                <label
                  key={`${g.id ?? g.name}-${g.name}`}
                  className={cn(
                    "flex cursor-pointer items-start gap-2 rounded-md border border-border px-2 py-1.5 text-xs hover:bg-muted/40",
                    disabled && "cursor-not-allowed opacity-50",
                  )}
                >
                  <Checkbox checked={checked} disabled={disabled} onCheckedChange={(v) => toggle(g.name, v === true)} />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center justify-between gap-2">
                      <span className="truncate font-medium">{g.name}</span>
                      <span className="flex shrink-0 items-center gap-1">
                        {checked ? <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">{selectedLabel}</Badge> : null}
                        <span className="tabular-nums text-muted-foreground">{formatRatio(g.ratio)}</span>
                      </span>
                    </span>
                    {g.description ? <span className="mt-0.5 line-clamp-2 text-muted-foreground">{g.description}</span> : null}
                  </span>
                </label>
              )
            })}
          </div>
        </ScrollArea>
      )}
    </div>
  )
}

function DecisionPanel({ policy, candidates }: { policy: AutoGroupPolicyView | null; candidates: AutoGroupCandidate[] }) {
  const healthy = candidates.filter((c) => c.status === "healthy")
  const best = healthy.slice().sort((a, b) => (a.ratio === b.ratio ? a.group_name.localeCompare(b.group_name, "zh-CN") : a.ratio - b.ratio))[0]
  const excluded = candidates.filter((c) => c.status !== "healthy").slice(0, 4)
  const currentGroupName = resolveCurrentGroupName(policy, candidates)
  return (
    <Card className="border border-border shadow-none xl:col-span-2">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ArrowRightLeft className="size-4" />
          {"当前决策"}
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-3 md:grid-cols-[260px_minmax(0,1fr)]">
        <div className="rounded-lg border border-border bg-muted/20 px-3 py-2">
          <p className="text-[11px] text-muted-foreground">{"目标 Key"}</p>
          <p className="mt-1 text-sm font-medium">{policy?.target_key_name || "auto"}</p>
          <p className="mt-2 text-[11px] text-muted-foreground">{"当前分组"}</p>
          <p className="mt-1 text-sm font-medium">{currentGroupName || "—"}</p>
          <p className="mt-2 text-[11px] text-muted-foreground">{"当前倍率"}</p>
          <p className="mt-1 text-sm font-medium">{formatRatio(policy?.current_ratio ?? 0)}</p>
        </div>
        <div className="space-y-2 text-xs">
          {best ? (
            <div className="rounded-lg border border-success/30 bg-success/5 px-3 py-2">
              <p className="font-medium text-success">{"当前最优候选：" + best.group_name}</p>
              <p className="mt-1 text-muted-foreground">
                {`探测可用，倍率 ${formatRatio(best.ratio)}，在当前健康候选中最低。${currentGroupName === best.group_name ? "目标 Key 已在该分组。" : "下次评估会优先尝试切换到该低倍率分组。"}`}
              </p>
            </div>
          ) : (
            <div className="rounded-lg border border-warning/30 bg-warning/5 px-3 py-2">
              <p className="font-medium text-warning">{"没有健康候选"}</p>
              <p className="mt-1 text-muted-foreground">{"系统会保持目标 Key 当前分组不变，并按通知订阅发送异常事件。"}</p>
            </div>
          )}
          {excluded.length > 0 ? (
            <div className="rounded-lg border border-border px-3 py-2">
              <p className="font-medium">{"未选择的主要原因"}</p>
              <div className="mt-1 space-y-1 text-muted-foreground">
                {excluded.map((c) => (
                  <p key={c.id} className="line-clamp-1">
                    {c.group_name}：{c.reason || c.last_error || c.status}
                  </p>
                ))}
              </div>
            </div>
          ) : null}
        </div>
      </CardContent>
    </Card>
  )
}

function resolveCurrentGroupName(policy: AutoGroupPolicyView | null, candidates: AutoGroupCandidate[]) {
  const direct = policy?.current_group_name?.trim()
  if (direct) return direct
  if (!policy?.current_group_id) return ""
  return candidates.find((c) => c.group_id === policy.current_group_id)?.group_name ?? ""
}

function CandidatesPanel({
  candidates,
  actionBusy,
  onAction,
}: {
  candidates: AutoGroupCandidate[]
  actionBusy: string | null
  onAction: (candidate: AutoGroupCandidate, action: CandidateAction) => void
}) {
  return (
    <Card className="border border-border shadow-none">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Settings2 className="size-4" />
          {"候选状态"}
        </CardTitle>
      </CardHeader>
      <CardContent>
        {candidates.length === 0 ? (
          <p className="text-xs text-muted-foreground">{"保存并评估后会显示候选分组状态。"}</p>
        ) : (
          <div className="max-h-96 overflow-auto overscroll-x-contain rounded-md border border-border/60">
            <div className="min-w-[880px]">
              <Table className="text-xs">
                <TableHeader>
                  <TableRow>
                    <TableHead>{"分组"}</TableHead>
                    <TableHead className="text-right">{"倍率"}</TableHead>
                    <TableHead>{"状态"}</TableHead>
                    <TableHead>{"探测"}</TableHead>
                    <TableHead>{"原因"}</TableHead>
                    <TableHead className="text-right">{"操作"}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {candidates.map((c) => {
                    const canForceSwitch = !c.manual_disabled && ["", "unknown", "healthy", "half_open"].includes(c.status)
                    return (
                      <TableRow key={c.id}>
                        <TableCell className="max-w-56">
                          <div className="truncate font-medium">{c.group_name}</div>
                          {c.description ? <div className="line-clamp-2 text-muted-foreground">{c.description}</div> : null}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">{formatRatio(c.ratio)}</TableCell>
                        <TableCell>
                          <CandidateBadge status={c.status} />
                        </TableCell>
                        <TableCell className="min-w-28 text-muted-foreground">
                          <div className="text-[11px]">
                            {c.last_probe_at ? (
                              <>
                                <span className={c.last_probe_success ? "text-success" : "text-danger"}>
                                  {c.last_probe_success ? "通过" : "失败"}
                                </span>
                                {c.last_probe_latency_ms ? ` · ${c.last_probe_latency_ms}ms` : ""}
                              </>
                            ) : (
                              "未探测"
                            )}
                          </div>
                          <div className="text-[10px]">
                            {c.success_count > 0 ? `连续成功 ${c.success_count}` : c.failure_count > 0 ? `失败 ${c.failure_count}` : "—"}
                          </div>
                        </TableCell>
                        <TableCell className="max-w-64 text-muted-foreground">
                          {c.last_error_code ? (
                            <div className="mb-1 flex flex-wrap items-center gap-1">
                              <span className="rounded border border-border bg-muted/50 px-1.5 py-0.5 font-mono text-[10px] text-foreground">
                                {c.last_error_code}
                              </span>
                              <span className="text-[11px] text-foreground">{probeErrorLabel(c.last_error_code)}</span>
                            </div>
                          ) : null}
                          <div className="line-clamp-2">
                            {c.reason || c.last_error || (c.circuit_open_until ? `熔断到 ${relativeTime(c.circuit_open_until)}` : "—")}
                          </div>
                          {c.last_error && c.reason && c.last_error !== c.reason ? (
                            <div className="mt-1 line-clamp-2 text-[10px] text-muted-foreground/80">{c.last_error}</div>
                          ) : null}
                        </TableCell>
                        <TableCell className="min-w-52 text-right">
                          <div className="flex flex-wrap justify-end gap-1">
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="h-7 px-2 text-[11px]"
                              disabled={!!actionBusy || c.manual_disabled}
                              onClick={() => onAction(c, "probe")}
                            >
                              {"探测"}
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="h-7 px-2 text-[11px]"
                              disabled={!!actionBusy}
                              onClick={() => onAction(c, c.manual_disabled ? "enable" : "disable")}
                            >
                              {c.manual_disabled ? "恢复" : "停用"}
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="h-7 px-2 text-[11px] text-warning hover:text-warning"
                              disabled={!!actionBusy || c.status === "circuit_open"}
                              onClick={() => onAction(c, "circuit")}
                            >
                              {"熔断"}
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="h-7 px-2 text-[11px] text-brand hover:text-brand"
                              disabled={!!actionBusy || !canForceSwitch}
                              onClick={() => onAction(c, "force-switch")}
                            >
                              {"强切"}
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function CandidateBadge({ status }: { status: string }) {
  const cls =
    status === "healthy"
      ? "bg-success/10 text-success ring-success/20"
      : status === "circuit_open"
        ? "bg-warning/10 text-warning ring-warning/20"
        : status === "half_open"
          ? "bg-brand/10 text-brand ring-brand/20"
      : status === "failed"
        ? "bg-danger/10 text-danger ring-danger/20"
        : status === "excluded"
          ? "bg-muted text-muted-foreground ring-border"
      : "bg-muted text-muted-foreground ring-border"
  const label =
    status === "healthy"
      ? "可用"
      : status === "circuit_open"
        ? "熔断"
        : status === "half_open"
          ? "半开"
        : status === "failed"
          ? "失败"
          : status === "excluded"
            ? "排除"
            : status
  return <span className={cn("inline-flex rounded px-1.5 py-0.5 text-[10px] ring-1 ring-inset", cls)}>{label}</span>
}

function LogsPanel({ logs, switchLogs }: { logs: AutoGroupEvaluationLogPage | null; switchLogs: AutoGroupSwitchLogPage | null }) {
  return (
    <Card className="border border-border shadow-none">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <RefreshCw className="size-4" />
          {"评估记录"}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-2">
          {(logs?.items ?? []).length === 0 ? (
            <p className="text-xs text-muted-foreground">{"暂无评估记录。"}</p>
          ) : (
            (logs?.items ?? []).map((log) => (
              <div key={log.id} className="rounded-lg border border-border px-3 py-2">
                <div className="flex items-center justify-between gap-2">
                  <Badge variant={log.success ? "secondary" : "destructive"}>{log.status}</Badge>
                  <span className="text-[11px] text-muted-foreground">{relativeTime(log.created_at)}</span>
                </div>
                <p className="mt-1 text-xs text-foreground">{log.message || "—"}</p>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  {log.current_group || "—"} {" -> "} {log.selected_group || "—"} · {log.available_count ?? 0}/{log.candidate_count} 可用
                  {log.circuit_open_count ? ` · ${log.circuit_open_count} 熔断` : ""}
                  {log.action ? ` · ${log.action}` : ""}
                </p>
              </div>
            ))
          )}
        </div>
        {(switchLogs?.items ?? []).length > 0 ? (
          <div className="border-t border-border pt-3">
            <p className="mb-2 text-xs font-medium text-muted-foreground">{"切换历史"}</p>
            <div className="space-y-2">
              {(switchLogs?.items ?? []).map((log) => (
                <div key={log.id} className="rounded-md bg-muted/30 px-3 py-2 text-xs">
                  <div className="flex items-center justify-between gap-2">
                    <span className={log.success ? "text-success" : "text-danger"}>
                      {log.from_group || "—"} {" -> "} {log.to_group || "—"}
                    </span>
                    <span className="text-muted-foreground">{relativeTime(log.created_at)}</span>
                  </div>
                  {log.error_message ? <p className="mt-1 text-danger">{log.error_message}</p> : null}
                </div>
              ))}
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}

function candidateActionLabel(action: "disable" | "enable" | "probe" | "circuit" | "force-switch") {
  switch (action) {
    case "disable":
      return "候选分组已停用"
    case "enable":
      return "候选分组已恢复"
    case "probe":
      return "候选分组探测完成"
    case "circuit":
      return "候选分组已临时熔断"
    case "force-switch":
      return "已强制切换"
  }
}

function probeErrorLabel(code?: string) {
  switch (code) {
    case "1001":
      return "请求超时"
    case "1002":
      return "连接失败"
    case "1003":
      return "HTTP 请求失败"
    case "2001":
      return "探测 Key 无效或未通过 NewAPI"
    case "2002":
      return "探测 Key IP 限制"
    case "2003":
      return "探测 Key 无权使用该分组"
    case "2004":
      return "探测模型无权限"
    case "2101":
      return "探测 Key 切组失败"
    case "3001":
      return "上游通道返回 401"
    case "3002":
      return "上游通道返回 403"
    case "3003":
      return "触发限流"
    case "3004":
      return "额度不足"
    case "3005":
      return "模型不可用"
    case "4001":
      return "响应为空"
    case "4002":
      return "响应不是有效 JSON"
    case "4003":
      return "上游返回错误对象"
    default:
      return code ? "未归类探测错误" : "—"
  }
}

function buildInput(form: FormState): AutoGroupPolicyInput {
  const channelID = Number(form.channel_id)
  if (!Number.isFinite(channelID) || channelID <= 0) throw new Error("请选择渠道")
  return {
    channel_id: channelID,
    name: form.name.trim() || "Auto Key 智能分组",
    enabled: form.enabled,
    notify_enabled: form.notify_enabled,
    target_key_id: numberOrZero(form.target_key_id),
    target_key_name: form.target_key_name.trim() || "auto",
    probe_key_id: numberOrZero(form.probe_key_id),
    probe_key_name: form.probe_key_name.trim() || "ops-probe-auto",
    probe_model: form.probe_model.trim() || defaultProbeModel,
    probe_timeout_seconds: Math.max(1, numberOrZero(form.probe_timeout_seconds) || 15),
    probe_success_cache_minutes: Math.max(1, numberOrZero(form.probe_success_cache_minutes) || 60),
    probe_failure_retry_minutes: Math.max(1, numberOrZero(form.probe_failure_retry_minutes) || 10),
    probe_max_per_run: Math.max(1, numberOrZero(form.probe_max_per_run) || 3),
    include_groups: form.include_groups,
    exclude_groups: form.exclude_groups,
    include_keywords: splitList(form.include_keywords),
    exclude_keywords: splitList(form.exclude_keywords),
    min_ratio: numberOrZero(form.min_ratio),
    max_ratio: numberOrZero(form.max_ratio),
    failure_threshold: Math.max(1, numberOrZero(form.failure_threshold) || 2),
    circuit_duration_minutes: Math.max(1, numberOrZero(form.circuit_duration_minutes) || 30),
    half_open_success_threshold: Math.max(1, numberOrZero(form.half_open_success_threshold) || 1),
    min_ratio_improvement_pct: numberOrZero(form.min_ratio_improvement_pct),
    switch_cooldown_minutes: Math.max(0, Number(form.switch_cooldown_minutes) || 0),
    force_switch_on_current_unhealthy: form.force_switch_on_current_unhealthy,
    keep_current_when_no_available: form.keep_current_when_no_available,
  }
}

function currentKeyGroup(key: ChannelAPIKey) {
  return key.group_name || key.group || ""
}

function numberOrZero(value: string) {
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? n : 0
}

function splitList(raw: string) {
  return raw.split(/[,，\n]/).map((item) => item.trim()).filter(Boolean)
}

function normalizeProbeModelOptions(options: ProbeModelOption[] = [], defaultModel = defaultProbeModel, current = "") {
  const seen = new Map<string, ProbeModelOption>()
  const add = (item: ProbeModelOption) => {
    const id = item.id?.trim()
    if (!id) return
    seen.set(id, { ...item, id })
  }
  add({ id: defaultModel, source: "default" })
  options.forEach(add)
  if (current.trim()) add({ id: current.trim(), source: "current" })
  const first = seen.get(defaultModel)
  if (first) seen.delete(defaultModel)
  const rest = Array.from(seen.values()).sort((a, b) => a.id.localeCompare(b.id, "zh-CN"))
  return first ? [first, ...rest] : rest
}

function parseJSONList(raw?: string) {
  if (!raw) return []
  try {
    const list = JSON.parse(raw)
    return Array.isArray(list) ? list.map(String).filter(Boolean) : []
  } catch {
    return []
  }
}
