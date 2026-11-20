import type { ReactNode } from 'react'
import { useLive } from '../lib/live'
import { num } from '../lib/format'

export type Route = 'overview' | 'connections' | 'models' | 'integrations' | 'stats' | 'logs' | 'system'

const NAV: { group: string; items: { id: Route; label: string; icon: string }[] }[] = [
  { group: 'Работа', items: [
    { id: 'overview', label: 'Обзор', icon: '◎' },
    { id: 'stats', label: 'Статистика', icon: '▤' },
    { id: 'models', label: 'Модели', icon: '◇' },
  ] },
  { group: 'Источники', items: [
    { id: 'connections', label: 'Подключения', icon: '⬡' },
    { id: 'integrations', label: 'Интеграции', icon: '⇄' },
  ] },
  { group: 'Система', items: [
    { id: 'logs', label: 'Журнал', icon: '≡' },
    { id: 'system', label: 'Система', icon: '⚙' },
  ] },
]

export function Sidebar({ route, onNavigate, running }: { route: Route; onNavigate: (r: Route) => void; running: boolean }) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="logo">OH</div>
        <div>
          <div className="brand-name">OmniHub</div>
          <div className="brand-sub">AI-шлюз для IDE</div>
        </div>
      </div>
      {NAV.map((g) => (
        <div key={g.group} className="nav-group">
          <div className="nav-label">{g.group}</div>
          {g.items.map((it) => (
            <button key={it.id} className={`nav-item ${route === it.id ? 'active' : ''}`} onClick={() => onNavigate(it.id)}>
              <span className="nav-icon">{it.icon}</span>
              {it.label}
            </button>
          ))}
        </div>
      ))}
      <div className="sidebar-foot">
        <span className={`dot ${running ? 'ok' : 'bad'}`} />
        <div>
          <div>{running ? 'Ядро работает' : 'Ядро не отвечает'}</div>
          <LiveMini />
        </div>
      </div>
    </aside>
  )
}

function LiveMini() {
  const { tokensPerSec, connected } = useLive()
  return <div className="muted small">{connected ? `${num(Math.round(tokensPerSec))} tok/s` : 'подключение…'}</div>
}

export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: string; actions?: ReactNode }) {
  return (
    <header className="page-head">
      <div>
        <h1>{title}</h1>
        {subtitle && <p className="muted">{subtitle}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  )
}
