import { useEffect, useRef, useState, useCallback } from 'react'
import { getAgentVersion, type AgentVersion } from '../api/agentVersion'

export interface VersionState {
  version: AgentVersion | null
  loading: boolean
  /** Transport/HTTP failure (NOT an "unknown" version, which is a valid 200). */
  error: string | null
}

const EMPTY: VersionState = { version: null, loading: true, error: null }

/**
 * Probe versions for a set of agent IDs, keyed by ID.
 *
 * Each agent is probed independently so one slow/failed probe never blocks the
 * others (the backend probe can take up to ~10s for an offline gateway). A
 * resolved value with source 'unknown' is a normal outcome, not an error.
 *
 * The probe is on-demand and mildly expensive (it can open a websocket to a
 * gateway), so this does NOT poll — it fetches once per id-set change and
 * exposes refresh() for an explicit re-check.
 */
export function useAgentVersions(agentIds: string[]): {
  versions: Record<string, VersionState>
  refresh: () => void
} {
  const [versions, setVersions] = useState<Record<string, VersionState>>({})
  const mountedRef = useRef(true)
  const acRef = useRef<AbortController | null>(null)

  // Stable key so the effect only re-runs when the actual set of ids changes,
  // not on every render that produces a new array identity.
  const idsKey = [...agentIds].sort().join(',')

  const run = useCallback(() => {
    acRef.current?.abort()
    const ac = new AbortController()
    acRef.current = ac
    const ids = idsKey ? idsKey.split(',') : []

    // Seed every id as loading so the UI shows a pending chip immediately.
    setVersions((prev) => {
      const next: Record<string, VersionState> = {}
      for (const id of ids) next[id] = prev[id] ?? { ...EMPTY }
      return next
    })

    for (const id of ids) {
      getAgentVersion(id, ac.signal)
        .then((version) => {
          if (!mountedRef.current || ac.signal.aborted) return
          setVersions((prev) => ({ ...prev, [id]: { version, loading: false, error: null } }))
        })
        .catch((e: unknown) => {
          if (e instanceof DOMException && e.name === 'AbortError') return
          if (!mountedRef.current) return
          setVersions((prev) => ({
            ...prev,
            [id]: {
              version: null,
              loading: false,
              error: e instanceof Error ? e.message : 'Failed to fetch version',
            },
          }))
        })
    }
  }, [idsKey])

  useEffect(() => {
    mountedRef.current = true
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async probe fan-out; setState lands after fetch, seeding loading state is intentional
    run()
    return () => {
      mountedRef.current = false
      acRef.current?.abort()
    }
  }, [run])

  return { versions, refresh: run }
}
