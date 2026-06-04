import { useState, useEffect, useCallback } from 'react'
import type { TimelineEvent } from '../../api/client'
import { getTimeline } from '../../api/client'
import { Icon } from '../Icon'

// --- Helpers ---

function relativeTime(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return 'Just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d ago`
  return new Date(dateStr).toLocaleDateString()
}

function getDateLabel(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterday = new Date(today.getTime() - 86400000)
  const weekAgo = new Date(today.getTime() - 7 * 86400000)

  const eventDate = new Date(date.getFullYear(), date.getMonth(), date.getDate())

  if (eventDate.getTime() === today.getTime()) return 'Today'
  if (eventDate.getTime() === yesterday.getTime()) return 'Yesterday'
  if (eventDate >= weekAgo) return 'This Week'
  return eventDate.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

// --- Event Type Config ---

interface EventTypeConfig {
  icon: string
  color: string
  bgColor: string
  borderColor: string
  label: string
}

const eventTypeConfig: Record<string, EventTypeConfig> = {
  conversation: {
    icon: 'chat',
    color: 'text-blue-400',
    bgColor: 'bg-blue-500/10',
    borderColor: 'border-blue-500/30',
    label: 'Conversation',
  },
  task_completed: {
    icon: 'check_circle',
    color: 'text-green-400',
    bgColor: 'bg-green-500/10',
    borderColor: 'border-green-500/30',
    label: 'Task Completed',
  },
  artifact_created: {
    icon: 'attach_file',
    color: 'text-purple-400',
    bgColor: 'bg-purple-500/10',
    borderColor: 'border-purple-500/30',
    label: 'Artifact Created',
  },
  workflow_run: {
    icon: 'settings',
    color: 'text-orange-400',
    bgColor: 'bg-orange-500/10',
    borderColor: 'border-orange-500/30',
    label: 'Workflow Run',
  },
  delegation: {
    icon: 'refresh',
    color: 'text-cyan-400',
    bgColor: 'bg-cyan-500/10',
    borderColor: 'border-cyan-500/30',
    label: 'Delegation',
  },
}

function getEventConfig(type: string): EventTypeConfig {
  return eventTypeConfig[type] ?? {
    icon: 'circle',
    color: 'text-gray-400',
    bgColor: 'bg-gray-500/10',
    borderColor: 'border-gray-500/30',
    label: type,
  }
}

// --- Grouped Events ---

interface GroupedEvents {
  label: string
  events: TimelineEvent[]
}

function groupEvents(events: TimelineEvent[]): GroupedEvents[] {
  const groups: GroupedEvents[] = []
  let currentLabel = ''
  let currentEvents: TimelineEvent[] = []

  for (const event of events) {
    const label = getDateLabel(event.timestamp)
    if (label !== currentLabel) {
      if (currentEvents.length > 0) {
        groups.push({ label: currentLabel, events: currentEvents })
      }
      currentLabel = label
      currentEvents = []
    }
    currentEvents.push(event)
  }

  if (currentEvents.length > 0) {
    groups.push({ label: currentLabel, events: currentEvents })
  }

  return groups
}

// --- Timeline Item ---

function TimelineItem({ event, isLast, onClick }: { event: TimelineEvent; isLast: boolean; onClick?: () => void }) {
  const config = getEventConfig(event.type)
  const clickable = event.type === 'conversation' || event.type === 'artifact_created' || event.type === 'task_completed' || event.type === 'workflow_run'

  return (
    <div className="flex gap-4 group">
      {/* Timeline line and dot */}
      <div className="flex flex-col items-center flex-shrink-0">
        <div
          className={`w-9 h-9 rounded-full flex items-center justify-center text-sm ${config.bgColor} border ${config.borderColor} shrink-0`}
        >
          <Icon name={config.icon} size={14} />
        </div>
        {!isLast && (
          <div className="w-px flex-1 bg-gray-800 my-1" />
        )}
      </div>

      {/* Content */}
      <div className={`flex-1 pb-6 ${isLast ? '' : ''}`}>
        <div
          className={`bg-gray-900 border border-gray-800 rounded-lg px-4 py-3 transition-colors ${
            clickable
              ? 'cursor-pointer hover:border-gray-700 hover:bg-gray-800/50'
              : 'group-hover:border-gray-700'
          }`}
          onClick={clickable ? onClick : undefined}
          role={clickable ? 'button' : undefined}
          tabIndex={clickable ? 0 : undefined}
          onKeyDown={clickable ? (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onClick?.() } } : undefined}
        >
          <div className="flex items-start justify-between gap-3">
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 mb-1">
                <span className={`text-xs font-medium px-2 py-0.5 rounded ${config.bgColor} ${config.color}`}>
                  {config.label}
                </span>
                {event.agent_name && (
                  <span className="text-xs text-gray-500">{event.agent_name}</span>
                )}
              </div>
              <p className="text-sm text-gray-200 font-medium truncate">{event.title}</p>
              {event.description && (
                <p className="text-xs text-gray-400 mt-1 line-clamp-2">{event.description}</p>
              )}
            </div>
            <span className="text-xs text-gray-500 whitespace-nowrap flex-shrink-0 mt-1">
              {relativeTime(event.timestamp)}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

// --- Stats Bar ---

function StatsBar({ events }: { events: TimelineEvent[] }) {
  const counts = {
    conversation: events.filter((e) => e.type === 'conversation').length,
    task_completed: events.filter((e) => e.type === 'task_completed').length,
    artifact_created: events.filter((e) => e.type === 'artifact_created').length,
    workflow_run: events.filter((e) => e.type === 'workflow_run').length,
  }

  const stats = [
    { label: 'Conversations', count: counts.conversation, color: 'text-blue-400', icon: 'chat' },
    { label: 'Tasks Done', count: counts.task_completed, color: 'text-green-400', icon: 'check_circle' },
    { label: 'Artifacts', count: counts.artifact_created, color: 'text-purple-400', icon: 'attach_file' },
    { label: 'Workflows', count: counts.workflow_run, color: 'text-orange-400', icon: 'settings' },
  ]

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-6">
      {stats.map((stat) => (
        <div key={stat.label} className="bg-gray-900 border border-gray-800 rounded-lg px-4 py-3">
          <div className="flex items-center gap-2">
            <span className="text-base"><Icon name={stat.icon} size={16} /></span>
            <span className="text-lg font-bold text-gray-100">{stat.count}</span>
          </div>
          <p className="text-xs text-gray-500 mt-1">{stat.label}</p>
        </div>
      ))}
    </div>
  )
}

// --- Main Component ---

export function TimelineView({ onNavigate }: { onNavigate?: (tab: string, data?: { agentId?: string; conversationId?: string }) => void }) {
  const [events, setEvents] = useState<TimelineEvent[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const limit = 50

  const loadEvents = useCallback(async (offset: number = 0) => {
    try {
      if (offset === 0) {
        setLoading(true)
      } else {
        setLoadingMore(true)
      }
      setError(null)
      const res = await getTimeline(limit, offset)
      if (offset === 0) {
        setEvents(res.events)
      } else {
        setEvents((prev) => [...prev, ...res.events])
      }
      setTotal(res.total)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load timeline')
    } finally {
      setLoading(false)
      setLoadingMore(false)
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await
    loadEvents(0)
  }, [loadEvents])

  const hasMore = events.length < total

  function handleEventClick(event: TimelineEvent) {
    if (!onNavigate) return
    const meta = event.metadata as Record<string, string> | undefined
    switch (event.type) {
      case 'conversation':
        onNavigate('Chat', { agentId: event.agent_id, conversationId: meta?.['conversation_id'] })
        break
      case 'artifact_created':
        onNavigate('Workspace')
        break
      case 'task_completed':
        onNavigate('Kanban')
        break
      case 'workflow_run':
        onNavigate('Workflows')
        break
      case 'delegation':
        onNavigate('Chat', { agentId: event.agent_id })
        break
    }
  }

  if (loading) {
    return (
      <div className="p-6">
        <h2 className="text-2xl font-bold mb-6">Timeline</h2>
        <div className="flex items-start gap-4">
          <div className="flex flex-col items-center flex-shrink-0">
            {Array.from({ length: 5 }).map((_, i) => (
              <div key={i} className="flex items-start gap-4">
                <div className="w-9 h-9 bg-gray-800 rounded-full animate-pulse" />
                <div className="flex-1 pb-6">
                  <div className="bg-gray-900 border border-gray-800 rounded-lg px-4 py-3">
                    <div className="h-3 bg-gray-800 rounded w-24 mb-2 animate-pulse" />
                    <div className="h-4 bg-gray-800 rounded w-3/4 mb-2 animate-pulse" />
                    <div className="h-3 bg-gray-800 rounded w-1/2 animate-pulse" />
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-6">
        <h2 className="text-2xl font-bold mb-6">Timeline</h2>
        <div className="bg-red-500/10 border border-red-500/30 rounded-lg p-4">
          <p className="text-red-400 text-sm">{error}</p>
          <button
            onClick={() => loadEvents(0)}
            className="mt-2 text-xs text-red-300 hover:text-red-200 underline"
          >
            Retry
          </button>
        </div>
      </div>
    )
  }

  const grouped = groupEvents(events)

  return (
    <div className="p-6 max-w-3xl">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">Timeline</h2>
        <span className="text-xs text-gray-500">{total} events</span>
      </div>

      {events.length > 0 && <StatsBar events={events} />}

      {events.length === 0 ? (
        <div className="text-center py-16">
          <div className="text-4xl mb-4"><Icon name="checklist" size={36} /></div>
          <p className="text-gray-400 text-sm">No activity yet. Events will appear here as conversations, tasks, and artifacts are created.</p>
        </div>
      ) : (
        <>
          {grouped.map((group) => (
            <div key={group.label} className="mb-6">
              <div className="flex items-center gap-3 mb-4">
                <h3 className="text-sm font-semibold text-gray-300 uppercase tracking-wider">{group.label}</h3>
                <div className="flex-1 h-px bg-gray-800" />
                <span className="text-xs text-gray-600">{group.events.length}</span>
              </div>
              <div>
                {group.events.map((event, idx) => (
                  <TimelineItem
                    key={event.id}
                    event={event}
                    isLast={idx === group.events.length - 1}
                    onClick={() => handleEventClick(event)}
                  />
                ))}
              </div>
            </div>
          ))}

          {hasMore && (
            <div className="flex justify-center py-4">
              <button
                onClick={() => loadEvents(events.length)}
                disabled={loadingMore}
                className="px-4 py-2 text-sm text-gray-300 bg-gray-900 border border-gray-700 rounded-lg hover:bg-gray-800 hover:border-gray-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {loadingMore ? 'Loading...' : `Load More (${total - events.length} remaining)`}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
