export interface RepeaterDraft {
  scheme: "auto" | "http" | "https"
  rawRequest: string
  timeoutSeconds: number
  verifyTls: boolean
  autoContentLength: boolean
}
export interface RepeaterInput extends RepeaterDraft { requestId: string }
export interface RepeaterResult {
  requestId: string
  connectionTarget: string
  requestRaw: string
  responseRaw: string
  responseBase64: string | null
  statusCode: number
  elapsedMs: number
  byteCount: number
  sent: boolean
  truncated: boolean
  binary: boolean
  requestProtocol?: string
  responseProtocol?: string
  representation?: "http1_raw" | "http2_decoded"
  normalizedHeaders?: string[]
  errorCode?: string
}
