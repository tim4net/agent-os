import type { Liveness } from '../../hooks/useWorkUnits'

/** TrackerBadge — renders an external_ref as a colored pill.
 *  SC-#### => Shortcut (purple); #### => GitHub issue (blue). */
export function TrackerBadge({ externalRef }: { externalRef: string | null }) {
  if (!externalRef) return null
  const isShortcut = /^SC-\d+$/i.test(externalRef)
  const cls = isShortcut
    ? 'bg-[var(--accent-purple)]/20 text-[var(--accent-purple)]'
    : 'bg-[var(--accent-blue)]/20 text-[var(--accent-blue)]'
  return (
    <span className={`text-[11px] font-bold px-2 py-0.5 rounded-full shrink-0 ${cls}`}>
      {externalRef}
    </span>
  )
}

const livenessColor: Record<Liveness, string> = {
  running: 'var(--accent-blue)',
  done: 'var(--color-text-muted)',
  stale: '#fbbf24',
  failed: '#f87171',
}

const livenessRunning: Record<Liveness, boolean> = {
  running: true, done: false, stale: false, failed: false,
}

/** LivenessDot — colored status dot; pulses while running. */
export function LivenessDot({ state }: { state: Liveness }) {
  return (
    <span
      className="relative inline-block w-2.5 h-2.5 rounded-full shrink-0"
      style={{
        backgroundColor: livenessColor[state],
        boxShadow: state !== 'done' ? `0 0 0 4px ${livenessColor[state]}28` : undefined,
      }}
      aria-label={`status: ${state}`}
      title={`status: ${state}`}
    >
      {livenessRunning[state] && (
        <span
          className="absolute inset-0 rounded-full animate-ping"
          style={{ border: `1px solid ${livenessColor[state]}` }}
        />
      )}
    </span>
  )
}

const statusPillColor: Record<Liveness, string> = {
  running: 'bg-[var(--accent-blue)]/15 text-[var(--accent-blue)]',
  done: 'bg-[var(--color-text-muted)]/20 text-[var(--color-text-secondary)]',
  stale: 'bg-[#fbbf24]/15 text-[#fbbf24]',
  failed: 'bg-[#f87171]/15 text-[#f87171]',
}

/** StatusPill — uppercase text pill mirroring the liveness state. */
export function StatusPill({ state }: { state: Liveness }) {
  return (
    <span className={`text-[10px] font-bold uppercase tracking-wide px-2 py-0.5 rounded-full shrink-0 ${statusPillColor[state]}`}>
      {state}
    </span>
  )
}
