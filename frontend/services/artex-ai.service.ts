import { api as client, getErrorMessage } from "@/lib/api-client"
import type { LLMProfile, LLMPoolStatus, LLMRetryPolicy, InterceptRule, JudgeConfig, JudgeUsage, AssetInterceptRule, AssetInterceptRuleInput, TaskCategory, TaskTemplate } from "@/types/artex-ai.types"
import { agentAPI } from "./artex-agent.service"
import { chatAPI } from "./artex-chat.service"
import { skillAPI } from "./artex-skill.service"
import { mcpAPI } from "./artex-mcp.service"
import { scheduleAPI } from "./artex-schedule.service"
export type { ScheduleInput } from "./artex-schedule.service"
export { http, openSSE } from "./artex-chat.service"

type ProfileWrite = Partial<LLMProfile> & { id?: string; name: string; format: string; model: string; api_key?: string }
interface ProfileDTO { id: string; displayName: string; provider: LLMProfile["format"]; model: string; baseUrl: string; isDefault: boolean; hasApiKey: boolean; apiKeyHint?: string; options?: Partial<LLMProfile> }
const profile = (item: ProfileDTO): LLMProfile => ({ rate_per_second: 0, rate_per_minute: 0, ...item.options, id: item.id, name: item.displayName, format: item.provider, model: item.model, base_url: item.baseUrl, is_default: item.isDefault, api_key_hint: item.apiKeyHint || (item.hasApiKey ? "••••" : "") })

const actions = {
  ...agentAPI,
  ...chatAPI,
  ...skillAPI,
  ...mcpAPI,
  ...scheduleAPI,
  taskCategories: async () => (await client.get<{categories:TaskCategory[]}>("/aiTaskCategories")).data.categories ?? [],
  createTaskCategory: async (name:string) => (await client.post<TaskCategory>("/aiTaskCategories",{name})).data,
  updateTaskCategoryName: async (id:string,name:string) => (await client.put<TaskCategory>(`/aiTaskCategories/${encodeURIComponent(id)}`,{name})).data,
  deleteTaskCategory: async (id:string) => (await client.delete(`/aiTaskCategories/${encodeURIComponent(id)}`)).data,
  taskTemplates: async () => (await client.get<{templates:TaskTemplate[]}>("/aiTaskTemplates")).data.templates ?? [],
  createTaskTemplate: async (input:Omit<TaskTemplate,"id"|"created_at"|"updated_at">) => (await client.post<TaskTemplate>("/aiTaskTemplates",input)).data,
  updateTaskTemplate: async (id:string,input:Omit<TaskTemplate,"id"|"created_at"|"updated_at">) => (await client.put<TaskTemplate>(`/aiTaskTemplates/${encodeURIComponent(id)}`,input)).data,
  deleteTaskTemplate: async (id:string) => (await client.delete(`/aiTaskTemplates/${encodeURIComponent(id)}`)).data,
  assetInterceptRules: async () => (await client.get<{rules:AssetInterceptRule[]}>("/aiAssetInterceptRules")).data.rules,
  createAssetInterceptRule: async (rule:AssetInterceptRuleInput) => (await client.post<AssetInterceptRule>("/aiAssetInterceptRules",rule)).data,
  updateAssetInterceptRule: async (id:string,rule:AssetInterceptRuleInput) => (await client.put<AssetInterceptRule>(`/aiAssetInterceptRules/${encodeURIComponent(id)}`,rule)).data,
  deleteAssetInterceptRule: async (id:string) => (await client.delete(`/aiAssetInterceptRules/${encodeURIComponent(id)}`)).data,
  toggleAssetInterceptRule: async (id:string,enabled:boolean) => (await client.post(`/aiAssetInterceptRules/${encodeURIComponent(id)}/toggle`,{enabled})).data,
  taskInterceptRules: async (id:string) => (await client.get<{rules:AssetInterceptRule[]}>(`/aiTasks/${encodeURIComponent(id)}/assetInterceptRules`)).data.rules,
  createTaskInterceptRule: async (id:string,rule:AssetInterceptRuleInput) => (await client.post<AssetInterceptRule>(`/aiTasks/${encodeURIComponent(id)}/assetInterceptRules`,rule)).data,
  updateTaskInterceptRule: async (id:string,ruleId:string,rule:AssetInterceptRuleInput) => (await client.put<AssetInterceptRule>(`/aiTasks/${encodeURIComponent(id)}/assetInterceptRules/${encodeURIComponent(ruleId)}`,rule)).data,
  deleteTaskInterceptRule: async (id:string,ruleId:string) => (await client.delete(`/aiTasks/${encodeURIComponent(id)}/assetInterceptRules/${encodeURIComponent(ruleId)}`)).data,
  toggleTaskInterceptRule: async (id:string,ruleId:string,enabled:boolean) => (await client.post(`/aiTasks/${encodeURIComponent(id)}/assetInterceptRules/${encodeURIComponent(ruleId)}/toggle`,{enabled})).data,
  interceptRules: async () => (await client.get<{rules:InterceptRule[]}>("/aiInterceptRules")).data.rules,
  createInterceptRule: async (rule:Omit<InterceptRule,"id"|"created_at"|"updated_at">) => (await client.post<InterceptRule>("/aiInterceptRules",rule)).data,
  updateInterceptRule: async (id:number,rule:Omit<InterceptRule,"id"|"created_at"|"updated_at">) => (await client.put<InterceptRule>(`/aiInterceptRules/${id}`,rule)).data,
  deleteInterceptRule: async (id:number) => (await client.delete(`/aiInterceptRules/${id}`)).data,
  toggleInterceptRule: async (id:number,enabled:boolean) => (await client.post(`/aiInterceptRules/${id}/toggle`,{enabled})).data,
  interceptGetToolConfig: async () => (await client.get<{enabled_tools:string[]}>("/aiInterceptTools")).data,
  interceptSetToolConfig: async (enabled_tools:string[]) => (await client.put("/aiInterceptTools",{enabled_tools})).data,
  interceptGetJudgeConfig: async () => (await client.get<JudgeConfig>("/aiInterceptJudge")).data,
  interceptSetJudgeConfig: async (config:JudgeConfig) => (await client.put("/aiInterceptJudge",config)).data,
  interceptJudgeUsage: async (days=30) => (await client.get<JudgeUsage>("/aiInterceptJudge/usage",{params:{days}})).data,

  llmProfiles: async () => (await client.get<{profiles: ProfileDTO[]}>("/aiLlmProfiles")).data.profiles.map(profile),
  chatLLMProfiles: async () => (await client.get<{models:ProfileDTO[]}>("/aiModels")).data.models.map(profile),
  saveLLMProfile: async (input: ProfileWrite) => {
    const { id, name, format, model, api_key, base_url, ...options } = input
    const body = { name, provider: format, model, apiKey: api_key, baseUrl: base_url || "", options, preserveDefault: true }
    const response = id ? await client.put<ProfileDTO>(`/aiLlmProfiles/${encodeURIComponent(id)}`, body) : await client.post<ProfileDTO>("/aiLlmProfiles", body)
    return { id: response.data.id }
  },
  deleteLLMProfile: async (id: string) => { await client.delete(`/aiLlmProfiles/${encodeURIComponent(id)}`); return { deleted: 1 } },
  activateLLMProfile: async (id: string) => { await client.post(`/aiLlmProfiles/${encodeURIComponent(id)}/activate`, {}); return { ok: true } },
  llmPool: async () => (await client.get<LLMPoolStatus>("/aiLlmRuntime/pool")).data,
  resetLLMPool: async (id?: string) => (await client.post<LLMPoolStatus>("/aiLlmRuntime/pool/reset", { id })).data,
  setSettings: async (patch: Record<string, unknown>) => (await client.patch(Object.keys(patch).every(key=>key==="llm_pool_enabled"||key==="llm_pool_bind_fallback")?"/aiLlmRuntime":"/aiRuntimeSettings", patch)).data,
  detectPython: async () => (await client.post<{python_interpreter:string}>("/aiRuntimeSettings/pythonDetection",{})).data,
  testWebSearch: async (input:{web_search_backend:string;web_search_proxy:string;brave_search_api_key:string;tavily_search_api_key:string}) => (await client.post<{ok:boolean;backend:string;count?:number;error?:string}>("/aiWebSearchTests",input,{timeout:125000})).data,
  llmRetryPolicy: async () => (await client.get<LLMRetryPolicy>("/aiLlmRuntime/retryPolicy")).data,
  saveLLMRetryPolicy: async (value: LLMRetryPolicy) => (await client.put<LLMRetryPolicy>("/aiLlmRuntime/retryPolicy", value)).data,
  fetchLLMModels: async (provider: string, baseUrl: string, apiKey: string, proxy = "", profileId?: string) => (await client.post<{ok:boolean;models?:string[];error?:string}>("/aiLlmModelCatalog", { provider, baseUrl, apiKey, profileId, proxy })).data,
  testLLM: async (provider: string, model: string, baseUrl: string, apiKey: string, proxy = "", thinkingType = "", reasoningEffort = "", profileId?: string, streaming = true, sessionHeaderKey = "") => (await client.post<{ok:boolean;error?:string;latency_ms?:number;model?:string;reply?:string}>("/aiLlmProfileTests", { provider, model, baseUrl, apiKey, profileId, options: { proxy, thinking_type: thinkingType, reasoning_effort: reasoningEffort, streaming, session_header_key: sessionHeaderKey } })).data,
}

export const api=Object.fromEntries(Object.entries(actions).map(([key,operation])=>[key,async(...args:unknown[])=>{try{return await Reflect.apply(operation,undefined,args)}catch(error){throw new Error(getErrorMessage(error))}}])) as typeof actions
