import { Card, Button, SectionHead, Badge } from '../components/ui'
import { PageHeader } from '../components/Shell'
import { call } from '../lib/api'

type Props = { endpoint: string; keyMasked: string; notify: (t: string, tone?: 'ok' | 'bad') => void }

export function Integrations({ endpoint, keyMasked, notify }: Props) {
  async function copyVSCodeConfig() {
    try {
      const body = await call<unknown>('GET', '/bridge/vscode/export')
      await window.hub.copy({ what: 'text', text: JSON.stringify(body, null, 2) })
      notify('Конфиг VS Code скопирован. Вставь его в chatLanguageModels.json')
    } catch (e) {
      notify((e as Error).message, 'bad')
    }
  }

  async function installVSCode(edition: 'stable' | 'insiders') {
    try {
      const r = await window.hub.vscodeInstall({ edition })
      if (r.cancelled) return
      if (!r.ok) return notify(r.error || 'Не удалось записать конфиг', 'bad')
      notify(`Записано моделей: ${r.models}. В VS Code выполни «Developer: Reload Window»`)
    } catch (e) {
      notify((e as Error).message, 'bad')
    }
  }

  return (
    <>
      <PageHeader title="Интеграции" subtitle="Подключи IDE и расширения один раз — дальше модели обновляются сами" />
      <div className="cards-2">
        <Card glow>
          <SectionHead kicker="Рекомендуется" title="GitHub Copilot Chat (VS Code)" aside={<Badge tone="info">BYOK</Badge>} />
          <ol className="steps">
            <li>Нажми «Скопировать конфиг» — OmniHub подготовит список моделей.</li>
            <li>В VS Code открой <code>Chat: Manage Language Models</code> и выбери <b>Custom Endpoint</b>, либо вставь конфиг в <code>chatLanguageModels.json</code>.</li>
            <li>Ключ остаётся в хранилище VS Code и не попадает в файлы проекта.</li>
            <li>Назначь вспомогательную модель в <code>chat.utilityModel</code>, иначе часть функций уйдёт на встроенные модели Copilot.</li>
          </ol>
          <div className="row-actions">
            <Button onClick={() => installVSCode('stable')}>Записать в VS Code</Button>
            <Button variant="ghost" onClick={() => installVSCode('insiders')}>Insiders</Button>
            <Button variant="ghost" onClick={copyVSCodeConfig}>Скопировать конфиг</Button>
          </div>
        </Card>

        <Card>
          <SectionHead kicker="Универсально" title="Cline · Kilo Code · любой OpenAI-совместимый клиент" />
          <div className="kv"><span className="muted">API Provider</span><code>OpenAI Compatible</code></div>
          <div className="kv"><span className="muted">Base URL</span><code>{endpoint}</code></div>
          <div className="kv"><span className="muted">API Key</span><code>{keyMasked || '—'}</code></div>
          <p className="muted small">Model ID подтянется автоматически из списка моделей. Ключ шлюза не равен ключам провайдеров — наружу они не передаются.</p>
          <div className="row-actions">
            <Button variant="ghost" onClick={() => window.hub.copy({ what: 'endpoint' })}>Копировать endpoint</Button>
            <Button variant="ghost" onClick={() => window.hub.copy({ what: 'key' })}>Копировать ключ</Button>
          </div>
        </Card>
      </div>

      <Card>
        <SectionHead kicker="Скоро" title="Другие клиенты" />
        <div className="chips">
          {['Cursor', 'Continue', 'Claude Code', 'Codex CLI', 'Open WebUI', 'Свой скрипт'].map((n) => (
            <span key={n} className="chip muted-chip">{n}</span>
          ))}
        </div>
      </Card>
    </>
  )
}
