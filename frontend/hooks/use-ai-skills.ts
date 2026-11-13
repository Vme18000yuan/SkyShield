"use client"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AISkillService } from "@/services/ai-skill.service"
import type { AISkillInput } from "@/types/ai-skill.types"
export function useAISkills() {
  const user = useAuth().data?.user?.id
  const client = useQueryClient()
  const key = ["ai-skills", "default-tenant", user] as const
  const list = useQuery({ queryKey: key, queryFn: AISkillService.list, enabled: Boolean(user) })
  const invalidate = () => client.invalidateQueries({ queryKey: key })
  const save = useMutation({ mutationFn: ({ id, input }: { id: string | null; input: AISkillInput }) => AISkillService.save(id, input), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: AISkillService.delete, onSuccess: invalidate })
  return { list, save, remove }
}
