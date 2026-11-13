"use client"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AIMCPService } from "@/services/ai-mcp.service"
import type { AIMCPInput } from "@/types/ai-mcp.types"
export function useAIMCP() {
  const user = useAuth().data?.user?.id
  const client = useQueryClient()
  const key = ["ai-mcp-servers", "default-tenant", user] as const
  const list = useQuery({ queryKey: key, queryFn: AIMCPService.list, enabled: Boolean(user) })
  const invalidate = () => client.invalidateQueries({ queryKey: key })
  const save = useMutation({ mutationFn: ({ id, input }: { id: string | null; input: AIMCPInput }) => AIMCPService.save(id, input), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: AIMCPService.delete, onSuccess: invalidate })
  const test = useMutation({ mutationFn: AIMCPService.test })
  return { list, save, remove, test }
}
