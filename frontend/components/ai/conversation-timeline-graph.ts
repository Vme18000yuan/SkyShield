import { MarkerType, Position, type Edge, type Node } from "@xyflow/react"
import type { ConversationTimeline, TimelineFinding, TimelineSection } from "@/types/conversation-exploration.types"
import { explorationLayout } from "./conversation-graph-layout"
import { timelineTaskTitle } from "./conversation-timeline-labels"

export type TimelineMapProps = {
  graph: ConversationTimeline
  from: number
  choose: (step: number, revealSteps?: boolean) => void
  goal: () => void
  outcome: () => void
  receipts: (finding: TimelineFinding) => void
}

type Translator = {
  (key: string, values?: Record<string, string | number>): string
  has: (key: string) => boolean
}

export const stages = {
  goal: { color: "var(--chart-2)", label: "request" },
  section: { color: "var(--chart-1)", label: "map.section" },
  finding: { color: "var(--destructive)", label: "map.finding" },
  end: { color: "var(--chart-3)", label: "runOutcome" },
} as const

export const relations = {
  contains: { color: "var(--chart-2)", dash: undefined, label: "map.contains" },
  registration: { color: "var(--destructive)", dash: undefined, label: "map.registration" },
  citation: { color: "var(--chart-4)", dash: "4 4", label: "citation" },
  summary: { color: "var(--chart-3)", dash: "4 4", label: "map.summary" },
} as const

type Badge = { text: string; tone: "neutral" | "live" | "ok" | "attention" }
type StageData = {
  kind: keyof typeof stages
  title: string
  label?: string
  meta?: string
  context?: string
  badge?: Badge
  selected?: boolean
  ordinal?: number
  cited?: boolean
  open: () => void
}
export type MapNode = Node<StageData, "stage">
type MapRelation = keyof typeof relations

export const nodeWidth = 244
export const nodeHeight = 140
export const sectionWindowSize = 6

function sectionBadge(section: TimelineSection, t: Translator): Badge | undefined {
  for (const key of ["failed", "interrupted", "pending", "missing"] as const) {
    if (section[key] > 0) return { text: `${t(`totals.${key}`)} ${section[key]}`, tone: key === "pending" ? "live" : "attention" }
  }
  if (section.steps > 0 && section.returned === section.steps) return { text: t("states.succeeded"), tone: "ok" }
}

function outcomeBadge(run: ConversationTimeline["run"], t: Translator): Badge | undefined {
  if (!run) return
  const key = `states.${run.status}`
  let tone: Badge["tone"] = "neutral"
  if (run.running) tone = "live"
  else if (["completed", "finished", "succeeded"].includes(run.status)) tone = "ok"
  else if (["failed", "timeout", "exhausted", "interrupted"].includes(run.status)) tone = "attention"
  return { text: t.has(key) ? t(key) : run.status, tone }
}

export function timelineDiagram({ graph, from, choose, goal, outcome, receipts }: TimelineMapProps, t: Translator) {
  const index = Math.max(0, graph.sections.findIndex(s => s.first <= from && from <= s.last))
  const offset = Math.floor(index / sectionWindowSize) * sectionWindowSize
  const sections = graph.sections.slice(offset, offset + sectionWindowSize)
  const focusId = graph.sections[index] ? `section-${graph.sections[index].first}` : "request"
  const nodes: MapNode[] = []
  const edges: Edge[] = []
  const usedRelations = new Set<MapRelation>()

  function add(id: string, data: StageData) {
    nodes.push({ id, type: "stage", position: { x: 0, y: 0 }, width: nodeWidth, height: nodeHeight,
      dragHandle: ".trace-drag-handle", sourcePosition: Position.Right, targetPosition: Position.Left, data })
  }

  function link(source: string, target: string, relation: MapRelation) {
    const { color, dash, label } = relations[relation]
    usedRelations.add(relation)
    edges.push({
      id: `${source}:${relation}:${target}`, source, target, type: "default",
      sourceHandle: "out", targetHandle: relation === "citation" ? "citation" : "in",
      label: t(label), labelShowBg: false, labelStyle: { fill: color, fontSize: 10, fontWeight: 500 },
      style: { stroke: color, strokeWidth: 1.5, strokeDasharray: dash, strokeOpacity: 0.65 },
      markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color },
    })
  }

  add("request", {
    kind: "goal", title: graph.request.text || t("noSummary"), open: goal,
    label: graph.request.activityId ? t("sourceRecord") : undefined, meta: graph.request.activityId ? `#${graph.request.activityId}` : undefined,
  })
  for (const [row, section] of sections.entries()) {
    const id = `section-${section.first}`
    add(id, {
      kind: "section", title: timelineTaskTitle(t, section.purpose), ordinal: offset + row + 1,
      label: t(`taskPurpose.sources.${section.purpose?.source ?? "unknown"}`),
      context: section.purpose?.subject,
      meta: t("range", { first: section.first, last: section.last }),
      badge: sectionBadge(section, t), selected: section.first <= from && from <= section.last,
      open: () => choose(section.first),
    })
    link("request", id, "contains")
  }
  // Keep every loaded receipt, including multiple receipts from one task and
  // receipts without a recorded source. A missing link is never synthesized.
  const findings = new Map<string, TimelineFinding>()
  for (const section of sections) if (section.firstFinding) findings.set(section.firstFinding.receiptId, section.firstFinding)
  for (const finding of graph.findings) {
    if (finding.step === undefined || sections.some(section => section.first <= finding.step! && finding.step! <= section.last)) {
      findings.set(finding.receiptId, finding)
    }
  }
  for (const finding of findings.values()) {
    const target = `receipt-${finding.receiptId}`
    const source = sections.find(section => finding.step !== undefined && section.first <= finding.step && finding.step <= section.last)
    const sources = new Set<string>()
    for (const ref of finding.evidenceSources ?? []) {
      const origin = sections.find(section => section.first <= ref.step && ref.step <= section.last)
      if (origin) sources.add(`section-${origin.first}`)
    }
    add(target, {
      kind: "finding", title: finding.title, cited: sources.size > 0, open: () => receipts(finding),
      label: finding.step ? t("stepNumber", { number: finding.step }) : t("unlinked"), meta: `#${finding.vulnerabilityId}`,
      badge: { text: t(finding.reviewed ? "states.reviewed" : "states.pending_review"), tone: finding.reviewed ? "ok" : "neutral" },
    })
    if (source) link(`section-${source.first}`, target, "registration")
    for (const origin of sources) link(origin, target, "citation")
  }
  add("outcome", {
    kind: "end", title: graph.conclusion.text || t(graph.run?.running ? "inProgress" : "noConclusion"), open: outcome,
    label: graph.conclusion.activityId ? t("sourceRecord") : undefined, meta: graph.conclusion.activityId ? `#${graph.conclusion.activityId}` : undefined,
    badge: outcomeBadge(graph.run, t),
  })
  link("request", "outcome", "summary")
  const layout = explorationLayout(nodes.map(node => node.id), edges)
  for (const node of nodes) node.position = layout.get(node.id)!
  return { nodes, edges, usedRelations, offset, focusId }
}
