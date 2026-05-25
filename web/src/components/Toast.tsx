import { useState } from 'react'

interface Toast {
  id: string
  message: string
  type: 'success' | 'error' | 'info'
}

let addToastFn: ((toast: Omit<Toast, 'id'>) => void) | null = null

export function showToast(message: string, type: Toast['type'] = 'info') {
  addToastFn?.({ message, type })
}

export function ToastContainer() {
  const [toasts, setToasts] = useState<Toast[]>([])

  // Register global toast function
  addToastFn = (toast) => {
    const id = crypto.randomUUID()
    setToasts((prev) => [...prev, { ...toast, id }])
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id))
    }, 4000)
  }

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
            ✕
          </button>
        </div>
      ))}
    </div>
  )
}
