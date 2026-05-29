import { useState, useEffect, useCallback } from 'react'
import type { Skill } from '../../api/client'
import { listSkills, getSkill, deleteSkill, syncSkillsFromHermes } from '../../api/client'
import { Icon } from '../Icon'
import { SkillEditor } from './SkillEditor'

const CATEGORIES = ['general', 'coding', 'research', 'writing', 'automation', 'creative', 'devops', 'gaming', 'media', 'productivity', 'software-development', 'mlops', 'apple', 'email', 'github', 'mcp', 'smart-home', 'social-media', 'red-teaming', 'note-taking', 'data-science', 'diagramming', 'dogfood', 'domain', 'inference-sh', 'multi-tier-llm-routing', 'autonomous-ai-agents', 'yuanbao'] as const

const CATEGORY_COLORS: Record<string, string> = {
  general: 'bg-gray-700 text-gray-300',
  coding: 'bg-blue-900/50 text-blue-300 border border-blue-800',
  'software-development': 'bg-blue-900/50 text-blue-300 border border-blue-800',
  research: 'bg-purple-900/50 text-purple-300 border border-purple-800',
  writing: 'bg-green-900/50 text-green-300 border border-green-800',
  automation: 'bg-yellow-900/50 text-yellow-300 border border-yellow-800',
  creative: 'bg-pink-900/50 text-pink-300 border border-pink-800',
  devops: 'bg-orange-900/50 text-orange-300 border border-orange-800',
  gaming: 'bg-red-900/50 text-red-300 border border-red-800',
  media: 'bg-teal-900/50 text-teal-300 border border-teal-800',
  productivity: 'bg-indigo-900/50 text-indigo-300 border border-indigo-800',
  mlops: 'bg-cyan-900/50 text-cyan-300 border border-cyan-800',
  apple: 'bg-gray-700 text-gray-300',
  email: 'bg-amber-900/50 text-amber-300 border border-amber-800',
  github: 'bg-gray-700 text-gray-300',
  mcp: 'bg-violet-900/50 text-violet-300 border border-violet-800',
  'smart-home': 'bg-lime-900/50 text-lime-300 border border-lime-800',
  'social-media': 'bg-sky-900/50 text-sky-300 border border-sky-800',
  'red-teaming': 'bg-rose-900/50 text-rose-300 border border-rose-800',
  'note-taking': 'bg-emerald-900/50 text-emerald-300 border border-emerald-800',
  'data-science': 'bg-fuchsia-900/50 text-fuchsia-300 border border-fuchsia-800',
  diagramming: 'bg-gray-700 text-gray-300',
  dogfood: 'bg-gray-700 text-gray-300',
  domain: 'bg-gray-700 text-gray-300',
  'inference-sh': 'bg-gray-700 text-gray-300',
  'multi-tier-llm-routing': 'bg-gray-700 text-gray-300',
  'autonomous-ai-agents': 'bg-gray-700 text-gray-300',
  yuanbao: 'bg-gray-700 text-gray-300',
}

export function SkillsList() {
  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(false)
  const [categoryFilter, setCategoryFilter] = useState<string>('all')
  const [editingSkill, setEditingSkill] = useState<Skill | null>(null)
  const [creating, setCreating] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [syncResult, setSyncResult] = useState<string | null>(null)

  const loadSkills = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listSkills()
      setSkills(data)
    } catch (err) {
      console.error('Failed to load skills:', err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadSkills()
  }, [loadSkills])

  async function handleDelete(id: string, name: string) {
    if (!confirm(`Delete skill "${name}"?`)) return
    try {
      await deleteSkill(id)
      await loadSkills()
    } catch (err) {
      console.error('Failed to delete skill:', err)
    }
  }

  async function handleEdit(skill: Skill) {
    try {
      // List returns summaries without content; fetch full skill for editing
      const fullSkill = await getSkill(skill.id)
      setEditingSkill(fullSkill)
    } catch (err) {
      console.error('Failed to load skill for editing:', err)
      // Fallback: open editor with whatever we have
      setEditingSkill(skill)
    }
  }

  function handleEditDone() {
    setEditingSkill(null)
    setCreating(false)
    loadSkills()
  }

  async function handleSync() {
    setSyncing(true)
    setSyncResult(null)
    try {
      const result = await syncSkillsFromHermes()
      setSyncResult(`Synced ${result.synced} of ${result.total} skills from Hermes.`)
      await loadSkills()
    } catch (err) {
      setSyncResult(`Sync failed: ${err instanceof Error ? err.message : 'Unknown error'}`)
    } finally {
      setSyncing(false)
    }
  }

  const filtered = categoryFilter === 'all'
    ? skills
    : skills.filter(s => s.category === categoryFilter)

  if (creating || editingSkill) {
    return (
      <div className="p-6">
        <SkillEditor
          skill={editingSkill}
          onDone={handleEditDone}
        />
      </div>
    )
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">Skills</h2>
        <div className="flex items-center gap-2">
          <button
            onClick={handleSync}
            disabled={syncing}
            className="px-4 py-2 bg-purple-600 hover:bg-purple-700 disabled:bg-purple-800 disabled:opacity-60 rounded-lg text-sm font-medium transition-colors"
            aria-label="Sync skills from Hermes"
          >
            {syncing ? '⟳ Syncing...' : <><Icon name="refresh" size={14} /> Sync from Hermes</>}
          </button>
          <button
            onClick={() => setCreating(true)}
            className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm font-medium transition-colors"
          >
            + New Skill
          </button>
        </div>
      </div>

      {/* Sync result feedback */}
      {syncResult && (
        <div className={`mb-4 px-4 py-2 rounded-lg text-sm ${
          syncResult.startsWith('Synced')
            ? 'bg-green-900/30 text-green-300 border border-green-800'
            : 'bg-red-900/30 text-red-300 border border-red-800'
        }`}>
          {syncResult}
          <button
            onClick={() => setSyncResult(null)}
            className="ml-2 text-gray-400 hover:text-white"
            aria-label="Dismiss"
          >
            <Icon name="close" size={16} />
          </button>
        </div>
      )}

      {/* Category filter — only show when there are skills */}
      {skills.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-6">
          <button
            onClick={() => setCategoryFilter('all')}
            className={`text-xs px-3 py-1.5 rounded-full transition-colors ${
              categoryFilter === 'all'
                ? 'bg-gray-600 text-white'
                : 'bg-gray-800 text-gray-400 hover:bg-gray-700'
            }`}
          >
            All ({skills.length})
          </button>
          {CATEGORIES.filter(cat => skills.some(s => s.category === cat)).map(cat => {
            const count = skills.filter(s => s.category === cat).length
            return (
              <button
                key={cat}
                onClick={() => setCategoryFilter(cat)}
                className={`text-xs px-3 py-1.5 rounded-full transition-colors ${
                  categoryFilter === cat
                    ? 'bg-gray-600 text-white'
                    : 'bg-gray-800 text-gray-400 hover:bg-gray-700'
                }`}
              >
                {cat} ({count})
              </button>
            )
          })}
        </div>
      )}

      {loading && skills.length === 0 ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="bg-gray-900 border border-gray-800 rounded-lg p-4 animate-pulse">
              <div className="h-5 bg-gray-800 rounded w-3/4 mb-3" />
              <div className="h-3 bg-gray-800 rounded w-1/2 mb-2" />
              <div className="h-3 bg-gray-800 rounded w-1/3" />
            </div>
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <div className="text-center py-12">
          <p className="text-gray-500 mb-2">
            {categoryFilter === 'all' ? 'No skills yet.' : `No ${categoryFilter} skills.`}
          </p>
          <p className="text-gray-600 text-sm mb-4">
            Click "Sync from Hermes" to import skills from the Hermes agent.
          </p>
          <button
            onClick={() => setCreating(true)}
            className="text-blue-400 hover:text-blue-300 text-sm"
          >
            Or create your first skill manually
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {filtered.map(skill => (
            <div
              key={skill.id}
              className="bg-gray-900 border border-gray-800 rounded-lg p-4 hover:border-gray-700 transition-colors flex flex-col"
            >
              <div className="flex items-start justify-between mb-2">
                <h3 className="text-sm font-medium text-white truncate flex-1">
                  {skill.name}
                </h3>
                <span className={`text-xs px-2 py-0.5 rounded-full ml-2 shrink-0 ${CATEGORY_COLORS[skill.category] || CATEGORY_COLORS.general}`}>
                  {skill.category}
                </span>
              </div>

              {skill.description && (
                <p className="text-xs text-gray-500 mb-3 line-clamp-2">{skill.description}</p>
              )}

              {skill.triggers && skill.triggers.length > 0 && (
                <div className="flex flex-wrap gap-1 mb-3">
                  {skill.triggers.map((trigger, i) => (
                    <span
                      key={i}
                      className="text-xs bg-gray-800 text-gray-400 px-2 py-0.5 rounded"
                    >
                      {trigger}
                    </span>
                  ))}
                </div>
              )}

              <div className="flex gap-2 mt-auto pt-2 border-t border-gray-800">
                <button
                  onClick={() => handleEdit(skill)}
                  className="text-xs px-3 py-1.5 bg-gray-700 hover:bg-gray-600 rounded transition-colors"
                >
                  Edit
                </button>
                <button
                  onClick={() => handleDelete(skill.id, skill.name)}
                  className="text-xs px-3 py-1.5 bg-gray-700 hover:bg-gray-600 text-red-400 rounded transition-colors"
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
