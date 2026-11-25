"use client"

import { useResourceMutation } from "@/hooks/_shared/create-resource-mutation"
import { deleteAIData } from "@/services/ai-data-deletion.service"
import type { AIDataDeletionRequest } from "@/types/ai-data-deletion.types"
const affectedKeys = ["ai-tasks", "ai-task", "ai-tool-executions", "ai-tool-execution", "traffic", "traffic-hosts", "traffic-exchange", "task-traffic-blob"]

export function useAIDataDeletion() {
  return useResourceMutation({
    mutationFn: deleteAIData,
    loadingToast: { key: "common.status.deleting", id: (request: AIDataDeletionRequest) => `ai-data-delete-${request.kind}-${request.kind === "executions" ? request.source + request.ids.join("-") : request.id}` },
    invalidate: affectedKeys.map(key => ({ queryKey: [key] })),
    onSuccess: ({ data, toast }) => { if (data.cleanupPending) toast.warning("aiDataDeletion.pending"); else toast.success("aiDataDeletion.success") },
    errorFallbackKey: "aiDataDeletion.error",
    onSettled: async ({ error, queryClient }) => { if (error) await Promise.all(affectedKeys.map(key => queryClient.invalidateQueries({ queryKey: [key] }))) },
  })
}
