import type { Agent } from '../api/client'

export interface PaletteItem {
  id: string
  type: 'nav' | 'agent' | 'action'
  label: string
  subtitle?: string
  icon: string
  hint?: string
  tab?: string
  agent?: Agent
  actionType?: 'new-chat' | 'mission-control'
}

const tabMeta: Record<string, string> = {
  Chat: 'chat',
  Create: 'palette',
  Build: 'view_kanban',
  Knowledge: 'psychology',
  Automate: 'bolt',
  Observe: 'radar',
  Control: 'tune',
  Settings: 'settings',
}

/**
 * Simple substring / fuzzy match scoring helper.
 * Ranks exact match highest, then prefix, then substring, then character sequence match.
 */
export function scoreMatch(text: string, query: string): number {
  if (!query) return 1
  const t = text.toLowerCase()
  const q = query.toLowerCase()
  if (t === q) return 100
  if (t.startsWith(q)) return 80
  if (t.includes(q)) return 50

  let qIdx = 0
  for (let i = 0; i < t.length; i++) {
    if (t[i] === q[qIdx]) {
      qIdx++
      if (qIdx === q.length) {
        return 10
      }
    }
  }
  return 0
}

/**
 * Fuzzy search filter and ranker.
 */
export function filterItems(
  query: string,
  tabs: readonly string[],
  agents: Agent[]
): { goTo: PaletteItem[]; agents: PaletteItem[]; actions: PaletteItem[] } {
  const q = query.trim()

  const tabItems: PaletteItem[] = tabs.map((tab) => ({
    id: `nav-${tab}`,
    type: 'nav',
    label: tab,
    icon: tabMeta[tab] || 'arrow_forward',
    hint: 'Tab',
    tab,
  }))

  const agentItems: PaletteItem[] = agents.map((agent) => ({
    id: `agent-${agent.id}`,
    type: 'agent',
    label: agent.display_name || agent.name,
    subtitle: agent.role || agent.status,
    icon: 'smart_toy',
    hint: 'Agent',
    agent,
  }))

  const actionItems: PaletteItem[] = [
    {
      id: 'action-new-chat',
      type: 'action',
      label: 'New Chat',
      subtitle: 'Start a fresh conversation',
      icon: 'add',
      hint: 'Action',
      actionType: 'new-chat',
    },
    {
      id: 'action-mission-control',
      type: 'action',
      label: 'Go to Mission Control',
      subtitle: 'Open the main dashboard',
      icon: 'dashboard',
      hint: 'Action',
      actionType: 'mission-control',
    },
  ]

  if (!q) {
    return {
      goTo: tabItems,
      agents: agentItems,
      actions: actionItems,
    }
  }

  const getBestScore = (item: PaletteItem): number => {
    const searchTexts: string[] = [item.label]
    if (item.subtitle) searchTexts.push(item.subtitle)
    if (item.type === 'agent' && item.agent) {
      searchTexts.push(item.agent.name)
      if (item.agent.role) searchTexts.push(item.agent.role)
    }

    let maxScore = 0
    for (const text of searchTexts) {
      const score = scoreMatch(text, q)
      if (score > maxScore) {
        maxScore = score
      }
    }
    return maxScore
  }

  const scoredTabs = tabItems
    .map((item) => ({ item, score: getBestScore(item) }))
    .filter((x) => x.score > 0)
    .sort((a, b) => b.score - a.score)
    .map((x) => x.item)

  const scoredAgents = agentItems
    .map((item) => ({ item, score: getBestScore(item) }))
    .filter((x) => x.score > 0)
    .sort((a, b) => b.score - a.score)
    .map((x) => x.item)

  const scoredActions = actionItems
    .map((item) => ({ item, score: getBestScore(item) }))
    .filter((x) => x.score > 0)
    .sort((a, b) => b.score - a.score)
    .map((x) => x.item)

  return {
    goTo: scoredTabs,
    agents: scoredAgents,
    actions: scoredActions,
  }
}
