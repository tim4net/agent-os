import { useEffect, useState } from 'react'
import { Icon } from './Icon'
import type { Agent, SessionStatus, Incident, SpendRow } from '../api/client'
import {
  timeAgo,
  formatCurrency,
  formatTokens,
  getIncidentStatusColor,
  getSessionStatusStyles,
  getTenantStyles,
  deriveTenant,
} from './MissionControl'

interface AgentDetailDrawerProps {
  row: SpendRow | null
  onClose: () => void
  agents: Agent[]
  sessions: SessionStatus[]
  incidents: Incident[]
}

export default function AgentDetailDrawer({
  row,
  onClose,
  agents,
  sessions,
  incidents,
}: AgentDetailDrawerProps) {
  const [isOpen, setIsOpen] = useState(false)

  useEffect(() => {
    if (row) {
      const t = setTimeout(() => setIsOpen(true), 10)
      return () => {
        clearTimeout(t)
        setIsOpen(false)
      }
    }
  }, [row])

  // ESC key handler
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
      }
    }
    if (row) {
      window.addEventListener('keydown', handleKeyDown)
    }
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [row, onClose])

  if (!row) return null

  // Normalize arrays once so every downstream consumer (incl. deriveTenant) is undefined-safe.
  const safeAgents = agents || []
  const safeSessions = sessions || []
  const safeIncidents = incidents || []

  const derivedTenant = deriveTenant(row.dimension_key, safeAgents, safeSessions, safeIncidents)
  const tenantStyles = getTenantStyles(derivedTenant || '')

  // Filter and sort sessions
  const filteredSessions = safeSessions.filter(
    (s) => s.harness.toLowerCase() === row.dimension_key.toLowerCase()
  )
  const sortedSessions = [...filteredSessions].sort((a, b) => {
    const aRunning = a.status.toLowerCase() === 'running' ? 1 : 0
    const bRunning = b.status.toLowerCase() === 'running' ? 1 : 0
    return bRunning - aRunning
  })

  // Filter incidents
  const filteredIncidents = safeIncidents.filter(
    (i) => i.harness.toLowerCase() === row.dimension_key.toLowerCase()
  )

  return (
    <div
      className={`fixed inset-0 z-50 flex justify-end bg-black/60 backdrop-blur-sm transition-opacity duration-[var(--duration-base)] ${
        isOpen ? 'opacity-100' : 'opacity-0'
      }`}
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="agent-detail-title"
        className={`w-full max-w-[460px] bg-[var(--bg-surface)] border-l border-[var(--border-subtle)] shadow-[var(--shadow-float)] flex flex-col h-full transform transition-transform duration-[var(--duration-base)] ${
          isOpen ? 'translate-x-0' : 'translate-x-full'
        }`}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-start justify-between border-b border-[var(--border-subtle)] p-6 flex-shrink-0">
          <div className="space-y-1.5 min-w-0">
            <h2 id="agent-detail-title" className="text-2xl font-extrabold tracking-tight text-[var(--text-primary)] truncate">
              {row.dimension_key}
            </h2>
            <div className="flex items-center gap-2 flex-wrap">
              {row.billing_mode === 'metered' && row.total_cost_usd !== null ? (
                <span className={`px-1.5 py-0.5 rounded text-[9px] font-semibold font-mono uppercase tracking-wider shrink-0 ${tenantStyles.chip}`}>
                  {row.provider ? `${row.provider} · ` : ''}metered · {formatCurrency(row.total_cost_usd)}
                </span>
              ) : row.billing_mode === 'subscription' ? (
                <span className={`px-1.5 py-0.5 rounded text-[9px] font-semibold font-mono uppercase tracking-wider shrink-0 ${tenantStyles.chip}`}>
                  {row.provider ? `${row.provider} · ` : ''}subscription
                </span>
              ) : (
                <span className="px-1.5 py-0.5 rounded text-[9px] font-semibold font-mono uppercase tracking-wider bg-white/5 text-[var(--text-secondary)] border border-white/10 shrink-0">
                  {row.provider ? `${row.provider} · ` : ''}usage-only
                </span>
              )}
              {derivedTenant && (
                <span className={`px-1.5 py-0.5 rounded text-[9px] font-semibold font-mono uppercase tracking-wider shrink-0 ${tenantStyles.chip}`}>
                  {tenantStyles.label}
                </span>
              )}
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-1 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-white/5 transition-colors cursor-pointer shrink-0"
            aria-label="Close drawer"
          >
            <Icon name="close" size={20} />
          </button>
        </div>

        {/* Scrollable Content */}
        <div className="flex-1 overflow-y-auto divide-y divide-[var(--border-subtle)]">
          {/* Usage stat strip */}
          <div className="p-6 space-y-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
              Usage Overview
            </h3>
            <div className="glass-card p-4 bg-gradient-to-br from-white/5 to-transparent border border-[var(--border-subtle)] flex items-center justify-between">
              <div className="space-y-0.5">
                <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                  Total Tokens
                </span>
                <p className="text-3xl font-extrabold tracking-tight text-[var(--text-primary)]">
                  {formatTokens(row.total_tokens)}
                </p>
                <p className="text-xs text-[var(--text-muted)] font-mono">
                  {row.total_turns} turns · {row.session_count} sessions
                </p>
              </div>
              {row.billing_mode === 'metered' && row.total_cost_usd !== null && (
                <div className="text-right space-y-0.5">
                  <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Metered Cost
                  </span>
                  <p className="text-3xl font-extrabold tracking-tight text-[var(--text-primary)]">
                    {formatCurrency(row.total_cost_usd)}
                  </p>
                </div>
              )}
            </div>
          </div>

          {/* Live sessions */}
          <div className="p-6 space-y-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
              Live Sessions ({sortedSessions.length})
            </h3>
            {sortedSessions.length === 0 ? (
              <div className="flex flex-col items-center justify-center p-6 border border-dashed border-[var(--border-subtle)] rounded-lg text-center space-y-1.5 bg-white/5">
                <Icon name="smart_toy" className="text-[var(--text-muted)]" size={24} />
                <p className="text-xs text-[var(--text-secondary)] font-medium">No live sessions</p>
              </div>
            ) : (
              <div className="space-y-2">
                {sortedSessions.map((session, idx) => {
                  const statusStyles = getSessionStatusStyles(session.status)
                  const sessionTenantStyles = getTenantStyles(session.tenant)
                  return (
                    <div
                      key={`${session.session_id}-${idx}`}
                      className="flex items-start justify-between p-3 border border-[var(--border-subtle)] rounded-lg bg-[var(--bg-surface)] hover:bg-[var(--bg-hover)] transition-colors gap-3"
                    >
                      <div className="flex items-start gap-2.5 min-w-0">
                        <span
                          className={`w-2 h-2 rounded-full mt-1.5 shrink-0 ${statusStyles.dot}`}
                          title={session.status}
                        />
                        <div className="min-w-0 space-y-0.5">
                          <div className="flex items-center gap-1.5 flex-wrap">
                            <span
                              className="text-xs font-semibold text-[var(--text-primary)] font-mono truncate"
                              title={session.session_id}
                            >
                              ID: {session.session_id.slice(0, 8)}
                            </span>
                            <span className="text-[10px] text-[var(--text-muted)] font-mono truncate">
                              host: {session.host || 'unknown'}
                            </span>
                          </div>
                          {session.last_event_kind && (
                            <p className="text-[10px] text-[var(--text-secondary)] font-mono bg-white/5 border border-white/5 rounded px-1 py-0.5 truncate">
                              Event: {session.last_event_kind}
                            </p>
                          )}
                        </div>
                      </div>
                      <div className="flex flex-col items-end gap-1.5 shrink-0">
                        <span className={`text-[9px] px-1.5 py-0.5 rounded-full font-semibold font-mono uppercase tracking-wider ${sessionTenantStyles.chip}`}>
                          {sessionTenantStyles.label}
                        </span>
                        <span className="text-[10px] text-[var(--text-muted)]">
                          {timeAgo(session.last_event_at)}
                        </span>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>

          {/* Incidents */}
          <div className="p-6 space-y-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
              Incidents ({filteredIncidents.length})
            </h3>
            {filteredIncidents.length === 0 ? (
              <div className="flex flex-col items-center justify-center p-6 border border-dashed border-[var(--border-subtle)] rounded-lg text-center space-y-1.5 bg-white/5">
                <Icon name="check_circle" className="text-emerald-500" size={24} />
                <p className="text-xs text-[var(--text-secondary)] font-medium">No incidents — all clear</p>
              </div>
            ) : (
              <div className="space-y-2">
                {filteredIncidents.map((incident, idx) => {
                  const severityColor = getIncidentStatusColor(incident.status)
                  const incidentTenantStyles = getTenantStyles(incident.tenant)
                  return (
                    <div
                      key={`${incident.session_id}-${idx}`}
                      className="flex flex-col p-3 border border-[var(--border-subtle)] rounded-lg bg-[var(--bg-surface)] hover:bg-[var(--bg-hover)] transition-colors gap-2"
                    >
                      <div className="flex items-start justify-between gap-3">
                        <span className="text-xs font-semibold text-[var(--text-primary)]">
                          {incident.title}
                        </span>
                        <span className={`text-[9px] uppercase font-bold tracking-wider px-1.5 py-0.5 rounded-full shrink-0 ${severityColor}`}>
                          {incident.status}
                        </span>
                      </div>
                      <div className="flex justify-between items-center text-[10px] text-[var(--text-muted)] flex-wrap gap-2 pt-1 border-t border-white/5">
                        <div className="flex items-center gap-1.5 font-mono truncate">
                          {incident.project_slug && (
                            <span className="truncate">proj: {incident.project_slug}</span>
                          )}
                          {incident.project_slug && incident.branch && <span>·</span>}
                          {incident.branch && (
                            <span className="truncate">branch: {incident.branch}</span>
                          )}
                        </div>
                        <div className="flex items-center gap-2">
                          <span className={`text-[8px] px-1.5 py-0.5 rounded-full font-semibold font-mono uppercase tracking-wider ${incidentTenantStyles.chip}`}>
                            {incidentTenantStyles.label}
                          </span>
                          <span>{timeAgo(incident.received_at)}</span>
                        </div>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
