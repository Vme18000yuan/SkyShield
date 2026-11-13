"use client"

import { useQuery } from "@tanstack/react-query"

import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { AIModelOption } from "@/types/ai-chat.types"
import type { AIUsageSummary } from "@/types/ai-usage.types"

export const aiUsageKeys = {
  all: ["ai-model-usage"] as const,
  summary: (user: number | undefined, days: number) => ["ai-model-usage", user, days] as const,
}

export const aiModelOptionKeys = {
  all: ["ai-model-options", "default-tenant"] as const,
  list: (user: number | undefined) => ["ai-model-options", "default-tenant", user] as const,
}

export function useAIUsageSummary(days: number) {
  const user = useAuth().data?.user?.id
  return useQuery({
    queryKey: aiUsageKeys.summary(user, days),
    enabled: Boolean(user),
    queryFn: async ({ signal }) => (await api.get<AIUsageSummary>("/aiModelUsage", { params: { days }, signal })).data,
    refetchInterval: 30000,
  })
}

export function useAIUsageModelOptions() {
  const user = useAuth().data?.user?.id
  return useQuery({
    queryKey: aiModelOptionKeys.list(user),
    enabled: Boolean(user),
    queryFn: async ({ signal }) => (await api.get<{ models: AIModelOption[] }>("/aiModels", { signal })).data.models,
  })
}
