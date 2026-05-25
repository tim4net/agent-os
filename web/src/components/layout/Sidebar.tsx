import { useEffect, useState } from 'react'
import type { Agent, Model, DiscoveredAgent } from '../../api/client'
import { getAgentModels, discoverAgents, autoRegisterAgents } from '../../api/client'
import { DiscoverModal } from '../agents/DiscoverModal'

interface SidebarProps {
  agents: Agent[]
  selectedAgent: Agent | null
  onSelectAgent: (agent: Agent) => void
  onAgentsChanged?: () => void
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

export function Sidebar({ agents, selectedAgent, onSelectAgent, onAgentsChanged }: SidebarProps) {
  const [models, setModels] = useState<Model[]>([])
  const [showDiscover, setShowDiscover] = useState(false)
  const [discovered, setDiscovered] = useState<DiscoveredAgent[]>([])
  const [discovering, setDiscovering] = useState(false)

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

  async function handleDiscover() {
    setShowDiscover(true)
    setDiscovering(true)
    try {
      const found = await discoverAgents()
      setDiscovered(found)
    } catch {
      setDiscovered([])
    } finally {
      setDiscovering(false)
    }
  }

  async function handleRegister(agent: DiscoveredAgent) {
    try {
      await autoRegisterAgents([agent.id])
      onAgentsChanged?.()
    } catch {
      // ignore
    }
    // Refresh discovered list
    try {
      const found = await discoverAgents()
      setDiscovered(found)
    } catch {
      setDiscovered([])
    }
  }

  return (
    <>
      <aside className="w-64 md:w-64 max-md:w-14 bg-gray-900 flex-shrink-0 flex flex-col border-r border-gray-800 h-screen max-md:items-center">
        {/* Heading */}
        <div className="p-4 border-b border-gray-800 w-full">
          <h1 className="text-lg font-semibold text-white max-md:text-center max-md:text-sm">
            <span className="hidden md:inline">Agent OS</span>
            <span className="md:hidden">OS</span>
          </h1>
        </div>

        {/* Agents */}
        <div className="flex-1 overflow-y-auto p-2 md:p-4 md:space-y-3 space-y-1">
          <p className="text-xs text-gray-500 uppercase tracking-wider font-semibold hidden md:block">Agents</p>
          {agents.map((agent) => (
            <button
              key={agent.id}
              onClick={() => onSelectAgent(agent)}
              title={agent.display_name || agent.name}
              className={`w-full flex items-center gap-2 px-2 md:px-3 py-2 rounded text-sm transition-colors ${
                selectedAgent?.id === agent.id
                  ? 'bg-gray-800 text-white'
                  : 'text-gray-400 hover:text-white hover:bg-gray-800'
              }`}
            >
              <span className={`w-2.5 h-2.5 rounded-full flex-shrink-0 ${statusColor(agent.status)}`} />
              <span className="truncate hidden md:inline">{agent.display_name || agent.name}</span>
            </button>
          ))}

          {/* Models */}
          {selectedAgent && models.length > 0 && (
            <>
              <p className="text-xs text-gray-500 uppercase tracking-wider font-semibold pt-4 hidden md:block">Models</p>
              {models.map((m) => (
                <div key={m.id} className="px-3 py-1 text-xs text-gray-400 truncate hidden md:block">
                  {m.id}
                </div>
              ))}
            </>
          )}
        </div>

        {/* Discover + Status */}
        <div className="p-2 md:p-4 border-t border-gray-800 space-y-2">
          <button
            onClick={handleDiscover}
            className="w-full px-2 md:px-3 py-2 text-xs bg-gray-800 hover:bg-gray-700 rounded transition-colors text-gray-300"
            title="Discover Agents"
          >
            <span className="hidden md:inline">🔍 Discover Agents</span>
            <span className="md:hidden">🔍</span>
          </button>
          <p className="text-xs text-gray-500 hidden md:block">
            {onlineCount}/{agents.length} agents online
          </p>
        </div>
      </aside>

      {/* Discover modal */}
      {showDiscover && (
        <DiscoverModal
          agents={discovered}
          loading={discovering}
          onRegister={handleRegister}
          onClose={() => setShowDiscover(false)}
        />
      )}
    </>
  )
}
