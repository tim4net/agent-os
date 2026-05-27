import { useState } from 'react'
import type { Skill } from '../../api/client'
import { createSkill, updateSkill } from '../../api/client'

const CATEGORIES = ['general', 'coding', 'research', 'writing', 'automation', 'creative']

interface SkillEditorProps {
  skill?: Skill | null
  onDone: () => void
}

export function SkillEditor({ skill, onDone }: SkillEditorProps) {
  const isEditing = !!skill
  const [name, setName] = useState(skill?.name ?? '')
  const [description, setDescription] = useState(skill?.description ?? '')
  const [category, setCategory] = useState(skill?.category ?? 'general')
  const [content, setContent] = useState(skill?.content ?? '')
  const [triggersText, setTriggersText] = useState(
    skill?.triggers?.join(', ') ?? ''
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSave() {
    if (!name.trim()) {
      setError('Name is required')
      return
    }

    if (!content.trim()) {
      setError('Content is required')
      return
    }

    const triggers = triggersText
      .split(',')
      .map(t => t.trim())
      .filter(t => t.length > 0)

    setSaving(true)
    setError(null)

    try {
      if (isEditing && skill) {
        await updateSkill(skill.id, {
          name: name.trim(),
          description: description.trim(),
          category,
          content: content.trim(),
          triggers,
        })
      } else {
        await createSkill({
          name: name.trim(),
          description: description.trim(),
          category,
          content: content.trim(),
          triggers,
        })
      }
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save skill')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">
          {isEditing ? 'Edit Skill' : 'New Skill'}
        </h2>
        <button
          onClick={onDone}
          className="text-gray-400 hover:text-white text-sm"
        >
          ✕ Cancel
        </button>
      </div>

      {error && (
        <div className="mb-4 px-4 py-3 bg-red-900/30 text-red-300 border border-red-800 rounded-lg text-sm">
          {error}
        </div>
      )}

      <div className="space-y-4">
        {/* Name */}
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-1">Name</label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Code Review Assistant"
            className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
          />
        </div>

        {/* Description */}
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-1">Description</label>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="What does this skill do?"
            rows={2}
            className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500 resize-none"
          />
        </div>

        {/* Category */}
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-1">Category</label>
          <select
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
          >
            {CATEGORIES.map(cat => (
              <option key={cat} value={cat}>{cat.charAt(0).toUpperCase() + cat.slice(1)}</option>
            ))}
          </select>
        </div>

        {/* Triggers */}
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-1">
            Triggers <span className="text-gray-500 font-normal">(comma-separated)</span>
          </label>
          <input
            type="text"
            value={triggersText}
            onChange={(e) => setTriggersText(e.target.value)}
            placeholder="e.g. /review, code-review, refactor"
            className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
          />
        </div>

        {/* Content */}
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-1">
            Content <span className="text-gray-500 font-normal">(markdown prompt template)</span>
          </label>
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="Enter the skill's prompt template in markdown..."
            rows={12}
            className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm font-mono focus:outline-none focus:border-blue-500 resize-y leading-relaxed"
          />
        </div>

        {/* Actions */}
        <div className="flex gap-3 pt-4">
          <button
            onClick={handleSave}
            disabled={saving}
            className="px-6 py-2 bg-blue-600 hover:bg-blue-700 disabled:bg-gray-700 disabled:text-gray-500 rounded-lg text-sm font-medium transition-colors"
          >
            {saving ? 'Saving...' : isEditing ? 'Update Skill' : 'Create Skill'}
          </button>
          <button
            onClick={onDone}
            className="px-6 py-2 bg-gray-700 hover:bg-gray-600 rounded-lg text-sm transition-colors"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}
