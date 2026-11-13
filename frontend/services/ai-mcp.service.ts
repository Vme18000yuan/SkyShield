import { api } from "@/lib/api-client"
import type { AIMCPInput, AIMCPServer, AIMCPTool } from "@/types/ai-mcp.types"
export class AIMCPService {
  static async list(): Promise<AIMCPServer[]> { return (await api.get<{ servers: AIMCPServer[] }>("/aiMcpServers")).data.servers }
  static async save(id: string | null, input: AIMCPInput): Promise<AIMCPServer> {
    return id ? (await api.put<AIMCPServer>(`/aiMcpServers/${encodeURIComponent(id)}`, input)).data : (await api.post<AIMCPServer>("/aiMcpServers", input)).data
  }
  static async delete(id: string): Promise<void> { await api.delete(`/aiMcpServers/${encodeURIComponent(id)}`) }
  static async test(id: string): Promise<AIMCPTool[]> { return (await api.post<{ tools: AIMCPTool[] }>(`/aiMcpServers/${encodeURIComponent(id)}/test`)).data.tools }
}
