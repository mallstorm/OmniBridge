import { useCallback, useEffect, useRef, useState } from 'react'
import { call } from './api'

// Loads one admin resource, reloads on demand and optionally on an interval.
export function useApi<T>(path: string | null, opts: { intervalMs?: number; deps?: unknown[] } = {}) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const alive = useRef(true)

  const reload = useCallback(async () => {
    if (!path) return
    setLoading(true)
    try {
      const d = await call<T>('GET', path)
      if (alive.current) { setData(d); setError('') }
    } catch (e) {
      if (alive.current) setError((e as Error).message)
    } finally {
      if (alive.current) setLoading(false)
    }
  }, [path])

  useEffect(() => {
    alive.current = true
    reload()
    if (!opts.intervalMs) return () => { alive.current = false }
    const t = setInterval(reload, opts.intervalMs)
    return () => { alive.current = false; clearInterval(t) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reload, opts.intervalMs, ...(opts.deps ?? [])])

  return { data, error, loading, reload }
}
