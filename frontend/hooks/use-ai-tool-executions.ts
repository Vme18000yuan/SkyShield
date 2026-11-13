"use client"

import { useQuery } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AIToolExecutionService } from "@/services/ai-tool-execution.service"
import type { AIToolExecutionSource } from "@/types/ai-tool-execution.types"

export function useAIToolExecutions(source: AIToolExecutionSource, query: string, before: string) {
  const userId = useAuth().data?.user?.id
  return useQuery({ queryKey: ["ai-tool-executions", "default-tenant", userId, source, query, before],
    queryFn: () => AIToolExecutionService.list(source, query, before), enabled: Boolean(userId) })
}

export function useAIToolExecution(source: AIToolExecutionSource, id: string | null) {
  const userId = useAuth().data?.user?.id
  return useQuery({ queryKey: ["ai-tool-execution", "default-tenant", userId, source, id],
    queryFn: () => AIToolExecutionService.get(source, id!), enabled: Boolean(userId && id) })
}
