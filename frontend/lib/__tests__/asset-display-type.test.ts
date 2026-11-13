import { describe, expect, it } from "vitest"
import { assetDisplayType } from "@/lib/asset-display-type"

describe("asset display classification", () => {
  it("shows HTTP(S) websites and URL endpoints as Web", () => {
    expect(assetDisplayType({ assetType: "service", label: "http://a.example/" })).toBe("web")
    expect(assetDisplayType({ assetType: "service", label: "HTTPS://a.example/path" })).toBe("web")
    expect(assetDisplayType({ assetType: "endpoint", label: "https://a.example/api" })).toBe("web")
  })
  it("retains non-Web services and respects the server classification", () => {
    expect(assetDisplayType({ assetType: "service", label: "ssh.example:22" })).toBe("service")
    expect(assetDisplayType({ assetType: "ip", label: "8.8.8.8" })).toBe("ip")
    expect(assetDisplayType({ assetType: "service", label: "https://a.example", displayType: "service" })).toBe("service")
  })
})
