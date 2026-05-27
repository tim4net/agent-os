import { useState, useEffect } from 'react'
import type { Artifact, LinkedNote } from '../../api/client'
import { deleteArtifact, getArtifactNotes, exportArtifact } from '../../api/client'
import { showToast } from '../Toast'

interface ArtifactPreviewProps {
  artifact: Artifact
  onClose: () => void
  onDeleted: () => void
}

export function ArtifactPreview({ artifact, onClose, onDeleted }: ArtifactPreviewProps) {
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [notes, setNotes] = useState<LinkedNote[]>([])
  const [loadingNotes, setLoadingNotes] = useState(true)
  const [textContent, setTextContent] = useState<string | null>(null)
  const [loadingText, setLoadingText] = useState(false)

  useEffect(() => {
    getArtifactNotes(artifact.id)
      .then(setNotes)
      .catch(() => setNotes([]))
      .finally(() => setLoadingNotes(false))
  }, [artifact.id])

  // Fetch actual content for code/text artifacts
  useEffect(() => {
    if (artifact.artifact_type === 'code' || artifact.artifact_type === 'text') {
      setLoadingText(true)
      fetch(`/api/artifacts/${artifact.id}/file`)
        .then((res) => {
          if (!res.ok) throw new Error('Failed to fetch')
          return res.text()
        })
        .then((text) => setTextContent(text))
        .catch(() => setTextContent(artifact.metadata?.['preview'] ?? null))
        .finally(() => setLoadingText(false))
    }
  }, [artifact.id, artifact.artifact_type, artifact.metadata])

  function handleDelete() {
    if (!confirmDelete) {
      setConfirmDelete(true)
      return
    }
    deleteArtifact(artifact.id)
      .then(onDeleted)
      .catch((err) => console.error('Delete failed:', err))
  }

  async function handleExport() {
    setExporting(true)
    try {
      const result = await exportArtifact(artifact.id)
      showToast(`Exported to Obsidian: ${result.path}`, 'success')
    } catch {
      showToast('Failed to export artifact', 'error')
    } finally {
      setExporting(false)
    }
  }

  const fileUrl = `/api/artifacts/${artifact.id}/file`

  function renderContent() {
    switch (artifact.artifact_type) {
      case 'image':
        return (
          <div className="flex items-center justify-center max-h-[70vh] overflow-auto">
            <img
              src={fileUrl}
              alt={artifact.filename}
              className="max-w-full max-h-[70vh] object-contain rounded"
            />
          </div>
        )
      case 'video':
        return (
          <video
            src={fileUrl}
            controls
            className="w-full max-h-[70vh] rounded"
          />
        )
      case 'audio':
        return (
          <div className="flex items-center justify-center py-12">
            <audio src={fileUrl} controls className="w-full max-w-lg" />
          </div>
        )
      case 'code':
        return (
          <pre className="bg-gray-950 rounded p-4 overflow-auto max-h-[70vh] text-sm text-gray-200 border border-gray-800">
            <code>{loadingText ? 'Loading...' : (textContent ?? artifact.metadata?.['preview'] ?? artifact.filename)}</code>
          </pre>
        )
      case 'text':
      default:
        return (
          <div className="bg-gray-950 rounded p-4 overflow-auto max-h-[70vh] text-sm text-gray-200 border border-gray-800 whitespace-pre-wrap">
            {loadingText ? 'Loading...' : (textContent ?? artifact.metadata?.['preview'] ?? `Text artifact: ${artifact.filename}`)}
          </div>
        )
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={`Preview ${artifact.filename}`}
        className="bg-gray-900 rounded-lg border border-gray-700 w-full max-w-4xl mx-4 max-h-[90vh] flex flex-col shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-gray-800">
          <div className="min-w-0">
            <h3 className="text-lg font-semibold text-white truncate">
              {artifact.filename}
            </h3>
            <p className="text-xs text-gray-400 mt-0.5">
              {artifact.artifact_type} · {artifact.content_type} · {(artifact.size / 1024).toFixed(1)} KB
            </p>
          </div>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-white transition-colors ml-4 flex-shrink-0"
            aria-label="Close"
          >
            <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-auto p-4">
          {renderContent()}

          {/* Linked Notes */}
          <div className="mt-4 pt-4 border-t border-gray-800">
            <h4 className="text-sm font-medium text-gray-300 mb-2">📝 Linked Notes</h4>
            {loadingNotes ? (
              <div className="space-y-2">
                <div className="h-4 bg-gray-800 rounded animate-pulse w-3/4" />
                <div className="h-4 bg-gray-800 rounded animate-pulse w-1/2" />
              </div>
            ) : notes.length === 0 ? (
              <p className="text-xs text-gray-500">No linked notes found.</p>
            ) : (
              <div className="space-y-2">
                {notes.map((note) => (
                  <a
                    key={note.path}
                    href={`/api/memory/file?path=${encodeURIComponent(note.path)}`}
                    className="block bg-gray-800 hover:bg-gray-750 border border-gray-700 rounded-lg px-3 py-2 transition-colors"
                  >
                    <p className="text-sm text-blue-400 font-medium truncate">{note.title}</p>
                    <p className="text-xs text-gray-400 mt-0.5 line-clamp-2">{note.snippet}</p>
                  </a>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* Footer actions */}
        <div className="flex items-center justify-end gap-3 p-4 border-t border-gray-800">
          <button
            onClick={handleExport}
            disabled={exporting}
            className="px-4 py-2 text-sm font-medium rounded bg-gray-800 text-gray-200 hover:bg-gray-700 transition-colors disabled:opacity-50"
            aria-label="Export to Obsidian"
          >
            {exporting ? 'Exporting...' : '📄 Export to Obsidian'}
          </button>
          <a
            href={fileUrl}
            download={artifact.filename}
            className="px-4 py-2 text-sm font-medium rounded bg-gray-800 text-gray-200 hover:bg-gray-700 transition-colors"
          >
            Download
          </a>
          <button
            onClick={handleDelete}
            onBlur={() => setConfirmDelete(false)}
            className={`px-4 py-2 text-sm font-medium rounded transition-colors ${
              confirmDelete
                ? 'bg-red-600 text-white hover:bg-red-700'
                : 'bg-gray-800 text-red-400 hover:bg-gray-700'
            }`}
          >
            {confirmDelete ? 'Confirm Delete' : 'Delete'}
          </button>
        </div>
      </div>
    </div>
  )
}
