import { api as client, apiClient, ensurePrimaryTokenFresh } from "@/lib/api-client"
import type { Activity, ChatAttachment, Conversation, InterceptPending, InterceptExecution, InterceptExecutionContext, InterceptDetail, InterceptApprovalRow, InterceptApprovalFilter } from "@/types/artex-ai.types"
import type { ChatMention } from "@/lib/artex/chat-mentions"
import type { ConversationFeedPage } from "@/lib/artex/conversation-feed"

type ConversationDTO = {id:string; title:string; agentKey:string; llmProfileId?:string; pinned:boolean; createdAt:string; updatedAt:string; running:boolean; reviewReadOnly?:boolean}
type ConversationDeletion = {items:{id:string;ok:boolean;error?:string;cleanupPending?:boolean}[]}
const conversation=(item:ConversationDTO):Conversation=>({id:item.id,title:item.title,agent_key:item.agentKey==="assistant"?"mainagent":item.agentKey,llm_profile_id:item.llmProfileId,pinned:item.pinned,created_at:item.createdAt,updated_at:item.updatedAt,running:item.running,review_read_only:item.reviewReadOnly})
const enc=encodeURIComponent
const query=(filter:InterceptApprovalFilter,page=1,size=20)=>new URLSearchParams({...Object.fromEntries(Object.entries(filter).filter(([,value])=>value)),page:String(page),size:String(size)}).toString()
export function nativeRoute(path:string):string{return path.replace(/^\/api/,"").replace(/^\/conversations/,"/aiConversations").replace(/^\/side-questions/,"/aiSideQuestions").replace(/^\/tasks/,"/aiTasks")}
export async function http<T>(path:string,init?:RequestInit):Promise<T>{return (await apiClient.request<T>({url:nativeRoute(path),method:init?.method||"GET",data:typeof init?.body==="string"?JSON.parse(init.body):init?.body,signal:init?.signal||undefined})).data}

// Keep the native EventTarget surface while sharing the product's token refresh.
export function openSSE(path:string){
 const target=new EventTarget();const controller=new AbortController();let closed=false
 void (async()=>{try{const token=await ensurePrimaryTokenFresh();const response=await fetch(`/v1${nativeRoute(path)}`,{headers:token?{Authorization:`Bearer ${token}`}:{},signal:controller.signal});if(!response.ok||!response.body)throw new Error("侧问事件流连接失败");const reader=response.body.getReader();const decoder=new TextDecoder();let buffer="";while(!closed){const{done,value}=await reader.read();if(done)break;buffer+=decoder.decode(value,{stream:true}).replace(/\r\n/g,"\n");let boundary;while((boundary=buffer.indexOf("\n\n"))>=0){const frame=buffer.slice(0,boundary);buffer=buffer.slice(boundary+2);const lines=frame.split("\n");const type=lines.find(line=>line.startsWith("event:"))?.slice(6).trim()||"message";const data=lines.filter(line=>line.startsWith("data:")).map(line=>line.slice(5).trimStart()).join("\n");if(data)target.dispatchEvent(new MessageEvent(type,{data}))}}}catch{if(!closed)target.dispatchEvent(new Event("error"))}})()
 return {addEventListener:target.addEventListener.bind(target),close:()=>{closed=true;controller.abort()}}
}

export const chatAPI={
 conversations:async()=>(await client.get<{conversations:ConversationDTO[]}>("/aiConversations")).data.conversations.map(conversation),
 createConversation:async(agent_key:string,title="",llm_profile_id?:string|null)=>{const item=(await client.post<ConversationDTO>("/aiConversations",{agentKey:agent_key,title})).data;if(llm_profile_id)await client.put(`/aiConversations/${enc(item.id)}/model`,{llmProfileId:llm_profile_id});return conversation({...item,llmProfileId:llm_profile_id||undefined})},
 renameConversation:async(id:string,title:string)=>{await client.patch(`/aiConversations/${enc(id)}`,{title})},
 pinConversation:async(id:string,pinned:boolean)=>{await client.patch(`/aiConversations/${enc(id)}`,{pinned})},
 updateConversationProfile:async(id:string,llm_profile_id:string|null)=>(await client.put(`/aiConversations/${enc(id)}/model`,{llmProfileId:llm_profile_id})).data,
 deleteConversation:async(id:string)=>(await client.delete<ConversationDeletion>(`/aiConversations/${enc(id)}`)).data,
 deleteConversations:async(ids:string[])=>(await client.post<ConversationDeletion>("/aiConversationDeletions",{ids})).data,
 conversationMessages:async(id:string,since=0)=>(await client.get<ConversationFeedPage>(`/aiConversations/${enc(id)}/messages`,{params:{since}})).data,
 conversationHistory:async(id:string,before=0,limit=200)=>(await client.get<ConversationFeedPage & {hasMore:boolean}>(`/aiConversations/${enc(id)}/messages`,{params:{before,limit}})).data,
 conversationMsgDetail:async(id:string,seq:number)=>(await client.get<{detail:string}>(`/aiConversations/${enc(id)}/messages/${seq}`)).data.detail,
 sendConversationMessage:async(id:string,message:string,attachments?:ChatAttachment[])=>(await client.post(`/aiConversations/${enc(id)}/messages`,{message,attachments})).data,
 stopConversation:async(id:string)=>(await client.post(`/aiConversations/${enc(id)}/stop`,{})).data,
 chatUpload:async(scope:"session"|"task"|"staging",id:string,files:File[])=>{const body=new FormData();for(const file of files)body.append("file",file);return(await client.post<{attachments:ChatAttachment[]}>("/aiChatAttachments",body,{params:{scope,id},headers:{"Content-Type":undefined},timeout:120000})).data},
 chatMentions:async(kind:string,q:string,signal?:AbortSignal,cursor="")=>(await client.get<{items:ChatMention[];next_cursor?:string}>("/aiChatMentions",{params:{kind,q,cursor},signal})).data,
 activityDetail:async(seq:number,taskId?:string)=>(await client.get<{detail:string}>(`/aiTasks/${enc(taskId||"")}/activities/${seq}`)).data,
 interceptGetOne:async(id:number)=>(await client.get<InterceptPending>(`/aiApprovals/${id}`)).data,
 interceptDecide:async(id:number,decision:"allowed"|"denied")=>(await client.post(`/aiApprovals/${id}/decision`,{decision})).data,
 interceptExecution:async(id:number,conversationId?:string)=>(await client.get<InterceptExecution>(`/aiApprovals/${id}/execution`,{params:{conversation:conversationId}})).data,
 interceptExecutionContext:async(id:number,before?:number,after?:number)=>(await client.get<InterceptExecutionContext>(`/aiApprovals/${id}/execution/context`,{params:{before,after,size:50}})).data,
 interceptDetail:async(id:number)=>(await client.get<InterceptDetail>(`/aiApprovals/${id}/detail`)).data,
 interceptPending:async()=>(await client.get<{pending:InterceptPending[]}>("/aiApprovals/pending")).data.pending,
 interceptHistoryPage:async(page=1,size=20,filter:InterceptApprovalFilter={})=>(await client.get<{items:InterceptApprovalRow[];total:number}>(`/aiApprovals?${query(filter,page,size)}`)).data,
 interceptTaskPage:async(taskId:string,page=1,size=20,filter:InterceptApprovalFilter={})=>(await client.get<{items:InterceptApprovalRow[];total:number}>(`/aiTasks/${enc(taskId)}/approvals?${query(filter,page,size)}`)).data,
}
