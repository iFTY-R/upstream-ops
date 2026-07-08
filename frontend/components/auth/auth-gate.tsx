"use client"

import { useAuth } from "@/lib/auth-context"
import { LoginPage } from "@/components/auth/login-page"
import type { ReactNode } from "react"

/**
 * AuthGate 把根渲染分成三态：
 *   loading       本地有 token 但还没验完 — 显示占位
 *   anonymous     未登录 — 显示登录页
 *   authenticated 已登录 — 显示业务内容
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const { status, embeddedMode, embeddedError } = useAuth()

  if (status === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">
        {embeddedMode ? "正在通过 Sub2API 授权..." : "加载中..."}
      </div>
    )
  }
  if (status === "anonymous") {
    if (embeddedMode) {
      return (
        <div className="flex min-h-screen items-center justify-center bg-muted/30 px-4">
          <div className="w-full max-w-md rounded-2xl border bg-background p-6 text-center shadow-sm">
            <div className="text-base font-semibold">Sub2API 授权不可用</div>
            <p className="mt-3 text-sm leading-6 text-muted-foreground">
              {embeddedError ?? "Sub2API 会话已失效，请回到 Sub2API 刷新页面或重新登录。"}
            </p>
          </div>
        </div>
      )
    }
    return <LoginPage />
  }
  return <>{children}</>
}
