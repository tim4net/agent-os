import type { Agent } from '../../api/client'

interface AgentCardProps {
  agent: Agent
}

function statusBadge(status: string): { bg: string; text: string } {
  switch (status) {
    case 'online':
      return { bg: 'bg-green-900/50', text: 'text-green-400' }
    case 'offline':
    case 'error':
      return { bg: 'bg-red-900/50', text: 'text-red-400' }
    default:
      return { bg: 'bg-gray-800', text: 'text-gray-400' }
  }
}

function relativeTime(dateStr: string | null): string {
  if (!dateStr) return 'Never'
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

export function AgentCard({ agent }: AgentCardProps) {
  const badge = statusBadge(agent.status)

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4 hover:border-gray-700 transition-colors">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold text-white truncate">
          {agent.display_name || agent.name}
        </h3>
        <span className={`text-xs px-2 py-0.5 rounded-full ${badge.bg} ${badge.text}`}>
          {agent.status}
        </span>
      </div>
      <div className="flex items-center justify-between text-xs text-gray-500">
        <span>{agent.harness}</span>
        <span>{relativeTime(agent.last_seen)}</span>
      </div>
    </div>
  )
}
