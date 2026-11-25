import { api } from "@/lib/api-client"
import type { AIDataDeletionRequest, AIDataDeletionResult } from "@/types/ai-data-deletion.types"

export async function deleteAIData(request: AIDataDeletionRequest): Promise<AIDataDeletionResult> {
  const numeric = (id: string) => /^[1-9][0-9]*$/.test(id)
  const uuid = (id: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id)
  let path: string
  let data: object | undefined
  if (request.kind === "task") {
    if (!numeric(request.id)) throw new Error("Invalid AI task")
    path = `/aiTasks/${request.id}`
  } else if (request.kind === "executions") {
    if (!request.ids.length || request.ids.length > 100 || new Set(request.ids).size !== request.ids.length || !request.ids.every(numeric)) throw new Error("Invalid tool executions")
    path = request.source === "task" ? "/aiTaskToolExecutions" : "/aiToolExecutions"
    data = { ids: request.ids }
  } else {
    if (!(request.source === "task" ? numeric(request.id) : uuid(request.id))) throw new Error("Invalid traffic source")
    if (request.all ? Boolean(request.ids?.length) : !request.ids?.length || request.ids.length > 100) throw new Error("Invalid traffic selection")
    path = `/${request.source === "task" ? "aiTasks" : "aiConversations"}/${encodeURIComponent(request.id)}/traffic`
    data = request.all ? { all: true } : { ids: request.ids }
  }
  const result = (await api.delete<AIDataDeletionResult>(path, data ? { data } : undefined)).data
  if (!Number.isSafeInteger(result.deleted) || result.deleted < 0 || typeof result.cleanupPending !== "boolean") throw new Error("Invalid deletion response")
  return result
}
