import { useEffect, useState } from 'react'
import { Button, Card, SectionHead } from './ui'
import { call } from '../lib/api'
import type { ModelRow } from '../lib/types'

export function ModelSwitch({ notify }: { notify: (text: string, tone?: 'ok' | 'bad') => void }) {
  const [models, setModels] = useState<ModelRow[]>([])
  const [active, setActive] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const alias = 'hub-active'
  useEffect(() => {
    let mounted = true
    async function load() {
      try {
        const [list, route] = await Promise.all([window.hub.models(), call<{ aliases?: Record<string,string> }>('GET', '/bridge/router')])
        if (!list.ok) throw new Error('Каталог моделей недоступен')
        if (mounted) {
          setModels(list.models.filter(m => m.owned_by !== 'omnibridge-router'))
          setActive(route.aliases?.[alias] ?? '')
          setError('')
        }
      } catch (e) { if (mounted) setError((e as Error).message) }
    }
    if (!pending) void load()
    const timer = setInterval(() => { if (!pending) void load() }, 10000)
    return () => { mounted = false; clearInterval(timer) }
  }, [pending])

  async function select(model: string) {
    setPending(true)
    try {
      await call('PUT', '/bridge/router/active-model', { model })
      setActive(model)
      notify('Модель переключена')
    } catch (e) { notify((e as Error).message, 'bad') }
    finally { setPending(false) }
  }

  return <Card className="model-switch">
    <SectionHead title="Хаб моделей" kicker="Маршрутизация" />
    <label htmlFor="active-model">Активная модель для IDE</label>
    <select id="active-model" value={active} disabled={pending || !!error || !models.length} onChange={e => void select(e.target.value)}>
      <option value="" disabled>{models.length ? 'Выберите модель' : 'Нет доступных моделей'}</option>
      {active && !models.some(m => m.id === active) && <option value={active} disabled>{active} · недоступна</option>}
      {models.map(m => <option key={m.id} value={m.id}>{m.id}</option>)}
    </select>
    <div className="row-between"><span className="muted">Модель в клиенте <code>{alias}</code></span>
      <Button size="sm" variant="ghost" onClick={() => window.hub.copy({ what: 'text', text: alias }).then(() => notify('ID скопирован'))}>Копировать ID</Button>
    </div>
    {error && <div role="alert" className="error-line">{error}</div>}
  </Card>
}
