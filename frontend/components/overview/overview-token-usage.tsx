// Inspired by ARTEX's dashboard token statistics; AGPL-3.0-only.
"use client"

import * as React from "react"
import Link from "next/link"
import { useLocale, useTranslations } from "next-intl"
import { ZapIcon } from "lucide-react"
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"

import { OverviewSectionPanel } from "@/components/overview/overview-section-layouts"
import { OverviewTokenUsageSkeleton } from "@/components/overview/overview-token-usage-skeleton"
import {
  OVERVIEW_TOKEN_DAYS,
  OVERVIEW_TOKEN_DEFAULT_DAYS,
  buildTokenDays,
  buildTokenProfiles,
  emptyTokenMetrics,
} from "@/components/overview/overview-token-usage-data"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { useAIUsageModelOptions, useAIUsageSummary } from "@/hooks/use-ai-usage"
import { textRole } from "@/lib/typography"
import { cn } from "@/lib/utils"

const TOKEN_COLORS = { input: "hsl(217 91% 60%)", cacheRead: "hsl(160 60% 45%)", output: "hsl(263 70% 60%)" }

function formatCompact(value: number, locale: string) {
  return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 }).format(value)
}

export function OverviewTokenUsage() {
  const t = useTranslations("overview.tokenUsage")
  const locale = useLocale()
  const auth = useAuth()
  const user = auth.data?.user?.id
  const [days, setDays] = React.useState<number>(OVERVIEW_TOKEN_DEFAULT_DAYS)
  const [selected, setSelected] = React.useState("all")
  const usage = useAIUsageSummary(days)
  const models = useAIUsageModelOptions()
  const profiles = React.useMemo(
    () => buildTokenProfiles(models.data ?? [], usage.data?.profiles ?? [], t("environment")),
    [models.data, usage.data?.profiles, t],
  )
  const daily = React.useMemo(
    () => buildTokenDays(usage.data?.daily ?? [], selected, days),
    [usage.data?.daily, selected, days],
  )
  const title = <span className="flex items-center gap-2"><ZapIcon aria-hidden="true" className="size-4 text-muted-foreground" />{t("title")}</span>

  if (!user && !auth.isLoading) {
    return <OverviewSectionPanel title={title} className="h-full"><p className={textRole.bodySubtle}>{t("signIn")}</p></OverviewSectionPanel>
  }
  if (usage.isError) {
    return <OverviewSectionPanel title={title} className="h-full"><div role="alert" className="flex flex-wrap items-center gap-2 text-sm text-destructive">{t("loadError")}<Button size="sm" variant="outline" onClick={() => void usage.refetch()}>{t("retry")}</Button></div></OverviewSectionPanel>
  }
  if (!usage.data) return <OverviewTokenUsageSkeleton />

  const metrics = selected === "all" ? usage.data.totals : profiles.find(profile => profile.key === selected)?.metrics ?? emptyTokenMetrics()
  const total = metrics.input_tokens + metrics.output_tokens
  const readPart = Math.min(metrics.input_tokens, metrics.cache_read_tokens)
  const parts = [
    { key: "inputUncached", value: Math.max(0, metrics.input_tokens - readPart), color: TOKEN_COLORS.input },
    { key: "cacheRead", value: readPart, color: TOKEN_COLORS.cacheRead },
    { key: "output", value: metrics.output_tokens, color: TOKEN_COLORS.output },
  ] as const
  const cacheRate = metrics.input_tokens > 0 && metrics.cache_read_tokens <= metrics.input_tokens
    ? `${Math.round(metrics.cache_read_tokens / metrics.input_tokens * 100)}%`
    : "—"
  const hasDaily = daily.some(day => day.input > 0 || day.cacheRead > 0 || day.output > 0)
  const profileButton = (active: boolean) => cn("shrink-0 rounded-md px-2.5 py-1 text-xs font-medium transition-colors", active ? "bg-foreground text-background" : "bg-muted/40 text-muted-foreground hover:text-foreground")

  return (
    <OverviewSectionPanel title={title} className="h-full">
      <div className="space-y-4">
        <div role="group" aria-label={t("profileFilter")} className="flex max-w-full gap-1 overflow-x-auto pb-1">
          <button type="button" aria-pressed={selected === "all"} onClick={() => setSelected("all")} className={profileButton(selected === "all")}>{t("all")}</button>
          {profiles.map(profile => <button key={profile.key} type="button" aria-pressed={selected === profile.key} onClick={() => setSelected(profile.key)} className={profileButton(selected === profile.key)}>
            {profile.label}{profile.isDefault ? ` · ${t("default")}` : profile.historical ? ` · ${t("historical")}` : ""}
          </button>)}
        </div>
        {models.isError && <div role="alert" className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">{t("profilesError")}<button type="button" className="text-primary underline" onClick={() => void models.refetch()}>{t("retry")}</button></div>}
        <div className="grid min-w-0 gap-5 @2xl/panel:grid-cols-[minmax(140px,0.7fr)_minmax(0,1.3fr)]">
          <div className="min-w-0 space-y-3">
            <div><p className={textRole.caption}>{t("total")}</p><p className="mt-0.5 text-2xl font-semibold tabular-nums" title={total.toLocaleString(locale)}>{formatCompact(total, locale)}</p><p className={textRole.caption}>{t("calls", { count: metrics.calls })}</p></div>
            {parts.map(part => <div key={part.key}>
              <div className="mb-1 flex items-center justify-between gap-2 text-xs"><span className="text-muted-foreground">{t(part.key)}</span><span className="tabular-nums" title={part.value.toLocaleString(locale)}>{formatCompact(part.value, locale)}</span></div>
              <div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full" style={{ width: `${total > 0 ? part.value / total * 100 : 0}%`, backgroundColor: part.color }} /></div>
            </div>)}
            <div className="flex items-center justify-between gap-2 text-xs"><span className="text-muted-foreground">{t("cacheRate")}</span><span className="font-medium tabular-nums">{cacheRate}</span></div>
            <p className="text-xs text-muted-foreground">{t("inputDetail", { input: metrics.input_tokens.toLocaleString(locale), write: metrics.cache_write_tokens.toLocaleString(locale) })}</p>
          </div>
          <div className="min-w-0 space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-medium">{t("daily")} <span className="font-normal text-muted-foreground">{t("utc")}</span></span>
              <div role="group" aria-label={t("period")} className="flex flex-wrap gap-1 rounded-md border p-0.5">
                {OVERVIEW_TOKEN_DAYS.map(value => <button key={value} type="button" aria-pressed={days === value} onClick={() => setDays(value)} className={cn("rounded px-2 py-1 text-xs", days === value ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground")}>{t("days", { count: value })}</button>)}
              </div>
            </div>
            {hasDaily ? <div className="h-44 w-full min-w-0" role="img" aria-label={t("chartLabel", { count: days })}>
              <ResponsiveContainer width="100%" height="100%"><BarChart data={daily} margin={{ top: 4, right: 2, bottom: 0, left: 0 }} maxBarSize={36}>
                <CartesianGrid vertical={false} stroke="var(--border)" strokeOpacity={0.5} />
                <XAxis dataKey="date" tickFormatter={value => String(value).slice(5)} tick={{ fontSize: 10, fill: "var(--muted-foreground)" }} tickLine={false} axisLine={false} minTickGap={24} />
                <YAxis tickFormatter={value => formatCompact(Number(value), locale)} tick={{ fontSize: 10, fill: "var(--muted-foreground)" }} tickLine={false} axisLine={false} width={42} />
                <Tooltip contentStyle={{ background: "var(--card)", border: "1px solid var(--border)", borderRadius: 8 }} formatter={value => Number(value ?? 0).toLocaleString(locale)} />
                <Bar dataKey="input" name={t("inputUncached")} stackId="tokens" fill={TOKEN_COLORS.input} isAnimationActive={false} />
                <Bar dataKey="cacheRead" name={t("cacheRead")} stackId="tokens" fill={TOKEN_COLORS.cacheRead} isAnimationActive={false} />
                <Bar dataKey="output" name={t("output")} stackId="tokens" fill={TOKEN_COLORS.output} isAnimationActive={false} />
              </BarChart></ResponsiveContainer>
            </div> : <div className="flex h-44 items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground">{t("empty")}</div>}
            <p className="text-xs text-muted-foreground">{t("recordingNote")} <Link href="/settings/ai/llm/" className="text-primary hover:underline">{t("settings")}</Link></p>
          </div>
        </div>
      </div>
    </OverviewSectionPanel>
  )
}
