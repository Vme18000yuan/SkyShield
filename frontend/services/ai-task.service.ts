import { api } from "@/lib/api-client"
import type { AITask, AITaskAction, AITaskActivity, AITaskAssetPage, AITaskGraph, AITaskInput, AITaskPage, AITaskModelState } from "@/types/ai-task.types"

export class AITaskService {
  static async list(before: string, filter?: { q?: string; status?: string; category?: string }): Promise<AITaskPage> {
    return (await api.get<AITaskPage>("/aiTasks", { params: { before: before || undefined, ...filter } })).data
  }
  static async get(id: string): Promise<AITask> {
    return (await api.get<AITask>(`/aiTasks/${encodeURIComponent(id)}`)).data
  }
  static async create(input: AITaskInput): Promise<AITask> {
    return (await api.post<AITask>("/aiTasks", input)).data
  }
  static async setModels(id:string,profileIds:string[],activeProfileId:string|null):Promise<AITaskModelState>{
    return (await api.put<AITaskModelState>(`/aiTasks/${encodeURIComponent(id)}/models`,{profileIds,activeProfileId})).data
  }
  static async control(id: string, action: AITaskAction): Promise<AITask> {
    return (await api.post<AITask>(`/aiTasks/${encodeURIComponent(id)}/actions`, { action })).data
  }
  static async updateMetadata(id: string, patch: { displayName?: string; remark?: string; pinned?: boolean; categoryId?: string | null }): Promise<AITask> {
    return (await api.patch<AITask>(`/aiTasks/${encodeURIComponent(id)}`, patch)).data
  }
  static async rerunIntent(taskId: string, intentId?: string): Promise<number> {
    const path = intentId ? `${encodeURIComponent(intentId)}/rerun` : "rerun-blocked"
    return (await api.post<{ reopened: number }>(`/aiTasks/${encodeURIComponent(taskId)}/intents/${path}`)).data.reopened
  }
  static async controlIntent(taskId: string, intentId: string, action: "pause" | "resume"): Promise<string> {
    return (await api.post<{ state: string }>(`/aiTasks/${encodeURIComponent(taskId)}/intents/${encodeURIComponent(intentId)}/control`, { action })).data.state
  }
  static async cancelIntent(taskId: string, intentId: string, reason: string, mode: "soft" | "hard"): Promise<string> {
    return (await api.post<{ state: string }>(`/aiTasks/${encodeURIComponent(taskId)}/intents/${encodeURIComponent(intentId)}/control`, { action: "cancel", reason, mode })).data.state
  }
  static async sendWorkerMessage(taskId: string, intentId: string, requestId: string, message: string): Promise<void> {
    await api.post(`/aiTasks/${encodeURIComponent(taskId)}/intents/${encodeURIComponent(intentId)}/messages`, { request_id: requestId, message })
  }
  static async graph(id: string): Promise<AITaskGraph> {
    return (await api.get<AITaskGraph>(`/aiTasks/${encodeURIComponent(id)}/graph`)).data
  }
  static async activities(id: string, after: string): Promise<AITaskActivity[]> {
    return (await api.get<{ activities: AITaskActivity[] }>(`/aiTasks/${encodeURIComponent(id)}/activities`, { params: { after } })).data.activities
  }
  static async assets(id: string, after: string): Promise<AITaskAssetPage> {
    return (await api.get<AITaskAssetPage>(`/aiTasks/${encodeURIComponent(id)}/assets`, { params: { after: after || undefined } })).data
  }
  static async chat(id: string, message: string): Promise<string> {
    return (await api.post<{ reply: string }>(`/aiTasks/${encodeURIComponent(id)}/messages`, { message })).data.reply
  }
}
