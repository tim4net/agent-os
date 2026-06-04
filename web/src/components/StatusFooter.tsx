import { useEffect, useMemo, useState } from 'react'
import { checkHealth } from '../api/client'

interface StatusFooterProps {
  sseConnected: boolean
  agents?: { name: string; status: string }[]
}

export function StatusFooter({ sseConnected, agents = [] }: StatusFooterProps) {
  const [backendStatus, setBackendStatus] = useState<'ok' | 'error' | 'checking'>('checking')
  const [lastCheck, setLastCheck] = useState<number>(0)
  // A 1s clock so the "Xs ago" label updates live without reading Date.now()
  // in the render body (which the react-hooks/purity rule forbids).
  const [now, setNow] = useState<number>(() => Date.now())

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

  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1_000)
    return () => clearInterval(tick)
  }, [])

  const backendOk = backendStatus === 'ok'
  const backendChecking = backendStatus === 'checking'

  // Elapsed since the last health check, derived from the ticking clock (no
  // Date.now() in render — `now` is the impure read, captured in state).
  const ago = lastCheck > 0 ? `${Math.max(0, Math.round((now - lastCheck) / 1000))}s ago` : ''

  // Backend now handles visibility filtering (agents.visible column)
  const { onlineAgents, totalAgents } = useMemo(() => {
    const userAgents = agents.filter(a => a.status)
    return {
      onlineAgents: userAgents.filter(a => a.status === 'online').length,
      totalAgents: userAgents.length,
    }
  }, [agents])

  return (
    <footer className="flex items-center gap-4 px-5 py-1 flex-shrink-0 bg-[var(--bg-base)]">
      {/* Backend status */}
      <div className="flex items-center gap-1.5">
        <span
          className={`inline-block w-1.5 h-1.5 rounded-full ${
            backendChecking
              ? 'bg-yellow-500/60 animate-pulse'
              : backendOk
                ? 'bg-emerald-400/70'
                : 'bg-red-400/70'
          }`}
        />
        <span className="text-[10px] text-[var(--color-text-muted)]/60 leading-none">
          API{ago && <span className="ml-0.5 opacity-50"> {ago}</span>}
        </span>
      </div>

      {/* Separator */}
      <span className="text-[var(--color-text-muted)]/20 text-[8px]">|</span>

      {/* SSE status */}
      <div className="flex items-center gap-1.5">
        <span
          className={`inline-block w-1.5 h-1.5 rounded-full ${
            sseConnected
              ? 'bg-emerald-400/70'
              : 'bg-yellow-500/60 animate-pulse'
          }`}
        />
        <span className="text-[10px] text-[var(--color-text-muted)]/60 leading-none">
          SSE
        </span>
      </div>

      {/* Agent status summary */}
      {totalAgents > 0 && (
        <>
          <span className="text-[var(--color-text-muted)]/20 text-[8px]">|</span>
          <span className="text-[10px] text-[var(--color-text-muted)]/60 leading-none">
            {onlineAgents}/{totalAgents} agents
          </span>
        </>
      )}
    </footer>
  )
}
