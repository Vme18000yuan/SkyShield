export const inventoryFilters = ["all", "root_domain", "subdomain", "ip", "app", "web", "port", "endpoint"] as const
export type InventoryFilter = typeof inventoryFilters[number]

export function inventoryFilter(value: unknown): InventoryFilter {
  return inventoryFilters.find(filter => filter === value) ?? "all"
}

export function assetInventoryHref(options: {
  organizationId?: number
  view?: "inventory"
  type?: InventoryFilter
  showCdn?: boolean
  q?: string
  path?: string
  from?: "overview"
} = {}) {
  const params = new URLSearchParams()
  if (options.organizationId) params.set("organizationId", String(options.organizationId))
  if (options.view) params.set("view", options.view)
  if (options.type && options.type !== "all") params.set("type", options.type)
  if (options.showCdn) params.set("showCdn", "1")
  if (options.from) params.set("from", options.from)
  if (options.q) params.set("q", options.q)
  return (options.path ?? "/assets/") + (params.size ? `?${params}` : "")
}
