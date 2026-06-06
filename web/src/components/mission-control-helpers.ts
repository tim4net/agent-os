import type { Agent, Incident, SessionStatus } from '../api/client'

export function timeAgo(iso: string): string {
  if (!iso) return 'never'
  const ts = new Date(iso).getTime()
  if (!Number.isFinite(ts)) return 'unknown'
  const diff = Date.now() - ts
  if (diff < 0) return 'just now'
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

export function formatCurrency(val: number) {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
  }).format(val);
}

export function formatTokens(tokens: number): string {
  if (tokens >= 1_000_000) {
    return `${(tokens / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`;
  }
  if (tokens >= 1_000) {
    return `${(tokens / 1_000).toFixed(1).replace(/\.0$/, '')}K`;
  }
  return tokens.toString();
}

export function getIncidentStatusColor(status: string) {
  switch (status.toLowerCase()) {
    case 'failed':
    case 'down':
    case 'error':
      return 'text-red-400 bg-red-500/10 border-red-500/20';
    case 'stale':
    case 'warning':
      return 'text-amber-400 bg-amber-500/10 border-amber-500/20';
    default:
      return 'text-blue-400 bg-blue-500/10 border-blue-500/20';
  }
}

export function getIncidentSideBarColor(status: string) {
  switch (status.toLowerCase()) {
    case 'failed':
    case 'down':
    case 'error':
      return 'bg-red-500 shadow-[0_0_8px_rgba(239,68,68,0.4)]';
    case 'stale':
    case 'warning':
      return 'bg-amber-500 shadow-[0_0_8px_rgba(245,158,11,0.3)]';
    default:
      return 'bg-blue-500 shadow-[0_0_8px_rgba(59,130,246,0.3)]';
  }
}

export function getSessionStatusStyles(status: string) {
  switch (status.toLowerCase()) {
    case 'running':
      return {
        badge: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20',
        dot: 'bg-emerald-500 animate-pulse',
      };
    case 'failed':
    case 'error':
      return {
        badge: 'bg-red-500/10 text-red-400 border-red-500/20',
        dot: 'bg-red-500',
      };
    case 'stale':
    case 'warning':
      return {
        badge: 'bg-amber-500/10 text-amber-400 border-amber-500/20',
        dot: 'bg-amber-500',
      };
    case 'done':
    case 'completed':
    default:
      return {
        badge: 'bg-gray-500/10 text-gray-400 border-gray-500/20',
        dot: 'bg-gray-500',
      };
  }
}

export function getTenantStyles(tenant: string) {
  const isPersonal = tenant === 'personal';
  const isDayjob = tenant === 'dayjob';

  if (isPersonal) {
    return {
      dot: 'bg-[var(--tenant-personal)] shadow-[0_0_8px_var(--tenant-personal)]',
      chip: 'bg-[var(--tenant-personal)]/10 text-[var(--tenant-personal)] border border-[var(--tenant-personal)]/20',
      avatar: 'bg-gradient-to-br from-[var(--tenant-personal)] to-[var(--tenant-personal-2)] text-black font-bold shadow-[0_0_12px_rgba(34,211,238,0.2)]',
      border: 'border-[var(--tenant-personal)]/20',
      text: 'text-[var(--tenant-personal)]',
      bar: 'bg-gradient-to-r from-[var(--tenant-personal)] to-[var(--tenant-personal-2)]',
      label: 'Personal',
    };
  }

  if (isDayjob) {
    return {
      dot: 'bg-[var(--tenant-dayjob)] shadow-[0_0_8px_var(--tenant-dayjob)]',
      chip: 'bg-[var(--tenant-dayjob)]/10 text-[var(--tenant-dayjob)] border border-[var(--tenant-dayjob)]/20',
      avatar: 'bg-gradient-to-br from-[var(--tenant-dayjob)] to-[var(--tenant-dayjob-2)] text-white font-bold shadow-[0_0_12px_rgba(251,146,60,0.25)]',
      border: 'border-[var(--tenant-dayjob)]/20',
      text: 'text-[var(--tenant-dayjob)]',
      bar: 'bg-gradient-to-r from-[var(--tenant-dayjob)] to-[var(--tenant-dayjob-2)]',
      label: 'Work',
    };
  }

  return {
    dot: 'bg-[var(--accent-blue)] shadow-[0_0_8px_var(--accent-blue)]',
    chip: 'bg-white/5 text-[var(--text-secondary)] border border-white/10',
    avatar: 'bg-gradient-to-br from-[var(--gradient-start)] to-[var(--gradient-end)] text-white font-bold',
    border: 'border-white/5',
    text: 'text-[var(--text-secondary)]',
    bar: 'bg-gradient-to-r from-[var(--gradient-start)] via-[var(--gradient-mid)] to-[var(--gradient-end)]',
    label: tenant || 'System',
  };
}

// Map an internal tenant key to its user-facing display label. The internal
// "dayjob" key is walled (ADR-002) and must NEVER leak raw into the UI — it is
// always shown as "Work". "personal" → "Personal"; anything else is title-cased
// as a sensible fallback. This is the single source of truth for tenant display
// text so every surface (Mission Control, Observe, drawers) stays consistent.
export function tenantLabel(tenant: string): string {
  switch (tenant) {
    case 'dayjob':
      return 'Work';
    case 'personal':
      return 'Personal';
    case '':
      return 'System';
    default:
      return tenant.charAt(0).toUpperCase() + tenant.slice(1);
  }
}

export function deriveTenant(key: string, agents: Agent[], sessions: SessionStatus[], incidents: Incident[]): string | undefined {
  const lowerKey = key.toLowerCase();
  
  // Search sessions
  const sessionMatch = sessions.find(s => 
    s.harness.toLowerCase() === lowerKey || 
    s.session_id.toLowerCase() === lowerKey ||
    s.host.toLowerCase() === lowerKey
  );
  if (sessionMatch) return sessionMatch.tenant;

  // Search incidents
  const incidentMatch = incidents.find(i => 
    i.harness.toLowerCase() === lowerKey || 
    i.project_slug.toLowerCase() === lowerKey
  );
  if (incidentMatch) return incidentMatch.tenant;

  // Search agents prop
  const agentMatch = agents.find(a => 
    a.id.toLowerCase() === lowerKey || 
    a.name.toLowerCase() === lowerKey || 
    a.harness.toLowerCase() === lowerKey ||
    a.display_name.toLowerCase() === lowerKey
  );
  if (agentMatch) {
    const hMatch = sessions.find(s => s.harness.toLowerCase() === agentMatch.harness.toLowerCase());
    if (hMatch) return hMatch.tenant;
    
    const iMatch = incidents.find(i => i.harness.toLowerCase() === agentMatch.harness.toLowerCase());
    if (iMatch) return iMatch.tenant;
  }

  // Fallbacks
  if (lowerKey.includes('personal') || lowerKey.includes('home') || lowerKey.includes('cool')) return 'personal';
  if (lowerKey.includes('dayjob') || lowerKey.includes('work') || lowerKey.includes('job') || lowerKey.includes('warm')) return 'dayjob';

  return undefined;
}
