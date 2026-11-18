import type { TimelinePurpose, TimelineStep } from "@/types/conversation-exploration.types"

const knownTools = new Set(["Bash", "Read", "Write", "Edit", "LS", "Glob", "Grep", "TodoWrite", "report_vulnerability", "traffic_get"])
const actionKinds = new Set(["http", "script", "fileRead", "fileSearch", "directory"])

export function timelineCaption(t: (key: string) => string, step: Pick<TimelineStep, "action" | "actionKind" | "tool" | "subject">) {
  if (step.action) return step.action
  const name = step.actionKind && actionKinds.has(step.actionKind) ? t(`actions.${step.actionKind}`)
    : knownTools.has(step.tool) ? t(`toolNames.${step.tool}`) : step.tool
  return [name, step.subject].filter(Boolean).join(" · ") || t("noSummary")
}

export function timelineTaskTitle(t: (key: string, values?: { title: string }) => string, purpose?: TimelinePurpose) {
  if (purpose?.source === "receipt" && purpose.text) return t("taskPurpose.receiptTitle", { title: purpose.text })
  if (purpose?.text) return purpose.text
  if (purpose?.topics?.length) return t(`taskPurpose.topics.${purpose.topics[0]}`)
  return t("taskPurpose.unknownTitle")
}
