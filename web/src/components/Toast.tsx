import { useState, useEffect, useRef } from 'react'
import { Icon } from './Icon'
import { registerToastHandler, nextToastId, type Toast } from './toast-bus'

export function ToastContainer() {
  const [toasts, setToasts] = useState<Toast[]>([])
  // Track pending dismissal timers so we can clear them on unmount.
  const timers = useRef<Set<ReturnType<typeof setTimeout>>>(new Set())

  useEffect(() => {
    const unsubscribe = registerToastHandler((toast) => {
      const id = nextToastId()
      setToasts((prev) => [...prev, { ...toast, id }])
      const timer = setTimeout(() => {
        setToasts((prev) => prev.filter((t) => t.id !== id))
        timers.current.delete(timer)
      }, 4000)
      timers.current.add(timer)
    })
    const pending = timers.current
    return () => {
      unsubscribe()
      pending.forEach(clearTimeout)
      pending.clear()
    }
  }, [])

  function dismiss(id: string) {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }

  const colorMap: Record<string, string> = {
    success: 'bg-green-900/80 border-green-700 text-green-200',
    error: 'bg-red-900/80 border-red-700 text-red-200',
    info: 'bg-gray-800 border-gray-700 text-gray-200',
  }

  return (
    <div className="fixed bottom-4 right-4 z-[60] flex flex-col gap-2">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          className={`px-4 py-3 rounded-lg border shadow-lg text-sm flex items-center gap-3 animate-[slideIn_0.2s_ease-out] ${colorMap[toast.type]}`}
        >
          <span className="flex-1">{toast.message}</span>
          <button
            onClick={() => dismiss(toast.id)}
            className="text-gray-400 hover:text-white transition-colors shrink-0"
          >
            <Icon name="close" size={14} />
          </button>
        </div>
      ))}
    </div>
  )
}
