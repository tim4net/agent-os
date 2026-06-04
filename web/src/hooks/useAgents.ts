import { useEffect, useState, useCallback } from 'react'
import type { Agent } from '../api/client'
import { listAgents } from '../api/client'
import { useSSE } from './useSSE'

export function useAgents() {
  const [agents, setAgents] = useState<Agent[]>([])
  const [loading, setLoading] = useState(true)
  const { lastEvent } = useSSE()

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listAgents()
      setAgents(data)
    } catch (err) {
      console.error('Failed to fetch agents:', err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await
    refresh()
  }, [refresh])

  useEffect(() => {
    if (!lastEvent) return
    if (lastEvent.type === 'agent_status_changed') {
      const data = lastEvent.data as { agent_id: string; status: string; last_seen?: string }
      // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing state from external SSE event, not a render-derived value
      setAgents((prev) =>
        prev.map((a) =>
          a.id === data.agent_id
            ? { ...a, status: data.status, last_seen: data.last_seen ?? a.last_seen }
            : a,
        ),
      )
    }
  }, [lastEvent])

  return { agents, loading, refresh }
}
