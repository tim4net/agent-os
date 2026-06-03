import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useControlMode } from './useControlMode'

/**
 * Hook tests for useControlMode — no setCadence (deleted in F3 fix).
 * Cadence is sent via setMode(mode, cadence_seconds) to POST /api/control/mode.
 */

describe('useControlMode', () => {
  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls POST /api/control/mode with correct body', async () => {
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve({}),
    } as Response)

    const onSuccess = vi.fn()
    const { result } = renderHook(() => useControlMode(onSuccess))

    await act(async () => {
      await result.current.setMode('stopped')
    })

    expect(globalThis.fetch).toHaveBeenCalledWith('/api/control/mode', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode: 'stopped' }),
    })
    expect(onSuccess).toHaveBeenCalled()
    expect(result.current.error).toBeNull()
  })

  it('calls POST /api/control/mode with cadence_seconds', async () => {
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: true, status: 200, statusText: 'OK',
      json: () => Promise.resolve({}),
    } as Response)

    const { result } = renderHook(() => useControlMode())

    await act(async () => {
      await result.current.setMode('tick', 60)
    })

    expect(globalThis.fetch).toHaveBeenCalledWith('/api/control/mode', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode: 'tick', cadence_seconds: 60 }),
    })
  })

  it('handles error on setMode', async () => {
    vi.mocked(globalThis.fetch).mockResolvedValueOnce({
      ok: false, status: 500, statusText: 'Internal Server Error',
      json: () => Promise.resolve({}),
    } as Response)

    const { result } = renderHook(() => useControlMode())

    await act(async () => {
      await result.current.setMode('continuous')
    })

    expect(result.current.error).toBe('API error 500: Internal Server Error')
  })

  it('does NOT expose setCadence (deleted — cadence goes through setMode)', () => {
    const { result } = renderHook(() => useControlMode())
    // setCadence should not exist on the returned object
    expect('setCadence' in result.current).toBe(false)
  })
})
