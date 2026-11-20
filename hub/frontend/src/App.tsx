import { useEffect, useState, useCallback } from 'react'
import { Sidebar, type Route } from './components/Shell'
import { Toast } from './components/ui'
import { Overview } from './screens/Overview'
import { Connections } from './screens/Connections'
import { Models } from './screens/Models'
import { Integrations } from './screens/Integrations'
import { Statistics } from './screens/Statistics'
import { Logs } from './screens/Logs'
import { System } from './screens/System'
import './App.css'

type ToastMsg = { text: string; tone: 'ok' | 'bad'; id: number }

export default function App() {
  const [route, setRoute] = useState<Route>(() => {
    const h = window.location.hash.replace('#', '') as Route
    return ['overview', 'connections', 'models', 'integrations', 'stats', 'logs', 'system'].includes(h) ? h : 'overview'
  })
  const [running, setRunning] = useState(false)
  const [endpoint, setEndpoint] = useState('')
  const [keyMasked, setKeyMasked] = useState('')
  const [toast, setToast] = useState<ToastMsg | null>(null)

  useEffect(() => {
    window.hub.connection().then((c) => { setEndpoint(c.endpoint); setKeyMasked(c.keyMasked) })
    const poll = () => window.hub.status().then((s) => { setRunning(s.running); setEndpoint(s.endpoint) }).catch(() => setRunning(false))
    poll()
    const t = setInterval(poll, 3000)
    return () => clearInterval(t)
  }, [])

  const notify = useCallback((text: string, tone: 'ok' | 'bad' = 'ok') => {
    const id = Date.now()
    setToast({ text, tone, id })
    setTimeout(() => setToast((cur) => (cur && cur.id === id ? null : cur)), 2600)
  }, [])

  const page = (() => {
    switch (route) {
      case 'overview': return <Overview running={running} endpoint={endpoint} keyMasked={keyMasked} go={setRoute} notify={notify} />
      case 'connections': return <Connections notify={notify} />
      case 'models': return <Models notify={notify} />
      case 'integrations': return <Integrations endpoint={endpoint} keyMasked={keyMasked} notify={notify} />
      case 'stats': return <Statistics />
      case 'logs': return <Logs />
      case 'system': return <System />
    }
  })()

  return (
    <div className="shell">
      <Sidebar route={route} onNavigate={setRoute} running={running} />
      <main className="content" key={route}>{page}</main>
      {toast && <Toast text={toast.text} tone={toast.tone} />}
    </div>
  )
}
