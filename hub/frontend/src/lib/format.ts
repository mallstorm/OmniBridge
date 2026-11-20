export function num(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  const abs = Math.abs(n)
  if (abs >= 1e9) return (n / 1e9).toFixed(2).replace(/\.?0+$/, '') + ' млрд'
  if (abs >= 1e6) return (n / 1e6).toFixed(2).replace(/\.?0+$/, '') + ' млн'
  if (abs >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, '') + ' тыс'
  return Math.round(n).toLocaleString('ru-RU')
}

export function pct(part: number, total: number): string {
  if (!total) return '—'
  return Math.round((part / total) * 100) + '%'
}

export function ago(iso?: string): string {
  if (!iso) return 'никогда'
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return 'только что'
  if (s < 3600) return Math.floor(s / 60) + ' мин назад'
  if (s < 86400) return Math.floor(s / 3600) + ' ч назад'
  return Math.floor(s / 86400) + ' дн назад'
}
