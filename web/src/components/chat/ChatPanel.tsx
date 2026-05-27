import { useEffect, useRef, useState } from 'react'
import type { Agent, Message, Model, AgentCommand } from '../../api/client'
import { getAgentModels, getAgentCommands, getMessages, sendChat, exportConversation, executeSlashCommand } from '../../api/client'
import { MessageBubble } from './MessageBubble'
import { VoiceButton } from './VoiceButton'
import { showToast } from '../Toast'

interface ChatPanelProps {
  agent: Agent
  activeConversationId: string | null
  onConversationLoaded: () => void
  onConversationCreated: (convId: string) => void
  onNewChat: () => void
}

function modelLabel(m: Model): string {
  return m.display_name || m.id
}

export function ChatPanel({ agent, activeConversationId, onConversationLoaded, onConversationCreated, onNewChat }: ChatPanelProps) {
  const [models, setModels] = useState<Model[]>([])
  const [selectedModel, setSelectedModel] = useState('')
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [partialContent, setPartialContent] = useState('')
  const [conversationId, setConversationId] = useState<string | null>(activeConversationId)
  const [exporting, setExporting] = useState(false)
  const [loadingHistory, setLoadingHistory] = useState(false)
  const [showSlashMenu, setShowSlashMenu] = useState(false)
  const [slashFilter, setSlashFilter] = useState('')
  const [slashCommands, setSlashCommands] = useState<AgentCommand[]>([])
  const [contextSources, setContextSources] = useState<string[]>([])
  const bottomRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const slashMenuRef = useRef<HTMLDivElement>(null)

  // Load models and slash commands for the agent
  useEffect(() => {
    getAgentModels(agent.id)
      .then((m) => {
        setModels(m)
        if (m.length > 0 && !selectedModel) setSelectedModel(m[0].id)
      })
      .catch(() => setModels([]))
    getAgentCommands(agent.id)
      .then(setSlashCommands)
      .catch(() => setSlashCommands([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agent.id])

  // Load conversation when activeConversationId changes
  useEffect(() => {
    if (activeConversationId && activeConversationId !== conversationId) {
      setConversationId(activeConversationId)
      setLoadingHistory(true)
      getMessages(activeConversationId)
        .then((msgs) => {
          setMessages(msgs)
          setPartialContent('')
          onConversationLoaded()
        })
        .catch(() => {
          showToast('Failed to load conversation', 'error')
        })
        .finally(() => setLoadingHistory(false))
    } else if (activeConversationId === null && conversationId !== null) {
      // New chat — clear everything
      setMessages([])
      setConversationId(null)
      setPartialContent('')
      setInput('')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeConversationId])

  // Auto-scroll to bottom
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, partialContent])

  // Close slash menu on outside click
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (slashMenuRef.current && !slashMenuRef.current.contains(e.target as Node)) {
        setShowSlashMenu(false)
      }
    }
    if (showSlashMenu) {
      document.addEventListener('mousedown', handleClickOutside)
      return () => document.removeEventListener('mousedown', handleClickOutside)
    }
  }, [showSlashMenu])

  // Detect slash commands in input
  useEffect(() => {
    const trimmed = input.trimStart()
    if (trimmed.startsWith('/')) {
      const parts = trimmed.split(' ')
      setSlashFilter(parts[0].toLowerCase())
      setShowSlashMenu(true)
    } else {
      setShowSlashMenu(false)
    }
  }, [input])

  const filteredCommands = slashCommands.filter((c) =>
    c.command.startsWith(slashFilter)
  )

  async function handleSlashCommand(command: string) {
    setStreaming(true)
    setInput('')
    setShowSlashMenu(false)
    try {
      const result = await executeSlashCommand(command, agent.id, conversationId ?? undefined)
      showToast(result.message, 'success')

      if (result.type === 'new' && result.data?.conversation_id) {
        const newConvId = result.data.conversation_id as string
        setConversationId(newConvId)
        onConversationCreated(newConvId)
        setMessages([])
        setPartialContent('')
      } else if (result.type === 'clear') {
        setMessages([])
        setPartialContent('')
      } else if (result.type === 'compact') {
        if (conversationId) {
          const msgs = await getMessages(conversationId)
          setMessages(msgs)
        }
      }
    } catch (err) {
      showToast('Slash command failed: ' + (err instanceof Error ? err.message : 'Unknown error'), 'error')
    } finally {
      setStreaming(false)
    }
  }

  function handleSlashSelect(cmd: string) {
    handleSlashCommand(cmd)
  }

  async function handleSend() {
    const text = input.trim()
    if (!text || streaming) return

    if (text.startsWith('/')) {
      await handleSlashCommand(text)
      return
    }

    const userMsg: Message = {
      id: crypto.randomUUID(),
      role: 'user',
      content: text,
      created_at: new Date().toISOString(),
    }
    setMessages((prev) => [...prev, userMsg])
    setInput('')
    setStreaming(true)
    setPartialContent('')
    setContextSources([])

    try {
      const stream = sendChat(agent.id, text, selectedModel || undefined, conversationId ?? undefined)
      const reader = stream.getReader()

      let accumulated = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        if (value.content !== undefined) {
          accumulated += value.content
          setPartialContent(accumulated)
        }

        if (value.done) {
          const assistantMsg: Message = {
            id: crypto.randomUUID(),
            role: 'assistant',
            content: accumulated,
            created_at: new Date().toISOString(),
          }
          setMessages((prev) => [...prev, assistantMsg])
          setPartialContent('')
          if (value.conversation_id) {
            const newConvId = value.conversation_id as string
            setConversationId(newConvId)
            // Notify parent about new conversation so sidebar can refresh
            if (!conversationId) {
              onConversationCreated(newConvId)
            }
          }
          // Show RAG context sources if any were injected
          if (Array.isArray(value.context_sources) && value.context_sources.length > 0) {
            setContextSources(value.context_sources as string[])
          }
        }
      }
    } catch (err) {
      console.error('Chat error:', err)
      const errMsg = err instanceof Error ? err.message : 'Unknown error'
      setMessages((prev) => [
        ...prev,
        {
          id: crypto.randomUUID(),
          role: 'assistant',
          content: `⚠️ **Failed to get response.**\n\n\`${errMsg}\`\n\nTry sending your message again.`,
          created_at: new Date().toISOString(),
        },
      ])
      setPartialContent('')
    } finally {
      setStreaming(false)
    }
  }

  async function handleExport() {
    if (!conversationId) {
      showToast('No conversation to export yet', 'info')
      return
    }
    setExporting(true)
    try {
      const result = await exportConversation(conversationId)
      showToast(`Exported to Obsidian: ${result.path}`, 'success')
    } catch {
      showToast('Failed to export conversation', 'error')
    } finally {
      setExporting(false)
    }
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* Minimal header */}
      <div className="flex items-center justify-between px-5 py-3 border-b border-[var(--border-subtle)] bg-[var(--bg-surface)]">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <span className="agent-dot agent-dot--online" />
            <h2 className="text-sm font-medium text-[var(--text-primary)]">
              {agent.display_name || agent.name}
            </h2>
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          <button
            onClick={onNewChat}
            className="pill-btn pill-btn--ghost text-xs py-1.5 px-3"
            title="New chat"
          >
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            <span className="hidden sm:inline">New</span>
          </button>
          <button
            onClick={handleExport}
            disabled={exporting || !conversationId}
            className="pill-btn pill-btn--ghost text-xs py-1.5 px-3 disabled:opacity-30"
            title={!conversationId ? 'Send a message first to export' : 'Export to Obsidian'}
            aria-label={!conversationId ? 'Export to Obsidian (disabled)' : 'Export to Obsidian'}
          >
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 10v6m0 0l-3-3m3 3l3-3m2 8H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
          </button>
          {models.length > 0 && (
            <select
              value={selectedModel}
              onChange={(e) => setSelectedModel(e.target.value)}
              aria-label="Select model"
              className="bg-[var(--bg-elevated)] text-[var(--text-secondary)] text-xs border border-[var(--border-subtle)] rounded-xl px-2.5 py-1.5 focus:outline-none focus:border-[var(--accent-blue)] font-mono"
            >
              {models.map((m) => (
                <option key={m.id} value={m.id}>
                  {modelLabel(m)}
                </option>
              ))}
            </select>
          )}
        </div>
      </div>

      {/* Loading overlay */}
      {loadingHistory && (
        <div className="px-5 py-2 bg-[var(--bg-elevated)] text-[var(--text-muted)] text-xs flex items-center gap-2 border-b border-[var(--border-subtle)]">
          <div className="typing-dots">
            <span /><span /><span />
          </div>
          Loading conversation...
        </div>
      )}

      {/* Messages area */}
      <div className="flex-1 overflow-y-auto px-5 py-6">
        {messages.length === 0 && !partialContent && !streaming && (
          <div className="flex flex-col items-center justify-center h-full fade-in">
            <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-[var(--accent-blue)] via-[var(--accent-purple)] to-[var(--accent-cyan)] flex items-center justify-center mb-4 shadow-[var(--shadow-glow)]">
              <svg className="w-8 h-8 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
              </svg>
            </div>
            <h3 className="text-lg font-medium text-[var(--text-primary)] mb-1">
              Chat with {agent.display_name || agent.name}
            </h3>
            <p className="text-sm text-[var(--text-muted)] max-w-sm text-center">
              Start a conversation below. Type / for available commands.
            </p>
          </div>
        )}

        <div className="max-w-3xl mx-auto space-y-1">
          {messages.map((msg) => (
            <MessageBubble key={msg.id} message={msg} />
          ))}
          {partialContent && (
            <MessageBubble
              message={{
                id: 'streaming',
                role: 'assistant',
                content: partialContent,
              }}
            />
          )}
          {streaming && !partialContent && (
            <div className="flex items-start mb-4 slide-up">
              <div className="glass-card px-4 py-3 rounded-2xl rounded-bl-md">
                <div className="typing-dots">
                  <span /><span /><span />
                </div>
              </div>
            </div>
          )}
        </div>
        <div ref={bottomRef} />
      </div>

      {/* Floating input bar */}
      <div className="px-5 pb-5 pt-2 relative">
        {/* Slash command autocomplete */}
        {showSlashMenu && filteredCommands.length > 0 && (
          <div
            ref={slashMenuRef}
            className="absolute bottom-full left-5 right-5 mb-2 glass-card p-1.5 shadow-[var(--shadow-float)] z-10"
          >
            {filteredCommands.map((cmd) => (
              <button
                key={cmd.command}
                onClick={() => handleSlashSelect(cmd.command)}
                className="w-full flex items-center gap-3 px-4 py-2.5 text-sm rounded-xl hover:bg-[var(--bg-active)] transition-colors text-left"
              >
                <span className="text-[var(--accent-blue)] font-mono font-medium text-xs">{cmd.command}</span>
                <span className="text-[var(--text-muted)] text-xs">{cmd.description}</span>
              </button>
            ))}
          </div>
        )}

        <div className="floating-input flex items-end gap-2 p-2 max-w-3xl mx-auto">
          <VoiceButton onTranscribed={(text) => setInput((prev) => prev ? prev + ' ' + text : text)} />
          <textarea
            ref={inputRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Message..."
            aria-label="Chat message"
            rows={1}
            className="flex-1 bg-transparent text-[var(--text-primary)] text-sm px-4 py-2.5 resize-none border-none focus:outline-none placeholder:text-[var(--text-muted)]"
          />
          <button
            onClick={handleSend}
            disabled={streaming || !input.trim()}
            aria-label="Send message"
            className={`flex-shrink-0 w-10 h-10 rounded-xl flex items-center justify-center transition-all duration-200 ${
              input.trim() && !streaming
                ? 'bg-gradient-to-br from-[var(--accent-blue)] to-[var(--accent-purple)] text-white shadow-[var(--shadow-glow)] hover:shadow-[0_0_28px_rgba(91,141,239,0.4)] hover:scale-105 active:scale-95'
                : 'bg-[var(--bg-elevated)] text-[var(--text-muted)] cursor-not-allowed'
            }`}
          >
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 12h14M12 5l7 7-7 7" />
            </svg>
          </button>
        </div>

        {/* Subtle context line */}
        {contextSources.length > 0 && (
          <div className="flex items-center justify-center mt-1.5 gap-1">
            <span className="text-[10px] text-[var(--accent-blue)] opacity-60">
              📚 {contextSources.length} knowledge source{contextSources.length !== 1 ? 's' : ''} used:
            </span>
            {contextSources.map((src, i) => (
              <span key={i} className="text-[10px] text-[var(--text-muted)] opacity-50" title={src}>
                {src.length > 20 ? src.slice(0, 20) + '…' : src}
                {i < contextSources.length - 1 && ','}
              </span>
            ))}
            <button
              onClick={() => setContextSources([])}
              className="text-[10px] text-[var(--text-muted)] opacity-30 hover:opacity-60 ml-1"
              aria-label="Dismiss context sources"
            >
              ✕
            </button>
          </div>
        )}
        {conversationId && (
          <div className="flex items-center justify-center mt-1.5">
            <span className="text-[10px] text-[var(--text-muted)] opacity-50 font-mono">
              {messages.length > 0 ? `${messages.length} messages` : 'New conversation'} · {conversationId.slice(0, 8)}
            </span>
          </div>
        )}
      </div>
    </div>
  )
}
