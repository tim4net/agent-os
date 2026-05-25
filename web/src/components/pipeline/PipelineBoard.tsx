import { useState, useEffect, useCallback } from 'react'
import type { PipelineItem } from '../../api/client'
import { listPipeline, createPipelineItem, updatePipelineItem, generateContent, advancePipeline } from '../../api/client'
import { ContentEditor } from './ContentEditor'

const COLUMNS = [
  { key: 'draft', label: 'Draft' },
  { key: 'ai_review', label: 'AI Review' },
  { key: 'human_review', label: 'Human Review' },
  { key: 'published', label: 'Published' },
] as const

const typeBadgeColors: Record<string, string> = {
  blog: 'bg-indigo-500',
  social: 'bg-pink-500',
  email: 'bg-teal-500',
  ad: 'bg-orange-500',
  other: 'bg-gray-500',
}

export function PipelineBoard() {
  const [items, setItems] = useState<PipelineItem[]>([])
  const [loading, setLoading] = useState(false)
  const [typeFilter, setTypeFilter] = useState<string>('')
  const [showAdd, setShowAdd] = useState(false)
  const [newTitle, setNewTitle] = useState('')
  const [newType, setNewType] = useState<string>('blog')
  const [newOutline, setNewOutline] = useState('')
  const [editingItem, setEditingItem] = useState<PipelineItem | null>(null)
  const [generating, setGenerating] = useState<string | null>(null)

  const loadItems = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listPipeline(undefined, typeFilter || undefined)
      setItems(data)
    } catch (err) {
      console.error('Failed to load pipeline:', err)
    } finally {
      setLoading(false)
    }
  }, [typeFilter])

  useEffect(() => {
    loadItems()
  }, [loadItems])

  function getColumnItems(status: string): PipelineItem[] {
    return items.filter((i) => i.status === status)
  }

  async function handleAddItem(e: React.FormEvent) {
    e.preventDefault()
    if (!newTitle.trim()) return
    try {
      await createPipelineItem({
        title: newTitle.trim(),
        type: newType,
        outline: newOutline.trim(),
      })
      setNewTitle('')
      setNewType('blog')
      setNewOutline('')
      setShowAdd(false)
      await loadItems()
    } catch (err) {
      console.error('Failed to create pipeline item:', err)
    }
  }

  async function handleGenerate(id: string) {
    setGenerating(id)
    try {
      const updated = await generateContent(id)
      setItems((prev) => prev.map((i) => (i.id === id ? updated : i)))
    } catch (err) {
      console.error('Failed to generate content:', err)
    } finally {
      setGenerating(null)
    }
  }

  async function handleAdvance(id: string) {
    try {
      const updated = await advancePipeline(id)
      setItems((prev) => prev.map((i) => (i.id === id ? updated : i)))
    } catch (err) {
      console.error('Failed to advance pipeline:', err)
    }
  }

  async function handleSaveContent(id: string, content: string) {
    try {
      const updated = await updatePipelineItem(id, { content })
      setItems((prev) => prev.map((i) => (i.id === id ? updated : i)))
      setEditingItem(null)
    } catch (err) {
      console.error('Failed to save content:', err)
    }
  }

  async function handleGenerateInEditor(id: string) {
    setGenerating(id)
    try {
      const updated = await generateContent(id)
      setItems((prev) => prev.map((i) => (i.id === id ? updated : i)))
      setEditingItem(updated)
    } catch (err) {
      console.error('Failed to generate content:', err)
    } finally {
      setGenerating(null)
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* Filter bar */}
      <div className="px-4 py-3 border-b border-gray-800 flex items-center gap-4">
        <h2 className="text-lg font-semibold">Content Pipeline</h2>
        <select
          value={typeFilter}
          onChange={(e) => setTypeFilter(e.target.value)}
          className="bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
        >
          <option value="">All Types</option>
          <option value="blog">Blog</option>
          <option value="social">Social</option>
          <option value="email">Email</option>
          <option value="ad">Ad</option>
          <option value="other">Other</option>
        </select>
        <button
          onClick={() => loadItems()}
          className="text-sm text-gray-400 hover:text-white transition-colors"
        >
          ↻ Refresh
        </button>
        <button
          onClick={() => setShowAdd(!showAdd)}
          className="text-sm bg-blue-600 hover:bg-blue-700 px-3 py-1.5 rounded font-medium transition-colors ml-auto"
        >
          + New Item
        </button>
      </div>

      {/* Add form */}
      {showAdd && (
        <form onSubmit={handleAddItem} className="px-4 py-3 border-b border-gray-800 bg-gray-900/50 flex items-end gap-3">
          <div className="flex-1">
            <label className="block text-xs text-gray-400 mb-1">Title</label>
            <input
              type="text"
              value={newTitle}
              onChange={(e) => setNewTitle(e.target.value)}
              className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
              autoFocus
            />
          </div>
          <div>
            <label className="block text-xs text-gray-400 mb-1">Type</label>
            <select
              value={newType}
              onChange={(e) => setNewType(e.target.value)}
              className="bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
            >
              <option value="blog">Blog</option>
              <option value="social">Social</option>
              <option value="email">Email</option>
              <option value="ad">Ad</option>
              <option value="other">Other</option>
            </select>
          </div>
          <div className="flex-1">
            <label className="block text-xs text-gray-400 mb-1">Outline</label>
            <input
              type="text"
              value={newOutline}
              onChange={(e) => setNewOutline(e.target.value)}
              placeholder="Optional outline"
              className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
            />
          </div>
          <button type="submit" className="text-sm bg-blue-600 hover:bg-blue-700 px-4 py-1.5 rounded font-medium transition-colors">
            Create
          </button>
          <button type="button" onClick={() => setShowAdd(false)} className="text-sm text-gray-400 hover:text-white transition-colors">
            Cancel
          </button>
        </form>
      )}

      {/* Board */}
      {loading && items.length === 0 ? (
        <div className="flex items-center justify-center flex-1">
          <p className="text-gray-400">Loading pipeline...</p>
        </div>
      ) : (
        <div className="flex gap-4 p-4 flex-1 overflow-x-auto">
          {COLUMNS.map((col) => {
            const colItems = getColumnItems(col.key)
            return (
              <div
                key={col.key}
                className="flex-shrink-0 w-80 bg-gray-900/50 rounded-lg border border-gray-800 flex flex-col"
              >
                <div className="flex items-center justify-between px-3 py-2 border-b border-gray-800">
                  <h3 className="text-sm font-medium">{col.label}</h3>
                  <span className="text-xs text-gray-500 bg-gray-800 px-2 py-0.5 rounded-full">
                    {colItems.length}
                  </span>
                </div>
                <div className="flex-1 overflow-y-auto p-2 space-y-2 min-h-[100px]">
                  {colItems.length === 0 ? (
                    <p className="text-xs text-gray-600 text-center py-4">No items</p>
                  ) : (
                    colItems.map((item) => (
                      <div
                        key={item.id}
                        className="bg-gray-800 border border-gray-700 rounded-lg p-3"
                      >
                        <div className="flex items-start justify-between gap-2">
                          <h4 className="text-sm font-medium text-white flex-1 min-w-0 truncate">
                            {item.title}
                          </h4>
                          <span className={`${typeBadgeColors[item.type] ?? 'bg-gray-500'} text-white text-xs px-2 py-0.5 rounded-full capitalize shrink-0`}>
                            {item.type}
                          </span>
                        </div>
                        {item.outline && (
                          <p className="text-xs text-gray-400 mt-1 line-clamp-2">{item.outline}</p>
                        )}
                        <div className="flex gap-2 mt-3">
                          {(col.key === 'draft' || col.key === 'ai_review') && (
                            <button
                              onClick={() => handleGenerate(item.id)}
                              disabled={generating === item.id}
                              className="text-xs px-2 py-1 bg-purple-600 hover:bg-purple-700 disabled:bg-purple-800 rounded transition-colors"
                            >
                              {generating === item.id ? 'Generating...' : '✨ Generate'}
                            </button>
                          )}
                          {col.key !== 'published' && (
                            <button
                              onClick={() => handleAdvance(item.id)}
                              className="text-xs px-2 py-1 bg-gray-700 hover:bg-gray-600 rounded transition-colors"
                            >
                              Advance →
                            </button>
                          )}
                          <button
                            onClick={() => setEditingItem(item)}
                            className="text-xs px-2 py-1 bg-gray-700 hover:bg-gray-600 rounded transition-colors ml-auto"
                          >
                            Edit
                          </button>
                        </div>
                      </div>
                    ))
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Content editor modal */}
      {editingItem && (
        <ContentEditor
          item={editingItem}
          generating={generating === editingItem.id}
          onClose={() => setEditingItem(null)}
          onSave={(content: string) => handleSaveContent(editingItem.id, content)}
          onGenerate={() => handleGenerateInEditor(editingItem.id)}
          onAdvance={() => {
            handleAdvance(editingItem.id)
            setEditingItem(null)
          }}
        />
      )}
    </div>
  )
}
