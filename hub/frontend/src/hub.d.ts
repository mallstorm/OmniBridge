export {}

export type ApiResult<T = unknown> = { ok: boolean; status: number; body: T }

export type LiveFrame = {
  at: number
  total: Counter
  providers: Record<string, Counter>
}

declare global {
  interface Window {
    hub: {
      status: () => Promise<{ running: boolean; endpoint: string }>
      connection: () => Promise<{ endpoint: string; keyMasked: string }>
      models: () => Promise<{ ok: boolean; status: number; models: { id: string; owned_by: string }[] }>
      api: <T = unknown>(req: { method: 'GET' | 'PUT' | 'POST' | 'DELETE'; path: string; body?: unknown }) => Promise<ApiResult<T>>
      logs: () => Promise<string[]>
      copy: (req: { what: 'key' | 'endpoint' | 'text'; text?: string }) => Promise<boolean>
      info: () => Promise<{ version: string; endpoint: string; home: string }>
      vscodeInstall: (req: { edition: 'stable' | 'insiders' }) => Promise<{ ok: boolean; cancelled?: boolean; error?: string; path?: string; models?: number; backup?: string | null }>
      openUrl: (url: string) => Promise<{ ok: boolean }>
      onLive: (cb: (f: LiveFrame) => void) => () => void
    }
  }
}

export type Counter = {
  requests: number
  errors: number
  input_tokens: number
  cached_input_tokens?: number
  output_tokens: number
  total_tokens: number
  latency_ms: number
}
