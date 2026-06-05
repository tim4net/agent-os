import { useEffect, useState, useMemo } from 'react'
import { listResources, deleteResource, type Resource } from '../../api/client'
import { showToast } from '../toast-bus'
import { ResourceModal } from './ResourceModal'

export function VaultManager() {
  const [resources, setResources] = useState<Resource[]>([])
  const [loading, setLoading] = useState(true)
  const [showAddModal, setShowAddModal] = useState(false)
  const [editingResource, setEditingResource] = useState<Resource | null>(null)
  const [search, setSearch] = useState('')

  const loadResources = async () => {
    try {
      setLoading(true)
      const res = await listResources()
      setResources(res.resources || [])
    } catch (err) {
      showToast((err as Error).message || 'Failed to load resources', 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    loadResources()
  }, [])

  const handleDelete = async (id: string, slug: string) => {
    if (!window.confirm(`Delete ${slug}?\n\nWARNING: This will revoke it from all agents.`)) return
    try {
      await deleteResource(id)
      showToast('Resource deleted', 'success')
      loadResources()
    } catch (err) {
      showToast((err as Error).message || 'Failed to delete resource', 'error')
    }
  }

  const filteredResources = useMemo(() => {
    if (!search) return resources
    return resources.filter(r => 
      r.label?.toLowerCase().includes(search.toLowerCase()) || 
      r.slug.toLowerCase().includes(search.toLowerCase())
    )
  }, [resources, search])

  const groupedResources = useMemo(() => {
    const groups: Record<string, Resource[]> = { credential: [], integration: [], mcp_server: [] }
    filteredResources.forEach(r => groups[r.kind].push(r))
    return groups
  }, [filteredResources])

  const getMaskedDetail = (r: Resource) => {
    if (r.kind === 'credential') {
      if (!r.is_set) return 'not set'
      return r.last4 ? `••••${r.last4}` : '••••••••'
    } else {
      const keys = Object.keys(r.config || {})
      return keys.length > 0 ? `${keys.length} config keys` : 'no config'
    }
  }

  const getKindIcon = (kind: string) => {
    if (kind === 'credential') return <svg className="w-5 h-5 opacity-70" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" /></svg>
    if (kind === 'integration') return <svg className="w-5 h-5 opacity-70" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" /></svg>
    return <svg className="w-5 h-5 opacity-70" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" /></svg>
  }

  return (
    <div className="fade-in space-y-6">
      <div className="flex items-center justify-between">
        <input 
          type="text"
          placeholder="Search vault..."
          value={search}
          onChange={e => setSearch(e.target.value)}
          className="px-3 py-1.5 text-sm rounded-md w-64"
          style={{ backgroundColor: 'var(--bg-surface)', borderColor: 'var(--border-subtle)', borderWidth: '1px', color: 'var(--color-text-primary)' }}
        />
        <button
          onClick={() => setShowAddModal(true)}
          className="pill-btn pill-btn--primary text-xs py-1.5 px-3"
          aria-label="Add Resource"
        >
          + Add Resource
        </button>
      </div>

      {loading ? (
        <div className="space-y-4">
          <div className="shimmer h-12 w-full rounded-xl" />
          <div className="shimmer h-24 w-full rounded-xl" />
        </div>
      ) : resources.length === 0 ? (
        <div className="glass-card p-10 text-center flex flex-col items-center">
          <svg className="w-12 h-12 mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" /></svg>
          <h3 className="text-lg font-medium" style={{ color: 'var(--color-text-primary)' }}>Your vault is empty</h3>
          <p className="text-sm mt-2 max-w-sm" style={{ color: 'var(--color-text-muted)' }}>
            Store credentials and configure integrations here so you can securely grant access to your agents.
          </p>
        </div>
      ) : (
        <div className="space-y-8">
          {(['credential', 'integration', 'mcp_server'] as const).map(kind => {
            const group = groupedResources[kind]
            if (!group || group.length === 0) return null
            const kindLabel = kind === 'credential' ? 'Credentials' : kind === 'integration' ? 'Integrations' : 'MCP Servers'
            
            return (
              <div key={kind}>
                <h3 className="text-sm font-medium uppercase tracking-wider mb-3 flex items-center gap-2" style={{ color: 'var(--color-text-muted)' }}>
                  {getKindIcon(kind)}
                  {kindLabel}
                  <span className="ml-2 text-xs font-mono opacity-50">{group.length}</span>
                </h3>
                <div className="glass-card overflow-hidden">
                  <div className="flex flex-col">
                    {group.map(resource => (
                      <div key={resource.id} className="flex items-center justify-between p-4 group transition-colors hover:bg-white/5" style={{ borderBottom: '1px solid var(--border-subtle)' }}>
                        <div className="flex items-center gap-4">
                          <div className={`w-2 h-2 rounded-full ${resource.status === 'error' ? 'bg-red-500' : resource.status === 'active' || resource.status === 'online' ? 'bg-emerald-500' : 'bg-gray-500'}`} title={`Status: ${resource.status}`} />
                          <div>
                            <div className="flex items-center gap-2 mb-0.5">
                              <span className="font-semibold text-sm" style={{ color: 'var(--color-text-primary)' }}>
                                {resource.label || resource.slug}
                              </span>
                              {resource.provider && (
                                <span className="text-[10px] px-1.5 py-0.5 rounded shrink-0 bg-white/10" style={{ color: 'var(--color-text-secondary)' }}>
                                  {resource.provider}
                                </span>
                              )}
                            </div>
                            <div className="flex items-center gap-2 text-xs font-mono">
                              <span className="opacity-70" style={{ color: 'var(--color-text-muted)' }}>{resource.slug}</span>
                              <span className="opacity-40">•</span>
                              <span className={resource.kind === 'credential' && !resource.is_set ? 'text-amber-500/70' : ''} style={{ color: resource.kind === 'credential' && !resource.is_set ? undefined : 'var(--color-text-muted)' }}>
                                {getMaskedDetail(resource)}
                              </span>
                            </div>
                          </div>
                        </div>
                        <div className="flex items-center gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                          <button
                            onClick={() => setEditingResource(resource)}
                            className="p-1.5 rounded transition-colors"
                            style={{ backgroundColor: 'var(--bg-active)', color: 'var(--color-text-primary)' }}
                            aria-label="Edit resource"
                          >
                            <svg className="w-4 h-4 hover:text-[var(--accent-blue)]" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" /></svg>
                          </button>
                          <button
                            onClick={() => handleDelete(resource.id, resource.slug)}
                            className="p-1.5 rounded text-red-400 hover:bg-red-500 hover:text-white transition-colors"
                            style={{ backgroundColor: 'var(--bg-active)' }}
                            aria-label="Delete resource"
                          >
                            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg>
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {showAddModal && (
        <ResourceModal
          onClose={() => setShowAddModal(false)}
          onSaved={() => {
            setShowAddModal(false)
            loadResources()
          }}
        />
      )}

      {editingResource && (
        <ResourceModal
          resource={editingResource}
          onClose={() => setEditingResource(null)}
          onSaved={() => {
            setEditingResource(null)
            loadResources()
          }}
        />
      )}
    </div>
  )
}
