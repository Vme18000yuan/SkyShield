import type { MonitorTheme } from "@/lib/asset-monitor-theme"

export const MAX_MONITOR_ORGANIZATIONS = 50

export function parseMonitorSelection(raw: string | null): number[] | null {
  if (!raw || raw.length > 600) return null
  const parts = raw.split(",")
  if (parts.length > MAX_MONITOR_ORGANIZATIONS || parts.some(part => !/^[1-9]\d*$/.test(part))) return null
  const ids = parts.map(Number)
  if (ids.some(id => !Number.isSafeInteger(id) || id > 2147483647) || new Set(ids).size !== ids.length) return null
  return ids.sort((a, b) => a - b)
}

export function monitorScreenHref(ids: number[], theme?: MonitorTheme): string {
  const scope = [...ids].sort((a, b) => a - b).join(",")
  return `/asset-monitor/screen/?organizations=${scope}${theme ? `&theme=${theme}` : ""}`
}

export function isAssetMonitorScreenPath(pathname: string | null): boolean {
  return pathname === "/asset-monitor/screen" || pathname === "/asset-monitor/screen/"
}
