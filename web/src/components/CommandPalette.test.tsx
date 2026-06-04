import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import CommandPalette from './CommandPalette'
import { scoreMatch, filterItems } from './command-palette-utils'
import type { Agent } from '../api/client'

const mockTabs = ['Chat', 'Create', 'Build', 'Knowledge', 'Automate', 'Observe', 'Control', 'Settings'] as const

const mockAgents: Agent[] = [
  {
    id: 'agent-1',
    name: 'dev',
    display_name: 'Developer Agent',
    harness: 'dev-harness',
    status: 'online',
    last_seen: null,
    role: 'Software Development',
    base_url: 'http://localhost',
  },
  {
    id: 'agent-2',
    name: 'security',
    display_name: 'Security Shield',
    harness: 'security-harness',
    status: 'idle',
    last_seen: null,
    role: 'Vulnerability Analysis',
    base_url: 'http://localhost',
  },
]

describe('CommandPalette Helper Functions', () => {
  it('scoreMatch ranks exact and prefix matches above substring matches', () => {
    // Exact match
    const exact = scoreMatch('Chat', 'chat')
    // Prefix match
    const prefix = scoreMatch('Chat', 'ch')
    // Substring match
    const substring = scoreMatch('Observe Tech', 'ch')
    // Fuzzy sequence match
    const fuzzy = scoreMatch('Control Panel', 'cp')

    expect(exact).toBe(100)
    expect(prefix).toBe(80)
    expect(substring).toBe(50)
    expect(fuzzy).toBe(10)

    expect(exact).toBeGreaterThan(prefix)
    expect(prefix).toBeGreaterThan(substring)
    expect(substring).toBeGreaterThan(fuzzy)
  })

  it('filterItems returns all tabs, agents, and actions when query is empty', () => {
    const results = filterItems('', mockTabs, mockAgents)
    expect(results.goTo.length).toBe(mockTabs.length)
    expect(results.agents.length).toBe(mockAgents.length)
    expect(results.actions.length).toBe(2)
  })

  it('filterItems filters appropriately based on match scores', () => {
    // Search query matches "Build" tab exactly, plus "Developer Agent"'s role contains "dev"
    const results = filterItems('Build', mockTabs, mockAgents)
    expect(results.goTo.length).toBe(1)
    expect(results.goTo[0].label).toBe('Build')
    expect(results.agents.length).toBe(0)
  })
})

describe('CommandPalette Component', () => {
  const onNavigate = vi.fn()
  const onSelectAgent = vi.fn()
  const onNewChat = vi.fn()
  const onClose = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders nothing when open is false', () => {
    const { container } = render(
      <CommandPalette
        open={false}
        onClose={onClose}
        tabs={mockTabs}
        agents={mockAgents}
        onNavigate={onNavigate}
        onSelectAgent={onSelectAgent}
        onNewChat={onNewChat}
      />
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders input, grouped headers, tabs, agents and actions when open', () => {
    render(
      <CommandPalette
        open={true}
        onClose={onClose}
        tabs={mockTabs}
        agents={mockAgents}
        onNavigate={onNavigate}
        onSelectAgent={onSelectAgent}
        onNewChat={onNewChat}
      />
    )

    expect(screen.getByPlaceholderText('Search tabs, agents, actions...')).toBeInTheDocument()
    expect(screen.getByText('Go to')).toBeInTheDocument()
    expect(screen.getByText('Agents')).toBeInTheDocument()
    expect(screen.getByText('Actions')).toBeInTheDocument()

    // Check tabs
    expect(screen.getByText('Chat')).toBeInTheDocument()
    expect(screen.getByText('Settings')).toBeInTheDocument()

    // Check agents
    expect(screen.getByText('Developer Agent')).toBeInTheDocument()
    expect(screen.getByText('Security Shield')).toBeInTheDocument()

    // Check actions
    expect(screen.getByText('New Chat')).toBeInTheDocument()
    expect(screen.getByText('Go to Mission Control')).toBeInTheDocument()
  })

  it('filters results live on typing', async () => {
    const user = userEvent.setup()
    render(
      <CommandPalette
        open={true}
        onClose={onClose}
        tabs={mockTabs}
        agents={mockAgents}
        onNavigate={onNavigate}
        onSelectAgent={onSelectAgent}
        onNewChat={onNewChat}
      />
    )

    const input = screen.getByPlaceholderText('Search tabs, agents, actions...')
    await user.type(input, 'Create')

    // Tab "Create" should be visible
    expect(screen.getByText('Create')).toBeInTheDocument()
    // Tab "Chat" should NOT be visible
    expect(screen.queryByText('Chat')).not.toBeInTheDocument()
  })

  it('shows empty state row when query matches nothing', async () => {
    const user = userEvent.setup()
    render(
      <CommandPalette
        open={true}
        onClose={onClose}
        tabs={mockTabs}
        agents={mockAgents}
        onNavigate={onNavigate}
        onSelectAgent={onSelectAgent}
        onNewChat={onNewChat}
      />
    )
    const input = screen.getByPlaceholderText('Search tabs, agents, actions...')
    await user.type(input, 'not-matching-query-123')
    expect(
      screen.getByText((_content, element) => {
        return element?.textContent === 'No results for "not-matching-query-123"'
      })
    ).toBeInTheDocument()
  })

  it('activates tab on Enter keypress', async () => {
    const user = userEvent.setup()
    render(
      <CommandPalette
        open={true}
        onClose={onClose}
        tabs={mockTabs}
        agents={mockAgents}
        onNavigate={onNavigate}
        onSelectAgent={onSelectAgent}
        onNewChat={onNewChat}
      />
    )

    const input = screen.getByPlaceholderText('Search tabs, agents, actions...')
    await user.type(input, 'Observe')
    await user.keyboard('{Enter}')

    expect(onNavigate).toHaveBeenCalledWith('Observe')
    expect(onClose).toHaveBeenCalled()
  })

  it('activates agent on Enter keypress', async () => {
    const user = userEvent.setup()
    render(
      <CommandPalette
        open={true}
        onClose={onClose}
        tabs={mockTabs}
        agents={mockAgents}
        onNavigate={onNavigate}
        onSelectAgent={onSelectAgent}
        onNewChat={onNewChat}
      />
    )

    const input = screen.getByPlaceholderText('Search tabs, agents, actions...')
    await user.type(input, 'Security Shield')
    await user.keyboard('{Enter}')

    expect(onSelectAgent).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 'agent-2',
        name: 'security',
      })
    )
    expect(onClose).toHaveBeenCalled()
  })

  it('closes on Escape keypress', async () => {
    const user = userEvent.setup()
    render(
      <CommandPalette
        open={true}
        onClose={onClose}
        tabs={mockTabs}
        agents={mockAgents}
        onNavigate={onNavigate}
        onSelectAgent={onSelectAgent}
        onNewChat={onNewChat}
      />
    )

    const input = screen.getByPlaceholderText('Search tabs, agents, actions...')
    await user.type(input, '{Escape}')

    expect(onClose).toHaveBeenCalled()
  })
})
