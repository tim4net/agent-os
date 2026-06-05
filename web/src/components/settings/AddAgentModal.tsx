import { useState, useEffect } from 'react'
import { listHarnesses, createAgent, type HarnessInfo } from '../../api/client'
import { showToast } from '../toast-bus'

interface AddAgentModalProps {
  onClose: () => void
  onAdded: () => void
}

export function AddAgentModal({ onClose, onAdded }: AddAgentModalProps) {
  const [harnesses, setHarnesses] = useState<HarnessInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const [displayName, setDisplayName] = useState('')
  const [name, setName] = useState('')
  const [harness, setHarness] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [authToken, setAuthToken] = useState('')

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  useEffect(() => {
    async function load() {
      try {
        const res = await listHarnesses()
        setHarnesses(res)
        if (res.length > 0) {
          setHarness(res[0].name)
        }
      } catch (err) {
        showToast((err as Error).message || 'Failed to load harnesses', 'error')
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [])

  const selectedHarness = harnesses.find(h => h.name === harness)
  const requiresAuth = selectedHarness?.requires_auth_token

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!displayName || !name || !harness || !baseUrl) {
      showToast('Please fill in all required fields', 'error')
      return
    }

    setSaving(true)
    try {
      await createAgent({
        name,
        display_name: displayName,
        harness,
        base_url: baseUrl,
        auth_token: authToken || undefined,
      })
      showToast('Agent created', 'success')
      onAdded()
      onClose()
    } catch (err) {
      showToast((err as Error).message || 'Failed to create agent', 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="add-agent-modal-title"
        className="rounded-xl w-full max-w-md max-h-[90vh] flex flex-col shadow-2xl overflow-hidden"
        style={{ backgroundColor: 'var(--bg-surface)', borderColor: 'var(--border-subtle)', borderWidth: '1px' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-4 border-b" style={{ borderColor: 'var(--border-subtle)', backgroundColor: 'var(--bg-elevated)' }}>
          <h3 id="add-agent-modal-title" className="text-lg font-semibold" style={{ color: 'var(--color-text-primary)' }}>
            Add Agent
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

        {loading ? (
          <div className="p-8 flex justify-center">
            <div className="shimmer h-8 w-8 rounded-full" />
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="p-4 overflow-y-auto space-y-4">
            <div>
              <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
                Display Name *
              </label>
              <input
                type="text"
                value={displayName}
                onChange={e => setDisplayName(e.target.value)}
                placeholder="My Custom Agent"
                className="w-full px-3 py-2 text-sm"
                style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
                required
              />
            </div>

            <div>
              <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
                Name (slug) *
              </label>
              <input
                type="text"
                value={name}
                onChange={e => setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))}
                placeholder="my-custom-agent"
                className="w-full px-3 py-2 text-sm"
                style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
                required
              />
              <p className="text-xs mt-1" style={{ color: 'var(--color-text-muted)' }}>Unique ID, e.g. my-openclaw</p>
            </div>

            <div>
              <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
                Harness *
              </label>
              <select
                value={harness}
                onChange={e => setHarness(e.target.value)}
                className="w-full px-3 py-2 text-sm"
                style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
                required
              >
                {harnesses.map(h => (
                  <option key={h.name} value={h.name}>{h.name}</option>
                ))}
              </select>
              {selectedHarness && (
                <p className="text-xs mt-1" style={{ color: 'var(--color-text-muted)' }}>{selectedHarness.description}</p>
              )}
            </div>

            <div>
              <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
                Base URL *
              </label>
              <input
                type="text"
                value={baseUrl}
                onChange={e => setBaseUrl(e.target.value)}
                placeholder="http://host:port"
                className="w-full px-3 py-2 text-sm"
                style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
                required
              />
            </div>

            {requiresAuth && (
              <div>
                <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
                  Auth Token
                </label>
                <input
                  type="password"
                  autoComplete="off"
                  value={authToken}
                  onChange={e => setAuthToken(e.target.value)}
                  placeholder="Secret token..."
                  className="w-full px-3 py-2 text-sm"
                  style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
                />
              </div>
            )}

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
                {saving ? 'Creating...' : 'Create Agent'}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
