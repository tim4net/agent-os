import { useEffect, useState } from 'react'
import type { Agent, Model } from '../../api/client'
import { getAgentModels } from '../../api/client'

interface SidebarProps {
  agents: Agent[]
  selectedAgent: Agent | null
  onSelectAgent: (agent: Agent) => void
}

function statusColor(status: string): string {
  switch (status) {
    case 'online':
      return 'bg-green-500'
    case 'offline':
    case 'error':
      return 'bg-red-500'
    default:
      return 'bg-gray-500'
  }
}

export function Sidebar({ agents, selectedAgent, onSelectAgent }: SidebarProps) {
  const [models, setModels] = useState<Model[]>([])

  const onlineCount = agents.filter((a) => a.status === 'online').length

  useEffect(() => {
    if (!selectedAgent) {
      setModels([])
      return
    }
    getAgentModels(selectedAgent.id)
      .then(setModels)
      .catch(() => setModels([]))
  }, [selectedAgent])

  return (
    <aside className="w-64 bg-gray-900 flex-shrink-0 flex flex-col border-r border-gray-800 h-screen">
      {/* Heading */}
      <div className="p-4 border-b border-gray-800">
        <h1 className="text-lg font-semibold text-white">Agent OS</h1>
      </div>

      {/* Agents */}
      <div className="flex-1 overflow-y-auto p-4 space-y-3">
        <p className="text-xs text-gray-500 uppercase tracking-wider font-semibold">Agents</p>
        {agents.map((agent) => (
          <button
            key={agent.id}
            onClick={() => onSelectAgent(agent)}
            className={`w-full flex items-center gap-2 px-3 py-2 rounded text-sm transition-colors ${
              selectedAgent?.id === agent.id
                ? 'bg-gray-800 text-white'
                : 'text-gray-400 hover:text-white hover:bg-gray-800'
            }`}
          >
            <span className={`w-2.5 h-2.5 rounded-full flex-shrink-0 ${statusColor(agent.status)}`} />
            <span className="truncate">{agent.display_name || agent.name}</span>
          </button>
        ))}

        {/* Models */}
        {selectedAgent && models.length > 0 && (
          <>
            <p className="text-xs text-gray-500 uppercase tracking-wider font-semibold pt-4">Models</p>
            {models.map((m) => (
              <div key={m.id} className="px-3 py-1 text-xs text-gray-400 truncate">
                {m.id}
              </div>
            ))}
          </>
        )}
      </div>

      {/* Status */}
      <div className="p-4 border-t border-gray-800">
        <p className="text-xs text-gray-500 uppercase tracking-wider font-semibold mb-1">Status</p>
        <p className="text-sm text-gray-300">
          {onlineCount}/{agents.length} agents online
        </p>
      </div>
    </aside>
  )
}
