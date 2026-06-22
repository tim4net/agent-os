import { useState, useEffect, useCallback } from 'react'
import type { Workflow, WorkflowTemplate } from '../../api/client'
import { listWorkflows, deleteWorkflow, runWorkflow, listWorkflowTemplates, createWorkflow } from '../../api/client'
import { WorkflowEditor } from './WorkflowEditor'
import { Icon } from '../Icon'

export function WorkflowList() {
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [loading, setLoading] = useState(false)
  const [runningId, setRunningId] = useState<string | null>(null)
  const [runResult, setRunResult] = useState<{ id: string; status: string; message: string } | null>(null)
  const [editingWorkflow, setEditingWorkflow] = useState<Workflow | null>(null)
  const [creating, setCreating] = useState(false)
  const [templates, setTemplates] = useState<WorkflowTemplate[]>([])
  const [usingTemplate, setUsingTemplate] = useState<string | null>(null)

  const loadWorkflows = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listWorkflows()
      setWorkflows(data)
    } catch (err) {
      console.error('Failed to load workflows:', err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await
    loadWorkflows()
  }, [loadWorkflows])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async data fetch; setState lands after await
    listWorkflowTemplates().then(setTemplates).catch((err) => console.error('Failed to load workflow templates:', err))
  }, [])

  // useTemplate instantiates a predefined template as a runnable workflow.
  async function useTemplate(tpl: WorkflowTemplate) {
    setUsingTemplate(tpl.key)
    try {
      await createWorkflow({
        name: tpl.name,
        description: tpl.description,
        steps: tpl.steps,
      })
      await loadWorkflows()
    } catch (err) {
      console.error('Failed to instantiate template:', err)
    } finally {
      setUsingTemplate(null)
    }
  }

  async function handleDelete(id: string) {
    if (!confirm('Delete this workflow?')) return
    try {
      await deleteWorkflow(id)
      await loadWorkflows()
    } catch (err) {
      console.error('Failed to delete workflow:', err)
    }
  }

  async function handleRun(id: string, name: string) {
    setRunningId(id)
    setRunResult(null)
    try {
      const result = await runWorkflow(id)
      setRunResult({ id, status: result.status, message: `Workflow "${name}" completed: ${result.status}` })
    } catch (err) {
      setRunResult({ id, status: 'error', message: `Failed: ${err instanceof Error ? err.message : 'Unknown error'}` })
    } finally {
      setRunningId(null)
    }
  }

  function handleEditDone() {
    setEditingWorkflow(null)
    setCreating(false)
    loadWorkflows()
  }

  if (creating || editingWorkflow) {
    return (
      <div className="p-6">
        <WorkflowEditor
          workflow={editingWorkflow}
          onDone={handleEditDone}
        />
      </div>
    )
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">Workflows</h2>
        <button
          onClick={() => setCreating(true)}
          className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm font-medium transition-colors"
        >
          + New Workflow
        </button>
      </div>

      {runResult && (
        <div
          className={`mb-4 px-4 py-3 rounded-lg text-sm ${
            runResult.status === 'completed'
              ? 'bg-green-900/30 text-green-300 border border-green-800'
              : 'bg-red-900/30 text-red-300 border border-red-800'
          }`}
        >
          {runResult.message}
          <button
            onClick={() => setRunResult(null)}
            className="ml-3 text-gray-400 hover:text-white"
          >
            <Icon name="close" size={14} />
          </button>
        </div>
      )}

      {templates.length > 0 && (
        <div className="mb-6">
          <h3 className="text-sm font-semibold text-gray-400 mb-3 uppercase tracking-wide">Templates</h3>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {templates.map((tpl) => (
              <div
                key={tpl.key}
                className="bg-gray-900/60 border border-dashed border-gray-700 rounded-lg p-4"
              >
                <div className="flex items-start justify-between mb-1">
                  <h4 className="text-sm font-medium text-white">{tpl.name}</h4>
                  <span className="text-xs bg-indigo-900/40 text-indigo-300 px-2 py-0.5 rounded-full ml-2 shrink-0">
                    {tpl.category}
                  </span>
                </div>
                <p className="text-xs text-gray-500 mb-2 line-clamp-2">{tpl.description}</p>
                <p className="text-xs text-gray-600 mb-3">{tpl.steps.length} steps</p>
                <button
                  onClick={() => useTemplate(tpl)}
                  disabled={usingTemplate === tpl.key}
                  className="text-xs px-3 py-1.5 bg-indigo-700 hover:bg-indigo-600 disabled:bg-gray-700 disabled:text-gray-500 rounded transition-colors"
                >
                  {usingTemplate === tpl.key ? 'Adding...' : '+ Use Template'}
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      {loading && workflows.length === 0 ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="bg-gray-900 border border-gray-800 rounded-lg p-4 animate-pulse">
              <div className="h-5 bg-gray-800 rounded w-3/4 mb-3" />
              <div className="h-3 bg-gray-800 rounded w-1/2 mb-2" />
              <div className="h-3 bg-gray-800 rounded w-1/3" />
            </div>
          ))}
        </div>
      ) : workflows.length === 0 ? (
        <div className="text-center py-12">
          <p className="text-gray-500 mb-2">No workflows yet.</p>
          <button
            onClick={() => setCreating(true)}
            className="text-blue-400 hover:text-blue-300 text-sm"
          >
            Create your first workflow
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {workflows.map((wf) => {
            const stepCount = wf.steps?.length ?? 0
            return (
              <div
                key={wf.id}
                className="bg-gray-900 border border-gray-800 rounded-lg p-4 hover:border-gray-700 transition-colors"
              >
                <div className="flex items-start justify-between mb-2">
                  <h3 className="text-sm font-medium text-white truncate flex-1">
                    {wf.name}
                  </h3>
                  <span className="text-xs bg-gray-800 text-gray-400 px-2 py-0.5 rounded-full ml-2 shrink-0">
                    {stepCount} step{stepCount !== 1 ? 's' : ''}
                  </span>
                </div>

                {wf.description && (
                  <p className="text-xs text-gray-500 mb-3 line-clamp-2">{wf.description}</p>
                )}

                {stepCount > 0 && (
                  <div className="mb-3">
                    <div className="flex flex-wrap gap-1">
                      {wf.steps.slice(0, 3).map((step, i) => (
                        <span
                          key={i}
                          className="text-xs bg-gray-800/50 text-gray-400 px-2 py-0.5 rounded"
                        >
                          {i + 1}. {step.name}
                        </span>
                      ))}
                      {stepCount > 3 && (
                        <span className="text-xs text-gray-600">+{stepCount - 3} more</span>
                      )}
                    </div>
                  </div>
                )}

                <div className="flex gap-2 mt-auto pt-2 border-t border-gray-800">
                  <button
                    onClick={() => handleRun(wf.id, wf.name)}
                    disabled={runningId === wf.id}
                    className="text-xs px-3 py-1.5 bg-green-700 hover:bg-green-600 disabled:bg-gray-700 disabled:text-gray-500 rounded transition-colors flex items-center gap-1"
                  >
                    {runningId === wf.id ? (
                      <>
                        <svg className="animate-spin h-3 w-3" viewBox="0 0 24 24" fill="none">
                          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                        </svg>
                        Running...
                      </>
                    ) : (
                      '▶ Run'
                    )}
                  </button>
                  <button
                    onClick={() => setEditingWorkflow(wf)}
                    className="text-xs px-3 py-1.5 bg-gray-700 hover:bg-gray-600 rounded transition-colors"
                  >
                    Edit
                  </button>
                  <button
                    onClick={() => handleDelete(wf.id)}
                    className="text-xs px-3 py-1.5 bg-gray-700 hover:bg-gray-600 text-red-400 rounded transition-colors"
                  >
                    Delete
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
