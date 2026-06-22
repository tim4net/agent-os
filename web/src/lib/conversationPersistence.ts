/**
 * Per-agent conversation persistence via sessionStorage.
 *
 * Instead of a single global "last conversation" key, we maintain a map from
 * agent-id → conversation-id.  This lets the UI restore the correct conversation
 * when the user switches back to a previously-selected agent (issue #139).
 *
 * Two keys are used:
 *  - `agent-os-last-conv-map`  → JSON `{ [agentId]: convId }`
 *  - `agent-os-last-agent`     → the most recently selected agent id (for page reload restore)
 */

const CONV_MAP_KEY = 'agent-os-last-conv-map'
const LAST_AGENT_KEY = 'agent-os-last-agent'

/** Read and parse the conversation map (empty object on parse failure). */
export function getConversationMap(): Record<string, string> {
  try {
    const raw = sessionStorage.getItem(CONV_MAP_KEY)
    return raw ? (JSON.parse(raw) as Record<string, string>) : {}
  } catch {
    return {}
  }
}

/** Serialise the conversation map back to sessionStorage. */
function saveConversationMap(map: Record<string, string>): void {
  sessionStorage.setItem(CONV_MAP_KEY, JSON.stringify(map))
}

/**
 * Return the last active conversation id for `agentId`, or `null` if none.
 */
export function getLastConversationForAgent(agentId: string): string | null {
  const map = getConversationMap()
  return map[agentId] ?? null
}

/**
 * Persist (or clear) the conversation id associated with an agent.
 *
 * When `convId` is null the agent's entry is removed — e.g. when the user
 * explicitly starts a *new* chat.
 */
export function setLastConversationForAgent(agentId: string, convId: string | null): void {
  const map = getConversationMap()
  if (convId) {
    map[agentId] = convId
  } else {
    delete map[agentId]
  }
  saveConversationMap(map)
}

/** Return the id of the most recently active agent (for page-reload restore). */
export function getLastActiveAgent(): string | null {
  return sessionStorage.getItem(LAST_AGENT_KEY)
}

/** Persist the most recently active agent id. */
export function setLastActiveAgent(agentId: string): void {
  sessionStorage.setItem(LAST_AGENT_KEY, agentId)
}
