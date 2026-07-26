import { lazy, Suspense } from "react"

import { KpiRow } from "@/components/monitor/kpi-row"
import { MultiplierChanges } from "@/components/monitor/multiplier-changes"
import { ChannelCards } from "@/components/monitor/channel-cards"
import { BottomPanels } from "@/components/monitor/bottom-panels"

// recharts 只被余额概览图使用；懒加载把它从首屏主包拆成独立 chunk。
const BalanceOverview = lazy(() =>
  import("@/components/monitor/balance-overview").then((module) => ({
    default: module.BalanceOverview,
  })),
)

export default function Page() {
  return (
    <>
      <KpiRow />

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-5">
        <div className="lg:col-span-3">
          <Suspense
            fallback={
              <div className="h-64 animate-pulse rounded-xl border border-border bg-card lg:h-100" />
            }
          >
            <BalanceOverview />
          </Suspense>
        </div>
        <div className="lg:col-span-2">
          <MultiplierChanges />
        </div>
      </div>

      <ChannelCards />

      <BottomPanels />
    </>
  )
}
