interface GoalProgressProps {
  progress: number // 0-100 from API
}

export function GoalProgress({ progress }: GoalProgressProps) {
  const pct = Math.round(progress)

  if (pct === 0) {
    return (
      <div className="w-full">
        <p className="text-xs text-[var(--text-muted)] italic">
          Not started yet — use AI breakdown to generate tasks
        </p>
      </div>
    )
  }

  return (
    <div className="w-full">
      <div className="flex justify-between text-xs text-gray-400 mb-1">
        <span>In progress</span>
        <span>{pct}%</span>
      </div>
      <div className="w-full bg-gray-800 rounded-full h-2">
        <div
          className="h-2 rounded-full transition-all duration-300"
          style={{
            width: `${pct}%`,
            backgroundColor: pct === 100 ? '#22c55e' : pct >= 50 ? '#3b82f6' : '#eab308',
          }}
        />
      </div>
    </div>
  )
}
