import { MarkerType, Position, type Edge, type Node } from "@xyflow/react"
import type { ConversationTimeline, TimelineFinding, TimelineSection } from "@/types/conversation-exploration.types"
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
  goal: { color: "#12856d", label: "request" },
  section: { color: "#526bd0", label: "map.section" },
  finding: { color: "#be4d71", label: "map.finding" },
  end: { color: "#805cb1", label: "runOutcome" },
} as const

export const relations = {
  order: { color: "#7888c1", dash: "5 4", label: "order" },
  registration: { color: "#cc6986", dash: undefined, label: "map.registration" },
  citation: { color: "#b0833a", dash: "2 4", label: "citation" },
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
  cited?: boolean
  open: () => void
}
export type MapNode = Node<StageData, "stage">
type MapRelation = keyof typeof relations

const nodeWidth = 230
const nodeHeight = 120
const columnGap = 74
const branchGap = 52
export const sectionWindowSize = 12

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

  function add(id: string, x: number, y: number, data: StageData) {
    const targetPosition = data.kind === "finding" ? (y < 0 ? Position.Bottom : Position.Top) : Position.Left
    nodes.push({ id, type: "stage", position: { x, y }, width: nodeWidth, height: nodeHeight, targetPosition, data })
  }

  function link(source: string, target: string, relation: MapRelation, above = false) {
    const { color, dash } = relations[relation]
    usedRelations.add(relation)
    edges.push({
      id: `${source}:${relation}:${target}`, source, target, type: "default",
      sourceHandle: relation === "order" ? "next" : above ? "branch-up" : "branch-down",
      targetHandle: relation === "order" ? "previous" : relation === "citation" ? "citation" : "receipt",
      style: { stroke: color, strokeWidth: relation === "registration" ? 2 : 1.6, strokeDasharray: dash, strokeLinecap: "round" },
      markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color },
    })
  }

  let previous: string | undefined
  let x = 0
  function mainline(id: string, data: StageData) {
    add(id, x, 0, data)
    if (previous) link(previous, id, "order")
    previous = id
    x += nodeWidth + columnGap
  }

  if (!offset) mainline("request", {
    kind: "goal", title: graph.request.text || t("noSummary"), open: goal,
    label: graph.request.activityId ? t("sourceRecord") : undefined,
    meta: graph.request.activityId ? `#${graph.request.activityId}` : undefined,
  })
  for (const section of sections) {
    const id = `section-${section.first}`
    const branchX = x + columnGap
    const secondary = section.purpose?.topics?.[1]
    const range = t("range", { first: section.first, last: section.last })
    mainline(id, {
      kind: "section", title: timelineTaskTitle(t, section.purpose),
      label: t(`taskPurpose.sources.${section.purpose?.source ?? "unknown"}`),
      context: section.purpose?.subject || (secondary ? t(`taskPurpose.topics.${secondary}`) : undefined),
      meta: section.findings ? `${range} · ${t("registrations", { count: section.findings })}` : range,
      badge: sectionBadge(section, t), selected: section.first <= from && from <= section.last, open: () => choose(section.first),
    })

    const listed = graph.findings.filter(f => f.step !== undefined && section.first <= f.step && f.step <= section.last)
    const findings = listed.length ? listed : section.firstFinding ? [section.firstFinding] : []
    // One lane on each side keeps a second receipt out of the first one's path.
    // The receipt list under the map still exposes every registration.
    for (const [row, finding] of findings.slice(0, 2).entries()) {
      const target = `receipt-${finding.receiptId}`
      const above = row === 1
      const sources = new Set<string>()
      for (const ref of finding.evidenceSources ?? []) {
        const origin = sections.find(s => s.first <= ref.step && ref.step <= s.last)
        if (origin) sources.add(`section-${origin.first}`)
      }
      add(target, branchX, (above ? -1 : 1) * (nodeHeight + branchGap), {
        kind: "finding", title: finding.title, cited: sources.size > 0, open: () => receipts(finding),
        label: finding.step ? t("stepNumber", { number: finding.step }) : t("unlinked"), meta: `#${finding.vulnerabilityId}`,
        badge: { text: t(finding.reviewed ? "states.reviewed" : "states.pending_review"), tone: finding.reviewed ? "ok" : "neutral" },
      })
      link(id, target, "registration", above)
      for (const source of sources) link(source, target, "citation", above)
    }
  }
  if (offset + sections.length === graph.sections.length) {
    const run = graph.run
    mainline("outcome", {
      kind: "end", title: graph.conclusion.text || t(run?.running ? "inProgress" : "noConclusion"), open: outcome,
      label: graph.conclusion.activityId ? t("sourceRecord") : undefined,
      meta: graph.conclusion.activityId ? `#${graph.conclusion.activityId}` : undefined,
      badge: outcomeBadge(run, t),
    })
  }
  return { nodes, edges, usedRelations, offset, focusId }
}
