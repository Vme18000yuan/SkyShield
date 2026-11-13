const STORAGE_KEY = "tianqiong.remembered-login"

export interface RememberedLogin {
  username: string
  password: string
}

export function loadRememberedLogin(): RememberedLogin | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const saved: unknown = JSON.parse(raw)
    if (
      typeof saved !== "object" || saved === null ||
      !("username" in saved) || typeof saved.username !== "string" || !saved.username.trim() ||
      !("password" in saved) || typeof saved.password !== "string" || !saved.password
    ) return null
    return { username: saved.username, password: saved.password }
  } catch {
    return null
  }
}

export function saveRememberedLogin(login: RememberedLogin): boolean {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(login))
    return true
  } catch {
    return false
  }
}

export function clearRememberedLogin(): boolean {
  try {
    window.localStorage.removeItem(STORAGE_KEY)
    return true
  } catch {
    return false
  }
}
