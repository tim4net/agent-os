import { useState, useRef, useEffect, useCallback } from 'react'
import type { Agent } from './api/client'
import { uploadArtifact } from './api/client'
import { useAgents } from './hooks/useAgents'
import { useSSE } from './hooks/useSSE'
import SettingsPanel from './components/SettingsPanel'
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
import { WorkflowList } from './components/workflows/WorkflowList'
import { SkillsList } from './components/skills/SkillsList'
import { TimelineView } from './components/timeline/TimelineView'
import { ActivityFeed } from './components/ActivityFeed'
import { ErrorBoundary } from './components/ErrorBoundary'
import { StatusFooter } from './components/StatusFooter'
import { ToastContainer } from './components/Toast'

const MOBILE_BREAKPOINT = 768

// Mobile bottom nav tabs — subset of main tabs for quick mobile access
const mobileNavItems = [
  { tab: 'Chat', icon: '💬' },
  { tab: 'Studio', icon: '🎨' },
  { tab: 'Memory', icon: '🧠' },
  { tab: 'Kanban', icon: '📋' },
  { tab: 'Settings', icon: '⚙️' },
] as const

const tabMeta: Record<string, string> = {
  Overview: '📊',
  Chat: '💬',
  Timeline: '📜',
  Studio: '🎨',
  Workspace: '🗂️',
  Kanban: '📋',
  Goals: '🎯',
  Pipeline: '🔄',
  Memory: '🧠',
  Skills: '🛠️',
  Workflows: '⚡',
  Settings: '⚙️',
}

// Tab groups for visual sectioning in the nav bar
const tabGroups = [
  { label: null, tabs: ['Overview'] as const },
  { label: 'Agent', tabs: ['Chat', 'Timeline'] as const },
  { label: 'Create', tabs: ['Studio', 'Workspace'] as const },
  { label: 'Plan', tabs: ['Kanban', 'Goals', 'Pipeline'] as const },
  { label: 'Knowledge', tabs: ['Memory', 'Skills'] as const },
  { label: 'Automate', tabs: ['Workflows'] as const },
  { label: null, tabs: ['Settings'] as const },
]

const tabs = tabGroups.flatMap(g => [...g.tabs]) as unknown as readonly string[]
type Tab = (typeof tabs)[number]

function App() {
  const [activeTab, setActiveTab] = useState<Tab>('Overview')
  const [selectedAgent, setSelectedAgent] = useState<Agent | null>(null)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false) // mobile overlay
  const [isMobile, setIsMobile] = useState(false)
  const [artifactGridKey, setArtifactGridKey] = useState(0)
  const [memoryFilePath, setMemoryFilePath] = useState<string | null>(null)
  const [mediaPreviewKey, setMediaPreviewKey] = useState(0)
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null)
  const [conversationVersion, setConversationVersion] = useState(0)
  const { agents, loading, refresh: refreshAgents } = useAgents()
  const { sseConnected } = useSSE()
  const uploadInputRef = useRef<HTMLInputElement>(null)
  const tablistRef = useRef<HTMLDivElement>(null)

  // Auto-scroll active tab into view when it changes
  useEffect(() => {
    if (!tablistRef.current) return
    const activeBtn = tablistRef.current.querySelector('[role="tab"][aria-selected="true"]') as HTMLElement
    activeBtn?.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'nearest' })
  }, [activeTab])

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
    // Reset input so same file can be re-uploaded
    e.target.value = ''
  }

  function handleTimelineNavigate(tab: string, data?: { agentId?: string; conversationId?: string }) {
    setActiveTab(tab as Tab)
    if (data?.agentId) {
      const agent = agents.find((a) => a.id === data.agentId)
      if (agent) setSelectedAgent(agent)
    }
  }

  function renderContent() {
    switch (activeTab) {
      case 'Overview':
        return (
          <div className="p-6">
            <h2 className="text-2xl font-bold mb-6 text-[var(--color-text-primary)]">Overview</h2>
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
              {/* Agent Status */}
              <div>
                <h3 className="text-lg font-semibold mb-3 text-[var(--color-text-secondary)]">Agents</h3>
                {loading ? (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 stagger-children">
                    {Array.from({ length: 3 }).map((_, i) => (
                      <div key={i} className="glass-card p-4 animate-pulse">
                        <div className="h-4 bg-[var(--bg-elevated)] rounded w-3/4 mb-3" />
                        <div className="h-3 bg-[var(--bg-elevated)] rounded w-1/2" />
                      </div>
                    ))}
                  </div>
                ) : agents.length === 0 ? (
                  <p className="text-[var(--color-text-muted)]">No agents registered.</p>
                ) : (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 stagger-children">
                    {agents.map((agent) => (
                      <AgentCard key={agent.id} agent={agent} />
                    ))}
                  </div>
                )}
              </div>
              {/* Activity Feed */}
              <div>
                <h3 className="text-lg font-semibold mb-3 text-[var(--color-text-secondary)]">Recent Activity</h3>
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
            <div className="flex flex-col items-center justify-center h-full fade-in">
              <h2 className="text-2xl font-bold text-[var(--text-primary)] mb-2">Chat</h2>
              <p className="text-[var(--text-muted)]">Select an agent or conversation from the sidebar to start chatting.</p>
            </div>
          )
        }
        return (
          <ErrorBoundary name="Chat">
            <ChatPanel
              agent={selectedAgent}
              activeConversationId={activeConversationId}
              onConversationLoaded={() => {}}
              onConversationCreated={handleConversationCreated}
              onNewChat={handleNewChat}
            />
          </ErrorBoundary>
        )
      case 'Timeline':
        return (
          <ErrorBoundary name="Timeline">
            <TimelineView onNavigate={handleTimelineNavigate} />
          </ErrorBoundary>
        )
      case 'Studio':
        return (
          <ErrorBoundary name="Studio">
            <div className="flex flex-col h-full">
              <div className="px-4 py-3 border-b border-[var(--border-subtle)] flex-shrink-0">
                <h2 className="text-2xl font-bold text-[var(--text-primary)]">Studio</h2>
              </div>
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
            </div>
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
      case 'Kanban':
        return (
          <ErrorBoundary name="Kanban">
            <Board agents={agents} />
          </ErrorBoundary>
        )
      case 'Memory':
        return (
          <ErrorBoundary name="Memory">
            <div className="flex flex-col h-full">
              <div className="px-4 py-3 border-b border-[var(--border-subtle)] flex-shrink-0">
                <h2 className="text-2xl font-bold text-[var(--text-primary)] mb-3">Memory</h2>
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
            </div>
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
      case 'Workflows':
        return (
          <ErrorBoundary name="Workflows">
            <WorkflowList />
          </ErrorBoundary>
        )
      case 'Skills':
        return (
          <ErrorBoundary name="Skills">
            <SkillsList />
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
          activeTab={activeTab}
          collapsed={isMobile ? false : sidebarCollapsed}
          activeConversationId={activeConversationId}
          onSelectConversation={handleSelectConversation}
          onNewChat={handleNewChat}
          conversationVersion={conversationVersion}
        />
      </div>

      <div className="flex-1 flex flex-col min-w-0">
        {/* Sleek horizontal nav bar — hidden on mobile (replaced by bottom nav) */}
        {!isMobile && (
          <nav className="flex-shrink-0 bg-[var(--bg-surface)]/60 backdrop-blur-sm">
            <div
              ref={tablistRef}
              role="tablist"
              aria-label="Main navigation"
              className="flex items-center gap-1 px-3 md:px-6 py-2 overflow-x-auto"
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
              {tabGroups.map((group, gi) => {
                // Render group separator (thin divider) between groups
                const separator = gi > 0 ? (
                  <span
                    key={`sep-${gi}`}
                    className="flex-shrink-0 w-px h-5 bg-[var(--border-subtle)] mx-1"
                    aria-hidden="true"
                  />
                ) : null

                // Render optional group label for logical sections
                const label = group.label ? (
                  <span
                    key={`label-${gi}`}
                    className="flex-shrink-0 text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)] opacity-50 mr-1 hidden md:inline-block select-none"
                    aria-hidden="true"
                  >
                    {group.label}
                  </span>
                ) : null

                const groupTabs = group.tabs.map((tab) => {
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
                        <span className="text-sm">{tabMeta[tab]}</span>
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
                })

                return [separator, label, ...groupTabs]
              })}
            </div>
          </nav>
        )}

        {/* Mobile top bar with hamburger + current tab title */}
        {isMobile && (
          <div className="flex-shrink-0 flex items-center gap-3 px-4 py-2 bg-[var(--bg-surface)]/60 backdrop-blur-sm border-b border-[var(--border-subtle)]">
            <button
              onClick={() => setSidebarOpen(!sidebarOpen)}
              aria-label="Open sidebar"
              className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]/40"
            >
              <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
              </svg>
            </button>
            <h1 className="text-sm font-semibold text-[var(--text-primary)]">
              {tabMeta[activeTab]} {activeTab}
            </h1>
            <div className="flex-1" />
            {selectedAgent && activeTab === 'Chat' && (
              <span className="text-xs text-[var(--text-muted)]">
                <span className="agent-dot agent-dot--online" /> {selectedAgent.display_name || selectedAgent.name}
              </span>
            )}
          </div>
        )}

        {/* Content with page transition */}
        <main role="tabpanel" aria-label={`${activeTab} panel`} className={`flex-1 overflow-auto page-transition ${isMobile ? 'pb-16' : ''}`} key={activeTab}>
          {renderContent()}
        </main>

        {/* Status Footer — desktop only */}
        {!isMobile && <StatusFooter sseConnected={sseConnected} agents={agents} />}

        {/* Mobile bottom nav */}
        {isMobile && (
          <nav
            className="fixed bottom-0 left-0 right-0 z-30 bg-[var(--bg-surface)]/95 backdrop-blur-md border-t border-[var(--border-subtle)]"
            aria-label="Mobile navigation"
          >
            <div className="flex items-center justify-around h-14 max-w-lg mx-auto">
              {mobileNavItems.map((item) => {
                const isActive = activeTab === item.tab
                return (
                  <button
                    key={item.tab}
                    onClick={() => setActiveTab(item.tab as Tab)}
                    className={`flex flex-col items-center justify-center gap-0.5 px-2 py-1 rounded-lg transition-colors ${
                      isActive
                        ? 'text-[var(--accent-blue)]'
                        : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
                    }`}
                    aria-label={item.tab}
                    aria-current={isActive ? 'page' : undefined}
                  >
                    <span className="text-lg">{item.icon}</span>
                    <span className="text-[10px] font-medium leading-none">{item.tab}</span>
                  </button>
                )
              })}
            </div>
            {/* Safe area spacer for notch devices */}
            <div className="h-[env(safe-area-inset-bottom)]" />
          </nav>
        )}
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
