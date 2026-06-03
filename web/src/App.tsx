import { useState, useRef, useEffect, useCallback } from 'react'
import type { Agent } from './api/client'
import { uploadArtifact } from './api/client'
import { useAgents } from './hooks/useAgents'
import { useSSE } from './hooks/useSSE'
import { Icon } from './components/Icon'
import SettingsPanel from './components/SettingsPanel'
import { Sidebar } from './components/layout/Sidebar'

import MissionControl from './components/MissionControl'
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
import { WorkflowList } from './components/workflows/WorkflowList'
import { SkillsList } from './components/skills/SkillsList'
import { ObserveView } from './components/observe/ObserveView'
import { ControlView } from './components/control/ControlView'

import { ErrorBoundary } from './components/ErrorBoundary'
import { StatusFooter } from './components/StatusFooter'
import { ToastContainer } from './components/Toast'

const MOBILE_BREAKPOINT = 768

const tabMeta: Record<string, string> = {
  Chat: 'chat',
  Create: 'palette',
  Build: 'view_kanban',
  Knowledge: 'psychology',
  Automate: 'bolt',
  Observe: 'radar',
  Control: 'tune',
  Settings: 'settings',
}

const tabs = ['Chat', 'Create', 'Build', 'Knowledge', 'Automate', 'Observe', 'Control', 'Settings'] as const
type Tab = (typeof tabs)[number]

/** Reusable segmented toggle for sub-views within a tab */
function SubViewToggle({ options, value, onChange }: {
  options: { key: string; label: string }[]
  value: string
  onChange: (v: string) => void
}) {
  return (
    <div className="flex items-center gap-1 bg-[var(--bg-elevated)]/60 rounded-xl p-1">
      {options.map((opt) => (
        <button
          key={opt.key}
          onClick={() => onChange(opt.key)}
          className={`px-3 py-1 text-xs font-medium rounded-lg transition-all ${
            value === opt.key
              ? 'bg-[var(--accent-blue)] text-white'
              : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}

function App() {
  const [activeTab, setActiveTab] = useState<Tab>('Chat')
  const [selectedAgent, setSelectedAgent] = useState<Agent | null>(null)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false) // mobile overlay
  const [isMobile, setIsMobile] = useState(false)
  const [artifactGridKey, setArtifactGridKey] = useState(0)
  const [memoryFilePath, setMemoryFilePath] = useState<string | null>(null)
  const [mediaPreviewKey, setMediaPreviewKey] = useState(0)
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null)
  const [conversationVersion, setConversationVersion] = useState(0)

  // Sub-view states for merged tabs
  const [createSubView, setCreateSubView] = useState<'generate' | 'gallery'>('generate')
  const [buildSubView, setBuildSubView] = useState<'board' | 'goals' | 'pipeline'>('board')
  const [knowledgeSubView, setKnowledgeSubView] = useState<'files' | 'skills'>('files')

  const { agents, loading: _loading, refresh: refreshAgents } = useAgents()
  const { sseConnected } = useSSE()
  const uploadInputRef = useRef<HTMLInputElement>(null)
  const tablistRef = useRef<HTMLDivElement>(null)

  // Detect mobile viewport
  useEffect(() => {
    function checkMobile() {
      const mobile = window.innerWidth < MOBILE_BREAKPOINT
      setIsMobile(mobile)
      if (mobile) {
        setSidebarCollapsed(true)
        setSidebarOpen(false)
      }
    }
    checkMobile()
    window.addEventListener('resize', checkMobile)
    return () => window.removeEventListener('resize', checkMobile)
  }, [])

  const handleSelectAgent = useCallback((agent: Agent) => {
    setSelectedAgent(agent)
    setActiveTab('Chat')
    setActiveConversationId(null)
    if (isMobile) setSidebarOpen(false)
  }, [isMobile])

  // Auto-restore last conversation on mount (chat persistence)
  const hasAutoRestored = useRef(false)
  useEffect(() => {
    if (hasAutoRestored.current || agents.length === 0) return
    hasAutoRestored.current = true

    const savedConvId = sessionStorage.getItem('agent-os-last-conv')
    const savedAgentId = sessionStorage.getItem('agent-os-last-agent')
    if (savedConvId && savedAgentId) {
      const agent = agents.find((a) => a.id === savedAgentId)
      if (agent) {
        setSelectedAgent(agent)
        setActiveConversationId(savedConvId)
        setActiveTab('Chat')
        return
      }
    }

    const onlineAgent = agents.find((a) => a.status === 'online')
    if (onlineAgent) {
      setSelectedAgent(onlineAgent)
      setActiveTab('Chat')
    }
  }, [agents])

  // Persist active conversation to sessionStorage on change
  useEffect(() => {
    if (activeConversationId && selectedAgent) {
      sessionStorage.setItem('agent-os-last-conv', activeConversationId)
      sessionStorage.setItem('agent-os-last-agent', selectedAgent.id)
    } else {
      sessionStorage.removeItem('agent-os-last-conv')
      sessionStorage.removeItem('agent-os-last-agent')
    }
  }, [activeConversationId, selectedAgent])

  const handleSelectConversation = useCallback((conv: import('./api/client').Conversation) => {
    const agent = agents.find((a) => a.id === conv.agent_id)
    if (agent) {
      setSelectedAgent(agent)
    }
    setActiveConversationId(conv.id)
    setActiveTab('Chat')
    if (isMobile) setSidebarOpen(false)
  }, [agents, isMobile])

  const handleNewChat = useCallback(() => {
    setSelectedAgent(null)
    setActiveConversationId(null)
    setActiveTab('Chat')
    if (isMobile) setSidebarOpen(false)
  }, [isMobile])

  const handleNewChatWithAgent = useCallback((agent: Agent) => {
    setSelectedAgent(agent)
    setActiveConversationId(null)
    setActiveTab('Chat')
    if (isMobile) setSidebarOpen(false)
  }, [isMobile])

  function handleConversationCreated(convId: string) {
    setActiveConversationId(convId)
    setConversationVersion((v) => v + 1)
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
    e.target.value = ''
  }

  function renderContent() {
    switch (activeTab) {
      case 'Chat':
        if (!selectedAgent) {
          return (
            <ErrorBoundary name="Mission Control">
              <MissionControl agents={agents} />
            </ErrorBoundary>
          )
        }
        return (
          <ErrorBoundary name="Chat">
            <ChatPanel
              key={selectedAgent.id}
              agent={selectedAgent}
              activeConversationId={activeConversationId}
              onConversationLoaded={() => {}}
              onConversationCreated={handleConversationCreated}
              onNewChat={handleNewChat}
            />
          </ErrorBoundary>
        )

      case 'Create': {
        const createOptions = [
          { key: 'generate', label: 'Generate' },
          { key: 'gallery', label: 'Gallery' },
        ]
        return (
          <ErrorBoundary name="Create">
            <div className="flex flex-col h-full">
              <div className="px-4 py-3 border-b border-[var(--border-subtle)] flex-shrink-0 flex items-center justify-between">
                <h2 className="text-2xl font-bold text-[var(--text-primary)]">Create</h2>
                <SubViewToggle
                  options={createOptions}
                  value={createSubView}
                  onChange={(v) => setCreateSubView(v as 'generate' | 'gallery')}
                />
              </div>
              {createSubView === 'generate' ? (
                <div className="flex flex-col md:flex-row flex-1 min-h-0 gap-6 p-4">
                  <div className="w-full md:w-96 flex-shrink-0">
                    <GeneratorForm
                      onGenerated={() => setMediaPreviewKey((k) => k + 1)}
                      agentId={selectedAgent?.id}
                    />
                  </div>
                  <div className="flex-1 min-w-0 overflow-y-auto">
                    <MediaPreview key={mediaPreviewKey} />
                  </div>
                </div>
              ) : (
                <ArtifactGrid
                  key={artifactGridKey}
                  agents={agents}
                  selectedAgent={selectedAgent}
                  onUploadClick={handleUploadClick}
                />
              )}
            </div>
          </ErrorBoundary>
        )
      }

      case 'Build': {
        const buildOptions = [
          { key: 'board', label: 'Board' },
          { key: 'goals', label: 'Goals' },
          { key: 'pipeline', label: 'Pipeline' },
        ]
        return (
          <ErrorBoundary name="Build">
            <div className="flex flex-col h-full">
              <div className="px-4 py-3 border-b border-[var(--border-subtle)] flex-shrink-0 flex items-center justify-between">
                <h2 className="text-2xl font-bold text-[var(--text-primary)]">Build</h2>
                <SubViewToggle
                  options={buildOptions}
                  value={buildSubView}
                  onChange={(v) => setBuildSubView(v as 'board' | 'goals' | 'pipeline')}
                />
              </div>
              <div className="flex-1 min-h-0">
                {buildSubView === 'board' && <Board agents={agents} />}
                {buildSubView === 'goals' && <GoalList />}
                {buildSubView === 'pipeline' && <PipelineBoard />}
              </div>
            </div>
          </ErrorBoundary>
        )
      }

      case 'Knowledge': {
        const knowledgeOptions = [
          { key: 'files', label: 'Files' },
          { key: 'skills', label: 'Skills' },
        ]
        return (
          <ErrorBoundary name="Knowledge">
            <div className="flex flex-col h-full">
              <div className="px-4 py-3 border-b border-[var(--border-subtle)] flex-shrink-0 flex items-center justify-between">
                <h2 className="text-2xl font-bold text-[var(--text-primary)]">Knowledge</h2>
                <SubViewToggle
                  options={knowledgeOptions}
                  value={knowledgeSubView}
                  onChange={(v) => setKnowledgeSubView(v as 'files' | 'skills')}
                />
              </div>
              {knowledgeSubView === 'files' ? (
                <>
                  <div className="px-4 py-2 border-b border-[var(--border-subtle)] flex-shrink-0">
                    <SearchBar onFileSelect={setMemoryFilePath} />
                  </div>
                  <div className="flex flex-col md:flex-row flex-1 min-h-0">
                    <div className="w-full md:w-72 flex-shrink-0 border-b md:border-b-0 md:border-r border-[var(--border-subtle)] overflow-y-auto bg-[var(--bg-surface)]/50 max-h-64 md:max-h-none">
                      <FileTree onFileSelect={setMemoryFilePath} selectedPath={memoryFilePath ?? undefined} />
                    </div>
                    <div className="flex-1 min-w-0">
                      <NoteViewer filePath={memoryFilePath} />
                    </div>
                  </div>
                </>
              ) : (
                <SkillsList />
              )}
            </div>
          </ErrorBoundary>
        )
      }

      case 'Automate': {
        // WP-H: the legacy Timeline sub-tab read the delegation proxy (getTimeline),
        // not work_events. Agent activity over time now lives in Observe → Activity
        // (the work-event observability plane). Automate is workflows only.
        return (
          <ErrorBoundary name="Automate">
            <div className="flex flex-col h-full">
              <div className="px-4 py-3 border-b border-[var(--border-subtle)] flex-shrink-0 flex items-center justify-between">
                <h2 className="text-2xl font-bold text-[var(--text-primary)]">Automate</h2>
              </div>
              <div className="flex-1 min-h-0">
                <WorkflowList />
              </div>
            </div>
          </ErrorBoundary>
        )
      }

      case 'Observe':
        return (
          <ErrorBoundary name="Observe">
            <ObserveView />
          </ErrorBoundary>
        )

      case 'Control':
        return (
          <ErrorBoundary name="Control">
            <ControlView />
          </ErrorBoundary>
        )

      case 'Settings':
        return (
          <ErrorBoundary name="Settings">
            <SettingsPanel />
          </ErrorBoundary>
        )

      default:
        return (
          <div>
            <h2 className="text-2xl font-bold mb-4 text-[var(--color-text-primary)]">{activeTab}</h2>
            <p className="text-[var(--color-text-muted)]">Coming soon.</p>
          </div>
        )
    }
  }

  return (
    <div className="flex h-screen bg-[var(--bg-base)] text-gray-100">
      {/* Mobile sidebar overlay backdrop */}
      {isMobile && sidebarOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/50 backdrop-blur-sm"
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}

      {/* Sidebar: inline on desktop, overlay on mobile */}
      <div className={isMobile ? (sidebarOpen ? 'fixed inset-y-0 left-0 z-50' : 'hidden') : ''}>
        <Sidebar
          agents={agents}
          selectedAgent={selectedAgent}
          onSelectAgent={handleSelectAgent}
          onAgentsChanged={refreshAgents}
          collapsed={isMobile ? false : sidebarCollapsed}
          activeConversationId={activeConversationId}
          onSelectConversation={handleSelectConversation}
          onNewChat={handleNewChat}
          onNewChatWithAgent={handleNewChatWithAgent}
          conversationVersion={conversationVersion}
        />
      </div>

      <div className="flex-1 flex flex-col min-w-0">
        {/* Horizontal tab nav bar — compact scrollable on mobile */}
        <nav className="flex-shrink-0 bg-[var(--bg-surface)]/60 backdrop-blur-sm border-b border-[var(--border-subtle)]">
          <div
            ref={tablistRef}
            role="tablist"
            aria-label="Main navigation"
            className={`flex items-center gap-1 px-3 md:px-6 py-2 ${isMobile ? 'overflow-x-auto' : 'flex-wrap'}`}
            onKeyDown={(e) => {
              const currentIdx = tabs.indexOf(activeTab)
              if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
                e.preventDefault()
                const next = tabs[(currentIdx + 1) % tabs.length]
                setActiveTab(next)
              } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
                e.preventDefault()
                const prev = tabs[(currentIdx - 1 + tabs.length) % tabs.length]
                setActiveTab(prev)
              } else if (e.key === 'Home') {
                e.preventDefault()
                setActiveTab(tabs[0])
              } else if (e.key === 'End') {
                e.preventDefault()
                setActiveTab(tabs[tabs.length - 1])
              }
            }}
          >
            {/* Sidebar toggle */}
            <button
              onClick={() => setSidebarCollapsed(!sidebarCollapsed)}
              aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
              className="flex-shrink-0 p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]/40 transition-all duration-200 mr-1"
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                {sidebarCollapsed ? (
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
                ) : (
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
                )}
              </svg>
            </button>

            {/* Flat list of 6 tabs */}
            {tabs.map((tab) => {
              const isActive = activeTab === tab
              return (
                <button
                  key={tab}
                  role="tab"
                  aria-selected={isActive}
                  tabIndex={isActive ? 0 : -1}
                  onClick={() => setActiveTab(tab)}
                  className={`
                    relative px-3 md:px-4 py-1.5 text-xs md:text-sm font-medium rounded-full whitespace-nowrap transition-all duration-200
                    ${isActive
                      ? 'text-white'
                      : 'text-[var(--color-text-muted)] hover:text-[var(--color-text-secondary)] hover:bg-[var(--bg-elevated)]/40'
                    }
                  `}
                >
                  <span className="flex items-center gap-1.5">
                    <Icon name={tabMeta[tab]} size={16} />
                    <span>{tab}</span>
                  </span>
                  {/* Gradient underline accent for active tab */}
                  {isActive && (
                    <span
                      className="absolute bottom-0 left-2 right-2 h-0.5 rounded-full"
                      style={{
                        background: 'linear-gradient(to right, var(--accent-blue), var(--accent-purple))',
                      }}
                    />
                  )}
                  {/* Subtle hover glow for inactive tabs */}
                  {!isActive && (
                    <span className="absolute inset-0 rounded-full opacity-0 hover:opacity-100 transition-opacity duration-300 pointer-events-none"
                      style={{ boxShadow: '0 0 12px 2px rgba(91,141,239,0.06)' }}
                    />
                  )}
                </button>
              )
            })}
          </div>
        </nav>

        {/* Content with page transition */}
        <main role="tabpanel" aria-label={`${activeTab} panel`} className="flex-1 overflow-auto page-transition" key={activeTab}>
          {renderContent()}
        </main>

        {/* Status Footer — desktop only */}
        {!isMobile && <StatusFooter sseConnected={sseConnected} agents={agents} />}
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
