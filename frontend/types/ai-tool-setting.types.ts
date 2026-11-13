import type { AIAgentKey } from "@/types/ai-agent-setting.types"
export type AIPlatformToolKey = "search_platform_assets" | "insert_assets"
export interface AIToolSetting { name: string; agentKey: AIAgentKey; toolKey: AIPlatformToolKey; enabled: boolean }
