import { useCallback, useEffect, useRef, useState } from 'react'
import type { Agent } from '../../api/client'
import { sendChat, transcribeAudio } from '../../api/client'
import { consumeJarvisStream, isDestructiveCommand, processCommand } from './jarvis-engine'
import { showToast } from '../toast-bus'

/**
 * Jarvis Mode — voice-activated computer control (#125).
 *
 * The user speaks a command (push-to-talk). The utterance is transcribed via
 * the existing STT endpoint, routed to the agent with `mode="jarvis"` (which
 * injects a computer-control system prompt), and the agent executes it using
 * its existing browser/terminal tools. The result is shown in a command
 * transcript.
 *
 * Destructive commands (delete, close, overwrite, etc.) are flagged client-side
 * via `isDestructiveCommand` and gated behind an explicit confirmation dialog
 * before they are sent (#125 acceptance criterion). The agent-side system prompt
 * also instructs the agent to confirm destructive actions, so safety is
 * defence-in-depth.
 *
 * The orchestration lives in `jarvis-engine.ts` (processCommand /
 * consumeJarvisStream / isDestructiveCommand) so it is unit-testable without a
 * browser audio stack.
 */
export type JarvisPhase = 'idle' | 'listening' | 'transcribing' | 'executing' | 'result'

interface Entry {
  id: string
  role: 'user' | 'assistant'
  text: string
  /** Tools that fired during this command's execution. */
  tools?: string[]
}

/** A destructive command awaiting user approval before execution. */
interface PendingCommand {
  transcript: string
}

interface JarvisModeProps {
  agent: Agent
  conversationId: string | null
  onConversationCreated: (convId: string) => void
  model?: string
}

const PHASE_LABEL: Record<JarvisPhase, string> = {
  idle: 'Tap the mic and speak a command',
  listening: 'Listening…',
  transcribing: 'Transcribing…',
  executing: 'Executing command…',
  result: 'Done',
}

function toolLabel(name: string): string {
  const map: Record<string, string> = {
    browser: '🌐 Browser',
    terminal: '💻 Terminal',
    shell: '💻 Terminal',
    search_files: '🔍 File search',
    read_file: '📄 Read file',
    write_file: '📝 Write file',
    patch: '📝 Edit file',
    web_search: '🌐 Web search',
    fetch: '🌐 Fetch URL',
    vision_analyze: '👁️ Vision',
  }
  return map[name] ?? `🔧 ${name}`
}

function uuid(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return Math.random().toString(36).slice(2)
}

export function JarvisMode({ agent, conversationId, onConversationCreated, model }: JarvisModeProps) {
  const [phase, setPhase] = useState<JarvisPhase>('idle')
  const [entries, setEntries] = useState<Entry[]>([])
  const [activeTools, setActiveTools] = useState<string[]>([])
  const [pendingCommand, setPendingCommand] = useState<PendingCommand | null>(null)
  const [error, setError] = useState<string | null>(null)

  const mediaRecorderRef = useRef<MediaRecorder | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const chunksRef = useRef<Blob[]>([])
  const activeRef = useRef(false)
  const conversationIdRef = useRef<string | null>(conversationId)

  const onStopRef = useRef<() => void>(() => {})
  const entriesEndRef = useRef<HTMLDivElement | null>(null)

  const live = useRef({ agentId: agent.id, model, onConversationCreated })
  useEffect(() => {
    live.current = { agentId: agent.id, model, onConversationCreated }
  })

  useEffect(() => {
    conversationIdRef.current = conversationId
  }, [conversationId])

  useEffect(() => {
    entriesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [entries])

  const releaseMic = useCallback(() => {
    streamRef.current?.getTracks().forEach((t) => t.stop())
    streamRef.current = null
  }, [])

  // Send a command to the agent with mode="jarvis" and consume the response.
  const executeCommand = useCallback(
    async (transcript: string): Promise<void> => {
      setPhase('executing')
      setActiveTools([])
      const result = await processCommand(
        new Blob([transcript]),
        live.current.agentId,
        conversationIdRef.current ?? undefined,
        {
          transcribe: async () => transcript, // Already transcribed; pass through.
          execute: (agentId, command, conv) =>
            consumeJarvisStream(sendChat(agentId, command, live.current.model, conv, 'jarvis')),
        },
        {
          onUserText: () => {},
          onAssistantText: (text, convId) => {
            if (convId && convId !== conversationIdRef.current) {
              conversationIdRef.current = convId
              live.current.onConversationCreated(convId)
            }
          },
          onToolActivity: (toolName) => {
            setActiveTools((prev) => (prev.includes(toolName) ? prev : [...prev, toolName]))
          },
        },
      )

      if (result) {
        setEntries((prev) => [
          ...prev,
          { id: uuid(), role: 'user', text: transcript, tools: [] },
          { id: uuid(), role: 'assistant', text: result.replyText, tools: result.tools },
        ])
      }
      setPhase('idle')
    },
    [],
  )

  // Re-arm the microphone for the next command.
  const startListening = useCallback(async (): Promise<void> => {
    setError(null)
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      streamRef.current = stream
      const mimeType = MediaRecorder.isTypeSupported('audio/webm;codecs=opus')
        ? 'audio/webm;codecs=opus'
        : 'audio/webm'
      const recorder = new MediaRecorder(stream, { mimeType })
      chunksRef.current = []
      recorder.ondataavailable = (e) => {
        if (e.data.size > 0) chunksRef.current.push(e.data)
      }
      recorder.onstop = () => onStopRef.current()
      mediaRecorderRef.current = recorder
      recorder.start()
      setPhase('listening')
    } catch {
      activeRef.current = false
      setError('Microphone access denied. Enable mic permissions and try again.')
      showToast('Microphone access denied', 'error')
      setPhase('idle')
    }
  }, [])

  // Fires when MediaRecorder stops.
  const handleRecorderStop = useCallback(async () => {
    releaseMic()
    const blob = new Blob(chunksRef.current, { type: 'audio/webm' })
    chunksRef.current = []
    if (!activeRef.current) {
      setPhase('idle')
      return
    }

    setPhase('transcribing')
    const transcript = (await transcribeAudio(blob)).trim()
    if (!transcript) {
      // Silence — graceful no-input path.
      showToast("Didn't catch that — try again", 'info')
      setPhase('idle')
      return
    }

    // Destructive command gate: show confirmation before sending (#125).
    if (isDestructiveCommand(transcript)) {
      setPendingCommand({ transcript })
      setPhase('idle')
      return
    }

    await executeCommand(transcript)
  }, [releaseMic, executeCommand])

  useEffect(() => {
    onStopRef.current = handleRecorderStop
  }, [handleRecorderStop])

  // Begin a command capture.
  const handleMicOn = useCallback(() => {
    activeRef.current = true
    void startListening()
  }, [startListening])

  // Release the current utterance (push-to-talk release).
  const handleMicOff = useCallback(() => {
    const recorder = mediaRecorderRef.current
    if (recorder && recorder.state !== 'inactive') {
      recorder.stop()
    } else {
      activeRef.current = false
      setPhase('idle')
    }
  }, [])

  // Cancel an in-flight turn / session.
  const handleCancel = useCallback(() => {
    activeRef.current = false
    const recorder = mediaRecorderRef.current
    if (recorder && recorder.state !== 'inactive') recorder.stop()
    releaseMic()
    setPhase('idle')
  }, [releaseMic])

  // Approve a pending destructive command.
  const handleApprove = useCallback(async () => {
    const cmd = pendingCommand
    setPendingCommand(null)
    if (cmd) {
      activeRef.current = true
      await executeCommand(cmd.transcript)
    }
  }, [pendingCommand, executeCommand])

  // Reject a pending destructive command.
  const handleReject = useCallback(() => {
    const cmd = pendingCommand
    setPendingCommand(null)
    if (cmd) {
      setEntries((prev) => [
        ...prev,
        { id: uuid(), role: 'user', text: cmd.transcript, tools: [] },
        { id: uuid(), role: 'assistant', text: '⛔ Command cancelled — no action taken.', tools: [] },
      ])
    }
    activeRef.current = false
    setPhase('idle')
  }, [pendingCommand])

  // Release the mic on unmount / view switch.
  useEffect(() => {
    return () => {
      activeRef.current = false
      streamRef.current?.getTracks().forEach((t) => t.stop())
      const recorder = mediaRecorderRef.current
      if (recorder && recorder.state !== 'inactive') recorder.stop()
    }
  }, [])

  const busy = phase === 'transcribing' || phase === 'executing'
  const listening = phase === 'listening'

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-5 py-3 border-b border-[var(--border-subtle)] bg-[var(--bg-surface)]">
        <div className="flex items-center gap-2">
          <span className="agent-dot agent-dot--online" />
          <h2 className="text-sm font-medium text-[var(--text-primary)]">
            Jarvis · {agent.display_name || agent.name}
          </h2>
        </div>
      </div>

      {/* Command transcript */}
      <div className="flex-1 overflow-y-auto px-5 py-6">
        <div className="max-w-2xl mx-auto space-y-3">
          {entries.length === 0 && (
            <div className="flex flex-col items-center justify-center h-full fade-in text-center">
              <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-[var(--accent-purple)] via-[var(--accent-blue)] to-[var(--accent-cyan)] flex items-center justify-center mb-4 shadow-[var(--shadow-glow)]">
                <svg className="w-8 h-8 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3Z" />
                  <path strokeLinecap="round" strokeLinejoin="round" d="M19 10v2a7 7 0 0 1-14 0v-2" />
                  <line x1="12" y1="19" x2="12" y2="23" />
                  <line x1="8" y1="23" x2="16" y2="23" />
                </svg>
              </div>
              <h3 className="text-lg font-medium text-[var(--text-primary)] mb-1">Jarvis Mode</h3>
              <p className="text-sm text-[var(--text-muted)] max-w-sm">
                Speak a command and {agent.display_name || agent.name} will execute it on your computer —
                open apps, navigate the web, run tasks. Destructive actions ask for confirmation.
              </p>
            </div>
          )}

          {entries.map((e) => (
            <div key={e.id} className="space-y-1">
              <div className={`flex ${e.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                <div
                  className={`px-4 py-2.5 rounded-2xl text-sm max-w-[85%] ${
                    e.role === 'user'
                      ? 'bg-gradient-to-br from-[var(--accent-purple)] to-[var(--accent-blue)] text-white rounded-br-md'
                      : 'glass-card text-[var(--text-primary)] rounded-bl-md'
                  }`}
                >
                  {e.text}
                </div>
              </div>
              {e.role === 'assistant' && e.tools && e.tools.length > 0 && (
                <div className="flex flex-wrap gap-1.5 pl-1">
                  {e.tools.map((t) => (
                    <span
                      key={t}
                      className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md bg-[var(--bg-elevated)] text-[10px] text-[var(--text-muted)] border border-[var(--border-subtle)]"
                    >
                      {toolLabel(t)}
                    </span>
                  ))}
                </div>
              )}
            </div>
          ))}

          {/* Live tool activity during execution */}
          {busy && activeTools.length > 0 && (
            <div className="flex flex-wrap gap-1.5 justify-center">
              {activeTools.map((t) => (
                <span
                  key={t}
                  className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md bg-[var(--bg-elevated)] text-[11px] text-[var(--text-secondary)] border border-[var(--accent-blue)]/30 animate-pulse"
                >
                  {toolLabel(t)}…
                </span>
              ))}
            </div>
          )}

          <div ref={entriesEndRef} />
        </div>
      </div>

      {/* Error banner */}
      {error && (
        <div className="px-5 py-2 bg-[rgba(239,68,68,0.08)] text-[var(--accent-red)] text-xs border-t border-[var(--border-subtle)]">
          {error}
        </div>
      )}

      {/* Destructive command confirmation gate (#125) */}
      {pendingCommand && (
        <div className="px-5 py-4 border-t border-[var(--border-subtle)] bg-[rgba(245,158,11,0.06)]">
          <div className="flex items-start gap-3">
            <svg className="w-5 h-5 text-amber-500 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v4m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
            </svg>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-[var(--text-primary)] mb-1">Destructive command detected</p>
              <p className="text-xs text-[var(--text-muted)] mb-2">"{pendingCommand.transcript}"</p>
              <p className="text-xs text-[var(--text-secondary)] mb-3">
                This command may modify or delete data. Confirm to execute it.
              </p>
              <div className="flex gap-2">
                <button
                  onClick={handleApprove}
                  className="px-4 py-1.5 rounded-lg bg-red-600 hover:bg-red-700 text-white text-xs font-medium transition-colors"
                  aria-label="Approve and execute command"
                >
                  Approve & Execute
                </button>
                <button
                  onClick={handleReject}
                  className="px-4 py-1.5 rounded-lg bg-[var(--bg-elevated)] hover:bg-[var(--bg-surface)] text-[var(--text-secondary)] text-xs font-medium transition-colors border border-[var(--border-subtle)]"
                  aria-label="Cancel command"
                >
                  Cancel
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Status + controls */}
      {pendingCommand === null && (
        <div className="px-5 py-4 border-t border-[var(--border-subtle)] flex flex-col items-center gap-3">
          <span className="text-xs text-[var(--text-muted)]" data-testid="jarvis-phase">
            {PHASE_LABEL[phase]}
          </span>
          <div className="flex items-center gap-3">
            {listening ? (
              <button
                onClick={handleMicOff}
                className="relative flex items-center gap-2 px-5 py-2.5 rounded-xl bg-red-600 hover:bg-red-700 text-white text-sm font-medium transition-colors"
                aria-label="Stop recording"
              >
                <span className="absolute inset-0 rounded-xl bg-red-500 animate-ping opacity-30" />
                <svg className="relative w-4 h-4" fill="currentColor" viewBox="0 0 24 24">
                  <rect x="6" y="6" width="12" height="12" rx="1" />
                </svg>
                Stop
              </button>
            ) : busy ? (
              <button
                onClick={handleCancel}
                className="px-5 py-2.5 rounded-xl bg-[var(--bg-elevated)] text-[var(--text-secondary)] text-sm font-medium hover:text-[var(--accent-red)] transition-colors"
                aria-label="Cancel execution"
              >
                Cancel
              </button>
            ) : (
              <button
                onClick={handleMicOn}
                className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-gradient-to-br from-[var(--accent-purple)] to-[var(--accent-blue)] text-white text-sm font-medium shadow-[var(--shadow-glow)] hover:scale-105 active:scale-95 transition-transform"
                aria-label="Speak a command"
              >
                <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3Z" />
                  <path strokeLinecap="round" strokeLinejoin="round" d="M19 10v2a7 7 0 0 1-14 0v-2" />
                  <line x1="12" y1="19" x2="12" y2="23" />
                  <line x1="8" y1="23" x2="16" y2="23" />
                </svg>
                Speak a command
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
