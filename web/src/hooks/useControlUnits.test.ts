import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useControlUnits } from './useControlUnits'

describe('useControlUnits', () => {
  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('fetches units without status filter', async () => {
    const mockUnits = [
      { id: 'u1', wp_ref: 'WP-1', status: 'queued', created_at: '2026-06-02T12:00:00Z', updated_at: '2026-06-02T12:00:00Z', error: null, result: null },
    ]
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve(mockUnits),
    } as Response)

    const { result } = renderHook(() => useControlUnits())

    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.units).toEqual(mockUnits)
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/control/units')
  })

  it('fetches units with status filter', async () => {
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve([]),
    } as Response)

    const { result } = renderHook(() => useControlUnits('failed'))

    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(globalThis.fetch).toHaveBeenCalledWith('/api/control/units?status=failed')
  })

  it('handles fetch error', async () => {
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(new Error('Network error'))

    const { result } = renderHook(() => useControlUnits())

    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.error).toBe('Network error')
    expect(result.current.units).toEqual([])
  })

  it('refetch refreshes the data', async () => {
    const units1 = [
      { id: 'u1', wp_ref: 'WP-1', status: 'running', created_at: '2026-06-02T12:00:00Z', updated_at: '2026-06-02T12:00:00Z', error: null, result: null },
    ]
    const units2 = [
      { id: 'u1', wp_ref: 'WP-1', status: 'done', created_at: '2026-06-02T12:00:00Z', updated_at: '2026-06-02T12:01:00Z', error: null, result: null },
      { id: 'u2', wp_ref: 'WP-2', status: 'queued', created_at: '2026-06-02T12:01:00Z', updated_at: '2026-06-02T12:01:00Z', error: null, result: null },
    ]

    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce({
        ok: true, status: 200, statusText: 'OK',
        json: () => Promise.resolve(units1),
      } as Response)
      .mockResolvedValueOnce({
        ok: true, status: 200, statusText: 'OK',
        json: () => Promise.resolve(units2),
      } as Response)

    const { result } = renderHook(() => useControlUnits())

    await waitFor(() => expect(result.current.units).toHaveLength(1))

    result.current.refetch()

    await waitFor(() => expect(result.current.units).toHaveLength(2))
  })
})
