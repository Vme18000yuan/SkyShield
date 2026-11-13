import type { AIModelOption } from "@/types/ai-chat.types"
import type { AIUsageDay, AIUsageProfile, AIUsageTotals } from "@/types/ai-usage.types"

export const OVERVIEW_TOKEN_DAYS = [7, 30, 90, 180, 365] as const
export const OVERVIEW_TOKEN_DEFAULT_DAYS = 30

export type TokenMetrics = Pick<AIUsageTotals, "calls" | "input_tokens" | "output_tokens" | "cache_read_tokens" | "cache_write_tokens">
export type TokenProfile = { key: string; label: string; isDefault: boolean; historical: boolean; metrics: TokenMetrics }
export type TokenDay = { date: string; input: number; cacheRead: number; output: number }

export function tokenProfileKey(id: string, name: string) {
  return id ? `id:${id}` : `name:${name}`
}

export function emptyTokenMetrics(): TokenMetrics {
  return { calls: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 }
}

// Profile IDs remain attached to historical usage after a rename or deletion.
// Only unprofiled environment calls fall back to their stored name.
export function buildTokenProfiles(models: AIModelOption[], usage: AIUsageProfile[], environmentLabel: string): TokenProfile[] {
  const groups = new Map<string, TokenProfile>()
  for (const model of models) {
    const key = tokenProfileKey(model.id, "")
    groups.set(key, { key, label: model.displayName, isDefault: model.isDefault, historical: false, metrics: emptyTokenMetrics() })
  }
  for (const row of usage) {
    const key = tokenProfileKey(row.profile_id, row.profile_name)
    let group = groups.get(key)
    if (!group) {
      group = { key, label: row.profile_name || environmentLabel, isDefault: false, historical: Boolean(row.profile_id), metrics: emptyTokenMetrics() }
      groups.set(key, group)
    }
    group.metrics.calls += row.calls
    group.metrics.input_tokens += row.input_tokens
    group.metrics.output_tokens += row.output_tokens
    group.metrics.cache_read_tokens += row.cache_read_tokens
    group.metrics.cache_write_tokens += row.cache_write_tokens
  }
  return [...groups.values()]
}

// The provider-normalized input includes cache reads, so split the input before
// stacking the chart; otherwise cache hits would be counted twice.
export function buildTokenDays(rows: AIUsageDay[], selected: string, days: number, now = new Date()): TokenDay[] {
  const daily = new Map<string, { input: number; cacheRead: number; output: number }>()
  for (const row of rows) {
    if (selected !== "all" && tokenProfileKey(row.profile_id, row.profile_name) !== selected) continue
    const item = daily.get(row.date) ?? { input: 0, cacheRead: 0, output: 0 }
    item.input += row.input_tokens
    item.cacheRead += row.cache_read_tokens
    item.output += row.output_tokens
    daily.set(row.date, item)
  }
  const today = new Date(now)
  today.setUTCHours(0, 0, 0, 0)
  return Array.from({ length: days }, (_, index) => {
    const date = new Date(today)
    date.setUTCDate(today.getUTCDate() - (days - index - 1))
    const key = date.toISOString().slice(0, 10)
    const item = daily.get(key) ?? { input: 0, cacheRead: 0, output: 0 }
    const cacheRead = Math.min(item.input, item.cacheRead)
    return { date: key, input: Math.max(0, item.input - cacheRead), cacheRead, output: item.output }
  })
}
