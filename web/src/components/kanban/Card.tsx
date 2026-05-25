import { useState, useEffect } from 'react'
import type { Task, Agent, LinkedNote } from '../../api/client'
import { getTaskNotes } from '../../api/client'

interface CardProps {
  task: Task
  agents: Agent[]
  onDelete: (id: string) => void
  onUpdate: (id: string, data: Record<string, unknown>) => void
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

export function Card({ task, agents, onDelete, onUpdate }: CardProps) {
  const [expanded, setExpanded] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editTitle, setEditTitle] = useState(task.title)
  const [editDesc, setEditDesc] = useState(task.description)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [notes, setNotes] = useState<LinkedNote[]>([])
  const [loadingNotes, setLoadingNotes] = useState(false)

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

  return (
    <div
      draggable={!expanded}
      onDragStart={(e) => {
        e.dataTransfer.setData('text/plain', task.id)
        e.dataTransfer.effectAllowed = 'move'
      }}
      className="bg-gray-800 border border-gray-700 rounded-lg p-3 cursor-pointer hover:border-gray-600 transition-colors"
      onClick={() => { if (!editing) setExpanded(!expanded) }}
    >
      <div className="flex items-start justify-between gap-2">
        <h4 className="text-sm font-medium text-white flex-1 min-w-0 truncate">
          {task.title}
        </h4>
        <span
          className={`${priorityColors[task.priority] ?? 'bg-gray-500'} text-white text-xs px-2 py-0.5 rounded-full shrink-0`}
          title={priorityLabels[task.priority] ?? ''}
        >
          P{task.priority}
        </span>
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
