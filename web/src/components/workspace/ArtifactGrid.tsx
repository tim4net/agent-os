import { useEffect, useState, useCallback, useRef } from 'react'
import type { Artifact, Agent } from '../../api/client'
import { listArtifacts } from '../../api/client'
import { ArtifactPreview } from './ArtifactPreview'

const TYPE_TABS = [
  { label: 'All', value: '' },
  { label: 'Images', value: 'image' },
  { label: 'Video', value: 'video' },
  { label: 'Audio', value: 'audio' },
  { label: 'Code', value: 'code' },
  { label: 'Text', value: 'text' },
] as const

interface ArtifactGridProps {
  agents: Agent[]
  selectedAgent: Agent | null // kept for potential future use but not auto-applied as filter
  onUploadClick: () => void
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function ArtifactCard({
  artifact,
  onClick,
}: {
  artifact: Artifact
  onClick: () => void
}) {
  const fileUrl = `/api/artifacts/${artifact.id}/file`

  function renderThumbnail() {
    switch (artifact.artifact_type) {
      case 'image':
        return (
          <img
            src={fileUrl}
            alt={artifact.filename}
            className="w-full h-32 object-cover rounded-t"
            loading="lazy"
          />
        )
      case 'video':
        return (
          <div className="w-full h-32 bg-gray-800 rounded-t flex items-center justify-center">
            <svg className="w-12 h-12 text-gray-500" fill="currentColor" viewBox="0 0 24 24">
              <path d="M8 5v14l11-7z" />
            </svg>
          </div>
        )
      case 'audio':
        return (
          <div className="w-full h-32 bg-gray-800 rounded-t flex items-center justify-center">
            <svg className="w-12 h-12 text-gray-500" fill="currentColor" viewBox="0 0 24 24">
              <path d="M12 3v10.55A4 4 0 1014 17V7h4V3h-6z" />
            </svg>
          </div>
        )
      case 'code':
        return (
          <div className="w-full h-32 bg-gray-800 rounded-t flex items-center justify-center gap-2">
            <svg className="w-8 h-8 text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 20l4-16m4 4l4 4-4 4M6 16l-4-4 4-4" />
            </svg>
            <span className="text-xs text-gray-400 truncate max-w-[8rem]">{artifact.filename}</span>
          </div>
        )
      case 'text':
      default:
        return (
          <div className="w-full h-32 bg-gray-800 rounded-t flex flex-col items-center justify-center gap-2 p-2">
            <svg className="w-8 h-8 text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
            <span className="text-xs text-gray-400 truncate max-w-[8rem] text-center">
              {artifact.metadata?.['preview'] ?? artifact.filename}
            </span>
          </div>
        )
    }
  }

  return (
    <button
      onClick={onClick}
      className="bg-gray-900 rounded-lg border border-gray-800 hover:border-gray-600 transition-colors text-left overflow-hidden focus:outline-none focus:ring-2 focus:ring-blue-500"
    >
      {renderThumbnail()}
      <div className="p-3">
        <p className="text-sm font-medium text-gray-200 truncate">{artifact.filename}</p>
        <p className="text-xs text-gray-500 mt-1">
          {artifact.artifact_type} · {formatSize(artifact.size)}
        </p>
      </div>
    </button>
  )
}

export function ArtifactGrid({ agents, onUploadClick }: ArtifactGridProps) {
  const [artifacts, setArtifacts] = useState<Artifact[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [typeFilter, setTypeFilter] = useState('')
  const [agentFilter, setAgentFilter] = useState('')
  const [previewArtifact, setPreviewArtifact] = useState<Artifact | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const fetchArtifacts = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await listArtifacts(
        typeFilter || undefined,
        agentFilter || undefined,
      )
      setArtifacts(res.artifacts ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load artifacts')
      setArtifacts([])
    } finally {
      setLoading(false)
    }
  }, [typeFilter, agentFilter])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await
    fetchArtifacts()
  }, [fetchArtifacts])

  // Don't auto-set agentFilter from sidebar — Workspace should show all agents by default

  function handleDeleted() {
    setPreviewArtifact(null)
    fetchArtifacts()
  }

  return (
    <div>
      {/* Header with filters */}
      <div className="mb-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-2xl font-bold">Workspace</h2>
          <button
            onClick={onUploadClick}
            className="px-4 py-2 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 transition-colors"
          >
            + Upload
          </button>
        </div>

        {/* Type tabs */}
        <div className="flex items-center gap-1 mb-3">
          {TYPE_TABS.map((tab) => (
            <button
              key={tab.value}
              onClick={() => setTypeFilter(tab.value)}
              className={`px-3 py-1.5 text-sm rounded-full transition-colors ${
                typeFilter === tab.value
                  ? 'bg-gray-700 text-white'
                  : 'text-gray-400 hover:text-gray-200 hover:bg-gray-800'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {/* Agent filter dropdown */}
        <select
          value={agentFilter}
          onChange={(e) => setAgentFilter(e.target.value)}
          className="bg-gray-800 text-gray-200 text-sm rounded px-3 py-1.5 border border-gray-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          <option value="">All agents</option>
          {agents.map((a) => (
            <option key={a.id} value={a.id}>
              {a.display_name || a.name}
            </option>
          ))}
        </select>
      </div>

      {/* Content */}
      {loading ? (
        <p className="text-gray-400">Loading artifacts...</p>
      ) : error ? (
        <p className="text-red-400">{error}</p>
      ) : artifacts.length === 0 ? (
        <p className="text-gray-400">No artifacts found.</p>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 gap-4">
          {artifacts.map((artifact) => (
            <ArtifactCard
              key={artifact.id}
              artifact={artifact}
              onClick={() => setPreviewArtifact(artifact)}
            />
          ))}
        </div>
      )}

      {/* Preview modal */}
      {previewArtifact && (
        <ArtifactPreview
          artifact={previewArtifact}
          onClose={() => setPreviewArtifact(null)}
          onDeleted={handleDeleted}
        />
      )}

      {/* Hidden file input for upload */}
      <input
        ref={fileInputRef}
        type="file"
        className="hidden"
        onChange={() => {}}
      />
    </div>
  )
}
