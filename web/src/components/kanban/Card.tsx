import { useState, useEffect } from 'react'
import type { Task, Agent, LinkedNote } from '../../api/client'
import { getTaskNotes, breakdownTask } from '../../api/client'

interface CardProps {
  task: Task
  agents: Agent[]
  subtaskCount?: number
  onDelete: (id: string) => void
  onUpdate: (id: string, data: Record<string, unknown>) => void
  onBreakdown?: (id: string) => void
}

const priorityColors: Record<number, string> = {
  1: 'bg-red-500',
  2: 'bg-orange-500',
  3: 'bg-yellow-500',
  4: 'bg-blue-500',
  5: 'bg-gray-500',
}

const priorityLabels: Record<number, string> = {
  1: 'Critical',
  2: 'High',
  3: 'Medium',
  4: 'Low',
  5: 'Minimal',
}

export function Card({ task, agents, subtaskCount = 0, onDelete, onUpdate, onBreakdown }: CardProps) {
  const [expanded, setExpanded] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editTitle, setEditTitle] = useState(task.title)
  const [editDesc, setEditDesc] = useState(task.description)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [notes, setNotes] = useState<LinkedNote[]>([])
  const [loadingNotes, setLoadingNotes] = useState(false)
  const [breakingDown, setBreakingDown] = useState(false)
  const [breakdownError, setBreakdownError] = useState<string | null>(null)

  const agent = agents.find((a) => a.id === task.agent_id)

  useEffect(() => {
    if (expanded) {
      setLoadingNotes(true)
      getTaskNotes(task.id)
        .then(setNotes)
        .catch(() => setNotes([]))
        .finally(() => setLoadingNotes(false))
    }
  }, [expanded, task.id])

  function handleSave() {
    onUpdate(task.id, { title: editTitle.trim(), description: editDesc })
    setEditing(false)
  }

  function handleCancelEdit() {
    setEditTitle(task.title)
    setEditDesc(task.description)
    setEditing(false)
  }

  function handleDelete() {
    if (!confirmDelete) {
      setConfirmDelete(true)
      return
    }
    onDelete(task.id)
  }

  async function handleBreakdown(e: React.MouseEvent) {
    e.stopPropagation()
    if (breakingDown) return
    setBreakingDown(true)
    setBreakdownError(null)
    try {
      await breakdownTask(task.id)
      onBreakdown?.(task.id)
    } catch (err) {
      setBreakdownError(err instanceof Error ? err.message : 'Breakdown failed')
    } finally {
      setBreakingDown(false)
    }
  }

  return (
    <div
      draggable={!expanded}
      onDragStart={(e) => {
        e.dataTransfer.setData('text/plain', task.id)
        e.dataTransfer.effectAllowed = 'move'
      }}
      role="listitem"
      aria-label={`${task.title} - Priority ${task.priority}`}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          setExpanded(!expanded)
        }
      }}
      className="bg-gray-800 border border-gray-700 rounded-lg p-3 cursor-grab active:cursor-grabbing hover:border-gray-600 transition-colors group"
      onClick={() => { if (!editing) setExpanded(!expanded) }}
    >
      {/* Drag handle + Title row */}
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2 flex-1 min-w-0">
          {/* Grab handle icon */}
          <span
            className="text-gray-600 group-hover:text-gray-400 transition-colors cursor-grab active:cursor-grabbing select-none flex-shrink-0"
            aria-hidden="true"
            title="Drag to reorder"
          >
            <svg className="w-3.5 h-3.5" viewBox="0 0 16 16" fill="currentColor">
              <circle cx="5" cy="3" r="1.5" />
              <circle cx="11" cy="3" r="1.5" />
              <circle cx="5" cy="8" r="1.5" />
              <circle cx="11" cy="8" r="1.5" />
              <circle cx="5" cy="13" r="1.5" />
              <circle cx="11" cy="13" r="1.5" />
            </svg>
          </span>
          <h4 className="text-sm font-medium text-white truncate">
            {task.title}
          </h4>
          {subtaskCount > 0 && (
            <span
              className="text-xs bg-purple-600/30 text-purple-300 px-1.5 py-0.5 rounded-full shrink-0"
              title={`${subtaskCount} subtask${subtaskCount !== 1 ? 's' : ''}`}
            >
              {subtaskCount}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1.5 shrink-0">
          <button
            onClick={handleBreakdown}
            disabled={breakingDown}
            aria-label="Break down with AI"
            className="text-gray-400 hover:text-yellow-400 disabled:text-gray-600 transition-colors"
            title="AI Breakdown"
          >
            {breakingDown ? (
              <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
              </svg>
            ) : (
              <span className="text-sm">✨</span>
            )}
          </button>
          <span
            className={`${priorityColors[task.priority] ?? 'bg-gray-500'} text-white text-xs px-2 py-0.5 rounded-full`}
            title={priorityLabels[task.priority] ?? ''}
          >
            P{task.priority}
          </span>
        </div>
      </div>

      <div className="flex items-center gap-2 mt-2 text-xs text-gray-400">
        {agent && (
          <span className="bg-gray-700 px-2 py-0.5 rounded">
            {agent.display_name || agent.name}
          </span>
        )}
        {task.due_date && (
          <span>Due {new Date(task.due_date).toLocaleDateString()}</span>
        )}
      </div>

      {breakdownError && (
        <p className="text-xs text-red-400 mt-2">{breakdownError}</p>
      )}

      {expanded && !editing && (
        <div className="mt-3 pt-3 border-t border-gray-700">
          {task.description && (
            <p className="text-sm text-gray-300 whitespace-pre-wrap mb-3">
              {task.description}
            </p>
          )}

          {/* Related Notes */}
          <div className="mb-3">
            <p className="text-xs text-gray-400 font-medium mb-1">📝 Related Notes</p>
            {loadingNotes ? (
              <div className="space-y-1">
                <div className="h-3 bg-gray-700 rounded animate-pulse w-3/4" />
                <div className="h-3 bg-gray-700 rounded animate-pulse w-1/2" />
              </div>
            ) : notes.length === 0 ? (
              <p className="text-xs text-gray-600">No related notes.</p>
            ) : (
              <div className="space-y-1">
                {notes.map((note) => (
                  <div
                    key={note.path}
                    className="bg-gray-700/50 rounded px-2 py-1"
                  >
                    <p className="text-xs text-blue-400 truncate">{note.title}</p>
                    <p className="text-xs text-gray-500 line-clamp-1">{note.snippet}</p>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="flex gap-2">
            <button
              onClick={(e) => { e.stopPropagation(); setEditing(true) }}
              className="text-xs px-3 py-1 bg-gray-700 hover:bg-gray-600 rounded transition-colors"
            >
              Edit
            </button>
            <button
              onClick={handleBreakdown}
              disabled={breakingDown}
              aria-label="Break down with AI"
              className="text-xs px-3 py-1 bg-purple-700 hover:bg-purple-600 disabled:bg-gray-700 disabled:text-gray-500 rounded transition-colors"
            >
              {breakingDown ? 'Breaking down...' : '✨ AI Breakdown'}
            </button>
            <button
              onClick={(e) => { e.stopPropagation(); handleDelete() }}
              className={`text-xs px-3 py-1 rounded transition-colors ${
                confirmDelete
                  ? 'bg-red-600 hover:bg-red-700 text-white'
                  : 'bg-gray-700 hover:bg-gray-600 text-red-400'
              }`}
              onBlur={() => setConfirmDelete(false)}
            >
              {confirmDelete ? 'Confirm?' : 'Delete'}
            </button>
          </div>
        </div>
      )}

      {expanded && editing && (
        <div
          className="mt-3 pt-3 border-t border-gray-700 space-y-2"
          onClick={(e) => e.stopPropagation()}
        >
          <input
            type="text"
            value={editTitle}
            onChange={(e) => setEditTitle(e.target.value)}
            className="w-full bg-gray-900 border border-gray-600 rounded px-2 py-1 text-sm focus:outline-none focus:border-blue-500"
          />
          <textarea
            value={editDesc}
            onChange={(e) => setEditDesc(e.target.value)}
            rows={3}
            className="w-full bg-gray-900 border border-gray-600 rounded px-2 py-1 text-sm focus:outline-none focus:border-blue-500 resize-none"
          />
          <div className="flex gap-2">
            <button
              onClick={handleSave}
              className="text-xs px-3 py-1 bg-blue-600 hover:bg-blue-700 rounded transition-colors"
            >
              Save
            </button>
            <button
              onClick={handleCancelEdit}
              className="text-xs px-3 py-1 bg-gray-700 hover:bg-gray-600 rounded transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
