import { useState } from 'react'
import { Card, Stat, AreaChart, Bar, SectionHead } from '../components/ui'
import { PageHeader } from '../components/Shell'
import { useLive } from '../lib/live'
import { useApi } from '../lib/useApi'
import { num, pct } from '../lib/format'
import type { PeriodStats } from '../lib/types'

const PERIODS = [
  { id: 'session', label: 'Сессия' },
  { id: '24h', label: '24 часа' },
  { id: '7d', label: '7 дней' },
  { id: '30d', label: '30 дней' },
  { id: 'all', label: 'Всё время' },
] as const

export function Statistics() {
  const [period, setPeriod] = useState<(typeof PERIODS)[number]['id']>('24h')
  const live = useLive()
  const snap = useApi<PeriodStats>(`/bridge/statistics?period=${period}`, { intervalMs: period === 'session' ? 3000 : 15000, deps: [period] })
  const t = snap.data?.total
  const providers = Object.entries(snap.data?.providers ?? {}).sort((a, b) => b[1].total_tokens - a[1].total_tokens)
  const models = Object.entries(snap.data?.models ?? {}).sort((a, b) => b[1].total_tokens - a[1].total_tokens).slice(0, 15)
  const maxP = Math.max(1, ...providers.map((p) => p[1].total_tokens))
  const maxM = Math.max(1, ...models.map((m) => m[1].total_tokens))

  return (
    <>
      <PageHeader title="Статистика" subtitle="Токены и запросы через шлюз. Обновляется в реальном времени." actions={
        <div className="segmented small-seg">
          {PERIODS.map((p) => (
            <button key={p.id} className={period === p.id ? 'on' : ''} onClick={() => setPeriod(p.id)}>{p.label}</button>
          ))}
        </div>
      } />

      <Card glow>
        <SectionHead kicker="Живой поток" title="Токены в секунду" aside={<span className="big-number small-num">{num(Math.round(live.tokensPerSec))} tok/s</span>} />
        <AreaChart values={live.history.map((h) => h.tps)} height={180} label="Токены в секунду, живой график" />
        <div className="muted small">Последние {live.history.length} с · {num(Math.round(live.requestsPerMin))} запросов в минуту</div>
      </Card>

      <div className="grid-stats">
        <Stat label="Всего токенов" value={num(t?.total_tokens)} hint={`вход ${num(t?.input_tokens)} · выход ${num(t?.output_tokens)}`} tone="accent" />
        <Stat label="Запросов" value={num(t?.requests)} hint={`ошибок ${num(t?.errors)}`} />
        <Stat label="Успешность" value={t && t.requests ? `${Math.round((1 - t.errors / t.requests) * 100)}%` : '—'} hint={`p95 ${num(t?.p95_latency_ms)} мс`} tone={t && t.errors ? 'warn' : 'ok'} />
        <Stat label="Средняя задержка" value={t ? `${Math.round(t.average_latency_ms || 0)} мс` : '—'} hint={t?.cached_input_tokens ? `кэш ${num(t.cached_input_tokens)}` : undefined} />
      </div>

      <div className="grid-2">
        <Card>
          <SectionHead kicker="По провайдерам" title="Расход токенов" />
          {providers.length === 0 && <div className="muted">За выбранный период данных нет.</div>}
          <div className="table">
            {providers.map(([id, c]) => (
              <div className="table-row" key={id}>
                <div className="grow">
                  <div className="row-between"><span>{id}</span><span className="muted small">{pct(c.total_tokens, t?.total_tokens ?? 0)}</span></div>
                  <Bar value={c.total_tokens} max={maxP} />
                  <div className="muted small">{num(c.total_tokens)} токенов · {num(c.requests)} запросов · ошибок {num(c.errors)}</div>
                </div>
              </div>
            ))}
          </div>
        </Card>

        <Card>
          <SectionHead kicker="Топ моделей" title="По токенам" />
          {models.length === 0 && <div className="muted">Нет данных.</div>}
          <div className="table">
            {models.map(([id, c]) => (
              <div className="table-row" key={id}>
                <div className="grow">
                  <div className="row-between"><span className="model-id">{id}</span><span className="muted small">{num(c.total_tokens)}</span></div>
                  <Bar value={c.total_tokens} max={maxM} tone="ok" />
                  <div className="muted small">запросов {num(c.requests)} · успешность {c.requests ? Math.round((1 - c.errors / c.requests) * 100) : 0}%</div>
                </div>
              </div>
            ))}
          </div>
        </Card>
      </div>
    </>
  )
}
