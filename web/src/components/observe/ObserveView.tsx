import { useMemo, useState } from 'react'
import { useWorkUnits } from '../../hooks/useWorkUnits'
import { Icon } from '../Icon'
import { WorkUnitCard } from './WorkUnitCard'
import { UncorrelatedBucket } from './UncorrelatedBucket'
import { TrackersView } from './TrackersView'

type SubView = 'activity' | 'trackers'

/** Default tenant. Mixing tenants in one glance is what the ADR-002 wall prevents,
 *  so we default to 'personal' and offer an explicit switcher. */
const DEFAULT_TENANT = 'personal'

export function ObserveView() {
  const [sub, setSub] = useState<SubView>('activity')
  const [tenant, setTenant] = useState(DEFAULT_TENANT)
  const { units, loading, error, now } = useWorkUnits(100)

  // tenant-scope client-side (the work-units API returns all tenants; the wall is
  // visual here + server-enforced on tracker reads). Personal is the default lens.
  const scoped = useMemo(() => units.filter((u) => u.tenant === tenant), [units, tenant])
  const correlated = useMemo(() => scoped.filter((u) => u.correlated), [scoped])
  const uncorrelated = useMemo(() => scoped.filter((u) => !u.correlated), [scoped])
  const tenants = useMemo(() => {
    const set = new Set<string>([DEFAULT_TENANT, ...units.map((u) => u.tenant)])
    return Array.from(set)
  }, [units])

  const stats = useMemo(() => {
    const activeSessions = correlated.filter((u) => {
      const s = (u.latest_status || '').toLowerCase()
      return s !== 'done' && s !== 'failed' && s !== 'cancelled'
    }).length
    const events = scoped.reduce((n, u) => n + (u.event_count || 0), 0)
    const spend = scoped.reduce((n, u) => n + (u.cost_usd || 0), 0)
    return { activeSessions, events, spend, uncorrelated: uncorrelated.length }
  }, [correlated, scoped, uncorrelated])

  return (
    <div className="flex flex-col h-full">
      {/* header */}
      <div className="px-6 py-4 border-b border-[var(--border-subtle)] flex-shrink-0 flex items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-bold text-[var(--text-primary)]">Observe</h2>
          <p className="text-xs text-[var(--color-text-muted)] mt-0.5">
            The work record across every harness — derived from the event stream, not asserted.
          </p>
        </div>
        <div className="flex items-center gap-3">
          {/* tenant switcher */}
          <label className="flex items-center gap-2 text-xs text-[var(--color-text-secondary)] bg-[var(--bg-card)] border border-[var(--glass-border)] rounded-full px-3 py-1.5">
            <Icon name="workspaces" size={14} />
            tenant
            <select
              value={tenant}
              onChange={(e) => setTenant(e.target.value)}
              className="bg-transparent font-semibold text-[var(--color-text-primary)] outline-none cursor-pointer"
            >
              {tenants.map((t) => <option key={t} value={t}>{t}</option>)}
            </select>
          </label>
          {/* sub-tabs */}
          <div className="flex gap-1.5">
            {(['activity', 'trackers'] as SubView[]).map((s) => (
              <button
                key={s}
                onClick={() => setSub(s)}
                className={`px-4 py-1.5 rounded-full text-[13px] font-semibold capitalize transition-colors ${
                  sub === s
                    ? 'bg-[var(--bg-elevated)] text-[var(--color-text-primary)] border border-[var(--glass-border)]'
                    : 'text-[var(--color-text-secondary)] hover:bg-[var(--bg-hover)] border border-transparent'
                }`}
              >
                {s}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* body */}
      <div className="flex-1 min-h-0 overflow-auto">
        {sub === 'trackers' ? (
          <TrackersView tenant={tenant} />
        ) : (
          <div className="p-6">
            {/* stat row */}
            <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
              <StatCard label="Active Sessions" value={String(stats.activeSessions)} icon="bolt" accent="emerald" />
              <StatCard label="Events" value={stats.events.toLocaleString()} icon="bar_chart" accent="blue" />
              <StatCard label="Spend" value={`$${stats.spend.toFixed(2)}`} icon="payments" accent="amber" />
              <StatCard label="Uncorrelated" value={String(stats.uncorrelated)} icon="link_off" accent="gray" />
            </div>

            <h3 className="text-sm font-semibold uppercase tracking-wider text-[var(--color-text-secondary)] mb-3 flex items-center gap-2">
              Work Units
              <span className="text-[var(--color-text-muted)] font-normal normal-case text-xs">
                · correlated by (project · external_ref · branch · sha · tenant)
              </span>
            </h3>

            {loading && <div className="glass-card p-6 text-center text-[var(--color-text-muted)]">Loading work units…</div>}
            {error && <div className="glass-card p-4 text-[#f87171] text-sm">{error}</div>}

            {!loading && !error && correlated.length === 0 && uncorrelated.length === 0 && (
              <div className="glass-card p-8 text-center text-[var(--color-text-muted)]">
                <Icon name="radar" size={28} />
                <p className="mt-2 text-sm">No work events for tenant <span className="font-mono">{tenant}</span> yet.</p>
                <p className="text-[11px] mt-1">Point an emitter at this instance and activity will appear here live.</p>
              </div>
            )}

            <div className="flex flex-col gap-3">
              {correlated.map((u, i) => (
                <WorkUnitCard key={`${u.external_ref}-${u.branch}-${u.sha}-${i}`} unit={u} now={now} />
              ))}
            </div>

            <UncorrelatedBucket units={uncorrelated} />

            <p className="text-[11px] text-[var(--color-text-muted)] italic mt-5">
              Updates live over SSE. Liveness is computed from received_at + the 5-min heartbeat rule, never a stored flag (F10).
            </p>
          </div>
        )}
      </div>
    </div>
  )
}

function StatCard({ label, value, icon, accent }: { label: string; value: string; icon: string; accent: string }) {
  const colorMap: Record<string, string> = {
    emerald: 'from-emerald-500/10 to-transparent border-emerald-500/20',
    blue: 'from-blue-500/10 to-transparent border-blue-500/20',
    amber: 'from-amber-500/10 to-transparent border-amber-500/20',
    gray: 'from-gray-500/10 to-transparent border-gray-500/20',
  }
  return (
    <div className={`glass-card p-4 bg-gradient-to-br ${colorMap[accent] || colorMap.gray}`}>
      <div className="flex items-center gap-2 mb-1">
        <Icon name={icon} size={16} />
        <span className="text-xs text-[var(--color-text-muted)]">{label}</span>
      </div>
      <p className="text-2xl font-bold text-[var(--color-text-primary)]">{value}</p>
    </div>
  )
}
