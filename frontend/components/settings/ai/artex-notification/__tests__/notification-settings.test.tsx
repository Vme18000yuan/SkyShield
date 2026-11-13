import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import NotificationSettings from "../notification-settings"

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn(), put: vi.fn() }))
vi.mock("@/lib/api-client", () => ({ api: requests, getErrorMessage: (error: Error) => error.message }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: 7 } } }) }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const saved = { id: "10", name: "安全群", kind: "dingtalk", enabled: true, mode: "realtime", config: { webhook: "__masked__:…123456", secret: "__masked__" }, filter: {}, rate_per_min: 20, version: 4, secret_keys: ["webhook", "secret"] }
const meta = { settings: { enabled: true, public_base_url: "", digest_interval_min: 30 }, kinds: [{ kind: "dingtalk", default_rate_per_min: 20, secret_keys: ["webhook", "secret"] }], stats: { channels: 1, channels_on: 1, pending: 0, failed: 0, sent_today: 0, backlog_age_ms: 0 } }

describe("ARTEX notification settings", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    requests.get.mockImplementation(async (url: string) => {
      if (url.endsWith("/meta")) return { data: meta }
      if (url.endsWith("/channels")) return { data: { channels: [saved] } }
      if (url.endsWith("/deliveries")) return { data: { items: [], total: 0 } }
      throw new Error(`unexpected GET ${url}`)
    })
    requests.post.mockResolvedValue({ data: {} })
    requests.patch.mockResolvedValue({ data: {} })
  })

  it("creates a channel and sends selected filter and credentials", async () => {
    render(<NotificationSettings />)
    fireEvent.click(await screen.findByRole("button", { name: "添加渠道" }))
    const dialog = await screen.findByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("渠道名称"), { target: { value: "测试群" } })
    fireEvent.change(within(dialog).getByLabelText("Webhook 地址"), { target: { value: "https://example.com/hook" } })
    fireEvent.change(within(dialog).getByLabelText("加签密钥"), { target: { value: "new-secret" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "添加" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiNotification/channels", expect.objectContaining({ name: "测试群", kind: "dingtalk", config: { webhook: "https://example.com/hook", secret: "new-secret" }, enabled: true })))
  })

  it("preserves masked secrets and includes the version when editing or toggling", async () => {
    render(<NotificationSettings />)
    fireEvent.click(await screen.findByText("安全群"))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByLabelText("Webhook 地址")).toHaveValue("")
    fireEvent.change(within(dialog).getByLabelText("渠道名称"), { target: { value: "安全群更新" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }))
    await waitFor(() => expect(requests.patch).toHaveBeenCalledWith("/aiNotification/channels/10", expect.objectContaining({ name: "安全群更新", version: 4, config: saved.config })))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole("switch", { name: "启用" }))
    await waitFor(() => expect(requests.patch).toHaveBeenCalledWith("/aiNotification/channels/10", { enabled: false, version: 4 }))
  })

  it("updates only the current account notification switch and opens delivery history", async () => {
    render(<NotificationSettings />)
    fireEvent.click(await screen.findByRole("switch", { name: "本账号推送总开关" }))
    await waitFor(() => expect(requests.patch).toHaveBeenCalledWith("/aiNotification/settings", { enabled: false }))
    fireEvent.click(screen.getByRole("tab", { name: "投递记录" }))
    expect(await screen.findByText("暂无投递记录")).toBeVisible()
    expect(requests.get).toHaveBeenCalledWith("/aiNotification/deliveries", expect.objectContaining({ params: expect.objectContaining({ page: 0, size: 50 }) }))
  })
})
