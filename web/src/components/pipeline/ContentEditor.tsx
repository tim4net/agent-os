import { useState } from 'react'
import type { PipelineItem } from '../../api/client'

interface ContentEditorProps {
  item: PipelineItem
  generating: boolean
  onClose: () => void
  onSave: (content: string) => void
  onGenerate: () => void
  onAdvance: () => void
}

export function ContentEditor({ item, generating, onClose, onSave, onGenerate, onAdvance }: ContentEditorProps) {
  const [content, setContent] = useState(item.content ?? '')

  function handleSave() {
    onSave(content)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <div className="bg-gray-900 border border-gray-700 rounded-lg w-full max-w-2xl max-h-[90vh] flex flex-col">
        {/* Header */}
        <div className="px-6 py-4 border-b border-gray-700">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">{item.title}</h3>
            <span className="text-xs bg-gray-700 px-2 py-1 rounded capitalize">{item.type}</span>
          </div>
          {item.outline && (
            <p className="text-sm text-gray-400 mt-1">{item.outline}</p>
          )}
        </div>

        {/* Editor */}
        <div className="flex-1 overflow-auto p-6">
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            rows={16}
            className="w-full bg-gray-800 border border-gray-700 rounded px-4 py-3 text-sm font-mono focus:outline-none focus:border-blue-500 resize-none"
            placeholder="Write your content here or use AI Generate..."
          />
        </div>

        {/* Actions */}
        <div className="px-6 py-3 border-t border-gray-700 flex items-center gap-3">
          <button
            onClick={onGenerate}
            disabled={generating}
            className="text-sm px-4 py-2 bg-purple-600 hover:bg-purple-700 disabled:bg-purple-800 rounded font-medium transition-colors"
          >
            {generating ? 'Generating...' : '✨ AI Generate'}
          </button>
          <button
            onClick={handleSave}
            className="text-sm px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded font-medium transition-colors"
          >
            Save
          </button>
          {item.status !== 'published' && (
            <button
              onClick={onAdvance}
              className="text-sm px-4 py-2 bg-green-600 hover:bg-green-700 rounded font-medium transition-colors"
            >
              Advance Status →
            </button>
          )}
          <button
            onClick={onClose}
            className="text-sm px-4 py-2 text-gray-400 hover:text-white transition-colors ml-auto"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
