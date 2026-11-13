export interface FofaSettings {
  name: string
  email: string
  configured: boolean
  canManage: boolean
  canSearch: boolean
  updatedAt: string | null
}
export interface FofaSearchInput { query: string; page: number; pageSize: number; full: boolean }
export interface FofaRecord {
  rowId: number
  host: string
  ip: string
  port: number
  protocol: string
  domain: string
  title: string
  server: string
  country: string
  countryName: string
  region: string
  city: string
  icp: string
  baseProtocol: string
  url: string
  hostname: string
  target: string
  importable: boolean
  importError: string
}
export interface FofaSearchPage {
  id: string
  name: string
  query: string
  page: number
  pageSize: number
  full: boolean
  total: number
  results: FofaRecord[]
  hasNext: boolean
  createdAt: string
  expiresAt: string
}
export interface FofaImportInput { searchId: string; rows: number[]; organizationId: number; targetId?: number }
export interface FofaImportResult {
  importedRows: number
  failedRows: number
  assetCount: number
  results: Array<{ rowId: number; targetId: number; assetIds: string[]; warnings: string[]; reused: boolean }>
  errors: Array<{ rowId: number; message: string }>
}
