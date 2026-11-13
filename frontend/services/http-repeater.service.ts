import { api } from "@/lib/api-client"
import type { RepeaterInput, RepeaterResult } from "@/types/http-repeater.types"
import { deriveReplayTarget, normalizeReplayRequest } from "./http-repeater-target"

export async function sendHttpReplay(input: RepeaterInput, signal: AbortSignal): Promise<RepeaterResult> {
  const { scheme, ...request } = input
  const rawRequest = normalizeReplayRequest(input.rawRequest)
  const targetUrl = deriveReplayTarget(rawRequest, scheme)
  const response = await api.post<Omit<RepeaterResult, "connectionTarget">>("/httpRepeater:send", { ...request, rawRequest, targetUrl }, { signal, timeout: (input.timeoutSeconds + 5) * 1000 })
  return { ...response.data, connectionTarget: targetUrl }
}
