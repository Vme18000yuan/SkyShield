export interface MonitorCount { key: string; count: number }
export interface MonitorOrganization { id: number; displayName: string; targetCount: number; assetCount: number }
export interface MonitorLocation {
  key: string; latitude: number | null; longitude: number | null; countryCode: string
  label: string; count: number; precision: "ip" | "dns" | "country"; expired: boolean
  regionName?: string; cityName?: string
}
export interface AssetMonitorSnapshot {
  generatedAt: string
  organizations: MonitorOrganization[]
  summary: {
    totalAssets: number; targets: number; ipAssets: number; risks: number
    newAssets24h: number; newRisks24h: number; runningScans: number; pendingScans: number
    locatedAssets: number; unlocatedAssets: number
  }
  assetTypes: MonitorCount[]; severities: MonitorCount[]; ports: MonitorCount[]; technologies: MonitorCount[]
  assetCategories?: MonitorCount[]
  inventoryCategories?: MonitorCount[]
  trend: Array<{ date: string; assets: number; risks: number }>
  recentAssets: Array<{ id: string; label: string; assetType: string; displayType?: string; firstSeenAt: string }>
  locations: MonitorLocation[]
}
