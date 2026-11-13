export type CoverageKind = "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint"

export type CoverageNode = {
  key: string
  kind: CoverageKind
  label: string
  tested: boolean
  in_scope: boolean
  asset_id?: string
  company_id?: string
  domain?: string
  root_domain?: string
  ip?: string
  url?: string
  port?: number
  service_type?: string
  app_name?: string
  page_title?: string
  status_code?: number
}
export type CoverageEdge = { src: string; dst: string }
export type CoverageGraph = { nodes: CoverageNode[]; edges: CoverageEdge[]; truncated: boolean }

export type CoverageScope = { kind: "task"; id: string } | { kind: "root"; id: string; organizationId?: number }
export type CoverageRef = { id: string; kind: string; state: string; summary: string; inherited?: boolean; source_task_id?: string; task_name?: string }
export type CoverageRefs = { intents: CoverageRef[]; facts: CoverageRef[]; findings: CoverageRef[]; truncated?: boolean }
