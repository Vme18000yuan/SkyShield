export interface AIToolExecution {
  id: string
  conversationId?: string
  taskId?: string
  conversation: string
  runId: string
  tool: string
  input: string
  output: string
  status: "running" | "succeeded" | "failed" | "unknown"
  createdAt: string
}

export type AIToolExecutionSource = "conversation" | "task"

export interface AIToolExecutionPage {
  executions: AIToolExecution[]
  nextBefore: string
}
