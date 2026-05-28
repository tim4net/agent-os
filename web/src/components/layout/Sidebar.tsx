import { useEffect, useState, useMemo, useCallback } from 'react'
import type { Agent, Conversation, Model, DiscoveredAgent } from '../../api/client'
import { listConversations, getAgentModels, discoverAgents, autoRegisterAgents } from '../../api/client'
import { DiscoverModal } from '../agents/DiscoverModal'
import { AgentConfig } from '../agents/AgentConfig'
import { ErrorBoundary } from '../ErrorBoundary'

function modelDisplayName(m: Model): string {
  return m.display_name || m.id
}

interface SidebarProps {
  agents: Agent[]
  selectedAgent: Agent | null
  onSelectAgent: (agent: Agent) => void
  onAgentsChanged?: () => void
  activeTab: string
  collapsed?: boolean
  activeConversationId: string | null
  onSelectConversation: (conv: Conversation) => void
  onNewChat: () => void
  conversationVersion: number // bump to trigger refresh
}

function statusClass(status: string): string {
  switch (status) {
    case 'online':
      return 'agent-dot agent-dot--online'
    case 'offline':
    case 'error':
      return 'agent-dot agent-dot--offline'
    default:
      return 'agent-dot'
  }
}

// Date grouping logic
function getDateGroup(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterday = new Date(today.getTime() - 86400000)
  const thisWeekStart = new Date(today.getTime() - today.getDay() * 86400000)

  if (date >= today) return 'Today'
  if (date >= yesterday) return 'Yesterday'
  if (date >= thisWeekStart) return 'This Week'
  return 'Older'
}

const GROUP_ORDER = ['Today', 'Yesterday', 'This Week', 'Older']
const MAX_VISIBLE_PER_GROUP = 5

export function Sidebar({
  agents,
  selectedAgent,
  onSelectAgent,
  onAgentsChanged,
  activeTab,
  collapsed,
  activeConversationId,
  onSelectConversation,
  onNewChat,
  conversationVersion,
}: SidebarProps) {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [searchQuery, setSearchQuery] = useState('')
  const [models, setModels] = useState<Model[]>([])
  const [showDiscover, setShowDiscover] = useState(false)
  const [discovered, setDiscovered] = useState<DiscoveredAgent[]>([])
  const [discovering, setDiscovering] = useState(false)
  const [configAgent, setConfigAgent] = useState<Agent | null>(null)
  const [showAgents, setShowAgents] = useState(true)
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())

  const onlineCount = agents.filter((a) => a.status === 'online').length

  // Fetch conversations
  const loadConversations = useCallback(() => {
    listConversations()
      .then((convs) => setConversations(convs))
      .catch(() => setConversations([]))
  }, [])

  useEffect(() => {
    loadConversations()
  }, [loadConversations, conversationVersion])

  // Fetch models for selected agent
  useEffect(() => {
    if (!selectedAgent) {
      setModels([])
      return
    }
    getAgentModels(selectedAgent.id)
      .then(setModels)
      .catch(() => setModels([]))
  }, [selectedAgent])

  // Filter conversations by search query
  const filteredConversations = useMemo(() => {
    if (!searchQuery.trim()) return conversations
    const q = searchQuery.toLowerCase()
    return conversations.filter(
      (c) =>
        (c.title || '').toLowerCase().includes(q) ||
        agents
          .find((a) => a.id === c.agent_id)
          ?.name?.toLowerCase()
          .includes(q),
    )
  }, [conversations, searchQuery, agents])

  // Group conversations by date
  const groupedConversations = useMemo(() => {
    const groups = new Map<string, Conversation[]>()
    for (const conv of filteredConversations) {
      const dateStr = conv.updated_at || conv.created_at || new Date().toISOString()
      const group = getDateGroup(dateStr)
      if (!groups.has(group)) groups.set(group, [])
      groups.get(group)!.push(conv)
    }
    return GROUP_ORDER.filter((g) => groups.has(g)).map((g) => ({
      label: g,
      conversations: groups.get(g)!,
    }))
  }, [filteredConversations])

  // Get agent name by ID
  function getAgentName(agentId: string): string {
    const agent = agents.find((a) => a.id === agentId)
    return agent?.display_name || agent?.name || 'Unknown'
  }

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
    try {
      const found = await discoverAgents()
      setDiscovered(found)
    } catch {
      setDiscovered([])
    }
  }

  function handleConfigSaved(updated: Agent) {
    onAgentsChanged?.()
    if (selectedAgent?.id === updated.id) {
      onSelectAgent(updated)
    }
  }

  return (
    <>
      <aside
        className={`${collapsed ? 'w-16' : 'w-72'} glass-sidebar flex-shrink-0 flex flex-col h-screen transition-all duration-300`}
      >
        {/* Logo + New Chat */}
        <div className="p-4 pb-3 border-b border-[var(--border-subtle)]">
          <div className="flex items-center justify-between mb-3">
            <h1 className="text-lg font-bold gradient-text tracking-tight">
              <span className={collapsed ? 'hidden' : ''}>Agent OS</span>
              <span className={collapsed ? '' : 'hidden'}>OS</span>
            </h1>
          </div>
          {!collapsed && (
            <button
              onClick={onNewChat}
              className="pill-btn pill-btn--primary w-full text-xs flex items-center justify-center gap-1.5"
            >
              <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              New Chat
            </button>
          )}
          {collapsed && (
            <button
              onClick={onNewChat}
              className="w-full flex items-center justify-center p-2 rounded-xl bg-gradient-to-br from-[var(--accent-blue)] to-[var(--accent-purple)] text-white"
              aria-label="New chat"
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
            </button>
          )}
        </div>

        {/* Search */}
        {!collapsed && (
          <div className="px-3 py-2">
            <div className="relative">
              <svg
                className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-[var(--text-muted)]"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search conversations..."
                aria-label="Search conversations"
                className="w-full bg-[var(--bg-elevated)] text-[var(--text-secondary)] text-xs border border-[var(--border-subtle)] rounded-xl pl-8 pr-3 py-2 focus:outline-none focus:border-[var(--accent-blue)] placeholder:text-[var(--text-muted)]"
              />
            </div>
          </div>
        )}

        {/* Conversation list - scrollable */}
        <div className="flex-1 overflow-y-auto px-2 py-1 stagger-children">
          {collapsed ? (
            /* Collapsed: just show dots for recent conversations */
            <div className="flex flex-col items-center gap-1.5 py-2">
              {conversations.slice(0, 10).map((conv) => (
                <button
                  key={conv.id}
                  onClick={() => onSelectConversation(conv)}
                  className={`w-8 h-8 rounded-lg flex items-center justify-center text-[10px] font-medium transition-all ${
                    activeConversationId === conv.id
                      ? 'bg-[var(--bg-active)] text-[var(--accent-blue)] border border-[var(--border-glow)]'
                      : 'text-[var(--text-muted)] hover:bg-[var(--bg-hover)]'
                  }`}
                  title={conv.title || 'Untitled'}
                >
                  {getAgentName(conv.agent_id).charAt(0).toUpperCase()}
                </button>
              ))}
            </div>
          ) : (
            /* Expanded: date-grouped conversation list */
            groupedConversations.map((group) => {
              const isExpanded = expandedGroups.has(group.label)
              const visible = isExpanded ? group.conversations : group.conversations.slice(0, MAX_VISIBLE_PER_GROUP)
              const hidden = group.conversations.length - visible.length
              return (
              <div key={group.label} className="mb-3">
                <p className="text-[10px] uppercase tracking-[0.15em] font-semibold text-[var(--text-muted)] px-2 mb-1 opacity-60">
                  {group.label}
                </p>
                <div className="space-y-0.5">
                  {visible.map((conv) => {
                    const isActive = activeConversationId === conv.id
                    const agentName = getAgentName(conv.agent_id)
                    return (
                      <button
                        key={conv.id}
                        onClick={() => onSelectConversation(conv)}
                        className={`w-full text-left px-3 py-2 rounded-xl text-xs transition-all duration-200 group relative ${
                          isActive
                            ? 'bg-[var(--bg-active)] border border-[var(--border-glow)]'
                            : 'hover:bg-[var(--bg-hover)] border border-transparent'
                        }`}
                      >
                        {isActive && (
                          <span className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-4 rounded-r-full bg-gradient-to-b from-[var(--accent-blue)] to-[var(--accent-cyan)]" />
                        )}
                        <div className="flex items-center gap-2 min-w-0">
                          <span className="text-[10px] text-[var(--text-muted)] flex-shrink-0 font-medium">
                            {agentName}
                          </span>
                          <span className="text-[var(--text-muted)] flex-shrink-0">·</span>
                          <span className={`truncate ${isActive ? 'text-[var(--text-primary)]' : 'text-[var(--text-secondary)]'}`}>
                            {conv.title || 'Untitled'}
                          </span>
                        </div>
                      </button>
                    )
                  })}
                  {hidden > 0 && (
                    <button
                      onClick={() => setExpandedGroups((prev) => new Set(prev).add(group.label))}
                      className="w-full text-left px-3 py-1.5 text-[10px] text-[var(--accent-blue)] hover:text-[var(--accent-cyan)] transition-colors"
                    >
                      ... {hidden} more
                    </button>
                  )}
                  {isExpanded && group.conversations.length > MAX_VISIBLE_PER_GROUP && (
                    <button
                      onClick={() => setExpandedGroups((prev) => {
                        const next = new Set(prev)
                        next.delete(group.label)
                        return next
                      })}
                      className="w-full text-left px-3 py-1.5 text-[10px] text-[var(--text-muted)] hover:text-[var(--text-secondary)] transition-colors"
                    >
                      Show less
                    </button>
                  )}
                </div>
              </div>
              )
            })
          )}

          {/* Empty state */}
          {!collapsed && conversations.length === 0 && !searchQuery && (
            <div className="px-3 py-6 text-center">
              <p className="text-xs text-[var(--text-muted)]">No conversations yet</p>
              <p className="text-[10px] text-[var(--text-muted)] opacity-50 mt-1">
                Click "New Chat" to start
              </p>
            </div>
          )}

          {/* No search results */}
          {!collapsed && searchQuery && filteredConversations.length === 0 && (
            <div className="px-3 py-4 text-center">
              <p className="text-xs text-[var(--text-muted)]">No results for "{searchQuery}"</p>
            </div>
          )}
        </div>

        {/* Models for selected agent - only show when expanded and in Chat tab */}
        {!collapsed && selectedAgent && models.length > 0 && activeTab === 'Chat' && (
          <div className="px-3 py-2 border-t border-[var(--border-subtle)]">
            <p className="text-[10px] uppercase tracking-[0.15em] font-semibold text-[var(--text-muted)] px-1 mb-1.5">
              Models · {selectedAgent.display_name || selectedAgent.name}
            </p>
            <div className="space-y-0.5">
              {models.map((m) => (
                <div key={m.id} className="px-2 py-0.5 text-[10px] font-mono text-[var(--text-muted)] truncate" title={m.id}>
                  {modelDisplayName(m)}
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Agents section */}
        <div className="border-t border-[var(--border-subtle)]">
          {!collapsed && (
            <button
              onClick={() => setShowAgents(!showAgents)}
              className="w-full flex items-center justify-between px-3 py-2 text-[10px] uppercase tracking-[0.15em] font-semibold text-[var(--text-muted)] hover:text-[var(--text-secondary)] transition-colors"
            >
              <span>
                Agents
                <span className="ml-1.5 inline-flex items-center gap-1 normal-case tracking-normal font-normal">
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 shadow-[0_0_6px_rgba(34,197,94,0.5)]" />
                  {onlineCount}/{agents.length}
                </span>
              </span>
              <svg
                className={`w-3 h-3 transition-transform duration-200 ${showAgents ? 'rotate-180' : ''}`}
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
              </svg>
            </button>
          )}

          {(collapsed || showAgents) && (
            <div className={collapsed ? 'flex flex-col items-center gap-1 py-2 px-2' : 'px-2 pb-2 space-y-0.5'}>
              {agents.map((agent) => {
                const isSelected = selectedAgent?.id === agent.id
                return (
                  <div key={agent.id} className="relative group">
                    <button
                      onClick={() => onSelectAgent(agent)}
                      title={agent.display_name || agent.name}
                      className={`w-full flex items-center gap-2 px-2 py-1.5 rounded-lg text-xs transition-all duration-200 ${
                        isSelected
                          ? 'bg-[var(--bg-active)] text-[var(--text-primary)]'
                          : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                      } ${collapsed ? 'justify-center' : ''}`}
                    >
                      <span className={statusClass(agent.status)} />
                      {!collapsed && (
                        <span className="truncate font-medium">
                          {agent.display_name || agent.name}
                        </span>
                      )}
                    </button>
                    {/* Gear icon */}
                    {!collapsed && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setConfigAgent(agent)
                        }}
                        title="Configure agent"
                        aria-label="Configure agent"
                        className="absolute right-1.5 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 p-1 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-active)] transition-all"
                      >
                        <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path
                            strokeLinecap="round"
                            strokeLinejoin="round"
                            strokeWidth={2}
                            d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 01-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 011.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
                          />
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                        </svg>
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* Bottom actions */}
        <div className="p-3 border-t border-[var(--border-subtle)] flex items-center gap-2">
          <button
            onClick={handleDiscover}
            className={`pill-btn pill-btn--ghost text-xs flex-1 ${collapsed ? 'px-0' : ''}`}
            title="Discover Agents"
            aria-label="Discover Agents"
          >
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            {!collapsed && <span className="ml-1">Discover</span>}
          </button>
          {!collapsed && (
            <span className="text-[10px] text-[var(--text-muted)] opacity-60">
              <span className="inline-flex items-center gap-1">
                <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 shadow-[0_0_6px_rgba(34,197,94,0.5)]" />
                {onlineCount}/{agents.length}
              </span>
            </span>
          )}
        </div>
      </aside>

      {/* Discover modal */}
      {showDiscover && (
        <ErrorBoundary name="Discover Modal">
          <DiscoverModal
            agents={discovered}
            loading={discovering}
            onRegister={handleRegister}
            onClose={() => setShowDiscover(false)}
          />
        </ErrorBoundary>
      )}

      {/* Agent config modal */}
      {configAgent && (
        <AgentConfig
          agent={configAgent}
          onClose={() => setConfigAgent(null)}
          onSaved={handleConfigSaved}
        />
      )}
    </>
  )
}
