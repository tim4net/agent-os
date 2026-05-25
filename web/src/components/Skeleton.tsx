export function SkeletonLine({ width = 'w-full' }: { width?: string }) {
  return <div className={`h-4 bg-gray-800 rounded animate-pulse ${width}`} />
}

export function SkeletonCard() {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4 animate-pulse">
      <div className="h-4 bg-gray-800 rounded w-3/4 mb-3" />
      <div className="h-3 bg-gray-800 rounded w-1/2 mb-2" />
      <div className="h-3 bg-gray-800 rounded w-1/3" />
    </div>
  )
}

export function SkeletonRow() {
  return (
    <div className="flex items-start gap-3 py-2 animate-pulse">
      <div className="w-6 h-6 bg-gray-800 rounded-full shrink-0" />
      <div className="flex-1">
        <div className="h-4 bg-gray-800 rounded w-3/4 mb-1" />
        <div className="h-3 bg-gray-800 rounded w-1/4" />
      </div>
    </div>
  )
}

export function Spinner() {
  return (
    <div className="flex items-center justify-center py-8">
      <svg className="animate-spin h-6 w-6 text-gray-400" viewBox="0 0 24 24">
        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
      </svg>
    </div>
  )
}
