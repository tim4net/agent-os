import type { Agent } from '../../api/client'

interface AgentCardProps {
  agent: Agent
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

function dotColor(status: string): string {
  switch (status) {
    case 'online':
      return 'bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.5)]'
    case 'offline':
    case 'error':
      return 'bg-red-400 shadow-[0_0_6px_rgba(248,113,113,0.4)]'
    default:
      return 'bg-gray-500'
  }
}

export function AgentCard({ agent }: AgentCardProps) {
  const isOnline = agent.status === 'online'

  return (
    <div
      className={`
        glass-card p-4 rounded-xl
        transition-all duration-300 ease-out
        hover:translate-y-[-2px]
        ${isOnline ? 'hover:shadow-[0_4px_20px_rgba(91,141,239,0.08)]' : 'hover:shadow-[0_4px_16px_rgba(0,0,0,0.2)]'}
      `}
    >
      <div className="flex items-center justify-between mb-2.5">
        <h3
          className={`text-sm font-semibold truncate ${
            isOnline ? 'gradient-text' : 'text-[var(--color-text-primary)]'
          }`}
        >
          {agent.display_name || agent.name}
        </h3>
        {/* Glowing status dot */}
        <span className={`inline-block w-2 h-2 rounded-full shrink-0 ml-2 ${dotColor(agent.status)}`} />
      </div>
      {agent.role && (
        <p className="text-[11px] text-[var(--color-text-muted)] mb-2 line-clamp-2 leading-relaxed">
          {agent.role}
        </p>
      )}
      <div className="flex items-center justify-between">
        {/* Harness type as subtle monospace tag */}
        <span className="text-[10px] font-mono text-[var(--color-text-muted)]/70 bg-[var(--bg-elevated)]/50 px-1.5 py-0.5 rounded">
          {agent.harness}
        </span>
        {/* Last seen timestamp */}
        <span className="text-[10px] text-[var(--color-text-muted)]/50">
          {relativeTime(agent.last_seen)}
        </span>
      </div>
    </div>
  )
}
