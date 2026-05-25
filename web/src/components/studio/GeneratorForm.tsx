import { useState } from 'react'
import type { StudioGeneration } from '../../api/client'
import { studioGenerate } from '../../api/client'

interface GeneratorFormProps {
  onGenerated?: (generation: StudioGeneration) => void
}

export function GeneratorForm({ onGenerated }: GeneratorFormProps) {
  const [prompt, setPrompt] = useState('')
  const [type, setType] = useState<'image' | 'video' | 'audio'>('image')
  const [model, setModel] = useState('')
  const [generating, setGenerating] = useState(false)
  const [result, setResult] = useState<StudioGeneration | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function handleGenerate() {
    if (!prompt.trim()) return
    setGenerating(true)
    setError(null)
    setResult(null)
    try {
      const gen = await studioGenerate(prompt, type, model || undefined)
      setResult(gen)
      onGenerated?.(gen)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Generation failed')
    } finally {
      setGenerating(false)
    }
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

      {/* Model input */}
      <input
        type="text"
        placeholder="Model (optional)"
        value={model}
        onChange={(e) => setModel(e.target.value)}
        className="w-full bg-gray-800 text-gray-200 text-sm px-3 py-2 rounded-lg border border-gray-700 focus:border-blue-500 focus:outline-none placeholder-gray-500"
      />

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
        disabled={generating || !prompt.trim()}
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
          <p className="text-xs text-gray-400 mb-2">Generated {result.type}</p>
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
