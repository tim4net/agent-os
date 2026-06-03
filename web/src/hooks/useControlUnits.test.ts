import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useControlUnits } from './useControlUnits'

/**
 * Hook tests that mock the REAL WP-O2 /api/control/units response envelope:
 *   { units: [...], count, limit, offset }
 * NOT a bare array — the wrong mock shape that previously masked F1.
 */

const BASE_UNIT = {
  id: 42,
  wp_ref: 'WP-A',
  status: 'queued',
  created_at: '2026-06-02T12:00:00Z',
  claimed_at: null,
  completed_at: null,
  error: null,
}

function makeEnvelope(units: unknown[]) {
  return { units, count: units.length, limit: 50, offset: 0 }
}

describe('useControlUnits', () => {
  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('parses the UnitListResponse envelope, not a bare array', async () => {
    const mockUnits = [{ ...BASE_UNIT, id: 1, wp_ref: 'WP-1' }]
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve(makeEnvelope(mockUnits)),
    } as Response)

    const { result } = renderHook(() => useControlUnits())

    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.units).toHaveLength(1)
    expect(result.current.units[0].id).toBe(1)
    expect(result.current.units[0].wp_ref).toBe('WP-1')
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/control/units')
  })

  it('passes status filter as query param', async () => {
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve(makeEnvelope([])),
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
    const units1 = [{ ...BASE_UNIT, id: 1, status: 'in_flight', claimed_at: '2026-06-02T12:01:00Z' }]
    const units2 = [
      { ...BASE_UNIT, id: 1, status: 'done', completed_at: '2026-06-02T12:05:00Z' },
      { ...BASE_UNIT, id: 2, wp_ref: 'WP-2', status: 'queued' },
    ]

    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce({
        ok: true, status: 200, statusText: 'OK',
        json: () => Promise.resolve(makeEnvelope(units1)),
      } as Response)
      .mockResolvedValueOnce({
        ok: true, status: 200, statusText: 'OK',
        json: () => Promise.resolve(makeEnvelope(units2)),
      } as Response)

    const { result } = renderHook(() => useControlUnits())

    await waitFor(() => expect(result.current.units).toHaveLength(1))

    result.current.refetch()

    await waitFor(() => expect(result.current.units).toHaveLength(2))
  })
})
