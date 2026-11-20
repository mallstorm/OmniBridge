import { useEffect, type ReactNode } from 'react'

export function Card({ children, className = '', glow = false }: { children: ReactNode; className?: string; glow?: boolean }) {
  return <section className={`card ${glow ? 'card-glow' : ''} ${className}`}>{children}</section>
}

export function SectionHead({ kicker, title, aside }: { kicker?: string; title: string; aside?: ReactNode }) {
  return (
    <div className="section-head">
      <div>
        {kicker && <div className="kicker">{kicker}</div>}
        <h3>{title}</h3>
      </div>
      {aside}
    </div>
  )
}

export function Badge({ tone = 'neutral', children }: { tone?: 'ok' | 'warn' | 'bad' | 'info' | 'neutral'; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>
}

export function Button({
  variant = 'primary',
  size = 'md',
  onClick,
  disabled,
  children,
  title,
}: {
  variant?: 'primary' | 'ghost' | 'danger' | 'subtle'
  size?: 'sm' | 'md'
  onClick?: () => void
  disabled?: boolean
  children: ReactNode
  title?: string
}) {
  return (
    <button className={`btn btn-${variant} btn-${size}`} onClick={onClick} disabled={disabled} title={title}>
      {children}
    </button>
  )
}

// Hot on/off switch. The parent decides what the change does (API call + refresh).
export function Toggle({ checked, onChange, disabled, label }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      aria-label={label}
      className={`toggle ${checked ? 'on' : ''}`}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span className="toggle-knob" />
    </button>
  )
}

export function Stat({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: 'ok' | 'warn' | 'bad' | 'accent' }) {
  return (
    <div className={`stat ${tone ? 'stat-' + tone : ''}`}>
      <div className="kicker">{label}</div>
      <div className="stat-value">{value}</div>
      {hint && <div className="stat-hint">{hint}</div>}
    </div>
  )
}

export function Empty({ title, text, action }: { title: string; text: string; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      <div className="muted">{text}</div>
      {action && <div className="empty-action">{action}</div>}
    </div>
  )
}

// Sparkline / area chart for live tokens per second. Pure SVG, no chart library.
export function AreaChart({ values, height = 120, label }: { values: number[]; height?: number; label: string }) {
  const w = 600
  const h = height
  const max = Math.max(1, ...values)
  const pts = values.map((v, i) => {
    const x = values.length <= 1 ? w : (i / (values.length - 1)) * w
    const y = h - (v / max) * (h - 8) - 4
    return [x, y] as const
  })
  const line = pts.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`).join(' ')
  const area = pts.length ? `${line} L${w},${h} L0,${h} Z` : ''
  return (
    <svg className="area-chart" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" role="img" aria-label={label}>
      <defs>
        <linearGradient id="areaFill" x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor="#22d3ee" stopOpacity="0.45" />
          <stop offset="100%" stopColor="#6366f1" stopOpacity="0" />
        </linearGradient>
      </defs>
      {[0.25, 0.5, 0.75].map((g) => (
        <line key={g} x1="0" x2={w} y1={h * g} y2={h * g} className="grid-line" />
      ))}
      {area && <path d={area} fill="url(#areaFill)" />}
      {line && <path d={line} className="area-line" />}
    </svg>
  )
}

export function Bar({ value, max, tone = 'accent' }: { value: number; max: number; tone?: 'accent' | 'ok' | 'warn' }) {
  const p = max > 0 ? Math.max(2, (value / max) * 100) : 0
  return (
    <div className="bar">
      <span className={`bar-fill bar-${tone}`} style={{ width: `${p}%` }} />
    </div>
  )
}

export function Modal({ title, text, confirmLabel, onConfirm, onClose, danger }: { title: string; text: string; confirmLabel: string; onConfirm: () => void; onClose: () => void; danger?: boolean }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [onClose])
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3>{title}</h3>
        <p className="muted">{text}</p>
        <div className="modal-actions">
          <Button variant="ghost" onClick={onClose}>Отмена</Button>
          <Button variant={danger ? 'danger' : 'primary'} onClick={onConfirm}>{confirmLabel}</Button>
        </div>
      </div>
    </div>
  )
}

export function Toast({ text, tone = 'ok' }: { text: string; tone?: 'ok' | 'bad' }) {
  return <div className={`toast toast-${tone}`}>{text}</div>
}
