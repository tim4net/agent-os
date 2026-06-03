import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useControlState } from './useControlState'

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
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/control/state')
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
