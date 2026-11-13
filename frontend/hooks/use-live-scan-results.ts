"use client"

import * as React from "react"
import { useQueryClient } from "@tanstack/react-query"
import { scanKeys } from "@/hooks/use-scans"

const resultRoots = new Set(["websites", "subdomains", "endpoints", "ip-addresses", "ipAddresses", "directories", "screenshots", "vulnerabilities", "asset-entities", "asset-inventory", "asset-ports"])
const terminalStates = new Set(["completed", "succeeded", "failed", "cancelled", "stopped"])

// Refresh only mounted result views while a scan is active, then once more at
// terminal transition so the final acknowledged batch is visible.
export function useLiveScanResults(scanId: number, status?: string) {
  const client = useQueryClient()
  React.useEffect(() => {
    if (!scanId || !status) return
    const refresh = () => {
      if (document.visibilityState !== "visible") return
      void client.refetchQueries({ queryKey: scanKeys.detail(scanId), exact: true, type: "active" })
      void client.refetchQueries({ type: "active", predicate: query => resultRoots.has(String(query.queryKey[0])) })
    }
    refresh()
    if (terminalStates.has(status)) return
    const timer = window.setInterval(refresh, 3000)
    document.addEventListener("visibilitychange", refresh)
    return () => { window.clearInterval(timer); document.removeEventListener("visibilitychange", refresh) }
  }, [client, scanId, status])
}
