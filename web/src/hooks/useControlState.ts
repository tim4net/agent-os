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

  const refetch = useCallback(() => {
    fetch('/api/control/state')
      .then((res) => {
        if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
        return res.json()
      })
      .then((data: ControlState) => {
        if (mountedRef.current) {
          setState(data)
          setError(null)
        }
      })
      .catch((e: unknown) => {
        if (mountedRef.current) {
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
    }
  }, [refetch])

  return { state, loading, error, refetch }
}
