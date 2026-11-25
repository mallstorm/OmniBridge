import { ConnectionMetrics, ProviderIcon } from '../components/ConnectionIdentity'
import { ModelSwitch } from '../components/ModelSwitch'
import { useEffect, useState } from 'react'
import { Card, Stat, Toggle, Badge, AreaChart, Button, SectionHead } from '../components/ui'
import { PageHeader } from '../components/Shell'
import { useLive } from '../lib/live'
import { useApi } from '../lib/useApi'
import { num } from '../lib/format'
import { setEnabled } from '../lib/api'
import type { Account, Provider } from '../lib/types'
import type { Route } from '../components/Shell'

export function Overview({ running, endpoint, keyMasked, go, notify }: {
  running: boolean; endpoint: string; keyMasked: string; go: (r: Route) => void; notify: (t: string, tone?: 'ok' | 'bad') => void
}) {
  const live = useLive()
  const providers = useApi<Provider[]>('/bridge/providers', { intervalMs: 5000 })
  const accounts = useApi<Account[]>('/bridge/accounts', { intervalMs: 5000 })
  const [models, setModels] = useState<number | null>(null)
  useEffect(() => {
    window.hub.models().then((m) => setModels(m.ok ? m.models.length : 0)).catch(() => setModels(0))
  }, [running])

  const pEnabled = (providers.data ?? []).filter((p) => p.enabled).length
  const aEnabled = (accounts.data ?? []).filter((a) => a.enabled).length
  const errors = live.frame?.total.errors ?? 0
  const requests = live.frame?.total.requests ?? 0

  async function toggle(kind: 'providers' | 'accounts', id: string, enabled: boolean) {
    try {
      await setEnabled(kind, id, enabled)
      notify(enabled ? 'Включено' : 'Выключено')
      if (kind === 'providers') providers.reload()
      else accounts.reload()
    } catch (e) {
      notify((e as Error).message, 'bad')
    }
  }

  return (
    <>
      <PageHeader title="Обзор шлюза" />

      <div className="grid-hero">
        <Card glow className="hero">
          <div className="hero-top">
            <Badge tone={running ? 'ok' : 'bad'}>{running ? 'Работает' : 'Нет соединения'}</Badge>
            <span className="muted small">{endpoint}</span>
          </div>
          <div className="hero-live">
            <div>
              <div className="kicker">Токены в секунду · сейчас</div>
              <div className="big-number">{live.connected ? num(Math.round(live.tokensPerSec)) : '—'}</div>
              <div className="muted small">{live.connected ? `${num(Math.round(live.requestsPerMin))} запросов в минуту` : 'ожидаем данные…'}</div>
            </div>
          </div>
          <AreaChart values={live.history.map((h) => h.tps)} height={140} label="Токены в секунду" />
          <div className="traffic-details">
            <span>Входящие токены <b>{live.connected ? num(live.frame?.total.input_tokens ?? 0) : '—'}</b></span>
            <span>Исходящие <b>{live.connected ? num(live.frame?.total.output_tokens ?? 0) : '—'}</b></span>
            <span>Ошибки сессии <b>{live.connected ? num(errors) : '—'}</b></span>
          </div>
        </Card>

        <div className="gateway-tools"><ModelSwitch notify={notify} />
        <Card>
          <SectionHead kicker="Подключение" title="Для IDE и расширений" />
          <div className="kv"><span className="muted">Endpoint</span><code>{endpoint}</code></div>
          <div className="kv"><span className="muted">Ключ</span><code>{keyMasked || '—'}</code></div>
          <div className="row-actions">
            <Button variant="ghost" size="sm" onClick={() => window.hub.copy({ what: 'endpoint' })}>Копировать endpoint</Button>
            <Button variant="ghost" size="sm" onClick={() => window.hub.copy({ what: 'key' })}>Копировать ключ</Button>
          </div>
          <Button variant="subtle" size="sm" onClick={() => go('integrations')}>Настроить интеграции →</Button>
        </Card></div>
      </div>

      <div className="grid-stats">
        <Stat label="API-ключи" value={`${pEnabled} / ${providers.data?.length ?? 0}`} hint="включено" tone="accent" />
        <Stat label="Подписки" value={`${aEnabled} / ${accounts.data?.length ?? 0}`} hint="включено" tone="accent" />
        <Stat label="Модели" value={models ?? '—'} hint="доступны клиентам" />
        <Stat label="Запросов за сессию" value={num(requests)} hint={errors ? `ошибок: ${num(errors)}` : 'без ошибок'} tone={errors ? 'warn' : 'ok'} />
      </div>

      <div className="overview-sources">
        <Card>
          <SectionHead kicker="Источники" title="Быстрое управление" aside={<Button variant="ghost" size="sm" onClick={() => go('connections')}>Все подключения</Button>} />
          <div className="source-list list">
            {(providers.data ?? []).map((p) => (
              <div className="list-row" key={'p' + p.id}>
                <div>
                  <div className="source-name"><ProviderIcon kind={p.kind} />{p.name}</div>
                  <div className="muted small">API-ключ · {p.kind}</div>
                </div>
                <ConnectionMetrics id={p.id} />
                <Toggle label={`Включить ${p.name}`} checked={p.enabled} onChange={(v) => toggle('providers', p.id, v)} />
              </div>
            ))}
            {(accounts.data ?? []).map((a) => (
              <div className="list-row" key={'a' + a.id}>
                <div>
                  <div className="source-name"><ProviderIcon kind={a.kind} />{a.name}</div>
                  <div className="muted small">Подписка · {a.email || a.plan || a.kind}</div>
                </div>
                <ConnectionMetrics id={a.id} />
                <Toggle label={`Включить ${a.name}`} checked={a.enabled} onChange={(v) => toggle('accounts', a.id, v)} />
              </div>
            ))}
            {!providers.data?.length && !accounts.data?.length && <div className="muted">Пока нет подключений. Добавь первый во вкладке «Подключения».</div>}
          </div>
        </Card>

        <Card>
          <SectionHead kicker="Активность" title="Последние провайдеры" />
          <ProviderTraffic />
        </Card>
      </div>
    </>
  )
}

function ProviderTraffic() {
  const live = useLive()
  const rows = Object.entries(live.frame?.providers ?? {}).sort((a, b) => b[1].total_tokens - a[1].total_tokens).slice(0, 6)
  if (!rows.length) return <div className="muted">Трафика пока нет. Он появится здесь, как только IDE начнёт запросы.</div>
  const max = Math.max(1, ...rows.map((r) => r[1].total_tokens))
  return (
    <div className="list">
      {rows.map(([id, c]) => (
        <div className="list-row" key={id}>
          <div className="grow">
            <div className="row-between"><span>{id}</span><span className="muted small">{num(c.total_tokens)} токенов</span></div>
            <div className="bar"><span className="bar-fill bar-accent" style={{ width: `${Math.max(2, (c.total_tokens / max) * 100)}%` }} /></div>
          </div>
        </div>
      ))}
    </div>
  )
}
