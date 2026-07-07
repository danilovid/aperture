// Typed client for the gateway admin API.
import { adminHeaders } from './auth'

export const API_URL = import.meta.env.VITE_APERTURE_URL || 'http://localhost:8080'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: adminHeaders({
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...((init.headers as Record<string, string>) ?? {}),
    }),
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const msg =
      typeof data?.error === 'string' ? data.error : data?.error?.message || `HTTP ${res.status}`
    throw new ApiError(res.status, msg)
  }
  return data as T
}

// ── Types (mirror the Go structs) ────────────────────────────────────────────

export type Period = '24h' | '7d' | '30d'

export interface StatsSummary {
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cost_usd: number
  avg_latency_ms: number
  error_rate: number
}

export interface TimeseriesBucket {
  ts: string
  requests: number
  total_tokens: number
  cost_usd: number
  avg_latency_ms: number
}

export interface ModelStat {
  model: string
  provider: string
  requests: number
  total_tokens: number
  cost_usd: number
  avg_latency_ms: number
}

export interface DLPEvent {
  id: number
  ts: string
  key_id: string
  model: string
  provider: string
  rule: string
  group: string
  action: 'blocked' | 'redacted' | 'alerted'
  masked_sample: string
}

export interface DLPSummary {
  total: number
  blocked: number
  redacted: number
  alerted: number
}

export interface CustomRule {
  name: string
  pattern: string
}

export type PolicyAction = 'off' | 'alert' | 'redact' | 'block'

export interface Policy {
  secrets: PolicyAction
  pii: PolicyAction
  custom: PolicyAction
  custom_rules?: CustomRule[]
}

export interface Finding {
  rule: string
  group: string
  action: PolicyAction
  masked_sample: string
}

export interface DryRunResult {
  verdict: PolicyAction
  findings: Finding[]
  upstream_text: string
}

export interface ApertureKey {
  id: string
  aperture_key: string
  name: string
  created_at: string
}

// ── Endpoints ────────────────────────────────────────────────────────────────

export const api = {
  statsSummary: (period: Period) =>
    request<StatsSummary>(`/admin/stats/summary?period=${period}`),
  statsTimeseries: (period: Period, bucketHours: number) =>
    request<{ buckets: TimeseriesBucket[] }>(
      `/admin/stats/timeseries?period=${period}&bucket_hours=${bucketHours}`,
    ),
  statsModels: (period: Period) =>
    request<{ models: ModelStat[] }>(`/admin/stats/models?period=${period}`),

  dlpSummary: (period: Period) => request<DLPSummary>(`/admin/dlp/summary?period=${period}`),
  dlpEvents: (params: { action?: string; rule?: string; key_id?: string; limit?: number }) => {
    const q = new URLSearchParams()
    if (params.action && params.action !== 'all') q.set('action', params.action)
    if (params.rule && params.rule !== 'all') q.set('rule', params.rule)
    if (params.key_id && params.key_id !== 'all') q.set('key_id', params.key_id)
    q.set('limit', String(params.limit ?? 200))
    return request<{ events: DLPEvent[] }>(`/admin/dlp/events?${q}`)
  },

  policies: () => request<{ default: Policy; keys: Record<string, Policy> }>('/admin/policies'),
  putDefaultPolicy: (p: Policy) =>
    request<{ ok: boolean }>('/admin/policies/default', { method: 'PUT', body: JSON.stringify(p) }),
  putKeyPolicy: (keyID: string, p: Policy) =>
    request<{ ok: boolean }>(`/admin/policies/keys/${encodeURIComponent(keyID)}`, {
      method: 'PUT',
      body: JSON.stringify(p),
    }),
  deleteKeyPolicy: (keyID: string) =>
    request<void>(`/admin/policies/keys/${encodeURIComponent(keyID)}`, { method: 'DELETE' }),
  testPolicy: (text: string, policy?: Policy, keyID?: string) =>
    request<DryRunResult>('/admin/policies/test', {
      method: 'POST',
      body: JSON.stringify({ text, policy, key_id: keyID }),
    }),

  config: () =>
    request<{ configured: boolean; configured_providers: string[] }>('/admin/config'),
  setConfig: (keys: { openai_api_key?: string; anthropic_api_key?: string; groq_api_key?: string }) =>
    request<{ ok: boolean }>('/admin/config', { method: 'POST', body: JSON.stringify(keys) }),
  clearConfig: () => request<{ ok: boolean }>('/admin/config', { method: 'DELETE' }),

  listKeys: () => request<{ keys: ApertureKey[] }>('/admin/keys'),
  createKey: (name: string) =>
    request<ApertureKey>('/admin/keys', { method: 'POST', body: JSON.stringify({ name }) }),
  deleteKey: (id: string) =>
    request<void>(`/admin/keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}
