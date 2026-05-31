import { useState } from 'react'
import type { WorkUnit } from '../../api/client'
import { livenessOf } from '../../hooks/useWorkUnits'
import { Icon } from '../Icon'
import { TrackerBadge, LivenessDot, StatusPill } from './TrackerBadge'

const harnessColor: Record<string, string> = {
  hermes: 'var(--accent-blue)',
  claude: 'var(--accent-blue)',
  codex: 'var(--accent-cyan)',
  antigravity: 'var(--accent-pink)',
  generic: 'var(--color-text-muted)',
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

function duration(firstIso: string, lastIso: string): string {
  const a = Date.parse(firstIso), b = Date.parse(lastIso)
  if (Number.isNaN(a) || Number.isNaN(b) || b < a) return '—'
  const s = Math.round((b - a) / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m`
}

export function WorkUnitCard({ unit }: { unit: WorkUnit }) {
  const [open, setOpen] = useState(false)
  const state = livenessOf(unit)
  const title = unit.title || unit.branch || unit.external_ref || '(uncorrelated work)'
  const harnesses = unit.harnesses ?? []
  const cost = typeof unit.cost_usd === 'number' ? `$${unit.cost_usd.toFixed(2)}` : null

  return (
    <div
      className="glass-card p-4 flex flex-col gap-2.5 cursor-pointer border border-[var(--border-subtle)] hover:border-[var(--border-glow)] transition-colors"
      onClick={() => setOpen((o) => !o)}
    >
      {/* top row */}
      <div className="flex items-center gap-2.5">
        <LivenessDot state={state} />
        <span className="font-semibold text-sm text-[var(--color-text-primary)] flex-1 min-w-0 truncate">
          {title}
        </span>
        <TrackerBadge externalRef={unit.external_ref} />
        <StatusPill state={state} />
      </div>

      {/* meta row */}
      <div className="flex items-center gap-3 text-xs text-[var(--color-text-secondary)] flex-wrap">
        {(unit.branch || unit.sha) && (
          <span className="font-mono text-[11px] px-2 py-0.5 rounded bg-[var(--bg-elevated)] text-[var(--color-text-secondary)]">
            {unit.branch}{unit.sha ? ` · ${unit.sha.slice(0, 7)}` : ''}
          </span>
        )}
        {harnesses.map((h) => (
          <span key={h} className="inline-flex items-center gap-1.5 text-[11px] px-2 py-0.5 rounded-full bg-[var(--bg-elevated)]">
            <span className="w-[7px] h-[7px] rounded-sm" style={{ backgroundColor: harnessColor[h] || harnessColor.generic }} />
            {h}
          </span>
        ))}
        <span className="inline-flex items-center gap-1.5 text-[var(--color-text-muted)]">
          <Icon name="bar_chart" size={13} />
          {unit.event_count} events{unit.session_count > 1 ? ` · ${unit.session_count} sessions` : ''}
        </span>
        <span className="inline-flex items-center gap-1.5 text-[var(--color-text-muted)]">
          <Icon name="schedule" size={13} />
          {state === 'running' ? `started ${relTime(unit.first_event_at)}` : duration(unit.first_event_at, unit.last_event_at)}
        </span>
        {cost && <span className="ml-auto font-semibold text-[var(--color-text-secondary)]">{cost}</span>}
      </div>

      {/* expanded detail */}
      {open && (
        <div className="border-t border-[var(--border-subtle)] mt-1 pt-2.5 flex flex-col gap-1.5 text-xs">
          <DetailRow label="tenant" value={unit.tenant} />
          <DetailRow label="external_ref" value={unit.external_ref || '—'} />
          <DetailRow label="branch" value={unit.branch || '—'} />
          <DetailRow label="sha" value={unit.sha || '—'} />
          <DetailRow label="first event" value={`${relTime(unit.first_event_at)} (${unit.first_event_at})`} />
          <DetailRow label="last event" value={`${relTime(unit.last_event_at)} (${unit.last_event_at})`} />
          {unit.latest_kind && <DetailRow label="latest kind" value={unit.latest_kind} />}
          <DetailRow label="sessions" value={`${unit.session_count} total · ${unit.active_session_count} active`} />
          <p className="text-[10px] text-[var(--color-text-muted)] italic mt-1">
            Liveness is computed server-side per session from received_at (the only liveness clock), aggregated failed&gt;running&gt;stale&gt;done. Never a stored flag.
          </p>
        </div>
      )}
    </div>
  )
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-3">
      <span className="font-mono text-[var(--color-text-muted)] w-28 shrink-0">{label}</span>
      <span className="text-[var(--color-text-secondary)] break-all">{value}</span>
    </div>
  )
}
