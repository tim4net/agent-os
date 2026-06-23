import { describe, it, expect, vi } from 'vitest'
import { chatToReply, processTurn } from './talk-engine'
import type { ChatChunk } from '../../api/client'

function streamFrom(chunks: ChatChunk[]): ReadableStream<ChatChunk> {
  return new ReadableStream<ChatChunk>({
    start(controller) {
      for (const c of chunks) controller.enqueue(c)
      controller.close()
    },
  })
}

describe('chatToReply', () => {
  it('accumulates content deltas and captures conversation_id on done', async () => {
    const stream = streamFrom([
      { content: 'Hello', done: false },
      { content: ', world', done: false },
      { content: '!', done: true, conversation_id: 'conv-9' },
    ])
    const result = await chatToReply(stream)
    expect(result.replyText).toBe('Hello, world!')
    expect(result.conversationId).toBe('conv-9')
  })

  it('returns reply without conversation_id when none emitted', async () => {
    const stream = streamFrom([{ content: 'No id', done: true }])
    const result = await chatToReply(stream)
    expect(result.replyText).toBe('No id')
    expect(result.conversationId).toBeUndefined()
  })
})

describe('processTurn', () => {
  it('trims transcript, sends chat, and returns the reply', async () => {
    const transcribe = vi.fn().mockResolvedValue('  hello agent  ')
    const chat = vi.fn().mockResolvedValue({ replyText: 'hi there', conversationId: 'c1' })
    const onUserText = vi.fn()
    const onAssistantText = vi.fn()

    const result = await processTurn(
      new Blob(['x']),
      'agent-1',
      undefined,
      { transcribe, chat },
      { onUserText, onAssistantText },
    )

    expect(transcribe).toHaveBeenCalledOnce()
    // Trimming applied before sending.
    expect(chat).toHaveBeenCalledWith('agent-1', 'hello agent', undefined)
    expect(onUserText).toHaveBeenCalledWith('hello agent')
    expect(onAssistantText).toHaveBeenCalledWith('hi there', 'c1')
    expect(result).toEqual({ replyText: 'hi there', conversationId: 'c1' })
  })

  it('forwards the current conversation id to chat', async () => {
    const transcribe = vi.fn().mockResolvedValue('ping')
    const chat = vi.fn().mockResolvedValue({ replyText: 'pong' })
    await processTurn(
      new Blob(['x']),
      'agent-1',
      'conv-existing',
      { transcribe, chat },
      { onUserText: vi.fn(), onAssistantText: vi.fn() },
    )
    expect(chat).toHaveBeenCalledWith('agent-1', 'ping', 'conv-existing')
  })

  // NEGATIVE: silence must NOT send a chat message (#124 graceful no-input path).
  it('returns null and sends NO chat message on an empty (silent) transcript', async () => {
    const transcribe = vi.fn().mockResolvedValue('   ')
    const chat = vi.fn()
    const onUserText = vi.fn()
    const onAssistantText = vi.fn()

    const result = await processTurn(
      new Blob(['x']),
      'agent-1',
      undefined,
      { transcribe, chat },
      { onUserText, onAssistantText },
    )

    expect(result).toBeNull()
    expect(chat).not.toHaveBeenCalled()
    expect(onUserText).not.toHaveBeenCalled()
    expect(onAssistantText).not.toHaveBeenCalled()
  })
})
