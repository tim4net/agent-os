import { useEffect, useState } from 'react'
import { useSSE } from '../hooks/useSSE'

interface HealthStatus {
  status: string
  uptime?: number
  version?: string
}

export function StatusFooter() {
  const [health, setHealth] = useState<HealthStatus | null>(null)
  const [sseStatus, setSseStatus] = useState<'connected' | 'disconnected'>('disconnected')
  const [lastEventTime, setLastEventTime] = useState<string | null>(null)
  const { lastEvent } = useSSE()

  useEffect(() => {
    fetch('/api/health')
      .then((r) => r.json())
      .then((data) => setHealth(data))
      .catch(() => setHealth(null))

    const interval = setInterval(() => {
      fetch('/api/health')
        .then((r) => r.json())
        .then((data) => setHealth(data))
        .catch(() => setHealth(null))
    }, 30000)

    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    if (lastEvent) {
      setSseStatus('connected')
      setLastEventTime(new Date().toISOString())
    }
  }, [lastEvent])

  // Detect disconnection if no event for 15s
  useEffect(() => {
    if (!lastEventTime) {
      const timer = setTimeout(() => setSseStatus('disconnected'), 5000)
      return () => clearTimeout(timer)
    }
    const timer = setTimeout(() => setSseStatus('disconnected'), 15000)
    return () => clearTimeout(timer)
  }, [lastEventTime])

  function relativeTime(dateStr: string): string {
    const diff = Date.now() - new Date(dateStr).getTime()
    const seconds = Math.floor(diff / 1000)
    if (seconds < 60) return `${seconds}s ago`
    const minutes = Math.floor(seconds / 60)
    return `${minutes}m ago`
  }

  const healthColor = health?.status === 'ok' ? 'text-green-400' : 'text-red-400'
  const sseColor = sseStatus === 'connected' ? 'text-green-400' : 'text-yellow-400'

  return (
    <footer className="flex items-center gap-4 px-4 py-1.5 border-t border-gray-800 bg-gray-900 text-xs text-gray-500 flex-shrink-0">
      <span>
        Backend: <span className={healthColor}>{health?.status ?? 'unknown'}</span>
      </span>
      <span>
        SSE: <span className={sseColor}>{sseStatus}</span>
      </span>
      {lastEventTime && (
        <span>
          Last event: {relativeTime(lastEventTime)}
        </span>
      )}
      {health?.version && (
        <span className="ml-auto">v{health.version}</span>
      )}
    </footer>
  )
}
