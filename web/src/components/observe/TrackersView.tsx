import { useEffect, useState } from 'react'
import type { TrackerItem } from '../../api/client'
import { getTrackerItems } from '../../api/client'
import { Icon } from '../Icon'
import { TrackerBadge } from './TrackerBadge'

function syncedRel(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return `${s}s ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  return `${Math.floor(m / 60)}h ago`
}

/** TrackersView — read-only mirror of tracker_items. NO edit controls by design:
 *  the plane never writes back to trackers (ADR-001 D4). */
export function TrackersView({ tenant }: { tenant: string }) {
  const [items, setItems] = useState<TrackerItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getTrackerItems({ tenant, limit: 200 })
      .then((res) => { if (!cancelled) { setItems(res.items ?? []); setError(null) } })
      .catch((e: unknown) => { if (!cancelled) setError(e instanceof Error ? e.message : 'failed to load tracker items') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [tenant])

  return (
    <div className="p-6">
      <div className="flex items-center gap-2 mb-3">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-[var(--color-text-secondary)]">
          Tracker mirror
        </h3>
        <span className="text-[10px] font-bold uppercase tracking-wide px-2 py-0.5 rounded-full bg-[var(--bg-elevated)] text-[var(--color-text-muted)]">
          read-only
        </span>
        <span className="text-[11px] text-[var(--color-text-muted)]">
          Agent OS never writes back to trackers (ADR-001 D4)
        </span>
      </div>

      {loading && <div className="glass-card p-6 text-center text-[var(--color-text-muted)]">Loading…</div>}
      {error && <div className="glass-card p-4 text-[#f87171] text-sm">{error}</div>}

      {!loading && !error && items.length === 0 && (
        <div className="glass-card p-6 text-center text-[var(--color-text-muted)] text-sm">
          No mirrored tracker items for tenant <span className="font-mono">{tenant}</span>.
          <div className="text-[11px] mt-1">Configure a project with a tracker source and run a sync.</div>
        </div>
      )}

      {!loading && !error && items.length > 0 && (
        <div className="glass-card overflow-hidden p-0">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr>
                {['Ref', 'Title', 'Source', 'Status', 'Synced'].map((h) => (
                  <th key={h} className="text-left text-[11px] uppercase tracking-wide text-[var(--color-text-muted)] font-semibold px-3.5 py-2.5 border-b border-[var(--border-subtle)]">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {items.map((it) => (
                <tr key={it.id} className="hover:bg-[var(--bg-hover)]">
                  <td className="px-3.5 py-2.5 border-b border-[var(--border-subtle)]">
                    {it.canonical_url ? (
                      <a href={it.canonical_url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
                        <TrackerBadge externalRef={it.external_ref} />
                      </a>
                    ) : (
                      <TrackerBadge externalRef={it.external_ref} />
                    )}
                  </td>
                  <td className="px-3.5 py-2.5 border-b border-[var(--border-subtle)] text-[var(--color-text-secondary)]">{it.title}</td>
                  <td className="px-3.5 py-2.5 border-b border-[var(--border-subtle)] text-[var(--color-text-muted)] font-mono text-[11px]">{it.item_type ? it.item_type : ''}</td>
                  <td className="px-3.5 py-2.5 border-b border-[var(--border-subtle)] text-[var(--color-text-secondary)]">{it.status}</td>
                  <td className="px-3.5 py-2.5 border-b border-[var(--border-subtle)]">
                    <span className="inline-flex items-center gap-1.5 text-[10px] text-[var(--color-text-muted)]">
                      <Icon name="sync" size={12} />
                      mirror · {syncedRel(it.synced_at)}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="text-[11px] text-[var(--color-text-muted)] italic mt-4">
        Non-authoritative mirror. The tracker is the source of truth; this is a read snapshot with synced_at. No edit controls — by design the plane has no write path.
      </p>
    </div>
  )
}
