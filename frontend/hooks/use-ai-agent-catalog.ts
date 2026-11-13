import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { AIAgentCatalogService } from "@/services/ai-agent-catalog.service"
import type { CreateAIAgentInput, UpdateAIAgentInput } from "@/types/ai-agent-catalog.types"

export const aiAgentCatalogKey = ["ai-agent-catalog"] as const

export function useAIAgentCatalog() {
  const queryClient = useQueryClient()
  const list = useQuery({ queryKey: aiAgentCatalogKey, queryFn: AIAgentCatalogService.list })
  const create = useMutation({
    mutationFn: (input: CreateAIAgentInput) => AIAgentCatalogService.create(input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: aiAgentCatalogKey }),
  })
  const update = useMutation({
    mutationFn: ({ key, input }: { key: string; input: UpdateAIAgentInput }) => AIAgentCatalogService.update(key, input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: aiAgentCatalogKey }),
  })
  const remove = useMutation({
    mutationFn: (key: string) => AIAgentCatalogService.delete(key),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: aiAgentCatalogKey }),
  })
  const loadDetail = (key: string) => queryClient.fetchQuery({ queryKey: [...aiAgentCatalogKey, key, "detail"], queryFn: () => AIAgentCatalogService.get(key), staleTime: 0 })
  const loadPromptVersions = (key: string) => queryClient.fetchQuery({ queryKey: [...aiAgentCatalogKey, key, "versions"], queryFn: () => AIAgentCatalogService.promptVersions(key), staleTime: 0 })
  const previewPrompt = useMutation({ mutationFn: ({ key, template }: { key: string; template: string }) => AIAgentCatalogService.previewPrompt(key, template) })
  return { list, create, update, remove, loadDetail, loadPromptVersions, previewPrompt }
}
