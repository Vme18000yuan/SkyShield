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
  if (!page.running && page.terminalReason === "model_error") return "模型调用中断，已完成的工具结果已保留。连接恢复后可继续未完成步骤。"
  if (!page.running && page.terminalReason === "prompt_too_long") return "上下文超过模型可用窗口，已有结果已保留。调整模型设置后可继续。"
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

export function conversationCanResume(page: ConversationFeedPage): boolean {
  return !page.running && !hasUnreadConversationEvents(page)
    && ["failed", "interrupted"].includes(page.runStatus ?? "")
    && ["model_error", "prompt_too_long"].includes(page.terminalReason ?? "")
}

export const RESUME_SAVED_WORK_PROMPT = "请从本会话最后一个完整的工具结果和已保存的阶段进度继续。先核对已完成项与未完成项，复用已有采集 run_id 和结果，只补充未完成步骤，不要重新执行已完成的扫描或有副作用的操作；最后汇总结果及仍存在的限制。"
