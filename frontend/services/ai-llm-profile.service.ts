import { api } from "@/lib/api-client"
import type { LLMProfile, LLMProfileInput } from "@/types/ai-llm-profile.types"

export class AILLMProfileService {
  static async list(): Promise<LLMProfile[]> {
    const response = await api.get<{ profiles: LLMProfile[] }>("/aiLlmProfiles")
    return response.data.profiles
  }
  static async save(id: string | null, input: LLMProfileInput): Promise<LLMProfile> {
    const response = id
      ? await api.put<LLMProfile>(`/aiLlmProfiles/${encodeURIComponent(id)}`, input)
      : await api.post<LLMProfile>("/aiLlmProfiles", input)
    return response.data
  }
  static async remove(id: string): Promise<void> {
    await api.delete(`/aiLlmProfiles/${encodeURIComponent(id)}`)
  }
  static async test(id: string): Promise<{ latencyMs: number; reply: string }> {
    const response = await api.post<{ latencyMs: number; reply: string }>(`/aiLlmProfiles/${encodeURIComponent(id)}/test`)
    return response.data
  }
}
