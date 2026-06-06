import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

  // AC4 regression guard: switching the tenant to the walled "dayjob" key must
  // render the EMPTY-STATE label as "Work", never the raw key. The data hook is
  // mocked to return zero units, so selecting "dayjob" forces the empty-state
  // path (ObserveView.tsx:114) to execute with tenant === 'dayjob' — the branch
  // that previously had no test (the credited switcher test never changed the
  // tenant, so this path never ran). Mutation-checked: reverting line 114 to
  // raw {tenant} turns THIS test RED.
  it('renders the empty-state tenant label as "Work" (not raw "dayjob") after switching', async () => {
    const user = userEvent.setup()
    render(<ObserveView />)

    // Fire the switcher to the walled tenant.
    const switcher = screen.getByRole('combobox')
    await user.selectOptions(switcher, 'dayjob')

    // The empty-state sentence must show the display label, never the raw key.
    const emptyState = await screen.findByText(/No work events for tenant/i)
    expect(within(emptyState).getByText('Work')).toBeInTheDocument()
    expect(emptyState.textContent).toContain('No work events for tenant Work yet')
    expect(emptyState.textContent).not.toContain('dayjob')
  })
})
