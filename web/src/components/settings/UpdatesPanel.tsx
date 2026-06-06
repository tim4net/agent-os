import { useMemo } from 'react'
import type { Agent } from '../../api/client'
import { hasVersion } from '../../api/agentVersion'
import { useAgentVersions } from '../../hooks/useAgentVersions'
import { VersionChip } from '../agents/VersionChip'
import { Icon } from '../Icon'

interface UpdatesPanelProps {
  agents: Agent[]
  loading: boolean
}

function relativeTime(dateStr: string | undefined): string {
  if (!dateStr) return '—'
  const t = new Date(dateStr).getTime()
  if (Number.isNaN(t)) return '—'
  const secs = Math.floor((Date.now() - t) / 1000)
  if (secs < 60) return 'just now'
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

/**
 * Global Updates panel — a single-pane view of every agent's reported version.
 *
 * Read-only by design (Slice 1 is version VISIBILITY; the apply/update action
 * is a later, separately-gated increment). Versions are probed per-agent and
 * degrade honestly to "unknown" when a backend doesn't report one — the panel
 * shows that as a muted state rather than hiding the row, so the fleet view
 * never lies about coverage.
 */
export function UpdatesPanel({ agents, loading }: UpdatesPanelProps) {
  const { versions, refresh } = useAgentVersions(agents.map((a) => a.id))

  const { known, unknown, probing } = useMemo(() => {
    let known = 0
    let unknown = 0
    let probing = 0
    for (const a of agents) {
      const st = versions[a.id]
      if (!st || st.loading) probing++
      else if (hasVersion(st.version)) known++
      else unknown++
    }
    return { known, unknown, probing }
  }, [agents, versions])

  return (
    <section className="fade-in">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3
            className="text-sm font-medium uppercase tracking-wider mb-1"
            style={{ color: 'var(--text-muted)' }}
          >
            Updates
          </h3>
          <p className="text-xs" style={{ color: 'var(--text-muted)' }}>
            Versions reported by every agent's backing service. Read-only — derived live, never stored.
          </p>
        </div>
        <button
          onClick={refresh}
          className="pill-btn pill-btn--ghost text-xs py-1 px-3 flex items-center gap-1.5"
          aria-label="Re-check versions"
        >
          <Icon name="refresh" size={14} />
          Re-check
        </button>
      </div>

      {/* stat strip */}
      <div className="grid grid-cols-3 gap-3 mb-5">
        <StatCard label="Reporting" value={known} tone="var(--accent-cyan)" />
        <StatCard label="Unknown" value={unknown} tone="var(--color-text-muted)" />
        <StatCard label="Probing" value={probing} tone="var(--accent-blue)" />
      </div>

      {loading ? (
        <div className="space-y-2">
          <div className="shimmer h-12 w-full rounded-lg" />
          <div className="shimmer h-12 w-full rounded-lg" />
          <div className="shimmer h-12 w-full rounded-lg" />
        </div>
      ) : agents.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--text-muted)' }}>
          No agents registered.
        </p>
      ) : (
        <div className="glass-card divide-y divide-[var(--border-subtle)] overflow-hidden">
          {agents.map((agent) => {
            const st = versions[agent.id]
            return (
              <div
                key={agent.id}
                className="flex items-center justify-between gap-3 px-4 py-3 hover:bg-[var(--bg-hover)] transition-colors"
              >
                <div className="min-w-0 flex items-center gap-2.5">
                  <span
                    className={`inline-block w-2 h-2 rounded-full shrink-0 ${
                      agent.status === 'online' ? 'bg-emerald-400' : 'bg-gray-500'
                    }`}
                  />
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-[var(--text-primary)] truncate">
                      {agent.display_name || agent.name}
                    </p>
                    <p className="text-[10px] font-mono text-[var(--color-text-muted)]/70 truncate">
                      {agent.harness}
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-3 shrink-0">
                  {st?.error ? (
                    <span
                      className="text-[10px] font-mono text-red-400/80"
                      title={st.error}
                    >
                      probe failed
                    </span>
                  ) : (
                    <>
                      <VersionChip version={st?.version ?? null} loading={st?.loading ?? true} />
                      <span className="text-[10px] text-[var(--color-text-muted)]/50 w-16 text-right">
                        {st?.loading ? '' : relativeTime(st?.version?.checked_at)}
                      </span>
                    </>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}

function StatCard({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div className="glass-card p-3 flex flex-col gap-0.5">
      <span
        className="text-[10px] font-semibold uppercase tracking-wider"
        style={{ color: 'var(--text-muted)' }}
      >
        {label}
      </span>
      <span className="text-2xl font-extrabold tracking-tight" style={{ color: tone }}>
        {value}
      </span>
    </div>
  )
}
