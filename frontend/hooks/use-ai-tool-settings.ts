"use client"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AIToolSettingService } from "@/services/ai-tool-setting.service"
export function useAIToolSettings() {
  const user = useAuth().data?.user?.id
  const client = useQueryClient()
  const key = ["ai-tool-settings", "default-tenant", user] as const
  const list = useQuery({ queryKey: key, queryFn: AIToolSettingService.list, enabled: Boolean(user) })
  const save = useMutation({ mutationFn: AIToolSettingService.save, onSuccess: () => client.invalidateQueries({ queryKey: key }) })
  return { list, save }
}
