"use client"

import * as React from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { ChevronDown, ChevronRight, CornerDownRight, Search } from "lucide-react"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { CoverageNode } from "@/types/asset-coverage.types"
import { cn } from "@/lib/utils"
import { coverageVisuals } from "./coverage-visuals"
import { COVERAGE_TREE_PAGE_SIZE, COVERAGE_TREE_ROOTS, coverageTreeGroupId, coverageTreeRows, initialCoverageTreeState, type CoverageTreeIndex, type CoverageTreeRow, type CoverageTreeState } from "./coverage-tree-model"

type Props = { index: CoverageTreeIndex; selectedKey: string | null; coverageEnabled: boolean; onSelect: (node: CoverageNode) => void }

export function CoverageTree({ index, selectedKey, coverageEnabled, onSelect }: Props) {
  const t = useTranslations("aiTasks.coverageGraph")
  const [state, setState] = React.useState<CoverageTreeState>(() => initialCoverageTreeState(index))
  const [query, setQuery] = React.useState("")
  const listRef = React.useRef<HTMLDivElement>(null)
  const previousSelection = React.useRef<string | null>(null)
  const pendingSelection = React.useRef<string | null>(null)
  const { rows, matches } = React.useMemo(() => coverageTreeRows(index, state, query), [index, state, query])
  const searching = Boolean(query.trim())
  const virtualized = rows.length > 150
  const virtualizer = useVirtualizer<HTMLDivElement, HTMLDivElement>({
    count: rows.length,
    getScrollElement: () => listRef.current,
    getItemKey: index => rows[index]?.id ?? index,
    estimateSize: () => 40,
    overscan: 8,
    enabled: virtualized,
    useFlushSync: false,
  })
  React.useEffect(() => { if (listRef.current) listRef.current.scrollTop = 0 }, [query])
  React.useEffect(() => {
    if (previousSelection.current && selectedKey && previousSelection.current !== selectedKey) pendingSelection.current = selectedKey
    previousSelection.current = selectedKey
  }, [selectedKey])
  React.useEffect(() => {
    if (!selectedKey || !index.nodes.has(selectedKey)) return
    setState(previous => {
      const nodes = new Set(previous.nodes), groups = new Set(previous.groups), limits = new Map(previous.limits)
      let changed = false, key = selectedKey
      while (index.primaryParent.has(key)) {
        const parent = index.primaryParent.get(key)!, node = index.nodes.get(key)!
        const groupId = coverageTreeGroupId(parent, node.kind)
        if (!nodes.has(parent)) { nodes.add(parent); changed = true }
        if (!groups.has(groupId)) { groups.add(groupId); changed = true }
        const position = index.children.get(parent)?.get(node.kind)?.findIndex(item => item.key === key) ?? -1
        if (position >= (limits.get(groupId) ?? COVERAGE_TREE_PAGE_SIZE)) { limits.set(groupId, position + 1); changed = true }
        key = parent
      }
      const rootPosition = index.roots.findIndex(node => node.key === key)
      if (rootPosition >= (limits.get(COVERAGE_TREE_ROOTS) ?? COVERAGE_TREE_PAGE_SIZE)) { limits.set(COVERAGE_TREE_ROOTS, rootPosition + 1); changed = true }
      return changed ? { nodes, groups, limits } : previous
    })
  }, [index, selectedKey])
  React.useEffect(() => {
    if (!selectedKey || pendingSelection.current !== selectedKey) return
    const position = rows.findIndex(row => row.type === "asset" && row.node.key === selectedKey && !row.reference)
    if (position < 0) return
    if (virtualized) virtualizer.scrollToIndex(position, { align: "auto" })
    else listRef.current?.querySelector<HTMLElement>(`[data-row-index="${position}"]`)?.scrollIntoView({ block: "nearest" })
    pendingSelection.current = null
  }, [rows, selectedKey, virtualized, virtualizer])

  const toggle = (field: "nodes" | "groups", key: string) => setState(previous => {
    const next = new Set(previous[field])
    if (next.has(key)) next.delete(key)
    else next.add(key)
    return { ...previous, [field]: next }
  })
  const showMore = (groupId: string) => setState(previous => {
    const limits = new Map(previous.limits)
    limits.set(groupId, (limits.get(groupId) ?? COVERAGE_TREE_PAGE_SIZE) + COVERAGE_TREE_PAGE_SIZE)
    return { ...previous, limits }
  })

  const renderRow = (row: CoverageTreeRow) => {
    const paddingInlineStart = 8 + Math.min(row.depth, 8) * 16
    if (row.type === "more") return <div className="flex h-10 items-center" style={{ paddingInlineStart }}>
      <Button variant="ghost" size="xs" onClick={() => showMore(row.groupId)}>{t("treeShowMore", { count: Math.min(row.remaining, COVERAGE_TREE_PAGE_SIZE) })}</Button>
    </div>
    if (row.type === "group") return <div className="flex h-10 items-center" style={{ paddingInlineStart }}>
      <Button variant="ghost" size="content" className="h-9 gap-2 px-2 text-sm text-muted-foreground" aria-expanded={row.open} disabled={searching} onClick={() => toggle("groups", row.groupId)}>
        {row.open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}{t(`kinds.${row.kind}`)} <span className="tabular-nums">{row.count}</span>
      </Button>
    </div>
    const Icon = coverageVisuals[row.node.kind].icon
    const open = searching || state.nodes.has(row.node.key)
    return <div className="flex h-10 min-w-0 items-center gap-1 pr-2" style={{ paddingInlineStart }}>
      {row.expandable ? <Button variant="ghost" size="icon-sm" className="size-7 shrink-0" aria-label={t(open ? "treeCollapseAsset" : "treeExpandAsset", { name: row.node.label })} aria-expanded={open} disabled={searching} onClick={() => toggle("nodes", row.node.key)}>{open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}</Button> : <span className="inline-flex size-7 shrink-0 items-center justify-center text-muted-foreground">{row.reference && <CornerDownRight className="size-4" />}</span>}
      <Button variant="ghost" size="content" className={cn("h-9 min-w-0 flex-1 justify-start gap-2 px-2 text-left font-normal", selectedKey === row.node.key && "bg-accent text-accent-foreground")} aria-pressed={selectedKey === row.node.key} onClick={() => onSelect(row.node)}>
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate" title={row.node.label}>{row.node.label}</span>
        {row.reference && <span className="shrink-0 text-xs text-muted-foreground">{t("treeReference")}</span>}
        {coverageEnabled && row.node.in_scope && row.node.tested && <span className="shrink-0 text-xs text-emerald-600">{t("hasFact")}</span>}
      </Button>
    </div>
  }

  return <div className="flex h-[48vh] min-h-0 min-w-0 flex-1 flex-col lg:h-auto" data-slot="coverage-tree">
    <div className="flex items-center gap-2 border-b p-3">
      <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <Input type="search" size="sm" value={query} onChange={event => setQuery(event.target.value)} placeholder={t("treeSearchPlaceholder")} aria-label={t("treeSearch")} />
    </div>
    {searching && <p className="px-4 pt-2 text-xs text-muted-foreground" role="status">{t("treeSearchCount", { count: matches })}</p>}
    <div ref={listRef} className="coverage-tree-viewport min-h-64 flex-1 overflow-y-auto py-2" role="list" aria-label={t("treeView")}>
      {!rows.length ? <p className="p-4 text-sm text-muted-foreground">{t("treeNoMatches")}</p> : virtualized ? <div className="relative" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map(item => <div key={item.key} role="listitem" data-row-index={item.index} className="absolute left-0 top-0 w-full" style={{ height: item.size, transform: `translateY(${item.start}px)` }}>{renderRow(rows[item.index])}</div>)}
      </div> : rows.map((row, rowIndex) => <div key={row.id} role="listitem" data-row-index={rowIndex}>{renderRow(row)}</div>)}
    </div>
  </div>
}
