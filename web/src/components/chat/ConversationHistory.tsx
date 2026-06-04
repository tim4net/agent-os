import { useCallback, useEffect, useState, useMemo } from 'react'
import type { Agent, Conversation } from '../../api/client'
import { listConversations } from '../../api/client'

interface ConversationHistoryProps {
  agents: Agent[]
  currentConversationId: string | null
  onSelectConversation: (conv: Conversation) => void
  onNewChat: () => void
}

export function ConversationHistory({
  agents,
  currentConversationId,
  onSelectConversation,
  onNewChat,
}: ConversationHistoryProps) {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; loading state starts the conversation list request and result state lands after the promise settles
    setLoading(true)
    setError(null)
    listConversations()
      .then((convs) => setConversations(convs))
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [currentConversationId])

  const getAgentName = useCallback((agentId: string): string => {
    const agent = agents.find((a) => a.id === agentId)
    return agent?.display_name || agent?.name || agentId
  }, [agents])

  // Group conversations by agent_id for better organization
  const groupedConversations = useMemo(() => {
    const groups = new Map<string, { agentName: string; convos: Conversation[] }>()
    for (const conv of conversations) {
      const agentName = getAgentName(conv.agent_id)
      if (!groups.has(conv.agent_id)) {
        groups.set(conv.agent_id, { agentName, convos: [] })
      }
      groups.get(conv.agent_id)!.convos.push(conv)
    }
    return Array.from(groups.entries())
  }, [conversations, getAgentName])

  return (
    <div className="flex flex-col h-full bg-gray-900/50">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2.5 border-b border-gray-800 flex-shrink-0">
        <h3 className="text-xs font-semibold text-gray-400 uppercase tracking-wider">
          History
        </h3>
        <button
          onClick={() => {
            onNewChat()
          }}
          className="px-2 py-1 text-xs font-medium rounded bg-gray-800 text-gray-300 hover:bg-gray-700 hover:text-white transition-colors"
        >
          + New
        </button>
      </div>

      {/* List */}
      <div className="flex-1 overflow-y-auto" role="list" aria-label="Conversation history">
        {loading && (
          <div className="px-3 py-4 text-gray-500 text-xs">Loading...</div>
        )}
        {error && (
          <div className="px-3 py-4 text-red-400 text-xs">Error: {error}</div>
        )}
        {!loading && !error && conversations.length === 0 && (
          <div className="px-3 py-4 text-gray-500 text-xs">No conversations yet.</div>
        )}
        {groupedConversations.map(([agentId, group]) => (
          <div key={agentId} role="group" aria-label={`${group.agentName} conversations`}>
            {/* Agent section header */}
            <div className="px-3 py-1.5 bg-gray-800/40 border-b border-gray-800/60">
              <span className="text-[10px] font-semibold text-gray-500 uppercase tracking-wider">
                {group.agentName}
              </span>
              <span className="text-[10px] text-gray-600 ml-1.5">({group.convos.length})</span>
            </div>
            {/* Conversations under this agent */}
            {group.convos.map((conv) => (
              <button
                key={conv.id}
                onClick={() => onSelectConversation(conv)}
                role="listitem"
                className={`w-full text-left px-3 py-2.5 border-b border-gray-800/50 transition-colors ${
                  currentConversationId === conv.id
                    ? 'bg-blue-600/10 border-l-2 border-l-blue-500'
                    : 'hover:bg-gray-800/50'
                }`}
              >
                <div className="text-sm text-gray-300 truncate">
                  {conv.title || 'Untitled'}
                </div>
              </button>
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}
