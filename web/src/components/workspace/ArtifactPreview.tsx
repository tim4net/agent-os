import { useState } from 'react'
import type { Artifact } from '../../api/client'
import { deleteArtifact } from '../../api/client'

interface ArtifactPreviewProps {
  artifact: Artifact
  onClose: () => void
  onDeleted: () => void
}

export function ArtifactPreview({ artifact, onClose, onDeleted }: ArtifactPreviewProps) {
  const [confirmDelete, setConfirmDelete] = useState(false)

  function handleDelete() {
    if (!confirmDelete) {
      setConfirmDelete(true)
      return
    }
    deleteArtifact(artifact.id)
      .then(onDeleted)
      .catch((err) => console.error('Delete failed:', err))
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
            <code>{artifact.metadata?.['preview'] ?? artifact.filename}</code>
          </pre>
        )
      case 'text':
      default:
        return (
          <div className="bg-gray-950 rounded p-4 overflow-auto max-h-[70vh] text-sm text-gray-200 border border-gray-800 whitespace-pre-wrap">
            {artifact.metadata?.['preview'] ?? `Text artifact: ${artifact.filename}`}
          </div>
        )
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
      <div className="bg-gray-900 rounded-lg border border-gray-700 w-full max-w-4xl mx-4 max-h-[90vh] flex flex-col shadow-2xl">
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
        </div>

        {/* Footer actions */}
        <div className="flex items-center justify-end gap-3 p-4 border-t border-gray-800">
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
