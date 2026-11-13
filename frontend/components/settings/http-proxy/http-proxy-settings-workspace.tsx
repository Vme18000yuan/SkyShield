"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"

import { PageHeader } from "@/components/common/page-header"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_FULL_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { useClearHTTPProxySettings, useHTTPProxySettings, useUpdateHTTPProxySettings } from "@/hooks/use-http-proxy-settings"
import { normalizeError } from "@/lib/errors/normalize-error"

export function HTTPProxySettingsWorkspace() {
  const t = useTranslations("settings.httpProxy")
  const settings = useHTTPProxySettings()
  const update = useUpdateHTTPProxySettings()
  const clear = useClearHTTPProxySettings()
  const [enabled, setEnabled] = React.useState(false)
  const [proxyUrl, setProxyUrl] = React.useState("")
  const [validationError, setValidationError] = React.useState("")
  const busy = update.isPending || clear.isPending

  React.useEffect(() => {
    if (settings.data) setEnabled(settings.data.enabled)
  }, [settings.data])

  const save = async () => {
    setValidationError("")
    if (enabled && !proxyUrl.trim() && !settings.data?.configured) {
      setValidationError(t("proxyUrlRequired"))
      return
    }
    try {
      await update.mutateAsync({ enabled, proxyUrl: proxyUrl.trim() })
      setProxyUrl("")
      toast.success(t("saveSuccess"))
    } catch {
      toast.error(t("saveError"))
    }
  }

  const remove = async () => {
    setValidationError("")
    try {
      const result = await clear.mutateAsync()
      setEnabled(result.enabled)
      setProxyUrl("")
      toast.success(t("clearSuccess"))
    } catch {
      toast.error(t("clearError"))
    }
  }

  if (settings.error) {
    return <AppErrorState error={normalizeError(settings.error, { notFoundKind: "unexpected-error" })} onRetry={() => void settings.refetch()} variant="page" />
  }

  return (
    <div className={`${COMPACT_FULL_PAGE_SHELL_CLASS} flex flex-col`}>
      <PageHeader code="SYS-05" title={t("pageTitle")} description={t("pageDescription")} />
      <div className={`${COMPACT_CONTENT_GUTTER_CLASS} space-y-3 pb-4`}>
        {settings.isPending && !settings.data ? (
          <div className="space-y-3" aria-label={t("loading")}>
            <Skeleton className="h-28 w-full" />
            <Skeleton className="h-36 w-full" />
          </div>
        ) : settings.data ? (
          <>
            <Card>
              <CardHeader className="flex flex-row items-center justify-between gap-4 border-b px-4 py-3">
                <div className="min-w-0 space-y-1">
                  <CardTitle className="text-base">{t("cardTitle")}</CardTitle>
                  <CardDescription>{t("cardDescription")}</CardDescription>
                </div>
                <div className="flex shrink-0 items-center gap-3">
                  <Badge size="compact" variant={enabled ? "success" : "outline"}>
                    {enabled ? t("enabled") : t("disabled")}
                  </Badge>
                  <Switch id="http-proxy-enabled" checked={enabled} disabled={busy} aria-label={t("enabledLabel")} onCheckedChange={setEnabled} />
                </div>
              </CardHeader>
              <CardContent className="grid gap-4 px-4 py-4">
                <div className="grid gap-2">
                  <Label htmlFor="http-proxy-url">{t("proxyUrlLabel")}</Label>
                  <Input
                    id="http-proxy-url"
                    type="url"
                    autoComplete="url"
                    autoCapitalize="none"
                    spellCheck={false}
                    placeholder={t("proxyUrlPlaceholder")}
                    value={proxyUrl}
                    disabled={busy}
                    onChange={(event) => setProxyUrl(event.target.value)}
                  />
                  <p className="text-sm text-muted-foreground">{t("proxyUrlHelp")}</p>
                  {settings.data.configured ? (
                    <p className="text-sm text-muted-foreground">{t("currentAddress", { address: settings.data.proxyAddress })}</p>
                  ) : (
                    <p className="text-sm text-muted-foreground">{t("notConfigured")}</p>
                  )}
                  {validationError ? <p role="alert" className="text-sm text-destructive">{validationError}</p> : null}
                </div>
                <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">{t("scopeDescription")}</div>
                <div className="flex flex-wrap justify-end gap-2">
                  {settings.data.configured ? (
                    <Button type="button" variant="outline" disabled={busy} onClick={() => void remove()}>
                      {clear.isPending ? t("clearing") : t("clear")}
                    </Button>
                  ) : null}
                  <Button type="button" disabled={busy} onClick={() => void save()}>
                    {update.isPending ? t("saving") : t("save")}
                  </Button>
                </div>
              </CardContent>
            </Card>
          </>
        ) : null}
      </div>
    </div>
  )
}
