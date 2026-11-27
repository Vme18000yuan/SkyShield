import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import ArtexLLMSettings from "../artex-llm-settings"

const calls = vi.hoisted(() => ({ llmProfiles: vi.fn(), llmPool: vi.fn(), saveLLMProfile: vi.fn(), llmRetryPolicy: vi.fn() }))
vi.mock("@/services/artex-ai.service", () => ({ api: calls }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

describe("editing an activated model", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    calls.llmProfiles.mockResolvedValue([{id:"active-profile",name:"Active fixture",format:"openai",model:"fixture",base_url:"https://example.test/v1",is_default:true,api_key_hint:"••••",rate_per_second:0,rate_per_minute:0,context_window_k:256}])
    calls.llmPool.mockResolvedValue({enabled:false,chain:[]})
    calls.saveLLMProfile.mockResolvedValue({id:"active-profile"})
  })
  it("exposes an enabled edit action and saves without asking for the saved key", async () => {
    render(<ArtexLLMSettings />)
    const edit=await screen.findByRole("button",{name:"编辑配置 Active fixture"})
    expect(edit).toBeEnabled()
    expect(screen.getByRole("button",{name:"已激活"})).toBeDisabled()
    fireEvent.click(edit)
    expect(await screen.findByText("编辑：Active fixture")).toBeVisible()
    fireEvent.change(screen.getByLabelText("上下文窗口(K)"),{target:{value:"512"}})
    fireEvent.click(screen.getByRole("button",{name:/^保存$/}))
    await waitFor(()=>expect(calls.saveLLMProfile).toHaveBeenCalledWith(expect.objectContaining({id:"active-profile",context_window_k:512,api_key:""})))
  })
})
