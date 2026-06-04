import { useState, useEffect, useRef } from 'react'
import type { Agent } from '../api/client'
import { Icon } from './Icon'
import { filterItems, type PaletteItem } from './command-palette-utils'

interface CommandPaletteProps {
  open: boolean
  onClose: () => void
  tabs: readonly string[]
  agents: Agent[]
  onNavigate: (tab: string) => void
  onSelectAgent: (agent: Agent) => void
  onNewChat: () => void
}

export default function CommandPalette({
  open,
  onClose,
  tabs,
  agents,
  onNavigate,
  onSelectAgent,
  onNewChat,
}: CommandPaletteProps) {
  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const activeItemRef = useRef<HTMLButtonElement>(null)

  // Filter items based on query
  const filtered = filterItems(query, tabs, agents)

  // Map each item in sections to include its global index in flattened list
  let runningIndex = 0
  const tabsWithIndex = filtered.goTo.map((item) => ({ ...item, index: runningIndex++ }))
  const agentsWithIndex = filtered.agents.map((item) => ({ ...item, index: runningIndex++ }))
  const actionsWithIndex = filtered.actions.map((item) => ({ ...item, index: runningIndex++ }))

  const flattenedItems = [...tabsWithIndex, ...agentsWithIndex, ...actionsWithIndex]

  // Focus input on open
  useEffect(() => {
    if (open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing command palette draft state from external open prop when the modal opens
      setQuery('')
      setSelectedIndex(0)
      const timer = setTimeout(() => {
        inputRef.current?.focus()
      }, 50)
      return () => clearTimeout(timer)
    }
  }, [open])

  // Scroll active item into view when selection changes
  useEffect(() => {
    if (activeItemRef.current && typeof activeItemRef.current.scrollIntoView === 'function') {
      activeItemRef.current.scrollIntoView({
        block: 'nearest',
      })
    }
  }, [selectedIndex])

  if (!open) return null

  const handleItemActivate = (item: PaletteItem) => {
    if (item.type === 'nav' && item.tab) {
      onNavigate(item.tab)
    } else if (item.type === 'agent' && item.agent) {
      onSelectAgent(item.agent)
    } else if (item.type === 'action' && item.actionType) {
      if (item.actionType === 'new-chat' || item.actionType === 'mission-control') {
        onNewChat()
      }
    }
    onClose()
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelectedIndex((prev) => (flattenedItems.length > 0 ? (prev + 1) % flattenedItems.length : 0))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelectedIndex((prev) => (flattenedItems.length > 0 ? (prev - 1 + flattenedItems.length) % flattenedItems.length : 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (flattenedItems[selectedIndex]) {
        handleItemActivate(flattenedItems[selectedIndex])
      }
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }

  const isEmpty = flattenedItems.length === 0

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh] p-4 bg-black/60 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="relative w-full max-w-2xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] rounded-2xl shadow-[var(--shadow-float)] overflow-hidden flex flex-col max-h-[60vh]"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Search Input Container */}
        <div className="flex items-center gap-3 px-4 py-3.5 border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30">
          <Icon name="search" className="text-[var(--text-muted)]" />
          <input
            ref={inputRef}
            type="text"
            placeholder="Search tabs, agents, actions..."
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setSelectedIndex(0)
            }}
            onKeyDown={handleKeyDown}
            className="w-full bg-transparent text-[var(--text-primary)] placeholder-[var(--text-muted)] border-none outline-none text-base font-medium focus:ring-0"
            autoFocus
          />
          <kbd className="hidden sm:inline-block text-[10px] font-mono text-[var(--text-muted)] bg-[var(--bg-elevated)]/80 px-1.5 py-0.5 rounded border border-[var(--border-subtle)]">
            ESC
          </kbd>
        </div>

        {/* Results List */}
        <div className="flex-1 overflow-y-auto p-2 space-y-4">
          {isEmpty ? (
            <div className="flex flex-col items-center justify-center p-8 text-center text-[var(--text-muted)]">
              <Icon name="search_off" size={32} className="text-[var(--text-muted)] mb-2" />
              <p className="text-sm">
                No results for <span className="text-[var(--text-primary)] font-semibold">"{query}"</span>
              </p>
            </div>
          ) : (
            <>
              {/* Go to Section */}
              {tabsWithIndex.length > 0 && (
                <div>
                  <div className="px-3 py-1.5 text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Go to
                  </div>
                  <div className="space-y-0.5">
                    {tabsWithIndex.map((item) => {
                      const isSelected = item.index === selectedIndex
                      return (
                        <button
                          key={item.id}
                          ref={isSelected ? activeItemRef : null}
                          onClick={() => handleItemActivate(item)}
                          onMouseEnter={() => setSelectedIndex(item.index)}
                          className={`w-full flex items-center justify-between px-3 py-2.5 rounded-xl transition-all duration-150 border text-left cursor-pointer ${
                            isSelected
                              ? 'border-[var(--accent-blue)]/40 bg-[var(--accent-blue)]/10 text-[var(--text-primary)] shadow-[0_0_12px_rgba(91,141,239,0.2)]'
                              : 'border-transparent bg-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                          }`}
                        >
                          <div className="flex items-center min-w-0">
                            <Icon
                              name={item.icon}
                              size={18}
                              className={`mr-3 ${isSelected ? 'text-[var(--accent-blue)]' : 'text-[var(--text-muted)]'}`}
                            />
                            <span className="text-sm font-medium leading-none">{item.label}</span>
                          </div>
                          <span
                            className={`text-[10px] font-mono uppercase tracking-wider px-2 py-0.5 rounded border ${
                              isSelected
                                ? 'bg-[var(--accent-blue)]/20 text-white border-[var(--accent-blue)]/30'
                                : 'bg-white/5 text-[var(--text-muted)] border-white/5'
                            }`}
                          >
                            {item.hint}
                          </span>
                        </button>
                      )
                    })}
                  </div>
                </div>
              )}

              {/* Agents Section */}
              {agentsWithIndex.length > 0 && (
                <div>
                  <div className="px-3 py-1.5 text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Agents
                  </div>
                  <div className="space-y-0.5">
                    {agentsWithIndex.map((item) => {
                      const isSelected = item.index === selectedIndex
                      return (
                        <button
                          key={item.id}
                          ref={isSelected ? activeItemRef : null}
                          onClick={() => handleItemActivate(item)}
                          onMouseEnter={() => setSelectedIndex(item.index)}
                          className={`w-full flex items-center justify-between px-3 py-2.5 rounded-xl transition-all duration-150 border text-left cursor-pointer ${
                            isSelected
                              ? 'border-[var(--accent-blue)]/40 bg-[var(--accent-blue)]/10 text-[var(--text-primary)] shadow-[0_0_12px_rgba(91,141,239,0.2)]'
                              : 'border-transparent bg-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                          }`}
                        >
                          <div className="flex items-center min-w-0">
                            <Icon
                              name={item.icon}
                              size={18}
                              className={`mr-3 ${isSelected ? 'text-[var(--accent-blue)]' : 'text-[var(--text-muted)]'}`}
                            />
                            <div className="min-w-0 flex flex-col">
                              <span className="text-sm font-medium leading-none">{item.label}</span>
                              {item.subtitle && (
                                <span className="text-xs text-[var(--text-muted)] mt-1 truncate">{item.subtitle}</span>
                              )}
                            </div>
                          </div>
                          <span
                            className={`text-[10px] font-mono uppercase tracking-wider px-2 py-0.5 rounded border ${
                              isSelected
                                ? 'bg-[var(--accent-blue)]/20 text-white border-[var(--accent-blue)]/30'
                                : 'bg-white/5 text-[var(--text-muted)] border-white/5'
                            }`}
                          >
                            {item.hint}
                          </span>
                        </button>
                      )
                    })}
                  </div>
                </div>
              )}

              {/* Actions Section */}
              {actionsWithIndex.length > 0 && (
                <div>
                  <div className="px-3 py-1.5 text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Actions
                  </div>
                  <div className="space-y-0.5">
                    {actionsWithIndex.map((item) => {
                      const isSelected = item.index === selectedIndex
                      return (
                        <button
                          key={item.id}
                          ref={isSelected ? activeItemRef : null}
                          onClick={() => handleItemActivate(item)}
                          onMouseEnter={() => setSelectedIndex(item.index)}
                          className={`w-full flex items-center justify-between px-3 py-2.5 rounded-xl transition-all duration-150 border text-left cursor-pointer ${
                            isSelected
                              ? 'border-[var(--accent-blue)]/40 bg-[var(--accent-blue)]/10 text-[var(--text-primary)] shadow-[0_0_12px_rgba(91,141,239,0.2)]'
                              : 'border-transparent bg-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                          }`}
                        >
                          <div className="flex items-center min-w-0">
                            <Icon
                              name={item.icon}
                              size={18}
                              className={`mr-3 ${isSelected ? 'text-[var(--accent-blue)]' : 'text-[var(--text-muted)]'}`}
                            />
                            <div className="min-w-0 flex flex-col">
                              <span className="text-sm font-medium leading-none">{item.label}</span>
                              {item.subtitle && (
                                <span className="text-xs text-[var(--text-muted)] mt-1 truncate">{item.subtitle}</span>
                              )}
                            </div>
                          </div>
                          <span
                            className={`text-[10px] font-mono uppercase tracking-wider px-2 py-0.5 rounded border ${
                              isSelected
                                ? 'bg-[var(--accent-blue)]/20 text-white border-[var(--accent-blue)]/30'
                                : 'bg-white/5 text-[var(--text-muted)] border-white/5'
                            }`}
                          >
                            {item.hint}
                          </span>
                        </button>
                      )
                    })}
                  </div>
                </div>
              )}
            </>
          )}
        </div>

        {/* Footer controls instruction */}
        <div className="px-4 py-2 border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 flex items-center justify-between text-[10px] text-[var(--text-muted)] flex-shrink-0">
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1">
              <kbd className="font-mono bg-white/5 px-1 py-0.5 rounded border border-white/5">↑↓</kbd> Navigate
            </span>
            <span className="flex items-center gap-1">
              <kbd className="font-mono bg-white/5 px-1 py-0.5 rounded border border-white/5">Enter</kbd> Select
            </span>
          </div>
          <div>
            <kbd className="font-mono bg-white/5 px-1 py-0.5 rounded border border-white/5">⌘K</kbd> to toggle
          </div>
        </div>
      </div>
    </div>
  )
}
