import { describe, it, expect, vi } from 'vitest'
import { isDestructiveCommand, consumeJarvisStream, processCommand } from './jarvis-engine'
import type { ChatChunk } from '../../api/client'

function streamFrom(chunks: ChatChunk[]): ReadableStream<ChatChunk> {
  return new ReadableStream<ChatChunk>({
    start(controller) {
      for (const c of chunks) controller.enqueue(c)
      controller.close()
    },
  })
}

describe('isDestructiveCommand', () => {
  it('flags commands with destructive keywords', () => {
    expect(isDestructiveCommand('delete that file')).toBe(true)
    expect(isDestructiveCommand('close the browser')).toBe(true)
    expect(isDestructiveCommand('rm everything in downloads')).toBe(true)
    expect(isDestructiveCommand('shut down the server')).toBe(true)
    expect(isDestructiveCommand('uninstall the app')).toBe(true)
    expect(isDestructiveCommand('overwrite the config')).toBe(true)
  })

  it('does NOT flag benign commands', () => {
    expect(isDestructiveCommand('open the browser and go to google')).toBe(false)
    expect(isDestructiveCommand('launch the calculator')).toBe(false)
    expect(isDestructiveCommand('search for cute cats')).toBe(false)
    expect(isDestructiveCommand('what time is it')).toBe(false)
  })

  it('is case-insensitive', () => {
    expect(isDestructiveCommand('DELETE that')).toBe(true)
    expect(isDestructiveCommand('Close The App')).toBe(true)
  })

  it('returns false for empty / whitespace-only text', () => {
    expect(isDestructiveCommand('')).toBe(false)
    expect(isDestructiveCommand('   ')).toBe(false)
  })
})

describe('consumeJarvisStream', () => {
  it('accumulates content, captures conversation_id, and collects tools', async () => {
    const stream = streamFrom([
      { content: 'Opening', done: false },
      { content: ' browser…', done: false, tool_name: 'browser', tool_status: 'started' },
      { content: ' Done!', done: true, conversation_id: 'conv-7', tool_name: 'browser', tool_status: 'completed' },
    ])
    const result = await consumeJarvisStream(stream)
    expect(result.replyText).toBe('Opening browser… Done!')
    expect(result.conversationId).toBe('conv-7')
    expect(result.tools).toEqual(['browser'])
  })

  it('collects multiple distinct tools', async () => {
    const stream = streamFrom([
      { content: '', done: false, tool_name: 'terminal', tool_status: 'started' },
      { content: '', done: false, tool_name: 'browser', tool_status: 'started' },
      { content: 'ok', done: true },
    ])
    const result = await consumeJarvisStream(stream)
    expect(result.tools).toEqual(['terminal', 'browser'])
  })

  it('deduplicates repeated tool starts', async () => {
    const stream = streamFrom([
      { content: '', done: false, tool_name: 'browser', tool_status: 'started' },
      { content: '', done: false, tool_name: 'browser', tool_status: 'started' },
      { content: 'done', done: true },
    ])
    const result = await consumeJarvisStream(stream)
    expect(result.tools).toEqual(['browser'])
  })

  it('returns reply without conversation_id when none emitted', async () => {
    const stream = streamFrom([{ content: 'No id', done: true }])
    const result = await consumeJarvisStream(stream)
    expect(result.replyText).toBe('No id')
    expect(result.conversationId).toBeUndefined()
    expect(result.tools).toEqual([])
  })
})

describe('processCommand', () => {
  it('trims transcript, executes command, and returns the reply', async () => {
    const transcribe = vi.fn().mockResolvedValue('  open google.com  ')
    const execute = vi.fn().mockResolvedValue({
      replyText: 'Opened google.com',
      conversationId: 'c1',
      tools: ['browser'],
    })
    const onUserText = vi.fn()
    const onAssistantText = vi.fn()
    const onToolActivity = vi.fn()

    const result = await processCommand(
      new Blob(['x']),
      'agent-1',
      undefined,
      { transcribe, execute },
      { onUserText, onAssistantText, onToolActivity },
    )

    expect(transcribe).toHaveBeenCalledOnce()
    expect(execute).toHaveBeenCalledWith('agent-1', 'open google.com', undefined)
    expect(onUserText).toHaveBeenCalledWith('open google.com')
    expect(onAssistantText).toHaveBeenCalledWith('Opened google.com', 'c1')
    expect(onToolActivity).toHaveBeenCalledWith('browser')
    expect(result).toEqual({ replyText: 'Opened google.com', conversationId: 'c1', tools: ['browser'] })
  })

  it('forwards the current conversation id to execute', async () => {
    const transcribe = vi.fn().mockResolvedValue('click the button')
    const execute = vi.fn().mockResolvedValue({ replyText: 'clicked', tools: [] })
    await processCommand(
      new Blob(['x']),
      'agent-1',
      'conv-existing',
      { transcribe, execute },
      { onUserText: vi.fn(), onAssistantText: vi.fn(), onToolActivity: vi.fn() },
    )
    expect(execute).toHaveBeenCalledWith('agent-1', 'click the button', 'conv-existing')
  })

  // NEGATIVE: silence must NOT trigger an agent execution (#125 graceful no-input path).
  it('returns null and does NOT execute on an empty (silent) transcript', async () => {
    const transcribe = vi.fn().mockResolvedValue('   ')
    const execute = vi.fn()
    const onUserText = vi.fn()
    const onAssistantText = vi.fn()

    const result = await processCommand(
      new Blob(['x']),
      'agent-1',
      undefined,
      { transcribe, execute },
      { onUserText, onAssistantText, onToolActivity: vi.fn() },
    )

    expect(result).toBeNull()
    expect(execute).not.toHaveBeenCalled()
    expect(onUserText).not.toHaveBeenCalled()
    expect(onAssistantText).not.toHaveBeenCalled()
  })
})
