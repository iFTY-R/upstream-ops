import { useEffect, useMemo, useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { useTheme } from "next-themes"
import { Activity, Github, Home, LogOut, PackageSearch, RefreshCw, Sun, Moon, Settings, Store, SlidersHorizontal, type LucideIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useAuth } from "@/lib/auth-context"
import { apiFetch } from "@/lib/api"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { useAppVersion, useChannels } from "@/lib/queries"
import type { AppVersion } from "@/lib/api-types"
import { relativeTime } from "@/lib/format"
import { toast } from "sonner"

export function MonitorHeader() {
  const navigate = useNavigate()
  const location = useLocation()
  const { theme, setTheme } = useTheme()
  const { username, authDisabled, logout } = useAuth()
  const refresh = useTriggerRefresh()
  const channels = useChannels()
  const appVersion = useAppVersion()
  const [mounted, setMounted] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [checkingVersion, setCheckingVersion] = useState(false)

  const appTitle = appVersion.data?.title?.trim() || "UpstreamOps"
  const version = appVersion.data?.version?.trim()
  const latestVersion = appVersion.data?.latest_version?.trim()
  const updateAvailable = Boolean(appVersion.data?.update_available && latestVersion)
  const updateURL = appVersion.data?.release_url?.trim() || appVersion.data?.repo_url?.trim()
  const navItems = [
    { label: "主页", path: "/", icon: Home },
    { label: "商品", path: "/shop-goods", icon: PackageSearch },
    { label: "店铺", path: "/shops", icon: Store },
    { label: "分组", path: "/auto-groups", icon: SlidersHorizontal },
    { label: "设置", path: "/settings", icon: Settings },
  ]
  const fullNavLabels: Record<string, string> = {
    商品: "商品总览",
    店铺: "店铺监控",
    分组: "智能分组",
  }

  useEffect(() => setMounted(true), [])

  useEffect(() => {
    document.title = appTitle
  }, [appTitle])

  /**
   * 找出所有渠道中最近一次采集时间——这是"上次采集"展示的依据，
   * 让用户知道页面上的余额到底是多新的快照（区别于"我刚点了刷新"）。
   */
  const lastCollectedAt = useMemo(() => {
    const list = channels.data ?? []
    let best: string | null = null
    let bestT = -Infinity
    for (const c of list) {
      if (!c.last_balance_at) continue
      const t = new Date(c.last_balance_at).getTime()
      if (Number.isFinite(t) && t > bestT) {
        bestT = t
        best = c.last_balance_at
      }
    }
    return best
  }, [channels.data])

  function handleRefresh() {
    setSyncing(true)
    refresh()
    setTimeout(() => setSyncing(false), 800)
  }

  async function handleCheckVersion() {
    setCheckingVersion(true)
    try {
      const result = await apiFetch<AppVersion>("/version?force=1")
      appVersion.setData(result)
      if (result.update_error) {
        toast.error(result.update_error)
      } else if (result.update_available && result.latest_version) {
        toast.warning(`发现新版本 ${result.latest_version}`)
      } else {
        toast.success("当前已是最新版本")
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "检测更新失败")
    } finally {
      setCheckingVersion(false)
    }
  }

  return (
    <header className="sticky top-0 z-20 border-b border-border bg-background/95 shadow-[0_10px_30px_rgba(15,23,42,0.06)] backdrop-blur-sm">
      <div className="mx-auto flex max-w-360 flex-col gap-2 px-3 py-2 sm:h-14 sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:px-5 sm:py-0">
        <div className="flex min-w-0 items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2.5">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-foreground text-background shadow-sm sm:size-8 sm:rounded-lg">
              <Activity className="size-4" strokeWidth={2.5} />
            </div>
            <div className="min-w-0">
              <h1 className="truncate text-base font-semibold tracking-tight text-foreground">{appTitle}</h1>
              {version ? (
                <p className="truncate text-[11px] leading-3 text-muted-foreground">
                  <button
                    type="button"
                    className="font-medium underline-offset-2 hover:text-foreground hover:underline"
                    onClick={handleCheckVersion}
                    disabled={checkingVersion}
                    title="点击检测更新"
                  >
                    {checkingVersion ? "检测中..." : `v${version}`}
                  </button>
                  {updateAvailable ? (
                    <a
                      href={updateURL || "https://github.com/ifty-r/upstream-ops"}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="ml-2 font-medium text-emerald-600 underline-offset-2 hover:text-emerald-700 hover:underline"
                    >
                      有新版本 {latestVersion}
                    </a>
                  ) : null}
                </p>
              ) : null}
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-1 sm:hidden">
            <Button
              variant="outline"
              size="icon"
              onClick={handleRefresh}
              disabled={syncing}
              className="size-9 rounded-xl border-border bg-background text-foreground shadow-sm hover:bg-muted"
              aria-label="刷新视图"
            >
              <RefreshCw className={cn("size-4", syncing && "animate-spin")} />
            </Button>
            <Button
              variant="outline"
              size="icon"
              onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
              className="size-9 rounded-xl border-border bg-background text-foreground shadow-sm hover:bg-muted"
              aria-label="切换主题"
            >
              {mounted && theme === "dark" ? <Moon className="size-4" /> : <Sun className="size-4" />}
            </Button>
            {authDisabled ? null : (
              <Button
                variant="outline"
                size="icon"
                onClick={logout}
                className="size-9 rounded-xl border-border bg-background text-foreground shadow-sm hover:bg-muted"
                aria-label="退出登录"
              >
                <LogOut className="size-4" />
              </Button>
            )}
          </div>
        </div>

        <div className="-mx-3 overflow-hidden sm:hidden">
          <div className="flex gap-2 overflow-x-auto px-3 pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {navItems.map((item) => (
              <MobileNavButton
                key={item.path}
                label={item.label}
                icon={item.icon}
                active={location.pathname === item.path}
                onClick={() => navigate(item.path)}
              />
            ))}
            <MobileNavButton
              label="GitHub"
              icon={Github}
              href="https://github.com/ifty-r/upstream-ops"
            />
          </div>
        </div>

        <div className="hidden shrink-0 items-center gap-1.5 sm:flex sm:gap-3">
          <div className="hidden items-center gap-2 sm:flex">
            <span className="text-xs text-muted-foreground">
              {"上次采集 "}
              <span className="font-medium text-foreground">{relativeTime(lastCollectedAt)}</span>
            </span>
            <Tooltip delayDuration={200}>
              <TooltipTrigger asChild>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleRefresh}
                  disabled={syncing}
                  className="gap-1.5 border-border bg-background text-foreground hover:bg-muted"
                  aria-label="刷新视图"
                >
                  <RefreshCw className={cn("size-3.5", syncing && "animate-spin")} />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom" className="max-w-xs text-xs">
                <p>{"重新拉取最新的快照数据。"}</p>
                <p className="mt-1 text-muted-foreground">
                  {"提示：实际采集由后台定时任务执行，如需立即采集请到具体渠道点 \"同步\"。"}
                </p>
              </TooltipContent>
            </Tooltip>
          </div>

          {navItems.map((item) => (
            <HeaderIconButton
              key={item.path}
              label={fullNavLabels[item.label] ?? item.label}
              icon={item.icon}
              active={location.pathname === item.path}
              onClick={() => navigate(item.path)}
            />
          ))}

          <Tooltip delayDuration={200}>
            <TooltipTrigger asChild>
              <Button
                asChild
                variant="outline"
                size="icon"
                className="size-8 border-border bg-background text-foreground hover:bg-muted"
                aria-label="GitHub 仓库"
              >
                <a
                  href="https://github.com/ifty-r/upstream-ops"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <Github className="size-3.5" />
                </a>
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom" className="text-xs">
              {"GitHub · ifty-r/upstream-ops"}
            </TooltipContent>
          </Tooltip>

          {/* theme toggle */}
          <Tooltip delayDuration={200}>
            <TooltipTrigger asChild>
              <Button
                variant="outline"
                size="icon"
                onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
                className="size-8 border-border bg-background text-foreground hover:bg-muted"
                aria-label="切换主题"
              >
                {mounted && theme === "dark" ? (
                  <Moon className="size-3.5" />
                ) : (
                  <Sun className="size-3.5" />
                )}
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom" className="text-xs">
              {mounted && theme === "dark" ? "深色模式 · 点击切换浅色" : "浅色模式 · 点击切换深色"}
            </TooltipContent>
          </Tooltip>

          {/* logout — 鉴权关闭时整个按钮不显示 */}
          {authDisabled ? null : (
            <Tooltip delayDuration={200}>
              <TooltipTrigger asChild>
                <Button
                  variant="outline"
                  size="icon"
                  onClick={logout}
                  className="size-8 border-border bg-background text-foreground hover:bg-muted"
                  aria-label="退出登录"
                >
                  <LogOut className="size-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom" className="text-xs">
                {username ? `${username} · 退出登录` : "退出登录"}
              </TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>
    </header>
  )
}

function HeaderIconButton({
  label,
  icon: Icon,
  active,
  onClick,
}: {
  label: string
  icon: LucideIcon
  active?: boolean
  onClick: () => void
}) {
  return (
    <Tooltip delayDuration={200}>
      <TooltipTrigger asChild>
        <Button
          variant="outline"
          size="icon"
          onClick={onClick}
          className={cn(
            "size-8 border-border bg-background text-foreground hover:bg-muted",
            active && "border-foreground/20 bg-foreground text-background hover:bg-foreground/90",
          )}
          aria-label={label}
        >
          <Icon className="size-3.5" />
        </Button>
      </TooltipTrigger>
      <TooltipContent side="bottom" className="text-xs">
        {label}
      </TooltipContent>
    </Tooltip>
  )
}

function MobileNavButton({
  label,
  icon: Icon,
  active,
  onClick,
  href,
}: {
  label: string
  icon: LucideIcon
  active?: boolean
  onClick?: () => void
  href?: string
}) {
  const className = cn(
    "inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border px-3 text-xs font-medium shadow-sm transition",
    active
      ? "border-foreground bg-foreground text-background"
      : "border-border bg-card text-foreground hover:bg-muted",
  )
  const content = (
    <>
      <Icon className="size-3.5" />
      <span>{label}</span>
    </>
  )

  if (href) {
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className={className}>
        {content}
      </a>
    )
  }

  return (
    <button type="button" onClick={onClick} className={className}>
      {content}
    </button>
  )
}
