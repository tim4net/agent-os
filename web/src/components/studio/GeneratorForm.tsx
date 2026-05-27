import { useEffect, useState } from 'react'
import type { StudioGeneration, StudioProvider } from '../../api/client'
import { getStudioProviders, studioGenerate } from '../../api/client'

interface GeneratorFormProps {
  onGenerated?: (generation: StudioGeneration) => void
  agentId?: string
}

export function GeneratorForm({ onGenerated, agentId }: GeneratorFormProps) {
  const [prompt, setPrompt] = useState('')
  const [type, setType] = useState<'image' | 'video' | 'audio'>('image')
  const [model, setModel] = useState('')
  const [provider, setProvider] = useState('')
  const [providers, setProviders] = useState<StudioProvider[]>([])
  const [providersLoading, setProvidersLoading] = useState(true)
  const [generating, setGenerating] = useState(false)
  const [result, setResult] = useState<StudioGeneration | null>(null)
  const [error, setError] = useState<string | null>(null)

  // Fetch providers on mount
  useEffect(() => {
    getStudioProviders()
      .then((list) => {
        setProviders(list)
        // Default to first available provider
        const firstAvailable = list.find((p) => p.available)
        if (firstAvailable) {
          setProvider(firstAvailable.name)
          // Default to first model of that provider
          if (firstAvailable.models.length > 0) {
            setModel(firstAvailable.models[0])
          }
        }
      })
      .catch(() => setProviders([]))
      .finally(() => setProvidersLoading(false))
  }, [])

  // When provider changes, update model to first of that provider's models
  function handleProviderChange(name: string) {
    const selected = providers.find((p) => p.name === name)
    if (!selected || !selected.available) return
    setProvider(name)
    setModel(selected.models.length > 0 ? selected.models[0] : '')
  }

  const selectedProvider = providers.find((p) => p.name === provider)
  const noProvidersAvailable = providers.length > 0 && providers.every((p) => !p.available)
  const canGenerate = prompt.trim() && !generating && provider && selectedProvider?.available

  async function handleGenerate() {
    if (!prompt.trim() || !provider) return
    setGenerating(true)
    setError(null)
    setResult(null)
    try {
      const gen = await studioGenerate(prompt, type, model || undefined, provider, agentId)
      setResult(gen)
      onGenerated?.(gen)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Generation failed')
    } finally {
      setGenerating(false)
    }
  }

  // Loading providers
  if (providersLoading) {
    return (
      <div className="flex flex-col gap-4">
        <h3 className="text-lg font-semibold text-white">Generate Media</h3>
        <div className="p-4 text-gray-500 text-sm animate-pulse">Loading providers…</div>
      </div>
    )
  }

  // No providers at all
  if (providers.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <h3 className="text-lg font-semibold text-white">Generate Media</h3>
        <div className="p-4 bg-yellow-900/20 text-yellow-400 text-sm rounded-lg border border-yellow-900/30">
          ⚠️ No generation providers are configured. Please check your server configuration.
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <h3 className="text-lg font-semibold text-white">Generate Media</h3>

      {/* Type selector */}
      <div className="flex gap-2">
        {(['image', 'video', 'audio'] as const).map((t) => (
          <button
            key={t}
            onClick={() => setType(t)}
            className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
              type === t
                ? 'bg-blue-600 text-white'
                : 'bg-gray-800 text-gray-400 hover:bg-gray-700 hover:text-white'
            }`}
          >
            {t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {/* Provider pills */}
      <div>
        <label className="block text-xs font-medium text-gray-400 mb-2">Provider</label>
        <div className="flex flex-wrap gap-2">
          {/* Show available providers first */}
          {providers.filter(p => p.available).map((p) => {
            const isSelected = provider === p.name
            return (
              <button
                key={p.name}
                onClick={() => handleProviderChange(p.name)}
                className={`px-3 py-1.5 text-sm rounded-lg transition-colors border ${
                  isSelected
                    ? 'bg-blue-600 text-white border-blue-500'
                    : 'bg-gray-800 text-gray-300 border-gray-700 hover:bg-gray-700 hover:text-white hover:border-gray-600'
                }`}
              >
                {p.name}
              </button>
            )
          })}
          {/* Show unavailable providers with lock, only if any exist */}
          {providers.filter(p => !p.available).length > 0 && (
            <details className="group/details">
              <summary className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-lg border border-gray-800 bg-gray-800/50 text-gray-600 cursor-pointer hover:text-gray-400 transition-colors list-none">
                <span>🔒 {providers.filter(p => !p.available).length} locked</span>
                <svg className="w-3 h-3 transition-transform group-open/details:rotate-180" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
                </svg>
              </summary>
              <div className="flex flex-wrap gap-2 mt-2">
                {providers.filter(p => !p.available).map((p) => (
                  <button
                    key={p.name}
                    disabled
                    title="API key not configured"
                    className="px-3 py-1.5 text-sm rounded-lg transition-colors border bg-gray-800/50 text-gray-600 border-gray-800 cursor-not-allowed flex items-center gap-1.5"
                  >
                    {p.name}
                    <span className="text-gray-600 text-xs">🔒</span>
                  </button>
                ))}
              </div>
            </details>
          )}
        </div>
      </div>

      {/* No available providers warning */}
      {noProvidersAvailable && (
        <div className="p-3 bg-yellow-900/20 text-yellow-400 text-sm rounded-lg border border-yellow-900/30">
          ⚠️ No providers are available. Configure an API key to enable image generation.
        </div>
      )}

      {/* Model dropdown */}
      {selectedProvider && selectedProvider.models.length > 0 && (
        <div>
          <label className="block text-xs font-medium text-gray-400 mb-2">Model</label>
          <select
            value={model}
            onChange={(e) => setModel(e.target.value)}
            className="w-full bg-gray-800 text-gray-200 text-sm px-3 py-2 rounded-lg border border-gray-700 focus:border-blue-500 focus:outline-none appearance-none cursor-pointer"
          >
            {selectedProvider.models.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </div>
      )}

      {/* Prompt textarea */}
      <textarea
        placeholder="Describe what you want to generate…"
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        rows={4}
        className="w-full bg-gray-800 text-gray-200 text-sm px-3 py-2 rounded-lg border border-gray-700 focus:border-blue-500 focus:outline-none placeholder-gray-500 resize-none"
      />

      {/* Generate button */}
      <button
        onClick={handleGenerate}
        disabled={!canGenerate}
        className="px-4 py-2 bg-blue-600 text-white text-sm font-medium rounded-lg hover:bg-blue-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {generating ? (
          <span className="flex items-center gap-2">
            <span className="animate-spin">⏳</span> Generating…
          </span>
        ) : (
          `Generate ${type}`
        )}
      </button>

      {/* Error */}
      {error && (
        <div className="p-3 bg-red-900/20 text-red-400 text-sm rounded-lg border border-red-900/30">
          {error}
        </div>
      )}

      {/* Result preview */}
      {result && (
        <div className="p-3 bg-gray-800 rounded-lg border border-gray-700">
          <p className="text-xs text-gray-400 mb-2">
            Generated {result.type} via <span className="text-gray-300">{provider}</span>
          </p>
          {result.type === 'image' && (
            <img
              src={result.url}
              alt={result.prompt}
              className="w-full rounded-lg max-h-96 object-contain"
            />
          )}
          {result.type === 'video' && (
            <video
              src={result.url}
              controls
              className="w-full rounded-lg max-h-96"
            />
          )}
          {result.type === 'audio' && (
            <audio src={result.url} controls className="w-full" />
          )}
          <p className="text-xs text-gray-500 mt-2 truncate">
            Prompt: {result.prompt}
          </p>
        </div>
      )}
    </div>
  )
}
