"use client"

import { useId } from "react"
import { useTranslations } from "next-intl"
import { Switch } from "@/components/ui/switch"

export function CdnVisibilityToggle({ shown, onChange }: { shown: boolean; onChange: (value: boolean) => void }) {
  const t = useTranslations("assetInventory")
  const id = useId()
  return <label htmlFor={id} className="inline-flex items-center gap-2 text-sm text-muted-foreground">
    <Switch id={id} checked={shown} onCheckedChange={onChange} />{t("showCdn")}
  </label>
}
