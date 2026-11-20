import { useEffect, useMemo, useState } from 'react'
import { Card, Button } from '../components/ui'
import { PageHeader } from '../components/Shell'

export function Logs() {
  const [lines, setLines] = useState<string[]>([])
  const [q, setQ] = useState('')
  const [level, setLevel] = useState<'all' | 'error' | 'warn'>('all')
  const [paused, setPaused] = useState(false)

  useEffect(() => {
    let alive = true
    const load = () => window.hub.logs().then((l) => alive && setLines(l)).catch(() => {})
    load()
    if (paused) return () => { alive = false }
    const t = setInterval(load, 3000)
    return () => { alive = false; clearInterval(t) }
  }, [paused])

  const shown = useMemo(() => lines.filter((l) => {
    const low = l.toLowerCase()
    if (level === 'error' && !/error|fail|panic|fatal/.test(low)) return false
    if (level === 'warn' && !/warn|error|fail/.test(low)) return false
    return !q || low.includes(q.toLowerCase())
  }), [lines, q, level])

  return (
    <>
      <PageHeader title="Журнал" subtitle="Последние события ядра. Ключи и токены в журнале маскируются." actions={
        <Button variant="ghost" onClick={() => setPaused((p) => !p)}>{paused ? 'Продолжить' : 'Пауза'}</Button>
      } />
      <div className="toolbar">
        <input className="search" placeholder="Фильтр по тексту" value={q} onChange={(e) => setQ(e.target.value)} />
        <div className="chips">
          {(['all', 'warn', 'error'] as const).map((l) => (
            <button key={l} className={`chip ${level === l ? 'on' : ''}`} onClick={() => setLevel(l)}>{l === 'all' ? 'Все' : l === 'warn' ? 'Предупреждения' : 'Ошибки'}</button>
          ))}
        </div>
      </div>
      <Card className="log-card">
        {shown.length === 0 && <div className="muted">Записей нет.</div>}
        {shown.map((l, i) => (
          <div key={i} className={`log-line ${/error|fail|panic|fatal/i.test(l) ? 'log-bad' : /warn/i.test(l) ? 'log-warn' : ''}`}>{l}</div>
        ))}
      </Card>
    </>
  )
}
