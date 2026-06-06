import type { AgentVersion, VersionSource } from '../../api/agentVersion'
import { hasVersion } from '../../api/agentVersion'

interface VersionChipProps {
  version: AgentVersion | null
  loading?: boolean
}

// Provenance → short label + accent. Reuses the existing aurora accent tokens;
// introduces no new color system (ADR-007 D5 fidelity rule). The accent is a
// subtle left tint only — the chip stays in the muted mono-tag family used for
// the harness tag on the same card.
const SOURCE_META: Record<VersionSource, { label: string; tone: string }> = {
  'hello-ok': { label: 'gateway', tone: 'var(--accent-cyan)' },
  health: { label: 'health', tone: 'var(--accent-blue)' },
  openapi: { label: 'openapi', tone: 'var(--accent-purple)' },
  http: { label: 'http', tone: 'var(--accent-blue)' },
  cli: { label: 'cli', tone: 'var(--accent-blue)' },
  unknown: { label: 'unknown', tone: 'var(--color-text-muted)' },
}

/**
 * Compact, monospace version chip for an agent card.
 *
 * - loading  → a quiet shimmer pill (probe in flight; can take a few seconds)
 * - known    → `v{current}` tinted by provenance, with the source in the title
 * - unknown  → a muted "v —" so the absence reads as honest "couldn't determine"
 *   rather than a bug. (Matches the F10 honesty idiom: never assert what we
 *   don't know.)
 */
export function VersionChip({ version, loading }: VersionChipProps) {
  if (loading) {
    return (
      <span
        className="shimmer inline-block h-[15px] w-12 rounded"
        aria-label="Loading version"
      />
    )
  }

  if (!hasVersion(version)) {
    return (
      <span
        className="text-[10px] font-mono text-[var(--color-text-muted)]/50 bg-[var(--bg-elevated)]/40 px-1.5 py-0.5 rounded"
        title="Version unavailable — this agent does not report one"
      >
        v —
      </span>
    )
  }

  const meta = SOURCE_META[version.source] ?? SOURCE_META.unknown
  return (
    <span
      className="text-[10px] font-mono px-1.5 py-0.5 rounded border"
      style={{
        color: 'var(--color-text-secondary)',
        borderColor: 'color-mix(in srgb, ' + meta.tone + ' 35%, transparent)',
        backgroundColor: 'color-mix(in srgb, ' + meta.tone + ' 10%, transparent)',
      }}
      title={`Version ${version.current} · source: ${meta.label}`}
    >
      v{version.current}
    </span>
  )
}
