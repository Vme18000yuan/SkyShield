import { api } from "@/lib/api-client"
import type { AIActivityPage, AIConversation, AIModelOption } from "@/types/ai-chat.types"

export class AIChatService {
  static async list(): Promise<AIConversation[]> {
    const response = await api.get<{ conversations: AIConversation[] }>("/aiConversations")
    return response.data.conversations
  }

  static async create(title: string, agentKey = "assistant"): Promise<AIConversation> {
    const response = await api.post<AIConversation>("/aiConversations", { title, agentKey })
    return response.data
  }

  static async send(id: string, message: string): Promise<void> {
    await api.post(`/aiConversations/${encodeURIComponent(id)}/messages`, { message })
  }

  static async stop(id: string): Promise<void> {
    await api.post(`/aiConversations/${encodeURIComponent(id)}/stop`)
  }

  static async models(): Promise<AIModelOption[]> {
    return (await api.get<{ models: AIModelOption[] }>("/aiModels")).data.models
  }

  static async setModel(id: string, llmProfileId: string | null): Promise<void> {
    await api.put(`/aiConversations/${encodeURIComponent(id)}/model`, { llmProfileId })
  }

  static async updateMetadata(id: string, input: { title?: string; pinned?: boolean }): Promise<void> {
    await api.patch(`/aiConversations/${encodeURIComponent(id)}`, input)
  }

  static async activities(id: string, after: string): Promise<AIActivityPage> {
    const response = await api.get<AIActivityPage>(`/aiConversations/${encodeURIComponent(id)}/activities`, { params: { after } })
    return response.data
  }
}
