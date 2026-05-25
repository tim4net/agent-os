import { useEffect, useState } from 'react'
import { getMemoryFile, saveMemoryFile } from '../../api/client'

interface NoteViewerProps {
  filePath: string | null
}

export function NoteViewer({ filePath }: NoteViewerProps) {
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [editContent, setEditContent] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!filePath) {
      setContent(null)
      setEditing(false)
      return
    }
    setLoading(true)
    setError(null)
    setEditing(false)
    getMemoryFile(filePath)
      .then((file) => setContent(file.content))
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [filePath])

  function startEdit() {
    if (content === null) return
    setEditContent(content)
    setEditing(true)
  }

  async function handleSave() {
    if (!filePath) return
    setSaving(true)
    setError(null)
    try {
      const saved = await saveMemoryFile(filePath, editContent)
      setContent(saved.content)
      setEditing(false)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  function cancelEdit() {
    setEditing(false)
    setEditContent('')
  }

  if (!filePath) {
    return (
      <div className="flex items-center justify-center h-full text-gray-500">
        Select a file from the tree to view its contents.
      </div>
    )
  }

  const fileName = filePath.split('/').pop() ?? filePath

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-gray-800 flex-shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-lg">📝</span>
          <h3 className="text-sm font-semibold text-white truncate">{fileName}</h3>
          <span className="text-xs text-gray-500 truncate">{filePath}</span>
        </div>
        {!editing && content !== null && (
          <button
            onClick={startEdit}
            className="px-3 py-1 text-xs bg-gray-800 text-gray-300 hover:bg-gray-700 hover:text-white rounded transition-colors flex-shrink-0"
          >
            Edit
          </button>
        )}
      </div>

      {/* Error */}
      {error && (
        <div className="px-4 py-2 bg-red-900/20 text-red-400 text-sm border-b border-red-900/30">
          {error}
        </div>
      )}

      {/* Body */}
      <div className="flex-1 overflow-auto">
        {loading ? (
          <div className="p-4 text-gray-500 text-sm">Loading…</div>
        ) : editing ? (
          <div className="flex flex-col h-full">
            <textarea
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              className="flex-1 w-full bg-gray-900 text-gray-200 font-mono text-sm p-4 resize-none border-none outline-none"
              spellCheck={false}
            />
            <div className="flex items-center gap-2 px-4 py-2 border-t border-gray-800 bg-gray-900 flex-shrink-0">
              <button
                onClick={handleSave}
                disabled={saving}
                className="px-4 py-1.5 text-xs bg-blue-600 text-white hover:bg-blue-500 rounded transition-colors disabled:opacity-50"
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
              <button
                onClick={cancelEdit}
                disabled={saving}
                className="px-4 py-1.5 text-xs bg-gray-700 text-gray-300 hover:bg-gray-600 rounded transition-colors disabled:opacity-50"
              >
                Cancel
              </button>
            </div>
          </div>
        ) : content !== null ? (
          <pre className="p-4 text-sm text-gray-300 font-mono whitespace-pre-wrap break-words leading-relaxed">
            {content}
          </pre>
        ) : null}
      </div>
    </div>
  )
}
