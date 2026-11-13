import { api as client } from "@/lib/api-client"
import type { Task, TaskSchedule } from "@/types/artex-ai.types"

export type ScheduleInput={name:string;enabled?:boolean;timezone_mode?:"beijing"|"system";schedule_type:"once"|"weekly";run_date?:string;end_date?:string;weekdays?:number[];start_time:string;end_time?:string;start_immediately?:boolean;task_ids:string[]}
type ScheduleDTO={id:string;displayName:string;remark:string;enabled:boolean;timezoneMode:TaskSchedule["timezone_mode"];timezone:string;scheduleType:TaskSchedule["schedule_type"];runDate:string;endDate:string;weekdays:number[];startTime:string;endTime:string;startImmediately:boolean;status:TaskSchedule["status"];lastTransitionAt?:string;lastError?:string;createdAt:string;updatedAt:string;taskIds:string[];nextStart?:string;nextEnd?:string;runCount:number;lastRunAt?:string;history?:Array<{id:string;taskId?:string;action:string;success:boolean;message:string;createdAt:string}>}
type TaskDTO={id:string;displayName:string;description:string;goal:string;status:Task["status"];queued:boolean;workerCount:number;createdAt:string;completedAt?:string}
const enc=encodeURIComponent
const task=(item:TaskDTO):Task=>({id:item.id,name:item.displayName,description:item.description,goal:item.goal,status:item.status,queued:item.queued,worker_count:item.workerCount,created_at:item.createdAt,completed_at:item.completedAt})
const schedule=(item:ScheduleDTO):TaskSchedule=>({id:item.id,name:item.displayName,remark:item.remark,enabled:item.enabled,timezone_mode:item.timezoneMode,timezone:item.timezone,schedule_type:item.scheduleType,run_date:item.runDate,end_date:item.endDate,weekdays:item.weekdays||[],start_time:item.startTime,end_time:item.endTime,start_immediately:item.startImmediately,status:item.status,last_transition_at:item.lastTransitionAt,last_error:item.lastError,next_start:item.nextStart,next_end:item.nextEnd,run_count:item.runCount,last_run_at:item.lastRunAt,task_ids:item.taskIds||[],history:item.history?.map(h=>({id:h.id,task_id:h.taskId,action:h.action,success:h.success,message:h.message,created_at:h.createdAt}))})
const payload=(input:ScheduleInput)=>({displayName:input.name,enabled:input.enabled,timezoneMode:input.timezone_mode||"beijing",scheduleType:input.schedule_type,runDate:input.run_date||"",endDate:input.end_date||"",weekdays:input.weekdays||[],startTime:input.start_time,endTime:input.end_time||"",startImmediately:input.start_immediately??false,taskIds:input.task_ids})
export const scheduleAPI={
 tasks:async()=>{const items:Task[]=[];const cursors=new Set<string>();let before="";do{const page=(await client.get<{tasks:TaskDTO[];nextBefore:string}>("/aiTasks",{params:before?{before}:undefined})).data;items.push(...page.tasks.map(task));before=page.nextBefore||"";if(before&&cursors.has(before))throw new Error("任务分页游标重复，请稍后重试");if(before)cursors.add(before)}while(before);return{tasks:items}},
 schedules:async()=>(await client.get<{schedules:ScheduleDTO[]}>("/aiSchedules")).data.schedules.map(schedule),
 schedule:async(id:string)=>schedule((await client.get<ScheduleDTO>(`/aiSchedules/${enc(id)}`)).data),
 createSchedule:async(input:ScheduleInput)=>schedule((await client.post<ScheduleDTO>("/aiSchedules",payload(input))).data),
 updateSchedule:async(id:string,input:ScheduleInput)=>schedule((await client.put<ScheduleDTO>(`/aiSchedules/${enc(id)}`,payload(input))).data),
 renameSchedule:async(id:string,name:string)=>schedule((await client.patch<ScheduleDTO>(`/aiSchedules/${enc(id)}`,{displayName:name})).data),
 updateScheduleRemark:async(id:string,remark:string)=>schedule((await client.patch<ScheduleDTO>(`/aiSchedules/${enc(id)}`,{remark})).data),
 deleteSchedule:async(id:string)=>(await client.delete(`/aiSchedules/${enc(id)}`)).data,
 pauseSchedule:async(id:string)=>(await client.post(`/aiSchedules/${enc(id)}/actions`,{action:"disable"})).data,
 resumeSchedule:async(id:string)=>(await client.post(`/aiSchedules/${enc(id)}/actions`,{action:"enable"})).data,
 runScheduleNow:async(id:string)=>(await client.post<{started:number;errors?:string[]}>(`/aiSchedules/${enc(id)}/actions`,{action:"run"})).data,
}
