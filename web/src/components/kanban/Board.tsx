import { useState, useEffect, useCallback } from 'react'
import type { Agent, Task } from '../../api/client'
import { listTasks, createTask, updateTask, deleteTask, reorderTasks } from '../../api/client'
import { Card } from './Card'
import { TaskModal } from './TaskModal'

const COLUMNS = [
  { key: 'backlog', label: 'Backlog' },
  { key: 'in_progress', label: 'In Progress' },
  { key: 'review', label: 'Review' },
  { key: 'done', label: 'Done' },
] as const

interface BoardProps {
  agents: Agent[]
}

export function Board({ agents }: BoardProps) {
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(false)
  const [agentFilter, setAgentFilter] = useState<string>('')
  const [showModal, setShowModal] = useState(false)
  const [modalStatus, setModalStatus] = useState<string>('backlog')
  const [dragOverColumn, setDragOverColumn] = useState<string | null>(null)

  const loadTasks = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listTasks(undefined, agentFilter || undefined)
      setTasks(data)
    } catch (err) {
      console.error('Failed to load tasks:', err)
    } finally {
      setLoading(false)
    }
  }, [agentFilter])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await, not synchronously
    loadTasks()
  }, [loadTasks])

  function handleAddTask(status: string) {
    setModalStatus(status)
    setShowModal(true)
  }

  async function handleSaveTask(data: {
    title: string
    description: string
    status: string
    priority: number
    agent_id: string | null
    due_date: string | null
  }) {
    try {
      await createTask(data)
      setShowModal(false)
      await loadTasks()
    } catch (err) {
      console.error('Failed to create task:', err)
    }
  }

  async function handleUpdateTask(id: string, data: Record<string, unknown>) {
    try {
      await updateTask(id, data)
      await loadTasks()
    } catch (err) {
      console.error('Failed to update task:', err)
    }
  }

  async function handleDeleteTask(id: string) {
    try {
      await deleteTask(id)
      await loadTasks()
    } catch (err) {
      console.error('Failed to delete task:', err)
    }
  }

  function getColumnTasks(status: string): Task[] {
    return tasks
      .filter((t) => t.status === status)
      .sort((a, b) => a.order - b.order)
  }

  async function handleDrop(e: React.DragEvent, newStatus: string) {
    e.preventDefault()
    setDragOverColumn(null)
    const taskId = e.dataTransfer.getData('text/plain')
    if (!taskId) return

    const task = tasks.find((t) => t.id === taskId)
    if (!task || task.status === newStatus) return

    // Optimistic update
    setTasks((prev) =>
      prev.map((t) => (t.id === taskId ? { ...t, status: newStatus as Task['status'] } : t)),
    )

    try {
      const columnTasks = tasks
        .filter((t) => t.status === newStatus || t.id === taskId)
        .map((t, i) => ({
          id: t.id,
          status: t.id === taskId ? newStatus : t.status,
          order: t.id === taskId ? 0 : i + 1,
        }))

      await reorderTasks(columnTasks)
    } catch (err) {
      console.error('Failed to reorder tasks:', err)
      await loadTasks()
    }
  }

  function handleDragOver(e: React.DragEvent) {
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
  }

  function handleDragLeave() {
    setDragOverColumn(null)
  }

  return (
    <div className="flex flex-col h-full">
      {/* Filter bar */}
      <div className="px-4 py-3 border-b border-gray-800 flex items-center gap-4">
        <h2 className="text-lg font-semibold">Kanban Board</h2>
        <select
          value={agentFilter}
          onChange={(e) => setAgentFilter(e.target.value)}
          className="bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
        >
          <option value="">All Agents</option>
          {agents.map((a) => (
            <option key={a.id} value={a.id}>
              {a.display_name || a.name}
            </option>
          ))}
        </select>
        <button
          onClick={() => loadTasks()}
          className="text-sm text-gray-400 hover:text-white transition-colors ml-auto"
        >
          ↻ Refresh
        </button>
      </div>

      {/* Board */}
      {loading && tasks.length === 0 ? (
        <div className="flex items-center justify-center flex-1">
          <p className="text-gray-400">Loading tasks...</p>
        </div>
      ) : (
        <div className="flex gap-4 p-4 flex-1 overflow-x-auto">
          {COLUMNS.map((col) => {
            const colTasks = getColumnTasks(col.key)
            return (
              <div
                key={col.key}
                role="list"
                aria-label={`${col.label} column`}
                className={`flex-shrink-0 w-72 bg-gray-900/50 rounded-lg border flex flex-col transition-colors ${
                  dragOverColumn === col.key
                    ? 'border-blue-500/60 bg-blue-950/20 ring-1 ring-blue-500/30'
                    : 'border-gray-800'
                }`}
                onDragOver={(e) => { handleDragOver(e); setDragOverColumn(col.key) }}
                onDragLeave={handleDragLeave}
                onDrop={(e) => { handleDrop(e, col.key) }}
              >
                {/* Column header */}
                <div className="flex items-center justify-between px-3 py-2 border-b border-gray-800">
                  <div className="flex items-center gap-2">
                    <h3 className="text-sm font-medium">{col.label}</h3>
                    <span className="text-xs text-gray-500 bg-gray-800 px-2 py-0.5 rounded-full">
                      {colTasks.length}
                    </span>
                  </div>
                  <button
                    onClick={() => handleAddTask(col.key)}
                    className="text-gray-400 hover:text-white text-lg leading-none transition-colors"
                    title="Add task"
                  >
                    +
                  </button>
                </div>

                {/* Cards */}
                <div className="flex-1 overflow-y-auto p-2 space-y-2 min-h-[100px]">
                  {colTasks.length === 0 ? (
                    <p className="text-xs text-gray-600 text-center py-4">No tasks</p>
                  ) : (
                    colTasks.map((task) => (
                      <Card
                        key={task.id}
                        task={task}
                        agents={agents}
                        onDelete={handleDeleteTask}
                        onUpdate={handleUpdateTask}
                        onBreakdown={() => loadTasks()}
                      />
                    ))
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Task modal */}
      {showModal && (
        <TaskModal
          agents={agents}
          initialStatus={modalStatus}
          onClose={() => setShowModal(false)}
          onSave={handleSaveTask}
        />
      )}
    </div>
  )
}
