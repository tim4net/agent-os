import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { JarvisMode } from './JarvisMode'
import type { Agent, ChatChunk } from '../../api/client'

// Mock the API client so STT/LLM calls are controllable.
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    sendChat: vi.fn(),
    transcribeAudio: vi.fn(),
  }
})

import * as Client from '../../api/client'

const sendChat = vi.mocked(Client.sendChat)
const transcribeAudio = vi.mocked(Client.transcribeAudio)

const agent: Agent = {
  id: 'agent-1',
  name: 'dev',
  display_name: 'Dev Agent',
  harness: 'litellm',
  base_url: 'http://localhost',
  status: 'online',
  last_seen: null,
}

function chatStream(
  content: string,
  conversationId?: string,
  tools?: { name: string; status: string }[],
): ReadableStream<ChatChunk> {
  return new ReadableStream<ChatChunk>({
    start(controller) {
      for (const t of tools ?? []) {
        controller.enqueue({ content: '', done: false, tool_name: t.name, tool_status: t.status })
      }
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

describe('JarvisMode', () => {
  let getUserMedia: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.clearAllMocks()
    getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [{ stop: vi.fn() }] })
    Object.defineProperty(navigator, 'mediaDevices', {
      configurable: true,
      value: { getUserMedia },
    })
    ;(globalThis as unknown as { MediaRecorder: unknown }).MediaRecorder = MockMediaRecorder
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders the idle phase and a Speak button', () => {
    render(<JarvisMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)
    expect(screen.getByTestId('jarvis-phase').textContent).toMatch(/speak a command/i)
    expect(screen.getByRole('button', { name: /speak a command/i })).toBeInTheDocument()
  })

  it('runs a full STT -> command execution flow and renders both transcripts', async () => {
    const user = userEvent.setup()
    transcribeAudio.mockResolvedValue('open google.com')
    sendChat.mockReturnValue(chatStream('Opened google.com in your browser.', 'conv-1', [
      { name: 'browser', status: 'started' },
      { name: 'browser', status: 'completed' },
    ]))

    render(<JarvisMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: /speak a command/i }))
    expect(getUserMedia).toHaveBeenCalledOnce()
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /stop recording/i })).toBeInTheDocument(),
    )
    await user.click(screen.getByRole('button', { name: /stop recording/i }))

    await waitFor(() => expect(screen.getByText('open google.com')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Opened google.com in your browser.')).toBeInTheDocument())

    // The command was sent with mode="jarvis".
    expect(sendChat).toHaveBeenCalledWith('agent-1', 'open google.com', undefined, undefined, 'jarvis')
  })

  it('shows tool badges from the execution', async () => {
    const user = userEvent.setup()
    transcribeAudio.mockResolvedValue('run ls')
    sendChat.mockReturnValue(chatStream('Done.', undefined, [
      { name: 'terminal', status: 'started' },
      { name: 'terminal', status: 'completed' },
    ]))

    render(<JarvisMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: /speak a command/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /stop recording/i })).toBeInTheDocument(),
    )
    await user.click(screen.getByRole('button', { name: /stop recording/i }))

    await waitFor(() => expect(screen.getByText('💻 Terminal')).toBeInTheDocument())
  })

  // NEGATIVE: silence does not send a command or show a confirmation gate.
  it('does not call sendChat on a silent (empty) transcript', async () => {
    const user = userEvent.setup()
    transcribeAudio.mockResolvedValue('')

    render(<JarvisMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: /speak a command/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /stop recording/i })).toBeInTheDocument(),
    )
    await user.click(screen.getByRole('button', { name: /stop recording/i }))

    await waitFor(() => {
      expect(sendChat).not.toHaveBeenCalled()
    })
    // Returns to a usable state.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /speak a command/i })).toBeInTheDocument(),
    )
  })

  // KEY: destructive commands show a confirmation gate before execution (#125).
  it('shows a confirmation gate for destructive commands and does NOT execute until approved', async () => {
    const user = userEvent.setup()
    transcribeAudio.mockResolvedValue('delete the temp folder')

    render(<JarvisMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: /speak a command/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /stop recording/i })).toBeInTheDocument(),
    )
    await user.click(screen.getByRole('button', { name: /stop recording/i }))

    // The confirmation gate appears with the command text.
    await waitFor(() =>
      expect(screen.getByText(/destructive command detected/i)).toBeInTheDocument(),
    )
    expect(screen.getByText(/delete the temp folder/i)).toBeInTheDocument()
    // sendChat was NOT called yet — the command is gated.
    expect(sendChat).not.toHaveBeenCalled()

    // Approve → command is now sent.
    sendChat.mockReturnValue(chatStream('Deleted temp folder contents.'))
    await user.click(screen.getByRole('button', { name: /approve and execute/i }))
    await waitFor(() =>
      expect(sendChat).toHaveBeenCalledWith('agent-1', 'delete the temp folder', undefined, undefined, 'jarvis'),
    )
    await waitFor(() => expect(screen.getByText('Deleted temp folder contents.')).toBeInTheDocument())
  })

  // KEY: rejecting a destructive command does NOT execute it.
  it('cancels a destructive command without executing it', async () => {
    const user = userEvent.setup()
    transcribeAudio.mockResolvedValue('close all applications')

    render(<JarvisMode agent={agent} conversationId={null} onConversationCreated={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: /speak a command/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /stop recording/i })).toBeInTheDocument(),
    )
    await user.click(screen.getByRole('button', { name: /stop recording/i }))

    await waitFor(() =>
      expect(screen.getByText(/destructive command detected/i)).toBeInTheDocument(),
    )
    await user.click(screen.getByRole('button', { name: /cancel command/i }))

    // Command was never sent.
    expect(sendChat).not.toHaveBeenCalled()
    // Cancellation is logged in the transcript.
    await waitFor(() =>
      expect(screen.getByText(/command cancelled/i)).toBeInTheDocument(),
    )
  })
})
