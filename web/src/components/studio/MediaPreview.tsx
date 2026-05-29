import { useEffect, useState } from 'react'
import type { StudioGeneration } from '../../api/client'
import { listGenerations } from '../../api/client'
import { Icon } from '../Icon'

export function MediaPreview() {
  const [generations, setGenerations] = useState<StudioGeneration[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<StudioGeneration | null>(null)

  function refresh() {
    setLoading(true)
    setError(null)
    listGenerations()
      .then(setGenerations)
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    refresh()
  }, [])

  if (loading && generations.length === 0) {
    return (
      <div className="p-4 text-gray-500 text-sm">Loading generations…</div>
    )
  }

  if (error && generations.length === 0) {
    return (
      <div className="p-4 text-red-400 text-sm">Error: {error}</div>
    )
  }

  if (generations.length === 0) {
    return (
      <div className="p-4 text-gray-500 text-sm">No generations yet. Create one from the form.</div>
    )
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-lg font-semibold text-white">Past Generations</h3>
        <button
          onClick={refresh}
          className="text-xs text-gray-400 hover:text-white transition-colors"
        >
          Refresh
        </button>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
        {generations.map((gen) => (
          <button
            key={gen.id}
            onClick={() => setExpanded(gen)}
            className="group bg-gray-800 rounded-lg border border-gray-700 hover:border-gray-600 overflow-hidden transition-colors text-left"
          >
            {/* Thumbnail */}
            <div className="aspect-square bg-gray-900 flex items-center justify-center overflow-hidden">
              {gen.type === 'image' && (
                <img
                  src={gen.url}
                  alt={gen.prompt}
                  className="w-full h-full object-cover"
                />
              )}
              {gen.type === 'video' && (
                <div className="flex items-center justify-center text-gray-600">
                  <Icon name="movie" size={48} />
                </div>
              )}
              {gen.type === 'audio' && (
                <div className="flex items-center justify-center text-gray-600">
                  <Icon name="music_note" size={48} />
                </div>
              )}
            </div>
            {/* Info */}
            <div className="p-2">
              <p className="text-xs text-gray-300 truncate">{gen.prompt}</p>
              <p className="text-xs text-gray-500 mt-0.5">
                {gen.type} · {new Date(gen.created_at).toLocaleDateString()}
              </p>
            </div>
          </button>
        ))}
      </div>

      {/* Expanded overlay */}
      {expanded && (
        <div
          className="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-8"
          onClick={() => setExpanded(null)}
        >
          <div
            className="bg-gray-900 rounded-xl border border-gray-700 max-w-3xl w-full max-h-full overflow-auto p-4"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between mb-3">
              <p className="text-sm text-white font-medium truncate mr-4">{expanded.prompt}</p>
              <button
                onClick={() => setExpanded(null)}
                className="text-gray-400 hover:text-white text-lg flex-shrink-0"
              >
                <Icon name="close" size={16} />
              </button>
            </div>

            {expanded.type === 'image' && (
              <img
                src={expanded.url}
                alt={expanded.prompt}
                className="w-full rounded-lg"
              />
            )}
            {expanded.type === 'video' && (
              <video src={expanded.url} controls className="w-full rounded-lg" />
            )}
            {expanded.type === 'audio' && (
              <audio src={expanded.url} controls className="w-full" />
            )}

            <p className="text-xs text-gray-500 mt-3">
              Model: {expanded.model} · Created: {new Date(expanded.created_at).toLocaleString()}
            </p>
          </div>
        </div>
      )}
    </div>
  )
}
