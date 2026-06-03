import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

import userEvent from '@testing-library/user-event'
import { ControlView } from './ControlView'

// --- Mock hooks ---
const mockRefetchState = vi.fn()
const mockRefetchUnits = vi.fn()

let mockControlState: {
  mode: 'continuous' | 'tick' | 'stopped'
  cadence_seconds: number
  queue_counts: Record<string, number>
  updated_at: string
} | null = {
  mode: 'continuous',
  cadence_seconds: 30,
  queue_counts: { queued: 2, running: 1, done: 5, failed: 1 },
  updated_at: '2026-06-02T12:00:00Z',
}

let mockControlUnits: {
  id: string
  wp_ref: string
  status: 'queued' | 'running' | 'done' | 'failed'
  created_at: string
  updated_at: string
  error: string | null
  result: unknown
}[] = [
  { id: 'u1', wp_ref: 'WP-1', status: 'queued', created_at: '2026-06-02T12:00:00Z', updated_at: '2026-06-02T12:00:00Z', error: null, result: null },
  { id: 'u2', wp_ref: 'WP-2', status: 'running', created_at: '2026-06-02T12:00:00Z', updated_at: '2026-06-02T12:00:00Z', error: null, result: null },
  { id: 'u3', wp_ref: 'WP-3', status: 'failed', created_at: '2026-06-02T11:00:00Z', updated_at: '2026-06-02T11:30:00Z', error: 'OOM killed', result: null },
]

vi.mock('../../hooks/useControlState', () => ({
  useControlState: () => ({
    state: mockControlState,
    loading: !mockControlState,
    error: null,
    refetch: mockRefetchState,
  }),
}))

vi.mock('../../hooks/useControlUnits', () => ({
  useControlUnits: (_status?: string) => ({
    units: mockControlUnits,
    loading: false,
    error: null,
    refetch: mockRefetchUnits,
  }),
}))

vi.mock('../../hooks/useControlMode', () => ({
  useControlMode: (onSuccess?: () => void) => ({
    setMode: vi.fn(async (mode: string) => {
      if (mockControlState) {
        mockControlState = { ...mockControlState, mode: mode as 'continuous' | 'tick' | 'stopped' }
      }
      onSuccess?.()
    }),
    setCadence: vi.fn(async (cadence_seconds: number) => {
      if (mockControlState) {
        mockControlState = { ...mockControlState, cadence_seconds }
      }
      onSuccess?.()
    }),
    loading: false,
    error: null,
  }),
}))

describe('ControlView', () => {
  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch')
    mockControlState = {
      mode: 'continuous',
      cadence_seconds: 30,
      queue_counts: { queued: 2, running: 1, done: 5, failed: 1 },
      updated_at: '2026-06-02T12:00:00Z',
    }
    mockControlUnits = [
      { id: 'u1', wp_ref: 'WP-1', status: 'queued', created_at: '2026-06-02T12:00:00Z', updated_at: '2026-06-02T12:00:00Z', error: null, result: null },
      { id: 'u2', wp_ref: 'WP-2', status: 'running', created_at: '2026-06-02T12:00:00Z', updated_at: '2026-06-02T12:00:00Z', error: null, result: null },
      { id: 'u3', wp_ref: 'WP-3', status: 'failed', created_at: '2026-06-02T11:00:00Z', updated_at: '2026-06-02T11:30:00Z', error: 'OOM killed', result: null },
    ]
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders heading and queue count cards', () => {
    render(<ControlView />)

    expect(screen.getByRole('heading', { name: 'Control', level: 2 })).toBeInTheDocument()
    // Count card labels
    expect(screen.getByText('Queued')).toBeInTheDocument()
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(screen.getByText('Failed')).toBeInTheDocument()
    // Count values - use getAllByText since "1" appears in both Running count and Failed count
    expect(screen.getByText('2')).toBeInTheDocument() // queued: unique
    expect(screen.getByText('5')).toBeInTheDocument() // done: unique
    // 1 appears twice (running:1, failed:1) — just check they exist
    const ones = screen.getAllByText('1')
    expect(ones.length).toBe(2)
  })

  it('renders current mode and cadence in Mode section', () => {
    render(<ControlView />)

    // The mode text appears in the "Current: continuous · cadence 30s" line
    expect(screen.getByText((_content, element) => (
      element?.tagName.toLowerCase() === 'p'
        && element.textContent?.includes('Current: continuous')
        && element.textContent?.includes('cadence 30s')
    ))).toBeInTheDocument()
  })

  it('renders work units with wp_ref text', () => {
    render(<ControlView />)

    expect(screen.getByText('WP-1')).toBeInTheDocument()
    expect(screen.getByText('WP-2')).toBeInTheDocument()
    expect(screen.getByText('WP-3')).toBeInTheDocument()
  })

  it('shows error text for failed units', () => {
    render(<ControlView />)

    expect(screen.getByText('OOM killed')).toBeInTheDocument()
  })

  it('shows requeue button for failed units', () => {
    render(<ControlView />)

    const requeueButtons = screen.getAllByText('Requeue')
    expect(requeueButtons.length).toBeGreaterThanOrEqual(1)
  })

  it('click STOP calls setMode with stopped', async () => {
    const user = userEvent.setup()
    render(<ControlView />)

    const stopButton = screen.getByText('STOP')
    expect(stopButton).toBeInTheDocument()

    await user.click(stopButton)

    // After clicking STOP, the mode display should update to "stopped"
    await waitFor(() => {
      expect(screen.getByText('stopped')).toBeInTheDocument()
    })
  })

  it('clicking tick mode button updates mode display', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<ControlView />)

    // The mode buttons are inside the ModeControls glass-card
    // "tick" appears as a mode button
    const tickButtons = screen.getAllByText('tick')
    // Find the one that's a button (inside mode switch)
    const tickButton = tickButtons.find((el) => el.closest('button') !== null)
    expect(tickButton).toBeTruthy()

    if (tickButton) {
      await user.click(tickButton)
    }
    rerender(<ControlView />)

    await waitFor(() => {
      // After click, the mode display should show tick (but there will be multiple)
      const allTick = screen.getAllByText('tick')
      expect(allTick.length).toBeGreaterThanOrEqual(2)
    })
  })

  it('failed unit requeue calls the requeue endpoint', async () => {
    const user = userEvent.setup()
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve({}),
    } as Response)

    render(<ControlView />)

    const requeueButtons = screen.getAllByText('Requeue')
    await user.click(requeueButtons[0])

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/control/units/'),
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('uses theme CSS variables', () => {
    const { container } = render(<ControlView />)

    const html = container.innerHTML

    // Check that theme CSS variables are used
    expect(html).toContain('var(--text-primary)')
    expect(html).toContain('var(--color-text-muted)')
    expect(html).toContain('var(--bg-elevated)')
    expect(html).toContain('var(--glass-border)')
    expect(html).toContain('var(--border-subtle)')
    expect(html).toContain('glass-card')
  })

  it('renders status filter pills in the queue section', () => {
    render(<ControlView />)

    // The filter pills show status names — "all" filter should be present
    expect(screen.getByText('Work Units')).toBeInTheDocument()
    // Check that all filter options exist as buttons
    const allFilterButtons = screen.getAllByText('all')
    expect(allFilterButtons.length).toBeGreaterThanOrEqual(1)
  })

  it('mode buttons include all three modes', () => {
    render(<ControlView />)

    const modeButtons = ['continuous', 'tick', 'stopped']
    for (const mode of modeButtons) {
      const matches = screen.getAllByText((content) => content.includes(mode))
      expect(matches.length).toBeGreaterThan(0)
    }
  })
})
