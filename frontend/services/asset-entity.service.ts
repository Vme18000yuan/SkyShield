import { api } from "@/lib/api-client"
import type { ApplicationOverview, AssetEntityListParams, AssetEntityListResponse, AssetInventoryResponse, AssetRegistrationInput, AssetRegistrationResponse, PortAssetListResponse, RootDomainOverview } from "@/types/asset-entity.types"

export class AssetEntityService {
  static async list(params: AssetEntityListParams): Promise<AssetEntityListResponse> {
    const response = await api.get<AssetEntityListResponse>("/assetEntities", { params })
    return response.data
  }
  static async register(assets: AssetRegistrationInput[], requestId: string): Promise<AssetRegistrationResponse> {
    const response = await api.post<AssetRegistrationResponse>("/assetEntities:batchCreate", { assets, requestId })
    return response.data
  }
  static async listPorts(params: Pick<AssetEntityListParams, "q" | "page" | "pageSize" | "organizationId" | "rootDomainId">): Promise<PortAssetListResponse> {
    return (await api.get<PortAssetListResponse>("/assetPorts", { params })).data
  }
  static async listInventory(params: Pick<AssetEntityListParams, "q" | "page" | "pageSize" | "organizationId" | "rootDomainId">): Promise<AssetInventoryResponse> {
    return (await api.get<AssetInventoryResponse>("/assetInventory", { params })).data
  }
  static async rootOverview(id: string, organizationId?: number): Promise<RootDomainOverview> {
    return (await api.get<RootDomainOverview>(`/assetEntities/${encodeURIComponent(id)}/rootDomainOverview`, { params: { organizationId } })).data
  }
  static async applicationOverview(id: string, params: { organizationId?: number; page: number; pageSize: number }): Promise<ApplicationOverview> {
    return (await api.get<ApplicationOverview>(`/assetEntities/${encodeURIComponent(id)}/applicationOverview`, { params })).data
  }
}
