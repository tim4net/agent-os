import { useState } from 'react'
import { saveMemoryFile, synthesizeMemory } from '../../api/client'
import { showToast } from '../Toast'
import { Icon } from '../Icon'

interface SynthesisModalProps {
  filePaths: string[]
  onClose: () => void
}

const SYNTHESIS_TYPES = [
  { value: 'summary', label: 'Summary' },
  { value: 'study_guide', label: 'Study Guide' },
  { value: 'flashcards', label: 'Flashcards' },
  { value: 'outline', label: 'Outline' },
] as const

export function SynthesisModal({ filePaths, onClose }: SynthesisModalProps) {
  const [synthType, setSynthType] = useState<string>('summary')
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState<{ path: string; content: string } | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSynthesize() {
    setLoading(true)
    setError(null)
    try {
      const res = await synthesizeMemory(filePaths, synthType)
      setResult(res)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Synthesis failed')
    } finally {
      setLoading(false)
    }
  }

  async function handleSaveToObsidian() {
    if (!result) return
    setSaving(true)
    try {
      const fileName = `synthesis/${synthType}-${Date.now()}.md`
      await saveMemoryFile(fileName, result.content)
      showToast(`Saved to Obsidian: ${fileName}`, 'success')
    } catch {
      showToast('Failed to save to Obsidian', 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="synthesis-title"
        className="bg-gray-900 border border-gray-700 rounded-xl shadow-2xl w-full max-w-3xl max-h-[85vh] flex flex-col mx-4"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-gray-800 flex-shrink-0">
          <h2 id="synthesis-title" className="text-lg font-semibold text-white">Notebook LM Synthesis</h2>
          <button
            onClick={onClose}
            aria-label="Close"
            className="text-gray-400 hover:text-white transition-colors text-xl leading-none"
            >
             <Icon name="close" size={16} />
            </button>
        </div>

        {/* Selected files */}
        <div className="px-5 py-3 border-b border-gray-800 flex-shrink-0">
          <p className="text-xs text-gray-500 mb-2">
            {filePaths.length} file{filePaths.length !== 1 ? 's' : ''} selected
          </p>
          <div className="flex flex-wrap gap-1.5">
            {filePaths.map((p) => (
              <span
                key={p}
                className="inline-flex items-center gap-1 px-2 py-0.5 bg-gray-800 text-gray-300 text-xs rounded-full"
                >
                 <Icon name="edit_note" size={12} /> {p.split('/').pop()}
                </span>
            ))}
          </div>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-auto p-5">
          {!result && !loading && !error && (
            <div className="flex flex-col items-center justify-center py-8 text-center">
              <p className="text-gray-400 mb-6">
                Choose a synthesis type and generate a combined document from your selected notes.
              </p>
            </div>
          )}

          {loading && (
            <div className="flex items-center justify-center py-12">
              <div className="flex items-center gap-3 text-gray-400">
                <span className="inline-flex gap-1 text-lg">
                  <span className="animate-bounce">●</span>
                  <span className="animate-bounce [animation-delay:0.1s]">●</span>
                  <span className="animate-bounce [animation-delay:0.2s]">●</span>
                </span>
                <span className="text-sm">Synthesizing...</span>
              </div>
            </div>
          )}

          {error && (
            <div className="bg-red-900/20 border border-red-900/30 text-red-400 text-sm rounded-lg p-4">
              {error}
            </div>
          )}

          {result && (
            <div>
              <div className="bg-gray-950 border border-gray-800 rounded-lg p-4 max-h-[50vh] overflow-auto">
                <pre className="text-sm text-gray-300 font-mono whitespace-pre-wrap break-words leading-relaxed">
                  {result.content}
                </pre>
              </div>
            </div>
          )}
        </div>

        {/* Footer actions */}
        <div className="flex items-center justify-between px-5 py-4 border-t border-gray-800 flex-shrink-0">
          <div className="flex items-center gap-2">
            <label className="text-xs text-gray-500">Type:</label>
            <select
              value={synthType}
              onChange={(e) => {
                setSynthType(e.target.value)
                setResult(null)
                setError(null)
              }}
              disabled={loading}
              className="bg-gray-800 text-gray-200 text-sm border border-gray-700 rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:opacity-50"
            >
              {SYNTHESIS_TYPES.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-2">
            {result && (
              <button
                onClick={handleSaveToObsidian}
                disabled={saving}
                className="px-4 py-1.5 text-xs font-medium rounded bg-green-700 text-white hover:bg-green-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                >
                 {saving ? 'Saving...' : <><Icon name="edit_note" size={14} /> Save to Obsidian</>}
                </button>
            )}
            <button
              onClick={handleSynthesize}
              disabled={loading || filePaths.length < 2}
              className="px-4 py-1.5 text-xs font-medium rounded bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
               {loading ? 'Synthesizing...' : <><Icon name="auto_awesome" size={14} /> Synthesize</>}
              </button>
          </div>
        </div>
      </div>
    </div>
  )
}
