import { useState, useCallback, useRef } from 'react'
import type { Message } from '../../api/client'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Components } from 'react-markdown'

interface MessageBubbleProps {
  message: Message
}

const mdComponents: Components = {
  p: ({ children }) => <p className="mb-2 last:mb-0 leading-relaxed">{children}</p>,
  ul: ({ children }) => <ul className="list-disc list-outside ml-5 mb-2 space-y-0.5">{children}</ul>,
  ol: ({ children }) => <ol className="list-decimal list-outside ml-5 mb-2 space-y-0.5">{children}</ol>,
  li: ({ children }) => <li className="leading-relaxed">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="border-l-3 border-[var(--accent-purple)] pl-3 italic text-[var(--text-secondary)] my-2 bg-[var(--bg-hover)] rounded-r-lg py-1">
      {children}
    </blockquote>
  ),
  hr: () => <hr className="border-[var(--border-subtle)] my-3" />,
  a: ({ href, children }) => (
    <a
      href={href}
      className="text-[var(--accent-blue)] underline underline-offset-2 hover:text-[var(--accent-cyan)] transition-colors"
      target="_blank"
      rel="noopener noreferrer"
    >
      {children}
    </a>
  ),
  code: ({ className, children }) => {
    const isInline = !className
    if (isInline) {
      return (
        <code className="bg-[var(--accent-blue)]/10 text-[var(--accent-cyan)] px-1.5 py-0.5 rounded-md text-[0.85em] font-mono">
          {children}
        </code>
      )
    }
    return <code className={`${className ?? ''} block text-[0.85em]`}>{children}</code>
  },
  pre: ({ children }) => (
    <pre className="bg-[var(--bg-base)] border border-[var(--border-subtle)] rounded-xl p-3 my-2 overflow-x-auto text-[0.85em] leading-relaxed font-mono">
      {children}
    </pre>
  ),
  table: ({ children }) => (
    <div className="overflow-x-auto my-2">
      <table className="min-w-full border-collapse border border-[var(--border-subtle)] rounded-lg overflow-hidden">
        {children}
      </table>
    </div>
  ),
  th: ({ children }) => (
    <th className="border border-[var(--border-subtle)] px-3 py-1.5 text-left font-semibold bg-[var(--bg-hover)]">
      {children}
    </th>
  ),
  td: ({ children }) => (
    <td className="border border-[var(--border-subtle)] px-3 py-1.5">{children}</td>
  ),
  strong: ({ children }) => <strong className="font-semibold text-[var(--text-primary)]">{children}</strong>,
  em: ({ children }) => <em className="italic text-[var(--text-secondary)]">{children}</em>,
  del: ({ children }) => <del className="line-through opacity-50">{children}</del>,
  h1: ({ children }) => <h1 className="text-base font-bold mt-3 mb-1 first:mt-0 gradient-text">{children}</h1>,
  h2: ({ children }) => <h2 className="font-bold mt-2 mb-1 first:mt-0 text-[var(--text-primary)]">{children}</h2>,
  h3: ({ children }) => <h3 className="font-semibold mt-2 mb-1 first:mt-0 text-[var(--text-primary)]">{children}</h3>,
}

export function MessageBubble({ message }: MessageBubbleProps) {
  const isUser = message.role === 'user'
  const [speaking, setSpeaking] = useState(false)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const [showActions, setShowActions] = useState(false)

  const handleReadAloud = useCallback(async () => {
    if (speaking && audioRef.current) {
      audioRef.current.pause()
      audioRef.current = null
      setSpeaking(false)
      return
    }

    try {
      setSpeaking(true)
      const res = await fetch('/api/voice/synthesize', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: message.content, voice: 'alloy' }),
      })
      if (!res.ok) {
        console.error('TTS failed:', res.status)
        setSpeaking(false)
        return
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const audio = new Audio(url)
      audioRef.current = audio
      audio.onended = () => {
        setSpeaking(false)
        URL.revokeObjectURL(url)
        audioRef.current = null
      }
      audio.onerror = () => {
        setSpeaking(false)
        URL.revokeObjectURL(url)
        audioRef.current = null
      }
      audio.play()
    } catch (err) {
      console.error('Read aloud error:', err)
      setSpeaking(false)
    }
  }, [message.content, speaking])

  return (
    <div
      className={`flex ${isUser ? 'justify-end' : 'justify-start'} mb-4 fade-in`}
      onMouseEnter={() => setShowActions(true)}
      onMouseLeave={() => setShowActions(false)}
    >
      <div className={`flex items-end gap-2 max-w-[80%] ${isUser ? 'flex-row-reverse' : 'flex-row'}`}>
        {/* Avatar */}
        <div className={`flex-shrink-0 w-7 h-7 rounded-full flex items-center justify-center text-[10px] font-bold ${
          isUser
            ? 'bg-gradient-to-br from-[var(--accent-blue)] to-[var(--accent-purple)] text-white shadow-[0_0_12px_rgba(91,141,239,0.25)]'
            : 'glass-card text-[var(--accent-cyan)]'
        }`}>
          {isUser ? 'T' : 'AI'}
        </div>

        {/* Message content */}
        <div className="relative group">
          <div
            className={`px-4 py-3 text-sm leading-relaxed ${
              isUser
                ? 'bg-gradient-to-br from-[var(--accent-blue)] to-[var(--accent-purple)] text-white rounded-2xl rounded-br-md shadow-[0_0_20px_rgba(91,141,239,0.15)]'
                : 'glass-card rounded-2xl rounded-bl-md text-[var(--text-primary)]'
            }`}
          >
            {isUser ? (
              <p className="whitespace-pre-wrap leading-relaxed">{message.content}</p>
            ) : (
              <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
                {message.content}
              </ReactMarkdown>
            )}
          </div>

          {/* Actions row - shows on hover */}
          <div className={`flex items-center gap-2 mt-1 transition-opacity duration-200 ${
            showActions ? 'opacity-100' : 'opacity-0'
          } ${isUser ? 'justify-end' : 'justify-start'}`}>
            {message.created_at && (
              <span className="text-[10px] text-[var(--text-muted)]">
                {new Date(message.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
              </span>
            )}
            {!isUser && message.content && !message.content.startsWith('⚠️') && (
              <button
                onClick={handleReadAloud}
                className={`p-1 rounded-lg transition-all ${
                  speaking
                    ? 'text-[var(--accent-blue)] bg-[var(--accent-blue)]/10'
                    : 'text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]'
                }`}
                title={speaking ? 'Stop reading' : 'Read aloud'}
              >
                {speaking ? (
                  <svg className="w-3 h-3" fill="currentColor" viewBox="0 0 24 24">
                    <rect x="6" y="6" width="12" height="12" rx="2" />
                  </svg>
                ) : (
                  <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15.536 8.464a5 5 0 010 7.072M17.95 6.05a8 8 0 010 11.9M6.5 8H8a1 1 0 001-1V5a1 1 0 00-1-1H6a1 1 0 00-1 1v12a1 1 0 001 1h2a1 1 0 001-1v-2a1 1 0 00-1-1H6.5" />
                  </svg>
                )}
              </button>
            )}
            <button
              onClick={() => {
                if (navigator.clipboard && navigator.clipboard.writeText) {
                  navigator.clipboard.writeText(message.content)
                } else {
                  // Fallback for non-secure contexts (HTTP)
                  const ta = document.createElement('textarea')
                  ta.value = message.content
                  ta.style.position = 'fixed'
                  ta.style.opacity = '0'
                  document.body.appendChild(ta)
                  ta.select()
                  document.execCommand('copy')
                  document.body.removeChild(ta)
                }
              }}
              className="p-1 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-all"
              title="Copy"
            >
              <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
              </svg>
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
