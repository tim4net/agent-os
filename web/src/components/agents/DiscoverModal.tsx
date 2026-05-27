import { useState, useEffect } from 'react'

interface DiscoveredAgent {
  id: string
  name: string
  base_url: string
  harness: string
}

interface DiscoverModalProps {
  agents: DiscoveredAgent[]
  loading: boolean
  onRegister: (agent: DiscoveredAgent) => void
  onClose: () => void
}

export function DiscoverModal({ agents, loading, onRegister, onClose }: DiscoverModalProps) {
  const [registering, setRegistering] = useState<string | null>(null)

  // Close on Escape key
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  async function handleRegister(agent: DiscoveredAgent) {
    setRegistering(agent.id)
    try {
      await onRegister(agent)
    } finally {
      setRegistering(null)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="discover-modal-title"
        className="bg-gray-900 border border-gray-700 rounded-lg w-full max-w-lg max-h-[80vh] flex flex-col shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-4 border-b border-gray-800">
          <h3 id="discover-modal-title" className="text-lg font-semibold text-white">Discovered Agents</h3>
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

        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <svg className="animate-spin h-6 w-6 text-gray-400" viewBox="0 0 24 24">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
              </svg>
              <span className="ml-2 text-gray-400 text-sm">Scanning network...</span>
            </div>
          ) : agents.length === 0 ? (
            <p className="text-gray-500 text-sm text-center py-8">No unregistered agents found on the network.</p>
          ) : (
            agents.map((agent) => (
              <div
                key={agent.id}
                className="bg-gray-800 border border-gray-700 rounded-lg p-4 flex items-center justify-between gap-3"
              >
                <div className="min-w-0">
                  <p className="text-sm font-medium text-white truncate">{agent.name}</p>
                  <p className="text-xs text-gray-400 truncate">{agent.base_url} · {agent.harness}</p>
                </div>
                <button
                  onClick={() => handleRegister(agent)}
                  disabled={registering === agent.id}
                  className="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed rounded font-medium transition-colors shrink-0"
                >
                  {registering === agent.id ? 'Registering...' : 'Register'}
                </button>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
