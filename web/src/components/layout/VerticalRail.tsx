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
 * Settings is pinned to the bottom; all other destinations sit at the top under
 * the sidebar toggle. Keyboard: role="tablist" with arrow-key roving focus.
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

  // Settings pinned to bottom; everything else flows from the top.
  const topTabs = tabs.filter((t) => t !== 'Settings')
  const bottomTabs = tabs.filter((t) => t === 'Settings')

  function handleKeyDown(e: React.KeyboardEvent) {
    const idx = tabs.indexOf(activeTab)
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
      e.preventDefault()
      onSelect(tabs[(idx + 1) % tabs.length])
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
      e.preventDefault()
      onSelect(tabs[(idx - 1 + tabs.length) % tabs.length])
    } else if (e.key === 'Home') {
      e.preventDefault()
      onSelect(tabs[0])
    } else if (e.key === 'End') {
      e.preventDefault()
      onSelect(tabs[tabs.length - 1])
    }
  }

  const renderItem = (tab: string) => {
    const isActive = activeTab === tab
    return (
      <button
        key={tab}
        role="tab"
        aria-selected={isActive}
        tabIndex={isActive ? 0 : -1}
        title={tab}
        onClick={() => onSelect(tab)}
        className={`rail-item ${isActive ? 'rail-item--active' : ''}`}
      >
        <Icon name={tabMeta[tab]} size={24} className="rail-item__icon" />
        <span className="rail-item__label">{tab}</span>
      </button>
    )
  }

  return (
    <nav
      className="vertical-rail"
      aria-label="Primary navigation"
    >
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
        {topTabs.map(renderItem)}
      </div>

      <div className="rail-items rail-items--bottom">
        {bottomTabs.map(renderItem)}
      </div>
    </nav>
  )
}
