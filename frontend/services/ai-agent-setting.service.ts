import { api } from "@/lib/api-client"
import type { AIAgentSetting } from "@/types/ai-agent-setting.types"

export class AIAgentSettingService {
  static async list(): Promise<AIAgentSetting[]> { return (await api.get<{ agents: AIAgentSetting[] }>("/aiAgentSettings")).data.agents }
  static async save(setting: AIAgentSetting): Promise<AIAgentSetting> {
    return (await api.put<AIAgentSetting>(`/aiAgentSettings/${encodeURIComponent(setting.key)}`, {
      enabled: setting.enabled, maxTurns: setting.maxTurns, runSeconds: setting.runSeconds, additionalPrompt: setting.additionalPrompt,
    })).data
  }
}
