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
  conversation_id?: string
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

// --- Artifacts ---

export interface Artifact {
  id: string
  agent_id: string
  filename: string
  content_type: string
  artifact_type: 'image' | 'video' | 'audio' | 'code' | 'text'
  size: number
  created_at: string
  metadata?: Record<string, string>
}

export interface ListArtifactsResponse {
  artifacts: Artifact[]
  total: number
}

export function listArtifacts(
  type?: string,
  agentId?: string,
  limit?: number,
  offset?: number,
): Promise<ListArtifactsResponse> {
  const params = new URLSearchParams()
  if (type) params.set('type', type)
  if (agentId) params.set('agent_id', agentId)
  if (limit !== undefined) params.set('limit', String(limit))
  if (offset !== undefined) params.set('offset', String(offset))
  const qs = params.toString()
  return request<ListArtifactsResponse>(`/api/artifacts${qs ? `?${qs}` : ''}`)
}

export function getArtifact(id: string): Promise<Artifact> {
  return request<Artifact>(`/api/artifacts/${id}`)
}

export async function deleteArtifact(id: string): Promise<void> {
  const res = await fetch(`/api/artifacts/${id}`, { method: 'DELETE' })
  if (!res.ok) {
    throw new Error(`API error ${res.status}: ${res.statusText}`)
  }
}

export async function uploadArtifact(
  file: File,
  metadata?: Record<string, string>,
): Promise<Artifact> {
  const form = new FormData()
  form.append('file', file)
  if (metadata) {
    form.append('metadata', JSON.stringify(metadata))
  }
  const res = await fetch('/api/artifacts', {
    method: 'POST',
    body: form,
  })
  if (!res.ok) {
    throw new Error(`API error ${res.status}: ${res.statusText}`)
  }
  return res.json() as Promise<Artifact>
}

// --- Memory ---

export interface MemoryTreeNode {
  name: string
  path: string
  type: 'file' | 'folder'
  children?: MemoryTreeNode[]
}

export interface MemoryFile {
  path: string
  content: string
}

export interface MemorySearchResult {
  path: string
  title: string
  snippet: string
}

export function getMemoryTree(path?: string): Promise<MemoryTreeNode[]> {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  const qs = params.toString()
  return request<MemoryTreeNode[]>(`/api/memory/tree${qs ? `?${qs}` : ''}`)
}

export function getMemoryFile(path: string): Promise<MemoryFile> {
  const params = new URLSearchParams({ path })
  return request<MemoryFile>(`/api/memory/file?${params}`)
}

export function saveMemoryFile(path: string, content: string): Promise<MemoryFile> {
  return request<MemoryFile>('/api/memory/file', {
    method: 'POST',
    body: JSON.stringify({ path, content }),
  })
}

export function searchMemory(query: string): Promise<MemorySearchResult[]> {
  const params = new URLSearchParams({ q: query })
  return request<MemorySearchResult[]>(`/api/memory/search?${params}`)
}

export interface SynthesizeResponse {
  path: string
  content: string
}

export function synthesizeMemory(
  paths: string[],
  type: string,
): Promise<SynthesizeResponse> {
  return request<SynthesizeResponse>('/api/memory/synthesize', {
    method: 'POST',
    body: JSON.stringify({ paths, type }),
  })
}

// --- Studio ---

export interface StudioGeneration {
  id: string
  prompt: string
  type: 'image' | 'video' | 'audio'
  model: string
  url: string
  created_at: string
}

export function studioGenerate(
  prompt: string,
  type: string,
  model?: string,
): Promise<StudioGeneration> {
  return request<StudioGeneration>('/api/studio/generate', {
    method: 'POST',
    body: JSON.stringify({ prompt, type, model }),
  })
}

export function listGenerations(): Promise<StudioGeneration[]> {
  return request<StudioGeneration[]>('/api/studio/generations')
}

// --- Tasks ---

export interface Task {
  id: string
  title: string
  description: string
  status: 'backlog' | 'in_progress' | 'review' | 'done'
  priority: number
  agent_id: string | null
  due_date: string | null
  order: number
  created_at?: string
  updated_at?: string
}

export interface TaskCreate {
  title: string
  description?: string
  status?: string
  priority?: number
  agent_id?: string | null
  due_date?: string | null
  order?: number
}

export interface TaskUpdate {
  title?: string
  description?: string
  status?: string
  priority?: number
  agent_id?: string | null
  due_date?: string | null
  order?: number
}

export function listTasks(
  status?: string,
  agentId?: string,
  priority?: number,
): Promise<Task[]> {
  const params = new URLSearchParams()
  if (status) params.set('status', status)
  if (agentId) params.set('agent_id', agentId)
  if (priority !== undefined) params.set('priority', String(priority))
  const qs = params.toString()
  return request<Task[]>(`/api/tasks${qs ? `?${qs}` : ''}`)
}

export function createTask(data: TaskCreate): Promise<Task> {
  return request<Task>('/api/tasks', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateTask(id: string, data: TaskUpdate): Promise<Task> {
  return request<Task>(`/api/tasks/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export async function deleteTask(id: string): Promise<void> {
  const res = await fetch(`/api/tasks/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
}

export function reorderTasks(tasks: { id: string; status: string; order: number }[]): Promise<Task[]> {
  return request<Task[]>('/api/tasks/reorder', {
    method: 'POST',
    body: JSON.stringify({ tasks }),
  })
}

// --- Goals ---

export interface Goal {
  id: string
  title: string
  description: string
  status: 'active' | 'completed' | 'paused'
  target_date: string | null
  completed_tasks: number
  total_tasks: number
  created_at?: string
  updated_at?: string
}

export interface GoalCreate {
  title: string
  description?: string
  status?: string
  target_date?: string | null
}

export interface GoalUpdate {
  title?: string
  description?: string
  status?: string
  target_date?: string | null
}

export function listGoals(): Promise<Goal[]> {
  return request<Goal[]>('/api/goals')
}

export function createGoal(data: GoalCreate): Promise<Goal> {
  return request<Goal>('/api/goals', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateGoal(id: string, data: GoalUpdate): Promise<Goal> {
  return request<Goal>(`/api/goals/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export function breakdownGoal(id: string): Promise<Task[]> {
  return request<Task[]>(`/api/goals/${id}/breakdown`, {
    method: 'POST',
  })
}

// --- Pipeline ---

export interface PipelineItem {
  id: string
  title: string
  type: 'blog' | 'social' | 'email' | 'ad' | 'other'
  status: 'draft' | 'ai_review' | 'human_review' | 'published'
  content: string
  outline: string
  created_at?: string
  updated_at?: string
}

export interface PipelineCreate {
  title: string
  type?: string
  status?: string
  content?: string
  outline?: string
}

export interface PipelineUpdate {
  title?: string
  type?: string
  status?: string
  content?: string
  outline?: string
}

export function listPipeline(
  status?: string,
  type?: string,
): Promise<PipelineItem[]> {
  const params = new URLSearchParams()
  if (status) params.set('status', status)
  if (type) params.set('type', type)
  const qs = params.toString()
  return request<PipelineItem[]>(`/api/pipeline${qs ? `?${qs}` : ''}`)
}

export function createPipelineItem(data: PipelineCreate): Promise<PipelineItem> {
  return request<PipelineItem>('/api/pipeline', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updatePipelineItem(id: string, data: PipelineUpdate): Promise<PipelineItem> {
  return request<PipelineItem>(`/api/pipeline/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export function generateContent(id: string): Promise<PipelineItem> {
  return request<PipelineItem>(`/api/pipeline/${id}/generate`, {
    method: 'POST',
  })
}

export function advancePipeline(id: string): Promise<PipelineItem> {
  return request<PipelineItem>(`/api/pipeline/${id}/advance`, {
    method: 'POST',
  })
}

// --- Activity Feed ---

export interface ActivityEvent {
  id: string
  type: 'agent_status' | 'chat' | 'artifact' | 'task' | 'other'
  summary: string
  timestamp: string
  target?: string
}

interface ActivityListResponse {
  events: ActivityEvent[]
  total: number
  limit: number
  offset: number
}

export function getActivity(limit?: number, offset?: number): Promise<ActivityEvent[]> {
  const params = new URLSearchParams()
  if (limit !== undefined) params.set('limit', String(limit))
  if (offset !== undefined) params.set('offset', String(offset))
  const qs = params.toString()
  return request<ActivityListResponse>(`/api/activity${qs ? `?${qs}` : ''}`)
    .then((res) => res.events)
}

// --- Obsidian Export ---

export interface ExportResult {
  path: string
  title: string
}

export function exportConversation(id: string): Promise<ExportResult> {
  return request<ExportResult>(`/api/conversations/${id}/export`, {
    method: 'POST',
  })
}

export interface LinkedNote {
  path: string
  title: string
  snippet: string
}

export function getArtifactNotes(id: string): Promise<LinkedNote[]> {
  return request<LinkedNote[]>(`/api/artifacts/${id}/notes`)
}

export function getTaskNotes(id: string): Promise<LinkedNote[]> {
  return request<LinkedNote[]>(`/api/tasks/${id}/notes`)
}

// --- Agent Discovery ---

export interface DiscoveredAgent {
  id: string
  name: string
  base_url: string
  harness: string
}

export function discoverAgents(): Promise<DiscoveredAgent[]> {
  return request<DiscoveredAgent[]>('/api/agents/discover')
}

export function autoRegisterAgents(agentIds?: string[]): Promise<Agent[]> {
  return request<Agent[]>('/api/agents/auto-register', {
    method: 'POST',
    body: JSON.stringify(agentIds ? { agent_ids: agentIds } : {}),
  })
}

// --- Health ---

export interface HealthStatus {
  status: string
  uptime?: number
  version?: string
}

export function getHealth(): Promise<HealthStatus> {
  return request<HealthStatus>('/api/health')
}

/** Simple boolean health check for the status footer. */
export async function checkHealth(): Promise<boolean> {
  try {
    const res = await fetch('/api/health')
    return res.ok
  } catch {
    return false
  }
}
