import { useEffect, useState, useCallback, useRef } from 'react'

export interface ControlUnit {
  id: number
  wp_ref: string
  status: 'queued' | 'in_flight' | 'done' | 'failed'
  created_at: string
  claimed_at?: string | null
  completed_at?: string | null
  error?: string | null
}

/** Matches the real WP-O2 UnitListResponse envelope. */
interface UnitListResponse {
  units: ControlUnit[]
  count: number
  limit: number
  offset: number
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
  // AbortController ref — shared between refetch and the effect cleanup.
  // Each fetch gets a fresh signal; the previous fetch is aborted so a stale
  // filter response can never overwrite the current state.
  const acRef = useRef<AbortController | null>(null)
  // Monotonic request-id guard: only the response matching the latest status
  // is allowed to update state. This prevents a race where an older filtered
  // request resolves after a newer one (belt-and-suspenders with AbortController).
  const requestIdRef = useRef(0)

  const refetch = useCallback(() => {
    // Abort the previous in-flight request (if any).
    acRef.current?.abort()
    const ac = new AbortController()
    acRef.current = ac
    const signal = ac.signal

    // Capture the current status in a closure-local snapshot so the .then
    // can validate it matches what the caller expects.
    const currentStatus = status
    const requestId = ++requestIdRef.current

    const params = new URLSearchParams()
    if (currentStatus) params.set('status', currentStatus)
    const qs = params.toString()
    fetch(`/api/control/units${qs ? `?${qs}` : ''}`, { signal })
      .then((res) => {
        if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
        return res.json()
      })
      .then((data: UnitListResponse) => {
        // Staleness guard: only update if this is still the latest request
        // and the component is still mounted.
        if (mountedRef.current && requestIdRef.current === requestId) {
          setUnits(data.units ?? [])
          setError(null)
        }
      })
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        if (mountedRef.current && requestIdRef.current === requestId) {
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
      acRef.current?.abort()
    }
  }, [refetch])

  return { units, loading, error, refetch }
}
