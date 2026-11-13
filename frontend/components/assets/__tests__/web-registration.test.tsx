import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { AssetRegistrationDialog } from "../asset-registration-dialog"

const register = vi.hoisted(() => vi.fn())
vi.mock("@/hooks/use-asset-entities", () => ({ useRegisterAssets: () => ({ mutateAsync: register, isPending: false }) }))
vi.mock("@/hooks/use-targets", () => ({ useTargets: () => ({ data: { targets: [], nextPageToken: "" }, isFetching: false }) }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

describe("asset registration categories", () => {
  beforeEach(() => { register.mockReset(); register.mockResolvedValue({ results: [{ id: "1" }], errors: [] }) })
  it("registers a Web URL using the existing service identity", async () => {
    render(<AssetRegistrationDialog initialType="web" />)
    fireEvent.click(screen.getByRole("button", { name: "add" }))
    fireEvent.change(screen.getByLabelText("url"), { target: { value: "https://web.example/" } })
    fireEvent.click(screen.getByRole("button", { name: "save" }))
    await waitFor(() => expect(register).toHaveBeenCalledWith(expect.objectContaining({ assets: [expect.objectContaining({ type: "service", url: "https://web.example/" })] })))
  })
  it("registers a non-Web service without treating it as a URL", async () => {
    render(<AssetRegistrationDialog initialType="service" />)
    fireEvent.click(screen.getByRole("button", { name: "add" }))
    fireEvent.change(screen.getByLabelText("domain"), { target: { value: "ssh.example" } })
    fireEvent.change(screen.getByLabelText("serviceName"), { target: { value: "SSH" } })
    fireEvent.change(screen.getByLabelText("port"), { target: { value: "22" } })
    fireEvent.click(screen.getByRole("button", { name: "save" }))
    await waitFor(() => expect(register).toHaveBeenCalledWith(expect.objectContaining({ assets: [expect.objectContaining({ type: "service", domain: "ssh.example", serviceName: "SSH", port: 22 })] })))
  })
  it("registers an application with its organization and bound domains", async () => {
    render(<AssetRegistrationDialog initialType="app" organizationId={7} />)
    fireEvent.click(screen.getByRole("button", { name: "add" }))
    fireEvent.change(screen.getByLabelText("appName"), { target: { value: "Sales Console" } })
    fireEvent.change(screen.getByLabelText("boundDomains"), { target: { value: "sales.example.com\napp.example.com" } })
    fireEvent.click(screen.getByRole("button", { name: "save" }))
    await waitFor(() => expect(register).toHaveBeenCalledWith(expect.objectContaining({ assets: [expect.objectContaining({ type: "app", appName: "Sales Console", organizationId: 7, boundDomains: ["sales.example.com", "app.example.com"] })] })))
  })
})
