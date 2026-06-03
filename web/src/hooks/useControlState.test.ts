import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useControlState, type ControlState } from './useControlState'

/**
 * Hook tests using real WP-O2 queue_counts keys: queued, in_flight, done, failed.
 * NOT the fabricated 'running' key (F2).
 */

describe('useControlState', () => {
  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('fetches control state on mount', async () => {
    const mockState = {
      mode: 'continuous',
      cadence_seconds: 30,
      queue_counts: { queued: 1, in_flight: 2, done: 5, failed: 0 },
      updated_at: '2026-06-02T12:00:00Z',
    }
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: () => Promise.resolve(mockState),
    } as Response)

    const { result } = renderHook(() => useControlState())

    expect(result.current.loading).toBe(true)

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.state).toEqual(mockState)
    expect(result.current.error).toBeNull()
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/control/state', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })

  it('handles fetch error', async () => {
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(new Error('Network error'))

    const { result } = renderHook(() => useControlState())

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.state).toBeNull()
    expect(result.current.error).toBe('Network error')
  })

  it('handles non-ok response', async () => {
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: () => Promise.resolve({}),
    } as Response)

    const { result } = renderHook(() => useControlState())

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error).toBe('API error 500: Internal Server Error')
  })

  // Regression test: older /state response resolving after a newer one
  // must NOT overwrite the newer state. Without the AbortController + requestId
  // guard, the older response would clobber the newer state.
  it('discards stale /state responses (older request resolves after newer)', async () => {
    const newState: ControlState = {
      mode: 'stopped',
      cadence_seconds: 30,
      queue_counts: { queued: 1, in_flight: 0, done: 0, failed: 0 },
      updated_at: '2026-06-03T10:00:00Z',
    }
    const oldState: ControlState = {
      mode: 'continuous',
      cadence_seconds: 60,
      queue_counts: { queued: 2, in_flight: 3, done: 4, failed: 1 },
      updated_at: '2026-06-03T09:59:00Z',
    }

    // First call (initial mount) — returns quickly
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve(newState),
    } as Response)

    const { result } = renderHook(() => useControlState())

    // Wait for the initial state to land
    await waitFor(() => {
      expect(result.current.state?.mode).toBe('stopped')
    })

    // Second call (refetch) — return a deferred promise so we can
    // control resolution order
    let resolveOld!: (value: unknown) => void
    const oldPromise = new Promise((resolve) => { resolveOld = resolve })
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => oldPromise,
    } as Response)

    // Third call (another refetch, e.g. from interval or onModeChanged) — returns quickly
    const newerState: ControlState = {
      mode: 'stopped',
      cadence_seconds: 30,
      queue_counts: { queued: 1, in_flight: 0, done: 0, failed: 0 },
      updated_at: '2026-06-03T10:01:00Z',
    }
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve(newerState),
    } as Response)

    // Trigger first refetch (the slow one)
    result.current.refetch()

    // Trigger second refetch (the fast one)
    result.current.refetch()

    // Wait for the newer response to land
    await waitFor(() => {
      expect(result.current.state?.updated_at).toBe('2026-06-03T10:01:00Z')
    })

    // Now resolve the older (stale) response — it must NOT overwrite the newer state
    resolveOld(oldState)
    await waitFor(() => {
      expect(result.current.state?.mode).toBe('stopped')
    })

    // The stale mode ('continuous') must never appear
    expect(result.current.state?.mode).toBe('stopped')
  })

  it('refetches on refetch() call', async () => {
    const mockState1 = {
      mode: 'stopped' as const,
      cadence_seconds: 30,
      queue_counts: {},
      updated_at: '2026-06-02T12:00:00Z',
    }
    const mockState2 = {
      mode: 'continuous' as const,
      cadence_seconds: 30,
      queue_counts: {},
      updated_at: '2026-06-02T12:01:00Z',
    }

    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce({
        ok: true, status: 200, statusText: 'OK',
        json: () => Promise.resolve(mockState1),
      } as Response)
      .mockResolvedValueOnce({
        ok: true, status: 200, statusText: 'OK',
        json: () => Promise.resolve(mockState2),
      } as Response)

    const { result } = renderHook(() => useControlState())

    await waitFor(() => {
      expect(result.current.state?.mode).toBe('stopped')
    })

    result.current.refetch()

    await waitFor(() => {
      expect(result.current.state?.mode).toBe('continuous')
    })
  })
})
