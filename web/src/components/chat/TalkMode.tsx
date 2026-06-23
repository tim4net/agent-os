import { useCallback, useEffect, useRef, useState } from 'react'
import type { Agent } from '../../api/client'
import { sendChat, synthesizeSpeech, transcribeAudio } from '../../api/client'
import { chatToReply, processTurn } from './talk-engine'
import { showToast } from '../toast-bus'

/**
 * Talk Mode — a hands-free, bidirectional real-time voice conversation
 * surface for the web UI (#124).
 *
 * Pipeline per turn:  mic capture -> STT (/api/voice/transcribe) -> LLM
 * (sendChat stream) -> TTS (/api/voice/synthesize) -> audio playback, then
 * re-arm. Existing voice endpoints are reused (not duplicated) and empty
 * transcriptions (silence) are skipped gracefully without an LLM round-trip.
 *
 * The orchestration lives in `talk-engine.ts` (processTurn / chatToReply) so it
 * is unit-testable without a browser audio stack.
 */
export type VoicePhase = 'idle' | 'listening' | 'transcribing' | 'thinking' | 'speaking'

interface Turn {
  id: string
  role: 'user' | 'assistant'
  text: string
}

interface TalkModeProps {
  agent: Agent
  conversationId: string | null
  onConversationCreated: (convId: string) => void
  model?: string
}

const PHASE_LABEL: Record<VoicePhase, string> = {
  idle: 'Tap the mic to start talking',
  listening: 'Listening…',
  transcribing: 'Transcribing…',
  thinking: 'Thinking…',
  speaking: 'Speaking…',
}

function uuid(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return Math.random().toString(36).slice(2)
}

export function TalkMode({ agent, conversationId, onConversationCreated, model }: TalkModeProps) {
  const [phase, setPhase] = useState<VoicePhase>('idle')
  const [turns, setTurns] = useState<Turn[]>([])
  const [autoContinue, setAutoContinue] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const mediaRecorderRef = useRef<MediaRecorder | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const chunksRef = useRef<Blob[]>([])
  const audioRef = useRef<HTMLAudioElement | null>(null)
  // Whether a talk session is live (mic may be armed or a turn may be in flight).
  const activeRef = useRef(false)
  const conversationIdRef = useRef<string | null>(conversationId)
  const turnsEndRef = useRef<HTMLDivElement | null>(null)
  // Breaks the startListening <-> handleRecorderStop cycle: the recorder's
  // onstop always invokes the latest pipeline via this stable ref.
  const onStopRef = useRef<() => void>(() => {})

  // Latest props/state snapshot, read inside stable async callbacks so they
  // never close over stale values (and stay dependency-free). Updated in an
  // effect (never during render) per react-hooks/refs.
  const live = useRef({ agentId: agent.id, model, autoContinue, onConversationCreated })
  useEffect(() => {
    live.current = { agentId: agent.id, model, autoContinue, onConversationCreated }
  })

  useEffect(() => {
    conversationIdRef.current = conversationId
  }, [conversationId])

  // Auto-scroll the transcript to the latest turn.
  useEffect(() => {
    turnsEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [turns])

  const releaseMic = useCallback(() => {
    streamRef.current?.getTracks().forEach((t) => t.stop())
    streamRef.current = null
  }, [])

  const playReply = useCallback(async (text: string): Promise<void> => {
    setPhase('speaking')
    let url: string | null = null
    try {
      const blob = await synthesizeSpeech(text)
      url = URL.createObjectURL(blob)
      const audio = new Audio(url)
      audioRef.current = audio
      await audio.play()
      // Block until playback finishes so the mic does not capture the TTS output.
      await new Promise<void>((resolve) => {
        audio.onended = () => resolve()
        audio.onerror = () => resolve()
      })
    } catch {
      // TTS is best-effort: the text reply is already on screen.
      showToast('Voice playback unavailable — reply shown as text', 'info')
    } finally {
      if (url) URL.revokeObjectURL(url)
      audioRef.current = null
    }
  }, [])

  // Re-arm the microphone for the next utterance.
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

  // Fires when MediaRecorder stops (user released the mic, or session ended).
  const handleRecorderStop = useCallback(async () => {
    releaseMic()
    const blob = new Blob(chunksRef.current, { type: 'audio/webm' })
    chunksRef.current = []
    // If the session was cancelled, drop the captured audio entirely.
    if (!activeRef.current) {
      setPhase('idle')
      return
    }

    setPhase('transcribing')
    const result = await processTurn(
      blob,
      live.current.agentId,
      conversationIdRef.current ?? undefined,
      {
        transcribe: transcribeAudio,
        chat: (id, msg, conv) => {
          setPhase('thinking')
          return chatToReply(sendChat(id, msg, live.current.model, conv))
        },
      },
      {
        onUserText: (text) => setTurns((prev) => [...prev, { id: uuid(), role: 'user', text }]),
        onAssistantText: (text, convId) => {
          setTurns((prev) => [...prev, { id: uuid(), role: 'assistant', text }])
          if (convId && convId !== conversationIdRef.current) {
            conversationIdRef.current = convId
            live.current.onConversationCreated(convId)
          }
        },
      },
    )

    if (result === null) {
      // Silence / no input — graceful: nothing was sent to the LLM.
      showToast("Didn't catch that — try again", 'info')
    } else if (result.replyText.trim()) {
      await playReply(result.replyText)
    }

    // Re-arm for the next turn if still live.
    if (activeRef.current && live.current.autoContinue) {
      void startListening()
    } else {
      setPhase('idle')
    }
  }, [releaseMic, playReply, startListening])

  // Keep the recorder's onstop pointed at the latest pipeline (effect, not render).
  useEffect(() => {
    onStopRef.current = handleRecorderStop
  }, [handleRecorderStop])

  // Begin a talk session.
  const handleStart = useCallback(() => {
    activeRef.current = true
    void startListening()
  }, [startListening])

  // Release the current utterance (push-to-talk) and process it.
  const handleStop = useCallback(() => {
    const recorder = mediaRecorderRef.current
    if (recorder && recorder.state !== 'inactive') {
      recorder.stop()
    } else {
      // Nothing captured yet — just return to idle.
      activeRef.current = false
      setPhase('idle')
    }
  }, [])

  // Hard cancel: abort an in-flight turn and end the session.
  const handleCancel = useCallback(() => {
    activeRef.current = false
    const recorder = mediaRecorderRef.current
    if (recorder && recorder.state !== 'inactive') recorder.stop()
    releaseMic()
    audioRef.current?.pause()
    audioRef.current = null
    setPhase('idle')
  }, [releaseMic])

  // Release the mic + audio on unmount / view switch.
  useEffect(() => {
    return () => {
      activeRef.current = false
      streamRef.current?.getTracks().forEach((t) => t.stop())
      const recorder = mediaRecorderRef.current
      if (recorder && recorder.state !== 'inactive') recorder.stop()
      audioRef.current?.pause()
    }
  }, [])

  const busy = phase === 'transcribing' || phase === 'thinking' || phase === 'speaking'

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-5 py-3 border-b border-[var(--border-subtle)] bg-[var(--bg-surface)]">
        <div className="flex items-center gap-2">
          <span className="agent-dot agent-dot--online" />
          <h2 className="text-sm font-medium text-[var(--text-primary)]">
            Talk Mode · {agent.display_name || agent.name}
          </h2>
        </div>
        <label className="flex items-center gap-2 text-xs text-[var(--text-muted)] cursor-pointer select-none">
          <input
            type="checkbox"
            checked={autoContinue}
            onChange={(e) => setAutoContinue(e.target.checked)}
            className="accent-[var(--accent-blue)]"
            aria-label="Auto-continue conversation"
          />
          Auto-continue
        </label>
      </div>

      {/* Transcript */}
      <div className="flex-1 overflow-y-auto px-5 py-6">
        <div className="max-w-2xl mx-auto space-y-3">
          {turns.length === 0 && (
            <div className="flex flex-col items-center justify-center h-full fade-in text-center">
              <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-[var(--accent-blue)] via-[var(--accent-purple)] to-[var(--accent-cyan)] flex items-center justify-center mb-4 shadow-[var(--shadow-glow)]">
                <svg className="w-8 h-8 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3Z" />
                  <path strokeLinecap="round" strokeLinejoin="round" d="M19 10v2a7 7 0 0 1-14 0v-2" />
                  <line x1="12" y1="19" x2="12" y2="23" />
                  <line x1="8" y1="23" x2="16" y2="23" />
                </svg>
              </div>
              <h3 className="text-lg font-medium text-[var(--text-primary)] mb-1">Talk Mode</h3>
              <p className="text-sm text-[var(--text-muted)] max-w-sm">
                Have a hands-free spoken conversation. Tap the mic, speak, and {agent.display_name || agent.name} replies out loud.
              </p>
            </div>
          )}

          {turns.map((t) => (
            <div
              key={t.id}
              className={`flex ${t.role === 'user' ? 'justify-end' : 'justify-start'}`}
            >
              <div
                className={`px-4 py-2.5 rounded-2xl text-sm max-w-[85%] ${
                  t.role === 'user'
                    ? 'bg-gradient-to-br from-[var(--accent-blue)] to-[var(--accent-purple)] text-white rounded-br-md'
                    : 'glass-card text-[var(--text-primary)] rounded-bl-md'
                }`}
              >
                {t.text}
              </div>
            </div>
          ))}
          <div ref={turnsEndRef} />
        </div>
      </div>

      {/* Error banner */}
      {error && (
        <div className="px-5 py-2 bg-[rgba(239,68,68,0.08)] text-[var(--accent-red)] text-xs border-t border-[var(--border-subtle)]">
          {error}
        </div>
      )}

      {/* Status + controls */}
      <div className="px-5 py-4 border-t border-[var(--border-subtle)] flex flex-col items-center gap-3">
        <span className="text-xs text-[var(--text-muted)]" data-testid="talk-phase">
          {PHASE_LABEL[phase]}
        </span>
        <div className="flex items-center gap-3">
          {phase === 'idle' ? (
            <button
              onClick={handleStart}
              className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-gradient-to-br from-[var(--accent-blue)] to-[var(--accent-purple)] text-white text-sm font-medium shadow-[var(--shadow-glow)] hover:scale-105 active:scale-95 transition-transform"
              aria-label="Start talking"
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3Z" />
                <path strokeLinecap="round" strokeLinejoin="round" d="M19 10v2a7 7 0 0 1-14 0v-2" />
                <line x1="12" y1="19" x2="12" y2="23" />
                <line x1="8" y1="23" x2="16" y2="23" />
              </svg>
              Start talking
            </button>
          ) : phase === 'listening' ? (
            <button
              onClick={handleStop}
              className="relative flex items-center gap-2 px-5 py-2.5 rounded-xl bg-red-600 hover:bg-red-700 text-white text-sm font-medium transition-colors"
              aria-label="Stop recording"
            >
              <span className="absolute inset-0 rounded-xl bg-red-500 animate-ping opacity-30" />
              <svg className="relative w-4 h-4" fill="currentColor" viewBox="0 0 24 24">
                <rect x="6" y="6" width="12" height="12" rx="1" />
              </svg>
              Stop
            </button>
          ) : (
            <button
              onClick={handleCancel}
              disabled={!busy}
              className="px-5 py-2.5 rounded-xl bg-[var(--bg-elevated)] text-[var(--text-secondary)] text-sm font-medium hover:text-[var(--accent-red)] disabled:opacity-40 transition-colors"
              aria-label="Cancel turn"
            >
              Cancel
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
