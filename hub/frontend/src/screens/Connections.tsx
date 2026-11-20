import { useState } from 'react'
import { Card, Badge, Toggle, Button, Modal, Empty } from '../components/ui'
import { PageHeader } from '../components/Shell'
import { AddKey } from '../components/AddKey'
import { AddSubscription } from '../components/AddSubscription'
import { useApi } from '../lib/useApi'
import { setEnabled, refreshItem, removeItem, call } from '../lib/api'
import { ago } from '../lib/format'
import type { Account, Provider } from '../lib/types'

type Tab = 'keys' | 'subs'

export function Connections({ notify }: { notify: (t: string, tone?: 'ok' | 'bad') => void }) {
  const [tab, setTab] = useState<Tab>('keys')
  const providers = useApi<Provider[]>('/bridge/providers', { intervalMs: 8000 })
  const accounts = useApi<Account[]>('/bridge/accounts', { intervalMs: 8000 })
  const [adding, setAdding] = useState(false)
  const [subscribing, setSubscribing] = useState(false)
  const [confirm, setConfirm] = useState<null | { kind: 'providers' | 'accounts'; id: string; name: string }>(null)
  const [busyId, setBusyId] = useState('')

  async function run(fn: () => Promise<unknown>, ok: string) {
    try { await fn(); notify(ok) } catch (e) { notify((e as Error).message, 'bad') }
    providers.reload(); accounts.reload()
  }

  async function logoutAccount(a: Account) {
    setBusyId(a.id)
    try {
      await call('POST', `/bridge/accounts/${encodeURIComponent(a.id)}/logout`)
      notify('Вышли из подписки')
    } catch (e) {
      notify((e as Error).message, 'bad')
    } finally {
      setBusyId('')
      accounts.reload()
    }
  }

  const keyCount = providers.data?.length ?? 0
  const subCount = accounts.data?.length ?? 0
  const activeKeys = (providers.data ?? []).filter((p) => p.enabled).length
  const activeSubs = (accounts.data ?? []).filter((a) => a.enabled && !a.last_error).length

  return (
    <>
      <PageHeader
        title="Подключения"
        subtitle="Ключи и подписки, которыми пользуется шлюз. Выключение действует сразу, без перезапуска."
        actions={tab === 'keys'
          ? <Button onClick={() => setAdding(true)}>Добавить ключ</Button>
          : <Button onClick={() => setSubscribing(true)}>Добавить подписку</Button>}
      />

      <div className="conn-summary">
        <span><b>{activeKeys}</b> из {keyCount} ключей активны</span>
        <span className="sep" aria-hidden="true" />
        <span><b>{activeSubs}</b> из {subCount} подписок работают</span>
      </div>

      <div className="segmented">
        <button className={tab === 'keys' ? 'on' : ''} onClick={() => setTab('keys')}>API-ключи <span className="count">{keyCount}</span></button>
        <button className={tab === 'subs' ? 'on' : ''} onClick={() => setTab('subs')}>Подписки <span className="count">{subCount}</span></button>
      </div>

      {tab === 'keys' && (
        <div className="cards">
          {keyCount === 0 && (
            <Empty title="Ключей пока нет" text="Добавь API-ключ OpenAI, Anthropic, Gemini, OpenRouter или любого OpenAI-совместимого провайдера." action={<Button onClick={() => setAdding(true)}>Добавить ключ</Button>} />
          )}
          {(providers.data ?? []).map((p) => (
            <Card key={p.id} className={`conn ${p.enabled ? '' : 'conn-off'}`}>
              <div className="conn-head">
                <div>
                  <div className="conn-name">{p.name}</div>
                  <div className="muted small">{p.kind}{p.base_url ? ' · ' + hostOf(p.base_url) : ''}</div>
                </div>
                <Toggle label={`Включить ${p.name}`} checked={p.enabled} onChange={(v) => run(() => setEnabled('providers', p.id, v), v ? 'Включено' : 'Выключено')} />
              </div>
              <div className="conn-meta">
                {p.enabled ? <Badge tone="ok">Активен</Badge> : <Badge tone="neutral">Выключен</Badge>}
                {p.support_level && <Badge tone={p.support_level === 'native' ? 'info' : 'neutral'}>{levelLabel(p.support_level)}</Badge>}
                {p.auth_type && <span className="muted small">{p.auth_type}</span>}
              </div>
              <div className="conn-actions">
                <Button variant="ghost" size="sm" onClick={() => run(() => refreshItem('providers', p.id), 'Модели обновлены')}>Обновить модели</Button>
                <Button variant="danger" size="sm" onClick={() => setConfirm({ kind: 'providers', id: p.id, name: p.name })}>Удалить</Button>
              </div>
            </Card>
          ))}
        </div>
      )}

      {tab === 'subs' && (
        <div className="cards">
          {subCount === 0 && (
            <Empty title="Подписок пока нет" text="Подключи подписку ChatGPT / Codex или Gemini. Вход выполняется на официальной странице провайдера." action={<Button onClick={() => setSubscribing(true)}>Добавить подписку</Button>} />
          )}
          {(accounts.data ?? []).map((a) => (
            <Card key={a.id} className={`conn ${a.enabled ? '' : 'conn-off'}`}>
              <div className="conn-head">
                <div>
                  <div className="conn-name">{a.name}</div>
                  <div className="muted small">{a.email || 'аккаунт не определён'}{a.plan ? ' · ' + a.plan : ''}</div>
                </div>
                <Toggle label={`Включить ${a.name}`} checked={a.enabled} onChange={(v) => run(() => setEnabled('accounts', a.id, v), v ? 'Включено' : 'Выключено')} />
              </div>
              <div className="conn-meta">
                {a.last_error ? <Badge tone="bad">Ошибка</Badge> : a.enabled ? <Badge tone="ok">Активна</Badge> : <Badge tone="neutral">Выключена</Badge>}
                <span className="muted small">проверено {ago(a.last_connected_at)}</span>
              </div>
              {a.last_error && <div className="error-line">{a.last_error}</div>}
              <div className="conn-actions">
                <Button variant="ghost" size="sm" disabled={busyId === a.id} onClick={() => run(() => refreshItem('accounts', a.id), 'Статус обновлён')}>Проверить</Button>
                <Button variant="ghost" size="sm" disabled={busyId === a.id} onClick={() => logoutAccount(a)}>Выйти</Button>
                <Button variant="danger" size="sm" onClick={() => setConfirm({ kind: 'accounts', id: a.id, name: a.name })}>Удалить</Button>
              </div>
            </Card>
          ))}
        </div>
      )}

      {adding && (
        <AddKey
          onClose={() => setAdding(false)}
          onDone={(n) => { setAdding(false); providers.reload(); notify(`Ключ «${n}» добавлен`) }}
        />
      )}

      {subscribing && (
        <AddSubscription
          notify={notify}
          onClose={() => { setSubscribing(false); accounts.reload() }}
          onDone={() => { accounts.reload() }}
        />
      )}

      {confirm && (
        <Modal
          danger
          title={`Удалить «${confirm.name}»?`}
          text={confirm.kind === 'accounts' ? 'Подписка будет отключена и выйдет из системы. Вход придётся выполнить заново.' : 'Ключ будет удалён из шлюза. Клиенты, которые его использовали, перестанут получать модели.'}
          confirmLabel="Удалить"
          onClose={() => setConfirm(null)}
          onConfirm={() => { const c = confirm; setConfirm(null); run(() => removeItem(c.kind, c.id), 'Удалено') }}
        />
      )}
    </>
  )
}

function hostOf(u: string) {
  try { return new URL(u).host } catch { return u }
}

function levelLabel(l: string) {
  const map: Record<string, string> = { native: 'Native', preset: 'Preset', local: 'Local', template: 'Шаблон', reserved: 'Скоро', custom: 'Custom' }
  return map[l] ?? l
}
