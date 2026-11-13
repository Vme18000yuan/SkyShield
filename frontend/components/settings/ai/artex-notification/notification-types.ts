export interface NotificationFilter {
  min_severity?: string
  task_ids?: number[]
  asset_ids?: number[]
  vulnclass_include?: string[]
  vulnclass_exclude?: string[]
  on_status_change?: boolean
}

export interface NotificationChannel {
  id: string
  name: string
  kind: string
  enabled: boolean
  mode: "realtime" | "digest"
  config: Record<string, unknown>
  filter: NotificationFilter
  rate_per_min: number
  version: number
  created_at: string
  updated_at: string
  secret_keys: string[]
}

export interface NotificationMeta {
  kinds: Array<{ kind: string; default_rate_per_min: number; secret_keys: string[] }>
  enabled: boolean
  public_base_url: string
  digest_interval_min: string
  defaults: { digest_interval_min: number }
  stats: { channels: number; channels_on: number; pending: number; failed: number; sent_today: number; backlog_age_ms: number }
}

export interface NotificationDelivery {
  id: string
  finding_id: string
  task_id: string
  event_kind: string
  channel_id: string
  channel_name: string
  channel_kind: string
  state: "pending" | "sending" | "sent" | "failed" | "skipped"
  attempts: number
  last_error: string
  created_at: string
  sent_at?: string
  next_attempt_at: string
  title: string
  severity: string
}
