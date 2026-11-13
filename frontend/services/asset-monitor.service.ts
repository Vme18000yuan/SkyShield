import { api } from "@/lib/api-client"
import type { AssetMonitorSnapshot } from "@/types/asset-monitor.types"

export class AssetMonitorService {
  static async current(organizationIds: number[]): Promise<AssetMonitorSnapshot> {
    return (await api.get<AssetMonitorSnapshot>("/assetMonitors/current", {
      params: { organizationIds: organizationIds.join(",") },
    })).data
  }
}
