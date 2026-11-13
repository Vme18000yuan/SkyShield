import { api as client } from "@/lib/api-client"
import type { NotificationChannel, NotificationDelivery, NotificationMeta } from "./notification-types"

type Settings = { enabled: boolean; public_base_url: string; digest_interval_min: number }
type MetaResponse = { settings: Settings; kinds: NotificationMeta["kinds"]; stats: NotificationMeta["stats"] }

export const api = {
  async notifyMeta(): Promise<NotificationMeta> {
    const { data } = await client.get<MetaResponse>("/aiNotification/meta")
    return { ...data.settings, digest_interval_min: String(data.settings.digest_interval_min), kinds: data.kinds, stats: data.stats, defaults: { digest_interval_min: 30 } }
  },
  async notifyChannels() { return (await client.get<{ channels: NotificationChannel[] }>("/aiNotification/channels")).data.channels },
  async notifyCreateChannel(payload: object) { return (await client.post<NotificationChannel>("/aiNotification/channels", payload)).data },
  async notifyUpdateChannel(id: string, payload: object) { return (await client.patch<NotificationChannel>(`/aiNotification/channels/${encodeURIComponent(id)}`, payload)).data },
  async notifyDeleteChannel(id: string) { return (await client.delete(`/aiNotification/channels/${encodeURIComponent(id)}`)).data },
  async notifyTestChannel(id: string) { const start = Date.now(); await client.post(`/aiNotification/channels/${encodeURIComponent(id)}/test`); return { latency_ms: Date.now() - start } },
  async notifyDeliveries(query: { channelId?: string; state?: string; page?: number; pageSize?: number } = {}) {
    const { data } = await client.get<{ items: NotificationDelivery[]; total: number }>("/aiNotification/deliveries", { params: { channel_id: query.channelId, state: query.state, page: Math.max(0, (query.page ?? 1) - 1), size: query.pageSize ?? 50 } })
    return { deliveries: data.items, total: data.total }
  },
  async notifyRetryDelivery(id: string) { return (await client.post(`/aiNotification/deliveries/${encodeURIComponent(id)}/retry`)).data },
  async setSettings(patch: { notify_enabled?: boolean; notify_public_base_url?: string; notify_digest_interval_min?: number }) {
    const settings = { ...(patch.notify_enabled === undefined ? {} : { enabled: patch.notify_enabled }), ...(patch.notify_public_base_url === undefined ? {} : { public_base_url: patch.notify_public_base_url }), ...(patch.notify_digest_interval_min === undefined ? {} : { digest_interval_min: patch.notify_digest_interval_min }) }
    await client.patch("/aiNotification/settings", settings)
  },
}
