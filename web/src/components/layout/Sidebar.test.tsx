import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

import { Sidebar } from './Sidebar'
import type { Agent, Conversation } from '../../api/client'

// --- Mock the client module: Sidebar imports listConversations / discoverAgents /
// autoRegisterAgents directly. We only care about the conversation list here. ---
let mockConversations: Conversation[] = []

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    listConversations: () => Promise.resolve(mockConversations),
    discoverAgents: () => Promise.resolve([]),
    autoRegisterAgents: () => Promise.resolve([]),
  }
})

const agent: Agent = {
  id: 'agent-1',
  name: 'roux',
  display_name: 'Roux',
  harness: 'hermes',
  base_url: 'http://localhost:9999',
  status: 'online',
  last_seen: null,
}

function renderSidebar() {
  // Passing the agent as selectedAgent auto-expands its conversation tree
  // (Sidebar's useEffect on selectedAgentId), so nested conversations render.
  return render(
    <Sidebar
      agents={[agent]}
      selectedAgent={agent}
      onSelectAgent={() => {}}
      activeConversationId={null}
      onSelectConversation={() => {}}
      onNewChat={() => {}}
      onNewChatWithAgent={() => {}}
      conversationVersion={0}
    />,
  )
}

const baseConv: Omit<Conversation, 'id' | 'title' | 'summary'> = {
  agent_id: 'agent-1',
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
}

describe('Sidebar conversation label', () => {
  beforeEach(() => {
    mockConversations = []
  })

  it('displays the generated title when summary is absent (the core bug)', async () => {
    // This is the live defect: the backend titling pipeline writes `title`,
    // but `summary` is only populated by an endpoint the FE never calls. The
    // sidebar must fall back to `title` instead of showing "New conversation".
    mockConversations = [
      { ...baseConv, id: 'c1', title: 'Fix voice transcription errors', summary: null },
    ]
    renderSidebar()

    await waitFor(() => {
      expect(screen.getByText('Fix voice transcription errors')).toBeInTheDocument()
    })
    // The placeholder must NOT appear for a titled conversation.
    expect(screen.queryByText('New conversation')).not.toBeInTheDocument()
  })

  it('prefers summary over title when both are present', async () => {
    mockConversations = [
      { ...baseConv, id: 'c2', title: 'raw first message text', summary: 'Configure LiteLLM proxy routing' },
    ]
    renderSidebar()

    await waitFor(() => {
      expect(screen.getByText('Configure LiteLLM proxy routing')).toBeInTheDocument()
    })
    expect(screen.queryByText('raw first message text')).not.toBeInTheDocument()
  })

  it('falls back to placeholder only when neither title nor summary exists', async () => {
    mockConversations = [{ ...baseConv, id: 'c3', title: '', summary: null }]
    renderSidebar()

    await waitFor(() => {
      expect(screen.getByText('New conversation')).toBeInTheDocument()
    })
  })
})
