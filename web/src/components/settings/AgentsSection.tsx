import { useEffect, useState } from 'react'
import { listAgents, deleteAgent, updateAgentConfigFull, type Agent } from '../../api/client'
import { showToast } from '../toast-bus'
import { AgentCard } from '../agents/AgentCard'
import { AddAgentModal } from './AddAgentModal'
import { useAgentVersions } from '../../hooks/useAgentVersions'

export function AgentsSection({ onOpenAccess }: { onOpenAccess?: (agent: Agent) => void }) {
  const [agents, setAgents] = useState<Agent[]>([])
  const [loading, setLoading] = useState(true)
  const [showAddModal, setShowAddModal] = useState(false)
  const [editingAgent, setEditingAgent] = useState<Agent | null>(null)
  const { versions } = useAgentVersions(agents.map((a) => a.id))

  const loadAgents = async () => {
    try {
      setLoading(true)
      const res = await listAgents()
      setAgents(res)
    } catch (err) {
      showToast((err as Error).message || 'Failed to load agents', 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await
    loadAgents()
  }, [])

  const handleDelete = async (id: string) => {
    if (!window.confirm('Are you sure you want to delete this agent?')) return
    try {
      await deleteAgent(id)
      showToast('Agent deleted', 'success')
      loadAgents()
    } catch (err) {
      showToast((err as Error).message || 'Failed to delete agent', 'error')
    }
  }

  return (
    <section className="mb-8 fade-in">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-sm font-medium uppercase tracking-wider" style={{ color: 'var(--color-text-muted)' }}>
          Agents
        </h3>
        <button
          onClick={() => setShowAddModal(true)}
          className="pill-btn pill-btn--primary text-xs py-1 px-3"
          aria-label="Add Agent"
        >
          + Add Agent
        </button>
      </div>

      {loading ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div className="shimmer h-24 w-full rounded-xl" />
          <div className="shimmer h-24 w-full rounded-xl" />
        </div>
      ) : agents.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--color-text-muted)' }}>No agents registered.</p>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          {agents.map(agent => (
            <div key={agent.id} className="relative group">
              <AgentCard
                agent={agent}
                version={versions[agent.id]?.version ?? null}
                versionLoading={versions[agent.id]?.loading ?? true}
              />
              <div className="absolute top-2 right-2 flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                <button
                  onClick={() => onOpenAccess?.(agent)}
                  className="p-1 rounded transition-colors"
                  style={{ backgroundColor: 'var(--bg-active)', color: 'var(--color-text-primary)' }}
                  aria-label="Manage access"
                >
                  <svg className="w-4 h-4 hover:text-[var(--accent-purple)]" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                     <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                  </svg>
                </button>
                <button
                  onClick={() => setEditingAgent(agent)}
                  className="p-1 rounded transition-colors"
                  style={{ backgroundColor: 'var(--bg-active)', color: 'var(--color-text-primary)' }}
                  aria-label="Edit agent"
                >
                  <svg className="w-4 h-4 hover:text-[var(--accent-blue)]" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
                  </svg>
                </button>
                <button
                  onClick={() => handleDelete(agent.id)}
                  className="p-1 rounded text-red-400 hover:bg-red-500 hover:text-white transition-colors"
                  style={{ backgroundColor: 'var(--bg-active)' }}
                  aria-label="Delete agent"
                >
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                  </svg>
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {showAddModal && (
        <AddAgentModal
          onClose={() => setShowAddModal(false)}
          onAdded={loadAgents}
        />
      )}

      {editingAgent && (
        <EditAgentModal
          agent={editingAgent}
          onClose={() => setEditingAgent(null)}
          onUpdated={() => {
            setEditingAgent(null)
            loadAgents()
          }}
        />
      )}
    </section>
  )
}

function EditAgentModal({ agent, onClose, onUpdated }: { agent: Agent, onClose: () => void, onUpdated: () => void }) {
  const [role, setRole] = useState(agent.role || '')
  const [systemPrompt, setSystemPrompt] = useState(agent.system_prompt || '')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      await updateAgentConfigFull(agent.id, { role, system_prompt: systemPrompt })
      showToast('Agent updated', 'success')
      onUpdated()
    } catch (err) {
      showToast((err as Error).message || 'Failed to update agent', 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="edit-agent-modal-title"
        className="rounded-xl w-full max-w-lg max-h-[90vh] flex flex-col shadow-2xl overflow-hidden"
        style={{ backgroundColor: 'var(--bg-surface)', borderColor: 'var(--border-subtle)', borderWidth: '1px' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-4 border-b" style={{ borderColor: 'var(--border-subtle)', backgroundColor: 'var(--bg-elevated)' }}>
          <h3 id="edit-agent-modal-title" className="text-lg font-semibold" style={{ color: 'var(--color-text-primary)' }}>
            Edit Agent Config: {agent.display_name || agent.name}
          </h3>
          <button
            onClick={onClose}
            className="transition-colors"
            style={{ color: 'var(--color-text-muted)' }}
            aria-label="Close"
          >
            <svg className="w-5 h-5 hover:text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <form onSubmit={handleSubmit} className="p-4 overflow-y-auto space-y-4">
          <div>
            <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
              Role
            </label>
            <input
              type="text"
              value={role}
              onChange={e => setRole(e.target.value)}
              placeholder="e.g. You are a senior frontend developer..."
              className="w-full px-3 py-2 text-sm"
              style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
            />
          </div>

          <div>
            <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
              System Prompt
            </label>
            <textarea
              value={systemPrompt}
              onChange={e => setSystemPrompt(e.target.value)}
              placeholder="Full system instructions..."
              className="w-full px-3 py-2 text-sm h-48 resize-y"
              style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
            />
          </div>

          <div className="pt-4 flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              className="pill-btn pill-btn--ghost"
              disabled={saving}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="pill-btn pill-btn--primary"
              disabled={saving}
            >
              {saving ? 'Saving...' : 'Save Changes'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
