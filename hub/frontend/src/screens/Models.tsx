import { ModelSwitch } from '../components/ModelSwitch'
import { useEffect, useMemo, useState } from 'react'
import { Card, Empty, Button } from '../components/ui'
import { PageHeader } from '../components/Shell'
import type { ModelRow } from '../lib/types'

export function Models({ notify }: { notify: (t: string, tone?: 'ok' | 'bad') => void }) {
  const [models, setModels] = useState<ModelRow[] | null>(null)
  const [q, setQ] = useState('')
  const [group, setGroup] = useState<string>('all')

  useEffect(() => {
    window.hub.models().then((r) => setModels(r.ok ? r.models : [])).catch(() => setModels([]))
  }, [])

  const groups = useMemo(() => {
    const m = new Map<string, number>()
    for (const x of models ?? []) m.set(x.owned_by || 'other', (m.get(x.owned_by || 'other') ?? 0) + 1)
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [models])

  const shown = (models ?? []).filter((m) =>
    (group === 'all' || (m.owned_by || 'other') === group) &&
    (!q || m.id.toLowerCase().includes(q.toLowerCase())),
  )

  return (
    <>
      <PageHeader title="Каталог моделей" subtitle="Всё, что шлюз отдаёт клиентам через /v1/models" actions={
        <Button variant="ghost" onClick={() => window.hub.models().then((r) => setModels(r.ok ? r.models : []))}>Обновить список</Button>
      } />

      <ModelSwitch notify={notify} />
      {models === null && <div className="muted">Загрузка…</div>}
      {models && models.length === 0 && (
        <Empty title="Моделей пока нет" text="Включи хотя бы одно подключение во вкладке «Подключения», и список появится здесь." />
      )}

      {models && models.length > 0 && (
        <>
          <div className="toolbar">
            <input className="search" placeholder="Поиск по имени модели" value={q} onChange={(e) => setQ(e.target.value)} />
            <div className="chips">
              <button className={`chip ${group === 'all' ? 'on' : ''}`} onClick={() => setGroup('all')}>Все · {models.length}</button>
              {groups.map(([g, n]) => (
                <button key={g} className={`chip ${group === g ? 'on' : ''}`} onClick={() => setGroup(g)}>{g} · {n}</button>
              ))}
            </div>
          </div>
          <Card>
            <div className="model-grid">
              {shown.map((m) => (
                <div className="model-item" key={m.id}>
                  <div className="model-id">{m.id}</div>
                  <div className="row-between">
                    <span className="muted small">{m.owned_by || 'other'}</span>
                    <button className="link" onClick={() => window.hub.copy({ what: 'text', text: m.id }).then(() => notify('ID скопирован'))}>копировать ID</button>
                  </div>
                </div>
              ))}
            </div>
          </Card>
        </>
      )}
    </>
  )
}
