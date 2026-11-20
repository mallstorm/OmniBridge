import { useMemo, useState } from 'react'
import { Button, SectionHead } from './ui'
import { call } from '../lib/api'

type Preset = { id: string; name: string; baseURL: string; hint: string }

// Known providers. Base URL is pre-filled; the user can still change it.
export const PRESETS: Preset[] = [
  { id: 'openai', name: 'OpenAI', baseURL: 'https://api.openai.com/v1', hint: 'Ключ начинается с sk-' },
  { id: 'anthropic', name: 'Anthropic', baseURL: 'https://api.anthropic.com/v1', hint: 'Ключ начинается с sk-ant-' },
  { id: 'gemini', name: 'Gemini (API key)', baseURL: 'https://generativelanguage.googleapis.com/v1beta/openai', hint: 'Ключ из Google AI Studio' },
  { id: 'openrouter', name: 'OpenRouter', baseURL: 'https://openrouter.ai/api/v1', hint: 'Ключ начинается с sk-or-' },
  { id: 'deepseek', name: 'DeepSeek', baseURL: 'https://api.deepseek.com/v1', hint: 'Ключ начинается с sk-' },
  { id: 'custom', name: 'Свой провайдер', baseURL: '', hint: 'Любой OpenAI-совместимый endpoint' },
]

export function validateKey(name: string, baseURL: string, apiKey: string): string | null {
  if (!name.trim()) return 'Укажи название, чтобы отличать ключ в списке'
  if (!baseURL.trim()) return 'Укажи Base URL провайдера'
  try {
    const u = new URL(baseURL.trim())
    if (u.protocol !== 'https:' && u.protocol !== 'http:') return 'Base URL должен начинаться с https://'
  } catch {
    return 'Base URL выглядит некорректно'
  }
  if (apiKey.trim().length < 8) return 'Ключ слишком короткий — проверь, что вставил целиком'
  if (/\s/.test(apiKey.trim())) return 'В ключе есть пробелы или переносы строк'
  return null
}

export function AddKey({ onClose, onDone }: { onClose: () => void; onDone: (name: string) => void }) {
  const [preset, setPreset] = useState<string>('openai')
  const [name, setName] = useState(PRESETS[0].name)
  const [baseURL, setBaseURL] = useState(PRESETS[0].baseURL)
  const [apiKey, setApiKey] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const current = PRESETS.find((p) => p.id === preset) ?? PRESETS[0]
  const problem = useMemo(() => (apiKey ? validateKey(name, baseURL, apiKey) : null), [name, baseURL, apiKey])

  function pick(p: Preset) {
    setPreset(p.id)
    if (p.id !== 'custom') {
      setName(p.name)
      setBaseURL(p.baseURL)
    } else {
      setName('')
      setBaseURL('')
    }
    setError('')
  }

  async function submit() {
    const bad = validateKey(name, baseURL, apiKey)
    if (bad) return setError(bad)
    setBusy(true)
    setError('')
    try {
      await call('POST', '/bridge/providers', { name: name.trim(), kind: 'auto', base_url: baseURL.trim(), api_key: apiKey.trim() })
      setApiKey('')
      onDone(name.trim())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal modal-wide" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <SectionHead kicker="Новый API-ключ" title="Подключить провайдера" />

        <div className="preset-grid" role="radiogroup" aria-label="Провайдер">
          {PRESETS.map((p) => (
            <button
              key={p.id}
              type="button"
              role="radio"
              aria-checked={preset === p.id}
              className={`preset ${preset === p.id ? 'on' : ''}`}
              onClick={() => pick(p)}
            >
              {p.name}
            </button>
          ))}
        </div>

        <div className="form">
          <label>Название
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Например, OpenRouter (личный)" />
          </label>
          <label>Base URL
            <input value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://…/v1" spellCheck={false} />
          </label>
          <label>API-ключ
            <span className="key-field">
              <input
                type={reveal ? 'text' : 'password'}
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={current.hint}
                autoComplete="off"
                spellCheck={false}
              />
              <button type="button" className="key-reveal" onClick={() => setReveal((v) => !v)}>{reveal ? 'Скрыть' : 'Показать'}</button>
            </span>
          </label>
        </div>

        {(error || (apiKey && problem)) && <div className="error-line">{error || problem}</div>}

        <p className="muted small">
          Ключ сохраняется в защищённом хранилище и больше не показывается в интерфейсе. Наружу, к клиентам шлюза, он не передаётся.
        </p>

        <div className="modal-actions">
          <Button variant="ghost" onClick={onClose}>Отмена</Button>
          <Button disabled={busy || !apiKey || !!problem} onClick={submit}>{busy ? 'Проверяем…' : 'Подключить'}</Button>
        </div>
      </div>
    </div>
  )
}
