export type LLMProvider = "openai" | "openai-responses" | "anthropic"

export interface LLMProfile {
  id: string
  name: string
  displayName: string
  provider: LLMProvider
  model: string
  baseUrl: string
  isDefault: boolean
  hasApiKey: boolean
  createdAt: string
  updatedAt: string
}

export interface LLMProfileInput {
  name: string
  provider: LLMProvider
  model: string
  baseUrl: string
  apiKey: string
  isDefault: boolean
}
