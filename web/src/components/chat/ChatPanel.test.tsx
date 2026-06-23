import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import type { Agent, Message } from '../../api/client'

// Stub heavy/visual children so the test stays focused on the loading lifecycle.
vi.mock('./MessageBubble', () => ({
  MessageBubble: ({ message }: { message: { content: string } }) => (
    <div data-testid="message-bubble">{message.content}</div>
  ),
}))
vi.mock('./VoiceButton', () => ({
  VoiceButton: () => null,
}))
vi.mock('../toast-bus', () => ({
  showToast: vi.fn(),
}))

vi.mock('../../api/client', () => ({
  getAgentModels: vi.fn().mockResolvedValue([]),
  getAgentCommands: vi.fn().mockResolvedValue([]),
  getMessages: vi.fn(),
  sendChat: vi.fn(),
  exportConversation: vi.fn(),
  executeSlashCommand: vi.fn(),
}))

import { ChatPanel } from './ChatPanel'
import { getMessages, getAgentModels, getAgentCommands } from '../../api/client'

const agent: Agent = {
  id: 'agent-1',
  name: 'dev',
  display_name: 'Developer Agent',
  harness: 'dev-harness',
  base_url: 'http://localhost',
  status: 'online',
  last_seen: null,
  role: 'Software Development',
}

// jsdom does not implement Element.scrollIntoView, but ChatPanel's auto-scroll
// effect calls it when messages change. Provide a no-op so the lifecycle runs.
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function scrollIntoView() {}
}

type Resolver = {
  resolve: (m: Message[]) => void
  reject: (e: unknown) => void
}
// Each in-flight getMessages() call appends a resolver here so tests can settle
// the load (resolve/reject) on their own timeline.
let pending: Resolver[] = []

function currentLoad(): Resolver {
  const load = pending[pending.length - 1]
  if (!load) throw new Error('no in-flight getMessages call to settle')
  return load
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getAgentModels).mockResolvedValue([])
  vi.mocked(getAgentCommands).mockResolvedValue([])
  pending = []
  vi.mocked(getMessages).mockImplementation((_convId: string, _signal?: AbortSignal) => {
    return new Promise<Message[]>((resolve, reject) => {
      pending.push({ resolve, reject })
    })
  })
})

describe('ChatPanel conversation loading — #122 stale loading recovery', () => {
  it('clears the loading indicator and renders history once the fetch succeeds', async () => {
    const onConversationLoaded = vi.fn()
    render(
      <ChatPanel
        agent={agent}
        activeConversationId="conv-a"
        onConversationLoaded={onConversationLoaded}
        onConversationCreated={vi.fn()}
        onNewChat={vi.fn()}
      />,
    )

    // Loading shows while the history fetch is in flight.
    expect(await screen.findByText('Loading conversation...')).toBeInTheDocument()

    const userMsg: Message = { id: 'u1', role: 'user', content: 'hello from user', created_at: '2026-01-01T00:00:00Z' }
    const assistantMsg: Message = { id: 'a1', role: 'assistant', content: 'hello from assistant', created_at: '2026-01-01T00:00:01Z' }

    // Fetch succeeds.
    act(() => currentLoad().resolve([userMsg, assistantMsg]))

    // Spinner is gone, history is shown, parent was notified.
    await waitFor(() =>
      expect(screen.queryByText('Loading conversation...')).not.toBeInTheDocument(),
    )
    expect(screen.getByText(userMsg.content)).toBeInTheDocument()
    expect(screen.getByText(assistantMsg.content)).toBeInTheDocument()
    expect(onConversationLoaded).toHaveBeenCalledTimes(1)
  })

  // NEGATIVE: the bug was a spinner that never clears. Prove a failed load does
  // not leave "Loading conversation..." stuck — it must surface a retry banner.
  it('never gets stuck loading: clears the indicator and shows a retry banner when the fetch fails', async () => {
    render(
      <ChatPanel
        agent={agent}
        activeConversationId="conv-a"
        onConversationLoaded={vi.fn()}
        onConversationCreated={vi.fn()}
        onNewChat={vi.fn()}
      />,
    )

    expect(await screen.findByText('Loading conversation...')).toBeInTheDocument()

    act(() => currentLoad().reject(new Error('network down')))

    // Loading state resets on fetch failure.
    await waitFor(() =>
      expect(screen.queryByText('Loading conversation...')).not.toBeInTheDocument(),
    )
    // ...and a retryable error banner is surfaced instead of a hung spinner.
    expect(screen.getByText(/Failed to load conversation/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  // NEGATIVE (core #122 regression): navigating to a different conversation
  // while a previous load is in flight must NOT let the stale result overwrite
  // the freshly-selected conversation.
  it('ignores the stale result after navigating to a different conversation', async () => {
    const { rerender } = render(
      <ChatPanel
        agent={agent}
        activeConversationId="conv-a"
        onConversationLoaded={vi.fn()}
        onConversationCreated={vi.fn()}
        onNewChat={vi.fn()}
      />,
    )
    expect(await screen.findByText('Loading conversation...')).toBeInTheDocument()
    const loadA = pending[0] // conv-a fetch, still pending

    // Navigate to a different conversation before conv-a resolves.
    rerender(
      <ChatPanel
        agent={agent}
        activeConversationId="conv-b"
        onConversationLoaded={vi.fn()}
        onConversationCreated={vi.fn()}
        onNewChat={vi.fn()}
      />,
    )
    expect(pending.length).toBe(2)
    const loadB = pending[1] // conv-b fetch, now active

    // conv-a resolves late (stale) — its message must NOT appear.
    const staleMsg: Message = { id: 's1', role: 'assistant', content: 'STALE CONV A MESSAGE' }
    act(() => loadA.resolve([staleMsg]))
    await waitFor(() => expect(screen.queryByText(staleMsg.content)).not.toBeInTheDocument())

    // conv-b resolves — its message appears and the spinner clears.
    const freshMsg: Message = { id: 'f1', role: 'assistant', content: 'FRESH CONV B MESSAGE' }
    act(() => loadB.resolve([freshMsg]))
    await waitFor(() =>
      expect(screen.queryByText('Loading conversation...')).not.toBeInTheDocument(),
    )
    expect(screen.getByText(freshMsg.content)).toBeInTheDocument()
  })
})
