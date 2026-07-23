const routeTitles: Record<string, string> = {
  "/": "主页",
  "/shop-goods": "商品总览",
  "/shops": "店铺监控",
  "/auto-groups": "智能分组",
  "/captcha": "打码平台",
  "/notifications": "通知渠道",
  "/settings": "系统设置",
}

export function documentTitleForPath(pathname: string, appTitle: string): string {
  const normalizedPath = pathname.replace(/\/+$/, "") || "/"
  const title = appTitle.trim() || "UpstreamOps"
  const routeTitle = routeTitles[normalizedPath]
  return routeTitle ? `${routeTitle} - ${title}` : title
}
