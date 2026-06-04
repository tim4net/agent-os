// Display label for an internal tenant key. The 'dayjob' key is internal-only;
// every user-visible surface must show 'Work'. Keeps the radar consistent with
// Mission Control's tenant switcher labels.
export function tenantLabel(tenant: string): string {
  switch (tenant) {
    case 'dayjob':
      return 'Work'
    case 'personal':
      return 'Personal'
    case 'all':
      return 'All'
    default:
      return tenant
  }
}

// Deterministic string hashing function to return angle in radians [0, 2*PI]
export function getAngleFromSessionId(sessionId: string): number {
  let hash = 0
  for (let i = 0; i < sessionId.length; i++) {
    hash = sessionId.charCodeAt(i) + ((hash << 5) - hash)
  }
  const degrees = Math.abs(hash) % 360
  return (degrees * Math.PI) / 180
}
