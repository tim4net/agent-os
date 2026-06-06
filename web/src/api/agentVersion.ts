// Agent version probe client.
//
// Mirrors the Go VersionInfo contract (internal/harness/version.go): the API
// returns the upstream version a harness reports for its backing service, with
// provenance. The endpoint always returns 200 with source "unknown" when the
// version can't be determined (offline, no version field, unsupported harness)
// — it never 500s — so the UI degrades to a muted "—" rather than an error.
//
// Defined here (not in the shared client.ts) because client.ts is an
// integrator-owned file; the control hooks set the precedent of fetching
// directly from a feature module.

export type VersionSource =
  | 'hello-ok'
  | 'health'
  | 'cli'
  | 'http'
  | 'openapi'
  | 'unknown'

export interface AgentVersion {
  current: string // upstream-reported version; "" if unknown
  source: VersionSource
  checked_at: string // RFC3339 timestamp
}

/** True when the probe yielded a real version (non-empty, provenance known). */
export function hasVersion(v: AgentVersion | null | undefined): v is AgentVersion {
  return !!v && v.current !== '' && v.source !== 'unknown'
}

/**
 * Fetch the version a single agent's harness reports.
 *
 * Resolves to the parsed VersionInfo on 200. The endpoint returns 200 even when
 * the version is unknown, so a resolved value with source 'unknown' is the
 * normal "couldn't determine" outcome — NOT an error. Throws only on transport
 * failure or a non-200 (e.g. 404 unknown agent, 400 bad id), so callers can
 * distinguish "agent reports unknown" from "request failed".
 */
export async function getAgentVersion(
  agentId: string,
  signal?: AbortSignal,
): Promise<AgentVersion> {
  const res = await fetch(`/api/agents/${agentId}/version`, { signal })
  if (!res.ok) {
    throw new Error(`API error ${res.status}: ${res.statusText}`)
  }
  return (await res.json()) as AgentVersion
}
