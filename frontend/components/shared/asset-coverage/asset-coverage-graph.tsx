// Adapted from ARTEX's task coverage graph, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { Background, Controls, Handle, Position, ReactFlow, useNodesState, type Edge, type Node, type NodeProps } from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import Link from "next/link"
import { useCoverageGraph, useCoverageRefs } from "@/hooks/use-asset-coverage"
import type { CoverageKind, CoverageNode, CoverageScope } from "@/types/asset-coverage.types"
import { useTranslations } from "next-intl"
import { AppWindow, Building2, Globe, Link2, MoreHorizontal, Radio, Server, Waypoints, type LucideIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useAuth } from "@/hooks/use-auth"
import {
  COVERAGE_FOLD_LIMIT, COVERAGE_FOLD_STEP, coverageNodeLabel, coveragePositions, coverageVisible,
  type CoverageFold,
} from "./coverage-graph-layout"

type AssetVisual = Node<{ asset: CoverageNode }, "asset">
type FoldVisual = Node<{ fold: CoverageFold }, "fold">
type VisualNode = AssetVisual | FoldVisual

const meta: Record<CoverageKind, { icon: LucideIcon; color: string; layer: number }> = {
  company: { icon: Building2, color: "#64748b", layer: 0 }, root_domain: { icon: Globe, color: "#6366f1", layer: 1 },
  subdomain: { icon: Waypoints, color: "#3b82f6", layer: 2 }, ip: { icon: Server, color: "#0891b2", layer: 2 },
  service: { icon: Radio, color: "#d97706", layer: 3 }, app: { icon: AppWindow, color: "#c026d3", layer: 3 }, endpoint: { icon: Link2, color: "#e11d48", layer: 4 },
}

function AssetNode({ data }: NodeProps<AssetVisual>) {
  const t = useTranslations("aiTasks.coverageGraph")
  const item = data.asset, config = meta[item.kind], Icon = config.icon
  return <div className={`flex min-w-40 max-w-56 items-center gap-2 rounded-lg border bg-card px-3 py-2 shadow-sm ${item.tested ? "ring-2 ring-emerald-500/70" : ""} ${!item.in_scope ? "border-dashed opacity-65" : ""}`}>
    <Handle type="target" position={Position.Left} className="!size-2 !border-0 !bg-muted-foreground" />
    <span className="flex size-7 shrink-0 items-center justify-center rounded-md text-white" style={{ backgroundColor: item.tested ? config.color : "#94a3b8" }}><Icon className="size-4" /></span>
    <span className="min-w-0"><span className="block text-[10px] text-muted-foreground">{t(`kinds.${item.kind}`)}</span><span className="block truncate text-xs font-medium" title={item.label}>{coverageNodeLabel(item)}</span></span>
    <Handle type="source" position={Position.Right} className="!size-2 !border-0 !bg-muted-foreground" />
  </div>
}

function FoldNode({ data }: NodeProps<FoldVisual>) {
  const t = useTranslations("aiTasks.coverageGraph")
  return <div className="flex min-w-40 items-center gap-2 rounded-lg border border-dashed bg-muted px-3 py-2 text-xs text-muted-foreground shadow-sm">
    <Handle type="target" position={Position.Left} className="!size-2 !border-0 !bg-muted-foreground" />
    <MoreHorizontal className="size-5 shrink-0" />
    <span>{t("foldCount", { count: data.fold.hidden.length, kind: t(`kinds.${data.fold.kind}`) })}</span>
  </div>
}
const nodeTypes = { asset: AssetNode, fold: FoldNode }

type CoverageGraphProps = { scope: CoverageScope; coverageEnabled?: boolean; sourceNames?: Record<string, string> }

export function AssetCoverageGraph(props: CoverageGraphProps) {
  const user = useAuth().data?.user?.id
  if (!user) return null
  return <AccountCoverageGraph key={`${user}:${JSON.stringify(props.scope)}`} {...props} />
}

function AccountCoverageGraph({ scope, coverageEnabled = true, sourceNames = {} }: CoverageGraphProps) {
  const t = useTranslations("aiTasks.coverageGraph")
  const [expanded, setExpanded] = React.useState<Map<string, number>>(() => new Map())
  const [selected, setSelected] = React.useState<CoverageNode | null>(null)
  const [selectedFoldId, setSelectedFoldId] = React.useState<string | null>(null)
  const graph = useCoverageGraph(scope)
  const refs = useCoverageRefs(scope, selected?.asset_id, !graph.isError)
  const view = React.useMemo(() => {
    const data = graph.data ?? { nodes: [], edges: [], truncated: false }
    const nodes = coverageEnabled ? data.nodes : data.nodes.map(node => ({ ...node, tested: false }))
    return coverageVisible({ ...data, nodes }, expanded)
  }, [graph.data, coverageEnabled, expanded])
  const layout = React.useMemo(() => {
    const keys = [...view.nodes.map(asset => asset.key), ...view.folds.map(fold => fold.key)]
    const layers = new Map<string, number>()
    for (const asset of view.nodes) layers.set(asset.key, meta[asset.kind].layer)
    for (const fold of view.folds) layers.set(fold.key, meta[fold.kind].layer)
    const positions = coveragePositions(keys, view.edges, layers)
    const nodes: VisualNode[] = [
      ...view.nodes.map((asset): AssetVisual => ({ id: asset.key, type: "asset", position: positions.get(asset.key) ?? { x: 0, y: 0 }, data: { asset } })),
      ...view.folds.map((fold): FoldVisual => ({ id: fold.key, type: "fold", position: positions.get(fold.key) ?? { x: 0, y: 0 }, data: { fold } })),
    ]
    const edges: Edge[] = view.edges.map((edge, index) => ({ id: `${edge.src}-${edge.dst}-${index}`, source: edge.dst, target: edge.src, type: "smoothstep", style: { stroke: "var(--muted-foreground)", opacity: .55 } }))
    return { nodes, edges }
  }, [view])
  const [nodes, setNodes, onNodesChange] = useNodesState<VisualNode>([])
  React.useEffect(() => setNodes(layout.nodes), [layout.nodes, setNodes])
  React.useEffect(() => { if (graph.isError) { setSelected(null); setSelectedFoldId(null) } }, [graph.isError])
  const selectedFold = view.folds.find(fold => fold.key === selectedFoldId) ?? null
  React.useEffect(() => { if (selectedFoldId && !selectedFold) setSelectedFoldId(null) }, [selectedFoldId, selectedFold])
  const counts = React.useMemo(() => {
    const inScope = (graph.data?.nodes ?? []).filter(node => node.in_scope)
    return { scope: inScope.length, tested: inScope.filter(node => node.tested).length }
  }, [graph.data])
  const showMore = (groupId: string) => setExpanded(previous => {
    const next = new Map(previous)
    next.set(groupId, (next.get(groupId) ?? COVERAGE_FOLD_LIMIT) + COVERAGE_FOLD_STEP)
    return next
  })

  return <div className="space-y-3">
    <div className="flex flex-wrap items-center gap-3 text-sm">
      {!graph.isError && <span className="text-muted-foreground">{t("scopeCount", { count: counts.scope })}{coverageEnabled ? ` · ${t("testedCount", { count: counts.tested })}` : ""}</span>}
      {coverageEnabled && <span className="text-emerald-600">{t("legend")}</span>}
      <span className="text-muted-foreground">{t("foldHint")}</span>
      <Button size="sm" variant="outline" onClick={() => { void graph.refetch(); if (selected?.asset_id) void refs.refetch() }} disabled={graph.isFetching}>{t("refresh")}</Button>
      {!graph.isError && graph.data?.truncated && <span className="text-amber-600">{t("truncated")}</span>}
    </div>
    {graph.isError && <p role="alert" className="text-sm text-destructive">{t("loadError")}</p>}
    <div className="h-[68vh] min-h-96 overflow-hidden rounded-lg border bg-muted/20">
      {graph.isError ? null : graph.isLoading ? <div className="p-4 text-sm text-muted-foreground">{t("loading")}</div> : !graph.data?.nodes.length ? <div className="p-4 text-sm text-muted-foreground">{t("empty")}</div> : <ReactFlow nodes={nodes} edges={layout.edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange} onNodeClick={(_, node) => {
        if (node.type === "fold") { setSelected(null); setSelectedFoldId(node.id) }
        else if (node.type === "asset") { setSelectedFoldId(null); setSelected(node.data.asset) }
      }} fitView minZoom={0.1} maxZoom={2} nodesConnectable={false} elementsSelectable><Background /><Controls /></ReactFlow>}
    </div>
    <Sheet open={!!selectedFold && !graph.isError} onOpenChange={open => { if (!open) setSelectedFoldId(null) }}><SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md">
      {selectedFold && !graph.isError && <><SheetHeader className="border-b p-4"><SheetTitle>{t("hiddenAssets", { kind: t(`kinds.${selectedFold.kind}`), count: selectedFold.hidden.length })}</SheetTitle><SheetDescription>{t("foldDescription")}</SheetDescription></SheetHeader>
        <ScrollArea className="min-h-0 flex-1"><div className="flex flex-col gap-1 p-3">{selectedFold.hidden.map(asset => <button key={asset.key} type="button" onClick={() => { setSelectedFoldId(null); setSelected(asset) }} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent"><span className={`size-2 shrink-0 rounded-full ${asset.tested ? "bg-emerald-500" : "bg-muted-foreground"}`} /><span className="min-w-0 flex-1 truncate font-mono text-xs" title={asset.label}>{asset.label}</span></button>)}</div></ScrollArea>
        <div className="border-t p-3"><Button className="w-full" variant="outline" onClick={() => showMore(selectedFold.groupId)}>{t("showMoreCount", { count: COVERAGE_FOLD_STEP })}</Button></div></>}
    </SheetContent></Sheet>
    <Sheet open={!!selected && !graph.isError} onOpenChange={open => { if (!open) setSelected(null) }}><SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md">
      <SheetHeader className="border-b p-4"><SheetTitle>{selected ? t(`kinds.${selected.kind}`) : t("asset")}</SheetTitle><SheetDescription className="break-all">{selected?.label}</SheetDescription></SheetHeader>
      {selected && !graph.isError && <ScrollArea className="min-h-0 flex-1"><div className="space-y-4 p-4 text-sm"><section><h4 className="mb-2 text-xs font-medium text-muted-foreground">{t("attributes")}</h4><dl className="grid grid-cols-[6rem_1fr] gap-2"><dt className="text-muted-foreground">{t("scope")}</dt><dd>{selected.in_scope ? (coverageEnabled ? (selected.tested ? t("hasFact") : t("noFact")) : t("inScope")) : t("connector")}</dd>{([[t("domain"), selected.domain], [t("rootDomain"), selected.root_domain], [t("ip"), selected.ip], [t("port"), selected.port], [t("url"), selected.url], [t("pageTitle"), selected.page_title], [t("statusCode"), selected.status_code], [t("serviceType"), selected.service_type], [t("app"), selected.app_name], [t("assetId"), selected.asset_id]] as const).map(([label, value]) => value != null && value !== "" ? <React.Fragment key={label}><dt className="text-muted-foreground">{label}</dt><dd className="break-all">{value}</dd></React.Fragment> : null)}</dl></section>
        {refs.isError && <p role="alert" className="text-destructive">{t("refsError")}</p>}
        {!refs.isError && !refs.isFetching && refs.data && ([[t("intents"), refs.data.intents], [t("facts"), refs.data.facts], [t("findings"), refs.data.findings]] as const).map(([label, items]) => items.length ? <section className="border-t pt-2" key={label}><strong>{label}（{items.length}）</strong><ul className="mt-1 space-y-1">{items.map(item => <li key={`${item.kind}-${item.id}`} className="break-words rounded bg-muted/50 px-2 py-1.5">{item.summary || item.kind} <span className="text-xs text-muted-foreground">{item.state}{item.task_name && item.source_task_id ? <Link className="ml-2 underline" href={`/ai/tasks/${encodeURIComponent(item.source_task_id)}/`}>{item.task_name}</Link> : null}{item.inherited ? ` · ${t("inherited", { name: sourceNames[item.source_task_id ?? ""] ?? t("sourceTask") })}` : ""}</span></li>)}</ul></section> : null)}
        {refs.data?.truncated && <p className="text-xs text-muted-foreground">{t("refsTruncated")}</p>}
        <section className="border-t pt-3"><h4 className="mb-1 text-xs font-medium text-muted-foreground">{t("originalData")}</h4><pre className="max-w-full whitespace-pre-wrap break-all rounded-md border bg-muted/50 p-3 text-xs">{JSON.stringify(selected, null, 2)}</pre></section>
      </div></ScrollArea>}
    </SheetContent></Sheet>
  </div>
}
