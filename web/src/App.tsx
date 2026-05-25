import { useState, useRef } from 'react'
import type { Agent } from './api/client'
import { uploadArtifact } from './api/client'
import { useAgents } from './hooks/useAgents'
import { Sidebar } from './components/layout/Sidebar'
import { AgentCard } from './components/agents/AgentCard'
import { ChatPanel } from './components/chat/ChatPanel'
import { ArtifactGrid } from './components/workspace/ArtifactGrid'
import { FileTree } from './components/memory/FileTree'
import { NoteViewer } from './components/memory/NoteViewer'
import { SearchBar } from './components/memory/SearchBar'
import { GeneratorForm } from './components/studio/GeneratorForm'
import { MediaPreview } from './components/studio/MediaPreview'
import { Board } from './components/kanban/Board'
import { GoalList } from './components/goals/GoalList'
import { PipelineBoard } from './components/pipeline/PipelineBoard'
import { ActivityFeed } from './components/ActivityFeed'
import { ErrorBoundary } from './components/ErrorBoundary'
import { StatusFooter } from './components/StatusFooter'
import { ToastContainer } from './components/Toast'

const tabs = ['Overview', 'Chat', 'Studio', 'Workspace', 'Kanban', 'Memory', 'Goals', 'Pipeline'] as const
type Tab = (typeof tabs)[number]

function App() {
  const [activeTab, setActiveTab] = useState<Tab>('Overview')
  const [selectedAgent, setSelectedAgent] = useState<Agent | null>(null)
  const [artifactGridKey, setArtifactGridKey] = useState(0)
  const [memoryFilePath, setMemoryFilePath] = useState<string | null>(null)
  const [mediaPreviewKey, setMediaPreviewKey] = useState(0)
  const { agents, loading, refresh: refreshAgents } = useAgents()
  const uploadInputRef = useRef<HTMLInputElement>(null)

  function handleSelectAgent(agent: Agent) {
    setSelectedAgent(agent)
    setActiveTab('Chat')
  }

  function handleUploadClick() {
    uploadInputRef.current?.click()
  }

  async function handleFileSelected(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    try {
      await uploadArtifact(file)
      setArtifactGridKey((k) => k + 1)
    } catch (err) {
      console.error('Upload failed:', err)
    }
    // Reset input so same file can be re-uploaded
    e.target.value = ''
  }

  function renderContent() {
    switch (activeTab) {
      case 'Overview':
        return (
          <div className="p-6">
            <h2 className="text-2xl font-bold mb-6">Overview</h2>
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
              {/* Agent Status */}
              <div>
                <h3 className="text-lg font-semibold mb-3 text-gray-300">Agents</h3>
                {loading ? (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {Array.from({ length: 3 }).map((_, i) => (
                      <div key={i} className="bg-gray-900 border border-gray-800 rounded-lg p-4 animate-pulse">
                        <div className="h-4 bg-gray-800 rounded w-3/4 mb-3" />
                        <div className="h-3 bg-gray-800 rounded w-1/2" />
                      </div>
                    ))}
                  </div>
                ) : agents.length === 0 ? (
                  <p className="text-gray-400">No agents registered.</p>
                ) : (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {agents.map((agent) => (
                      <AgentCard key={agent.id} agent={agent} />
                    ))}
                  </div>
                )}
              </div>
              {/* Activity Feed */}
              <div>
                <h3 className="text-lg font-semibold mb-3 text-gray-300">Recent Activity</h3>
                <ErrorBoundary name="Activity Feed">
                  <ActivityFeed onNavigate={(tab) => setActiveTab(tab as Tab)} />
                </ErrorBoundary>
              </div>
            </div>
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
        return (
          <ErrorBoundary name="Chat">
            <ChatPanel agent={selectedAgent} />
          </ErrorBoundary>
        )
      case 'Workspace':
        return (
          <ErrorBoundary name="Workspace">
            <ArtifactGrid
              key={artifactGridKey}
              agents={agents}
              selectedAgent={selectedAgent}
              onUploadClick={handleUploadClick}
            />
          </ErrorBoundary>
        )
      case 'Memory':
        return (
          <ErrorBoundary name="Memory">
            <div className="flex flex-col h-full">
              <div className="px-4 py-3 border-b border-gray-800 flex-shrink-0">
                <SearchBar onFileSelect={setMemoryFilePath} />
              </div>
              <div className="flex flex-1 min-h-0">
                <div className="w-72 flex-shrink-0 border-r border-gray-800 overflow-y-auto bg-gray-900/50">
                  <FileTree onFileSelect={setMemoryFilePath} selectedPath={memoryFilePath ?? undefined} />
                </div>
                <div className="flex-1 min-w-0">
                  <NoteViewer filePath={memoryFilePath} />
                </div>
              </div>
            </div>
          </ErrorBoundary>
        )
      case 'Studio':
        return (
          <ErrorBoundary name="Studio">
            <div className="flex h-full gap-6 p-4">
              <div className="w-96 flex-shrink-0">
                <GeneratorForm
                  onGenerated={() => setMediaPreviewKey((k) => k + 1)}
                />
              </div>
              <div className="flex-1 min-w-0 overflow-y-auto">
                <MediaPreview key={mediaPreviewKey} />
              </div>
            </div>
          </ErrorBoundary>
        )
      case 'Kanban':
        return (
          <ErrorBoundary name="Kanban">
            <Board agents={agents} />
          </ErrorBoundary>
        )
      case 'Goals':
        return (
          <ErrorBoundary name="Goals">
            <GoalList />
          </ErrorBoundary>
        )
      case 'Pipeline':
        return (
          <ErrorBoundary name="Pipeline">
            <PipelineBoard />
          </ErrorBoundary>
        )
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
        onAgentsChanged={refreshAgents}
      />

      <div className="flex-1 flex flex-col min-w-0">
        {/* Tab bar */}
        <header className="bg-gray-900 border-b border-gray-800 flex-shrink-0">
          <div className="flex items-center gap-1 px-2 md:px-4 pt-2 overflow-x-auto">
            {tabs.map((tab) => (
              <button
                key={tab}
                onClick={() => setActiveTab(tab)}
                className={`px-3 md:px-4 py-2 text-xs md:text-sm font-medium rounded-t transition-colors whitespace-nowrap ${
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

        {/* Status Footer */}
        <StatusFooter />
      </div>

      {/* Hidden file input for artifact upload */}
      <input
        ref={uploadInputRef}
        type="file"
        className="hidden"
        onChange={handleFileSelected}
      />

      {/* Global toast container */}
      <ToastContainer />
    </div>
  )
}

export default App
