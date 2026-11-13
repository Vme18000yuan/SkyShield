import { api } from "@/lib/api-client"

export interface AIRetestFinding { id: string; node_id: string; task_id: string; name: string; status: string }
export interface AIFindingRetest {
  id: string
  finding_id: string
  conversation_id: string | null
  status: "pending" | "running" | "completed" | "failed" | "stopped"
  verdict: "" | "reproduced" | "fixed" | "inconclusive"
  notes: string
  summary: string
  evidence: string
  error: string
  created_at: string
  started_at: string | null
  finished_at: string | null
}

export const aiFindingRetestService = {
  findings: async (vulnerabilityId: number) =>
    (await api.get<{ findings: AIRetestFinding[] }>(`/aiVulnerabilityFindings/${vulnerabilityId}`)).data.findings ?? [],
  history: async (findingId: string) =>
    (await api.get<{ retests: AIFindingRetest[] }>(`/aiFindings/${encodeURIComponent(findingId)}/retests`)).data.retests ?? [],
  start: async (findingId: string, notes: string) =>
    (await api.post<{ retest: AIFindingRetest; created: boolean }>(`/aiFindings/${encodeURIComponent(findingId)}/retests`, { notes })).data,
}
