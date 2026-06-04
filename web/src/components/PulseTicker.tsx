import { useMemo } from 'react'
import { useSSE } from '../hooks/useSSE'
import { 
  timeAgo, 
  getTenantStyles, 
  getSessionStatusStyles, 
  getIncidentStatusColor 
} from './mission-control-helpers'
import type { SessionStatus, Incident } from '../api/client'

export interface PulseItem {
  kind: 'session' | 'incident'
  id: string
  harness: string
  tenant: string
  label: string
  status: string
  ts: string
}

interface PulseTickerProps {
  sessions?: SessionStatus[]
  incidents?: Incident[]
  loading: boolean
}

function getIncidentDotClass(status: string): string {
  const colorClass = getIncidentStatusColor(status)
  if (colorClass.includes('text-red-400')) return 'bg-red-500'
  if (colorClass.includes('text-amber-400')) return 'bg-amber-500'
  return 'bg-blue-500'
}

export default function PulseTicker({ 
  sessions, 
  incidents, 
  loading 
}: PulseTickerProps) {
  const { sseConnected } = useSSE()

  // Normalize once so null (not just undefined) can never crash a .length / .forEach.
  const safeSessions = Array.isArray(sessions) ? sessions : []
  const safeIncidents = Array.isArray(incidents) ? incidents : []

  const pulseItems = useMemo(() => {
    const items: PulseItem[] = []
    const srcSessions = Array.isArray(sessions) ? sessions : []
    const srcIncidents = Array.isArray(incidents) ? incidents : []

    srcSessions.forEach((s, idx) => {
      if (!s) return
      items.push({
        kind: 'session',
        id: `session-${s.session_id || 'na'}-${s.last_event_at || 'na'}-${idx}`,
        harness: s.harness || 'unknown',
        tenant: s.tenant || 'system',
        label: s.last_event_kind || 'active',
        status: s.status || 'unknown',
        ts: s.last_event_at || '',
      })
    })

    srcIncidents.forEach((i, idx) => {
      if (!i) return
      items.push({
        kind: 'incident',
        id: `incident-${i.session_id || 'na'}-${i.external_ref || 'na'}-${i.received_at || 'na'}-${idx}`,
        harness: i.harness || 'unknown',
        tenant: i.tenant || 'system',
        label: i.title || 'incident',
        status: i.status || 'unknown',
        ts: i.received_at || '',
      })
    })

    return items
      .filter((item) => item.ts)
      .sort((a, b) => {
        const timeA = new Date(a.ts).getTime()
        const timeB = new Date(b.ts).getTime()
        return (isNaN(timeB) ? 0 : timeB) - (isNaN(timeA) ? 0 : timeA)
      })
      .slice(0, 12)
  }, [sessions, incidents])

  const showLoading = loading && safeSessions.length === 0 && safeIncidents.length === 0

  return (
    <div className="min-h-[44px] h-11 flex items-center border border-[var(--border-subtle)] rounded-lg bg-[var(--bg-surface)] px-3 overflow-hidden select-none">
      <style dangerouslySetInnerHTML={{ __html: `
        .no-scrollbar::-webkit-scrollbar {
          display: none;
        }
        .no-scrollbar {
          -ms-overflow-style: none;
          scrollbar-width: none;
        }
        @keyframes pulseEntrance {
          from {
            opacity: 0;
            transform: scale(0.92) translateX(-8px);
          }
          to {
            opacity: 1;
            transform: scale(1) translateX(0);
          }
        }
        @media (prefers-reduced-motion: no-preference) {
          .pulse-newest-item {
            animation: pulseEntrance 0.4s cubic-bezier(0.16, 1, 0.3, 1) forwards;
          }
        }
      `}} />
      
      {/* Live Dot Indicator */}
      <div className="flex items-center gap-1.5 shrink-0 border-r border-[var(--border-subtle)] pr-3 mr-3">
        <span className="text-[10px] font-bold uppercase tracking-wider text-[var(--text-secondary)]">LIVE</span>
        <span className="relative flex h-2 w-2">
          {sseConnected ? (
            <>
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
            </>
          ) : (
            <span className="relative inline-flex rounded-full h-2 w-2 bg-gray-600"></span>
          )}
        </span>
      </div>

      {/* Scrollable list */}
      <div className="flex items-center gap-2 overflow-x-auto flex-1 py-1 no-scrollbar">
        {showLoading ? (
          <>
            <div className="h-6 w-24 bg-white/5 border border-white/5 rounded-full animate-pulse" />
            <div className="h-6 w-32 bg-white/5 border border-white/5 rounded-full animate-pulse" />
            <div className="h-6 w-28 bg-white/5 border border-white/5 rounded-full animate-pulse" />
          </>
        ) : pulseItems.length === 0 ? (
          <span className="text-xs text-[var(--text-muted)] italic">No recent activity</span>
        ) : (
          pulseItems.map((item, index) => {
            const isNewest = index === 0
            const isIncident = item.kind === 'incident'
            const tenantStyles = getTenantStyles(item.tenant)
            const statusDot = item.kind === 'session' 
              ? getSessionStatusStyles(item.status).dot
              : getIncidentDotClass(item.status)

            return (
              <div
                key={item.id}
                className={`flex items-center gap-2 border px-2.5 py-1 rounded-full text-xs font-medium whitespace-nowrap transition-colors duration-150 ${
                  isIncident 
                    ? 'bg-amber-500/5 border-amber-500/20 hover:bg-amber-500/10 text-amber-200/90' 
                    : 'bg-white/5 border-white/10 hover:bg-white/10 text-[var(--text-secondary)]'
                } ${isNewest ? 'pulse-newest-item' : ''}`}
              >
                {/* status dot */}
                <span className={`w-1.5 h-1.5 rounded-full ${statusDot}`} />
                
                {/* harness */}
                <span className="font-mono font-semibold text-[var(--text-primary)]">
                  {item.harness}
                </span>
                
                {/* separator */}
                <span className="text-white/20">·</span>
                
                {/* short label */}
                <span className="truncate max-w-[120px] font-mono text-[11px]">
                  {item.label}
                </span>
                
                {/* tenant dot */}
                <span className={`w-1.5 h-1.5 rounded-full ${tenantStyles.dot}`} title={`Tenant: ${tenantStyles.label}`} />
                
                {/* time */}
                <span className="text-[10px] text-[var(--text-muted)]">
                  {timeAgo(item.ts)}
                </span>
              </div>
            )
          })
        )}
      </div>
    </div>
  )
}
