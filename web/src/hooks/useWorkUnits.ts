import { useEffect, useState, useCallback } from 'react'
import type { WorkUnit } from '../api/client'
import { getWorkUnits } from '../api/client'
import { useSSE } from './useSSE'

/** Liveness is COMPUTED from the event stream + server clock, never a stored flag (F10).
 *  - failed:  latest terminal status is failed
 *  - done:    latest terminal status is done/cancelled
 *  - stale:   not terminal, but no event within the heartbeat window (presumed dead)
 *  - running: not terminal, recent activity
 */
export type Liveness = 'running' | 'done' | 'stale' | 'failed'

const STALE_AFTER_MS = 5 * 60 * 1000 // 5-min heartbeat window (contract §4)

export function deriveLiveness(unit: WorkUnit, now: number = Date.now()): Liveness {
  const status = (unit.latest_status || '').toLowerCase()
  if (status === 'failed') return 'failed'
  if (status === 'done' || status === 'cancelled') return 'done'
  // Non-terminal: decide running vs stale from the last event time.
  const last = Date.parse(unit.last_event_at)
  if (!Number.isNaN(last) && now - last > STALE_AFTER_MS) return 'stale'
  return 'running'
}

interface UseWorkUnits {
  units: WorkUnit[]
  loading: boolean
  error: string | null
  refresh: () => void
  /** server clock proxy: ticks every 30s so stale-derivation re-renders without new events */
  now: number
}

export function useWorkUnits(limit = 50): UseWorkUnits {
  const [units, setUnits] = useState<WorkUnit[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const { lastEvent } = useSSE()

  const refresh = useCallback(() => {
    getWorkUnits(limit, 0)
      .then((res) => {
        setUnits(res.work_units ?? [])
        setError(null)
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'failed to load work units'))
      .finally(() => setLoading(false))
  }, [limit])

  // initial load
  useEffect(() => {
    refresh()
  }, [refresh])

  // refetch when a work_event arrives over SSE
  useEffect(() => {
    if (lastEvent?.type === 'work_event') {
      refresh()
    }
  }, [lastEvent, refresh])

  // tick the clock so 'stale' is recomputed even with no new events
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(id)
  }, [])

  return { units, loading, error, refresh, now }
}
