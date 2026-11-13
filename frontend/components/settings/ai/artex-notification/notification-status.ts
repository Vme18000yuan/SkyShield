const delivery: Record<string, string> = { pending: "等待中", sending: "发送中", sent: "已送达", failed: "失败", skipped: "已跳过" }
const severity: Record<string, string> = { critical: "严重", high: "高危", medium: "中危", low: "低危", info: "信息" }
export const toneClasses: Record<string, string> = { default: "", red: "text-rose-600", amber: "text-amber-600", green: "text-emerald-600" }
export function statusMeta(kind: "severity" | "delivery", value: string): { label: string; tone: string } {
  const labels = kind === "severity" ? severity : delivery
  const tone = value === "critical" || value === "high" || value === "failed" ? "red" : value === "medium" || value === "pending" ? "amber" : value === "sent" ? "green" : "default"
  return { label: labels[value] || value || "未知", tone }
}
