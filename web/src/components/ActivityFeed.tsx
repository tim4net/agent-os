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

function eventColor(type: string): string {
  switch (type) {
    case 'agent_status':
      return 'bg-emerald-400/80'
    case 'chat':
      return 'bg-blue-400/80'
    case 'artifact':
      return 'bg-amber-400/80'
    case 'task':
      return 'bg-purple-400/80'
    default:
      return 'bg-gray-400/60'
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

function mapSSEToActivity(event: SSEEvent): ActivityEvent | null {
  // Filter out noisy background events
  const ignoredTypes = ['memory_indexed', 'keepalive', 'connected']
  if (ignoredTypes.includes(event.type)) return null

  // Filter litellm status events — it's infrastructure, not an agent
  const data = event.data as Record<string, unknown>
  const summary = (data['summary'] as string) ?? (data['message'] as string) ?? ''
  if (summary.toLowerCase().includes('litellm')) return null

  const typeMap: Record<string, ActivityEvent['type']> = {
    agent_status_changed: 'agent_status',
    chat_message: 'chat',
    artifact_created: 'artifact',
    task_updated: 'task',
  }
  return {
    id: crypto.randomUUID(),
    type: typeMap[event.type] ?? 'other',
    summary: summary || event.type,
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
      // Filter out noisy background events from initial load
      const filtered = (Array.isArray(data) ? data : []).filter(
        (e) => {
          if (e.type === 'other' && e.summary?.toLowerCase().includes('memory indexed')) return false
          if (e.summary?.toLowerCase().includes('litellm')) return false
          return true
        }
      )
      setEvents(filtered)
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
        if (!activity) return // skip filtered events
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
      <div className="space-y-3 stagger-children">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="flex items-start gap-3 animate-pulse pl-4">
            <div className="w-5 h-5 bg-[var(--bg-elevated)] rounded-full shrink-0" />
            <div className="flex-1">
              <div className="h-4 bg-[var(--bg-elevated)] rounded w-3/4 mb-1" />
              <div className="h-3 bg-[var(--bg-elevated)] rounded w-1/4" />
            </div>
          </div>
        ))}
      </div>
    )
  }

  if (events.length === 0) {
    return (
      <p className="text-[var(--color-text-muted)] text-sm py-8 text-center">No activity yet. Events will appear here as they occur.</p>
    )
  }

  return (
    <div className="relative max-h-[70vh] overflow-y-auto pr-1">
      {/* Timeline left border line */}
      <div className="absolute left-[7px] top-2 bottom-2 w-px bg-[var(--color-border-subtle)]/40" />

      <div className="space-y-1 stagger-children">
        {events.map((event) => (
          <button
            key={event.id}
            onClick={() => handleClick(event)}
            className="fade-in w-full flex items-center gap-3 pl-4 pr-2 py-2 rounded-lg hover:bg-[var(--bg-elevated)]/30 transition-colors text-left group"
          >
            {/* Colored circle icon */}
            <span className={`relative z-10 inline-block w-3.5 h-3.5 rounded-full shrink-0 ${eventColor(event.type)} ring-2 ring-[var(--bg-base)]`} />

            {/* Summary */}
            <div className="flex-1 min-w-0">
              <p className="text-sm text-[var(--color-text-secondary)] group-hover:text-[var(--color-text-primary)] transition-colors truncate">
                {event.summary}
              </p>
            </div>

            {/* Timestamp on the right */}
            <span className="text-[10px] text-[var(--color-text-muted)]/50 shrink-0 ml-2 tabular-nums">
              {relativeTime(event.timestamp)}
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}
