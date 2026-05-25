import { useEffect, useRef, useState } from 'react'
import type { SSEEvent } from '../api/client'
import { createEventSource } from '../api/client'

export function useSSE() {
  const [lastEvent, setLastEvent] = useState<SSEEvent | null>(null)
  const [connected, setConnected] = useState(false)
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    let stopped = false

    function connect() {
      if (stopped) return
      const es = createEventSource()
      esRef.current = es

      es.onopen = () => {
        setConnected(true)
      }

      es.onmessage = (e) => {
        try {
          const parsed = JSON.parse(e.data) as SSEEvent
          setLastEvent(parsed)
        } catch {
          // ignore malformed
        }
      }

      es.onerror = () => {
        setConnected(false)
        es.close()
        // auto-reconnect after 3s
        if (!stopped) {
          setTimeout(connect, 3000)
        }
      }
    }

    connect()

    return () => {
      stopped = true
      esRef.current?.close()
    }
  }, [])

  return { lastEvent, sseConnected: connected }
}
