import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import type { Agent } from './api/client'

// ── Mocks ───────────────────────────────────────────────────────────────────
// The App shell composes many data-fetching views. To isolate the unit under
// test — "the sidebar agent list survives tab navigation" — we stub the heavy
// child views and the SSE hook, and stub the API client so no real network runs.

vi.mock('./components/MissionControl', () => ({
  default: () => <div data-testid="mission-control" />,
}))
vi.mock('./components/kanban/Board', () => ({
  Board: () => <div data-testid="board" />,
}))
vi.mock('./components/StatusFooter', () => ({ StatusFooter: () => null }))
vi.mock('./components/Toast', () => ({ ToastContainer: () => null }))
vi.mock('./components/CommandPalette', () => ({ default: () => null }))
vi.mock('./hooks/useSSE', () => ({
  useSSE: () => ({ sseConnected: false, lastEvent: null }),
}))

// Mutable agent list + a spy for the conversation fetch. Declared as module
// state so the hoisted vi.mock factory below can close over them (access is
// deferred to render time, after these are initialized — same pattern as
// Sidebar.test.tsx).
let mockAgents: Agent[] = []
const mockListConversations = vi.fn((): Promise<unknown[]> => Promise.resolve([]))

vi.mock('./api/client', () => ({
  listAgents: () => Promise.resolve(mockAgents),
  listConversations: () => mockListConversations(),
  discoverAgents: () => Promise.resolve([]),
  autoRegisterAgents: () => Promise.resolve([]),
  uploadArtifact: () => Promise.resolve(undefined),
}))

import App from './App'

const agent: Agent = {
  id: 'agent-1',
  name: 'roux',
  display_name: 'Roux',
  harness: 'hermes',
  base_url: 'http://localhost:9999',
  status: 'online',
  last_seen: null,
}

describe('Sidebar agent list survives tab navigation (#120)', () => {
  beforeEach(() => {
    mockAgents = [agent]
    mockListConversations.mockClear()
  })

  it('keeps the agent list mounted across tabs and back, with no fetch storm', async () => {
    render(<App />)

    // Agent list is present in the sidebar on the initial Chat tab.
    await waitFor(() => {
      expect(screen.getByText('Roux')).toBeInTheDocument()
    })

    // The conversation list was fetched exactly once on the initial mount.
    expect(mockListConversations).toHaveBeenCalledTimes(1)

    // Navigate to a non-sidebar tab (Build). The sidebar must stay mounted
    // (hidden via CSS) — the pre-fix code unmounted it, wiping the agent list.
    // The rail tab's accessible name includes the material-symbol text, so match
    // on a substring.
    fireEvent.click(screen.getByRole('tab', { name: /Build/ }))
    expect(screen.getByText('Roux')).toBeInTheDocument() // still in the DOM
    expect(mockListConversations).toHaveBeenCalledTimes(1) // no re-fetch

    // Navigate back to Chat. The agent list is still there — still one fetch.
    fireEvent.click(screen.getByRole('tab', { name: /Chat/ }))
    expect(screen.getByText('Roux')).toBeInTheDocument()
    expect(mockListConversations).toHaveBeenCalledTimes(1) // no fetch storm
  })
})
