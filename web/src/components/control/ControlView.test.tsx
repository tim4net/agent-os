import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

import userEvent from '@testing-library/user-event'
import { ControlView } from './ControlView'

// --- Mock hooks ---
const mockRefetchState = vi.fn()
const mockRefetchUnits = vi.fn()

/**
 * Mock state uses real WP-O2 queue_counts keys: queued, in_flight, done, failed.
 * NOT the fabricated 'running' key (F2).
 */
let mockControlState: {
  mode: 'continuous' | 'tick' | 'stopped'
  cadence_seconds: number
  queue_counts: Record<string, number>
  updated_at: string
} | null = {
  mode: 'continuous',
  cadence_seconds: 30,
  queue_counts: { queued: 2, in_flight: 1, done: 5, failed: 1 },
  updated_at: '2026-06-02T12:00:00Z',
}

/**
 * Mock units use real WP-O2 WorkUnitResponse shape:
 *   id: number, no updated_at, no result, uses claimed_at/completed_at.
 */
let mockControlUnits: {
  id: number
  wp_ref: string
  status: 'queued' | 'in_flight' | 'done' | 'failed'
  created_at: string
  claimed_at: string | null
  completed_at: string | null
  error: string | null
}[] = [
  { id: 1, wp_ref: 'WP-1', status: 'queued', created_at: '2026-06-02T12:00:00Z', claimed_at: null, completed_at: null, error: null },
  { id: 2, wp_ref: 'WP-2', status: 'in_flight', created_at: '2026-06-02T11:50:00Z', claimed_at: '2026-06-02T11:51:00Z', completed_at: null, error: null },
  { id: 3, wp_ref: 'WP-3', status: 'failed', created_at: '2026-06-02T11:00:00Z', claimed_at: null, completed_at: null, error: 'OOM killed' },
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

const mockSetMode = vi.fn<(mode: string, cadence?: number) => Promise<void>>()

vi.mock('../../hooks/useControlMode', () => ({
  useControlMode: (_onSuccess?: () => void) => ({
    setMode: mockSetMode,
    loading: false,
    error: null,
  }),
}))

describe('ControlView', () => {
  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch')
    mockSetMode.mockClear()
    mockControlState = {
      mode: 'continuous',
      cadence_seconds: 30,
      queue_counts: { queued: 2, in_flight: 1, done: 5, failed: 1 },
      updated_at: '2026-06-02T12:00:00Z',
    }
    mockControlUnits = [
      { id: 1, wp_ref: 'WP-1', status: 'queued', created_at: '2026-06-02T12:00:00Z', claimed_at: null, completed_at: null, error: null },
      { id: 2, wp_ref: 'WP-2', status: 'in_flight', created_at: '2026-06-02T11:50:00Z', claimed_at: '2026-06-02T11:51:00Z', completed_at: null, error: null },
      { id: 3, wp_ref: 'WP-3', status: 'failed', created_at: '2026-06-02T11:00:00Z', claimed_at: null, completed_at: null, error: 'OOM killed' },
    ]
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders heading and queue count cards', () => {
    render(<ControlView />)

    expect(screen.getByRole('heading', { name: 'Control', level: 2 })).toBeInTheDocument()
    // Count card labels appear in both the card and the filter pills — use getAllByText
    expect(screen.getAllByText('Queued').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('In flight').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Done').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Failed').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('2')).toBeInTheDocument() // queued
    expect(screen.getByText('5')).toBeInTheDocument() // done
    const ones = screen.getAllByText('1')
    expect(ones.length).toBeGreaterThanOrEqual(2) // in_flight:1, failed:1
  })

  it('renders current mode and cadence in Mode section', () => {
    render(<ControlView />)

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

  it('shows requeue button only for failed units (not done)', () => {
    // WP-3 is failed → should have Requeue
    const unitRow = { ...mockControlUnits[2], status: 'failed' as const }
    mockControlUnits = [unitRow]
    render(<ControlView />)

    const requeueButtons = screen.getAllByText('Requeue')
    expect(requeueButtons.length).toBe(1)
  })

  it('does NOT show requeue for done units', () => {
    mockControlUnits = [
      { id: 4, wp_ref: 'WP-4', status: 'done', created_at: '2026-06-02T12:00:00Z', claimed_at: '2026-06-02T12:01:00Z', completed_at: '2026-06-02T12:05:00Z', error: null },
    ]
    render(<ControlView />)

    expect(screen.queryByText('Requeue')).not.toBeInTheDocument()
  })

  it('click STOP calls setMode with stopped', async () => {
    const user = userEvent.setup()
    render(<ControlView />)

    const stopButton = screen.getByText('STOP')
    expect(stopButton).toBeInTheDocument()

    await user.click(stopButton)

    // F2 fix: assert the kill-switch POST was actually wired (not tautological).
    // mockSetMode is the spy on useControlMode's setMode.
    expect(mockSetMode).toHaveBeenCalledWith('stopped')
  })

  it('clicking tick mode button calls setMode with tick', async () => {
    const user = userEvent.setup()
    render(<ControlView />)

    const tickButtons = screen.getAllByText('tick')
    const tickButton = tickButtons.find((el) => el.closest('button') !== null)
    expect(tickButton).toBeTruthy()

    if (tickButton) {
      await user.click(tickButton)
    }

    expect(mockSetMode).toHaveBeenCalledWith('tick')
  })

  it('failed unit requeue calls the requeue endpoint with numeric id', async () => {
    const user = userEvent.setup()
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve({}),
    } as Response)

    render(<ControlView />)

    const requeueButtons = screen.getAllByText('Requeue')
    await user.click(requeueButtons[0])

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/control/units/3/requeue'),
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('uses theme CSS variables', () => {
    const { container } = render(<ControlView />)

    const html = container.innerHTML

    expect(html).toContain('var(--text-primary)')
    expect(html).toContain('var(--color-text-muted)')
    expect(html).toContain('var(--bg-elevated)')
    expect(html).toContain('var(--glass-border)')
    expect(html).toContain('var(--border-subtle)')
    expect(html).toContain('glass-card')
  })

  it('renders status filter pills in the queue section', () => {
    render(<ControlView />)

    expect(screen.getByText('Work Units')).toBeInTheDocument()
    const allFilterButtons = screen.getAllByText('All')
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

  it('requeue error is surfaced to the user', async () => {
    const user = userEvent.setup()
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: false, status: 404, statusText: 'Not Found',
      text: () => Promise.resolve('unit not found or not in a requeueable state (failed only)'),
    } as unknown as Response)

    render(<ControlView />)

    const requeueButtons = screen.getAllByText('Requeue')
    await user.click(requeueButtons[0])

    await waitFor(() => {
      expect(screen.getByText(/Requeue failed/)).toBeInTheDocument()
    })
  })

  it('cadence Set button calls setMode with current mode and cadence_seconds', async () => {
    const user = userEvent.setup()
    render(<ControlView />)

    const cadenceInput = screen.getByRole('spinbutton')
    await user.clear(cadenceInput)
    await user.type(cadenceInput, '60')

    const setButton = screen.getByRole('button', { name: 'Set' })
    await user.click(setButton)

    // F3: prove the cadence input → handleCadenceSubmit → setMode wiring.
    // Current mode is 'continuous' (mockControlState default), cadence 60.
    expect(mockSetMode).toHaveBeenCalledWith('continuous', 60)
  })
})
