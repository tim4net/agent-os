import { useState, useRef, useCallback } from 'react'

interface VoiceButtonProps {
  onTranscribed: (text: string) => void
}

export function VoiceButton({ onTranscribed }: VoiceButtonProps) {
  const [recording, setRecording] = useState(false)
  const [loading, setLoading] = useState(false)
  const mediaRecorderRef = useRef<MediaRecorder | null>(null)
  const chunksRef = useRef<Blob[]>([])

  const startRecording = useCallback(async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const mediaRecorder = new MediaRecorder(stream, {
        mimeType: MediaRecorder.isTypeSupported('audio/webm;codecs=opus')
          ? 'audio/webm;codecs=opus'
          : 'audio/webm',
      })
      chunksRef.current = []

      mediaRecorder.ondataavailable = (e) => {
        if (e.data.size > 0) {
          chunksRef.current.push(e.data)
        }
      }

      mediaRecorder.onstop = async () => {
        // Stop all tracks to release the mic
        stream.getTracks().forEach((t) => t.stop())

        const blob = new Blob(chunksRef.current, { type: 'audio/webm' })
        setLoading(true)
        try {
          const formData = new FormData()
          formData.append('file', blob, 'recording.webm')

          const res = await fetch('/api/voice/transcribe', {
            method: 'POST',
            body: formData,
          })
          if (!res.ok) {
            console.error('Transcription failed:', res.status)
            return
          }
          const data = await res.json()
          if (data.text) {
            onTranscribed(data.text)
          }
        } catch (err) {
          console.error('Voice transcription error:', err)
        } finally {
          setLoading(false)
        }
      }

      mediaRecorderRef.current = mediaRecorder
      mediaRecorder.start()
      setRecording(true)
    } catch (err) {
      console.error('Microphone access denied:', err)
    }
  }, [onTranscribed])

  const stopRecording = useCallback(() => {
    if (mediaRecorderRef.current && recording) {
      mediaRecorderRef.current.stop()
      setRecording(false)
    }
  }, [recording])

  const handleClick = useCallback(() => {
    if (recording) {
      stopRecording()
    } else {
      startRecording()
    }
  }, [recording, startRecording, stopRecording])

  return (
    <button
      onClick={handleClick}
      disabled={loading}
      className={`relative flex items-center justify-center w-9 h-9 rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${
        recording
          ? 'bg-red-600 hover:bg-red-700 text-white'
          : 'bg-gray-800 hover:bg-gray-700 text-gray-300'
      }`}
      aria-label={recording ? 'Stop recording' : 'Start voice input'}
      title={recording ? 'Stop recording' : 'Start voice input'}
    >
      {loading ? (
        <span className="inline-flex gap-0.5">
          <span className="animate-bounce text-[8px]">●</span>
          <span className="animate-bounce text-[8px] [animation-delay:0.1s]">●</span>
          <span className="animate-bounce text-[8px] [animation-delay:0.2s]">●</span>
        </span>
      ) : recording ? (
        <>
          {/* Pulsing indicator */}
          <span className="absolute inset-0 rounded-lg bg-red-500 animate-ping opacity-40" />
          {/* Mic icon (stop indicator) */}
          <svg
            className="relative w-4 h-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            strokeWidth={2}
          >
            <rect x="6" y="6" width="12" height="12" rx="1" />
          </svg>
        </>
      ) : (
        /* Microphone icon */
        <svg
          className="w-4 h-4"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3Z"
          />
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M19 10v2a7 7 0 0 1-14 0v-2"
          />
          <line x1="12" y1="19" x2="12" y2="23" />
          <line x1="8" y1="23" x2="16" y2="23" />
        </svg>
      )}
    </button>
  )
}
