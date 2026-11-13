// Adapted from ARTEX, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { RotateCcwIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { aiFindingRetestService, type AIFindingRetest } from "@/services/ai-finding-retest.service"

export function FindingRetestDialog({ findingId, findingName, onClose, onStarted }: {
  findingId: string
  findingName?: string
  onClose: () => void
  onStarted: (retest: AIFindingRetest) => void
}) {
  const t = useTranslations("vulnerabilities.detail")
  const notesId = React.useId()
  const [notes, setNotes] = React.useState("")
  const [submitting, setSubmitting] = React.useState(false)
  const submitLock = React.useRef(false)
  async function start() {
    if (submitLock.current) return
    submitLock.current = true; setSubmitting(true)
    try {
      const result = await aiFindingRetestService.start(findingId, notes.trim())
      onStarted(result.retest); onClose()
      toast.success(result.created ? t("retestStarted") : t("retestAlreadyRunning"))
    } catch {
      toast.error(t("retestStartFailed"))
    } finally {
      submitLock.current = false; setSubmitting(false)
    }
  }
  return <Dialog open onOpenChange={open => { if (!open && !submitLock.current) onClose() }}>
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{t("retestDialogTitle", { id: findingId })}</DialogTitle>
        <DialogDescription>{findingName ? <span className="mb-2 block break-words">{findingName}</span> : null}{t("retestDescription")}</DialogDescription>
      </DialogHeader>
      <FieldGroup><Field data-disabled={submitting}>
        <FieldLabel htmlFor={notesId}>{t("retestNotes")}</FieldLabel>
        <Textarea id={notesId} value={notes} maxLength={4000} rows={4} disabled={submitting} onChange={event => setNotes(event.target.value)} placeholder={t("retestNotesPlaceholder")} />
        <FieldDescription>{t("retestNotesHint")}</FieldDescription>
      </Field></FieldGroup>
      <DialogFooter>
        <Button variant="outline" disabled={submitting} onClick={onClose}>{t("retestCancel")}</Button>
        <Button disabled={submitting} onClick={() => void start()}>{submitting ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}{submitting ? t("retestStarting") : t("retestStart")}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
}
