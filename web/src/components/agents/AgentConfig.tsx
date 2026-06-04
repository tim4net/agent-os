import { useEffect, useState } from 'react'
import type { Agent } from '../../api/client'
import { getAgentConfig, updateAgentConfig } from '../../api/client'
import { showToast } from '../toast-bus'

interface AgentConfigProps {
  agent: Agent
  onClose: () => void
  onSaved?: (agent: Agent) => void
}

export function AgentConfig({ agent, onClose, onSaved }: AgentConfigProps) {
  const [role, setRole] = useState('')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    getAgentConfig(agent.id)
      .then((config) => {
        setRole(config.role ?? '')
        setSystemPrompt(config.system_prompt ?? '')
      })
      .catch(() => {
        // Agent might not have config yet, use defaults
        setRole(agent.role ?? '')
        setSystemPrompt(agent.system_prompt ?? '')
      })
      .finally(() => setLoading(false))
  }, [agent.id, agent.role, agent.system_prompt])

  // Close on Escape key
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  async function handleSave() {
    setSaving(true)
    try {
      const updated = await updateAgentConfig(agent.id, {
        role,
        system_prompt: systemPrompt,
        persona: {},
      })
      showToast('Agent configuration saved', 'success')
      onSaved?.(updated)
      onClose()
    } catch {
      showToast('Failed to save agent configuration', 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="agent-config-title"
        className="bg-gray-900 border border-gray-700 rounded-lg w-full max-w-lg max-h-[80vh] flex flex-col shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-gray-800">
          <h3 id="agent-config-title" className="text-lg font-semibold text-white">
            Configure {agent.display_name || agent.name}
          </h3>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-white transition-colors"
            aria-label="Close"
          >
            <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-4 space-y-4">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <svg className="animate-spin h-6 w-6 text-gray-400" viewBox="0 0 24 24">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
              </svg>
              <span className="ml-2 text-gray-400 text-sm">Loading config...</span>
            </div>
          ) : (
            <>
              {/* Role field */}
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1">Role</label>
                <input
                  type="text"
                  value={role}
                  onChange={(e) => setRole(e.target.value)}
                  placeholder="e.g., Research Assistant, Code Reviewer, Task Manager"
                  className="w-full bg-gray-800 text-gray-100 text-sm rounded-lg px-4 py-2 border border-gray-700 focus:outline-none focus:ring-1 focus:ring-blue-500 placeholder-gray-500"
                />
                <p className="mt-1 text-xs text-gray-500">
                  A short label describing the agent's purpose or specialization.
                </p>
              </div>

              {/* System Prompt field */}
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1">System Prompt</label>
                <textarea
                  value={systemPrompt}
                  onChange={(e) => setSystemPrompt(e.target.value)}
                  placeholder="Enter the system prompt that will be prepended to every conversation..."
                  rows={8}
                  className="w-full bg-gray-800 text-gray-100 text-sm rounded-lg px-4 py-2 border border-gray-700 resize-y focus:outline-none focus:ring-1 focus:ring-blue-500 placeholder-gray-500"
                />
                <p className="mt-1 text-xs text-gray-500">
                  This prompt is injected as a system message at the start of every chat with this agent.
                </p>
              </div>
            </>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 p-4 border-t border-gray-800">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm text-gray-300 hover:text-white bg-gray-800 hover:bg-gray-700 rounded-lg transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={saving || loading}
            className="px-4 py-2 text-sm font-medium bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
          >
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  )
}
