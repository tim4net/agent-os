import { useState } from 'react'
import type { Agent } from './api/client'
import { useAgents } from './hooks/useAgents'
import { Sidebar } from './components/layout/Sidebar'
import { AgentCard } from './components/agents/AgentCard'
import { ChatPanel } from './components/chat/ChatPanel'

const tabs = ['Overview', 'Chat', 'Studio', 'Workspace', 'Kanban', 'Memory', 'Goals', 'Pipeline'] as const
type Tab = (typeof tabs)[number]

function App() {
  const [activeTab, setActiveTab] = useState<Tab>('Overview')
  const [selectedAgent, setSelectedAgent] = useState<Agent | null>(null)
  const { agents, loading } = useAgents()

  function handleSelectAgent(agent: Agent) {
    setSelectedAgent(agent)
    setActiveTab('Chat')
  }

  function renderContent() {
    switch (activeTab) {
      case 'Overview':
        return (
          <div>
            <h2 className="text-2xl font-bold mb-4">Overview</h2>
            {loading ? (
              <p className="text-gray-400">Loading agents...</p>
            ) : agents.length === 0 ? (
              <p className="text-gray-400">No agents registered.</p>
            ) : (
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
                {agents.map((agent) => (
                  <AgentCard key={agent.id} agent={agent} />
                ))}
              </div>
            )}
          </div>
        )
      case 'Chat':
        if (!selectedAgent) {
          return (
            <div className="flex items-center justify-center h-full">
              <p className="text-gray-500">Select an agent from the sidebar to start chatting.</p>
            </div>
          )
        }
        return <ChatPanel agent={selectedAgent} />
      default:
        return (
          <div>
            <h2 className="text-2xl font-bold mb-4">{activeTab}</h2>
            <p className="text-gray-400">Coming soon.</p>
          </div>
        )
    }
  }

  return (
    <div className="flex h-screen bg-gray-950 text-gray-100">
      <Sidebar
        agents={agents}
        selectedAgent={selectedAgent}
        onSelectAgent={handleSelectAgent}
      />

      <div className="flex-1 flex flex-col min-w-0">
        {/* Tab bar */}
        <header className="bg-gray-900 border-b border-gray-800 flex-shrink-0">
          <div className="flex items-center gap-1 px-4 pt-2">
            {tabs.map((tab) => (
              <button
                key={tab}
                onClick={() => setActiveTab(tab)}
                className={`px-4 py-2 text-sm font-medium rounded-t transition-colors ${
                  activeTab === tab
                    ? 'bg-gray-950 text-white border-t border-l border-r border-gray-800'
                    : 'text-gray-400 hover:text-gray-200 hover:bg-gray-800'
                }`}
              >
                {tab}
              </button>
            ))}
          </div>
        </header>

        {/* Content */}
        <main className="flex-1 overflow-auto">
          {renderContent()}
        </main>
      </div>
    </div>
  )
}

export default App
