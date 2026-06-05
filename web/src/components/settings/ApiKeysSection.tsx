import { useEffect, useState } from 'react'
import { listSettings, updateSetting, deleteSetting, type SettingView } from '../../api/client'
import { showToast } from '../toast-bus'

export function ApiKeysSection() {
  const [settings, setSettings] = useState<SettingView[]>([])
  const [secretsEnabled, setSecretsEnabled] = useState(true)
  const [loading, setLoading] = useState(true)

  const loadSettings = async () => {
    try {
      setLoading(true)
      const res = await listSettings()
      setSettings(res.settings.filter(s => s.group === 'Providers'))
      setSecretsEnabled(res.secrets_enabled)
    } catch (err) {
      showToast((err as Error).message || 'Failed to load settings', 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await
    loadSettings()
  }, [])

  if (loading) {
    return (
      <section className="mb-8">
        <h3 className="text-sm font-medium uppercase tracking-wider mb-4" style={{ color: 'var(--color-text-muted)' }}>
          API Keys & Providers
        </h3>
        <div className="shimmer h-32 w-full rounded-xl" />
      </section>
    )
  }

  return (
    <section className="mb-8 fade-in">
      <h3 className="text-sm font-medium uppercase tracking-wider mb-4" style={{ color: 'var(--color-text-muted)' }}>
        API Keys & Providers
      </h3>

      {!secretsEnabled && (
        <div className="mb-4 p-3 rounded-lg bg-orange-500/10 border border-orange-500/20 text-orange-400 text-sm">
          Encrypted secret storage is disabled — set AOS_MASTER_KEY on the server.
        </div>
      )}

      <div className="space-y-3">
        {settings.map(setting => (
          <ProviderKeyRow 
            key={setting.key} 
            setting={setting} 
            secretsEnabled={secretsEnabled}
            onUpdate={loadSettings}
          />
        ))}
        {settings.length === 0 && (
          <p className="text-sm" style={{ color: 'var(--color-text-muted)' }}>No providers configured.</p>
        )}
      </div>
    </section>
  )
}

function ProviderKeyRow({ 
  setting, 
  secretsEnabled, 
  onUpdate 
}: { 
  setting: SettingView; 
  secretsEnabled: boolean; 
  onUpdate: () => void 
}) {
  const [inputValue, setInputValue] = useState('')
  const [saving, setSaving] = useState(false)

  const handleSave = async () => {
    if (!inputValue.trim()) {
      showToast('API key cannot be empty', 'error')
      return
    }
    setSaving(true)
    try {
      await updateSetting(setting.key, inputValue.trim())
      setInputValue('')
      showToast(`Updated ${setting.label}`, 'success')
      onUpdate()
    } catch (err) {
      showToast((err as Error).message || 'Failed to update key', 'error')
    } finally {
      setSaving(false)
    }
  }

  const handleRemove = async () => {
    setSaving(true)
    try {
      await deleteSetting(setting.key)
      showToast(`Removed ${setting.label}`, 'success')
      onUpdate()
    } catch (err) {
      showToast((err as Error).message || 'Failed to remove key', 'error')
    } finally {
      setSaving(false)
    }
  }

  const disabled = !secretsEnabled || saving

  return (
    <div className="glass-card p-4 flex flex-col gap-2">
      <div className="flex justify-between items-center">
        <label className="text-sm font-semibold" style={{ color: 'var(--color-text-primary)' }}>
          {setting.label}
        </label>
        {setting.is_set && (
          <div className="flex items-center gap-2">
            <span className="text-xs px-2 py-0.5 rounded-full" style={{ backgroundColor: 'var(--bg-active)', borderColor: 'var(--border-subtle)', borderStyle: 'solid', borderWidth: '1px', color: 'var(--color-text-primary)' }}>
              ••••••{setting.last4 || '••••'}
            </span>
            {setting.source === 'env' && (
              <span className="text-xs" style={{ color: 'var(--color-text-muted)' }}>
                Set via env
              </span>
            )}
          </div>
        )}
      </div>
      
      {setting.help && (
        <p className="text-xs" style={{ color: 'var(--color-text-muted)' }}>{setting.help}</p>
      )}

      <div className="flex gap-2 mt-2">
        <input
          type="password"
          autoComplete="off"
          value={inputValue}
          onChange={e => setInputValue(e.target.value)}
          placeholder={setting.is_set ? "Enter new key to update..." : "Enter API key..."}
          disabled={disabled}
          className="flex-1 text-sm px-3 py-1.5"
          style={{ backgroundColor: 'var(--bg-elevated)', color: 'var(--color-text-primary)', borderColor: 'var(--border-subtle)' }}
        />
        <button
          onClick={handleSave}
          disabled={disabled || !inputValue.trim()}
          className="pill-btn pill-btn--primary text-xs"
        >
          {setting.is_set ? 'Update' : 'Set'}
        </button>
        {setting.is_set && setting.source !== 'env' && (
          <button
            onClick={handleRemove}
            disabled={disabled}
            className="pill-btn pill-btn--ghost text-xs text-red-400 hover:text-red-300 hover:bg-red-400/10 border-red-400/20"
          >
            Remove
          </button>
        )}
      </div>
    </div>
  )
}
