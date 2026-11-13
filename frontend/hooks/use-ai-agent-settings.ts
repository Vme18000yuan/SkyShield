"use client"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AIAgentSettingService } from "@/services/ai-agent-setting.service"

export function useAIAgentSettings() {
  const user = useAuth().data?.user?.id
  const client = useQueryClient()
  const key = ["ai-agent-settings", "default-tenant", user] as const
  const list = useQuery({ queryKey: key, queryFn: AIAgentSettingService.list, enabled: Boolean(user) })
  const save = useMutation({ mutationFn: AIAgentSettingService.save, onSuccess: () => client.invalidateQueries({ queryKey: key }) })
  return { list, save }
}
