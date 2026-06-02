import type { ControlUnit } from '../../hooks/useControlUnits'
import { Icon } from '../Icon'

type UnitStatus = ControlUnit['status']

const statusPillColor: Record<UnitStatus, string> = {
  queued: 'bg-[var(--accent-blue)]/15 text-[var(--accent-blue)]',
  running: 'bg-emerald-500/15 text-emerald-400',
  done: 'bg-[var(--color-text-muted)]/20 text-[var(--color-text-secondary)]',
  failed: 'bg-[#f87171]/15 text-[#f87171]',
}

function relTime(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return `${s}s ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m ago`
}

interface QueuePanelProps {
  units: ControlUnit[]
  loading: boolean
  error: string | null
  onRequeue: (id: string) => void
  requeueLoading: string | null
}

export function QueuePanel({ units, loading, error, onRequeue, requeueLoading }: QueuePanelProps) {
  if (loading && units.length === 0) {
    return (
      <div className="glass-card p-6 text-center text-[var(--color-text-muted)]">
        Loading work units…
      </div>
    )
  }

  if (error) {
    return (
      <div className="glass-card p-4 text-[#f87171] text-sm">{error}</div>
    )
  }

  if (units.length === 0) {
    return (
      <div className="glass-card p-8 text-center text-[var(--color-text-muted)]">
        <Icon name="inbox" size={28} />
        <p className="mt-2 text-sm">No work units in the queue.</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      {units.map((unit) => (
        <UnitRow
          key={unit.id}
          unit={unit}
          onRequeue={onRequeue}
          requeueLoading={requeueLoading}
        />
      ))}
    </div>
  )
}

function UnitRow({ unit, onRequeue, requeueLoading }: {
  unit: ControlUnit
  onRequeue: (id: string) => void
  requeueLoading: string | null
}) {
  const canRequeue = unit.status === 'failed' || unit.status === 'done'

  return (
    <div className="glass-card p-3 flex items-center gap-3 border border-[var(--border-subtle)] hover:border-[var(--glass-border)] transition-colors">
      {/* Status pill */}
      <span className={`text-[10px] font-bold uppercase tracking-wide px-2 py-0.5 rounded-full shrink-0 ${statusPillColor[unit.status]}`}>
        {unit.status}
      </span>

      {/* wp_ref */}
      <span className="font-mono text-xs text-[var(--text-primary)] flex-1 min-w-0 truncate">
        {unit.wp_ref}
      </span>

      {/* Timestamps */}
      <span className="text-[11px] text-[var(--color-text-muted)] shrink-0 flex items-center gap-1">
        <Icon name="schedule" size={12} />
        {relTime(unit.updated_at)}
      </span>

      {/* Error text for failed units */}
      {unit.status === 'failed' && unit.error && (
        <span className="text-[11px] text-[#f87171] truncate max-w-[200px]" title={unit.error}>
          {unit.error}
        </span>
      )}

      {/* Requeue button */}
      {canRequeue && (
        <button
          onClick={() => onRequeue(unit.id)}
          disabled={requeueLoading === unit.id}
          className="px-3 py-1 rounded-full text-[11px] font-semibold bg-[var(--accent-blue)]/15 text-[var(--accent-blue)] border border-[var(--accent-blue)]/30 hover:bg-[var(--accent-blue)]/25 transition-colors disabled:opacity-40 shrink-0"
        >
          {requeueLoading === unit.id ? '…' : 'Requeue'}
        </button>
      )}
    </div>
  )
}
