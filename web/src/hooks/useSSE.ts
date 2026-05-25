import { useEffect, useRef, useState } from 'react'
import type { SSEEvent } from '../api/client'
import { createEventSource } from '../api/client'

export function useSSE() {
  const [lastEvent, setLastEvent] = useState<SSEEvent | null>(null)
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    function connect() {
      const es = createEventSource()
      esRef.current = es

      es.onmessage = (e) => {
        try {
          const parsed = JSON.parse(e.data) as SSEEvent
          setLastEvent(parsed)
        } catch {
          // ignore malformed
        }
      }

      es.onerror = () => {
        es.close()
        // auto-reconnect after 3s
        setTimeout(connect, 3000)
      }
    }

    connect()

    return () => {
      esRef.current?.close()
    }
  }, [])

  return { lastEvent }
}
