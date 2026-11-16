import { AppWindow, Building2, Globe, Link2, Radio, Server, Waypoints, type LucideIcon } from "lucide-react"
import type { CoverageKind } from "@/types/asset-coverage.types"

export const coverageVisuals: Record<CoverageKind, { icon: LucideIcon; color: string; layer: number }> = {
  company: { icon: Building2, color: "#64748b", layer: 0 }, root_domain: { icon: Globe, color: "#6366f1", layer: 1 },
  subdomain: { icon: Waypoints, color: "#3b82f6", layer: 2 }, ip: { icon: Server, color: "#0891b2", layer: 2 },
  service: { icon: Radio, color: "#d97706", layer: 3 }, app: { icon: AppWindow, color: "#c026d3", layer: 3 }, endpoint: { icon: Link2, color: "#e11d48", layer: 4 },
}
