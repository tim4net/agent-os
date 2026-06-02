import { useState } from 'react'
import type { ControlState } from '../../hooks/useControlState'
import { useControlMode } from '../../hooks/useControlMode'
import { Icon } from '../Icon'

type Mode = ControlState['mode']

interface ModeControlsProps {
  state: ControlState
  onModeChanged: () => void
}

const MODES: Mode[] = ['continuous', 'tick', 'stopped']

const modeIcon: Record<Mode, string> = {
  continuous: 'play_circle',
  tick: 'step',
  stopped: 'stop_circle',
}

export function ModeControls({ state, onModeChanged }: ModeControlsProps) {
  const { setMode, setCadence, loading, error } = useControlMode(onModeChanged)
  const [cadenceInput, setCadenceInput] = useState(String(state.cadence_seconds))

  const handleCadenceSubmit = () => {
    const val = Number(cadenceInput)
    if (!Number.isFinite(val) || val <= 0) return
    setCadence(val)
  }

  return (
    <div className="glass-card p-4 flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold uppercase tracking-wider text-[var(--color-text-secondary)]">
            Mode
          </h3>
          <p className="text-xs text-[var(--color-text-muted)] mt-0.5">
            Current: <span className="font-semibold text-[var(--text-primary)]">{state.mode}</span>
            {' · '}
            cadence <span className="font-semibold text-[var(--text-primary)]">{state.cadence_seconds}s</span>
          </p>
        </div>

        {/* STOP button — prominent */}
        <button
          onClick={() => setMode('stopped')}
          disabled={loading || state.mode === 'stopped'}
          className="px-4 py-2 rounded-full text-[13px] font-bold bg-[#f87171]/15 text-[#f87171] border border-[#f87171]/30 hover:bg-[#f87171]/25 transition-colors disabled:opacity-40"
        >
          <span className="flex items-center gap-1.5">
            <Icon name="stop" size={14} />
            STOP
          </span>
        </button>
      </div>

      {/* Mode switch buttons */}
      <div className="flex gap-1.5">
        {MODES.map((m) => (
          <button
            key={m}
            onClick={() => setMode(m)}
            disabled={loading}
            className={`px-4 py-1.5 rounded-full text-[13px] font-semibold capitalize transition-colors ${
              state.mode === m
                ? 'bg-[var(--bg-elevated)] text-[var(--color-text-primary)] border border-[var(--glass-border)]'
                : 'text-[var(--color-text-secondary)] hover:bg-[var(--bg-card)] border border-transparent'
            }`}
          >
            <span className="flex items-center gap-1.5">
              <Icon name={modeIcon[m]} size={14} />
              {m}
            </span>
          </button>
        ))}
      </div>

      {/* Cadence input */}
      <div className="flex items-center gap-2">
        <label className="text-xs text-[var(--color-text-muted)]">Cadence (seconds)</label>
        <input
          type="number"
          min={1}
          value={cadenceInput}
          onChange={(e) => setCadenceInput(e.target.value)}
          className="w-24 px-3 py-1.5 rounded-lg bg-[var(--bg-elevated)] border border-[var(--glass-border)] text-[var(--text-primary)] text-sm outline-none focus:border-[var(--accent-blue)] transition-colors"
        />
        <button
          onClick={handleCadenceSubmit}
          disabled={loading}
          className="px-3 py-1.5 rounded-full text-[13px] font-semibold bg-[var(--accent-blue)]/15 text-[var(--accent-blue)] border border-[var(--accent-blue)]/30 hover:bg-[var(--accent-blue)]/25 transition-colors disabled:opacity-40"
        >
          Set
        </button>
      </div>

      {error && (
        <p className="text-xs text-[#f87171]">{error}</p>
      )}
    </div>
  )
}
