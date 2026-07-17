import { lazy, StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes, useLocation } from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import { ThemeProvider } from '@/components/theme-provider'
import { AuthProvider, useAuth } from '@/lib/auth-context'
import { RefreshProvider } from '@/lib/refresh-context'
import { AddChannelProvider } from '@/lib/add-channel-context'
import { AuthGate } from '@/components/auth/auth-gate'
import { AppShell } from '@/components/app-shell'
import { Toaster } from '@/components/ui/sonner'
import '@/app/globals.css'

// Operational screens are independent routes. Load them on demand so the dashboard does not pay for every tool up front.
const DashboardPage = lazy(() => import('@/app/page'))
const CaptchaPage = lazy(() => import('@/app/captcha-page'))
const NotificationsPage = lazy(() => import('@/app/notifications-page'))
const AutoGroupsPage = lazy(() => import('@/app/auto-groups-page'))
const ShopsPage = lazy(() => import('@/app/shops-page'))
const ShopGoodsPage = lazy(() => import('@/app/shop-goods-page'))
const SettingsPage = lazy(() => import('@/app/settings-page'))

function ProtectedApplication() {
  return (
    <AuthGate>
      <RefreshProvider>
        <AddChannelProvider>
          <Suspense fallback={<div className="min-h-screen" aria-busy="true" />}>
            <Routes>
              <Route element={<AppShell />}>
                <Route index element={<DashboardPage />} />
                <Route path="captcha" element={<CaptchaPage />} />
                <Route path="notifications" element={<NotificationsPage />} />
                <Route path="auto-groups" element={<AutoGroupsPage />} />
                <Route path="shops" element={<ShopsPage />} />
                <Route path="shop-goods" element={<ShopGoodsPage />} />
                <Route path="settings" element={<SettingsPage />} />
              </Route>
            </Routes>
          </Suspense>
        </AddChannelProvider>
      </RefreshProvider>
      <Toaster richColors closeButton position="top-right" />
    </AuthGate>
  )
}

function Application() {
  const location = useLocation()
  const { status } = useAuth()
  const isPublicShopGoods = location.pathname.replace(/\/+$/, "") === "/shop-goods" && status === "anonymous"

  if (!isPublicShopGoods) return <ProtectedApplication />

  return (
    <div className="min-h-screen bg-background">
      <main className="mx-auto max-w-360 space-y-4 px-3 py-3 sm:space-y-5 sm:px-5 sm:py-5">
        <Suspense fallback={<div className="min-h-screen" aria-busy="true" />}>
          <ShopGoodsPage publicMode />
        </Suspense>
      </main>
    </div>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider attribute="class" defaultTheme="light" enableSystem disableTransitionOnChange>
      <AuthProvider>
        <BrowserRouter>
          <Application />
        </BrowserRouter>
      </AuthProvider>
    </ThemeProvider>
  </StrictMode>,
)
