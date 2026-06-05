import { useEffect, useState, useMemo } from 'react'
import {
  listResources,
  listAgentGrants,
  grantResource,
  revokeResource,
  type Agent,
  type Resource,
} from '../../api/client'
import { showToast } from '../toast-bus'

export function AgentAccessDrawer({ agent, onClose }: { agent: Agent, onClose: () => void }) {
  const [resources, setResources] = useState<Resource[]>([])
  const [grantedIds, setGrantedIds] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  useEffect(() => {
    const load = async () => {
      try {
        setLoading(true)
        const [resAll, resGrants] = await Promise.all([
          listResources(),
          listAgentGrants(agent.id)
        ])
        setResources(resAll.resources || [])
        setGrantedIds(new Set((resGrants.resources || []).map(r => r.id)))
      } catch (err) {
        showToast((err as Error).message || 'Failed to load access', 'error')
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [agent.id])

  const toggleGrant = async (resourceId: string, isGranted: boolean) => {
    const newGrants = new Set(grantedIds)
    if (isGranted) newGrants.delete(resourceId)
    else newGrants.add(resourceId)
    setGrantedIds(newGrants)

    try {
      if (isGranted) {
        await revokeResource(agent.id, resourceId)
      } else {
        await grantResource(agent.id, resourceId)
      }
    } catch (err) {
      showToast((err as Error).message || 'Failed to update grant', 'error')
      // Revert
      const reverted = new Set(grantedIds)
      setGrantedIds(reverted)
    }
  }

  const filteredResources = useMemo(() => {
    return resources.filter(r => {
      if (search && !r.label.toLowerCase().includes(search.toLowerCase()) && !r.slug.toLowerCase().includes(search.toLowerCase())) return false
      return true
    })
  }, [resources, search])

  const groupedResources = useMemo(() => {
    const groups: Record<string, Resource[]> = { credential: [], integration: [], mcp_server: [] }
    filteredResources.forEach(r => groups[r.kind].push(r))
    return groups
  }, [filteredResources])

  const getMaskedDetail = (r: Resource) => {
    if (r.kind === 'credential') {
      if (!r.is_set) return 'not configured'
      return r.last4 ? `••••${r.last4}` : '••••••••'
    } else {
      const keys = Object.keys(r.config || {})
      return keys.length > 0 ? `${keys.length} keys` : 'no config'
    }
  }

  const getKindIcon = (kind: string) => {
    if (kind === 'credential') return <svg className="w-5 h-5 opacity-70" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" /></svg>
    if (kind === 'integration') return <svg className="w-5 h-5 opacity-70" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" /></svg>
    return <svg className="w-5 h-5 opacity-70" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" /></svg>
  }

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div 
        className="w-full max-w-md h-full shadow-2xl flex flex-col"
        style={{ backgroundColor: 'var(--bg-surface)', borderLeft: '1px solid var(--border-subtle)' }}
        onClick={e => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div className="p-6 border-b shrink-0" style={{ borderColor: 'var(--border-subtle)', backgroundColor: 'var(--bg-elevated)' }}>
          <div className="flex items-start justify-between mb-4">
            <div>
              <div className="flex items-center gap-2 mb-1">
                <div className={`w-2.5 h-2.5 rounded-full ${agent.status === 'online' ? 'bg-emerald-500' : 'bg-gray-500'}`} />
                <h2 className="text-xl font-semibold" style={{ color: 'var(--color-text-primary)' }}>
                  {agent.display_name || agent.name}
                </h2>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-xs uppercase font-mono px-2 py-0.5 rounded" style={{ backgroundColor: 'var(--bg-active)', color: 'var(--color-text-muted)' }}>
                  {agent.harness}
                </span>
                {!loading && (
                  <span className="text-sm" style={{ color: 'var(--color-text-secondary)' }}>
                    can access {grantedIds.size} of {resources.length} resources
                  </span>
                )}
              </div>
            </div>
            <button
              onClick={onClose}
              className="p-1 rounded-md transition-colors hover:bg-white/10"
              style={{ color: 'var(--color-text-muted)' }}
            >
              <svg className="w-6 h-6 hover:text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" /></svg>
            </button>
          </div>
          <input 
            type="text"
            placeholder="Search resources..."
            value={search}
            onChange={e => setSearch(e.target.value)}
            className="w-full px-4 py-2 text-sm rounded-lg transition-colors"
            style={{ backgroundColor: 'var(--bg-base)', color: 'var(--color-text-primary)', border: '1px solid var(--border-subtle)' }}
          />
        </div>

        <div className="flex-1 overflow-y-auto p-4 space-y-6">
          {loading ? (
            <div className="space-y-4">
              <div className="shimmer h-16 w-full rounded-xl" />
              <div className="shimmer h-16 w-full rounded-xl" />
            </div>
          ) : (
            (['credential', 'integration', 'mcp_server'] as const).map(kind => {
              const group = groupedResources[kind]
              if (!group || group.length === 0) return null
              const kindLabel = kind === 'credential' ? 'Credentials' : kind === 'integration' ? 'Integrations' : 'MCP Servers'
              return (
                <div key={kind}>
                  <h3 className="text-xs font-semibold tracking-wider uppercase mb-3 px-2 flex items-center gap-2" style={{ color: 'var(--color-text-muted)' }}>
                    {getKindIcon(kind)}
                    {kindLabel}
                  </h3>
                  <div className="space-y-2">
                    {group.map(resource => {
                      const isGranted = grantedIds.has(resource.id)
                      return (
                        <div key={resource.id} className="glass-card p-3 flex items-center justify-between group transition-colors hover:bg-white/5" style={{ border: '1px solid var(--border-subtle)' }}>
                          <div className="flex flex-col overflow-hidden pr-4">
                            <div className="flex items-center gap-2">
                              <span className="font-medium text-sm truncate" style={{ color: 'var(--color-text-primary)' }}>
                                {resource.label || resource.slug}
                              </span>
                            </div>
                            <div className="flex items-center gap-2 mt-1 text-xs font-mono">
                              <span className="truncate opacity-70" style={{ color: 'var(--color-text-muted)' }}>{resource.slug}</span>
                              <span className="opacity-40">•</span>
                              <span className={`shrink-0 ${resource.kind === 'credential' && !resource.is_set ? 'text-amber-500/70' : ''}`} style={{ color: resource.kind === 'credential' && !resource.is_set ? undefined : 'var(--color-text-muted)' }}>
                                {getMaskedDetail(resource)}
                              </span>
                            </div>
                          </div>
                          <button
                            onClick={() => toggleGrant(resource.id, isGranted)}
                            className="shrink-0 w-12 h-6 rounded-full relative transition-all duration-200 cursor-pointer ring-offset-2 ring-offset-[var(--bg-base)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-purple)]"
                            style={{ 
                              background: isGranted ? 'linear-gradient(to right, var(--accent-purple), var(--accent-cyan))' : 'var(--bg-surface)',
                              border: isGranted ? 'none' : '1px solid var(--border-subtle)',
                            }}
                            aria-pressed={isGranted}
                            aria-label={`Grant ${resource.slug}`}
                          >
                            <span 
                              className="absolute top-0.5 left-0.5 bg-white w-5 h-5 rounded-full transition-transform duration-200 shadow-sm"
                              style={{ transform: isGranted ? 'translateX(24px)' : 'translateX(0)' }}
                            />
                          </button>
                        </div>
                      )
                    })}
                  </div>
                </div>
              )
            })
          )}
          {!loading && resources.length === 0 && (
            <p className="text-center text-sm" style={{ color: 'var(--color-text-muted)' }}>No resources found.</p>
          )}
        </div>
      </div>
    </div>
  )
}
