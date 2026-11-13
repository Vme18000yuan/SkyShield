import { api } from "@/lib/api-client"
import type { AIToolSetting } from "@/types/ai-tool-setting.types"
export class AIToolSettingService {
  static async list(): Promise<AIToolSetting[]> { return (await api.get<{ tools: AIToolSetting[] }>("/aiToolSettings")).data.tools }
  static async save(item: AIToolSetting): Promise<AIToolSetting> {
    return (await api.put<AIToolSetting>(`/aiToolSettings/${encodeURIComponent(item.agentKey)}/${encodeURIComponent(item.toolKey)}`, { enabled: item.enabled })).data
  }
}
