"use client"

import * as React from "react"
import { Background, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { BookOpenCheck, Flag, Layers3, Link2 } from "lucide-react"
import { useTranslations } from "next-intl"
import type { ConversationTimeline, TimelineFinding } from "@/types/conversation-exploration.types"
import { timelineTaskTitle } from "./conversation-timeline-labels"

type CardData = { kind: "goal" | "section" | "finding" | "end"; label: string; title: string; context?: string; meta?: string; selected?: boolean; open: () => void }
type MapNode = Node<CardData, "stage">
const icons = { goal: Flag, section: Layers3, finding: Link2, end: BookOpenCheck }
const cardWidth = 228
const spacing = 255
const windowSize = 12
const zoom = .96

function Stage({ data }: NodeProps<MapNode>) {
  const Icon = icons[data.kind]
  return <div className="relative" style={{width: cardWidth}}>
    <Handle type="target" position={Position.Left} className="opacity-0" />
    <Handle type="target" position={Position.Top} id="evidence-in" className="opacity-0" />
    <button type="button" onClick={data.open} aria-pressed={data.selected} className={`flex h-[100px] w-full flex-col gap-1 rounded-lg border bg-card px-3 py-2 text-left shadow-sm focus-visible:ring-2 focus-visible:ring-ring ${data.selected ? "border-primary ring-1 ring-primary/40" : data.kind === "finding" ? "border-amber-500/60" : "border-border"}`}>
      <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground"><Icon className="size-3.5" />{data.label}</span>
      <strong className="line-clamp-2 break-words text-[13px] font-semibold leading-4" title={data.title}>{data.title}</strong>
      {data.context && <span className="block max-w-full truncate text-[11px] text-muted-foreground" title={data.context}>{data.context}</span>}
      {data.meta && <span className="mt-auto block max-w-full truncate text-[10px] text-muted-foreground">{data.meta}</span>}
    </button>
    <Handle type="source" position={Position.Right} className="opacity-0" />
    <Handle type="source" position={Position.Bottom} id="evidence-out" className="opacity-0" />
  </div>
}
const nodeTypes = { stage: Stage }

export function TimelineMap({ graph, from, choose, goal, outcome, receipts }: { graph: ConversationTimeline; from: number; choose: (n: number) => void; goal: () => void; outcome: () => void; receipts: (finding: TimelineFinding) => void }) {
  const t = useTranslations("aiChat.exploration")
  const scroller = React.useRef<HTMLDivElement>(null)
  const currentIndex = Math.max(0, graph.sections.findIndex(s => s.first <= from && s.last >= from))
  const offset = Math.floor(currentIndex / windowSize) * windowSize
  const visibleSections = React.useMemo(() => graph.sections.slice(offset, offset + windowSize), [graph.sections, offset])
  const column = currentIndex - offset + 1
  React.useEffect(() => {
    const el = scroller.current
    if (!el || !column) return
    const left = 14 + column * spacing * zoom
    if (left < el.scrollLeft || left + cardWidth * zoom > el.scrollLeft + el.clientWidth) el.scrollLeft = Math.max(0, left - 28)
  }, [column, offset])
  const { nodes, edges } = React.useMemo(() => {
    const nodes: MapNode[] = []
    const edges: Edge[] = []
    const add = (id: string, column: number, y: number, data: CardData) => nodes.push({ id, type: "stage", position: { x: column * spacing, y }, width: cardWidth, height: 100, data })
    const link = (source: string, target: string, evidence = false) => edges.push({
      id: `${source}:${target}`, source, target, type: "smoothstep",
      sourceHandle: evidence ? "evidence-out" : undefined, targetHandle: evidence ? "evidence-in" : undefined,
      style: { stroke: evidence ? "#b7791f" : "#64748b", strokeWidth: 1.5, strokeDasharray: evidence ? undefined : "4 4" },
      markerEnd: { type: MarkerType.ArrowClosed, color: evidence ? "#b7791f" : "#64748b" },
    })
    add("request", 0, 0, offset ? { kind: "section", label: t("taskPurpose.navigation"), title: t("taskPurpose.earlier"), open: () => choose(graph.sections[offset - 1].first) }
      : { kind: "goal", label: t("request"), title: graph.request.text || t("noSummary"), open: goal })
    let previous = "request"
    visibleSections.forEach((section, i) => {
      const id = `section-${section.first}`
      const range = t("range", { first: section.first, last: section.last })
      const secondary = section.purpose?.topics?.[1]
      const context = section.purpose?.subject ?? (secondary ? t(`taskPurpose.topics.${secondary}`) : undefined)
      add(id, i + 1, 0, { kind: "section", label: t(`taskPurpose.sources.${section.purpose?.source ?? "unknown"}`), title: timelineTaskTitle(t, section.purpose), context, meta: section.findings ? `${range} · ${t("registrations", { count: section.findings })}` : range, selected: section.first <= from && section.last >= from, open: () => choose(section.first) })
      link(previous, id)
      previous = id
      const listed = graph.findings.filter(f => f.step && f.step >= section.first && f.step <= section.last)
      const visible = listed.length ? listed : section.firstFinding ? [section.firstFinding] : []
      // Each branch belongs to one receipt, including when several findings
      // share the same section. The receipt list exposes the complete page.
      for (const [row, f] of visible.slice(0, 2).entries()) {
        const target = `receipt-${f.receiptId}`
        add(target, i + 1, 118 + row * 112, { kind: "finding", label: t("stepNumber", { number: f.step! }), title: f.title, open: () => receipts(f) })
        const origins = new Set([id])
        for (const ref of f.evidenceSources ?? []) {
          const origin = visibleSections.find(s => s.first <= ref.step && ref.step <= s.last)
          if (origin) origins.add(`section-${origin.first}`)
        }
        for (const source of origins) link(source, target, true)
      }
    })
    const following = graph.sections[offset + windowSize]
    add("outcome", visibleSections.length + 1, 0, following ? { kind: "section", label: t("taskPurpose.navigation"), title: t("taskPurpose.later"), open: () => choose(following.first) }
      : { kind: "end", label: t("runOutcome"), title: t.has(`states.${graph.run?.status}`) ? t(`states.${graph.run?.status}`) : graph.run?.status || "", open: outcome })
    link(previous, "outcome")
    return { nodes, edges }
  }, [graph, from, choose, goal, outcome, receipts, t, visibleSections, offset])
  return <div className="overflow-hidden rounded-xl border bg-muted/25">
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b bg-card px-3 py-2 text-xs text-muted-foreground">
      <strong className="mr-auto font-medium text-foreground">{t("mainline")}</strong><span className="flex items-center gap-1.5"><i className="w-5 border-t border-dashed border-slate-500" />{t("order")}</span>
      {graph.findingCount > 0 && <span className="flex items-center gap-1.5"><i className="w-5 border-t border-amber-600" />{t("citation")}</span>}
    </div>
    <div ref={scroller} tabIndex={0} className="overflow-x-auto" aria-label={t("mainline")}>
      <div className={visibleSections.some(s => s.findings > 1) ? "h-[23rem]" : visibleSections.some(s => s.findings) ? "h-64" : "h-36"} style={{ minWidth: (visibleSections.length + 2) * spacing * zoom + 28 }}>
      <ReactFlow<MapNode> nodes={nodes} edges={edges} nodeTypes={nodeTypes} nodesDraggable={false} nodesConnectable={false} edgesFocusable={false} defaultViewport={{ x: 14, y: 16, zoom }} minZoom={.75} maxZoom={1.6} zoomOnScroll={false} preventScrolling={false} deleteKeyCode={null}>
        <Background gap={18} size={1} /><Controls showInteractive={false} orientation="horizontal" position="bottom-right" />
      </ReactFlow>
      </div>
    </div>
  </div>
}
