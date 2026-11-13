export interface AIAgentCatalogItem {
  key: string
  name: string
  description: string
  role: string
  builtin: boolean
  enabled: boolean
  maxTurns: number
  runSeconds: number
  llmProfileId: string | null
  systemPrompt?: string
  defaultSystemPrompt?: string
  wrapupPrompt: string
  wrapupMaxTurns: number
  taskTimeoutPrompt: string
  taskTimeoutMaxTurns: number
  defaultWrapupPrompt?: string
  defaultWrapupMaxTurns?: number
  defaultTaskTimeoutPrompt?: string
}

export interface AIAgentPromptVersion {
  version: number
  template: string
  createdAt: string
}

export interface CreateAIAgentInput {
  key: string
  name: string
  description: string
  systemPrompt: string
}

export interface UpdateAIAgentInput {
  name: string
  description: string
  systemPrompt: string
  enabled: boolean
  maxTurns: number
  runSeconds: number
  llmProfileId?: string | null
  wrapupPrompt?: string
  wrapupMaxTurns?: number
  taskTimeoutPrompt?: string
  taskTimeoutMaxTurns?: number
}
