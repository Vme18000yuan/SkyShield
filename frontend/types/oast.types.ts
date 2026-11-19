export type OASTScope = { kind: "conversation" | "task"; id: string }

export type OASTProbe = {
  id: string
  conversationId?: string
  taskId?: string
  runId?: string
  label: string
  httpUrl: string
  dnsName?: string
  createdAt: string
  expiresAt: string
  revokedAt?: string
  eventCount: number
  lastEventAt?: string
  state: "active" | "expired" | "revoked" | "saturated"
}

export type OASTEvent = {
  id: string
  receivedAt: string
  protocol: "http" | "dns"
  remoteAddr: string
  method?: string
  host?: string
  path?: string
  headers?: string
  bodyBase64?: string
  queryName?: string
  queryType?: string
  truncated: boolean
}

export type OASTProbePage = { items: OASTProbe[]; nextBefore: string; enabled: boolean; dnsEnabled: boolean }
export type OASTEventPage = { probe: OASTProbe; items: OASTEvent[]; nextAfter: string; hasMore: boolean }
export type OASTCreateInput = { label: string; ttlSeconds: number; requestId: string }
