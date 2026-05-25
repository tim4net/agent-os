import { useEffect, useState, useCallback } from 'react'
import type { SSEEvent } from '../api/client'
import { getActivity, createEventSource } from '../api/client'

export interface ActivityEvent {
  id: string
  type: 'agent_status' | 'chat' | 'artifact' | 'task' | 'other'
  summary: string
  timestamp: string
  target?: string
}

function eventIcon(type: string): string {
  switch (type) {
    case 'agent_status':
      return '🟢'
    case 'chat':
      return '💬'
    case 'artifact':
      return '📎'
    case 'task':
      return '✅'
    default:
      return '•'
  }
}

function relativeTime(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return 'Just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

function mapSSEToActivity(event: SSEEvent): ActivityEvent {
  const data = event.data as Record<string, unknown>
  const typeMap: Record<string, ActivityEvent['type']> = {
    agent_status_changed: 'agent_status',
    chat_message: 'chat',
    artifact_created: 'artifact',
    task_updated: 'task',
  }
  return {
    id: crypto.randomUUID(),
    type: typeMap[event.type] ?? 'other',
    summary: (data['summary'] as string) ?? (data['message'] as string) ?? event.type,
    timestamp: new Date().toISOString(),
    target: (data['target'] as string) ?? (data['tab'] as string),
  }
}

interface ActivityFeedProps {
  onNavigate?: (tab: string) => void
}

export function ActivityFeed({ onNavigate }: ActivityFeedProps) {
  const [events, setEvents] = useState<ActivityEvent[]>([])
  const [loading, setLoading] = useState(true)

  const loadEvents = useCallback(async () => {
    try {
      const data = await getActivity(50, 0)
      setEvents(Array.isArray(data) ? data : [])
    } catch {
      // backend may not return events yet, use empty
      setEvents([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadEvents()
  }, [loadEvents])

  // SSE auto-refresh
  useEffect(() => {
    const es = createEventSource()
    es.onmessage = (e) => {
      try {
        const parsed = JSON.parse(e.data) as SSEEvent
        const activity = mapSSEToActivity(parsed)
        setEvents((prev) => [activity, ...prev].slice(0, 200))
      } catch {
        // ignore malformed
      }
    }
    es.onerror = () => {
      // auto-reconnect handled by EventSource
    }
    return () => es.close()
  }, [])

  function handleClick(event: ActivityEvent) {
    if (!onNavigate || !event.target) return
    const targetMap: Record<string, string> = {
      Chat: 'Chat',
      Workspace: 'Workspace',
      Kanban: 'Kanban',
      Studio: 'Studio',
      Memory: 'Memory',
      Goals: 'Goals',
      Pipeline: 'Pipeline',
    }
    const tab = targetMap[event.target] ?? event.target
    onNavigate(tab)
  }

  if (loading) {
    return (
      <div className="space-y-3">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="flex items-start gap-3 animate-pulse">
            <div className="w-6 h-6 bg-gray-800 rounded-full shrink-0" />
            <div className="flex-1">
              <div className="h-4 bg-gray-800 rounded w-3/4 mb-1" />
              <div className="h-3 bg-gray-800 rounded w-1/4" />
            </div>
          </div>
        ))}
      </div>
    )
  }

  if (events.length === 0) {
    return (
      <p className="text-gray-500 text-sm py-8 text-center">No activity yet. Events will appear here as they occur.</p>
    )
  }

  return (
    <div className="space-y-1 max-h-[70vh] overflow-y-auto pr-1">
      {events.map((event) => (
        <button
          key={event.id}
          onClick={() => handleClick(event)}
          className="w-full flex items-start gap-3 px-3 py-2.5 rounded-lg hover:bg-gray-800/50 transition-colors text-left group"
        >
          <span className="text-base shrink-0 mt-0.5">{eventIcon(event.type)}</span>
          <div className="flex-1 min-w-0">
            <p className="text-sm text-gray-200 group-hover:text-white transition-colors truncate">{event.summary}</p>
            <p className="text-xs text-gray-500 mt-0.5">{relativeTime(event.timestamp)}</p>
          </div>
        </button>
      ))}
    </div>
  )
}
