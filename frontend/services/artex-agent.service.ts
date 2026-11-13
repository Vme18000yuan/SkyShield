import { api as client } from "@/lib/api-client"
import type { Agent, AgentDetail, AgentTrigger, MCPServer, Settings, SkillItem, Tool } from "@/types/artex-ai.types"

interface AgentDTO { key: string; name: string; description: string; role: string; builtin: boolean; enabled: boolean; maxTurns: number; runSeconds: number; llmProfileId?: string | null; options?: Partial<Agent>; mcpCount?: number; skillCount?: number; toolCount?: number }
const agent = (item: AgentDTO): Agent => ({ ...item.options, id: item.key, key: item.key, name: item.name, description: item.description, role: item.role, builtin: item.builtin, enabled: item.enabled, max_turns: item.maxTurns, run_seconds: item.runSeconds, llm_profile_id: item.llmProfileId, mcp_count: item.mcpCount ?? 0, skill_count: item.skillCount ?? 0, tool_count: item.toolCount ?? 0 })
const route = (key: string) => `/aiAgents/${encodeURIComponent(key)}`

export const agentAPI = {
  agents: async () => (await client.get<{agents: AgentDTO[]}>("/aiAgents")).data.agents.map(agent),
  getAgent: async (key: string) => (await client.get<AgentDetail>(`${route(key)}/details`)).data,
  createAgent: async (key: string, name: string, description = "") => agent((await client.post<AgentDTO>("/aiAgents", { key, name, description })).data),
  deleteAgent: async (key: string) => (await client.delete(`${route(key)}`)).data,
  saveAgentConfig: async (key: string, patch: { enabled?: boolean; llm_profile_id?: string | null; max_turns?: number; run_seconds?: number; web_search?: boolean; interactive_shell?: boolean; trigger_run_mode?: "serial" | "parallel"; trigger_merge_mode?: "by_task" | "all" | "none"; trigger_max_parallel?: number }) => (await client.put(`${route(key)}/config`, patch)).data,
  saveAgentPrompt: async (key: string, template: string) => (await client.put<{version: number}>(`${route(key)}/prompt`, { template })).data,
  resetAgentPrompt: async (key: string) => (await client.post<{version: number}>(`${route(key)}/prompt/reset`, {})).data,
  previewAgentPrompt: async (key: string, template: string, sample?: Record<string, string>) => (await client.post<{rendered:string;error?:string}>(`${route(key)}/prompt/preview`, { template, sample })).data,
  saveAgentWrapup: async (key: string, prompt: string, maxTurns = 0) => (await client.put(`${route(key)}/wrapup`, { prompt, max_turns: maxTurns })).data,
  resetAgentWrapup: async (key: string) => (await client.post(`${route(key)}/wrapup/reset`, {})).data,
  saveAgentTaskTimeoutWrapup: async (key: string, prompt: string, maxTurns = 0) => (await client.put(`${route(key)}/wrapup/task-timeout`, { prompt, max_turns: maxTurns })).data,
  resetAgentTaskTimeoutWrapup: async (key: string) => (await client.post(`${route(key)}/wrapup/task-timeout/reset`, {})).data,
  setAgentVisibility: async (key: string, mcp: string[], skill: string[]) => (await client.put(`${route(key)}/visibility`, { mcp, skill })).data,
  agentTriggers: async (key: string) => (await client.get<{triggers:AgentTrigger[]}>(`${route(key)}/triggers`)).data.triggers,
  createTrigger: async (key: string, input: Omit<AgentTrigger,"id"|"agent_key"|"last_fire">) => (await client.post<AgentTrigger>(`${route(key)}/triggers`, input)).data,
  updateTrigger: async (id: number, input: Omit<AgentTrigger,"id"|"agent_key"|"last_fire">) => (await client.patch(`/aiAgentTriggers/${id}`, input)).data,
  deleteTrigger: async (id: number) => (await client.delete(`/aiAgentTriggers/${id}`)).data,
  mcpServers: async (): Promise<MCPServer[]> => (await client.get<{servers:Array<{id:string;displayName:string;endpoint:string;enabled:boolean;insecure:boolean;transport?:MCPServer["transport"];command?:string;args?:string[];env?:Record<string,string>;tools?:string[]}>}>("/aiMcpServers")).data.servers.map(item => ({ id: item.id, name: item.displayName, transport: item.transport ?? "http", url: item.endpoint, command: item.command, args: item.args ?? [], env: item.env ?? {}, enabled: item.enabled, insecure: item.insecure, tools: item.tools })),
  skills: async ():Promise<SkillItem[]> => (await client.get<{skills:SkillItem[]}>("/aiSkillPackages")).data.skills,

  resetTool: async (key:string) => (await client.post(`/aiTools/${encodeURIComponent(key)}/reset`, {})).data,
  createCustomTool: async (input:Pick<Tool,"key"|"description"|"schema"|"agents"|"enabled"|"kind"|"exec"|"deferred">)=>(await client.post("/aiTools",input)).data,
  updateCustomTool: async (key:string,input:Pick<Tool,"description"|"schema"|"agents"|"enabled"|"kind"|"exec"|"deferred">)=>(await client.put(`/aiTools/${encodeURIComponent(key)}`,input)).data,
  deleteCustomTool: async (key:string)=>(await client.delete(`/aiTools/${encodeURIComponent(key)}`)).data,
  testCustomTool: async (body:{kind:string;exec:Record<string,unknown>;params:Record<string,unknown>})=>(await client.post<{output:string;is_error:boolean}>("/aiToolTests",body,{timeout:610000})).data,
  settings: async () => (await client.get<Settings>("/aiRuntimeSettings")).data,
  tools: async () => (await client.get<{tools:Tool[]}>("/aiTools")).data.tools,
  saveTool: async (key: string, input: Pick<Tool,"description"|"schema"|"agents"|"enabled">) => (await client.put(`/aiTools/${encodeURIComponent(key)}`, input)).data,
}
