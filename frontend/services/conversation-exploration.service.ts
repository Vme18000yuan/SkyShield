import { api } from "@/lib/api-client"
import type { ConversationExploration, ConversationTimeline } from "@/types/conversation-exploration.types"

export async function getConversationExploration(id: string, run: string, beforeRun: string, before: string, signal?: AbortSignal) {
  return (await api.get<ConversationExploration>(`/aiConversations/${encodeURIComponent(id)}/exploration`, { params: { run: run || undefined, beforeRun: beforeRun || undefined, before: before || undefined }, signal })).data
}
export async function getConversationExplorationDetail(id: string, activity: string, signal?: AbortSignal) {
  return (await api.get<{ detail: string }>(`/aiConversations/${encodeURIComponent(id)}/messages/${encodeURIComponent(activity)}`, { signal })).data.detail
}

export async function getConversationTimeline(id: string, run: string, beforeRun: string, from: number, afterFinding: string, signal?: AbortSignal) {
  return (await api.get<ConversationTimeline>(`/aiConversations/${encodeURIComponent(id)}/exploration`, { params: { view: "timeline", run: run || undefined, beforeRun: beforeRun || undefined, from, afterFinding: afterFinding || undefined }, signal })).data
}
