"use client"

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"
import {
  apiFetch,
  setToken,
  setUnauthorizedHandler,
} from "@/lib/api"

type AuthStatus = "loading" | "anonymous" | "authenticated"

interface AuthContextValue {
  status: AuthStatus
  username: string | null
  /** 后端关闭了鉴权（AUTH_ENABLED=false），整套 UI 当作"已登录"渲染。 */
  authDisabled: boolean
  /** 是否由 Sub2API 自定义菜单 iframe 打开。 */
  embeddedMode: boolean
  /** 嵌入登录失败时展示给用户的可操作错误。 */
  embeddedError: string | null
  login: (username: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

interface LoginResponse {
  token?: string
  expires_at?: number
  username: string
  auth_disabled?: boolean
  source?: "sub2api"
}

interface MeResponse {
  username: string
  auth_disabled?: boolean
}

interface EmbeddedLoginPayload {
  token: string
  user_id: string | null
  src_host: string | null
}

function readEmbeddedLoginPayload(): EmbeddedLoginPayload | null {
  if (typeof window === "undefined") return null
  const params = new URLSearchParams(window.location.search)
  if (params.get("ui_mode") !== "embedded") return null
  const token = params.get("token")
  if (!token) return null
  return {
    token,
    user_id: params.get("user_id"),
    src_host: params.get("src_host"),
  }
}

function isEmbeddedModeURL() {
  if (typeof window === "undefined") return false
  return new URLSearchParams(window.location.search).get("ui_mode") === "embedded"
}

function removeEmbeddedTokenFromURL() {
  if (typeof window === "undefined") return
  const url = new URL(window.location.href)
  url.searchParams.delete("token")
  window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`)
}

function embeddedLoginMessage(err?: unknown) {
  const detail = err instanceof Error ? err.message : ""
  return detail
    ? `Sub2API 会话校验失败：${detail}。请回到 Sub2API 刷新页面或重新登录。`
    : "Sub2API 会话已失效，请回到 Sub2API 刷新页面或重新登录。"
}

export function AuthProvider({ children }: { children: ReactNode }) {
  // 启动时无论有没有 token 都先 /auth/me 探测一次，因为后端可能开了"无鉴权模式"。
  const [status, setStatus] = useState<AuthStatus>("loading")
  const [username, setUsername] = useState<string | null>(null)
  const [authDisabled, setAuthDisabled] = useState(false)
  const [embeddedMode, setEmbeddedMode] = useState(() => isEmbeddedModeURL())
  const [embeddedError, setEmbeddedError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const payload = readEmbeddedLoginPayload()
    setEmbeddedMode(isEmbeddedModeURL())

    async function bootstrap() {
      if (payload) {
        try {
          const res = await apiFetch<LoginResponse>("/auth/sub2api/exchange", {
            method: "POST",
            body: JSON.stringify(payload),
            skipAuthErrorHandler: true,
          })
          if (cancelled) return
          if (res.token) {
            setToken(res.token)
          }
          if (res.auth_disabled) {
            setAuthDisabled(true)
          }
          setEmbeddedError(null)
          setUsername(res.username)
          setStatus("authenticated")
          return
        } catch (err) {
          if (cancelled) return
          setToken(null)
          setUsername(null)
          setEmbeddedError(embeddedLoginMessage(err))
          setStatus("anonymous")
          return
        } finally {
          removeEmbeddedTokenFromURL()
        }
      }

      try {
        const me = await apiFetch<MeResponse>("/auth/me", { skipAuthErrorHandler: true })
        if (cancelled) return
        if (me.auth_disabled) {
          // 后端关了鉴权：清掉本地任何遗留 token，避免下次开启时困惑
          setToken(null)
          setAuthDisabled(true)
          setUsername(me.username)
          setStatus("authenticated")
          return
        }
        // 后端开启鉴权：me 成功说明现有 token 仍有效
        setEmbeddedError(null)
        setUsername(me.username)
        setStatus("authenticated")
      } catch {
        if (cancelled) return
        // me 失败：本地 token（如果有）已失效；显示登录页
        setToken(null)
        setUsername(null)
        if (isEmbeddedModeURL()) {
          setEmbeddedError(embeddedLoginMessage())
        }
        setStatus("anonymous")
      }
    }

    bootstrap()
    return () => {
      cancelled = true
    }
  }, [])

  // 注册全局 401 回调：让 apiFetch 在任何业务请求 401 时把我们打回登录页。
  // 鉴权关闭时不可能拿到 401，这里也无害。
  useEffect(() => {
    setUnauthorizedHandler(() => {
      setUsername(null)
      if (embeddedMode) {
        setEmbeddedError(embeddedLoginMessage())
      }
      setStatus("anonymous")
    })
    return () => setUnauthorizedHandler(null)
  }, [embeddedMode])

  const login = useCallback(async (u: string, p: string) => {
    const res = await apiFetch<LoginResponse>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username: u, password: p }),
      skipAuthErrorHandler: true,
    })
    if (res.token) {
      setToken(res.token)
    }
    if (res.auth_disabled) {
      setAuthDisabled(true)
    }
    setEmbeddedError(null)
    setUsername(res.username)
    setStatus("authenticated")
  }, [])

  const logout = useCallback(() => {
    // 鉴权关闭时 logout 按钮在 UI 上不会展示，这里仍保留兜底逻辑
    apiFetch("/auth/logout", { method: "POST" }).catch(() => {})
    setToken(null)
    setUsername(null)
    if (embeddedMode) {
      setEmbeddedError("已退出当前 Ops 会话。请回到 Sub2API 刷新页面重新进入。")
    }
    setStatus("anonymous")
  }, [embeddedMode])

  const value = useMemo(
    () => ({ status, username, authDisabled, embeddedMode, embeddedError, login, logout }),
    [status, username, authDisabled, embeddedMode, embeddedError, login, logout],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error("useAuth must be used within AuthProvider")
  }
  return ctx
}
