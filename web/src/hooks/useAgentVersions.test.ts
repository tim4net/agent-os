import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useAgentVersions } from './useAgentVersions'

describe('useAgentVersions', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('resolves a known version per agent', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ current: '1.85.1', source: 'openapi', checked_at: '2026-06-06T12:00:00Z' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const { result } = renderHook(() => useAgentVersions(['a1']))

    await waitFor(() => expect(result.current.versions['a1']?.loading).toBe(false))
    expect(result.current.versions['a1'].version?.current).toBe('1.85.1')
    expect(result.current.versions['a1'].version?.source).toBe('openapi')
    expect(result.current.versions['a1'].error).toBeNull()
  })

  it('treats a 200 unknown as a valid (non-error) outcome', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ current: '', source: 'unknown', checked_at: '2026-06-06T12:00:00Z' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const { result } = renderHook(() => useAgentVersions(['roux']))

    await waitFor(() => expect(result.current.versions['roux']?.loading).toBe(false))
    expect(result.current.versions['roux'].version?.source).toBe('unknown')
    expect(result.current.versions['roux'].error).toBeNull()
  })

  it('records a transport/HTTP failure as an error, not a version', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('not found', { status: 404, statusText: 'Not Found' }),
    )

    const { result } = renderHook(() => useAgentVersions(['ghost']))

    await waitFor(() => expect(result.current.versions['ghost']?.loading).toBe(false))
    expect(result.current.versions['ghost'].version).toBeNull()
    expect(result.current.versions['ghost'].error).toContain('404')
  })

  it('probes each agent independently (one failure does not sink the others)', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/agents/ok/')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ current: '2.0.0', source: 'http', checked_at: '2026-06-06T12:00:00Z' }),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          ),
        )
      }
      return Promise.resolve(new Response('boom', { status: 500, statusText: 'Server Error' }))
    })

    const { result } = renderHook(() => useAgentVersions(['ok', 'bad']))

    await waitFor(() => {
      expect(result.current.versions['ok']?.loading).toBe(false)
      expect(result.current.versions['bad']?.loading).toBe(false)
    })
    expect(result.current.versions['ok'].version?.current).toBe('2.0.0')
    expect(result.current.versions['bad'].error).toContain('500')
  })
})
