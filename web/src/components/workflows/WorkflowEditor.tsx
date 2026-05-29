import { useState } from 'react'
import type { Workflow, WorkflowStep } from '../../api/client'
import { createWorkflow, updateWorkflow } from '../../api/client'
import { Icon } from '../Icon'

interface WorkflowEditorProps {
  workflow?: Workflow | null
  onDone: () => void
}

export function WorkflowEditor({ workflow, onDone }: WorkflowEditorProps) {
  const isEditing = !!workflow
  const [name, setName] = useState(workflow?.name ?? '')
  const [description, setDescription] = useState(workflow?.description ?? '')
  const [steps, setSteps] = useState<WorkflowStep[]>(
    workflow?.steps?.length ? workflow.steps : [{ name: '', prompt: '' }]
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function addStep() {
    setSteps([...steps, { name: '', prompt: '' }])
  }

  function removeStep(index: number) {
    if (steps.length <= 1) return
    setSteps(steps.filter((_, i) => i !== index))
  }

  function updateStep(index: number, field: keyof WorkflowStep, value: string) {
    const updated = [...steps]
    updated[index] = { ...updated[index], [field]: value }
    setSteps(updated)
  }

  function moveStep(index: number, direction: -1 | 1) {
    const newIndex = index + direction
    if (newIndex < 0 || newIndex >= steps.length) return
    const updated = [...steps]
    const temp = updated[index]
    updated[index] = updated[newIndex]
    updated[newIndex] = temp
    setSteps(updated)
  }

  async function handleSave() {
    if (!name.trim()) {
      setError('Name is required')
      return
    }

    const validSteps = steps.filter((s) => s.name.trim() || s.prompt.trim())
    if (validSteps.length === 0) {
      setError('At least one step is required')
      return
    }

    setSaving(true)
    setError(null)

    try {
      if (isEditing && workflow) {
        await updateWorkflow(workflow.id, {
          name: name.trim(),
          description: description.trim(),
          steps: validSteps,
        })
      } else {
        await createWorkflow({
          name: name.trim(),
          description: description.trim(),
          steps: validSteps,
        })
      }
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save workflow')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">
          {isEditing ? 'Edit Workflow' : 'New Workflow'}
        </h2>
        <button
          onClick={onDone}
          className="text-gray-400 hover:text-white text-sm"
        >
          <Icon name="close" size={14} /> Cancel
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
            placeholder="e.g. Blog Post Pipeline"
            className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
          />
        </div>

        {/* Description */}
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-1">Description</label>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="What does this workflow do?"
            rows={2}
            className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500 resize-none"
          />
        </div>

        {/* Steps */}
        <div>
          <div className="flex items-center justify-between mb-2">
            <label className="block text-sm font-medium text-gray-300">Steps</label>
            <button
              onClick={addStep}
              className="text-xs px-3 py-1 bg-gray-700 hover:bg-gray-600 rounded transition-colors"
            >
              + Add Step
            </button>
          </div>

          <div className="space-y-3">
            {steps.map((step, i) => (
              <div
                key={i}
                className="bg-gray-900 border border-gray-800 rounded-lg p-3"
              >
                <div className="flex items-center gap-2 mb-2">
                  <span className="text-xs font-mono text-gray-500 bg-gray-800 px-2 py-0.5 rounded">
                    {i + 1}
                  </span>
                  <input
                    type="text"
                    value={step.name}
                    onChange={(e) => updateStep(i, 'name', e.target.value)}
                    placeholder="Step name"
                    className="flex-1 bg-gray-800 border border-gray-700 rounded px-2 py-1 text-sm focus:outline-none focus:border-blue-500"
                  />
                  <div className="flex gap-1">
                    <button
                      onClick={() => moveStep(i, -1)}
                      disabled={i === 0}
                      className="text-gray-500 hover:text-white disabled:text-gray-700 text-xs px-1"
                      title="Move up"
                    >
                      ↑
                    </button>
                    <button
                      onClick={() => moveStep(i, 1)}
                      disabled={i === steps.length - 1}
                      className="text-gray-500 hover:text-white disabled:text-gray-700 text-xs px-1"
                      title="Move down"
                    >
                      ↓
                    </button>
                    <button
                      onClick={() => removeStep(i)}
                      disabled={steps.length <= 1}
                      className="text-red-400 hover:text-red-300 disabled:text-gray-700 text-xs px-1"
                      title="Remove step"
                    >
                      <Icon name="close" size={14} />
                    </button>
                  </div>
                </div>
                <textarea
                  value={step.prompt}
                  onChange={(e) => updateStep(i, 'prompt', e.target.value)}
                  placeholder="Prompt for this step..."
                  rows={2}
                  className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-sm focus:outline-none focus:border-blue-500 resize-none"
                />
              </div>
            ))}
          </div>
        </div>

        {/* Actions */}
        <div className="flex gap-3 pt-4">
          <button
            onClick={handleSave}
            disabled={saving}
            className="px-6 py-2 bg-blue-600 hover:bg-blue-700 disabled:bg-gray-700 disabled:text-gray-500 rounded-lg text-sm font-medium transition-colors"
          >
            {saving ? 'Saving...' : isEditing ? 'Update Workflow' : 'Create Workflow'}
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
