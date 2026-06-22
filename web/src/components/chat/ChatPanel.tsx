import { useCallback, useEffect, useRef, useState } from 'react'
import type { Agent, Message, Model, AgentCommand } from '../../api/client'
import { Icon } from '../Icon'

// crypto.randomUUID() requires a secure context (HTTPS).
// Provide a fallback for HTTP / non-secure environments (Tailscale LAN, etc.).
function uuid(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0
    return (c === 'x' ? r : (r & 0x3) | 0x8).toString(16)
  })
}
import { getAgentModels, getAgentCommands, getMessages, sendChat, exportConversation, executeSlashCommand } from '../../api/client'
import { MessageBubble } from './MessageBubble'
import { VoiceButton } from './VoiceButton'
import { showToast } from '../toast-bus'

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

// Map raw tool names to friendly labels with icons
function toolLabel(name: string): string {
  const map: Record<string, string> = {
    terminal:       '💻 Running terminal…',
    shell:          '💻 Running terminal…',
    process:        '💻 Running process…',
    search_files:   '🔍 Searching files…',
    grep:           '🔍 Searching files…',
    read_file:      '📄 Reading file…',
    write_file:     '📝 Writing file…',
    patch:          '📝 Editing file…',
    vision_analyze: '👁️ Analyzing image…',
    browser:        '🌐 Using browser…',
    web_search:     '🌐 Searching web…',
    fetch:          '🌐 Fetching URL…',
  }
  if (map[name]) return map[name]
  // Generic fallback — prettify the name
  const pretty = name.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
  return `🔧 ${pretty}…`
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
  const [loadError, setLoadError] = useState<string | null>(null)
  const [slashMenuDismissed, setSlashMenuDismissed] = useState(false)
  const [slashCommands, setSlashCommands] = useState<AgentCommand[]>([])
  const [contextSources, setContextSources] = useState<string[]>([])
  const [activeTools, setActiveTools] = useState<string[]>([])
  const [queuedMessage, setQueuedMessage] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const slashMenuRef = useRef<HTMLDivElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  // Tracks the in-flight conversation-history request so it can be
  // cancelled on navigation/unmount and guarded against stale state updates.
  const loadControllerRef = useRef<AbortController | null>(null)

  const trimmedInput = input.trimStart()
  const slashFilter = trimmedInput.startsWith('/') ? trimmedInput.split(' ')[0].toLowerCase() : ''
  const showSlashMenu = slashFilter !== '' && !slashMenuDismissed

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

  // Load (or re-load) a conversation's history. Guards against hung requests
  // (#122): a 10s AbortController timeout cancels stale loads and surfaces a
  // retryable error state instead of leaving "Loading conversation..." up
  // forever. Any in-flight load is cancelled first so navigation between
  // conversations never races two requests.
  const LOAD_HISTORY_TIMEOUT_MS = 10_000
  const loadConversation = useCallback((convId: string) => {
    // Cancel any previous in-flight load.
    loadControllerRef.current?.abort()
    const controller = new AbortController()
    loadControllerRef.current = controller

    setLoadingHistory(true)
    setLoadError(null)

    const timeoutId = window.setTimeout(() => controller.abort(), LOAD_HISTORY_TIMEOUT_MS)

    getMessages(convId, controller.signal)
      .then((msgs) => {
        // Guard: if the request resolved after being aborted (timeout/nav),
        // ignore the result.
        if (controller.signal.aborted) return
        setMessages(msgs)
        setPartialContent('')
        onConversationLoaded()
      })
      .catch(() => {
        // Ignore outcomes for loads superseded by a newer selection.
        if (loadControllerRef.current !== controller) return
        const timedOut = controller.signal.aborted
        setLoadError(
          timedOut
            ? 'Conversation is taking too long to load. It may be stale or unreachable.'
            : 'Failed to load conversation. It may have been deleted or is temporarily unavailable.',
        )
        showToast(timedOut ? 'Conversation load timed out' : 'Failed to load conversation', 'error')
      })
      .finally(() => {
        window.clearTimeout(timeoutId)
        // Only clear the spinner if this is still the active load; a newer
        // selection has already set its own spinner state.
        if (loadControllerRef.current === controller) {
          setLoadingHistory(false)
        }
      })
  }, [onConversationLoaded])

  // Load conversation when activeConversationId changes
  useEffect(() => {
    if (activeConversationId) {
      const isDifferentConv = activeConversationId !== conversationId
      // Fresh-mount detection: React key={selectedAgent.id} remounts ChatPanel,
      // initializing conversationId to activeConversationId — the !== check above
      // never fires. Detect via messages.length === 0 on a non-streaming mount.
      const isFreshMount = conversationId === activeConversationId && messages.length === 0 && !streaming
      if (isDifferentConv || isFreshMount) {
        // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing local conversation id from external activeConversationId prop before async history load
        setConversationId(activeConversationId)
        loadConversation(activeConversationId)
      }
    } else if (activeConversationId === null && conversationId !== null) {
      // New chat — clear everything
      setMessages([])
      setConversationId(null)
      setPartialContent('')
      setInput('')
      setLoadError(null)
      loadControllerRef.current?.abort()
    }
    // Cancel any in-flight history load when navigating away / unmounting.
    return () => loadControllerRef.current?.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeConversationId])

  // Auto-scroll to bottom
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, partialContent, activeTools])

  // Close slash menu on outside click
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (slashMenuRef.current && !slashMenuRef.current.contains(e.target as Node)) {
        setSlashMenuDismissed(true)
      }
    }
    if (showSlashMenu) {
      document.addEventListener('mousedown', handleClickOutside)
      return () => document.removeEventListener('mousedown', handleClickOutside)
    }
  }, [showSlashMenu])

  const filteredCommands = slashCommands.filter((c) =>
    c.command.startsWith(slashFilter)
  )

  async function handleSlashCommand(command: string) {
    setStreaming(true)
    setInput('')
    setSlashMenuDismissed(true)
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
      } else if (result.type === 'compact' || result.type === 'compress') {
        if (conversationId) {
          const msgs = await getMessages(conversationId)
          setMessages(msgs)
        }
      } else if (result.type === 'undo') {
        if (conversationId) {
          const msgs = await getMessages(conversationId)
          setMessages(msgs)
        }
      } else if (result.type === 'retry') {
        // Remove last exchange locally and auto-resend
        if (conversationId) {
          const msgs = await getMessages(conversationId)
          setMessages(msgs)
        }
        const retryMsg = result.data?.retry_message as string | undefined
        if (retryMsg) {
          // Auto-resend the retried message
          setInput(retryMsg)
          // Use setTimeout to let state settle before sending
          setTimeout(() => {
            handleSendWithText(retryMsg)
          }, 100)
        }
      } else if (result.type === 'history') {
        const historyText = result.data?.history_text as string | undefined
        if (historyText) {
          // Show history as a system message in the chat
          setMessages((prev) => [
            ...prev,
            {
              id: uuid(),
              role: 'assistant' as const,
              content: `📋 **Conversation History**\n\n${historyText}`,
              created_at: new Date().toISOString(),
            },
          ])
        }
      } else if (result.type === 'title') {
        // Title updated — sidebar will refresh via onConversationLoaded
        onConversationLoaded()
      } else if (result.type === 'stop') {
        // Client handles abort separately
      } else if (result.type === 'save') {
        // Already showed toast with path
      } else if (result.type === 'forward') {
        // Agent handles this command natively — send as a chat message
        const forwardText = (result.data?.forward_text as string) ?? command
        await handleSendWithText(forwardText, true)
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
    if (!text) return
    if (streaming) {
      // Queue the message for after current stream ends
      setQueuedMessage(text)
      setInput('')
      setSlashMenuDismissed(true)
      showToast('Message queued', 'info')
      return
    }
    await handleSendWithText(text)
  }

  function handleStop() {
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
    }
    setStreaming(false)
    setPartialContent('')
    showToast('Stopped', 'info')
  }

  async function handleSendWithText(text: string, skipSlash = false) {
    if (!text) return

    if (!skipSlash && text.startsWith('/')) {
      // /stop is handled client-side — abort current stream
      if (text.trim() === '/stop') {
        if (abortRef.current) {
          abortRef.current.abort()
          abortRef.current = null
        }
        setStreaming(false)
        setPartialContent('')
        showToast('Stopped streaming', 'info')
        return
      }
      await handleSlashCommand(text)
      return
    }

    const userMsg: Message = {
      id: uuid(),
      role: 'user',
      content: text,
      created_at: new Date().toISOString(),
    }
    setMessages((prev) => [...prev, userMsg])
    setInput('')
    setStreaming(true)
    setPartialContent('')
    setContextSources([])
    setActiveTools([])

    try {
      const stream = sendChat(agent.id, text, selectedModel || undefined, conversationId ?? undefined)
      const reader = stream.getReader()

      let accumulated = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        // Handle tool lifecycle events
        if (value.tool_name) {
          if (value.tool_status === 'started') {
            setActiveTools((prev) => prev.includes(value.tool_name!) ? prev : [...prev, value.tool_name!])
          } else if (value.tool_status === 'completed') {
            setActiveTools((prev) => prev.filter((t) => t !== value.tool_name))
          }
          continue
        }

        if (value.content !== undefined) {
          accumulated += value.content
          setPartialContent(accumulated)
        }

        if (value.done) {
          const assistantMsg: Message = {
            id: uuid(),
            role: 'assistant',
            content: accumulated,
            created_at: new Date().toISOString(),
          }
          setMessages((prev) => [...prev, assistantMsg])
          setPartialContent('')
          setActiveTools([])
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
          id: uuid(),
          role: 'assistant',
          content: `⚠️ **Failed to get response.**\n\n\`${errMsg}\`\n\nTry sending your message again.`,
          created_at: new Date().toISOString(),
        },
      ])
      setPartialContent('')
      setActiveTools([])
    } finally {
      setStreaming(false)
      // Auto-send queued message after stream ends
      setQueuedMessage((prev) => {
        if (prev) {
          setTimeout(() => handleSendWithText(prev), 100)
        }
        return null
      })
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
      if (streaming && input.trim()) {
        // Queue the message
        setQueuedMessage(input.trim())
        setInput('')
        setSlashMenuDismissed(true)
        showToast('Message queued', 'info')
        return
      }
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
          {models.length > 0 && agent.harness === 'litellm' && (
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

      {/* Load-error banner with retry (#122) */}
      {loadError && !loadingHistory && (
        <div className="px-5 py-3 bg-[rgba(239,68,68,0.08)] text-[var(--accent-red)] text-xs flex flex-col gap-2 border-b border-[var(--border-subtle)]">
          <div className="flex items-center gap-2">
            <svg className="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
            </svg>
            <span>{loadError}</span>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => { if (conversationId) loadConversation(conversationId) }}
              className="pill-btn pill-btn--primary text-xs py-1.5 px-3"
            >
              Retry
            </button>
            <button
              type="button"
              onClick={onNewChat}
              className="pill-btn pill-btn--ghost text-xs py-1.5 px-3"
            >
              Start new chat
            </button>
          </div>
        </div>
      )}

      {/* Messages area */}
      <div className="flex-1 overflow-y-auto px-5 py-6">
        {messages.length === 0 && !partialContent && !streaming && !loadError && (
          <div className="flex flex-col items-center justify-center h-full fade-in">
            <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-[var(--accent-blue)] via-[var(--accent-purple)] to-[var(--accent-cyan)] flex items-center justify-center mb-4 shadow-[var(--shadow-glow)]">
              <svg className="w-8 h-8 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
              </svg>
            </div>
            <h3 className="text-lg font-medium text-[var(--text-primary)] mb-1">
              New conversation
            </h3>
            <p className="text-sm text-[var(--text-muted)] max-w-sm text-center">
              Message {agent.display_name || agent.name} below. Type / for available commands.
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
          {/* Active tool status pills */}
          {streaming && activeTools.length > 0 && (
            <div className="flex flex-wrap gap-1.5 mt-1 mb-2 slide-up">
              {activeTools.map((name) => (
                <span
                  key={name}
                  className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[11px] font-medium
                    bg-[var(--bg-elevated)] text-[var(--text-muted)] border border-[var(--border-subtle)]
                    animate-pulse opacity-80"
                >
                  {toolLabel(name)}
                </span>
              ))}
            </div>
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

        {/* Queued message indicator */}
        {queuedMessage && streaming && (
          <div className="flex items-center gap-2 mb-1.5 px-2">
            <span className="text-[10px] text-[var(--accent-blue)] opacity-70">
              ⏳ Queued: {queuedMessage.length > 40 ? queuedMessage.slice(0, 40) + '…' : queuedMessage}
            </span>
            <button
              onClick={() => { setQueuedMessage(null); showToast('Queue cleared', 'info') }}
              className="text-[10px] text-[var(--text-muted)] opacity-50 hover:opacity-100"
            >
              ✕
            </button>
          </div>
        )}

        <div className="floating-input flex items-end gap-2 p-2 max-w-3xl mx-auto">
          <VoiceButton onTranscribed={(text) => {
            setInput((prev) => prev ? prev + ' ' + text : text)
            // Allow the slash menu to re-evaluate for programmatic (voice) input,
            // matching the textarea onChange and the old input-derived behavior.
            setSlashMenuDismissed(false)
          }} />
          <textarea
            ref={inputRef}
            value={input}
            onChange={(e) => {
              setInput(e.target.value)
              setSlashMenuDismissed(false)
            }}
            onKeyDown={handleKeyDown}
            placeholder={streaming ? 'Type to queue…' : 'Message...'}
            aria-label="Chat message"
            rows={1}
            className="flex-1 bg-transparent text-[var(--text-primary)] text-sm px-4 py-2.5 resize-none border-none focus:outline-none placeholder:text-[var(--text-muted)]"
          />
          {streaming ? (
            <button
              onClick={handleStop}
              aria-label="Stop generation"
              title="Stop (or type /stop)"
              className="flex-shrink-0 w-10 h-10 rounded-xl flex items-center justify-center transition-all duration-200 bg-[var(--bg-elevated)] text-[var(--text-secondary)] hover:text-[var(--accent-red)] hover:bg-[rgba(239,68,68,0.1)]"
            >
              <svg className="w-4 h-4" fill="currentColor" viewBox="0 0 24 24">
                <rect x="6" y="6" width="12" height="12" rx="1" />
              </svg>
            </button>
          ) : (
            <button
              onClick={handleSend}
              disabled={!input.trim()}
              aria-label="Send message"
              className={`flex-shrink-0 w-10 h-10 rounded-xl flex items-center justify-center transition-all duration-200 ${
                input.trim()
                  ? 'bg-gradient-to-br from-[var(--accent-blue)] to-[var(--accent-purple)] text-white shadow-[var(--shadow-glow)] hover:shadow-[0_0_28px_rgba(91,141,239,0.4)] hover:scale-105 active:scale-95'
                  : 'bg-[var(--bg-elevated)] text-[var(--text-muted)] cursor-not-allowed'
              }`}
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 12h14M12 5l7 7-7 7" />
              </svg>
            </button>
          )}
        </div>

        {/* Subtle context line */}
        {contextSources.length > 0 && (
          <div className="flex items-center justify-center mt-1.5 gap-1">
            <span className="text-[10px] text-[var(--accent-blue)] opacity-60">
              <Icon name="auto_stories" size={10} /> {contextSources.length} knowledge source{contextSources.length !== 1 ? 's' : ''} used:
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
              <Icon name="close" size={10} />
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
