import { useEffect, useState } from 'react'
import { checkHealth } from '../api/client'

interface StatusFooterProps {
  sseConnected: boolean
}

export function StatusFooter({ sseConnected }: StatusFooterProps) {
  const [backendStatus, setBackendStatus] = useState<'ok' | 'error' | 'checking'>('checking')
  const [lastCheck, setLastCheck] = useState<number>(0)

  useEffect(() => {
    async function check() {
      try {
        const ok = await checkHealth()
        setBackendStatus(ok ? 'ok' : 'error')
        setLastCheck(Date.now())
      } catch {
        setBackendStatus('error')
        setLastCheck(Date.now())
      }
    }

    // Check immediately, then every 30s
    check()
    const interval = setInterval(check, 30_000)
    return () => clearInterval(interval)
  }, [])

  const healthColor = backendStatus === 'ok' ? 'text-green-400' : backendStatus === 'error' ? 'text-red-400' : 'text-yellow-400'
  const sseColor = sseConnected ? 'text-green-400' : 'text-yellow-400'

  const ago = lastCheck > 0 ? `${Math.round((Date.now() - lastCheck) / 1000)}s ago` : ''

  return (
    <footer className="flex items-center gap-4 px-4 py-1.5 border-t border-gray-800 bg-gray-900 text-xs text-gray-500 flex-shrink-0">
      <span>
        Backend: <span className={healthColor}>{backendStatus}</span>
        {ago && <span className="ml-1 text-gray-600">({ago})</span>}
      </span>
      <span>
        SSE: <span className={sseColor}>{sseConnected ? 'connected' : 'connecting...'}</span>
      </span>
    </footer>
  )
}
