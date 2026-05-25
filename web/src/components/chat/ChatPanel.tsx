import { useEffect, useRef, useState } from 'react'
import type { Agent, Message, Model } from '../../api/client'
import { getAgentModels, sendChat } from '../../api/client'
import { MessageBubble } from './MessageBubble'

interface ChatPanelProps {
  agent: Agent
}

export function ChatPanel({ agent }: ChatPanelProps) {
  const [models, setModels] = useState<Model[]>([])
  const [selectedModel, setSelectedModel] = useState('')
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [partialContent, setPartialContent] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    getAgentModels(agent.id)
      .then((m) => {
        setModels(m)
        if (m.length > 0 && !selectedModel) setSelectedModel(m[0].id)
      })
      .catch(() => setModels([]))
    // reset chat when agent changes
    setMessages([])
    setInput('')
    setStreaming(false)
    setPartialContent('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agent.id])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, partialContent])

  async function handleSend() {
    const text = input.trim()
    if (!text || streaming) return

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

    try {
      const stream = sendChat(agent.id, text, selectedModel || undefined)
      const reader = stream.getReader()

      let accumulated = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        accumulated += value.content
        setPartialContent(accumulated)

        if (value.done) {
          setMessages((prev) => [
            ...prev,
            {
              id: crypto.randomUUID(),
              role: 'assistant',
              content: accumulated,
              created_at: new Date().toISOString(),
            },
          ])
          setPartialContent('')
        }
      }
    } catch (err) {
      console.error('Chat error:', err)
      setMessages((prev) => [
        ...prev,
        {
          id: crypto.randomUUID(),
          role: 'assistant',
          content: 'Error: Failed to get response.',
          created_at: new Date().toISOString(),
        },
      ])
      setPartialContent('')
    } finally {
      setStreaming(false)
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
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-gray-800 bg-gray-900">
        <h2 className="text-sm font-semibold text-white">
          Chat with {agent.display_name || agent.name}
        </h2>
        {models.length > 0 && (
          <select
            value={selectedModel}
            onChange={(e) => setSelectedModel(e.target.value)}
            className="bg-gray-800 text-gray-200 text-sm border border-gray-700 rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id}
              </option>
            ))}
          </select>
        )}
      </div>

      {/* Messages */}
      <div className="flex-1 overflow-y-auto p-4 space-y-1">
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
          <div className="flex items-start mb-4">
            <div className="bg-gray-800 text-gray-400 text-sm px-4 py-2 rounded-lg">
              <span className="inline-flex gap-1">
                <span className="animate-bounce">●</span>
                <span className="animate-bounce [animation-delay:0.1s]">●</span>
                <span className="animate-bounce [animation-delay:0.2s]">●</span>
              </span>
            </div>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div className="border-t border-gray-800 bg-gray-900 p-4">
        <div className="flex gap-2">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Type a message..."
            rows={1}
            className="flex-1 bg-gray-800 text-gray-100 text-sm rounded-lg px-4 py-2 resize-none focus:outline-none focus:ring-1 focus:ring-blue-500 placeholder-gray-500"
          />
          <button
            onClick={handleSend}
            disabled={streaming || !input.trim()}
            className="bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors"
          >
            Send
          </button>
        </div>
      </div>
    </div>
  )
}
