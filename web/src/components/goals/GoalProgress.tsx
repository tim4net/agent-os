interface GoalProgressProps {
  completed: number
  total: number
}

export function GoalProgress({ completed, total }: GoalProgressProps) {
  const pct = total > 0 ? Math.round((completed / total) * 100) : 0

  return (
    <div className="w-full">
      <div className="flex justify-between text-xs text-gray-400 mb-1">
        <span>{completed}/{total} tasks done</span>
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
