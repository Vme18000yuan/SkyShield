import { api } from "@/lib/api-client"
import type { FofaImportInput, FofaImportResult, FofaSearchInput, FofaSearchPage, FofaSettings } from "@/types/fofa.types"

export class FofaService {
  static async settings(): Promise<FofaSettings> { return (await api.get<FofaSettings>("/fofaSettings/current")).data }
  static async save(input: { email: string; apiKey: string }): Promise<FofaSettings> {
    return (await api.put<FofaSettings>("/fofaSettings/current", input)).data
  }
  static async clear(): Promise<void> { await api.delete("/fofaSettings/current") }
  static async test(): Promise<void> { await api.post("/fofaSettings/current/test", undefined, { timeout: 20000 }) }
  static async search(input: FofaSearchInput): Promise<FofaSearchPage> {
    return (await api.post<FofaSearchPage>("/fofaSearches", input, { timeout: 45000 })).data
  }
  static async sync(input: FofaImportInput): Promise<FofaImportResult> {
    const { searchId, ...body } = input
    return (await api.post<FofaImportResult>(`/fofaSearches/${encodeURIComponent(searchId)}/import`, body, { timeout: 65000 })).data
  }
}
