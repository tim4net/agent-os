import { useState, useEffect, useCallback } from 'react'
import type { Goal } from '../../api/client'
import { listGoals, createGoal, updateGoal, breakdownGoal } from '../../api/client'
import { GoalProgress } from './GoalProgress'

const statusColors: Record<string, string> = {
  active: 'bg-blue-500',
  completed: 'bg-green-500',
  paused: 'bg-yellow-500',
}

export function GoalList() {
  const [goals, setGoals] = useState<Goal[]>([])
  const [loading, setLoading] = useState(false)
  const [showAdd, setShowAdd] = useState(false)
  const [newTitle, setNewTitle] = useState('')
  const [newDesc, setNewDesc] = useState('')
  const [newTargetDate, setNewTargetDate] = useState('')
  const [breakingDown, setBreakingDown] = useState<string | null>(null)

  const loadGoals = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listGoals()
      setGoals(data)
    } catch (err) {
      console.error('Failed to load goals:', err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadGoals()
  }, [loadGoals])

  async function handleAddGoal(e: React.FormEvent) {
    e.preventDefault()
    if (!newTitle.trim()) return
    try {
      await createGoal({
        title: newTitle.trim(),
        description: newDesc.trim(),
        target_date: newTargetDate || null,
      })
      setNewTitle('')
      setNewDesc('')
      setNewTargetDate('')
      setShowAdd(false)
      await loadGoals()
    } catch (err) {
      console.error('Failed to create goal:', err)
    }
  }

  async function handleBreakdown(id: string) {
    setBreakingDown(id)
    try {
      await breakdownGoal(id)
      await loadGoals()
    } catch (err) {
      console.error('Failed to break down goal:', err)
    } finally {
      setBreakingDown(null)
    }
  }

  async function handleStatusChange(id: string, status: string) {
    try {
      await updateGoal(id, { status })
      await loadGoals()
    } catch (err) {
      console.error('Failed to update goal:', err)
    }
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">Goals</h2>
        <div className="flex gap-2">
          <button
            onClick={() => loadGoals()}
            className="text-sm text-gray-400 hover:text-white transition-colors"
          >
            ↻ Refresh
          </button>
          <button
            onClick={() => setShowAdd(!showAdd)}
            className="text-sm bg-blue-600 hover:bg-blue-700 px-3 py-1.5 rounded font-medium transition-colors"
          >
            + Add Goal
          </button>
        </div>
      </div>

      {showAdd && (
        <form onSubmit={handleAddGoal} className="bg-gray-900 border border-gray-700 rounded-lg p-4 mb-6 space-y-3">
          <input
            type="text"
            placeholder="Goal title"
            value={newTitle}
            onChange={(e) => setNewTitle(e.target.value)}
            className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
            autoFocus
          />
          <textarea
            placeholder="Description (optional)"
            value={newDesc}
            onChange={(e) => setNewDesc(e.target.value)}
            rows={2}
            className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-2 text-sm focus:outline-none focus:border-blue-500 resize-none"
          />
          <div className="flex items-center gap-3">
            <input
              type="date"
              value={newTargetDate}
              onChange={(e) => setNewTargetDate(e.target.value)}
              className="bg-gray-800 border border-gray-700 rounded px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
            />
            <div className="flex gap-2 ml-auto">
              <button
                type="button"
                onClick={() => setShowAdd(false)}
                className="text-sm text-gray-400 hover:text-white transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                className="text-sm bg-blue-600 hover:bg-blue-700 px-4 py-1.5 rounded font-medium transition-colors"
              >
                Create
              </button>
            </div>
          </div>
        </form>
      )}

      {loading && goals.length === 0 ? (
        <p className="text-gray-400">Loading goals...</p>
      ) : goals.length === 0 ? (
        <p className="text-gray-400">No goals yet. Create one to get started!</p>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {goals.map((goal) => (
            <div
              key={goal.id}
              className="bg-gray-900 border border-gray-700 rounded-lg p-4 flex flex-col gap-3"
            >
              <div className="flex items-start justify-between">
                <h3 className="font-medium text-white">{goal.title}</h3>
                <select
                  value={goal.status}
                  onChange={(e) => handleStatusChange(goal.id, e.target.value)}
                  className="text-xs bg-gray-800 border border-gray-700 rounded px-2 py-1 focus:outline-none"
                >
                  <option value="active">Active</option>
                  <option value="completed">Completed</option>
                  <option value="paused">Paused</option>
                </select>
              </div>

              {goal.description && (
                <p className="text-sm text-gray-400 line-clamp-2">{goal.description}</p>
              )}

              <div className="flex items-center gap-2">
                <span className={`${statusColors[goal.status] ?? 'bg-gray-500'} text-white text-xs px-2 py-0.5 rounded-full capitalize`}>
                  {goal.status}
                </span>
                {goal.target_date && (
                  <span className="text-xs text-gray-500">
                    Target: {new Date(goal.target_date).toLocaleDateString()}
                  </span>
                )}
              </div>

              <GoalProgress progress={goal.progress ?? 0} />

              <button
                onClick={() => handleBreakdown(goal.id)}
                disabled={breakingDown === goal.id}
                className="text-sm bg-purple-600 hover:bg-purple-700 disabled:bg-purple-800 disabled:cursor-wait px-3 py-1.5 rounded font-medium transition-colors w-full"
              >
                {breakingDown === goal.id ? (
                  <span className="flex items-center justify-center gap-2">
                    <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                    Breaking down...
                  </span>
                ) : (
                  '🤖 Break down with AI'
                )}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
