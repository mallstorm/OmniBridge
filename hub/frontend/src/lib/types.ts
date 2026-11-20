import type { Counter } from '../hub'

export type Provider = {
  id: string
  name: string
  kind: string
  base_url?: string
  enabled: boolean
  support_level?: string
  auth_type?: string
  preferred_protocol?: string
}

export type Account = {
  id: string
  name: string
  kind: string
  enabled: boolean
  email?: string
  plan?: string
  status?: string
  last_error?: string
  last_error_kind?: string
  last_connected_at?: string
}

export type ModelRow = { id: string; owned_by: string }

export type PeriodStats = {
  period: string
  started_at: string
  total: Counter & { average_latency_ms: number; p95_latency_ms: number; success_rate: number }
  providers: Record<string, Counter & { success_rate?: number }>
  models: Record<string, Counter & { success_rate?: number }>
}

export type CatalogEntry = {
  id: string
  name: string
  status: string
  protocols?: string[]
  requires_helper?: string
  requires_config?: string
  note?: string
}

export type LoginStart = {
  account_id?: string
  attempt_id: string
  auth_url?: string
  verification_url?: string
  user_code?: string
  status: string
  backend?: string
}

export type LoginStatus = {
  account_id?: string
  attempt_id: string
  status: string
  success: boolean
  error?: string
  email?: string
  plan?: string
  backend?: string
}
