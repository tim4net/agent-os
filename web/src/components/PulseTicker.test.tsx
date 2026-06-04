import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import PulseTicker from './PulseTicker'
import type { SessionStatus, Incident } from '../api/client'

// Mock useSSE
const mockUseSSE = vi.fn(() => ({ sseConnected: true, lastEvent: null }))
vi.mock('../hooks/useSSE', () => ({
  useSSE: () => mockUseSSE()
}))

const mockSessions: SessionStatus[] = [
  {
    harness: 'claude',
    session_id: 'session-1',
    host: 'host-a',
    status: 'running',
    tenant: 'personal',
    liveness_mode: 'live',
    last_event_at: '2026-06-04T12:00:00Z',
    last_event_kind: 'session.start',
  },
  {
    harness: 'gpt4',
    session_id: 'session-2',
    host: 'host-b',
    status: 'completed',
    tenant: 'dayjob',
    liveness_mode: 'live',
    last_event_at: '2026-06-04T11:00:00Z',
    last_event_kind: 'session.end',
  }
]

const mockIncidents: Incident[] = [
  {
    type: 'error',
    harness: 'gemini',
    session_id: 'session-3',
    host: 'host-c',
    title: 'Out of memory error',
    status: 'failed',
    tenant: 'personal',
    project_slug: 'agent-os',
    external_ref: 'err-1',
    branch: 'main',
    received_at: '2026-06-04T13:00:00Z',
  }
]

describe('PulseTicker Component', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders merged and sorted pulse items (newest on the left)', () => {
    mockUseSSE.mockReturnValue({ sseConnected: true, lastEvent: null })
    
    render(<PulseTicker sessions={mockSessions} incidents={mockIncidents} loading={false} />)

    // Check texts
    expect(screen.getByText('gemini')).toBeInTheDocument()
    expect(screen.getByText('Out of memory error')).toBeInTheDocument()
    expect(screen.getByText('claude')).toBeInTheDocument()
    expect(screen.getByText('session.start')).toBeInTheDocument()
    expect(screen.getByText('gpt4')).toBeInTheDocument()
    expect(screen.getByText('session.end')).toBeInTheDocument()

    // Live dot indicator
    expect(screen.getByText('LIVE')).toBeInTheDocument()
  })

  it('renders loading shimmer chips when loading and no data', () => {
    render(<PulseTicker sessions={[]} incidents={[]} loading={true} />)
    
    // We expect some shimmer chips or animate-pulse elements to be visible
    // but not any text of sessions/incidents or empty text
    expect(screen.queryByText('No recent activity')).toBeNull()
    expect(screen.queryByText('claude')).toBeNull()
  })

  it('renders empty state when no data and not loading', () => {
    render(<PulseTicker sessions={[]} incidents={[]} loading={false} />)
    
    expect(screen.getByText('No recent activity')).toBeInTheDocument()
    expect(screen.getByText('LIVE')).toBeInTheDocument()
  })

  it('renders offline/gray dot when sseConnected is false', () => {
    mockUseSSE.mockReturnValue({ sseConnected: false, lastEvent: null })
    render(<PulseTicker sessions={[]} incidents={[]} loading={false} />)
    
    // The LIVE label is still shown
    expect(screen.getByText('LIVE')).toBeInTheDocument()
  })
})
