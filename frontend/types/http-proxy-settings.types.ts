export interface HTTPProxySettings {
  name: string
  enabled: boolean
  configured: boolean
  /** Credential-free address for display. Proxy credentials are never returned. */
  proxyAddress: string
  updatedAt: string | null
}

export interface UpdateHTTPProxySettingsRequest {
  enabled: boolean
  /** Empty preserves the saved address; DELETE removes it. */
  proxyUrl: string
}
