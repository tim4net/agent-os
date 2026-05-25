import type { Message } from '../../api/client'

interface MessageBubbleProps {
  message: Message
}

export function MessageBubble({ message }: MessageBubbleProps) {
  const isUser = message.role === 'user'

  return (
    <div className={`flex flex-col ${isUser ? 'items-end' : 'items-start'} mb-4`}>
      <span className="text-xs text-gray-500 mb-1">
        {isUser ? 'You' : 'Assistant'}
      </span>
      <div
        className={`max-w-[75%] px-4 py-2 rounded-lg text-sm whitespace-pre-wrap ${
          isUser ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-100'
        }`}
      >
        {message.content}
      </div>
      {message.created_at && (
        <span className="text-xs text-gray-600 mt-1">
          {new Date(message.created_at).toLocaleTimeString()}
        </span>
      )}
    </div>
  )
}
