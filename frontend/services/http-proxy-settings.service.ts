import { api } from "@/lib/api-client"
import type { HTTPProxySettings, UpdateHTTPProxySettingsRequest } from "@/types/http-proxy-settings.types"

const settingsPath = "/admin/system/httpProxy/current"

export class HTTPProxySettingsService {
  static async get(): Promise<HTTPProxySettings> {
    return (await api.get<HTTPProxySettings>(settingsPath)).data
  }

  static async update(input: UpdateHTTPProxySettingsRequest): Promise<HTTPProxySettings> {
    return (await api.put<HTTPProxySettings>(settingsPath, input)).data
  }

  static async clear(): Promise<HTTPProxySettings> {
    return (await api.delete<HTTPProxySettings>(settingsPath)).data
  }
}
