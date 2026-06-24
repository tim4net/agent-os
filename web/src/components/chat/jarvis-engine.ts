import type { ChatChunk } from '../../api/client'

/**
 * Jarvis Mode engine — voice-activated computer control (#125).
 *
 * Like `talk-engine.ts`, this factors the STT -> LLM pipeline out of the
 * component so it is unit-testable without a browser audio stack. The key
 * difference from Talk Mode is that Jarvis Mode is command-oriented: the
 * transcribed speech is treated as an actionable instruction and routed to
 * the agent with `mode="jarvis"` so the backend injects a computer-control
 * system prompt. The agent then uses its existing browser/terminal tools to
 * execute it.
 *
 * Destructive commands (delete, close, overwrite, etc.) are flagged client-side
 * so the UI can show a confirmation gate before the command is ever sent
 * (#125 acceptance criterion). This is a heuristic safety layer — the Jarvis
 * system prompt also instructs the agent to confirm destructive actions, so the
 * safety is defence-in-depth.
 */

/** A completed assistant turn, mirroring talk-engine's TurnResult. */
export interface JarvisTurnResult {
  replyText: string
  conversationId?: string
  /** Tool invocations observed during the stream (e.g. browser, terminal). */
  tools: string[]
}

/** Keywords that make a command potentially destructive and require confirmation. */
const DESTRUCTIVE_KEYWORDS = [
  'delete',
  'remove',
  'rm ',
  'close',
  'quit',
  'kill',
  'terminate',
  'overwrite',
  'format',
  'wipe',
  'purge',
  'drop',
  'shutdown',
  'shut down',
  'restart',
  'reboot',
  'uninstall',
  'reset',
  'clear all',
  'empty the trash',
]

/**
 * Heuristic check: does this command text contain a destructive keyword?
 * Case-insensitive substring match. False positives are acceptable (the user
 * can approve in the confirmation dialog); false negatives are caught by the
 * agent-side system prompt.
 */
export function isDestructiveCommand(text: string): boolean {
  const lower = text.toLowerCase()
  return DESTRUCTIVE_KEYWORDS.some((kw) => lower.includes(kw))
}

/**
 * Consume a chat SSE stream into a Jarvis turn result. Like `chatToReply` from
 * talk-engine but also collects the set of tool names that fired during the
 * stream (browser, terminal, etc.) so the UI can show "Executing: browser…".
 */
export async function consumeJarvisStream(
  stream: ReadableStream<ChatChunk>,
): Promise<JarvisTurnResult> {
  const reader = stream.getReader()
  let replyText = ''
  let conversationId: string | undefined
  const tools: string[] = []
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      // Track tool invocations (started events).
      if (value?.tool_name && value.tool_status === 'started') {
        if (!tools.includes(value.tool_name)) {
          tools.push(value.tool_name)
        }
      }
      if (value?.content) replyText += value.content
      if (value?.done && value.conversation_id) conversationId = value.conversation_id
    }
  } finally {
    reader.releaseLock()
  }
  return { replyText, conversationId, tools }
}

export interface JarvisDeps {
  /** STT: audio blob -> transcript text (reuses POST /api/voice/transcribe). */
  transcribe: (blob: Blob) => Promise<string>
  /**
   * LLM: agent + command -> assistant reply with mode="jarvis". May carry a new
   * conversation id and the list of tools that fired.
   */
  execute: (agentId: string, command: string, conversationId?: string) => Promise<JarvisTurnResult>
}

export interface JarvisHooks {
  onUserText: (text: string) => void
  onAssistantText: (text: string, conversationId?: string) => void
  onToolActivity: (toolName: string) => void
}

/**
 * Run one captured voice command end-to-end through the STT -> agent pipeline.
 *
 * Returns the assistant reply (with tool list), or `null` when the utterance
 * was silence (empty/whitespace transcript) — no chat message is sent, matching
 * the graceful no-input path established by Talk Mode (#124).
 *
 * NOTE: This function does NOT gate destructive commands. The caller checks
 * `isDestructiveCommand` on the transcript BEFORE calling this, and shows the
 * confirmation UI. Only an explicitly approved command reaches here.
 */
export async function processCommand(
  blob: Blob,
  agentId: string,
  conversationId: string | undefined,
  deps: JarvisDeps,
  hooks: JarvisHooks,
): Promise<JarvisTurnResult | null> {
  const transcript = (await deps.transcribe(blob)).trim()
  if (!transcript) {
    return null
  }
  hooks.onUserText(transcript)
  const result = await deps.execute(agentId, transcript, conversationId)
  hooks.onAssistantText(result.replyText, result.conversationId)
  for (const tool of result.tools) {
    hooks.onToolActivity(tool)
  }
  return result
}
