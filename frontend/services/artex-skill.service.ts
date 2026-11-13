import { api as client } from "@/lib/api-client"
import type { SkillCall, MissingSkill } from "@/types/artex-ai.types"

const enc=encodeURIComponent
const root=(name:string)=>`/aiSkillPackages/${enc(name)}`
const filePath=(name:string,path:string)=>`${root(name)}/files/${path.split("/").map(enc).join("/")}`
export const skillAPI={
 createSkill:async(input:{name:string;description:string;license?:string;compatibility?:string;mcps?:string[];instructions?:string})=>(await client.post<{name:string}>("/aiSkillPackages",input)).data,
 deleteSkill:async(name:string)=>(await client.delete(`${root(name)}`)).data,
 updateSkillMeta:async(name:string,meta:{mcps?:string[];description?:string;license?:string;compatibility?:string})=>(await client.put(`${root(name)}/metadata`,meta)).data,
 createSkillDir:async(name:string,path:string)=>(await client.post(`${root(name)}/directories`,{path})).data,
 skillFiles:async(name:string)=>(await client.get<{files:string[]}>(`${root(name)}/files`)).data.files,
 readSkillFile:async(name:string,path:string)=>(await client.get<{content:string}>(filePath(name,path))).data.content,
 writeSkillFile:async(name:string,path:string,content:string)=>(await client.put(filePath(name,path),{content})).data,
 deleteSkillPath:async(name:string,path:string)=>(await client.delete(filePath(name,path))).data,
 skillUsage:async(name:string,limit=50)=>(await client.get<{calls:SkillCall[]}>(`${root(name)}/usage`,{params:{limit}})).data.calls,
 missingSkills:async(limit=20)=>(await client.get<{missing:MissingSkill[]}>("/aiMissingSkills",{params:{limit}})).data.missing,
 skillVisibility:async(name:string)=>(await client.get<{agents:string[]}>(`${root(name)}/visibility`)).data.agents,
 toggleSkillVisibility:async(agent:string,name:string,visible:boolean)=>(await client.post(`${root(name)}/visibility`,{agent,visible})).data,
 uploadSkill:async(file:File,overwrite=false)=>{const body=new FormData();body.append("file",file);return(await client.post<{name:string;files:number}>("/aiSkillArchives",body,{params:{overwrite},headers:{"Content-Type":undefined},timeout:120000})).data},
}
