import { useEffect, useState, useCallback, useRef } from 'react'

export interface ControlUnit {
  id: string
  wp_ref: string
  status: 'queued' | 'running' | 'done' | 'failed'
  created_at: string
  updated_at: string
  error: string | null
  result: unknown
}

interface UseControlUnits {
  units: ControlUnit[]
  loading: boolean
  error: string | null
  refetch: () => void
}

const POLL_INTERVAL_MS = 5_000

export function useControlUnits(status?: string): UseControlUnits {
  const [units, setUnits] = useState<ControlUnit[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)

  const refetch = useCallback(() => {
    const params = new URLSearchParams()
    if (status) params.set('status', status)
    const qs = params.toString()
    fetch(`/api/control/units${qs ? `?${qs}` : ''}`)
      .then((res) => {
        if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
        return res.json()
      })
      .then((data: ControlUnit[]) => {
        if (mountedRef.current) {
          setUnits(data)
          setError(null)
        }
      })
      .catch((e: unknown) => {
        if (mountedRef.current) {
          setError(e instanceof Error ? e.message : 'Failed to fetch control units')
        }
      })
      .finally(() => {
        if (mountedRef.current) setLoading(false)
      })
  }, [status])

  useEffect(() => {
    mountedRef.current = true
    refetch()
    const id = setInterval(refetch, POLL_INTERVAL_MS)
    return () => {
      mountedRef.current = false
      clearInterval(id)
    }
  }, [refetch])

  return { units, loading, error, refetch }
}
