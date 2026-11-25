"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogClose } from "@/components/ui/alert-dialog"
import { useAIDataDeletion } from "@/hooks/use-ai-data-deletion"
import { textRole } from "@/lib/typography"
import type { AIDataDeletionRequest } from "@/types/ai-data-deletion.types"

export function AIDataDeleteButton({ request, disabled, onDeleted, clear = false, label, subject }: { request: AIDataDeletionRequest; disabled?: boolean; onDeleted?: () => void; clear?: boolean; label?: string; subject?: string }) {
  const t = useTranslations("aiDataDeletion")
  const [open, setOpen] = React.useState(false)
  const [confirmedRequest, setConfirmedRequest] = React.useState(request)
  const deletion = useAIDataDeletion()
  return <>
    <Button type="button" variant="outline" size="sm" disabled={disabled || deletion.isPending} onClick={() => { setConfirmedRequest(structuredClone(request)); setOpen(true) }}>{label ?? t(clear ? "clear" : "delete")}</Button>
    <AlertDialog open={open} onOpenChange={value => { if (!deletion.isPending) setOpen(value) }}>
      <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t("title")}</AlertDialogTitle><AlertDialogDescription>{t(confirmedRequest.kind === "task" ? "taskDescription" : confirmedRequest.kind === "executions" ? "executionDescription" : clear ? "clearTrafficDescription" : "trafficDescription")}</AlertDialogDescription></AlertDialogHeader>
        {subject && <p className="break-all">{subject}</p>}
        {confirmedRequest.kind === "executions" && <p className={textRole.bodySubtle}>{t("selectedCount", { count: confirmedRequest.ids.length })}</p>}
        <p className={textRole.bodySubtle}>{t("preserved")}</p>
        <AlertDialogFooter><AlertDialogClose variant="outline" disabled={deletion.isPending}>{t("cancel")}</AlertDialogClose><Button variant="destructive" disabled={deletion.isPending} onClick={() => deletion.mutate(confirmedRequest, { onSuccess: () => { setOpen(false); onDeleted?.() } })}>{t(deletion.isPending ? "deleting" : "confirm")}</Button></AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </>
}
