"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { AlertDialog, AlertDialogClose, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useFofa } from "@/hooks/use-fofa"
import { getErrorMessage, getErrorResponseData } from "@/lib/response-parser"
import type { FofaSettings } from "@/types/fofa.types"

export function FofaSettingsDialog({ settings, onSaved }: { settings: FofaSettings; onSaved: () => void }) {
  const t = useTranslations("spaceSearch")
  const { save, clear, test } = useFofa()
  const [open, setOpen] = React.useState(false)
  const [confirmClear, setConfirmClear] = React.useState(false)
  const [email, setEmail] = React.useState("")
  const [apiKey, setAPIKey] = React.useState("")
  const fail = (error: unknown) => toast.error(getErrorMessage(getErrorResponseData(error)) || t("error"))
  const submit = async () => {
    try { await save.mutateAsync({ email, apiKey }); setAPIKey(""); setOpen(false); onSaved(); toast.success(t("saved")) }
    catch (error) { fail(error) }
  }
  return <>
    <Button variant="outline" onClick={() => { setEmail(settings.email); setAPIKey(""); save.reset(); setOpen(true) }}>{t("settings")}</Button>
    <Dialog open={open} onOpenChange={value => { setOpen(value); if (!value) setAPIKey("") }}>
      <DialogContent><DialogHeader><DialogTitle>{t("settings")}</DialogTitle><DialogDescription>{t("settingsDescription")}</DialogDescription></DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={event => { event.preventDefault(); void submit() }}>
          <div className="flex flex-col gap-1"><Label htmlFor="fofa-email">{t("email")}</Label><Input id="fofa-email" type="email" autoComplete="off" maxLength={254} required value={email} onChange={event => setEmail(event.target.value)} /></div>
          <div className="flex flex-col gap-1"><Label htmlFor="fofa-api-key">{t("apiKey")}</Label><Input id="fofa-api-key" type="password" autoComplete="new-password" maxLength={4096} required={!settings.configured} value={apiKey} onChange={event => setAPIKey(event.target.value)} placeholder={settings.configured ? t("keepKey") : undefined} /></div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" disabled={!settings.configured || test.isPending || save.isPending} onClick={async () => { try { await test.mutateAsync(); toast.success(t("testSuccess")) } catch (error) { fail(error) } }}>{t("testSaved")}</Button>
            <Button type="button" variant="outline" disabled={!settings.configured || clear.isPending || save.isPending} onClick={() => setConfirmClear(true)}>{t("clearSettings")}</Button>
          </div>
          <DialogFooter><Button type="submit" disabled={save.isPending || clear.isPending}>{t("save")}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
    <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}><AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{t("clearSettings")}</AlertDialogTitle><AlertDialogDescription>{t("clearDescription")}</AlertDialogDescription></AlertDialogHeader>
      <AlertDialogFooter><AlertDialogClose render={<Button variant="outline" />}>{t("cancel")}</AlertDialogClose><Button variant="destructive" disabled={clear.isPending} onClick={async () => { try { await clear.mutateAsync(); setConfirmClear(false); setOpen(false); onSaved(); toast.success(t("cleared")) } catch (error) { fail(error) } }}>{t("confirmClear")}</Button></AlertDialogFooter>
    </AlertDialogContent></AlertDialog>
  </>
}
