// Imperative toast bus — a tiny non-component module so Fast Refresh stays
// happy (component files must only export components) and so the toast handler
// is registered via subscription rather than reassigned during render.

export interface Toast {
  id: string
  message: string
  type: 'success' | 'error' | 'info'
}

type ToastHandler = (toast: Omit<Toast, 'id'>) => void

let handler: ToastHandler | null = null

/** Register the active container's handler. Returns an unsubscribe fn. */
export function registerToastHandler(fn: ToastHandler): () => void {
  handler = fn
  return () => {
    if (handler === fn) handler = null
  }
}

/** Imperative API used across the app to surface a toast. */
export function showToast(message: string, type: Toast['type'] = 'info') {
  handler?.({ message, type })
}

/** Stable, collision-resistant id for a toast. */
export function nextToastId(): string {
  return typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : Math.random().toString(36).slice(2) + Date.now().toString(36)
}
