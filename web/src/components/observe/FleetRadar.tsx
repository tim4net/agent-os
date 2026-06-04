import { useEffect, useState, useMemo, useRef } from 'react'
import { getFleet, type SessionStatus } from '../../api/client'
import { Icon } from '../Icon'

interface FleetRadarProps {
  tenant: string
}

// Display label for an internal tenant key. The 'dayjob' key is internal-only;
// every user-visible surface must show 'Work'. Keeps the radar consistent with
// Mission Control's tenant switcher labels.
export function tenantLabel(tenant: string): string {
  switch (tenant) {
    case 'dayjob':
      return 'Work'
    case 'personal':
      return 'Personal'
    case 'all':
      return 'All'
    default:
      return tenant
  }
}

// Deterministic string hashing function to return angle in radians [0, 2*PI]
export function getAngleFromSessionId(sessionId: string): number {
  let hash = 0
  for (let i = 0; i < sessionId.length; i++) {
    hash = sessionId.charCodeAt(i) + ((hash << 5) - hash)
  }
  const degrees = Math.abs(hash) % 360
  return (degrees * Math.PI) / 180
}

function syncedRel(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return `${s}s ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m ago`
  return `${Math.floor(h / 24)}d ago`
}

export function FleetRadar({ tenant }: FleetRadarProps) {
  const [sessions, setSessions] = useState<SessionStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [tick, setTick] = useState(0)
  
  // Interactive state
  const [hoveredSession, setHoveredSession] = useState<SessionStatus | null>(null)
  const [hoveredPos, setHoveredPos] = useState<{ x: number; y: number } | null>(null)
  const [focusedSessionId, setFocusedSessionId] = useState<string | null>(null)
  
  const containerRef = useRef<HTMLDivElement>(null)

  // Auto-refresh coordinates and relative times every 5 seconds
  useEffect(() => {
    const interval = setInterval(() => {
      setTick((t) => t + 1)
    }, 5000)
    return () => clearInterval(interval)
  }, [])

  // Poll getFleet every 10 seconds
  useEffect(() => {
    let cancelled = false
    
    function fetchFleet() {
      getFleet(tenant)
        .then((res) => {
          if (!cancelled) {
            setSessions(res.sessions ?? [])
            setError(null)
          }
        })
        .catch((e: unknown) => {
          if (!cancelled) {
            setError(e instanceof Error ? e.message : 'Failed to load fleet')
          }
        })
        .finally(() => {
          if (!cancelled) {
            setLoading(false)
          }
        })
    }

    setLoading(true)
    fetchFleet()

    const interval = setInterval(fetchFleet, 10000)

    return () => {
      cancelled = true
      clearInterval(interval)
    }
  }, [tenant])

  const now = useMemo(() => {
    // eslint-disable-next-line @typescript-eslint/no-unused-expressions
    tick
    return Date.now()
  }, [tick])

  const maxWindow = 12 * 60 * 60 * 1000 // 12 hours
  const minRadius = 25
  const maxRadius = 175

  // Map session array to polar coordinates
  const sessionsWithCoords = useMemo(() => {
    return sessions.map((s) => {
      const angle = getAngleFromSessionId(s.session_id)
      const t = Date.parse(s.last_event_at)
      let radius = maxRadius
      if (!Number.isNaN(t)) {
        const elapsed = Math.max(0, now - t)
        const ratio = Math.min(1, elapsed / maxWindow)
        radius = minRadius + (maxRadius - minRadius) * Math.sqrt(ratio)
      }
      
      const x = 200 + radius * Math.cos(angle)
      const y = 200 + radius * Math.sin(angle)
      return {
        ...s,
        x,
        y,
        radius,
        angle,
      }
    })
  }, [sessions, now])

  // Aggregate counts per status
  const counts = useMemo(() => {
    const res = { running: 0, stale: 0, failed: 0, done: 0, total: sessions.length }
    sessions.forEach((s) => {
      if (s.status === 'running') res.running++
      else if (s.status === 'stale') res.stale++
      else if (s.status === 'failed') res.failed++
      else if (s.status === 'done') res.done++
    })
    return res
  }, [sessions])

  const getBlipColor = (status: string) => {
    switch (status) {
      case 'running': return '#10b981' // emerald-500
      case 'stale': return '#f59e0b'   // amber-500
      case 'failed': return '#ef4444'  // red-500
      case 'done': return '#6b7280'    // gray-500
      default: return '#64748b'        // slate-500
    }
  }

  const getBlipRadius = (status: string) => {
    switch (status) {
      case 'running': return 7
      case 'stale': return 5.5
      case 'failed': return 5.5
      case 'done': return 4.5
      default: return 5
    }
  }

  return (
    <div className="p-6 max-w-4xl mx-auto w-full flex flex-col gap-6">
      <style>{`
        @keyframes radar-sweep-rotate {
          from { transform: rotate(0deg); }
          to { transform: rotate(360deg); }
        }
        @keyframes blip-pulse {
          0%, 100% { transform: scale(1); opacity: 0.4; }
          50% { transform: scale(1.6); opacity: 0.1; }
        }
        .radar-sweep-line {
          transform-origin: 200px 200px;
          animation: radar-sweep-rotate 4s linear infinite;
        }
        @media (prefers-reduced-motion: reduce) {
          .radar-sweep-line {
            animation: none;
          }
        }
        .blip-pulse-element {
          animation: blip-pulse 2s ease-in-out infinite;
        }
      `}</style>

      {/* Header Info */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-[var(--border-subtle)] pb-4">
        <div>
          <h3 className="text-lg font-bold text-[var(--text-primary)] flex items-center gap-2">
            <Icon name="radar" className="text-[var(--accent-cyan)] animate-pulse" size={20} />
            Fleet Radar Scope
          </h3>
          <p className="text-xs text-[var(--color-text-muted)] mt-1">
            Real-time radial visualization of active sessions mapped by last event recency.
          </p>
        </div>
        <div className="flex items-center gap-2 bg-[var(--bg-elevated)] px-3 py-1.5 rounded-lg border border-[var(--glass-border)] text-xs text-[var(--color-text-secondary)] font-mono">
          <span className="w-2 h-2 rounded-full bg-emerald-500 animate-ping"></span>
          <span>Scope Area: 12h history</span>
        </div>
      </div>

      {error && (
        <div className="glass-card p-4 text-[#f87171] text-sm my-2">
          {error}
        </div>
      )}

      {/* Main Scope container */}
      <div className="flex flex-col items-center justify-center">
        <div ref={containerRef} className="relative w-full max-w-[420px] aspect-square bg-[var(--bg-card)] border border-[var(--glass-border)] rounded-2xl p-4 shadow-soft">
          <svg viewBox="0 0 400 400" className="w-full h-full">
            <defs>
              <filter id="runningGlow" x="-50%" y="-50%" width="200%" height="200%">
                <feGaussianBlur stdDeviation="3" result="blur" />
                <feMerge>
                  <feMergeNode in="blur" />
                  <feMergeNode in="SourceGraphic" />
                </feMerge>
              </filter>
              <linearGradient id="sweepGrad" x1="200" y1="20" x2="72.72" y2="72.72" gradientUnits="userSpaceOnUse">
                <stop offset="0%" stopColor="var(--accent-cyan)" stopOpacity="0.25" />
                <stop offset="100%" stopColor="var(--accent-cyan)" stopOpacity="0" />
              </linearGradient>
            </defs>

            {/* Radar Grid concentric rings (1h, 3h, 6h, 12h) */}
            <circle cx="200" cy="200" r="175" fill="none" stroke="var(--border-subtle)" strokeWidth="1" />
            <circle cx="200" cy="200" r="131" fill="none" stroke="var(--border-subtle)" strokeWidth="1" />
            <circle cx="200" cy="200" r="100" fill="none" stroke="var(--border-subtle)" strokeWidth="1" />
            <circle cx="200" cy="200" r="68" fill="none" stroke="var(--border-subtle)" strokeWidth="1" />
            <circle cx="200" cy="200" r="20" fill="none" stroke="var(--border-subtle)" strokeWidth="1" strokeDasharray="2 2" />

            {/* Radial Spokes */}
            <line x1="200" y1="20" x2="200" y2="380" stroke="var(--border-subtle)" strokeWidth="0.75" strokeDasharray="4 4" />
            <line x1="20" y1="200" x2="380" y2="200" stroke="var(--border-subtle)" strokeWidth="0.75" strokeDasharray="4 4" />
            <line x1="72.72" y1="72.72" x2="327.28" y2="327.28" stroke="var(--border-subtle)" strokeWidth="0.5" strokeDasharray="2 2" />
            <line x1="72.72" y1="327.28" x2="327.28" y2="72.72" stroke="var(--border-subtle)" strokeWidth="0.5" strokeDasharray="2 2" />

            {/* Ring Time Labels */}
            <text x="200" y="128" textAnchor="middle" className="fill-[var(--text-muted)] text-[8px] font-mono select-none pointer-events-none opacity-40">1h</text>
            <text x="200" y="96" textAnchor="middle" className="fill-[var(--text-muted)] text-[8px] font-mono select-none pointer-events-none opacity-40">3h</text>
            <text x="200" y="65" textAnchor="middle" className="fill-[var(--text-muted)] text-[8px] font-mono select-none pointer-events-none opacity-40">6h</text>
            <text x="200" y="21" textAnchor="middle" className="fill-[var(--text-muted)] text-[8px] font-mono select-none pointer-events-none opacity-40">12h</text>

            {/* Degree/Compass markers */}
            <text x="200" y="14" textAnchor="middle" className="fill-[var(--text-muted)] text-[9px] font-mono select-none pointer-events-none opacity-60">N</text>
            <text x="392" y="203" textAnchor="end" className="fill-[var(--text-muted)] text-[9px] font-mono select-none pointer-events-none opacity-60">E</text>
            <text x="200" y="394" textAnchor="middle" className="fill-[var(--text-muted)] text-[9px] font-mono select-none pointer-events-none opacity-60">S</text>
            <text x="8" y="203" textAnchor="start" className="fill-[var(--text-muted)] text-[9px] font-mono select-none pointer-events-none opacity-60">W</text>

            {/* Rotating Sweep Line & Trail Wedge */}
            <g className="radar-sweep-line">
              <path d="M 200 200 L 200 20 A 180 180 0 0 0 72.72 72.72 Z" fill="url(#sweepGrad)" />
              <line x1="200" y1="200" x2="200" y2="20" stroke="var(--accent-cyan)" strokeWidth="1.5" strokeLinecap="round" />
            </g>

            {/* Center Label (Total + Tenant) */}
            <g className="pointer-events-none select-none">
              <text x="200" y="196" textAnchor="middle" className="fill-[var(--text-primary)] font-bold text-base font-sans">
                {counts.total}
              </text>
              <text x="200" y="210" textAnchor="middle" className="fill-[var(--text-muted)] text-[8px] uppercase tracking-wider font-mono font-semibold">
                {tenantLabel(tenant)}
              </text>
            </g>

            {/* Blips */}
            {!loading && !error && sessionsWithCoords.map((s) => {
              const isFocused = focusedSessionId === s.session_id
              const r = getBlipRadius(s.status)
              const color = getBlipColor(s.status)
              
              return (
                <g
                  key={s.session_id}
                  tabIndex={0}
                  role="button"
                  aria-label={`Session ${s.session_id.substring(0, 8)} (${s.status})`}
                  className="cursor-pointer focus:outline-none"
                  onMouseEnter={() => {
                    setHoveredSession(s)
                    setHoveredPos({ x: s.x, y: s.y })
                  }}
                  onMouseLeave={() => {
                    setHoveredSession(null)
                    setHoveredPos(null)
                  }}
                  onFocus={() => {
                    setFocusedSessionId(s.session_id)
                    setHoveredSession(s)
                    setHoveredPos({ x: s.x, y: s.y })
                  }}
                  onBlur={() => {
                    setFocusedSessionId(null)
                    setHoveredSession(null)
                    setHoveredPos(null)
                  }}
                >
                  {/* Focus Ring */}
                  {isFocused && (
                    <circle
                      cx={s.x}
                      cy={s.y}
                      r={r + 5}
                      fill="none"
                      stroke="var(--accent-cyan)"
                      strokeWidth="1.5"
                      strokeDasharray="2 2"
                    />
                  )}

                  {/* Pulsing Aura (running only) */}
                  {s.status === 'running' && (
                    <circle
                      cx={s.x}
                      cy={s.y}
                      r={r}
                      fill={color}
                      className="blip-pulse-element"
                      style={{ transformOrigin: `${s.x}px ${s.y}px` }}
                    />
                  )}

                  {/* Main Blip */}
                  <circle
                    cx={s.x}
                    cy={s.y}
                    r={r}
                    fill={color}
                    filter={s.status === 'running' ? 'url(#runningGlow)' : undefined}
                    className="transition-all duration-200 hover:scale-125 origin-center"
                    style={{ transformOrigin: `${s.x}px ${s.y}px` }}
                  />
                </g>
              )
            })}
          </svg>

          {/* Loading Overlay */}
          {loading && (
            <div className="absolute inset-0 flex flex-col items-center justify-center bg-[var(--bg-base)]/40 backdrop-blur-[2px] rounded-2xl select-none pointer-events-none">
              <div className="bg-[var(--bg-elevated)]/90 border border-[var(--glass-border)] px-4 py-3 rounded-xl shadow-float text-center max-w-[200px] flex items-center justify-center gap-2">
                <span className="w-2.5 h-2.5 rounded-full bg-[var(--accent-cyan)] animate-ping"></span>
                <span className="text-xs font-semibold text-[var(--color-text-primary)]">Acquiring signal…</span>
              </div>
            </div>
          )}

          {/* Empty Overlay */}
          {!loading && !error && sessions.length === 0 && (
            <div className="absolute inset-0 flex flex-col items-center justify-center select-none pointer-events-none">
              <div className="bg-[var(--bg-elevated)]/95 border border-[var(--glass-border)] p-4 rounded-xl shadow-float text-center max-w-[280px]">
                <Icon name="radar" className="text-[var(--color-text-muted)] mb-1 flex justify-center" size={24} />
                <h4 className="text-xs font-bold text-[var(--color-text-primary)] uppercase tracking-wider">No active signal</h4>
                <p className="text-[11px] text-[var(--color-text-muted)] mt-1.5 leading-relaxed">
                  No sessions found for tenant <span className="font-mono text-[var(--color-text-secondary)] font-semibold">{tenantLabel(tenant)}</span>
                </p>
              </div>
            </div>
          )}

          {/* Detailed Tooltip */}
          {hoveredSession && hoveredPos && (
            <div
              className="absolute z-10 p-3 text-xs bg-[var(--bg-elevated)]/95 backdrop-blur-md border border-[var(--glass-border)] rounded-lg shadow-float text-[var(--color-text-primary)] w-[240px] pointer-events-none transition-opacity duration-150 animate-fade-in"
              style={{
                left: `${(hoveredPos.x / 400) * 100}%`,
                top: `${(hoveredPos.y / 400) * 100}%`,
                transform: hoveredPos.y < 80 ? 'translate(-50%, 20%)' : 'translate(-50%, -120%)',
              }}
            >
              <div className="font-semibold mb-1 flex items-center justify-between">
                <span className="truncate mr-2 font-mono text-[var(--color-text-primary)]">{hoveredSession.harness}</span>
                <span
                  className="px-1.5 py-0.5 rounded text-[9px] uppercase font-bold"
                  style={{
                    backgroundColor: `${getBlipColor(hoveredSession.status)}25`,
                    color: getBlipColor(hoveredSession.status),
                  }}
                >
                  {hoveredSession.status}
                </span>
              </div>
              <div className="font-mono text-[9px] text-[var(--color-text-muted)] mb-1.5">
                ID: {hoveredSession.session_id.substring(0, 16)}...
              </div>
              <div className="space-y-1 text-[11px] text-[var(--color-text-secondary)]">
                <div>
                  <strong className="text-[var(--color-text-muted)] font-normal">Host:</strong>{' '}
                  <span className="font-mono">{hoveredSession.host}</span>
                  {hoveredSession.pid ? ` (PID ${hoveredSession.pid})` : ''}
                </div>
                <div>
                  <strong className="text-[var(--color-text-muted)] font-normal">Last Event:</strong>{' '}
                  <code className="bg-[var(--bg-base)] px-1 py-0.5 rounded text-[10px] text-[var(--color-text-primary)] font-mono">
                    {hoveredSession.last_event_kind || 'none'}
                  </code>
                </div>
                <div className="text-[10px] text-[var(--color-text-muted)] mt-1.5 border-t border-[var(--border-subtle)] pt-1.5">
                  Updated {syncedRel(hoveredSession.last_event_at)}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Legend & Counts */}
      <div className="glass-card p-4 border border-[var(--glass-border)] bg-[var(--bg-card)] rounded-xl mt-2">
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-center">
          <div className="flex items-center justify-between md:justify-center gap-3 px-3 py-1.5 rounded-lg bg-[var(--bg-elevated)]/50 border border-[var(--border-subtle)]">
            <div className="flex items-center gap-2">
              <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-pulse"></span>
              <span className="text-xs text-[var(--color-text-secondary)] font-medium">Running</span>
            </div>
            <span className="text-xs font-mono font-bold text-[var(--color-text-primary)] bg-[var(--bg-base)] px-1.5 py-0.5 rounded">
              {counts.running}
            </span>
          </div>

          <div className="flex items-center justify-between md:justify-center gap-3 px-3 py-1.5 rounded-lg bg-[var(--bg-elevated)]/50 border border-[var(--border-subtle)]">
            <div className="flex items-center gap-2">
              <span className="w-2.5 h-2.5 rounded-full bg-amber-500"></span>
              <span className="text-xs text-[var(--color-text-secondary)] font-medium">Stale</span>
            </div>
            <span className="text-xs font-mono font-bold text-[var(--color-text-primary)] bg-[var(--bg-base)] px-1.5 py-0.5 rounded">
              {counts.stale}
            </span>
          </div>

          <div className="flex items-center justify-between md:justify-center gap-3 px-3 py-1.5 rounded-lg bg-[var(--bg-elevated)]/50 border border-[var(--border-subtle)]">
            <div className="flex items-center gap-2">
              <span className="w-2.5 h-2.5 rounded-full bg-red-500"></span>
              <span className="text-xs text-[var(--color-text-secondary)] font-medium">Failed</span>
            </div>
            <span className="text-xs font-mono font-bold text-[var(--color-text-primary)] bg-[var(--bg-base)] px-1.5 py-0.5 rounded">
              {counts.failed}
            </span>
          </div>

          <div className="flex items-center justify-between md:justify-center gap-3 px-3 py-1.5 rounded-lg bg-[var(--bg-elevated)]/50 border border-[var(--border-subtle)]">
            <div className="flex items-center gap-2">
              <span className="w-2.5 h-2.5 rounded-full bg-gray-500"></span>
              <span className="text-xs text-[var(--color-text-secondary)] font-medium">Done</span>
            </div>
            <span className="text-xs font-mono font-bold text-[var(--color-text-primary)] bg-[var(--bg-base)] px-1.5 py-0.5 rounded">
              {counts.done}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}
