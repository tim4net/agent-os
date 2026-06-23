import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TalkMode } from './TalkMode'
import type { Agent, ChatChunk } from '../../api/client'

// Mock the API client so the STT/LLM/TTS calls are controllable.
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    sendChat: vi.fn(),
    transcribeAudio: vi.fn(),
    synthesizeSpeech: vi.fn(),
  }
})

import * as Client from '../../api/client'

const sendChat = vi.mocked(Client.sendChat)
const transcribeAudio = vi.mocked(Client.transcribeAudio)
const synthesizeSpeech = vi.mocked(Client.synthesizeSpeech)

const agent: Agent = {
  id: 'agent-1',
  name: 'dev',
  display_name: 'Dev Agent',
  harness: 'litellm',
  base_url: 'http://localhost',
  status: 'online',
  last_seen: null,
}

function chatStream(content: string, conversationId?: string): ReadableStream<ChatChunk> {
  return new ReadableStream<ChatChunk>({
    start(controller) {
      controller.enqueue({ content, done: true, conversation_id: conversationId })
      controller.close()
    },
  })
}

// --- Browser audio mocks ---
class MockMediaRecorder {
  state: 'inactive' | 'recording' = 'inactive'
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  onstop: (() => void) | null = null
  static isTypeSupported() {
    return true
  }
  start() {
    this.state = 'recording'
  }
  stop() {
    this.state = 'inactive'
    this.ondataavailable?.({ data: new Blob(['chunk']) })
    this.onstop?.()
  }
}

class MockAudio {
  onended: (() => void) | null = null
  onerror: (() => void) | null = null
  // Fire onended on a macrotask so the component attaches the handler first.
  play() {
    setTimeout(() => this.onended?.(), 0)
    return Promise.resolve()
  }
  pause() {}
}

describe('TalkMode', () => {
  let getUserMedia: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.clearAllMocks()
    getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [{ stop: vi.fn() }] })
    Object.defineProperty(navigator, 'mediaDevices', {
      configurable: true,
      value: { getUserMedia },
    })
    ;(globalThis as unknown as { MediaRecorder: unknown }).MediaRecorder = MockMediaRecorder
    ;(globalThis as unknown as { Audio: unknown }).Audio = MockAudio
    ;(globalThis as unknown as { URL: { createObjectURL: () => string; revokeObjectURL: () => void } }).URL = {
      createObjectURL: () => 'blob:mock',
      revokeObjectURL: () => {},
    }
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders the idle phase and a Start button', () => {
    render(<TalkMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)
    expect(screen.getByTestId('talk-phase').textContent).toMatch(/tap the mic/i)
    expect(screen.getByRole('button', { name: /start talking/i })).toBeInTheDocument()
  })

  it('runs a full STT -> LLM -> TTS turn and renders both transcripts', async () => {
    const user = userEvent.setup()
    transcribeAudio.mockResolvedValue('hello there')
    sendChat.mockReturnValue(chatStream('Hi! How can I help?', 'conv-new'))
    synthesizeSpeech.mockResolvedValue(new Blob(['audio']))

    render(<TalkMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)

    // Start -> listening
    await user.click(screen.getByRole('button', { name: /start talking/i }))
    expect(getUserMedia).toHaveBeenCalledOnce()
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /stop recording/i })).toBeInTheDocument(),
    )

    // Stop -> processes the captured utterance
    await user.click(screen.getByRole('button', { name: /stop recording/i }))

    // User transcript + assistant reply appear; endpoints each called once.
    await waitFor(() => expect(screen.getByText('hello there')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Hi! How can I help?')).toBeInTheDocument())

    expect(transcribeAudio).toHaveBeenCalledOnce()
    expect(sendChat).toHaveBeenCalledWith('agent-1', 'hello there', undefined, undefined)
    await waitFor(() => expect(synthesizeSpeech).toHaveBeenCalledWith('Hi! How can I help?'))
  })

  // NEGATIVE: silence (empty transcript) sends nothing to the LLM and stays recoverable.
  it('does not call sendChat on a silent (empty) transcript', async () => {
    const user = userEvent.setup()
    transcribeAudio.mockResolvedValue('')

    render(<TalkMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)

    // Push-to-talk mode: disable auto-continue so a silent turn returns to idle.
    await user.click(screen.getByLabelText(/auto-continue/i))

    await user.click(screen.getByRole('button', { name: /start talking/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /stop recording/i })).toBeInTheDocument(),
    )
    await user.click(screen.getByRole('button', { name: /stop recording/i }))

    // No transcript bubbles, no LLM call, no TTS.
    await waitFor(() => {
      expect(sendChat).not.toHaveBeenCalled()
      expect(synthesizeSpeech).not.toHaveBeenCalled()
    })
    // Returns to a usable (idle) state — recoverable, not stuck.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start talking/i })).toBeInTheDocument(),
    )
  })
})
