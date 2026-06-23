import type { ChatChunk } from '../../api/client'

/** A completed assistant turn. */
export interface TurnResult {
  replyText: string
  conversationId?: string
}

/**
 * Consume a chat SSE stream (as produced by `sendChat`) into a single assistant
 * reply. Accumulates `content` deltas until the stream signals `done`, and
 * captures any `conversation_id` emitted with the final chunk.
 *
 * Factored out of the component so the STT -> LLM -> TTS pipeline can be unit
 * tested without a browser audio stack (Talk Mode, #124).
 */
export async function chatToReply(stream: ReadableStream<ChatChunk>): Promise<TurnResult> {
  const reader = stream.getReader()
  let replyText = ''
  let conversationId: string | undefined
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      if (value?.content) replyText += value.content
      if (value?.done && value.conversation_id) conversationId = value.conversation_id
    }
  } finally {
    reader.releaseLock()
  }
  return { replyText, conversationId }
}

export interface ProcessTurnDeps {
  /** STT: audio blob -> transcript text (reuses POST /api/voice/transcribe). */
  transcribe: (blob: Blob) => Promise<string>
  /** LLM: agent + message -> assistant reply (may carry a new conversation id). */
  chat: (agentId: string, message: string, conversationId?: string) => Promise<TurnResult>
}

export interface ProcessTurnHooks {
  onUserText: (text: string) => void
  onAssistantText: (text: string, conversationId?: string) => void
}

/**
 * Run one captured utterance end-to-end through the STT -> LLM chain.
 *
 * Returns the assistant reply, or `null` when the utterance was silence
 * (empty/whitespace transcript) — in which case **no chat message is sent**.
 * This is the graceful no-input path (#124): the caller simply re-arms the mic
 * (or waits) without burning an LLM round-trip on empty speech.
 *
 * TTS playback and mic re-arming are intentionally left to the caller so this
 * function owns only the voice endpoints it can reason about synchronously.
 */
export async function processTurn(
  blob: Blob,
  agentId: string,
  conversationId: string | undefined,
  deps: ProcessTurnDeps,
  hooks: ProcessTurnHooks,
): Promise<TurnResult | null> {
  const transcript = (await deps.transcribe(blob)).trim()
  if (!transcript) {
    return null
  }
  hooks.onUserText(transcript)
  const result = await deps.chat(agentId, transcript, conversationId)
  hooks.onAssistantText(result.replyText, result.conversationId)
  return result
}
