"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AIChatService } from "@/services/ai-chat.service"
import type { AIActivity } from "@/types/ai-chat.types"

export function useAIConversations() {
  const auth = useAuth()
  const userId = auth.data?.user?.id
  const queryClient = useQueryClient()
  const queryKey = ["ai-conversations", "default-tenant", userId] as const
  const list = useQuery({ queryKey, queryFn: AIChatService.list, enabled: Boolean(userId), refetchInterval: 5000 })
  const invalidate = () => queryClient.invalidateQueries({ queryKey })
  const create = useMutation({ mutationFn: ({ title, agentKey }: { title: string; agentKey: string }) => AIChatService.create(title, agentKey), onSuccess: invalidate })
  const send = useMutation({ mutationFn: ({ id, message }: { id: string; message: string }) => AIChatService.send(id, message), onSuccess: invalidate })
  const stop = useMutation({ mutationFn: AIChatService.stop, onSuccess: invalidate })
  const models = useQuery({ queryKey: ["ai-model-options", "default-tenant", userId], queryFn: AIChatService.models, enabled: Boolean(userId) })
  const setModel = useMutation({ mutationFn: ({ id, profileId }: { id: string; profileId: string | null }) => AIChatService.setModel(id, profileId), onSuccess: invalidate })
  const updateMetadata = useMutation({ mutationFn: ({ id, input }: { id: string; input: { title?: string; pinned?: boolean } }) => AIChatService.updateMetadata(id, input), onSuccess: invalidate })
  return { list, create, send, stop, models, setModel, updateMetadata }
}

export function useAIActivities(conversationId: string | null) {
  const [items, setItems] = React.useState<AIActivity[]>([])
  const [running, setRunning] = React.useState(false)
  const [runStatus, setRunStatus] = React.useState("")
  const [error, setError] = React.useState<unknown>(null)
  const refreshRef = React.useRef<() => void>(() => {})

  React.useEffect(() => {
    let active = true
    let inFlight = false
    let cursor = "0"
    setItems([])
    setRunning(false)
    setRunStatus("")
    setError(null)
    if (!conversationId) return () => { active = false }

    const refresh = async () => {
      if (!active || inFlight) return
      inFlight = true
      try {
        const page = await AIChatService.activities(conversationId, cursor)
        if (!active) return
        if (page.activities.length) {
          cursor = page.activities[page.activities.length - 1].id
          setItems(previous => [...previous, ...page.activities])
        }
        setRunning(page.running)
        setRunStatus(page.runStatus)
        setError(null)
      } catch (err) {
        if (active) setError(err)
      } finally {
        inFlight = false
      }
    }
    refreshRef.current = () => { void refresh() }
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 1500)
    return () => { active = false; window.clearInterval(timer); refreshRef.current = () => {} }
  }, [conversationId])

  return { items, running, runStatus, error, refresh: () => refreshRef.current() }
}
