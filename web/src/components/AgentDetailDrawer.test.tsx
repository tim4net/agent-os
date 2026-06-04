import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AgentDetailDrawer from './AgentDetailDrawer'
import type { SpendRow, Agent, SessionStatus, Incident } from '../api/client'

const mockAgents: Agent[] = [
  {
    id: 'claude-1',
    name: 'claude',
    display_name: 'Claude 3.5 Sonnet',
    harness: 'claude',
    status: 'online',
    last_seen: null,
    base_url: 'http://localhost',
  }
]

const mockSessions: SessionStatus[] = [
  {
    harness: 'claude',
    session_id: 'session-running-1234',
    host: 'host-a',
    status: 'running',
    tenant: 'personal',
    liveness_mode: 'live',
    last_event_at: new Date().toISOString(),
    last_event_kind: 'agent_start',
  },
  {
    harness: 'claude',
    session_id: 'session-completed-5678',
    host: 'host-b',
    status: 'completed',
    tenant: 'dayjob',
    liveness_mode: 'live',
    last_event_at: new Date().toISOString(),
    last_event_kind: 'agent_completed',
  }
]

const mockIncidents: Incident[] = [
  {
    type: 'error',
    harness: 'claude',
    session_id: 'session-running-1234',
    host: 'host-a',
    title: 'Claude output invalid JSON',
    status: 'failed',
    tenant: 'personal',
    project_slug: 'agent-os',
    external_ref: 'err-99',
    branch: 'main',
    received_at: new Date().toISOString(),
  }
]

describe('AgentDetailDrawer Component', () => {
  const onClose = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders nothing when row is null', () => {
    const { container } = render(
      <AgentDetailDrawer
        row={null}
        onClose={onClose}
        agents={mockAgents}
        sessions={mockSessions}
        incidents={mockIncidents}
      />
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders stats, sessions, incidents, and metadata for a selected metered row', () => {
    const row: SpendRow = {
      dimension_key: 'claude',
      total_cost_usd: 12.34,
      total_tokens: 1500000,
      total_turns: 45,
      session_count: 5,
      billing_mode: 'metered',
      provider: 'anthropic',
    }

    render(
      <AgentDetailDrawer
        row={row}
        onClose={onClose}
        agents={mockAgents}
        sessions={mockSessions}
        incidents={mockIncidents}
      />
    )

    // Check title (harness)
    expect(screen.getByText('claude')).toBeInTheDocument()

    // Check billing chip (metered and dollars)
    expect(screen.getByText(/anthropic · metered · \$12\.34/i)).toBeInTheDocument()

    // Check total tokens
    expect(screen.getByText('1.5M')).toBeInTheDocument()

    // Check turns & sessions
    expect(screen.getByText(/45 turns · 5 sessions/i)).toBeInTheDocument()

    // Check session rendering
    expect(screen.getAllByText(/session-/i)).toHaveLength(2)
    expect(screen.getByText(/host: host-a/i)).toBeInTheDocument()

    // Check incident rendering
    expect(screen.getByText('Claude output invalid JSON')).toBeInTheDocument()
    expect(screen.getByText('proj: agent-os')).toBeInTheDocument()
  })

  it('renders subscription billing mode chip and never renders cost for subscription rows', () => {
    const row: SpendRow = {
      dimension_key: 'claude',
      total_cost_usd: null,
      total_tokens: 1500000,
      total_turns: 45,
      session_count: 5,
      billing_mode: 'subscription',
      provider: 'anthropic',
    }

    render(
      <AgentDetailDrawer
        row={row}
        onClose={onClose}
        agents={mockAgents}
        sessions={mockSessions}
        incidents={mockIncidents}
      />
    )

    // Check subscription billing chip
    expect(screen.getByText(/anthropic · subscription/i)).toBeInTheDocument()
    // Should NOT render cost label or value
    expect(screen.queryByText(/Metered Cost/i)).toBeNull()
  })

  it('handles empty states for sessions and incidents gracefully', () => {
    const row: SpendRow = {
      dimension_key: 'claude',
      total_cost_usd: 0,
      total_tokens: 0,
      total_turns: 0,
      session_count: 0,
      billing_mode: 'unknown',
      provider: '',
    }

    render(
      <AgentDetailDrawer
        row={row}
        onClose={onClose}
        agents={[]}
        sessions={[]}
        incidents={[]}
      />
    )

    // Check explicit empty states
    expect(screen.getByText('No live sessions')).toBeInTheDocument()
    expect(screen.getByText('No incidents — all clear')).toBeInTheDocument()
    // Verify unknown billing Mode chip
    expect(screen.getByText('usage-only')).toBeInTheDocument()
  })

  it('triggers onClose when close button is clicked', async () => {
    const row: SpendRow = {
      dimension_key: 'claude',
      total_cost_usd: null,
      total_tokens: 1000,
      total_turns: 2,
      session_count: 1,
      billing_mode: 'subscription',
      provider: 'anthropic',
    }

    render(
      <AgentDetailDrawer
        row={row}
        onClose={onClose}
        agents={mockAgents}
        sessions={mockSessions}
        incidents={mockIncidents}
      />
    )

    const closeBtn = screen.getByRole('button', { name: /close drawer/i })
    await userEvent.click(closeBtn)
    expect(onClose).toHaveBeenCalledOnce()
  })
})
