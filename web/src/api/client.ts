export interface Agent {
  id: string
  name: string
  display_name: string
  harness: string
  base_url: string
  status: string
  last_seen: string | null
}

export interface Model {
  id: string
  owned_by: string
}

export interface Conversation {
  id: string
  agent_id: string
  title: string
}

export interface Message {
  id: string
  role: 'user' | 'assistant' | 'system'
  content: string
  created_at?: string
}

export interface SSEEvent {
  type: string
  data: unknown
}

export interface ChatChunk {
  content: string
  done: boolean
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
  if (!res.ok) {
    throw new Error(`API error ${res.status}: ${res.statusText}`)
  }
  return res.json() as Promise<T>
}

export function listAgents(): Promise<Agent[]> {
  return request<Agent[]>('/api/agents')
}

export function getAgent(id: string): Promise<Agent> {
  return request<Agent>(`/api/agents/${id}`)
}

export function getAgentModels(id: string): Promise<Model[]> {
  return request<Model[]>(`/api/agents/${id}/models`)
}

export function listConversations(): Promise<Conversation[]> {
  return request<Conversation[]>('/api/conversations')
}

export function getMessages(convId: string): Promise<Message[]> {
  return request<Message[]>(`/api/conversations/${convId}/messages`)
}

export function createConversation(agentId: string, title: string): Promise<Conversation> {
  return request<Conversation>('/api/conversations', {
    method: 'POST',
    body: JSON.stringify({ agent_id: agentId, title }),
  })
}

export function sendChat(
  agentId: string,
  message: string,
  model?: string,
  conversationId?: string,
): ReadableStream<ChatChunk> {
  const body: Record<string, string> = { message }
  if (model) body.model = model
  if (conversationId) body.conversation_id = conversationId

  const stream = new ReadableStream<ChatChunk>({
    async start(controller) {
      const res = await fetch(`/api/agents/${agentId}/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })

      if (!res.ok) {
        controller.error(new Error(`Chat API error ${res.status}`))
        return
      }

      const reader = res.body?.getReader()
      if (!reader) {
        controller.error(new Error('No response body'))
        return
      }

      const decoder = new TextDecoder()
      let buffer = ''

      try {
        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() ?? ''

          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const payload = line.slice(6).trim()
              if (payload === '') continue
              try {
                const chunk = JSON.parse(payload) as ChatChunk
                controller.enqueue(chunk)
              } catch {
                // skip malformed JSON lines
              }
            }
          }
        }
      } catch (err) {
        controller.error(err)
      } finally {
        controller.close()
      }
    },
  })

  return stream
}

export function createEventSource(): EventSource {
  return new EventSource('/api/events')
}
