import { useState } from 'react'
import type { WorkUnit } from '../../api/client'
import { Icon } from '../Icon'

/** UncorrelatedBucket — events not tied to a work unit. Intentionally second-class:
 *  dim, dashed, collapsed by default, so the eye trusts the correlated feed. */
export function UncorrelatedBucket({ units }: { units: WorkUnit[] }) {
  const [open, setOpen] = useState(false)
  if (units.length === 0) return null
  const eventTotal = units.reduce((n, u) => n + (u.event_count || 0), 0)

  return (
    <div className="mt-6 opacity-80">
      <div className="glass-card border-dashed" style={{ borderStyle: 'dashed' }}>
        <button
          className="w-full flex items-center gap-2.5 px-4 py-3 text-[var(--color-text-secondary)] text-left"
          onClick={() => setOpen((o) => !o)}
        >
          <Icon name={open ? 'expand_more' : 'chevron_right'} size={16} />
          <Icon name="link_off" size={15} />
          <span className="font-medium text-sm">Uncorrelated events</span>
          <span className="ml-auto text-[11px] text-[var(--color-text-muted)]">
            {eventTotal} event{eventTotal === 1 ? '' : 's'} · no external_ref / branch — not yet tied to a work unit
          </span>
        </button>
        {open && (
          <div className="px-4 pb-3 flex flex-col">
            {units.map((u, i) => (
              <div
                key={`${u.tenant}-${i}`}
                className="flex items-center gap-3 text-xs text-[var(--color-text-muted)] py-2 border-t border-[var(--border-subtle)]"
              >
                {(u.harnesses ?? []).map((h) => (
                  <span key={h} className="font-mono">{h}</span>
                ))}
                <span className="font-mono">{u.latest_kind || 'event'}</span>
                <span className="truncate flex-1">{u.title || '(no context)'}</span>
                <span className="ml-auto">{u.event_count} ev</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
