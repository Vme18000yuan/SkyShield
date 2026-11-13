"use client"

import * as React from "react"
import { DEFAULT_MONITOR_THEME, MONITOR_THEME_STORAGE_KEY, parseMonitorTheme, type MonitorTheme } from "@/lib/asset-monitor-theme"

export function useAssetMonitorTheme() {
  const [theme, setTheme] = React.useState<MonitorTheme>(DEFAULT_MONITOR_THEME)
  React.useEffect(() => {
    try { setTheme(parseMonitorTheme(window.localStorage.getItem(MONITOR_THEME_STORAGE_KEY))) } catch { /* storage may be disabled */ }
    const sync = (event: StorageEvent) => { if (event.key === MONITOR_THEME_STORAGE_KEY) setTheme(parseMonitorTheme(event.newValue)) }
    window.addEventListener("storage", sync)
    return () => window.removeEventListener("storage", sync)
  }, [])
  const changeTheme = React.useCallback((next: MonitorTheme) => {
    setTheme(next)
    try { window.localStorage.setItem(MONITOR_THEME_STORAGE_KEY, next) } catch { /* the selected link still carries this theme */ }
  }, [])
  return [theme, changeTheme] as const
}
