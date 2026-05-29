export interface Agent {
  id: string
  name: string
  display_name: string
  harness: string
  base_url: string
  status: string
  last_seen: string | null
  role?: string
  system_prompt?: string
}

export interface Model {
  id: string
  owned_by: string
  display_name?: string
}

export interface Conversation {
  id: string
  agent_id: string
  title: string
  summary: string | null
  created_at?: string
  updated_at?: string
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
  context_sources?: string[]
  tool_name?: string
  tool_status?: string
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

export interface AgentCommand {
  command: string
  description: string
}

export function getAgentCommands(id: string): Promise<AgentCommand[]> {
  return request<AgentCommand[]>(`/api/agents/${id}/commands`)
}

export function listConversations(): Promise<Conversation[]> {
  return request<Conversation[]>('/api/conversations')
}

export function summarizeConversations(conversationIds: string[]): Promise<Record<string, string>> {
  return request<{ summaries: Record<string, string> }>('/api/conversations/summarize', {
    method: 'POST',
    body: JSON.stringify({ conversation_ids: conversationIds }),
  }).then((r) => r.summaries)
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

  // Use AbortController only for connect timeout (10s).
  // No idle timeout — like Telegram, the connection stays open until
  // the server finishes or the user aborts.
  const abortController = new AbortController()
  const connectTimeout = setTimeout(() => abortController.abort(), 10_000)

  const stream = new ReadableStream<ChatChunk>({
    async start(controller) {
      let res: Response
      try {
        res = await fetch(`/api/agents/${agentId}/chat`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
          signal: abortController.signal,
        })
      } catch (err) {
        clearTimeout(connectTimeout)
        controller.error(err instanceof Error ? err : new Error(String(err)))
        return
      }
      clearTimeout(connectTimeout)

      if (!res.ok) {
        let errMsg = `Chat failed (${res.status})`
        try {
          const body = await res.text()
          if (body) errMsg = body
        } catch { /* ignore */ }
        // Friendly message for unsupported agents
        if (res.status === 501 || errMsg.includes('not supported')) {
          errMsg = 'This agent does not support chat. Try selecting a different agent.'
        }
        controller.error(new Error(errMsg))
        return
      }

      const reader = res.body?.getReader()
      if (!reader) {
        controller.error(new Error('No response body'))
        return
      }

      const decoder = new TextDecoder()
      let buffer = ''
      let currentEvent = ''

      try {
        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() ?? ''

          for (const line of lines) {
            // Track SSE event type
            if (line.startsWith('event: ')) {
              currentEvent = line.slice(7).trim()
              continue
            }
            // Empty line = event separator, reset event type
            if (line === '') {
              currentEvent = ''
              continue
            }
            if (line.startsWith('data: ')) {
              const payload = line.slice(6).trim()
              if (payload === '') continue
              try {
                const parsed = JSON.parse(payload)
                const chunk = parsed as ChatChunk

                // For tool events, embed the event info into the chunk
                if (currentEvent === 'tool' && parsed.tool_name) {
                  chunk.tool_name = parsed.tool_name
                  chunk.tool_status = parsed.tool_status
                  chunk.content = ''
                  chunk.done = false
                }

                controller.enqueue(chunk)
              } catch {
                // skip malformed JSON lines
              }
              currentEvent = ''
            }
          }
        }
      } catch (err) {
        if (abortController.signal.aborted) {
          controller.error(new Error('Connection timed out. Please try again.'))
        } else {
          controller.error(err)
        }
      } finally {
        controller.close()
      }
    },
    cancel() {
      abortController.abort()
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
  type: 'file' | 'dir' | 'folder'
  children?: MemoryTreeNode[] | null
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

export function getMemoryTree(path?: string, depth?: number): Promise<MemoryTreeNode[]> {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  if (depth !== undefined) params.set('depth', String(depth))
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

export interface StudioProvider {
  name: string
  type: string
  models: string[]
  requires_key: boolean
  available: boolean
}

export function getStudioProviders(): Promise<StudioProvider[]> {
  return request<StudioProvider[]>('/api/studio/providers')
}

export function studioGenerate(
  prompt: string,
  type: string,
  model?: string,
  provider?: string,
  agentId?: string,
): Promise<StudioGeneration> {
  const body: Record<string, string> = { prompt, type }
  if (model) body.model = model
  if (provider) body.provider = provider
  if (agentId) body.agent_id = agentId
  return request<StudioGeneration>('/api/studio/generate', {
    method: 'POST',
    body: JSON.stringify(body),
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
  progress: number
  target_date: string | null
  metadata: Record<string, unknown>
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

export function deleteGoal(id: string): Promise<void> {
  return request<void>(`/api/goals/${id}`, {
    method: 'DELETE',
  })
}

export function breakdownTask(id: string): Promise<Task[]> {
  return request<Task[]>(`/api/tasks/${id}/breakdown`, {
    method: 'POST',
  })
}

// --- Workflows ---

export interface WorkflowStep {
  name: string
  prompt: string
}

export interface Workflow {
  id: string
  name: string
  description: string
  steps: WorkflowStep[]
  agent_id: string | null
  created_at?: string
  updated_at?: string
}

export interface WorkflowRun {
  id: string
  workflow_id: string
  status: string
  current_step: number
  result: Record<string, unknown>
  created_at?: string
  updated_at?: string
}

export interface WorkflowCreate {
  name: string
  description?: string
  steps: WorkflowStep[]
  agent_id?: string | null
}

export interface WorkflowUpdate {
  name?: string
  description?: string
  steps?: WorkflowStep[]
  agent_id?: string | null
}

export function listWorkflows(): Promise<Workflow[]> {
  return request<Workflow[]>('/api/workflows')
}

export function getWorkflow(id: string): Promise<Workflow> {
  return request<Workflow>(`/api/workflows/${id}`)
}

export function createWorkflow(data: WorkflowCreate): Promise<Workflow> {
  return request<Workflow>('/api/workflows', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateWorkflow(id: string, data: WorkflowUpdate): Promise<Workflow> {
  return request<Workflow>(`/api/workflows/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  })
}

export async function deleteWorkflow(id: string): Promise<void> {
  const res = await fetch(`/api/workflows/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
}

export function runWorkflow(id: string): Promise<WorkflowRun> {
  return request<WorkflowRun>(`/api/workflows/${id}/run`, {
    method: 'POST',
  })
}

// --- Skills ---

export interface Skill {
  id: string
  name: string
  description: string
  category: string
  content?: string  // not included in list summaries, only in GET /api/skills/{id}
  triggers: string[]
  agent_id: string | null
  created_at?: string
  updated_at?: string
}

export interface SkillCreate {
  name: string
  description?: string
  category?: string
  content: string
  triggers?: string[]
  agent_id?: string | null
}

export interface SkillUpdate {
  name?: string
  description?: string
  category?: string
  content?: string
  triggers?: string[]
  agent_id?: string | null
}

export function listSkills(): Promise<Skill[]> {
  return request<Skill[]>('/api/skills')
}

export function getSkill(id: string): Promise<Skill> {
  return request<Skill>(`/api/skills/${id}`)
}

export function createSkill(data: SkillCreate): Promise<Skill> {
  return request<Skill>('/api/skills', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateSkill(id: string, data: SkillUpdate): Promise<Skill> {
  return request<Skill>(`/api/skills/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  })
}

export async function deleteSkill(id: string): Promise<void> {
  const res = await fetch(`/api/skills/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
}

export interface SkillSyncResult {
  status: string
  synced: number
  created: number
  updated: number
  total: number
  errors: string[]
}

export async function syncSkillsFromHermes(): Promise<SkillSyncResult> {
  return request<SkillSyncResult>('/api/skills/sync', { method: 'POST' })
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

// --- Timeline ---

export interface TimelineEvent {
  id: string
  type: 'conversation' | 'task_completed' | 'artifact_created' | 'workflow_run' | 'delegation'
  title: string
  description: string
  timestamp: string
  agent_id: string
  agent_name: string
  metadata?: Record<string, string>
}

interface TimelineResponse {
  events: TimelineEvent[]
  total: number
  limit: number
  offset: number
}

export function getTimeline(limit?: number, offset?: number): Promise<TimelineResponse> {
  const params = new URLSearchParams()
  if (limit !== undefined) params.set('limit', String(limit))
  if (offset !== undefined) params.set('offset', String(offset))
  const qs = params.toString()
  return request<TimelineResponse>(`/api/timeline${qs ? `?${qs}` : ''}`)
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

export function exportArtifact(id: string): Promise<ExportResult> {
  return request<ExportResult>(`/api/artifacts/${id}/export`, {
    method: 'POST',
  })
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

export async function discoverAgents(): Promise<DiscoveredAgent[]> {
  const res = await request<{discovered: DiscoveredAgent[], total: number}>('/api/agents/discover')
  return res.discovered ?? []
}

export async function autoRegisterAgents(agentIds?: string[]): Promise<Agent[]> {
  const res = await request<{registered: Agent[], count: number}>('/api/agents/auto-register', {
    method: 'POST',
    body: JSON.stringify(agentIds ? { agent_ids: agentIds } : {}),
  })
  return res.registered ?? []
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

// --- Agent Config ---

export interface AgentConfig {
  id: string
  role: string
  system_prompt: string
  persona: Record<string, unknown>
}

export function getAgentConfig(id: string): Promise<AgentConfig> {
  return request<AgentConfig>(`/api/agents/${id}/config`)
}

export function updateAgentConfig(
  id: string,
  data: { role: string; system_prompt: string; persona?: Record<string, unknown> },
): Promise<Agent> {
  return request<Agent>(`/api/agents/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  })
}

// --- Slash Commands ---

export interface SlashCommandResult {
  type: 'new' | 'clear' | 'compact' | 'retry' | 'undo' | 'history' | 'title' | 'stop' | 'save' | 'compress' | 'forward'
  message: string
  data: Record<string, unknown>
}

export function executeSlashCommand(
  command: string,
  agentId: string,
  conversationId?: string,
): Promise<SlashCommandResult> {
  return request<SlashCommandResult>('/api/slash-command', {
    method: 'POST',
    body: JSON.stringify({ command, agent_id: agentId, conversation_id: conversationId ?? '' }),
  })
}
