import { api } from "@/lib/api-client"
import type { AIToolExecution, AIToolExecutionPage, AIToolExecutionSource } from "@/types/ai-tool-execution.types"

export class AIToolExecutionService {
  static async list(source: AIToolExecutionSource, q: string, before: string): Promise<AIToolExecutionPage> {
    const response = await api.get<AIToolExecutionPage>(source === "task" ? "/aiTaskToolExecutions" : "/aiToolExecutions", { params: { q: q || undefined, before: before || undefined } })
    return response.data
  }
  static async get(source: AIToolExecutionSource, id: string): Promise<AIToolExecution> {
    const base = source === "task" ? "/aiTaskToolExecutions" : "/aiToolExecutions"
    const response = await api.get<AIToolExecution>(`${base}/${encodeURIComponent(id)}`)
    return response.data
  }
}
