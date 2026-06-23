import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import SettingsPanel from './SettingsPanel'
import { DensityProvider } from '../DensityProvider'
import { DENSITY_STORAGE_KEY } from '../density-context'

// Stub the heavy children + data hooks so the Appearance/Density UI can be
// rendered in isolation. Only the General tab is exercised here.
vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ agents: [], loading: false }),
}))
vi.mock('./settings/AgentsSection', () => ({
  AgentsSection: () => <div data-testid="agents-section" />,
}))
vi.mock('./settings/AccessMatrix', () => ({
  AccessMatrix: () => <div data-testid="access-matrix" />,
}))
vi.mock('./settings/VaultManager', () => ({
  VaultManager: () => <div data-testid="vault-manager" />,
}))
vi.mock('./settings/AgentAccessDrawer', () => ({
  AgentAccessDrawer: () => null,
}))
vi.mock('./settings/UpdatesPanel', () => ({
  UpdatesPanel: () => <div data-testid="updates-panel" />,
}))

function renderPanel() {
  return render(
    <DensityProvider>
      <SettingsPanel />
    </DensityProvider>,
  )
}

describe('SettingsPanel — Density selector', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-density')
  })
  afterEach(() => {
    cleanup()
    localStorage.clear()
    document.documentElement.removeAttribute('data-density')
  })

  it('renders the Density radiogroup with all three options under General', () => {
    renderPanel()
    fireEvent.click(screen.getByRole('button', { name: /General/i }))

    const group = screen.getByRole('radiogroup', { name: /Density/i })
    const options = group.querySelectorAll('[role="radio"]')
    expect(options).toHaveLength(3)

    // Cozy is the default and starts active (aria-checked).
    expect(screen.getByRole('radio', { name: /Cozy/ })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: /Compact/ })).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('radio', { name: /Comfortable/ })).toHaveAttribute('aria-checked', 'false')
  })

  it('selecting Compact marks it active and persists to localStorage', () => {
    renderPanel()
    fireEvent.click(screen.getByRole('button', { name: /General/i }))

    fireEvent.click(screen.getByRole('radio', { name: /Compact/ }))
    expect(screen.getByRole('radio', { name: /Compact/ })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: /Cozy/ })).toHaveAttribute('aria-checked', 'false')
    expect(localStorage.getItem(DENSITY_STORAGE_KEY)).toBe('compact')
    expect(document.documentElement.getAttribute('data-density')).toBe('compact')
  })
})
