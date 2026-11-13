export type AIAgentKey = "assistant" | "planner" | "worker"
export interface AIAgentSetting {
  name: string
  key: AIAgentKey
  enabled: boolean
  maxTurns: number
  runSeconds: number
  additionalPrompt: string
}
