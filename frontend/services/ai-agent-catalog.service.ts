import { api } from "@/lib/api-client"
import type { AIAgentCatalogItem, AIAgentPromptVersion, CreateAIAgentInput, UpdateAIAgentInput } from "@/types/ai-agent-catalog.types"

const path = "/aiAgents"

export class AIAgentCatalogService {
  static async list(): Promise<AIAgentCatalogItem[]> {
    return (await api.get<{ agents: AIAgentCatalogItem[] }>(path)).data.agents
  }

  static async get(key: string): Promise<AIAgentCatalogItem> {
    return (await api.get<AIAgentCatalogItem>(`${path}/${encodeURIComponent(key)}`)).data
  }

  static async create(input: CreateAIAgentInput): Promise<AIAgentCatalogItem> {
    return (await api.post<AIAgentCatalogItem>(path, input)).data
  }

  static async update(key: string, input: UpdateAIAgentInput): Promise<AIAgentCatalogItem> {
    return (await api.put<AIAgentCatalogItem>(`${path}/${encodeURIComponent(key)}`, input)).data
  }

  static async delete(key: string): Promise<void> {
    await api.delete(`${path}/${encodeURIComponent(key)}`)
  }

  static async promptVersions(key: string): Promise<AIAgentPromptVersion[]> {
    return (await api.get<{ versions: AIAgentPromptVersion[] }>(`${path}/${encodeURIComponent(key)}/promptVersions`)).data.versions
  }

  static async previewPrompt(key: string, template: string): Promise<string> {
    return (await api.post<{ rendered: string }>(`${path}/${encodeURIComponent(key)}/promptPreview`, { template })).data.rendered
  }
}
