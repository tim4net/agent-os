import { useState, useCallback } from 'react'

interface UseControlMode {
  setMode: (mode: 'continuous' | 'tick' | 'stopped', cadence_seconds?: number) => Promise<void>
  loading: boolean
  error: string | null
}

export function useControlMode(onSuccess?: () => void): UseControlMode {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const setMode = useCallback(async (mode: 'continuous' | 'tick' | 'stopped', cadence_seconds?: number) => {
    setLoading(true)
    setError(null)
    try {
      const body: Record<string, unknown> = { mode }
      if (cadence_seconds !== undefined) body.cadence_seconds = cadence_seconds
      const res = await fetch('/api/control/mode', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) throw new Error(`API error ${res.status}: ${res.statusText}`)
      onSuccess?.()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to set mode')
    } finally {
      setLoading(false)
    }
  }, [onSuccess])

  return { setMode, loading, error }
}
