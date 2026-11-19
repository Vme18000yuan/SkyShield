import { useQuery } from "@tanstack/react-query"
import { getConversationTimeline, getConversationExplorationDetail } from "@/services/conversation-exploration.service"

export function useConversationTimeline(user: number, conversation: string, run: string, beforeRun: string, from: number, afterFinding: string, running: boolean) {
  return useQuery({
    queryKey: ["conversation-timeline", user, conversation, run, beforeRun, from, afterFinding],
    queryFn: ({ signal }) => getConversationTimeline(conversation, run, beforeRun, from, afterFinding, signal),
    retry: false,
    placeholderData: previous => previous && run && previous.run?.id === run ? previous : undefined,
    refetchInterval: state => state.state.data?.run?.running || (!run && !beforeRun && running) ? 2500 : false,
  })
}

export function useConversationTimelineSource(user: number, conversation: string, id: string, open: boolean) {
  return useQuery({ queryKey: ["conversation-source", user, conversation, id], enabled: open, gcTime: 0, retry: false,
    queryFn: ({ signal }) => getConversationExplorationDetail(conversation, id, signal) })
}
