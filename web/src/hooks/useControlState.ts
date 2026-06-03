import { useEffect, useState, useCallback, useRef } from 'react'

export interface ControlState {
  mode: 'continuous' | 'tick' | 'stopped'
  cadence_seconds: number
  queue_counts: Record<string, number>
  updated_at: string
}

interface UseControlState {
  state: ControlState | null
  loading: boolean
  error: string | null
  refetch: () => void
}

const POLL_INTERVAL_MS = 5_000

export function useControlState(): UseControlState {
  const [state, setState] = useState<ControlState | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)
  // AbortController ref — shared between refetch and the effect cleanup.
  // Each fetch gets a fresh signal; the previous fetch is aborted so a stale
  // /state response can never overwrite the current state.
  const acRef = useRef<AbortController | null>(null)
  // Monotonic request-id guard: only the response matching the latest request
  // is allowed to update state. Belt-and-suspenders with AbortController.
  const requestIdRef = useRef(0)

  const refetch = useCallback(() => {
    // Abort the previous in-flight request (if any).
    acRef.current?.abort()
    const ac = new AbortController()
    acRef.current = ac
    const signal = ac.signal
    const requestId = ++requestIdRef.current

    fetch('/api/control/state', { signal })
      .then((res) => {
        if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
        return res.json()
      })
      .then((data: ControlState) => {
        if (mountedRef.current && requestIdRef.current === requestId) {
          setState(data)
          setError(null)
        }
      })
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        if (mountedRef.current && requestIdRef.current === requestId) {
          setError(e instanceof Error ? e.message : 'Failed to fetch control state')
        }
      })
      .finally(() => {
        if (mountedRef.current) setLoading(false)
      })
  }, [])

  useEffect(() => {
    mountedRef.current = true
    refetch()
    const id = setInterval(refetch, POLL_INTERVAL_MS)
    return () => {
      mountedRef.current = false
      clearInterval(id)
      acRef.current?.abort()
    }
  }, [refetch])

  return { state, loading, error, refetch }
}
