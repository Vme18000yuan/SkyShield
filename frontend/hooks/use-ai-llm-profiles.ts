"use client"

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AILLMProfileService } from "@/services/ai-llm-profile.service"
import type { LLMProfileInput } from "@/types/ai-llm-profile.types"

export function useAILLMProfiles() {
  const userId = useAuth().data?.user?.id
  const queryClient = useQueryClient()
  const queryKey = ["ai-llm-profiles", "default-tenant", userId] as const
  const list = useQuery({ queryKey, queryFn: AILLMProfileService.list, enabled: Boolean(userId) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey })
  const save = useMutation({ mutationFn: ({ id, input }: { id: string | null; input: LLMProfileInput }) => AILLMProfileService.save(id, input), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: AILLMProfileService.remove, onSuccess: invalidate })
  const test = useMutation({ mutationFn: AILLMProfileService.test })
  return { list, save, remove, test }
}
