import { useLive } from '../lib/live'
import { num } from '../lib/format'

const logos = import.meta.glob<string>('../../../../assets/providers/*.png', { eager: true, query: '?url', import: 'default' })
const aliases: Record<string, string> = { 'chatgpt-codex': 'openai', 'gemini-developer-oauth': 'gemini', 'nvidia-nim': 'nvidia' }

export function ProviderIcon({ kind }: { kind: string }) {
  const key = aliases[kind] ?? kind
  const src = logos['../../../../assets/providers/' + key + '.png']
  return src ? <img className="provider-icon" src={src} alt="" /> : <span className="provider-icon monogram" aria-hidden="true">{kind.slice(0, 2).toUpperCase()}</span>
}

export function ConnectionMetrics({ id }: { id: string }) {
  const live = useLive()
  const counter = live.frame?.providers[id]
  const requests = counter?.requests ?? 0
  return <div className="connection-metrics">
    <span>Запросы <b>{live.connected ? num(requests) : '—'}</b></span>
    <span>Токены <b>{live.connected ? num(counter?.total_tokens ?? 0) : '—'}</b></span>
    <span>Ошибки <b className={counter?.errors ? 'failed' : ''}>{live.connected ? num(counter?.errors ?? 0) : '—'}</b></span>
    <span>Среднее время <b>{live.connected && requests && counter ? num(Math.round(counter.latency_ms / requests)) + ' мс' : '—'}</b></span>
  </div>
}
