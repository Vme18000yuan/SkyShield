import { api } from "@/lib/api-client"

export interface InfoGatherRun {
  id: string
  target: string
  targetKind: string
  mode: string
  status: string
  error: string
  coverage: { steps?: Record<string, string>; tools?: Record<string, boolean> }
  createdAt: string
  updatedAt: string
  imported: number
  pending: number
  observations: number
}

export interface InfoGatherRecord {
  kind: "asset" | "vulnerability" | "observation"
  id: string
  status: string
  reason: string
  payload: {
    type?: string
    category?: string
    value?: string
    url?: string
    title?: string
    port?: number
    protocol?: string
    cdn?: boolean
    scope: string
    evidence: string[]
  }
}

export interface InfoGatherRecords { records: InfoGatherRecord[]; total: number; offset: number; pageSize: number }
export interface InfoGatherImportResult { imported: number; failed: number; failures: Array<{ record: { kind: string; id: string }; reason: string }> }

export const infoGather = {
  runs: async () => (await api.get<{ runs: InfoGatherRun[] }>("/aiInfoGatherRuns")).data.runs,
  records: async (id: string, offset: number) => (await api.get<InfoGatherRecords>(`/aiInfoGatherRuns/${encodeURIComponent(id)}/records`, { params: { offset } })).data,
  sync: async (id: string, targetId: number, records: Array<{ kind: string; id: string }>, allowCdn: boolean) =>
    (await api.post<InfoGatherImportResult>(`/aiInfoGatherRuns/${encodeURIComponent(id)}/sync`, { targetId, records, allowCdn })).data,
  ignore: async (id: string, records: Array<{ kind: string; id: string }>) =>
    (await api.post<{ processed: number }>(`/aiInfoGatherRuns/${encodeURIComponent(id)}/ignore`, { records })).data,
}
