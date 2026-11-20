import { useEffect, useRef, useState } from 'react'
import { Button, SectionHead, Badge } from './ui'
import { call } from '../lib/api'
import type { Account, CatalogEntry, LoginStart, LoginStatus } from '../lib/types'

type Phase = 'pick' | 'waiting' | 'done' | 'failed'

const POLL_MS = 2000
const POLL_LIMIT_MS = 5 * 60 * 1000

export function AddSubscription({ onClose, onDone, notify }: { onClose: () => void; onDone: (name: string) => void; notify: (t: string, tone?: 'ok' | 'bad') => void }) {
  const [catalog, setCatalog] = useState<CatalogEntry[]>([])
  const [loadError, setLoadError] = useState('')
  const [phase, setPhase] = useState<Phase>('pick')
  const [label, setLabel] = useState('')
  const [accountName, setAccountName] = useState('')
  const [login, setLogin] = useState<LoginStart | null>(null)
  const [status, setStatus] = useState<LoginStatus | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string>('')
  const timer = useRef<number | null>(null)
  const startedAt = useRef(0)

  useEffect(() => {
    call<CatalogEntry[]>('GET', '/bridge/account-catalog')
      .then(setCatalog)
      .catch((e) => setLoadError((e as Error).message))
    return () => { if (timer.current) window.clearInterval(timer.current) }
  }, [])

  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [onClose])

  async function connect(entry: CatalogEntry) {
    setError('')
    setBusy(entry.id)
    try {
      const acc = await call<Account>('POST', '/bridge/accounts', {
        id: `${entry.id}-${Date.now().toString(36)}`,
        name: accountName.trim() || entry.name,
        kind: entry.id,
      })
      setLabel(entry.name)
      const start = await call<LoginStart>('POST', `/bridge/accounts/${encodeURIComponent(acc.id)}/login/start`, { device: false })
      setLogin(start)
      setPhase('waiting')
      const url = start.auth_url || start.verification_url
      if (url) await window.hub.openUrl(url)
      startedAt.current = Date.now()
      poll(start.attempt_id, acc.id)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  function poll(attempt: string, accountId: string) {
    if (timer.current) window.clearInterval(timer.current)
    timer.current = window.setInterval(async () => {
      if (Date.now() - startedAt.current > POLL_LIMIT_MS) {
        window.clearInterval(timer.current!)
        setPhase('failed')
        setError('Время входа истекло. Попробуй ещё раз.')
        return
      }
      try {
        const st = await call<LoginStatus>('GET', `/bridge/accounts/login/status?attempt=${encodeURIComponent(attempt)}`)
        setStatus(st)
        if (st.status === 'completed' || st.success) {
          window.clearInterval(timer.current!)
          setPhase('done')
          notify(`Подписка подключена${st.email ? ': ' + st.email : ''}`)
          onDone(accountId)
        } else if (st.status === 'failed') {
          window.clearInterval(timer.current!)
          setPhase('failed')
          setError(st.error || 'Вход не выполнен')
        }
      } catch (e) {
        window.clearInterval(timer.current!)
        setPhase('failed')
        setError((e as Error).message)
      }
    }, POLL_MS)
  }

  async function cancelWait() {
    if (timer.current) window.clearInterval(timer.current)
    onClose()
  }

  const url = login?.auth_url || login?.verification_url

  return (
    <div className="modal-backdrop" onClick={phase === 'waiting' ? undefined : onClose}>
      <div className="modal modal-wide" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <SectionHead kicker="Новая подписка" title={phase === 'pick' ? 'Выбери сервис' : label} />

        {phase === 'pick' && (
          <>
            <div className="form">
              <label>Название подключения <span className="muted small">(необязательно)</span>
                <input value={accountName} onChange={(e) => setAccountName(e.target.value)} placeholder="Например, рабочий ChatGPT" />
              </label>
            </div>
            {loadError && <div className="error-line">{loadError}</div>}
            <div className="catalog">
              {catalog.map((c) => {
                const available = c.status === 'supported'
                return (
                  <div key={c.id} className={`catalog-item ${available ? '' : 'dim'}`}>
                    <div className="catalog-head">
                      <div className="conn-name">{c.name}</div>
                      {available ? <Badge tone="ok">Доступно</Badge> : <Badge tone="neutral">Скоро</Badge>}
                    </div>
                    {c.note && <p className="muted small">{c.note}</p>}
                    {c.requires_config && <p className="muted small">Нужно: {c.requires_config}</p>}
                    <Button size="sm" disabled={!available || !!busy} onClick={() => connect(c)}>
                      {busy === c.id ? 'Открываем вход…' : 'Войти'}
                    </Button>
                  </div>
                )
              })}
            </div>
            {error && <div className="error-line">{error}</div>}
            <p className="muted small">Вход выполняется на официальной странице провайдера. Пароль OmniHub не видит.</p>
            <div className="modal-actions">
              <Button variant="ghost" onClick={onClose}>Закрыть</Button>
            </div>
          </>
        )}

        {phase === 'waiting' && (
          <>
            <div className="wait-box">
              <div className="pulse" aria-hidden="true" />
              <div>
                <div className="conn-name">Ждём подтверждения входа</div>
                <p className="muted small">Открылась страница {label}. Войди и разреши доступ — OmniHub сам заметит результат.</p>
                {login?.user_code && <div className="code-line">Код: <code>{login.user_code}</code></div>}
                {status?.backend && <p className="muted small">Способ входа: {status.backend}</p>}
              </div>
            </div>
            <div className="row-actions">
              {url && <Button variant="ghost" onClick={() => window.hub.openUrl(url)}>Открыть страницу снова</Button>}
            </div>
            <div className="modal-actions">
              <Button variant="ghost" onClick={cancelWait}>Закрыть, войду позже</Button>
            </div>
          </>
        )}

        {phase === 'failed' && (
          <>
            <div className="error-line">{error || 'Вход не выполнен'}</div>
            <p className="muted small">Подключение сохранено со статусом ошибки. Его можно проверить или удалить в списке подписок.</p>
            <div className="modal-actions">
              <Button variant="ghost" onClick={onClose}>Закрыть</Button>
              <Button onClick={() => { setPhase('pick'); setError('') }}>Попробовать снова</Button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
