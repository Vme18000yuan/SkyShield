"use client"

import { useState, type ReactElement } from "react"
import { useTranslations } from "next-intl"
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog"
import { AboutDialogBranding, AboutDialogFooter, AboutDialogHeader, AboutDialogLinks, Separator } from "@/components/about-dialog-sections"

export function AboutDialog({ children }: { children: ReactElement }) {
  const [open, setOpen] = useState(false)
  const t = useTranslations("about")
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={children} />
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-md">
        <AboutDialogHeader t={t} />
        <div className="space-y-4">
          <AboutDialogBranding t={t} />
          <p className="text-center text-xs text-muted-foreground">{t("manualUpdate")}</p>
          <Separator />
          <AboutDialogLinks t={t} />
          <AboutDialogFooter t={t} />
        </div>
      </DialogContent>
    </Dialog>
  )
}
