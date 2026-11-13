export type AIFindingStatus = "pending"|"in_progress"|"confirmed"|"resolved"|"fixed"|"false_positive"|"ignored"|"duplicate"|"risk_accepted"
export interface AITaskFinding {
  id:string;task_id:string;node_id?:string;task_name:string;name:string;vulnclass:string;severity:string;status:AIFindingStatus
  summary:string;evidence?:string;report?:string;evidence_version:number;report_evidence_version:number;has_report:boolean;traffic_count:number;risk_id?:string;case_id?:string
  assets:Array<{id:string;type:string;label:string}>;inherited:boolean;created_at:string
}
export interface AITaskFindingPage {items:AITaskFinding[];total:number;page:number;size:number}
