import { useEffect, useState } from 'react'
import { Card, SectionHead, Badge } from '../components/ui'
import { PageHeader } from '../components/Shell'
import { useApi } from '../lib/useApi'

export function System() {
  const [info, setInfo] = useState<{ version: string; endpoint: string; home: string } | null>(null)
  const status = useApi<{ version?: string; release?: string; listen?: string }>('/bridge/status', { intervalMs: 10000 })
  useEffect(() => { window.hub.info().then(setInfo) }, [])

  return (
    <>
      <PageHeader title="Система" subtitle="Версии, пути и состояние ядра" />
      <div className="grid-2">
        <Card>
          <SectionHead kicker="Приложение" title="OmniHub" />
          <div className="kv"><span className="muted">Версия интерфейса</span><code>{info?.version ?? '—'}</code></div>
          <div className="kv"><span className="muted">Endpoint</span><code>{info?.endpoint ?? '—'}</code></div>
          <div className="kv"><span className="muted">Данные</span><code className="wrap">{info?.home ?? '—'}</code></div>
          <div className="row-actions"><Badge tone="info">Ядро OmniBridge</Badge></div>
        </Card>
        <Card>
          <SectionHead kicker="Ядро" title="Состояние" />
          <pre className="json">{status.data ? JSON.stringify(status.data, null, 2).slice(0, 1200) : status.error || 'загрузка…'}</pre>
        </Card>
      </div>
      <Card>
        <SectionHead kicker="Безопасность" title="Как устроены ключи" />
        <ul className="bullets">
          <li>Ключи провайдеров и ключ шлюза хранятся в защищённом хранилище Windows (DPAPI) текущего пользователя.</li>
          <li>Интерфейс не получает ключи напрямую: все запросы к ядру идут через главный процесс по списку разрешённых маршрутов.</li>
          <li>Ядро слушает только 127.0.0.1. Внешний доступ по сети выключен.</li>
        </ul>
      </Card>
    </>
  )
}
