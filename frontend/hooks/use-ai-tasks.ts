"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AITaskService } from "@/services/ai-task.service"
import type { AITaskAction, AITaskActivity } from "@/types/ai-task.types"

export function useAITasks(before: string, enabled = true, filter?: { q?: string; status?: string; category?: string }) {
  const user = useAuth().data?.user?.id
  return useQuery({ queryKey: ["ai-tasks", "default-tenant", user, before, filter?.q ?? "", filter?.status ?? "", filter?.category ?? ""], queryFn: () => AITaskService.list(before, filter), enabled: Boolean(user && enabled), refetchInterval: enabled ? 5000 : false })
}

export function useAITaskMutations() {
  const user = useAuth().data?.user?.id
  const client = useQueryClient()
  const invalidate = () => client.invalidateQueries({ queryKey: ["ai-tasks", "default-tenant", user] })
  const create = useMutation({ mutationFn: AITaskService.create, onSuccess: async () => {
    await Promise.all([invalidate(), client.invalidateQueries({ queryKey: ["ai-task-categories", "default-tenant", user] })])
  } })
  const chat = useMutation({ mutationFn: ({ id, message }: { id: string; message: string }) => AITaskService.chat(id, message) })
  const metadata = useMutation({ mutationFn: ({ id, patch }: { id: string; patch: { displayName?: string; remark?: string; pinned?: boolean; categoryId?: string | null } }) => AITaskService.updateMetadata(id, patch), onSuccess: async (_, { id }) => {
    await Promise.all([invalidate(), client.invalidateQueries({ queryKey: ["ai-task", "default-tenant", user, id] }), client.invalidateQueries({ queryKey: ["ai-task-categories", "default-tenant", user] })])
  } })
  const control = useMutation({ mutationFn: ({ id, action }: { id: string; action: AITaskAction }) => AITaskService.control(id, action), onSuccess: async (_, { id }) => {
    await Promise.all([invalidate(), client.invalidateQueries({ queryKey: ["ai-task", "default-tenant", user, id] })])
  } })
  return { create, control, chat, metadata }
}

export function useAITask(id: string) {
  const user = useAuth().data?.user?.id
  const task = useQuery({ queryKey: ["ai-task", "default-tenant", user, id], queryFn: () => AITaskService.get(id), enabled: Boolean(user && id), refetchInterval: 3000 })
  const graph = useQuery({ queryKey: ["ai-task-graph", "default-tenant", user, id], queryFn: () => AITaskService.graph(id), enabled: Boolean(user && id), refetchInterval: 5000 })
  return { task, graph }
}

export function useAITaskAssets(id: string, after: string, enabled = true) {
  const user = useAuth().data?.user?.id
  return useQuery({
    queryKey: ["ai-task-assets", "default-tenant", user, id, after],
    queryFn: () => AITaskService.assets(id, after),
    enabled: Boolean(user && id && enabled),
    refetchInterval: enabled ? 5000 : false,
  })
}

export function useAITaskActivities(id: string) {
  const user = useAuth().data?.user?.id
  const [items, setItems] = React.useState<AITaskActivity[]>([])
  const [error, setError] = React.useState<unknown>(null)
  const [truncated, setTruncated] = React.useState(false)
  React.useEffect(() => {
    let active = true
    let busy = false
    let cursor = "0"
    let received = 0
    setItems([]); setError(null); setTruncated(false)
    if (!id || !user) return () => { active = false }
    const refresh = async () => {
      if (!active || busy) return
      busy = true
      try {
        const next = await AITaskService.activities(id, cursor)
        if (!active) return
        if (next.length) {
          cursor = next[next.length - 1].id
          received += next.length
          if (received > 1000) setTruncated(true)
          setItems(previous => [...previous, ...next].slice(-1000))
        }
        setError(null)
      } catch (err) { if (active) { cursor = "0"; received = 0; setItems([]); setTruncated(false); setError(err) } }
      finally { busy = false }
    }
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 1500)
    return () => { active = false; window.clearInterval(timer) }
  }, [id, user])
  return { items, error, truncated }
}
