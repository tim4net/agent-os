import { useRef } from 'react'
import { Icon } from '../Icon'

interface VerticalRailProps {
  tabs: readonly string[]
  tabMeta: Record<string, string>
  activeTab: string
  onSelect: (tab: string) => void
  onToggleSidebar: () => void
  sidebarCollapsed: boolean
  isMobile: boolean
}

/**
 * Primary navigation as a slim vertical icon+label rail pinned to the far-left
 * edge of the shell (left of the agents sidebar). Replaces the horizontal tab
 * bar, which wrapped to a second row at tablet widths (~768-1024px). The rail
 * never runs out of horizontal room, so it scales as destinations are added.
 *
 * Settings is pinned to the bottom (CSS margin-top:auto) while remaining inside
 * the single role="tablist" so ARIA ownership and arrow-key roving focus cover
 * every destination. Keyboard: arrow keys (+ Home/End) move selection AND DOM
 * focus to the newly-active tab (true roving tabindex).
 */
export function VerticalRail({
  tabs,
  tabMeta,
  activeTab,
  onSelect,
  onToggleSidebar,
  sidebarCollapsed,
  isMobile,
}: VerticalRailProps) {
  const railRef = useRef<HTMLDivElement>(null)

  // Move selection and DOM focus together so the focused element is always the
  // active tab (roving tabindex): the other tabs are tabIndex=-1, so without
  // re-focusing, focus would be stranded on a now-unfocusable button.
  function selectAndFocus(tab: string) {
    onSelect(tab)
    const idx = tabs.indexOf(tab)
    // Focus after the click/keydown handler returns so the DOM has the new
    // tabIndex applied; querying the tablist keeps this independent of layout.
    requestAnimationFrame(() => {
      const buttons = railRef.current?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
      buttons?.[idx]?.focus()
    })
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    const idx = tabs.indexOf(activeTab)
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
      e.preventDefault()
      selectAndFocus(tabs[(idx + 1) % tabs.length])
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
      e.preventDefault()
      selectAndFocus(tabs[(idx - 1 + tabs.length) % tabs.length])
    } else if (e.key === 'Home') {
      e.preventDefault()
      selectAndFocus(tabs[0])
    } else if (e.key === 'End') {
      e.preventDefault()
      selectAndFocus(tabs[tabs.length - 1])
    }
  }

  const renderItem = (tab: string) => {
    const isActive = activeTab === tab
    // Settings is pinned to the bottom of the rail while staying in the tablist.
    const isEnd = tab === 'Settings'
    return (
      <button
        key={tab}
        role="tab"
        aria-selected={isActive}
        tabIndex={isActive ? 0 : -1}
        title={tab}
        onClick={() => onSelect(tab)}
        className={`rail-item ${isActive ? 'rail-item--active' : ''} ${isEnd ? 'rail-item--end' : ''}`}
      >
        <Icon name={tabMeta[tab]} size={24} className="rail-item__icon" />
        <span className="rail-item__label">{tab}</span>
      </button>
    )
  }

  return (
    <nav className="vertical-rail" aria-label="Primary navigation">
      {/* Sidebar toggle — collapses agents sidebar (desktop) or opens it (mobile) */}
      <button
        onClick={onToggleSidebar}
        aria-label={
          isMobile
            ? 'Open agents sidebar'
            : sidebarCollapsed
              ? 'Expand sidebar'
              : 'Collapse sidebar'
        }
        title="Toggle sidebar"
        className="rail-toggle"
      >
        <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          {sidebarCollapsed || isMobile ? (
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
          ) : (
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
          )}
        </svg>
      </button>

      <div
        ref={railRef}
        role="tablist"
        aria-orientation="vertical"
        aria-label="Main navigation"
        className="rail-items"
        onKeyDown={handleKeyDown}
      >
        {tabs.map(renderItem)}
      </div>
    </nav>
  )
}
