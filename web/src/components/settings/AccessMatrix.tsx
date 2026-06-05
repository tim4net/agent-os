import React, { useEffect, useState, useMemo } from 'react'
import {
  listResources,
  listAgents,
  listAllGrants,
  grantResource,
  revokeResource,
  type Agent,
  type Resource,
} from '../../api/client'
import { showToast } from '../toast-bus'

export function AccessMatrix({ onOpenAgent }: { onOpenAgent: (agent: Agent) => void }) {
  const [agents, setAgents] = useState<Agent[]>([])
  const [resources, setResources] = useState<Resource[]>([])
  const [grants, setGrants] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<'all' | 'credential' | 'integration' | 'mcp_server'>('all')
  const [search, setSearch] = useState('')

  const loadData = async () => {
    try {
      setLoading(true)
      const [resAgents, resResources, resGrants] = await Promise.all([
        listAgents(),
        listResources(),
        listAllGrants()
      ])
      setAgents(resAgents)
      setResources(resResources.resources || [])
      
      const grantSet = new Set<string>()
      resGrants.grants.forEach(g => grantSet.add(`${g.agent_id}:${g.resource_id}`))
      setGrants(grantSet)
    } catch (err) {
      showToast((err as Error).message || 'Failed to load access matrix', 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    loadData()
  }, [])

  const toggleGrant = async (agentId: string, resourceId: string, isGranted: boolean) => {
    const key = `${agentId}:${resourceId}`
    const newGrants = new Set(grants)
    if (isGranted) {
      newGrants.delete(key)
    } else {
      newGrants.add(key)
    }
    setGrants(newGrants)

    try {
      if (isGranted) {
        await revokeResource(agentId, resourceId)
      } else {
        await grantResource(agentId, resourceId)
      }
    } catch (err) {
      showToast((err as Error).message || 'Failed to update grant', 'error')
      // Revert
      const reverted = new Set(grants)
      setGrants(reverted)
    }
  }

  const toggleRow = async (resourceId: string, allGranted: boolean) => {
    const changes = agents.map(a => {
      const isGranted = grants.has(`${a.id}:${resourceId}`)
      if (allGranted && isGranted) return { a, isGranted }
      if (!allGranted && !isGranted) return { a, isGranted }
      return null
    }).filter(Boolean) as { a: Agent, isGranted: boolean }[]

    if (changes.length === 0) return

    const newGrants = new Set(grants)
    changes.forEach(({ a }) => {
      if (allGranted) newGrants.delete(`${a.id}:${resourceId}`)
      else newGrants.add(`${a.id}:${resourceId}`)
    })
    setGrants(newGrants)

    try {
      await Promise.all(changes.map(({ a, isGranted }) => 
        isGranted ? revokeResource(a.id, resourceId) : grantResource(a.id, resourceId)
      ))
    } catch (_err) {
      showToast('Failed to update some grants', 'error')
      loadData() // Reload on partial failure
    }
  }

  const filteredResources = useMemo(() => {
    return resources.filter(r => {
      if (filter !== 'all' && r.kind !== filter) return false
      if (search && !r.label.toLowerCase().includes(search.toLowerCase()) && !r.slug.toLowerCase().includes(search.toLowerCase())) return false
      return true
    })
  }, [resources, filter, search])

  const groupedResources = useMemo(() => {
    const groups: Record<string, Resource[]> = {
      credential: [],
      integration: [],
      mcp_server: []
    }
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
    if (kind === 'credential') return <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" /></svg>
    if (kind === 'integration') return <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" /></svg>
    return <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" /></svg>
  }

  if (loading) {
    return (
      <div className="fade-in space-y-4">
        <div className="shimmer h-10 w-full rounded-xl" />
        <div className="shimmer h-64 w-full rounded-xl" />
      </div>
    )
  }

  if (resources.length === 0) {
    return (
      <div className="fade-in glass-card p-10 text-center flex flex-col items-center">
        <svg className="w-12 h-12 mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" /></svg>
        <h3 className="text-lg font-medium" style={{ color: 'var(--color-text-primary)' }}>Your vault is empty</h3>
        <p className="text-sm mt-2 mb-6 max-w-sm" style={{ color: 'var(--color-text-muted)' }}>
          Add a credential, integration, or MCP server to start granting access to your agents.
        </p>
      </div>
    )
  }

  return (
    <div className="fade-in flex flex-col space-y-4">
      <div className="flex items-center gap-3 mb-2 flex-wrap">
        <input 
          type="text" 
          placeholder="Filter resources..." 
          value={search}
          onChange={e => setSearch(e.target.value)}
          className="px-3 py-1.5 text-sm rounded-md w-64"
          style={{ backgroundColor: 'var(--bg-surface)', borderColor: 'var(--border-subtle)', borderWidth: '1px', color: 'var(--color-text-primary)' }}
        />
        <div className="flex p-1 rounded-md" style={{ backgroundColor: 'var(--bg-surface)', border: '1px solid var(--border-subtle)' }}>
          {(['all', 'credential', 'integration', 'mcp_server'] as const).map(f => (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className={`text-xs px-3 py-1 rounded-md transition-colors ${filter === f ? 'shadow-sm font-medium' : 'opacity-70 hover:opacity-100'}`}
              style={{ 
                backgroundColor: filter === f ? 'var(--bg-active)' : 'transparent',
                color: filter === f ? 'var(--color-text-primary)' : 'var(--color-text-muted)'
              }}
            >
              {f === 'all' ? 'All' : f === 'credential' ? 'Credentials' : f === 'integration' ? 'Integrations' : 'MCP Servers'}
            </button>
          ))}
        </div>
      </div>

      <div className="glass-card overflow-auto" style={{ maxHeight: '70vh' }}>
        <table className="w-full text-left border-collapse whitespace-nowrap min-w-max">
          <thead className="sticky top-0 z-20 backdrop-blur-md" style={{ backgroundColor: 'var(--bg-surface)', borderBottom: '1px solid var(--border-subtle)' }}>
            <tr>
              <th className="sticky left-0 z-30 p-4 font-medium text-sm w-80 backdrop-blur-md" style={{ color: 'var(--color-text-secondary)', backgroundColor: 'var(--bg-surface)', borderRight: '1px solid var(--border-subtle)' }}>
                Resource
              </th>
              {agents.map(agent => {
                const grantCount = resources.filter(r => grants.has(`${agent.id}:${r.id}`)).length
                return (
                  <th key={agent.id} className="p-3 text-center min-w-[140px]">
                    <button onClick={() => onOpenAgent(agent)} className="w-full group flex flex-col items-center justify-center cursor-pointer p-2 rounded-lg transition-colors hover:bg-white/5">
                      <div className="flex items-center gap-1.5 mb-1">
                        <div className={`w-2 h-2 rounded-full ${agent.status === 'online' ? 'bg-emerald-500' : 'bg-gray-500'}`} />
                        <span className="text-sm font-semibold group-hover:text-[var(--accent-cyan)] transition-colors" style={{ color: 'var(--color-text-primary)' }}>
                          {agent.display_name || agent.name}
                        </span>
                      </div>
                      <div className="flex items-center gap-2">
                        <span className="text-[10px] uppercase font-mono px-1.5 py-0.5 rounded" style={{ backgroundColor: 'var(--bg-active)', color: 'var(--color-text-muted)' }}>
                          {agent.harness}
                        </span>
                        <span className="text-[10px] px-1.5 py-0.5 rounded-full" style={{ backgroundColor: 'var(--accent-purple)', color: 'white', opacity: 0.9 }}>
                          {grantCount}
                        </span>
                      </div>
                    </button>
                  </th>
                )
              })}
            </tr>
          </thead>
          <tbody>
            {(['credential', 'integration', 'mcp_server'] as const).map(kind => {
              const group = groupedResources[kind]
              if (!group || group.length === 0) return null
              const kindLabel = kind === 'credential' ? 'Credentials' : kind === 'integration' ? 'Integrations' : 'MCP Servers'
              return (
                <React.Fragment key={kind}>
                  <tr>
                    <td colSpan={agents.length + 1} className="p-3 text-xs font-semibold tracking-wider uppercase bg-black/20" style={{ color: 'var(--color-text-muted)', borderTop: '1px solid var(--border-subtle)', borderBottom: '1px solid var(--border-subtle)' }}>
                      <div className="flex items-center gap-2 sticky left-4">
                        {getKindIcon(kind)}
                        {kindLabel}
                      </div>
                    </td>
                  </tr>
                  {group.map(resource => {
                    const rowGrants = agents.filter(a => grants.has(`${a.id}:${resource.id}`))
                    const allGranted = rowGrants.length === agents.length && agents.length > 0
                    
                    return (
                      <tr key={resource.id} className="group hover:bg-white/5 transition-colors" style={{ borderBottom: '1px solid var(--border-subtle)' }}>
                        <td className="sticky left-0 z-10 p-3 w-80 backdrop-blur-md bg-[var(--bg-base)] group-hover:bg-[#1a1b23] transition-colors" style={{ borderRight: '1px solid var(--border-subtle)' }}>
                          <div className="flex items-center justify-between">
                            <div className="flex flex-col pr-4 overflow-hidden">
                              <div className="flex items-center gap-2 mb-0.5">
                                <span className="font-medium text-sm truncate" style={{ color: 'var(--color-text-primary)' }} title={resource.label || resource.slug}>{resource.label || resource.slug}</span>
                                {resource.provider && (
                                  <span className="text-[10px] px-1.5 py-0.5 rounded shrink-0 bg-white/10" style={{ color: 'var(--color-text-secondary)' }}>
                                    {resource.provider}
                                  </span>
                                )}
                              </div>
                              <div className="flex items-center gap-2 text-xs font-mono">
                                <span className="truncate opacity-60" style={{ color: 'var(--color-text-muted)' }}>{resource.slug}</span>
                                <span className="opacity-40">•</span>
                                <span className="shrink-0" style={{ color: 'var(--color-text-muted)' }}>{getMaskedDetail(resource)}</span>
                              </div>
                            </div>
                            <button 
                              onClick={() => toggleRow(resource.id, allGranted)}
                              className="opacity-0 group-hover:opacity-100 text-[10px] px-2 py-1 rounded transition-all hover:bg-white/10"
                              style={{ color: 'var(--color-text-muted)' }}
                              title={allGranted ? "Revoke from all" : "Grant to all"}
                            >
                              {allGranted ? "NONE" : "ALL"}
                            </button>
                          </div>
                        </td>
                        {agents.map(agent => {
                          const isGranted = grants.has(`${agent.id}:${resource.id}`)
                          return (
                            <td key={agent.id} className="p-3 text-center">
                              <button
                                onClick={() => toggleGrant(agent.id, resource.id, isGranted)}
                                className={`w-12 h-6 rounded-full relative transition-all duration-200 inline-block align-middle cursor-pointer ring-offset-2 ring-offset-[var(--bg-base)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-purple)]`}
                                style={{ 
                                  background: isGranted ? 'linear-gradient(to right, var(--accent-purple), var(--accent-cyan))' : 'var(--bg-surface)',
                                  border: isGranted ? 'none' : '1px solid var(--border-subtle)',
                                }}
                                aria-pressed={isGranted}
                                aria-label={`Grant ${resource.slug} to ${agent.display_name || agent.name}`}
                              >
                                <span 
                                  className={`absolute top-0.5 left-0.5 bg-white w-5 h-5 rounded-full transition-transform duration-200 shadow-sm`}
                                  style={{ transform: isGranted ? 'translateX(24px)' : 'translateX(0)' }}
                                />
                              </button>
                            </td>
                          )
                        })}
                      </tr>
                    )
                  })}
                </React.Fragment>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
