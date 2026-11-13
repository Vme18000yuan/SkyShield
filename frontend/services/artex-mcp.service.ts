import { api as client } from "@/lib/api-client"
import type { MCPServer, MCPTool } from "@/types/artex-ai.types"

const enc=encodeURIComponent
type MCPDTO={id:string;displayName:string;endpoint:string;enabled:boolean;insecure:boolean;transport:MCPServer["transport"];command:string;args:string[];tools:string[]}
export const mcpAPI={
 mcpServers:async():Promise<MCPServer[]> => (await client.get<{servers:MCPDTO[]}>("/aiMcpServers")).data.servers.map(item=>({id:item.id,name:item.displayName,url:item.endpoint,enabled:item.enabled,insecure:item.insecure,transport:item.transport||"http",command:item.command,args:item.args||[],env:{},tools:item.tools||[]})),
 saveMcpServer:async(input:Partial<MCPServer>)=>{const {id,...item}=input;const body={displayName:item.name,endpoint:item.url||"",enabled:item.enabled??true,insecure:item.insecure??false,transport:item.transport,command:item.command,args:item.args,env:item.env};return (id?await client.put(`/aiMcpServers/${enc(id)}`,body):await client.post("/aiMcpServers",body)).data},
 mcpDetails:async(id:string)=>(await client.get<{env:Record<string,string>}>(`/aiMcpServers/${enc(id)}/details`)).data,
 setMcpEnabled:async(id:string,enabled:boolean)=>(await client.patch(`/aiMcpServers/${enc(id)}`,{enabled})).data,
 deleteMcpServer:async(id:string)=>(await client.delete(`/aiMcpServers/${enc(id)}`)).data,
 mcpTools:async(id:string)=>(await client.get<{tools:MCPTool[]}>(`/aiMcpServers/${enc(id)}/tools`)).data.tools,
 refreshMcpServer:async(id:string)=>(await client.post<{tools:MCPTool[]}>(`/aiMcpServers/${enc(id)}/refresh`,{},{timeout:70000})).data.tools,
 resourceVisibility:async(kind:string,id:string)=>{if(kind!=="mcp")throw new Error("未知资源类型");return(await client.get<{agents:string[]}>(`/aiMcpServers/${enc(id)}/visibility`)).data.agents},
 toggleVisibility:async(agent:string,kind:string,id:string,visible:boolean)=>{if(kind!=="mcp")throw new Error("未知资源类型");return(await client.post(`/aiMcpServers/${enc(id)}/visibility`,{agent,visible})).data},
}
