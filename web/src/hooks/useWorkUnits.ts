import { useEffect, useState, useCallback } from 'react'
import type { WorkUnit } from '../api/client'
import { getWorkUnits } from '../api/client'
import { useSSE } from './useSSE'

/** Liveness is derived SERVER-SIDE (received_at is the only liveness clock, contract §4)
 *  and delivered on the WorkUnit as `liveness`. The UI renders it directly — it does NOT
 *  recompute from a browser clock. This type mirrors the server's vocabulary. */
export type Liveness = 'running' | 'done' | 'stale' | 'failed'

/** Normalize the server liveness string to a known state (defensive default: done). */
export function livenessOf(unit: WorkUnit): Liveness {
  switch (unit.liveness) {
    case 'failed': return 'failed'
    case 'running': return 'running'
    case 'stale': return 'stale'
    default: return 'done'
  }
}

interface UseWorkUnits {
  units: WorkUnit[]
  loading: boolean
  error: string | null
  refresh: () => void
}

// Periodic refetch heals two things the SSE stream can't guarantee on its own:
//  - the EventBus drops same-type events within a short window, so a fast start->end can
//    lose the terminal event (review finding B4);
//  - 'stale' is time-based (server clock), so a unit can age into stale with no new event.
const REFRESH_INTERVAL_MS = 20_000

export function useWorkUnits(tenant: string, limit = 100): UseWorkUnits {
  const [units, setUnits] = useState<WorkUnit[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const { lastEvent } = useSSE()

  const refresh = useCallback(() => {
    getWorkUnits({ tenant, limit, offset: 0 })
      .then((res) => {
        setUnits(res.work_units ?? [])
        setError(null)
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'failed to load work units'))
      .finally(() => setLoading(false))
  }, [tenant, limit])

  // initial load + reload when tenant changes. `loading` starts true; we don't toggle it
  // back on for refetches (avoids a setState-in-effect and a flicker — stale data shows
  // until the new data arrives, which is the desired live-dashboard behavior).
  useEffect(() => {
    refresh()
  }, [refresh])

  // refetch when a work_event arrives over SSE (best-effort; may be throttled — B4)
  useEffect(() => {
    if (lastEvent?.type === 'work_event') {
      refresh()
    }
  }, [lastEvent, refresh])

  // periodic refetch: heals dropped terminal SSE events + recomputes server-side 'stale'
  useEffect(() => {
    const id = setInterval(refresh, REFRESH_INTERVAL_MS)
    return () => clearInterval(id)
  }, [refresh])

  return { units, loading, error, refresh }
}
