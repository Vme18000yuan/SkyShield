import { api } from "@/lib/api-client"
import type { OASTCreateInput, OASTEventPage, OASTProbe, OASTProbePage, OASTScope } from "@/types/oast.types"

function base(scope: OASTScope) {
  return `/${scope.kind === "task" ? "aiTasks" : "aiConversations"}/${encodeURIComponent(scope.id)}/oastProbes`
}

export const OASTService = {
  async list(scope: OASTScope, before: string, signal?: AbortSignal) {
    return (await api.get<OASTProbePage>(base(scope), { params: { before: before || undefined }, signal })).data
  },
  async create(scope: OASTScope, input: OASTCreateInput) {
    return (await api.post<OASTProbe>(base(scope), input)).data
  },
  async events(scope: OASTScope, id: string, after: string, signal?: AbortSignal) {
    return (await api.get<OASTEventPage>(`${base(scope)}/${encodeURIComponent(id)}/events`, { params: { after: after || undefined }, signal })).data
  },
  async revoke(scope: OASTScope, id: string) {
    await api.delete(`${base(scope)}/${encodeURIComponent(id)}`)
  },
}
