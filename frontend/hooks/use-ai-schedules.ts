"use client"

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AIScheduleService } from "@/services/ai-schedule.service"
import type { AIScheduleAction, AIScheduleInput } from "@/types/ai-schedule.types"

export function useAISchedules() {
  const user = useAuth().data?.user?.id
  const client = useQueryClient()
  const key = ["ai-schedules", "default-tenant", user] as const
  const list = useQuery({ queryKey: key, queryFn: AIScheduleService.list, enabled: Boolean(user), refetchInterval: 5000 })
  const invalidate = async () => { await Promise.all([client.invalidateQueries({ queryKey: key }), client.invalidateQueries({ queryKey: ["ai-tasks", "default-tenant", user] })]) }
  const save = useMutation({ mutationFn: ({ id, input }: { id: string | null; input: AIScheduleInput }) => AIScheduleService.save(id, input), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: AIScheduleService.delete, onSuccess: invalidate })
  const action = useMutation({ mutationFn: ({ id, action }: { id: string; action: AIScheduleAction }) => AIScheduleService.action(id, action), onSuccess: invalidate })
  return { list, save, remove, action }
}
export function useAISchedule(id: string | null) {
  const user = useAuth().data?.user?.id
  return useQuery({ queryKey: ["ai-schedule", "default-tenant", user, id], queryFn: () => AIScheduleService.get(id!), enabled: Boolean(user && id), refetchInterval: 5000 })
}
