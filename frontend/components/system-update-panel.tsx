"use client"

import { useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import axios from "axios"
import { api } from "@/lib/api-client"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogClose } from "@/components/ui/alert-dialog"

type UpdateView = {
  currentVersion: string
  mode: "local" | "docker"
  updaterReady: boolean
  hasUpdate: boolean
  eligible: boolean
  eligibilityReason?: string
  checkState: string
  message?: string
  manifestDigest?: string
  checkedAt?: string
  candidate?: { version: string; revision: string; notes: string; publishedAt: string }
  operation?: { id: string; version: string; status: string; message: string; updatedAt: string } | null
}
const active = new Set(["queued", "preparing", "backing_up", "updating", "restarting", "verifying"])
const key = ["skyshield", "deployment-update"]

function errorText(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    if (error.response?.status === 403) return "仅系统管理员可以检查和安装更新 / Administrator access required"
    return error.response?.data?.error?.message || error.response?.data?.message || fallback
  }
  return fallback
}

export function SystemUpdatePanel({ enabled }: { enabled: boolean }) {
  const t = useTranslations("systemUpdate")
  const client = useQueryClient()
  const [confirm, setConfirm] = useState(false)
  const [acknowledged, setAcknowledged] = useState(false)
  const request = useRef<{ digest: string; id: string } | null>(null)
  const query = useQuery<UpdateView>({
    queryKey: key,
    queryFn: async () => (await api.get<UpdateView>("/deploymentUpdate")).data,
    enabled, retry: false,
    refetchInterval: (state) => {
      if (!enabled || (axios.isAxiosError(state.state.error) && state.state.error.response?.status === 403)) return false
      return active.has(state.state.data?.operation?.status ?? "") ? 3000 : 30000
    },
  })
  const check = useMutation({
    mutationFn: async () => (await api.post<UpdateView>("/system:checkForUpdates", {}, { timeout: 45000 })).data,
    onSuccess: (data) => client.setQueryData(key, data),
  })
  const install = useMutation({
    mutationFn: async (digest: string) => {
      if (request.current?.digest !== digest) request.current = { digest, id: crypto.randomUUID() }
      return (await api.post<UpdateView>("/deploymentUpdate:apply", {
        requestId: request.current.id, manifestDigest: digest, confirmed: true,
      }, { timeout: 45000 })).data
    },
    onSuccess: (data) => { client.setQueryData(key, data); setConfirm(false); setAcknowledged(false) },
  })
  const view = query.data
  const running = !!view?.operation && active.has(view.operation.status)
  const failed = view?.operation?.status === "failed" || view?.operation?.status === "needs_recovery"
  const error = install.error || check.error || query.error
  return (
    <div className="space-y-3 rounded-lg border p-4" aria-live="polite">
      <div className="flex items-center justify-between text-sm"><span>{t("current")}</span><code>{view?.currentVersion || "—"}</code></div>
      {view && <p className="text-xs text-muted-foreground">{view.mode === "local" ? t("local") : t("docker")}</p>}
      {view?.candidate && <div className="space-y-2 border-t pt-3">
        <div className="flex justify-between text-sm"><span>{view.hasUpdate ? t("available") : t("latest")}</span><code>{view.candidate.version}</code></div>
        {view.candidate.notes && <p className="max-h-40 overflow-y-auto whitespace-pre-wrap text-xs text-muted-foreground">{view.candidate.notes}</p>}
      </div>}
      {view?.message && <p className="text-xs text-muted-foreground">{view.message}</p>}
      {view?.eligibilityReason && <p className="text-xs text-muted-foreground">{view.eligibilityReason}</p>}
      {view?.operation && <div className="space-y-1 border-t pt-3">
        <p className={failed ? "text-sm text-destructive" : "text-sm"}>{t.has(view.operation.status) ? t(view.operation.status) : view.operation.status} · {view.operation.version}</p>
        <p className="text-xs text-muted-foreground">{view.operation.message}</p>
        {running && query.isError && <p className="text-xs">{t("reconnecting")}</p>}
        {view.operation.status === "succeeded" && <Button size="sm" variant="outline" onClick={() => window.location.reload()}>{t("reload")}</Button>}
      </div>}
      {error && !running && <p className="text-sm text-destructive">{errorText(error, t("requestFailed"))}</p>}
      <div className="flex gap-2">
        <Button size="sm" variant="outline" loading={check.isPending} disabled={running || check.isPending || install.isPending} onClick={() => check.mutate()}>{t("check")}</Button>
        {view?.hasUpdate && <Button size="sm" disabled={!view.eligible || running || install.isPending} onClick={() => {
          if (view.operation && !active.has(view.operation.status)) request.current = null
          setConfirm(true); setAcknowledged(false)
        }}>{t("install")}</Button>}
      </div>
      <p className="text-xs text-muted-foreground">{t("automaticCheck")}</p>
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader><AlertDialogTitle>{t("confirmTitle")}</AlertDialogTitle><AlertDialogDescription>{t("confirmDescription")}</AlertDialogDescription></AlertDialogHeader>
          <label className="flex items-start gap-2 text-sm"><Checkbox checked={acknowledged} onCheckedChange={(value) => setAcknowledged(value === true)} />{t("acknowledge")}</label>
          <AlertDialogFooter>
            <AlertDialogClose variant="outline" disabled={install.isPending}>{t("cancel")}</AlertDialogClose>
            <Button loading={install.isPending} disabled={!acknowledged || install.isPending || !view?.manifestDigest || !view?.eligible} onClick={() => { if (view?.manifestDigest) install.mutate(view.manifestDigest) }}>{t("install")}</Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
