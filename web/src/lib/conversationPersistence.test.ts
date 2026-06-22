import { describe, it, expect, beforeEach } from 'vitest'
import {
  getConversationMap,
  getLastConversationForAgent,
  setLastConversationForAgent,
  getLastActiveAgent,
  setLastActiveAgent,
} from './conversationPersistence'

describe('conversationPersistence', () => {
  beforeEach(() => {
    sessionStorage.clear()
  })

  // ── Per-agent conversation map ──

  describe('setLastConversationForAgent / getLastConversationForAgent', () => {
    it('stores and retrieves a conversation id for a single agent', () => {
      setLastConversationForAgent('agent-a', 'conv-1')
      expect(getLastConversationForAgent('agent-a')).toBe('conv-1')
    })

    it('maintains separate conversations per agent (issue #139 core scenario)', () => {
      setLastConversationForAgent('agent-a', 'conv-a')
      setLastConversationForAgent('agent-b', 'conv-b')

      // Switch back to agent-a → its conversation is still there
      expect(getLastConversationForAgent('agent-a')).toBe('conv-a')
      expect(getLastConversationForAgent('agent-b')).toBe('conv-b')
    })

    it('returns null for an agent with no saved conversation', () => {
      expect(getLastConversationForAgent('unknown')).toBeNull()
    })

    it('clears an agent entry when convId is null', () => {
      setLastConversationForAgent('agent-a', 'conv-1')
      expect(getLastConversationForAgent('agent-a')).toBe('conv-1')

      setLastConversationForAgent('agent-a', null)
      expect(getLastConversationForAgent('agent-a')).toBeNull()
    })

    it('clearing one agent does not affect others', () => {
      setLastConversationForAgent('agent-a', 'conv-a')
      setLastConversationForAgent('agent-b', 'conv-b')

      setLastConversationForAgent('agent-a', null)

      expect(getLastConversationForAgent('agent-a')).toBeNull()
      expect(getLastConversationForAgent('agent-b')).toBe('conv-b')
    })
  })

  describe('getConversationMap', () => {
    it('returns empty object when nothing stored', () => {
      expect(getConversationMap()).toEqual({})
    })

    it('returns all agent→conversation mappings', () => {
      setLastConversationForAgent('a', '1')
      setLastConversationForAgent('b', '2')
      expect(getConversationMap()).toEqual({ a: '1', b: '2' })
    })
  })

  // ── Last active agent (page-reload restore) ──

  describe('setLastActiveAgent / getLastActiveAgent', () => {
    it('stores and retrieves the last active agent', () => {
      setLastActiveAgent('agent-a')
      expect(getLastActiveAgent()).toBe('agent-a')
    })

    it('returns null when no agent has been set', () => {
      expect(getLastActiveAgent()).toBeNull()
    })

    it('overwrites with the most recently selected agent', () => {
      setLastActiveAgent('agent-a')
      setLastActiveAgent('agent-b')
      expect(getLastActiveAgent()).toBe('agent-b')
    })
  })

  // ── End-to-end scenario (issue #139 acceptance criteria) ──

  describe('agent switch scenario (issue #139)', () => {
    it('preserves per-agent conversations across full switch cycle', () => {
      // Chat with Agent A → conversation-a becomes active
      setLastActiveAgent('agent-a')
      setLastConversationForAgent('agent-a', 'conversation-a')

      // Switch to Agent B → conversation-b becomes active
      setLastActiveAgent('agent-b')
      setLastConversationForAgent('agent-b', 'conversation-b')

      // Verify both agents still have their conversations
      expect(getLastConversationForAgent('agent-a')).toBe('conversation-a')
      expect(getLastConversationForAgent('agent-b')).toBe('conversation-b')

      // Switch back to Agent A → its conversation should auto-restore
      const restored = getLastConversationForAgent('agent-a')
      expect(restored).toBe('conversation-a')
      expect(restored).not.toBeNull()
    })

    it('starting a new chat clears only the current agent conversation', () => {
      setLastConversationForAgent('agent-a', 'conv-a')
      setLastConversationForAgent('agent-b', 'conv-b')

      // User clicks "New Chat" while on agent-b
      setLastConversationForAgent('agent-b', null)

      expect(getLastConversationForAgent('agent-b')).toBeNull()
      expect(getLastConversationForAgent('agent-a')).toBe('conv-a')
    })
  })

  // ── Robustness ──

  describe('robustness', () => {
    it('handles corrupted sessionStorage gracefully', () => {
      sessionStorage.setItem('agent-os-last-conv-map', 'not-json{{{')
      expect(getConversationMap()).toEqual({})
      expect(getLastConversationForAgent('agent-a')).toBeNull()
    })
  })
})
