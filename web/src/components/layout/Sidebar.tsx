import { useEffect, useState, useMemo, useCallback } from 'react'
import type { Agent, Conversation, DiscoveredAgent } from '../../api/client'
import { listConversations, discoverAgents, autoRegisterAgents } from '../../api/client'
import { DiscoverModal } from '../agents/DiscoverModal'
import { AgentConfig } from '../agents/AgentConfig'
import { ErrorBoundary } from '../ErrorBoundary'

interface SidebarProps {
  agents: Agent[]
  selectedAgent: Agent | null
  onSelectAgent: (agent: Agent) => void
  onAgentsChanged?: () => void
  collapsed?: boolean
  activeConversationId: string | null
  onSelectConversation: (conv: Conversation) => void
  onNewChat: () => void
  onNewChatWithAgent: (agent: Agent) => void
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
  collapsed,
  activeConversationId,
  onSelectConversation,
  onNewChat,
  onNewChatWithAgent,
  conversationVersion,
}: SidebarProps) {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [searchQuery, setSearchQuery] = useState('')
  const [showDiscover, setShowDiscover] = useState(false)
  const [discovered, setDiscovered] = useState<DiscoveredAgent[]>([])
  const [discovering, setDiscovering] = useState(false)
  const [configAgent, setConfigAgent] = useState<Agent | null>(null)
  // Track which agent trees are expanded (by agent id)
  const [expandedAgents, setExpandedAgents] = useState<Set<string>>(new Set())
  // Track expanded "show more" groups within an agent
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())

  const onlineCount = agents.filter((a) => a.status === 'online').length
  const selectedAgentId = selectedAgent?.id

  // Auto-expand selected agent's tree
  useEffect(() => {
    if (selectedAgentId) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing expanded UI state to selected agent id while preserving user-expanded state
      setExpandedAgents((prev) => {
        if (prev.has(selectedAgentId)) return prev
        const next = new Set(prev)
        next.add(selectedAgentId)
        return next
      })
    }
  }, [selectedAgentId])

  // Fetch conversations
  const loadConversations = useCallback(() => {
    listConversations()
      .then((convs) => setConversations(convs))
      .catch(() => setConversations([]))
  }, [])

  useEffect(() => {
    loadConversations()
  }, [loadConversations, conversationVersion])

  // Filter conversations by search query
  const filteredConversations = useMemo(() => {
    if (!searchQuery.trim()) return conversations
    const q = searchQuery.toLowerCase()
    return conversations.filter(
      (c) =>
        (c.title || '').toLowerCase().includes(q) ||
        (c.summary || '').toLowerCase().includes(q) ||
        agents
          .find((a) => a.id === c.agent_id)
          ?.name?.toLowerCase()
          .includes(q),
    )
  }, [conversations, searchQuery, agents])

  // Group conversations by agent, then by date
  // NOTE: Not memoized — needs to re-evaluate when conversations change.
  // the conversation button rendering, and useMemo's reference equality
  // check was preventing that.
  const agentGroups = (() => {
    const byAgent = new Map<string, Conversation[]>()
    for (const conv of filteredConversations) {
      const aid = conv.agent_id
      if (!byAgent.has(aid)) byAgent.set(aid, [])
      byAgent.get(aid)!.push(conv)
    }
    // Sort each agent's conversations by date desc
    for (const convs of byAgent.values()) {
      convs.sort((a, b) => {
        const da = new Date(a.updated_at || a.created_at || 0).getTime()
        const db = new Date(b.updated_at || b.created_at || 0).getTime()
        return db - da
      })
    }
    return agents
      .filter((a) => byAgent.has(a.id))
      .map((agent) => ({
        agent,
        conversations: byAgent.get(agent.id)!,
      }))
  })()

  // Helper: group a conversation list by date
  function groupByDate(convs: Conversation[]) {
    const groups = new Map<string, Conversation[]>()
    for (const conv of convs) {
      const dateStr = conv.updated_at || conv.created_at || new Date().toISOString()
      const group = getDateGroup(dateStr)
      if (!groups.has(group)) groups.set(group, [])
      groups.get(group)!.push(conv)
    }
    return GROUP_ORDER.filter((g) => groups.has(g)).map((g) => ({
      label: g,
      conversations: groups.get(g)!,
    }))
  }

  function toggleAgentTree(agent: Agent) {
    const agentId = agent.id
    // If clicking a different agent than currently selected, switch to it.
    // Instead of dropping into a blank "New conversation", auto-select the
    // agent's most recent conversation so the user keeps their context (#139).
    // onSelectConversation also sets the active agent, so it fully replaces the
    // previous onSelectAgent(null-conversation) behaviour.
    if (selectedAgent?.id !== agentId) {
      const latestConv = conversations
        .filter((c) => c.agent_id === agentId)
        .sort(
          (a, b) =>
            new Date(b.updated_at || b.created_at || 0).getTime() -
            new Date(a.updated_at || a.created_at || 0).getTime(),
        )[0]
      if (latestConv) {
        onSelectConversation(latestConv)
      } else {
        onSelectAgent(agent)
      }
    }
    // Always toggle the tree expand/collapse
    setExpandedAgents((prev) => {
      const next = new Set(prev)
      if (next.has(agentId)) next.delete(agentId)
      else next.add(agentId)
      return next
    })
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

  function handleMissionControl() {
    // Deselect agent → shows MissionControl in main area
    onNewChat()
  }

  return (
    <>
      <aside
        className={`${collapsed ? 'w-16' : 'w-72'} glass-sidebar flex-shrink-0 flex flex-col h-screen transition-all duration-300`}
      >
        {/* Logo + New Chat */}
        <div className="p-4 pb-3 border-b border-[var(--border-subtle)]">
          <div className="flex items-center justify-between mb-3">
            <h1 className="text-lg font-bold gradient-text tracking-tight flex items-center gap-2">
              <img
                src="/favicon.svg"
                alt=""
                aria-hidden="true"
                className="w-6 h-6 flex-shrink-0"
                width={24}
                height={24}
              />
              <span className={collapsed ? 'hidden' : ''}>AgentOS</span>
            </h1>
            <span className="text-[10px] text-[var(--text-muted)] opacity-60">
              {!collapsed && <>{onlineCount}/{agents.length}</>}
            </span>
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
                placeholder="Search..."
                aria-label="Search conversations"
                className="w-full bg-[var(--bg-elevated)] text-[var(--text-secondary)] text-xs border border-[var(--border-subtle)] rounded-xl pl-8 pr-3 py-2 focus:outline-none focus:border-[var(--accent-blue)] placeholder:text-[var(--text-muted)]"
              />
            </div>
          </div>
        )}

        {/* Agent tree + conversations (scrollable) */}
        <div className="flex-1 overflow-y-auto px-2 py-1">
          {collapsed ? (
            /* Collapsed: dots for agents + recent conversations */
            <div className="flex flex-col items-center gap-1.5 py-2">
              {/* Mission Control dot */}
              <button
                onClick={handleMissionControl}
                className={`w-8 h-8 rounded-lg flex items-center justify-center transition-all ${
                  !selectedAgent
                    ? 'bg-[var(--bg-active)] text-[var(--accent-blue)] border border-[var(--border-glow)]'
                    : 'text-[var(--text-muted)] hover:bg-[var(--bg-hover)]'
                }`}
                title="Mission Control"
              >
                <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
                </svg>
              </button>
              {agents.map((agent) => (
                <button
                  key={agent.id}
                  onClick={() => onSelectAgent(agent)}
                  className={`w-8 h-8 rounded-lg flex items-center justify-center text-[10px] font-medium transition-all ${
                    selectedAgent?.id === agent.id
                      ? 'bg-[var(--bg-active)] text-[var(--accent-blue)] border border-[var(--border-glow)]'
                      : 'text-[var(--text-muted)] hover:bg-[var(--bg-hover)]'
                  }`}
                  title={agent.display_name || agent.name}
                >
                  {(agent.display_name || agent.name).charAt(0).toUpperCase()}
                </button>
              ))}
            </div>
          ) : (
            /* Expanded: agent tree */
            <>
              {/* Mission Control (always visible, no conversations) */}
              <div className="mb-1">
                <button
                  onClick={handleMissionControl}
                  className={`w-full flex items-center gap-2 px-2.5 py-2 rounded-xl text-xs transition-all duration-200 group ${
                    !selectedAgent
                      ? 'bg-[var(--bg-active)] text-[var(--text-primary)] border border-[var(--border-glow)]'
                      : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] border border-transparent'
                  }`}
                >
                  {!selectedAgent && (
                    <span className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-4 rounded-r-full bg-gradient-to-b from-[var(--accent-blue)] to-[var(--accent-cyan)]" />
                  )}
                  <svg className="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
                  </svg>
                  <span className="font-semibold">Mission Control</span>
                  <span className="ml-auto text-[10px] px-1.5 py-0.5 rounded-md bg-[var(--bg-elevated)] text-[var(--text-muted)] font-medium">
                    Dashboard
                  </span>
                </button>
              </div>

              {/* Agent folders with nested conversations */}
              {agentGroups.map(({ agent, conversations: agentConvs }) => {
                const isExpanded = expandedAgents.has(agent.id)
                const isSelected = selectedAgent?.id === agent.id
                const dateGroups = groupByDate(agentConvs)

                return (
                  <div key={agent.id} className="mb-1">
                    {/* Agent header row */}
                    <div className="relative group">
                      <button
                        onClick={() => toggleAgentTree(agent)}
                        className={`w-full flex items-center gap-2 pl-2.5 pr-8 py-2 rounded-xl text-xs transition-all duration-200 ${
                          isSelected
                            ? 'bg-[var(--bg-active)] text-[var(--text-primary)]'
                            : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                        }`}
                      >
                        <span className={statusClass(agent.status)} />
                        <span className={`flex-1 text-left font-medium truncate ${isSelected ? 'text-[var(--text-primary)]' : ''}`}>
                          {agent.display_name || agent.name}
                        </span>
                        {/* Chevron */}
                        <svg
                          className={`w-3 h-3 flex-shrink-0 text-[var(--text-muted)] transition-transform duration-200 ${isExpanded ? 'rotate-90' : ''}`}
                          fill="none"
                          stroke="currentColor"
                          viewBox="0 0 24 24"
                        >
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
                        </svg>
                      </button>
                      {/* Gear icon — sibling button (not nested) for valid a11y semantics */}
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setConfigAgent(agent)
                        }}
                        title="Configure agent"
                        aria-label="Configure agent"
                        className="absolute right-1.5 top-1/2 -translate-y-1/2 pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100 p-1 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-active)] transition-all"
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
                    </div>

                    {/* Nested conversations (collapsible) */}
                    {isExpanded && (
                      <div className="pl-3 mt-0.5">
                        {/* Per-agent new chat button */}
                        <button
                          onClick={() => onNewChatWithAgent(agent)}
                          className="w-full flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[10px] text-[var(--text-muted)] hover:text-[var(--accent-blue)] hover:bg-[var(--bg-hover)] transition-all duration-200 mb-1 group/new"
                        >
                          <svg className="w-3 h-3 opacity-50 group-hover/new:opacity-100" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
                          </svg>
                          New chat with {agent.display_name || agent.name}
                        </button>
                        {dateGroups.map((group) => {
                          const groupKey = `${agent.id}:${group.label}`
                          const isGroupExpanded = expandedGroups.has(groupKey)
                          const visible = isGroupExpanded ? group.conversations : group.conversations.slice(0, MAX_VISIBLE_PER_GROUP)
                          const hidden = group.conversations.length - visible.length

                          return (
                            <div key={group.label} className="mb-1">
                              <p className="text-[10px] uppercase tracking-[0.15em] font-semibold text-[var(--text-muted)] pl-3 mb-0.5 opacity-60">
                                {group.label}
                              </p>
                              <div className="space-y-0.5">
                                {visible.map((conv) => {
                                  const isActive = activeConversationId === conv.id
                                  // Prefer the LLM/first-message title (always populated by the
                                  // backend titling pipeline); fall back to the richer `summary`
                                  // override when present. Only show the placeholder when neither
                                  // exists — a freshly-created thread before its first message.
                                  const convLabel = conv.summary || conv.title || ''
                                  const displayText = convLabel || 'New conversation'
                                  return (
                                    <button
                                      key={conv.id}
                                      onClick={() => onSelectConversation(conv)}
                                      className={`w-full text-left px-3 py-1.5 rounded-lg text-[11px] transition-all duration-200 group relative ${
                                        isActive
                                          ? 'bg-[var(--bg-active)] border border-[var(--border-glow)]'
                                          : 'hover:bg-[var(--bg-hover)] border border-transparent'
                                      }`}
                                    >
                                      {isActive && (
                                        <span className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-3 rounded-r-full bg-gradient-to-b from-[var(--accent-blue)] to-[var(--accent-cyan)]" />
                                      )}
                                      <span className={`block truncate ${isActive ? 'text-[var(--text-primary)]' : convLabel ? 'text-[var(--text-secondary)]' : 'text-[var(--text-muted)]'}`}>
                                        {displayText}
                                      </span>
                                    </button>
                                  )
                                })}
                                {hidden > 0 && (
                                  <button
                                    onClick={() => setExpandedGroups((prev) => new Set(prev).add(groupKey))}
                                    className="w-full text-left px-3 py-1 text-[10px] text-[var(--accent-blue)] hover:text-[var(--accent-cyan)] transition-colors"
                                  >
                                    ... {hidden} more
                                  </button>
                                )}
                                {isGroupExpanded && group.conversations.length > MAX_VISIBLE_PER_GROUP && (
                                  <button
                                    onClick={() => setExpandedGroups((prev) => {
                                      const next = new Set(prev)
                                      next.delete(groupKey)
                                      return next
                                    })}
                                    className="w-full text-left px-3 py-1 text-[10px] text-[var(--text-muted)] hover:text-[var(--text-secondary)] transition-colors"
                                  >
                                    Show less
                                  </button>
                                )}
                              </div>
                            </div>
                          )
                        })}
                        {/* Agent has no conversations */}
                        {agentConvs.length === 0 && (
                          <p className="px-3 py-2 text-[10px] text-[var(--text-muted)] opacity-50">
                            No conversations yet
                          </p>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}

              {/* Agents with no conversations still show as collapsed headers */}
              {agents
                .filter((a) => !agentGroups.some((g) => g.agent.id === a.id))
                .map((agent) => {
                  const isSelected = selectedAgent?.id === agent.id
                  return (
                    <div key={agent.id} className="mb-1">
                      <div className="relative group">
                        <button
                          onClick={() => onSelectAgent(agent)}
                          className={`w-full flex items-center gap-2 pl-2.5 pr-8 py-2 rounded-xl text-xs transition-all duration-200 ${
                            isSelected
                              ? 'bg-[var(--bg-active)] text-[var(--text-primary)]'
                              : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                          }`}
                        >
                          <span className={statusClass(agent.status)} />
                          <span className={`flex-1 text-left font-medium truncate`}>
                            {agent.display_name || agent.name}
                          </span>
                        </button>
                        {/* Gear icon — sibling button (not nested) for valid a11y semantics */}
                        <button
                          onClick={(e) => {
                            e.stopPropagation()
                            setConfigAgent(agent)
                          }}
                          title="Configure agent"
                          aria-label="Configure agent"
                          className="absolute right-1.5 top-1/2 -translate-y-1/2 pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100 p-1 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-active)] transition-all"
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
                      </div>
                    </div>
                  )
                })}

              {/* Empty state */}
              {conversations.length === 0 && !searchQuery && (
                <div className="px-3 py-6 text-center">
                  <p className="text-xs text-[var(--text-muted)]">No conversations yet</p>
                  <p className="text-[10px] text-[var(--text-muted)] opacity-50 mt-1">
                    Click &quot;New Chat&quot; to start
                  </p>
                </div>
              )}

              {/* No search results */}
              {searchQuery && filteredConversations.length === 0 && (
                <div className="px-3 py-4 text-center">
                  <p className="text-xs text-[var(--text-muted)]">No results for &quot;{searchQuery}&quot;</p>
                </div>
              )}
            </>
          )}
        </div>

        {/* Bottom: Discover */}
        <div className="p-3 border-t border-[var(--border-subtle)]">
          <button
            onClick={handleDiscover}
            className={`pill-btn pill-btn--ghost text-xs w-full justify-center ${collapsed ? 'px-0' : ''}`}
            title="Discover Agents"
            aria-label="Discover Agents"
          >
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            {!collapsed && <span className="ml-1">Discover Agents</span>}
          </button>
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
