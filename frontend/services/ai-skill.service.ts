import { api } from "@/lib/api-client"
import type { AISkill, AISkillInput } from "@/types/ai-skill.types"
export class AISkillService {
  static async list(): Promise<AISkill[]> { return (await api.get<{ skills: AISkill[] }>("/aiSkills")).data.skills }
  static async save(id: string | null, input: AISkillInput): Promise<AISkill> {
    return id ? (await api.put<AISkill>(`/aiSkills/${encodeURIComponent(id)}`, input)).data : (await api.post<AISkill>("/aiSkills", input)).data
  }
  static async delete(id: string): Promise<void> { await api.delete(`/aiSkills/${encodeURIComponent(id)}`) }
}
