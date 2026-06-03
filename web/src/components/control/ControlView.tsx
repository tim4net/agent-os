import { useState, useCallback } from 'react'
import { useControlState } from '../../hooks/useControlState'
import { useControlUnits } from '../../hooks/useControlUnits'
import { Icon } from '../Icon'
import { ModeControls } from './ModeControls'
import { QueuePanel } from './QueuePanel'

type StatusFilter = 'all' | 'queued' | 'in_flight' | 'done' | 'failed'
const STATUS_FILTERS: StatusFilter[] = ['all', 'queued', 'in_flight', 'done', 'failed']

/** Display labels for filter buttons. */
const STATUS_DISPLAY: Record<StatusFilter, string> = {
  all: 'All',
  queued: 'Queued',
  in_flight: 'In flight',
  done: 'Done',
  failed: 'Failed',
}

export function ControlView() {
  const { state, loading: stateLoading, error: stateError, refetch: refetchState } = useControlState()
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const { units, loading: unitsLoading, error: unitsError, refetch: refetchUnits } = useControlUnits(
    statusFilter === 'all' ? undefined : statusFilter,
  )
  const [requeueLoading, setRequeueLoading] = useState<number | null>(null)
  const [requeueError, setRequeueError] = useState<string | null>(null)

  const handleRequeue = useCallback(async (id: number) => {
    setRequeueLoading(id)
    setRequeueError(null)
    try {
      const res = await fetch(`/api/control/units/${id}/requeue`, { method: 'POST' })
      if (!res.ok) {
        const body = await res.text().catch(() => res.statusText)
        throw new Error(`Requeue failed (${res.status}): ${body}`)
      }
      refetchUnits()
      refetchState()
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Requeue failed'
      setRequeueError(msg)
    } finally {
      setRequeueLoading(null)
    }
  }, [refetchUnits, refetchState])

  return (
    <div className="flex flex-col h-full">
      {/* header */}
      <div className="px-6 py-4 border-b border-[var(--border-subtle)] flex-shrink-0 flex items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-bold text-[var(--text-primary)]">Control</h2>
          <p className="text-xs text-[var(--color-text-muted)] mt-0.5">
            Orchestrator queue and mode controls.
          </p>
        </div>
      </div>

      {/* body */}
      <div className="flex-1 min-h-0 overflow-auto p-6 flex flex-col gap-6">
        {/* State loading/error */}
        {stateLoading && !state && (
          <div className="glass-card p-6 text-center text-[var(--color-text-muted)]">Loading control state…</div>
        )}
        {stateError && (
          <div className="glass-card p-4 text-[#f87171] text-sm">{stateError}</div>
        )}

        {/* Queue counts */}
        {state && (
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
            <CountCard label="Queued" value={state.queue_counts.queued ?? 0} icon="pending" accent="blue" />
            <CountCard label="In flight" value={state.queue_counts.in_flight ?? 0} icon="play_arrow" accent="emerald" />
            <CountCard label="Done" value={state.queue_counts.done ?? 0} icon="check_circle" accent="gray" />
            <CountCard label="Failed" value={state.queue_counts.failed ?? 0} icon="error" accent="red" />
          </div>
        )}

        {/* Mode controls */}
        {state && (
          <ModeControls state={state} onModeChanged={refetchState} />
        )}

        {/* Queue list */}
        <div>
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-sm font-semibold uppercase tracking-wider text-[var(--color-text-secondary)] flex items-center gap-2">
              <Icon name="list" size={14} />
              Work Units
            </h3>
            {/* Status filter pills */}
            <div className="flex gap-1.5">
              {STATUS_FILTERS.map((s) => (
                <button
                  key={s}
                  onClick={() => setStatusFilter(s)}
                  className={`px-3 py-1 rounded-full text-[11px] font-semibold transition-colors ${
                    statusFilter === s
                      ? 'bg-[var(--bg-elevated)] text-[var(--color-text-primary)] border border-[var(--glass-border)]'
                      : 'text-[var(--color-text-secondary)] hover:bg-[var(--bg-card)] border border-transparent'
                  }`}
                >
                  {STATUS_DISPLAY[s]}
                </button>
              ))}
            </div>
          </div>
          <QueuePanel
            units={units}
            loading={unitsLoading}
            error={unitsError}
            onRequeue={handleRequeue}
            requeueLoading={requeueLoading}
            requeueError={requeueError}
          />
        </div>
      </div>
    </div>
  )
}

function CountCard({ label, value, icon, accent }: { label: string; value: number; icon: string; accent: string }) {
  const colorMap: Record<string, string> = {
    emerald: 'from-emerald-500/10 to-transparent border-emerald-500/20',
    blue: 'from-blue-500/10 to-transparent border-blue-500/20',
    red: 'from-red-500/10 to-transparent border-red-500/20',
    gray: 'from-gray-500/10 to-transparent border-gray-500/20',
  }
  return (
    <div className={`glass-card p-4 bg-gradient-to-br ${colorMap[accent] || colorMap.gray}`}>
      <div className="flex items-center gap-2 mb-1">
        <Icon name={icon} size={16} />
        <span className="text-xs text-[var(--color-text-muted)]">{label}</span>
      </div>
      <p className="text-2xl font-bold text-[var(--text-primary)]">{value}</p>
    </div>
  )
}
