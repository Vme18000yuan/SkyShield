// Adapted from ARTEX's task coverage graph, AGPL-3.0-only.
"use client"

import * as React from "react"
import { Background, Controls, Handle, Position, ReactFlow, useNodesState, type Edge, type Node, type NodeProps } from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { MoreHorizontal } from "lucide-react"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import type { CoverageGraph, CoverageNode } from "@/types/asset-coverage.types"
import { COVERAGE_FOLD_LIMIT, COVERAGE_FOLD_STEP, coverageNodeLabel, coveragePositions, coverageVisible, type CoverageFold } from "./coverage-graph-layout"
import { coverageVisuals } from "./coverage-visuals"

type AssetVisual = Node<{ asset: CoverageNode }, "asset">
type FoldVisual = Node<{ fold: CoverageFold }, "fold">
type VisualNode = AssetVisual | FoldVisual

function AssetNode({ data }: NodeProps<AssetVisual>) {
  const t = useTranslations("aiTasks.coverageGraph")
  const item = data.asset, config = coverageVisuals[item.kind], Icon = config.icon
  return <div className={`flex min-w-40 max-w-56 items-center gap-2 rounded-lg border bg-card px-3 py-2 shadow-sm ${item.tested ? "ring-2 ring-emerald-500/70" : ""} ${!item.in_scope ? "border-dashed opacity-65" : ""}`}>
    <Handle type="target" position={Position.Left} className="!size-2 !border-0 !bg-muted-foreground" />
    <span className="flex size-7 shrink-0 items-center justify-center rounded-md text-white" style={{ backgroundColor: item.tested ? config.color : "#94a3b8" }}><Icon className="size-4" /></span>
    <span className="min-w-0"><span className="block text-[10px] text-muted-foreground">{t(`kinds.${item.kind}`)}</span><span className="block truncate text-xs font-medium" title={item.label}>{coverageNodeLabel(item)}</span></span>
    <Handle type="source" position={Position.Right} className="!size-2 !border-0 !bg-muted-foreground" />
  </div>
}

function FoldNode({ data }: NodeProps<FoldVisual>) {
  const t = useTranslations("aiTasks.coverageGraph")
  return <div className="flex min-w-40 items-center gap-2 rounded-lg border border-dashed bg-muted px-3 py-2 text-xs text-muted-foreground shadow-sm"><Handle type="target" position={Position.Left} className="!size-2 !border-0 !bg-muted-foreground" /><MoreHorizontal className="size-5 shrink-0" /><span>{t("foldCount", { count: data.fold.hidden.length, kind: t(`kinds.${data.fold.kind}`) })}</span></div>
}
const nodeTypes = { asset: AssetNode, fold: FoldNode }

export function CoverageTopology({ graph, onSelect }: { graph: CoverageGraph; onSelect: (node: CoverageNode) => void }) {
  const t = useTranslations("aiTasks.coverageGraph")
  const [expanded, setExpanded] = React.useState<Map<string, number>>(() => new Map())
  const [selectedFoldId, setSelectedFoldId] = React.useState<string | null>(null)
  const view = React.useMemo(() => coverageVisible(graph, expanded), [graph, expanded])
  const layout = React.useMemo(() => {
    const keys = [...view.nodes.map(asset => asset.key), ...view.folds.map(fold => fold.key)]
    const layers = new Map<string, number>()
    for (const asset of view.nodes) layers.set(asset.key, coverageVisuals[asset.kind].layer)
    for (const fold of view.folds) layers.set(fold.key, coverageVisuals[fold.kind].layer)
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
  const selectedFold = view.folds.find(fold => fold.key === selectedFoldId) ?? null
  React.useEffect(() => { if (selectedFoldId && !selectedFold) setSelectedFoldId(null) }, [selectedFoldId, selectedFold])
  const showMore = (groupId: string) => setExpanded(previous => {
    const next = new Map(previous)
    next.set(groupId, (next.get(groupId) ?? COVERAGE_FOLD_LIMIT) + COVERAGE_FOLD_STEP)
    return next
  })
  return <>
    <div className="h-[68vh] min-h-96 overflow-hidden rounded-lg border bg-muted/20" data-slot="coverage-topology">
      <ReactFlow nodes={nodes} edges={layout.edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange} onNodeClick={(_, node) => {
        if (node.type === "fold") setSelectedFoldId(node.id)
        else if (node.type === "asset") { setSelectedFoldId(null); onSelect(node.data.asset) }
      }} fitView minZoom={0.1} maxZoom={2} nodesConnectable={false} elementsSelectable><Background /><Controls /></ReactFlow>
    </div>
    <Sheet open={!!selectedFold} onOpenChange={open => { if (!open) setSelectedFoldId(null) }}><SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md">
      {selectedFold && <><SheetHeader className="border-b p-4"><SheetTitle>{t("hiddenAssets", { kind: t(`kinds.${selectedFold.kind}`), count: selectedFold.hidden.length })}</SheetTitle><SheetDescription>{t("foldDescription")}</SheetDescription></SheetHeader>
        <ScrollArea className="min-h-0 flex-1"><div className="flex flex-col gap-1 p-3">{selectedFold.hidden.map(asset => <button key={asset.key} type="button" onClick={() => { setSelectedFoldId(null); onSelect(asset) }} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent"><span className={`size-2 shrink-0 rounded-full ${asset.tested ? "bg-emerald-500" : "bg-muted-foreground"}`} /><span className="min-w-0 flex-1 truncate font-mono text-xs" title={asset.label}>{asset.label}</span></button>)}</div></ScrollArea>
        <div className="border-t p-3"><Button className="w-full" variant="outline" onClick={() => showMore(selectedFold.groupId)}>{t("showMoreCount", { count: COVERAGE_FOLD_STEP })}</Button></div></>}
    </SheetContent></Sheet>
  </>
}
