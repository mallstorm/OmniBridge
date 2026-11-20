import type { ApiResult } from '../hub'

// Thin wrapper over the allow-listed admin bridge exposed by the Electron shell.
export async function call<T>(method: 'GET' | 'PUT' | 'POST' | 'DELETE', path: string, body?: unknown): Promise<T> {
  const r: ApiResult<T> = await window.hub.api<T>({ method, path, body })
  if (!r.ok) {
    const msg = (r.body as { error?: string } | undefined)?.error ?? `HTTP ${r.status}`
    throw new Error(msg)
  }
  return r.body
}

export const setEnabled = (kind: 'providers' | 'accounts', id: string, enabled: boolean) =>
  call(`PUT`, `/bridge/${kind}/${encodeURIComponent(id)}/enabled`, { enabled })

export const refreshItem = (kind: 'providers' | 'accounts', id: string) =>
  call('POST', `/bridge/${kind}/${encodeURIComponent(id)}/refresh`)

export const removeItem = (kind: 'providers' | 'accounts', id: string) =>
  call('DELETE', `/bridge/${kind}/${encodeURIComponent(id)}`)
