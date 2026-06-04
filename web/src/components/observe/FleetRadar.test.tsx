import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { FleetRadar, getAngleFromSessionId } from './FleetRadar'
import { getFleet } from '../../api/client'

// Mock the client functions
vi.mock('../../api/client', () => ({
  getFleet: vi.fn(),
}))

describe('getAngleFromSessionId', () => {
  it('returns a deterministic angle in radians for any given session_id', () => {
    const angle1 = getAngleFromSessionId('session-abc')
    const angle2 = getAngleFromSessionId('session-abc')
    const angle3 = getAngleFromSessionId('session-xyz')

    expect(angle1).toBe(angle2)
    expect(angle1).toBeGreaterThanOrEqual(0)
    expect(angle1).toBeLessThanOrEqual(2 * Math.PI)
    expect(angle3).toBeGreaterThanOrEqual(0)
    expect(angle3).toBeLessThanOrEqual(2 * Math.PI)
    expect(angle1).not.toBe(angle3)
  })
})

describe('FleetRadar Component', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders acquiring signal loading state initially', async () => {
    let resolvePromise: (value: any) => void = () => {}
    const promise = new Promise((resolve) => {
      resolvePromise = resolve
    })
    vi.mocked(getFleet).mockReturnValue(promise as any)

    render(<FleetRadar tenant="personal" />)

    expect(screen.getByText('Acquiring signal…')).toBeInTheDocument()
    expect(getFleet).toHaveBeenCalledWith('personal')

    // Clean up/resolve the promise to prevent hanging
    await act(async () => {
      resolvePromise({ sessions: [], total: 0 })
    })
  })

  it('renders empty state grid when there are zero sessions', async () => {
    vi.mocked(getFleet).mockResolvedValue({ sessions: [], total: 0 })

    render(<FleetRadar tenant="dayjob" />)

    await waitFor(() => {
      expect(screen.queryByText('Acquiring signal…')).not.toBeInTheDocument()
    })

    // Grid details should render
    expect(screen.getByText('No active signal')).toBeInTheDocument()
    expect(screen.getByText(/No sessions found for tenant/i)).toBeInTheDocument()
    // Internal 'dayjob' key must be displayed as 'Work', never leaked raw.
    expect(screen.getAllByText('Work').length).toBeGreaterThan(0)
    expect(screen.queryByText('dayjob')).not.toBeInTheDocument()
    expect(screen.getAllByText('0').length).toBe(5) // Center label total + 4 legend items
  })

  it('renders active sessions on the radar scope', async () => {
    const mockSessions = [
      {
        harness: 'test-harness',
        session_id: 'session-running-123',
        host: 'host-a',
        status: 'running',
        tenant: 'personal',
        liveness_mode: 'live',
        last_event_at: new Date().toISOString(),
        last_event_kind: 'agent_start',
      },
      {
        harness: 'test-harness',
        session_id: 'session-stale-456',
        host: 'host-b',
        status: 'stale',
        tenant: 'personal',
        liveness_mode: 'live',
        last_event_at: new Date(Date.now() - 4 * 60 * 60 * 1000).toISOString(), // 4 hours ago
        last_event_kind: 'heartbeat',
      },
    ]

    vi.mocked(getFleet).mockResolvedValue({ sessions: mockSessions, total: 2 })

    render(<FleetRadar tenant="personal" />)

    await waitFor(() => {
      expect(screen.queryByText('Acquiring signal…')).not.toBeInTheDocument()
    })

    // Center total should be 2
    expect(screen.getByText('2')).toBeInTheDocument()

    // Legend status count checks
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByText('Stale')).toBeInTheDocument()
  })
})
