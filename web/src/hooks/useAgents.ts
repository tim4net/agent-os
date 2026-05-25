import { useEffect, useState } from 'react'
import type { Agent } from '../api/client'
import { listAgents } from '../api/client'
import { useSSE } from './useSSE'

export function useAgents() {
  const [agents, setAgents] = useState<Agent[]>([])
  const [loading, setLoading] = useState(true)
  const { lastEvent } = useSSE()

  useEffect(() => {
    listAgents()
      .then(setAgents)
      .catch((err) => console.error('Failed to fetch agents:', err))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    if (!lastEvent) return
    if (lastEvent.type === 'agent_status_changed') {
      const data = lastEvent.data as { agent_id: string; status: string; last_seen?: string }
      setAgents((prev) =>
        prev.map((a) =>
          a.id === data.agent_id
            ? { ...a, status: data.status, last_seen: data.last_seen ?? a.last_seen }
            : a,
        ),
      )
    }
  }, [lastEvent])

  return { agents, loading }
}
