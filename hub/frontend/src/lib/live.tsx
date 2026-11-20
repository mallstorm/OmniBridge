import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import type { Counter, LiveFrame } from '../hub'

export type LivePoint = { t: number; tps: number; total: number }

export type LiveState = {
  connected: boolean
  frame: LiveFrame | null
  history: LivePoint[]      // tokens per second, last 120 samples
  tokensPerSec: number
  requestsPerMin: number
}

const LiveCtx = createContext<LiveState>({ connected: false, frame: null, history: [], tokensPerSec: 0, requestsPerMin: 0 })
const MAX = 120

// One subscription for the whole app. Tokens/sec and requests/min are derived
// from successive session totals, so the chart is real traffic, not a simulation.
export function LiveProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<LiveState>({ connected: false, frame: null, history: [], tokensPerSec: 0, requestsPerMin: 0 })
  const prev = useRef<{ at: number; total: Counter } | null>(null)

  useEffect(() => {
    const off = window.hub.onLive((f) => {
      const p = prev.current
      let tps = 0
      let rpm = 0
      if (p && f.at > p.at) {
        const dt = (f.at - p.at) / 1000
        tps = Math.max(0, (f.total.total_tokens - p.total.total_tokens) / dt)
        rpm = Math.max(0, ((f.total.requests - p.total.requests) / dt) * 60)
      }
      prev.current = { at: f.at, total: f.total }
      setState((s) => {
        const history = [...s.history, { t: f.at, tps, total: f.total.total_tokens }].slice(-MAX)
        return { connected: true, frame: f, history, tokensPerSec: tps, requestsPerMin: rpm }
      })
    })
    return off
  }, [])

  return <LiveCtx.Provider value={state}>{children}</LiveCtx.Provider>
}

export function useLive() {
  return useContext(LiveCtx)
}
