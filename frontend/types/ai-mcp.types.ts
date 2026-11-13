export interface AIMCPInput { displayName: string; endpoint: string; enabled: boolean; insecure: boolean; headers?: Record<string, string>; clearHeaders?: boolean; agents: string[] }
export interface AIMCPServer extends Omit<AIMCPInput, "headers" | "clearHeaders"> { id: string; name: string; hasHeaders: boolean; createdAt: string; updatedAt: string }
export interface AIMCPTool { name: string; description: string }
