export interface AIConversation {
  id: string
  title: string
  agentKey?: string
  llmProfileId?: string | null
  pinned: boolean
  createdAt: string
  updatedAt: string
  running: boolean
}

export interface AIModelOption {
  id: string
  displayName: string
  model: string
  isDefault: boolean
}

export interface AIActivity {
  id: string
  runId: string
  kind: string
  tool: string
  toolUseId: string
  isError: boolean
  summary: string
  detail: string
  createdAt: string
}

export interface AIActivityPage {
  activities: AIActivity[]
  running: boolean
  runStatus: string
  errorMessage: string
}
