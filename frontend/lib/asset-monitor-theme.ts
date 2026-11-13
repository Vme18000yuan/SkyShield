export const MONITOR_THEMES = ["gold", "ocean", "violet", "teal", "black"] as const
export type MonitorTheme = typeof MONITOR_THEMES[number]
export const DEFAULT_MONITOR_THEME: MonitorTheme = "gold"
export const MONITOR_THEME_STORAGE_KEY = "skyshield.asset-monitor.theme"
export const MONITOR_THEME_PREVIEWS: Record<MonitorTheme, { background: string; accent: string }> = {
  gold: { background: "#151e27", accent: "#e4bc7b" },
  ocean: { background: "#10253c", accent: "#71b5ff" },
  violet: { background: "#211a30", accent: "#c8a5f5" },
  teal: { background: "#0b1c2a", accent: "#4bd9d1" },
  black: { background: "#111214", accent: "#d8e0e8" },
}

export function isMonitorTheme(value: string | null): value is MonitorTheme {
  return MONITOR_THEMES.includes(value as MonitorTheme)
}

export function parseMonitorTheme(value: string | null): MonitorTheme {
  return isMonitorTheme(value) ? value : DEFAULT_MONITOR_THEME
}
