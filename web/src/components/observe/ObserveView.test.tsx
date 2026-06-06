import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ObserveView } from './ObserveView'

// Mock the data hooks so the view renders deterministically with no work units
// (we are asserting the tenant switcher labels, not the feed).
vi.mock('../../hooks/useWorkUnits', () => ({
  useWorkUnits: () => ({ units: [], loading: false, error: null, refresh: vi.fn() }),
  livenessOf: () => 'done',
}))
vi.mock('../../hooks/useSSE', () => ({
  useSSE: () => ({ lastEvent: null }),
}))

describe('ObserveView tenant switcher', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the walled "dayjob" tenant option as "Work", never raw', () => {
    render(<ObserveView />)

    // The internal key must be displayed as "Work" ...
    expect(screen.getByRole('option', { name: 'Work' })).toBeInTheDocument()
    // ... and the raw "dayjob" string must never appear anywhere in the UI.
    expect(screen.queryByText('dayjob')).not.toBeInTheDocument()
  })

  it('renders the "personal" tenant option as "Personal"', () => {
    render(<ObserveView />)
    expect(screen.getByRole('option', { name: 'Personal' })).toBeInTheDocument()
  })

  it('keeps the option value as the internal key for server scoping', () => {
    render(<ObserveView />)
    // The visible label is "Work" but the underlying value stays "dayjob" so
    // server-side tenant scoping (ADR-002) still receives the real key.
    const workOption = screen.getByRole('option', { name: 'Work' }) as HTMLOptionElement
    expect(workOption.value).toBe('dayjob')
  })
})
