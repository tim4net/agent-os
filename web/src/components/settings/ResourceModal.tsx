import { useState, useEffect } from 'react'
import { createResource, updateResource, type Resource, type ResourceKind } from '../../api/client'
import { showToast } from '../toast-bus'

interface ResourceModalProps {
  resource?: Resource
  onClose: () => void
  onSaved: () => void
}

export function ResourceModal({ resource, onClose, onSaved }: ResourceModalProps) {
  const isEditing = !!resource

  const [kind, setKind] = useState<ResourceKind>(resource?.kind || 'credential')
  const [slug, setSlug] = useState(resource?.slug || '')
  const [label, setLabel] = useState(resource?.label || '')
  const [provider, setProvider] = useState(resource?.provider || '')
  const [secret, setSecret] = useState('')
  const [configStr, setConfigStr] = useState(
    resource?.config ? JSON.stringify(resource.config, null, 2) : ''
  )
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    
    // Validate slug (lowercase-hyphen)
    if (!isEditing && !/^[a-z0-9-]+$/.test(slug)) {
      showToast('Slug must be lowercase alphanumeric and hyphens only', 'error')
      return
    }

    let parsedConfig: Record<string, unknown> | undefined
    if (kind !== 'credential' && configStr.trim()) {
      try {
        parsedConfig = JSON.parse(configStr)
      } catch {
        showToast('Config must be valid JSON', 'error')
        return
      }
    }

    setSaving(true)
    try {
      if (isEditing) {
        await updateResource(resource.id, {
          label: label || undefined,
          provider: provider || undefined,
          secret: secret !== '' ? secret : undefined,
          config: parsedConfig
        })
        showToast('Resource updated', 'success')
      } else {
        await createResource({
          slug,
          kind,
          label: label || undefined,
          provider: provider || undefined,
          secret: secret || undefined,
          config: parsedConfig
        })
        showToast('Resource created', 'success')
      }
      setSecret('') // clear secret
      onSaved()
    } catch (err) {
      if ((err as Error).message.includes('409')) {
        showToast('Slug already exists', 'error')
      } else {
        showToast((err as Error).message || 'Failed to save resource', 'error')
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        className="rounded-xl w-full max-w-lg max-h-[90vh] flex flex-col shadow-2xl overflow-hidden"
        style={{ backgroundColor: 'var(--bg-surface)', borderColor: 'var(--border-subtle)', borderWidth: '1px' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-4 border-b shrink-0" style={{ borderColor: 'var(--border-subtle)', backgroundColor: 'var(--bg-elevated)' }}>
          <h3 className="text-lg font-semibold" style={{ color: 'var(--color-text-primary)' }}>
            {isEditing ? 'Edit Resource' : 'Add Resource'}
          </h3>
          <button onClick={onClose} className="transition-colors hover:text-white" style={{ color: 'var(--color-text-muted)' }}>
            <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" /></svg>
          </button>
        </div>

        <form onSubmit={handleSubmit} className="p-4 overflow-y-auto space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>Kind</label>
              <select
                value={kind}
                onChange={e => setKind(e.target.value as ResourceKind)}
                disabled={isEditing}
                className="w-full px-3 py-2 text-sm rounded disabled:opacity-50"
                style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', border: '1px solid var(--border-subtle)' }}
              >
                <option value="credential">Credential</option>
                <option value="integration">Integration</option>
                <option value="mcp_server">MCP Server</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>Provider</label>
              <input
                type="text"
                value={provider}
                onChange={e => setProvider(e.target.value)}
                placeholder="e.g. anthropic, openai"
                className="w-full px-3 py-2 text-sm rounded"
                style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', border: '1px solid var(--border-subtle)' }}
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
              Slug <span className="text-red-400">*</span>
            </label>
            <input
              type="text"
              value={slug}
              onChange={e => setSlug(e.target.value)}
              disabled={isEditing}
              required
              placeholder="e.g. openrouter-personal"
              className="w-full px-3 py-2 text-sm rounded disabled:opacity-50"
              style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', border: '1px solid var(--border-subtle)' }}
            />
            {!isEditing && <p className="text-xs mt-1" style={{ color: 'var(--color-text-muted)' }}>Unique ID, lowercase-hyphen only.</p>}
          </div>

          <div>
            <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>Label</label>
            <input
              type="text"
              value={label}
              onChange={e => setLabel(e.target.value)}
              placeholder="e.g. My OpenRouter API Key"
              className="w-full px-3 py-2 text-sm rounded"
              style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', border: '1px solid var(--border-subtle)' }}
            />
          </div>

          <div>
            <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
              {isEditing ? 'Update Secret' : 'Secret'}
            </label>
            <input
              type="password"
              value={secret}
              onChange={e => setSecret(e.target.value)}
              autoComplete="off"
              placeholder={isEditing ? 'Leave blank to keep unchanged' : 'Paste API key or secret token'}
              className="w-full px-3 py-2 text-sm rounded font-mono"
              style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', border: '1px solid var(--border-subtle)' }}
            />
          </div>

          {kind !== 'credential' && (
            <div>
              <label className="block text-sm font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>Config (JSON)</label>
              <textarea
                value={configStr}
                onChange={e => setConfigStr(e.target.value)}
                placeholder='{"base_url": "..."}'
                className="w-full px-3 py-2 text-sm rounded h-24 font-mono"
                style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', border: '1px solid var(--border-subtle)' }}
              />
            </div>
          )}

          <div className="pt-4 flex justify-end gap-2 border-t mt-4" style={{ borderColor: 'var(--border-subtle)' }}>
            <button type="button" onClick={onClose} className="pill-btn pill-btn--ghost" disabled={saving}>Cancel</button>
            <button type="submit" className="pill-btn pill-btn--primary" disabled={saving}>{saving ? 'Saving...' : 'Save Resource'}</button>
          </div>
        </form>
      </div>
    </div>
  )
}
