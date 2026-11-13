import type { Activity } from "@/types/artex-ai.types"

export type ConversationFeedPage = {
  items: Activity[]
  cursor: number
  running: boolean
  hasMore?: boolean
  finalCursor?: number
  runStatus?: string
  terminalReason?: string
  errorMessage?: string
  lastProgressAt?: string
  liveText?: string
  liveKind?: string
}

// A terminal run may still have several unread pages after a disconnection.
export function hasUnreadConversationEvents(page: ConversationFeedPage): boolean {
  return Boolean(page.hasMore || (page.finalCursor ?? page.cursor) > page.cursor)
}

export function conversationStatusMessage(page: ConversationFeedPage): string {
  if (page.errorMessage) return page.errorMessage
  switch (page.runStatus) {
    case "stopping": return "正在停止执行，等待工具退出…"
    case "timeout": return "本轮时间预算已用尽，已有结果已保留。"
    case "exhausted": return "本轮步数预算已用尽，已有结果已保留。"
    case "interrupted": return "执行已中断，结果可能不完整。"
    case "failed": return "本轮执行失败，请检查执行记录。"
    case "stopped": return "本轮已停止。"
    default: return ""
  }
}
