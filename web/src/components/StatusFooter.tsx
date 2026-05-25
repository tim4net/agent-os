interface StatusFooterProps {
  backendStatus: string
  sseStatus: 'connected' | 'disconnected'
}

export function StatusFooter({ backendStatus, sseStatus }: StatusFooterProps) {
  const healthColor = backendStatus === 'ok' ? 'text-green-400' : 'text-red-400'
  const sseColor = sseStatus === 'connected' ? 'text-green-400' : 'text-yellow-400'

  return (
    <footer className="flex items-center gap-4 px-4 py-1.5 border-t border-gray-800 bg-gray-900 text-xs text-gray-500 flex-shrink-0">
      <span>
        Backend: <span className={healthColor}>{backendStatus}</span>
      </span>
      <span>
        SSE: <span className={sseColor}>{sseStatus}</span>
      </span>
    </footer>
  )
}
