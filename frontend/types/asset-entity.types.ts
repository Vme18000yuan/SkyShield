export type AssetEntityType = "root_domain" | "subdomain" | "ip" | "app" | "service" | "endpoint"
export type AssetDisplayType = AssetEntityType | "web"
export type AssetCategory = "web" | "non_web_service"
export type AssetRegistrationType = AssetEntityType | "web"
export interface AssetOrganization { id: number; name: string }

export interface AssetEntity {
  name: string
  id: string
  assetType: AssetEntityType
  displayType?: AssetDisplayType
  label: string
  method: string | null
  sourceCount: number
  derived?: boolean
  applicationSources?: number
  firstSeenAt: string
  lastSeenAt: string
  organizations?: AssetOrganization[]
}

export interface AssetEntityListParams {
  assetType?: AssetEntityType
  assetCategory?: AssetCategory
  q?: string
  pageSize: number
  pageToken?: string
  page?: number
  organizationId?: number
  rootDomainId?: string
}

export interface AssetEntityListResponse {
  assetEntities: AssetEntity[]
  nextPageToken: string
  totalSize?: number
  page?: number
}

export interface PortAsset {
  label: string; port: number; service: string; serviceSource: "observed" | "port" | "unknown"
  firstSeenAt: string; lastSeenAt: string; organizations: AssetOrganization[]
}
export interface PortAssetListResponse { ports: PortAsset[]; totalSize: number; page: number }
export type AssetInventoryItem = { kind: "asset"; asset: AssetEntity } | { kind: "port"; port: PortAsset }
export interface AssetInventoryResponse { items: AssetInventoryItem[]; totalSize: number; page: number }
export interface RootDomainOverview {
  id: string; domain: string; organizations: AssetOrganization[]; firstSeenAt: string; lastSeenAt: string
  counts: { subdomains: number; ips: number; apps: number; ports: number; web: number; urls: number }
}
export interface ApplicationWebsite {
  id: number; url: string; host: string; title: string; statusCode: number | null; technologies: string[]
  evidenceKind: "title" | "technology" | "binding"; evidenceValue: string; firstSeenAt: string
}
export interface ApplicationOverview { application: AssetEntity; websites: ApplicationWebsite[]; totalSize: number; page: number }

export interface AssetRegistrationInput {
  type: AssetEntityType
  targetId?: number
  organizationId?: number
  domain?: string
  ip?: string
  url?: string
  method?: string
  appName?: string
  bundleId?: string
  platform?: string
  recordType?: string
  recordValue?: string[]
  boundDomains?: string[]
  serviceName?: string
  port?: number
  proto?: string
  pageTitle?: string
  statusCode?: number
  contentLength?: number
  technologies?: string[]
}

export interface AssetRegistrationResponse {
  requestId: string
  results: Array<{ index: number; id: string; type: AssetEntityType; projection: string }>
  errors: Array<{ index: number; error: string }>
}
