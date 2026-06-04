import { useEffect, useState, useCallback, useRef } from 'react'
import { Icon } from './Icon'
import { 
  listIncidents, 
  getSpend, 
  getFleet, 
  getRecurringFindings,
  getControlState
} from '../api/client'
import type { 
  Incident, 
  SpendRow, 
  SessionStatus, 
  RecurringFindingsRow,
  Agent,
  ControlState
} from '../api/client'
import AgentDetailDrawer from './AgentDetailDrawer'
import PulseTicker from './PulseTicker'

/* ─── Helpers ─── */
export function timeAgo(iso: string): string {
  if (!iso) return 'never'
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

export function formatCurrency(val: number) {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
  }).format(val);
}

export function formatTokens(tokens: number): string {
  if (tokens >= 1_000_000) {
    return `${(tokens / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`;
  }
  if (tokens >= 1_000) {
    return `${(tokens / 1_000).toFixed(1).replace(/\.0$/, '')}K`;
  }
  return tokens.toString();
}

export function getIncidentStatusColor(status: string) {
  switch (status.toLowerCase()) {
    case 'failed':
    case 'down':
    case 'error':
      return 'text-red-400 bg-red-500/10 border-red-500/20';
    case 'stale':
    case 'warning':
      return 'text-amber-400 bg-amber-500/10 border-amber-500/20';
    default:
      return 'text-blue-400 bg-blue-500/10 border-blue-500/20';
  }
}

function getIncidentSideBarColor(status: string) {
  switch (status.toLowerCase()) {
    case 'failed':
    case 'down':
    case 'error':
      return 'bg-red-500 shadow-[0_0_8px_rgba(239,68,68,0.4)]';
    case 'stale':
    case 'warning':
      return 'bg-amber-500 shadow-[0_0_8px_rgba(245,158,11,0.3)]';
    default:
      return 'bg-blue-500 shadow-[0_0_8px_rgba(59,130,246,0.3)]';
  }
}

export function getSessionStatusStyles(status: string) {
  switch (status.toLowerCase()) {
    case 'running':
      return {
        badge: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20',
        dot: 'bg-emerald-500 animate-pulse',
      };
    case 'failed':
    case 'error':
      return {
        badge: 'bg-red-500/10 text-red-400 border-red-500/20',
        dot: 'bg-red-500',
      };
    case 'stale':
    case 'warning':
      return {
        badge: 'bg-amber-500/10 text-amber-400 border-amber-500/20',
        dot: 'bg-amber-500',
      };
    case 'done':
    case 'completed':
    default:
      return {
        badge: 'bg-gray-500/10 text-gray-400 border-gray-500/20',
        dot: 'bg-gray-500',
      };
  }
}

export function getTenantStyles(tenant: string) {
  const isPersonal = tenant === 'personal';
  const isDayjob = tenant === 'dayjob';

  if (isPersonal) {
    return {
      dot: 'bg-[var(--tenant-personal)] shadow-[0_0_8px_var(--tenant-personal)]',
      chip: 'bg-[var(--tenant-personal)]/10 text-[var(--tenant-personal)] border border-[var(--tenant-personal)]/20',
      avatar: 'bg-gradient-to-br from-[var(--tenant-personal)] to-[var(--tenant-personal-2)] text-black font-bold shadow-[0_0_12px_rgba(34,211,238,0.2)]',
      border: 'border-[var(--tenant-personal)]/20',
      text: 'text-[var(--tenant-personal)]',
      bar: 'bg-gradient-to-r from-[var(--tenant-personal)] to-[var(--tenant-personal-2)]',
      label: 'Personal',
    };
  }

  if (isDayjob) {
    return {
      dot: 'bg-[var(--tenant-dayjob)] shadow-[0_0_8px_var(--tenant-dayjob)]',
      chip: 'bg-[var(--tenant-dayjob)]/10 text-[var(--tenant-dayjob)] border border-[var(--tenant-dayjob)]/20',
      avatar: 'bg-gradient-to-br from-[var(--tenant-dayjob)] to-[var(--tenant-dayjob-2)] text-white font-bold shadow-[0_0_12px_rgba(251,146,60,0.25)]',
      border: 'border-[var(--tenant-dayjob)]/20',
      text: 'text-[var(--tenant-dayjob)]',
      bar: 'bg-gradient-to-r from-[var(--tenant-dayjob)] to-[var(--tenant-dayjob-2)]',
      label: 'Work',
    };
  }

  return {
    dot: 'bg-[var(--accent-blue)] shadow-[0_0_8px_var(--accent-blue)]',
    chip: 'bg-white/5 text-[var(--text-secondary)] border border-white/10',
    avatar: 'bg-gradient-to-br from-[var(--gradient-start)] to-[var(--gradient-end)] text-white font-bold',
    border: 'border-white/5',
    text: 'text-[var(--text-secondary)]',
    bar: 'bg-gradient-to-r from-[var(--gradient-start)] via-[var(--gradient-mid)] to-[var(--gradient-end)]',
    label: tenant || 'System',
  };
}

export function deriveTenant(key: string, agents: Agent[], sessions: SessionStatus[], incidents: Incident[]): string | undefined {
  const lowerKey = key.toLowerCase();
  
  // Search sessions
  const sessionMatch = sessions.find(s => 
    s.harness.toLowerCase() === lowerKey || 
    s.session_id.toLowerCase() === lowerKey ||
    s.host.toLowerCase() === lowerKey
  );
  if (sessionMatch) return sessionMatch.tenant;

  // Search incidents
  const incidentMatch = incidents.find(i => 
    i.harness.toLowerCase() === lowerKey || 
    i.project_slug.toLowerCase() === lowerKey
  );
  if (incidentMatch) return incidentMatch.tenant;

  // Search agents prop
  const agentMatch = agents.find(a => 
    a.id.toLowerCase() === lowerKey || 
    a.name.toLowerCase() === lowerKey || 
    a.harness.toLowerCase() === lowerKey ||
    a.display_name.toLowerCase() === lowerKey
  );
  if (agentMatch) {
    const hMatch = sessions.find(s => s.harness.toLowerCase() === agentMatch.harness.toLowerCase());
    if (hMatch) return hMatch.tenant;
    
    const iMatch = incidents.find(i => i.harness.toLowerCase() === agentMatch.harness.toLowerCase());
    if (iMatch) return iMatch.tenant;
  }

  // Fallbacks
  if (lowerKey.includes('personal') || lowerKey.includes('home') || lowerKey.includes('cool')) return 'personal';
  if (lowerKey.includes('dayjob') || lowerKey.includes('work') || lowerKey.includes('job') || lowerKey.includes('warm')) return 'dayjob';

  return undefined;
}

/* ─── Internal Hooks ─── */
function useIncidents(tenantFilter: 'all' | 'personal' | 'dayjob') {
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)
    
    let promise;
    if (tenantFilter === 'all') {
      promise = Promise.allSettled([
        listIncidents('personal', { limit: 50 }),
        listIncidents('dayjob', { limit: 50 })
      ]).then((results) => {
        const successes = results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []);
        if (successes.length === 0) {
          const reason = results.find((result) => result.status === 'rejected')?.reason;
          throw new Error(reason instanceof Error ? reason.message : 'Failed to fetch incidents');
        }

        results.forEach((result) => {
          if (result.status === 'rejected') console.error(result.reason);
        });

        const merged = successes.flatMap((res) => res.incidents ?? []);
        merged.sort((a, b) => new Date(b.received_at).getTime() - new Date(a.received_at).getTime());
        return {
          incidents: merged,
          total: successes.reduce((sum, res) => sum + (res.total ?? 0), 0)
        };
      });
    } else {
      promise = listIncidents(tenantFilter, { limit: 50 });
    }

    promise
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setIncidents(res.incidents ?? [])
          setTotal(res.total ?? 0)
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch incidents')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [tenantFilter])

  useEffect(() => {
    mountedRef.current = true
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllersRef.current.forEach((controller) => controller.abort())
      controllersRef.current.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { incidents, total, loading, error, refresh }
}

function useFleet(tenantFilter: 'all' | 'personal' | 'dayjob') {
  const [sessions, setSessions] = useState<SessionStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    let promise;
    if (tenantFilter === 'all') {
      promise = Promise.allSettled([
        getFleet('personal'),
        getFleet('dayjob')
      ]).then((results) => {
        const successes = results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []);
        if (successes.length === 0) {
          const reason = results.find((result) => result.status === 'rejected')?.reason;
          throw new Error(reason instanceof Error ? reason.message : 'Failed to fetch fleet');
        }

        results.forEach((result) => {
          if (result.status === 'rejected') console.error(result.reason);
        });

        const merged = successes.flatMap((res) => res.sessions ?? []);
        merged.sort((a, b) => new Date(b.last_event_at).getTime() - new Date(a.last_event_at).getTime());
        return {
          sessions: merged,
          total: successes.reduce((sum, res) => sum + (res.total ?? 0), 0)
        }
      });
    } else {
      promise = getFleet(tenantFilter);
    }

    promise
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setSessions(res.sessions ?? [])
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch fleet')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [tenantFilter])

  useEffect(() => {
    mountedRef.current = true
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllersRef.current.forEach((controller) => controller.abort())
      controllersRef.current.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { sessions, loading, error, refresh }
}

function useSpend(tenantFilter: 'all' | 'personal' | 'dayjob') {
  const [rows, setRows] = useState<SpendRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    const tenant = tenantFilter === 'all' ? undefined : tenantFilter;
    getSpend({ group_by: 'agent', tenant })
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setRows(res.rows ?? [])
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch spend')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [tenantFilter])

  useEffect(() => {
    mountedRef.current = true
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllersRef.current.forEach((controller) => controller.abort())
      controllersRef.current.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { rows, loading, error, refresh }
}

function useRecurringFindings(minCount = 2) {
  const [records, setRecords] = useState<RecurringFindingsRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    getRecurringFindings(minCount)
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setRecords(res.records ?? [])
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch recurring findings')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [minCount])

  useEffect(() => {
    mountedRef.current = true
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllersRef.current.forEach((controller) => controller.abort())
      controllersRef.current.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { records, loading, error, refresh }
}

function useMissionControlState() {
  const [state, setState] = useState<ControlState | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    getControlState()
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setState(res)
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch control state')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [])

  useEffect(() => {
    mountedRef.current = true
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllersRef.current.forEach((controller) => controller.abort())
      controllersRef.current.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { state, loading, error, refresh }
}

/* ─── UI Helpers ─── */
function ShimmerRow() {
  return (
    <div className="flex items-center gap-4 p-4 border border-[var(--border-subtle)] rounded-lg bg-[var(--bg-surface)] animate-pulse">
      <div className="w-10 h-10 rounded-full bg-[var(--bg-elevated)]" />
      <div className="flex-1 space-y-2">
        <div className="h-4 bg-[var(--bg-elevated)] rounded w-1/3" />
        <div className="h-3 bg-[var(--bg-elevated)] rounded w-2/3" />
      </div>
      <div className="w-12 h-6 bg-[var(--bg-elevated)] rounded" />
    </div>
  )
}

function PanelError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center p-6 border border-red-500/20 rounded-lg bg-red-500/5 text-center space-y-3">
      <Icon name="warning" className="text-red-400" size={32} />
      <div className="space-y-1">
        <p className="text-sm font-semibold text-[var(--text-primary)]">Failed to load data</p>
        <p className="text-xs text-[var(--text-muted)] max-w-md">{message}</p>
      </div>
      <button 
        onClick={onRetry}
        className="px-3 py-1.5 bg-red-500/10 hover:bg-red-500/20 border border-red-500/30 hover:border-red-500/50 rounded-md text-xs font-semibold text-red-400 transition-colors cursor-pointer"
      >
        Retry
      </button>
    </div>
  )
}

function PanelEmpty({ message, icon = 'check_circle' }: { message: string; icon?: string }) {
  return (
    <div className="flex flex-col items-center justify-center p-8 border border-[var(--border-subtle)] rounded-lg bg-[var(--bg-surface)] text-center space-y-2">
      <Icon name={icon} className="text-[var(--text-muted)]" size={36} />
      <p className="text-sm text-[var(--text-secondary)]">{message}</p>
    </div>
  )
}

function StatCard({ 
  label, 
  value, 
  subtext, 
  icon, 
  loading, 
  error,
  empty = false,
  accent 
}: {
  label: string
  value: string | number
  subtext?: string
  icon: string
  loading: boolean
  error: boolean
  empty?: boolean
  accent: 'green' | 'amber' | 'blue' | 'purple' | 'red' | 'gray'
}) {
  const accentStyles = {
    green: 'from-emerald-500/10 to-transparent border-emerald-500/20 text-emerald-400 shadow-[0_0_12px_rgba(16,185,129,0.1)]',
    amber: 'from-amber-500/10 to-transparent border-amber-500/20 text-amber-400 shadow-[0_0_12px_rgba(245,158,11,0.1)]',
    blue: 'from-blue-500/10 to-transparent border-blue-500/20 text-blue-400 shadow-[0_0_12px_rgba(59,130,246,0.1)]',
    purple: 'from-purple-500/10 to-transparent border-purple-500/20 text-purple-400 shadow-[0_0_12px_rgba(167,139,250,0.1)]',
    red: 'from-red-500/10 to-transparent border-red-500/20 text-red-400 shadow-[0_0_12px_rgba(239,68,68,0.15)] animate-pulse',
    gray: 'from-gray-500/10 to-transparent border-gray-500/20 text-[var(--text-secondary)] shadow-none',
  }[accent];

  return (
    <div className={`glass-card p-5 bg-gradient-to-br transition-all duration-[var(--duration-base)] hover:scale-[1.02] ${accentStyles}`}>
      {loading ? (
        <div className="space-y-3 py-1">
          <div className="h-3 bg-[var(--bg-elevated)] rounded w-1/3 animate-pulse" />
          <div className="h-6 bg-[var(--bg-elevated)] rounded w-2/3 animate-pulse" />
          <div className="h-2.5 bg-[var(--bg-elevated)] rounded w-1/2 animate-pulse" />
        </div>
      ) : error ? (
        <div className="flex flex-col justify-center h-full space-y-1">
          <div className="flex items-center gap-1 text-red-400">
            <Icon name="warning" size={14} />
            <span className="text-[10px] font-semibold">Error</span>
          </div>
          <p className="text-xs text-[var(--text-muted)] font-medium">Failed to fetch</p>
        </div>
      ) : (
        <div className="flex flex-col justify-between h-full space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
              {label}
            </span>
            <Icon name={icon} className="opacity-80" size={18} />
          </div>
          
          <div className="space-y-0.5">
            <p className={`text-3xl font-extrabold tracking-tight ${empty ? 'text-[var(--text-muted)]' : 'text-[var(--text-primary)]'}`}>
              {empty ? '—' : value}
            </p>
            {subtext && (
              <p className="text-[10px] font-mono text-[var(--text-muted)] truncate">
                {subtext}
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/* ─── Panels ─── */
function IncidentsPanel({ 
  incidents, 
  loading, 
  error, 
  onRetry 
}: { 
  incidents: Incident[]
  loading: boolean
  error: string | null
  onRetry: () => void
}) {
  return (
    <div className="glass-card p-5 space-y-4">
      <div className="flex items-center justify-between border-b border-[var(--border-subtle)] pb-3">
        <div className="flex items-center gap-2">
          <Icon name="error" className="text-red-400" />
          <h2 className="text-lg font-bold tracking-tight text-[var(--text-primary)]">What's broken right now</h2>
        </div>
        <span className="text-xs font-mono text-[var(--text-muted)] bg-white/5 px-2 py-0.5 rounded-full">
          {incidents.length} active
        </span>
      </div>

      {loading ? (
        <div className="space-y-3">
          <ShimmerRow />
          <ShimmerRow />
          <ShimmerRow />
        </div>
      ) : error ? (
        <PanelError message={error} onRetry={onRetry} />
      ) : incidents.length === 0 ? (
        <PanelEmpty message="No incidents — all clear ✓" icon="check_circle" />
      ) : (
        <div className="space-y-2 divide-y divide-[var(--border-subtle)]">
          {incidents.map((incident, idx) => {
            const tenantStyles = getTenantStyles(incident.tenant);
            const severityColor = getIncidentStatusColor(incident.status);
            const sidebarColor = getIncidentSideBarColor(incident.status);
            return (
              <div 
                key={`${incident.session_id}-${idx}`}
                className="flex flex-col md:flex-row md:items-center justify-between gap-3 pt-3 first:pt-0 group hover:bg-[var(--bg-hover)] transition-colors p-2 rounded-lg"
              >
                <div className="flex items-start gap-3 min-w-0">
                  <span className={`w-1.5 h-10 rounded-full flex-shrink-0 ${sidebarColor}`} />
                  <div className="min-w-0 space-y-0.5">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-semibold text-[var(--text-primary)] truncate">
                        {incident.title}
                      </span>
                      <span className={`text-[10px] uppercase font-bold tracking-wider px-2 py-0.5 rounded-full ${severityColor}`}>
                        {incident.status}
                      </span>
                    </div>
                    <div className="flex items-center gap-2 text-xs text-[var(--text-muted)] flex-wrap">
                      <span className="font-mono bg-white/5 px-1.5 py-0.5 rounded text-[var(--text-secondary)]">
                        {incident.harness}
                      </span>
                      {incident.project_slug && (
                        <>
                          <span>·</span>
                          <span className="truncate">proj: {incident.project_slug}</span>
                        </>
                      )}
                      {incident.branch && (
                        <>
                          <span>·</span>
                          <span className="truncate font-mono">branch: {incident.branch}</span>
                        </>
                      )}
                    </div>
                  </div>
                </div>
                
                <div className="flex items-center gap-3 self-end md:self-center">
                  <span className={`text-xs px-2.5 py-0.5 rounded-full font-semibold ${tenantStyles.chip}`}>
                    {tenantStyles.label}
                  </span>
                  <span className="text-xs text-[var(--text-muted)] whitespace-nowrap">
                    {timeAgo(incident.received_at)}
                  </span>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function FleetPanel({
  sessions,
  loading,
  error,
  onRetry
}: {
  sessions: SessionStatus[]
  loading: boolean
  error: string | null
  onRetry: () => void
}) {
  return (
    <div className="glass-card p-5 space-y-4">
      <div className="flex items-center justify-between border-b border-[var(--border-subtle)] pb-3">
        <div className="flex items-center gap-2">
          <Icon name="smart_toy" className="text-accent-blue" />
          <h2 className="text-lg font-bold tracking-tight text-[var(--text-primary)]">Live agent work</h2>
        </div>
        <span className="text-xs font-mono text-[var(--text-muted)] bg-white/5 px-2 py-0.5 rounded-full">
          {sessions.length} sessions
        </span>
      </div>

      {loading ? (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <ShimmerRow />
          <ShimmerRow />
          <ShimmerRow />
          <ShimmerRow />
        </div>
      ) : error ? (
        <PanelError message={error} onRetry={onRetry} />
      ) : sessions.length === 0 ? (
        <PanelEmpty message="No active agent sessions" icon="smart_toy" />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {sessions.map((session, idx) => {
            const tenantStyles = getTenantStyles(session.tenant);
            const statusStyles = getSessionStatusStyles(session.status);
            const initial = (session.harness || 'A').charAt(0).toUpperCase();
            return (
              <div 
                key={`${session.session_id}-${idx}`}
                className="flex items-start gap-3 p-4 border border-[var(--border-subtle)] rounded-lg bg-[var(--bg-surface)] hover:bg-[var(--bg-hover)] hover:border-white/10 transition-all duration-[var(--duration-base)] hover:scale-[1.01]"
              >
                <div className={`w-10 h-10 rounded-full flex items-center justify-center text-sm font-bold shrink-0 ${tenantStyles.avatar}`}>
                  {initial}
                </div>

                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex items-center justify-between gap-2">
                    <p className="text-sm font-semibold text-[var(--text-primary)] truncate">
                      {session.harness}
                    </p>
                    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 text-[10px] font-semibold border rounded-full ${statusStyles.badge}`}>
                      <span className={`w-1.5 h-1.5 rounded-full ${statusStyles.dot}`} />
                      {session.status}
                    </span>
                  </div>

                  <p className="text-xs text-[var(--text-muted)] font-mono truncate">
                    host: {session.host || 'unknown'} · ID: {session.session_id.slice(0, 8)}
                  </p>

                  {session.last_event_kind && (
                    <div className="mt-2 text-xs bg-white/5 border border-white/5 rounded p-1.5 text-[var(--text-secondary)] font-mono truncate">
                      <span className="text-[var(--text-muted)]">Event: </span>
                      {session.last_event_kind}
                    </div>
                  )}
                  
                  <div className="flex justify-between items-center text-[10px] text-[var(--text-muted)] pt-1">
                    <span className={tenantStyles.text}>{tenantStyles.label}</span>
                    <span>{timeAgo(session.last_event_at)}</span>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function SpendPanel({
  rows,
  loading,
  error,
  onRetry,
  agents,
  sessions,
  incidents,
  onSelectAgentDetail
}: {
  rows: SpendRow[]
  loading: boolean
  error: string | null
  onRetry: () => void
  agents: Agent[]
  sessions: SessionStatus[]
  incidents: Incident[]
  onSelectAgentDetail?: (row: SpendRow) => void
}) {
  const maxTokens = Math.max(...rows.map(r => r.total_tokens), 1);

  return (
    <div className="glass-card p-5 space-y-4">
      <div className="flex items-center justify-between border-b border-[var(--border-subtle)] pb-3">
        <div className="flex items-center gap-2">
          <Icon name="payments" className="text-accent-purple" />
          <h2 className="text-lg font-bold tracking-tight text-[var(--text-primary)]">Token usage by agent</h2>
        </div>
        <span className="text-xs font-mono text-[var(--text-muted)] bg-white/5 px-2 py-0.5 rounded-full">
          agent usage
        </span>
      </div>

      {loading ? (
        <div className="space-y-4">
          {[...Array(3)].map((_, i) => (
            <div key={i} className="space-y-2">
              <div className="h-3 bg-[var(--bg-elevated)] rounded w-1/4 animate-pulse" />
              <div className="h-4 bg-[var(--bg-elevated)] rounded w-full animate-pulse" />
            </div>
          ))}
        </div>
      ) : error ? (
        <PanelError message={error} onRetry={onRetry} />
      ) : rows.length === 0 ? (
        <PanelEmpty message="No spend records found" icon="payments" />
      ) : (
        <div className="space-y-4">
          {rows.map((row, idx) => {
            const derivedTenant = deriveTenant(row.dimension_key, agents, sessions, incidents);
            const tenantStyles = getTenantStyles(derivedTenant || '');
            const pct = (row.total_tokens / maxTokens) * 100;

            let billingChip = null;
            if (row.billing_mode === 'metered' && row.total_cost_usd !== null) {
              billingChip = (
                <span className={`px-1.5 py-0.5 rounded text-[9px] font-semibold font-mono uppercase tracking-wider shrink-0 ${tenantStyles.chip}`}>
                  {row.provider ? `${row.provider} · ` : ''}metered · {formatCurrency(row.total_cost_usd)}
                </span>
              );
            } else if (row.billing_mode === 'subscription') {
              billingChip = (
                <span className={`px-1.5 py-0.5 rounded text-[9px] font-semibold font-mono uppercase tracking-wider shrink-0 ${tenantStyles.chip}`}>
                  {row.provider ? `${row.provider} · ` : ''}subscription
                </span>
              );
            } else {
              billingChip = (
                <span className="px-1.5 py-0.5 rounded text-[9px] font-semibold font-mono uppercase tracking-wider bg-white/5 text-[var(--text-secondary)] border border-white/10 shrink-0">
                  {row.provider ? `${row.provider} · ` : ''}usage-only
                </span>
              );
            }

            return (
              <button
                key={`${row.dimension_key}-${idx}`}
                type="button"
                onClick={() => onSelectAgentDetail?.(row)}
                className="w-full text-left space-y-1.5 p-2 -mx-2 rounded-lg hover:bg-white/5 transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]/50 cursor-pointer block"
              >
                <div className="flex justify-between items-center text-xs">
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="font-semibold text-[var(--text-primary)] truncate">
                      {row.dimension_key}
                    </span>
                    {billingChip}
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="text-[var(--text-muted)] font-mono">
                      {row.total_turns} turns · {row.session_count} sessions
                    </span>
                    <span className="font-bold text-[var(--text-primary)]">
                      {formatTokens(row.total_tokens)}
                    </span>
                  </div>
                </div>
                
                <div className="w-full h-2 bg-white/5 rounded-full overflow-hidden">
                  <div 
                    style={{ width: `${pct}%` }}
                    className={`h-full rounded-full transition-all duration-1000 ease-out ${tenantStyles.bar}`}
                  />
                </div>

                <div className="flex justify-between text-[10px] text-[var(--text-muted)]">
                  <span>Tenant: <span className={derivedTenant ? tenantStyles.text : 'text-[var(--text-muted)]'}>{derivedTenant ? tenantStyles.label : 'unclassified'}</span></span>
                  <span>{pct.toFixed(0)}% of max</span>
                </div>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

function RecurringFindingsPanel({
  records,
  loading,
  error,
  onRetry
}: {
  records: RecurringFindingsRow[]
  loading: boolean
  error: string | null
  onRetry: () => void
}) {
  return (
    <div className="glass-card p-5 space-y-4">
      <div className="flex items-center justify-between border-b border-[var(--border-subtle)] pb-3">
        <div className="flex items-center gap-2">
          <Icon name="history" className="text-accent-pink" />
          <h2 className="text-lg font-bold tracking-tight text-[var(--text-primary)]">Recurring findings</h2>
        </div>
        <span className="text-xs font-mono text-[var(--text-muted)] bg-white/5 px-2 py-0.5 rounded-full">
          min frequency 2
        </span>
      </div>

      {loading ? (
        <div className="space-y-3">
          <ShimmerRow />
          <ShimmerRow />
        </div>
      ) : error ? (
        <PanelError message={error} onRetry={onRetry} />
      ) : records.length === 0 ? (
        <PanelEmpty message="No recurring findings detected" icon="history" />
      ) : (
        <div className="space-y-2">
          {records.map((record, idx) => {
            return (
              <div 
                key={`${record.class}-${record.wp_ref}-${idx}`}
                className="flex items-center justify-between gap-3 p-3 border border-[var(--border-subtle)] rounded-lg bg-[var(--bg-surface)] hover:bg-[var(--bg-hover)] transition-colors"
              >
                <div className="min-w-0 flex-1 space-y-0.5">
                  <p className="text-sm font-bold text-[var(--text-primary)] truncate font-mono">
                    {record.class}
                  </p>
                  <p className="text-xs text-[var(--text-muted)] truncate">
                    author: <span className="text-[var(--text-secondary)]">{record.author_agent}</span> · ref: <span className="text-[var(--text-secondary)] font-mono">{record.wp_ref}</span>
                  </p>
                </div>
                
                <div className="shrink-0 flex items-center justify-center px-2.5 py-1 bg-red-500/10 border border-red-500/20 text-red-400 rounded-lg font-bold font-mono text-xs">
                  {record.count}x
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

/* ─── Main Component ─── */
export default function MissionControl({ agents }: { agents: Agent[] }) {
  const [tenantFilter, setTenantFilter] = useState<'all' | 'personal' | 'dayjob'>('all')
  const [detailRow, setDetailRow] = useState<SpendRow | null>(null)

  const { incidents, total: incidentsCount, loading: incidentsLoading, error: incidentsError, refresh: refreshIncidents } = useIncidents(tenantFilter)
  const { sessions, loading: fleetLoading, error: fleetError, refresh: refreshFleet } = useFleet(tenantFilter)
  const { rows: spendRows, loading: spendLoading, error: spendError, refresh: refreshSpend } = useSpend(tenantFilter)
  const { records: recurringRecords, loading: recurringLoading, error: recurringError, refresh: refreshRecurring } = useRecurringFindings(2)
  const { state: controlState, loading: controlLoading, error: controlError } = useMissionControlState()

  const activeCount = sessions.filter(s => s.status.toLowerCase() === 'running').length
  
  const totalTokensToday = spendRows.reduce((sum, row) => sum + (row.total_tokens ?? 0), 0)
  const meteredSpendToday = spendRows.reduce((sum, row) => sum + (row.total_cost_usd ?? 0), 0)
  const hasMeteredCost = spendRows.some(row => row.billing_mode === 'metered' && row.total_cost_usd !== null && row.total_cost_usd > 0)
  const usageSubtext = hasMeteredCost
    ? `+ ${formatCurrency(meteredSpendToday)} metered`
    : 'subscription — no metered spend'

  return (
    <div className="p-6 space-y-6 max-w-7xl mx-auto page-transition">
      {/* HEADER SECTION */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 pb-6 border-b border-[var(--border-subtle)]">
        <div className="flex items-center gap-4">
          <h1 className="text-3xl font-extrabold tracking-tight bg-gradient-to-r from-[var(--gradient-start)] via-[var(--gradient-mid)] to-[var(--gradient-end)] bg-clip-text text-transparent">
            Mission Control
          </h1>
          
          {/* Live Pill */}
          <div className="inline-flex items-center gap-2 px-3 py-1 bg-white/5 border border-[var(--border-subtle)] rounded-full text-xs font-medium text-[var(--text-secondary)]">
            <span className="w-2 h-2 rounded-full bg-emerald-500 relative flex">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
            </span>
            <span className="font-mono text-[var(--text-primary)] font-bold">{activeCount}</span>
            <span>agents active</span>
          </div>
        </div>

        {/* Tenant Switcher */}
        <div className="flex bg-white/5 border border-[var(--border-subtle)] rounded-lg p-0.5 self-start md:self-center">
          {(['all', 'personal', 'dayjob'] as const).map((t) => {
            const isActive = tenantFilter === t;
            let activeClass = '';
            if (isActive) {
              if (t === 'personal') {
                activeClass = 'bg-[var(--tenant-personal)]/15 text-[var(--tenant-personal)] border border-[var(--tenant-personal)]/30 shadow-[0_0_12px_rgba(34,211,238,0.2)] font-bold';
              } else if (t === 'dayjob') {
                activeClass = 'bg-[var(--tenant-dayjob)]/15 text-[var(--tenant-dayjob)] border border-[var(--tenant-dayjob)]/30 shadow-[0_0_12px_rgba(251,146,60,0.25)] font-bold';
              } else {
                activeClass = 'bg-white/10 text-[var(--text-primary)] border border-white/20 shadow-sm font-bold';
              }
            } else {
              activeClass = 'text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-white/5 border border-transparent';
            }

            return (
              <button
                key={t}
                onClick={() => setTenantFilter(t)}
                className={`px-4 py-1.5 rounded-md text-xs transition-all duration-[var(--duration-fast)] capitalize cursor-pointer ${activeClass}`}
              >
                {t === 'dayjob' ? 'work' : t}
              </button>
            );
          })}
        </div>
      </div>

      <PulseTicker sessions={sessions} incidents={incidents} loading={fleetLoading || incidentsLoading} />

      {/* STAT CARDS ROW */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard 
          label="In-flight work" 
          value={activeCount} 
          subtext={`${sessions.length} total sessions`}
          icon="bolt" 
          loading={fleetLoading}
          error={!!fleetError}
          accent={activeCount > 0 ? 'blue' : 'gray'}
        />

        <StatCard 
          label="Incidents now" 
          value={incidentsCount} 
          subtext={incidentsCount > 0 ? 'requires attention' : 'system healthy'}
          icon="warning" 
          loading={incidentsLoading}
          error={!!incidentsError}
          accent={incidentsCount > 0 ? 'red' : 'green'}
        />

        <StatCard 
          label="USAGE TODAY" 
          value={formatTokens(totalTokensToday)} 
          subtext={usageSubtext}
          icon="payments" 
          loading={spendLoading}
          error={!!spendError}
          empty={spendRows.length === 0}
          accent="purple"
        />

        <StatCard 
          label="Orchestrator" 
          value={controlState?.mode || 'stopped'} 
          subtext={controlState ? `cadence: ${controlState.cadence_seconds}s` : 'unknown'}
          icon="settings_suggest" 
          loading={controlLoading}
          error={!!controlError}
          empty={!controlState}
          accent={controlState?.mode === 'continuous' ? 'green' : controlState?.mode === 'tick' ? 'amber' : 'gray'}
        />
      </div>

      {/* MAIN TWO-COLUMN GRID */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-2 space-y-6">
          <IncidentsPanel 
            incidents={incidents}
            loading={incidentsLoading}
            error={incidentsError}
            onRetry={refreshIncidents}
          />
          
          <FleetPanel 
            sessions={sessions}
            loading={fleetLoading}
            error={fleetError}
            onRetry={refreshFleet}
          />
        </div>

        <div className="lg:col-span-1 space-y-6">
          <SpendPanel 
            rows={spendRows}
            loading={spendLoading}
            error={spendError}
            onRetry={refreshSpend}
            agents={agents}
            sessions={sessions}
            incidents={incidents}
            onSelectAgentDetail={setDetailRow}
          />

          <RecurringFindingsPanel 
            records={recurringRecords}
            loading={recurringLoading}
            error={recurringError}
            onRetry={refreshRecurring}
          />
        </div>
      </div>

      <AgentDetailDrawer
        row={detailRow}
        onClose={() => setDetailRow(null)}
        agents={agents}
        sessions={sessions}
        incidents={incidents}
      />
    </div>
  )
}
