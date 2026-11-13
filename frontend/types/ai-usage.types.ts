export interface AIUsageTotals {
  calls: number
  tasks: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
}
export interface AIUsageProfile extends AIUsageTotals { profile_id: string; profile_name: string }
export interface AIUsageDay extends AIUsageProfile { date: string }
export interface AIUsageSummary { totals: AIUsageTotals; profiles: AIUsageProfile[]; daily: AIUsageDay[]; days: number }
export interface AIUsageModel extends AIUsageTotals { model: string }
export interface AILLMRecord {
  id:string; task_id?:string; task_name?:string; conversation_id?:string; session_id:string; worker:string; model:string; profile_name:string
  duration_ms:number; input_tokens:number; output_tokens:number; cache_read_tokens:number; cache_write_tokens:number; status:"ok"|"error"|"interrupted"; truncated:boolean; created_at:string
}
export interface AILLMRecordDetail extends AILLMRecord {
  payload:{request_body:string;response_body:string;normalized_request:string;normalized_response:string;truncated:boolean}
}
export interface AILLMRecordPage {items:AILLMRecord[];total:number;page:number;size:number}
export interface AILLMRecordTasks {items:Array<{id:string;name:string;count:number}>;has_more:boolean}
