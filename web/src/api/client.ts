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
  /** Number of messages in the conversation (populated by GET /api/conversations). */
  message_count?: number
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

// ---------------------------------------------------------------------------
// Resource vault (credentials / integrations / mcp_servers), capability grants,
// harness catalog, and agent create/delete. Secrets are write-only: the API
// returns is_set + last4 only, never plaintext.
// ---------------------------------------------------------------------------

export type ResourceKind = 'credential' | 'integration' | 'mcp_server'

export interface Resource {
  id: string
  slug: string
  kind: ResourceKind
  label: string
  provider: string
  is_secret: boolean
  is_set: boolean
  last4?: string
  config: Record<string, unknown>
  status: string
  created_at?: string
  updated_at?: string
}

export interface ResourcesResponse {
  resources: Resource[]
  secrets_enabled: boolean
}

export function listResources(kind?: ResourceKind): Promise<ResourcesResponse> {
  const q = kind ? `?kind=${kind}` : ''
  return request<ResourcesResponse>(`/api/resources${q}`)
}

export interface CreateResourceInput {
  slug: string
  kind: ResourceKind
  label?: string
  provider?: string
  secret?: string // plaintext; encrypted server-side
  config?: Record<string, unknown>
}

export function createResource(input: CreateResourceInput): Promise<Resource> {
  return request<Resource>('/api/resources', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export interface UpdateResourceInput {
  label?: string
  provider?: string
  secret?: string // omit to leave unchanged; "" to clear
  config?: Record<string, unknown>
}

export function updateResource(id: string, input: UpdateResourceInput): Promise<Resource> {
  return request<Resource>(`/api/resources/${id}`, {
    method: 'PUT',
    body: JSON.stringify(input),
  })
}

export async function deleteResource(id: string): Promise<void> {
  const res = await fetch(`/api/resources/${id}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 204) {
    throw new Error(`API error ${res.status}: ${res.statusText}`)
  }
}

// Grant edges for the permission matrix.
export interface GrantEdge {
  agent_id: string
  resource_id: string
  scope: string
  granted_at?: string
}

export function listAllGrants(): Promise<{ grants: GrantEdge[] }> {
  return request<{ grants: GrantEdge[] }>('/api/grants')
}

export function listAgentGrants(agentId: string): Promise<{ resources: Resource[] }> {
  return request<{ resources: Resource[] }>(`/api/agents/${agentId}/grants`)
}

export function grantResource(agentId: string, resourceId: string): Promise<GrantEdge> {
  return request<GrantEdge>(`/api/agents/${agentId}/grants/${resourceId}`, {
    method: 'PUT',
    body: JSON.stringify({ scope: 'use' }),
  })
}

export async function revokeResource(agentId: string, resourceId: string): Promise<void> {
  const res = await fetch(`/api/agents/${agentId}/grants/${resourceId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 204) {
    throw new Error(`API error ${res.status}: ${res.statusText}`)
  }
}

export interface HarnessInfo {
  name: string
  description: string
  requires_auth_token: boolean
}

export function listHarnesses(): Promise<HarnessInfo[]> {
  return request<HarnessInfo[]>('/api/harnesses')
}

export interface CreateAgentInput {
  name: string
  display_name: string
  harness: string
  base_url: string
  auth_token?: string
}

export function createAgent(input: CreateAgentInput): Promise<Agent> {
  return request<Agent>('/api/agents', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function deleteAgent(id: string): Promise<void> {
  const res = await fetch(`/api/agents/${id}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 204) {
    throw new Error(`API error ${res.status}: ${res.statusText}`)
  }
}

export function updateAgentConfigFull(
  id: string,
  body: { role: string; system_prompt: string; persona?: Record<string, unknown> },
): Promise<Agent> {
  return request<Agent>(`/api/agents/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ persona: {}, ...body }),
  })
}


export function listConversations(): Promise<Conversation[]> {
  return request<Conversation[]>('/api/conversations')
}

export function getMessages(convId: string, signal?: AbortSignal): Promise<Message[]> {
  return request<Message[]>(`/api/conversations/${convId}/messages`, { signal })
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
  mode?: string,
): ReadableStream<ChatChunk> {
  const body: Record<string, string> = { message }
  if (model) body.model = model
  if (conversationId) body.conversation_id = conversationId
  if (mode) body.mode = mode

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

/**
 * Reuse the existing STT endpoint (POST /api/voice/transcribe -> Whisper via
 * LiteLLM). Shared by VoiceButton and Talk Mode so transcription is not
 * duplicated (#124). Accepts a recorded audio blob, returns trimmed text.
 */
export async function transcribeAudio(blob: Blob): Promise<string> {
  const formData = new FormData()
  formData.append('file', blob, 'recording.webm')
  const res = await fetch('/api/voice/transcribe', { method: 'POST', body: formData })
  if (!res.ok) {
    throw new Error(`Transcription failed (${res.status})`)
  }
  const data = (await res.json()) as { text?: string }
  return (data.text ?? '').trim()
}

/**
 * Reuse the existing TTS endpoint (POST /api/voice/synthesize -> tts-1 via
 * LiteLLM). Returns synthesized speech as a Blob ready for playback (#124).
 */
export async function synthesizeSpeech(text: string, voice = 'alloy'): Promise<Blob> {
  const res = await fetch('/api/voice/synthesize', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, voice }),
  })
  if (!res.ok) {
    throw new Error(`Speech synthesis failed (${res.status})`)
  }
  return res.blob()
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

export interface VideoJob { id: string; state: 'queued'|'processing'|'complete'|'failed'; progress: number; video_url?: string; error?: string }
export function submitVideoJob(prompt: string, model?: string, provider?: string): Promise<VideoJob> {
  const body: Record<string,string> = { prompt }
  if (model) body.model = model
  if (provider) body.provider = provider
  return request<VideoJob>('/api/studio/video/jobs', { method:'POST', body: JSON.stringify(body) })
}
export function getVideoJob(id: string): Promise<VideoJob> {
  return request<VideoJob>(`/api/studio/video/jobs/${id}`)
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

// --- Workflow Templates ---

// A predefined, instantiable workflow definition. Use createWorkflow() with a
// template's name/description/steps to instantiate it as a runnable workflow.
export interface WorkflowTemplate {
  key: string
  name: string
  description: string
  category: string
  steps: WorkflowStep[]
}

export function listWorkflowTemplates(): Promise<WorkflowTemplate[]> {
  return request<WorkflowTemplate[]>('/api/workflow-templates')
}

export function getWorkflowTemplate(key: string): Promise<WorkflowTemplate> {
  return request<WorkflowTemplate>(`/api/workflow-templates/${key}`)
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

// --- Observe (SPOG): work-units + trackers ---

/** A correlated work-unit from GET /api/work-units. Liveness is derived SERVER-SIDE
 *  (per-session, from received_at — the only liveness clock, contract §4) and aggregated
 *  with precedence failed>running>stale>done. The UI renders `liveness` directly. */
export interface WorkUnit {
  tenant: string
  external_ref: string | null
  branch: string | null
  sha: string | null
  event_count: number
  session_count: number
  first_event_at: string
  last_event_at: string
  correlated: boolean
  liveness: 'running' | 'done' | 'stale' | 'failed' | ''
  active_session_count: number
  latest_kind?: string
  title?: string
  harnesses?: string[]
  cost_usd?: number
}

export interface WorkUnitsResponse {
  work_units: WorkUnit[]
  total: number
  limit: number
  offset: number
}

/** List work-units. tenant scopes server-side (ADR-002); omit/'' for all tenants. */
export function getWorkUnits(opts: { tenant?: string; limit?: number; offset?: number } = {}): Promise<WorkUnitsResponse> {
  const params = new URLSearchParams({
    limit: String(opts.limit ?? 50),
    offset: String(opts.offset ?? 0),
  })
  if (opts.tenant) params.set('tenant', opts.tenant)
  return request<WorkUnitsResponse>(`/api/work-units?${params.toString()}`)
}

/** A mirrored tracker item from GET /api/trackers. Read-only; the plane never
 *  writes back to trackers (ADR-001 D4). Shape from the live tracker smoke test. */
export interface TrackerItem {
  id: string
  project_id: string
  external_ref: string
  title: string
  status: string
  item_type: string
  canonical_url: string
  tenant: string
  synced_at: string
  created_at?: string
  updated_at?: string
}

export interface TrackerItemsResponse {
  items: TrackerItem[]
  total: number
  limit: number
  offset: number
}

/** List tracker items. When projectId is given, tenant is required (server enforces
 *  ADR-002 tenant-scoping with a 400 otherwise). */
export function getTrackerItems(opts: {
  projectId?: string
  tenant?: string
  limit?: number
  offset?: number
} = {}): Promise<TrackerItemsResponse> {
  const params = new URLSearchParams()
  if (opts.projectId) params.set('project_id', opts.projectId)
  if (opts.tenant) params.set('tenant', opts.tenant)
  params.set('limit', String(opts.limit ?? 50))
  params.set('offset', String(opts.offset ?? 0))
  return request<TrackerItemsResponse>(`/api/trackers/?${params.toString()}`)
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

// --- SPOG Ledger ---

export interface RunLogResponse {
  id: number
  ts: string
  event_type: string
  pr_ref: string
  wp_ref: string
  summary?: string
  payload?: unknown
}

export interface RunLogListResponse {
  records: RunLogResponse[]
  total: number
  limit: number
  offset: number
}

export interface FindingResponse {
  id: number
  ts: string
  pr_ref: string
  wp_ref: string
  gate: number
  author_agent: string
  model: string
  severity: string
  class: string
  root_cause?: string
  summary?: string
}

export interface FindingsListResponse {
  records: FindingResponse[]
  total: number
  limit: number
  offset: number
}

export interface RecurringFindingsRow {
  class: string
  author_agent: string
  wp_ref: string
  count: number
}

export interface RecurringFindingsResponse {
  records: RecurringFindingsRow[]
}

export interface PostRunLogRequest {
  event_type: string
  pr_ref: string
  wp_ref: string
  summary: string
  payload: unknown
}

export interface PostFindingRequest {
  pr_ref: string
  wp_ref: string
  gate: number
  author_agent: string
  model: string
  severity: string
  class: string
  root_cause: string
  summary: string
}

export function listLedgerRuns(opts: { limit?: number; offset?: number; wp_ref?: string } = {}): Promise<RunLogListResponse> {
  const params = new URLSearchParams()
  if (opts.limit !== undefined) params.set('limit', String(opts.limit))
  if (opts.offset !== undefined) params.set('offset', String(opts.offset))
  if (opts.wp_ref !== undefined) params.set('wp_ref', opts.wp_ref)
  const qs = params.toString()
  return request<RunLogListResponse>(`/api/ledger/runs${qs ? `?${qs}` : ''}`)
}

export function listFindings(opts: {
  limit?: number
  offset?: number
  class?: string
  severity?: string
  wp_ref?: string
} = {}): Promise<FindingsListResponse> {
  const params = new URLSearchParams()
  if (opts.limit !== undefined) params.set('limit', String(opts.limit))
  if (opts.offset !== undefined) params.set('offset', String(opts.offset))
  if (opts.class !== undefined) params.set('class', opts.class)
  if (opts.severity !== undefined) params.set('severity', opts.severity)
  if (opts.wp_ref !== undefined) params.set('wp_ref', opts.wp_ref)
  const qs = params.toString()
  return request<FindingsListResponse>(`/api/ledger/findings${qs ? `?${qs}` : ''}`)
}

export function getRecurringFindings(min_count?: number): Promise<RecurringFindingsResponse> {
  const params = new URLSearchParams()
  if (min_count !== undefined) params.set('min_count', String(min_count))
  const qs = params.toString()
  return request<RecurringFindingsResponse>(`/api/ledger/recurrence${qs ? `?${qs}` : ''}`)
}

export function postRunLog(payload: PostRunLogRequest): Promise<RunLogResponse> {
  return request<RunLogResponse>('/api/ledger/runs', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function postFinding(payload: PostFindingRequest): Promise<FindingResponse> {
  return request<FindingResponse>('/api/ledger/findings', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

// --- SPOG Fleet ---

export interface SessionStatus {
  harness: string
  session_id: string
  host: string
  pid?: number
  liveness_mode: string
  tenant: string
  status: string
  last_event_at: string
  last_event_kind: string
  last_event_status?: string
}

export interface FleetResponse {
  sessions: SessionStatus[]
  total: number
}

export function getFleet(tenant: string): Promise<FleetResponse> {
  const params = new URLSearchParams()
  params.set('tenant', tenant)
  return request<FleetResponse>(`/api/fleet?${params.toString()}`)
}

// --- SPOG Incidents ---

export interface Incident {
  type: string
  harness: string
  session_id: string
  host: string
  title: string
  status: string
  tenant: string
  project_slug: string
  external_ref: string
  branch: string
  received_at: string
}

export interface IncidentsResponse {
  incidents: Incident[]
  total: number
  limit: number
  offset: number
}

export function listIncidents(tenant: string, opts: {
  limit?: number
  offset?: number
  stale_window?: string
} = {}): Promise<IncidentsResponse> {
  const params = new URLSearchParams()
  params.set('tenant', tenant)
  if (opts.limit !== undefined) params.set('limit', String(opts.limit))
  if (opts.offset !== undefined) params.set('offset', String(opts.offset))
  if (opts.stale_window !== undefined) params.set('stale_window', opts.stale_window)
  return request<IncidentsResponse>(`/api/incidents?${params.toString()}`)
}

// --- SPOG Spend ---

/**
 * A single spend/usage aggregation row.
 *
 * Usage (total_tokens, total_turns) is ALWAYS meaningful and is the primary metric.
 * total_cost_usd is NULLABLE: null means "no dollar cost applies" — the group is a
 * subscription (flat-rate) account or no session reported a cost. Only render a $
 * figure when billing_mode === 'metered' and total_cost_usd != null.
 */
export interface SpendRow {
  dimension_key: string
  /** Null for subscription/unknown billing modes — do NOT render as $0. */
  total_cost_usd: number | null
  total_tokens: number
  total_turns: number
  session_count: number
  /** 'subscription' | 'metered' | 'unknown' — only authoritative for group_by=agent. */
  billing_mode: 'subscription' | 'metered' | 'unknown'
  /** Resolved provider for agent-grouped rows ('' otherwise), e.g. 'anthropic'. */
  provider: string
}

export interface SpendResponse {
  rows: SpendRow[]
  total: number
  limit: number
  offset: number
}

export function getSpend(opts: {
  group_by?: string
  tenant?: string
  external_ref?: string
  limit?: number
  offset?: number
} = {}): Promise<SpendResponse> {
  const params = new URLSearchParams()
  if (opts.group_by !== undefined) params.set('group_by', opts.group_by)
  if (opts.tenant !== undefined) params.set('tenant', opts.tenant)
  if (opts.external_ref !== undefined) params.set('external_ref', opts.external_ref)
  if (opts.limit !== undefined) params.set('limit', String(opts.limit))
  if (opts.offset !== undefined) params.set('offset', String(opts.offset))
  const qs = params.toString()
  return request<SpendResponse>(`/api/spend${qs ? `?${qs}` : ''}`)
}

// --- SPOG Control ---

export interface ControlState {
  mode: 'continuous' | 'tick' | 'stopped'
  cadence_seconds: number
  queue_counts: Record<string, number>
  updated_at: string
}

export interface WorkUnitResponse {
  id: number
  wp_ref: string
  status: string
  payload: unknown
  claimed_at?: string
  completed_at?: string
  error?: string
  created_at: string
}
export interface UnitListResponse {
  units: WorkUnitResponse[]
  count: number
  limit: number
  offset: number
}

export function getControlState(): Promise<ControlState> {
  return request<ControlState>('/api/control/state')
}

export function setControlMode(mode: ControlState['mode'], cadenceSeconds?: number): Promise<ControlState> {
  return request<ControlState>('/api/control/mode', {
    method: 'POST',
    body: JSON.stringify({ mode, cadence_seconds: cadenceSeconds }),
  })
}

export function listControlUnits(status?: string, limit?: number, offset?: number): Promise<UnitListResponse> {
  const params = new URLSearchParams()
  if (status !== undefined) params.set('status', status)
  if (limit !== undefined) params.set('limit', String(limit))
  if (offset !== undefined) params.set('offset', String(offset))
  const qs = params.toString()
  return request<UnitListResponse>(`/api/control/units${qs ? `?${qs}` : ''}`)
}
