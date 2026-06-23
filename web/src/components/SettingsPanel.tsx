import { useState } from 'react'
import { useTheme, THEME_META, type ThemeName } from '../theme-context'
import { useDensity, DENSITY_META, type DensityName } from '../density-context'
import { AgentsSection } from './settings/AgentsSection'
import { AccessMatrix } from './settings/AccessMatrix'
import { VaultManager } from './settings/VaultManager'
import { AgentAccessDrawer } from './settings/AgentAccessDrawer'
import { UpdatesPanel } from './settings/UpdatesPanel'
import { useAgents } from '../hooks/useAgents'
import { type Agent } from '../api/client'

export default function SettingsPanel() {
  const { theme, setTheme } = useTheme()
  const { density, setDensity } = useDensity()
  const [activeTab, setActiveTab] = useState<'access' | 'vault' | 'agents' | 'updates' | 'general'>('access')
  const [selectedAgentForAccess, setSelectedAgentForAccess] = useState<Agent | null>(null)
  const { agents, loading: agentsLoading } = useAgents()

  return (
    <div className="fade-in max-w-5xl mx-auto p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-xl font-semibold" style={{ color: 'var(--text-primary)' }}>
          Settings
        </h2>
        
        {/* Segmented Control */}
        <div className="flex p-1 rounded-lg" style={{ backgroundColor: 'var(--bg-surface)', border: '1px solid var(--border-subtle)' }}>
          {(['access', 'vault', 'agents', 'updates', 'general'] as const).map(tab => (
            <button
              key={tab}
              onClick={() => setActiveTab(tab)}
              className={`px-4 py-1.5 text-sm font-medium rounded-md transition-colors ${
                activeTab === tab ? 'shadow-sm' : 'opacity-70 hover:opacity-100'
              }`}
              style={{
                backgroundColor: activeTab === tab ? 'var(--bg-active)' : 'transparent',
                color: activeTab === tab ? 'var(--color-text-primary)' : 'var(--color-text-muted)'
              }}
            >
              {tab.charAt(0).toUpperCase() + tab.slice(1)}
            </button>
          ))}
        </div>
      </div>

      {activeTab === 'access' && (
        <section className="fade-in mb-8">
          <div className="mb-4">
            <h3 className="text-sm font-medium uppercase tracking-wider mb-1" style={{ color: 'var(--text-muted)' }}>
              Access Matrix
            </h3>
            <p className="text-xs" style={{ color: 'var(--text-muted)' }}>
              Manage which agents have access to specific resources.
            </p>
          </div>
          <AccessMatrix onOpenAgent={setSelectedAgentForAccess} />
        </section>
      )}

      {activeTab === 'vault' && (
        <section className="fade-in mb-8">
          <div className="mb-4">
            <h3 className="text-sm font-medium uppercase tracking-wider mb-1" style={{ color: 'var(--text-muted)' }}>
              Vault
            </h3>
            <p className="text-xs" style={{ color: 'var(--text-muted)' }}>
              Manage your credentials, integrations, and MCP servers.
            </p>
          </div>
          <VaultManager />
        </section>
      )}

      {activeTab === 'agents' && (
        <div className="fade-in">
          <AgentsSection onOpenAccess={setSelectedAgentForAccess} />
        </div>
      )}

      {activeTab === 'updates' && (
        <div className="fade-in">
          <UpdatesPanel agents={agents} loading={agentsLoading} />
        </div>
      )}

      {activeTab === 'general' && (
        <div className="fade-in">
          {/* Theme Selector */}
          <section className="mb-8">
            <h3 className="text-sm font-medium uppercase tracking-wider mb-4" style={{ color: 'var(--text-muted)' }}>
              Appearance
            </h3>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 max-w-3xl">
              {THEME_META.map((t) => (
                <button
                  key={t.name}
                  onClick={() => setTheme(t.name)}
                  className={`glass-card text-left p-4 cursor-pointer transition-all ${
                    theme === t.name
                      ? 'ring-2 scale-[1.02]'
                      : 'hover:scale-[1.01]'
                  }`}
                  style={{
                    borderColor: theme === t.name ? 'var(--accent-blue)' : undefined,
                    boxShadow: theme === t.name ? 'var(--shadow-glow)' : undefined,
                  }}
                >
                  <div className="flex items-center gap-3 mb-2">
                    <ThemeSwatch themeName={t.name} />
                    <span
                      className="font-semibold text-sm"
                      style={{ color: 'var(--text-primary)' }}
                    >
                      {t.label}
                    </span>
                    {theme === t.name && (
                      <span
                        className="ml-auto text-xs px-2 py-0.5 rounded-full"
                        style={{
                          background: 'var(--accent-blue)',
                          color: 'var(--text-inverse)',
                        }}
                      >
                        Active
                      </span>
                    )}
                  </div>
                  <p className="text-xs leading-relaxed" style={{ color: 'var(--text-muted)' }}>
                    {t.description}
                  </p>
                </button>
              ))}
            </div>
          </section>

          {/* Density / Layout Selector */}
          <section className="mb-8">
            <h3 className="text-sm font-medium uppercase tracking-wider mb-4" style={{ color: 'var(--text-muted)' }}>
              Density
            </h3>
            <p className="text-xs mb-4" style={{ color: 'var(--text-muted)' }}>
              Adjust spacing and text scale across the whole interface. Saved per browser.
            </p>
            <div
              role="radiogroup"
              aria-label="Density"
              className="grid grid-cols-1 sm:grid-cols-3 gap-3 max-w-3xl"
            >
              {DENSITY_META.map((d) => (
                <button
                  key={d.name}
                  role="radio"
                  aria-checked={density === d.name}
                  onClick={() => setDensity(d.name as DensityName)}
                  className={`glass-card text-left p-4 cursor-pointer transition-all ${
                    density === d.name
                      ? 'ring-2 scale-[1.02]'
                      : 'hover:scale-[1.01]'
                  }`}
                  style={{
                    borderColor: density === d.name ? 'var(--accent-blue)' : undefined,
                    boxShadow: density === d.name ? 'var(--shadow-glow)' : undefined,
                  }}
                >
                  <div className="flex items-center gap-2 mb-1">
                    <span className="font-semibold text-sm" style={{ color: 'var(--text-primary)' }}>
                      {d.label}
                    </span>
                    {density === d.name && (
                      <span
                        className="ml-auto text-xs px-2 py-0.5 rounded-full"
                        style={{ background: 'var(--accent-blue)', color: 'var(--text-inverse)' }}
                      >
                        Active
                      </span>
                    )}
                  </div>
                  <p className="text-xs leading-relaxed" style={{ color: 'var(--text-muted)' }}>
                    {d.description}
                  </p>
                </button>
              ))}
            </div>
          </section>

          {/* About */}
          <section className="max-w-3xl">
            <h3 className="text-sm font-medium uppercase tracking-wider mb-4" style={{ color: 'var(--text-muted)' }}>
              About
            </h3>
            <div className="glass-card p-4">
              <p className="text-sm" style={{ color: 'var(--text-secondary)' }}>
                <strong style={{ color: 'var(--text-primary)' }}>AgentOS</strong> — Multi-agent operating system
              </p>
              <p className="text-xs mt-2" style={{ color: 'var(--text-muted)' }}>
                Stack: Go (Chi + sqlc + pgx) • React 19 + TypeScript 5 + Tailwind CSS v4 • PostgreSQL 17
              </p>
            </div>
          </section>
        </div>
      )}

      {selectedAgentForAccess && (
        <AgentAccessDrawer 
          agent={selectedAgentForAccess} 
          onClose={() => setSelectedAgentForAccess(null)} 
        />
      )}
    </div>
  )
}

function ThemeSwatch({ themeName }: { themeName: ThemeName }) {
  const swatches: Record<ThemeName, string[]> = {
    default: ['#0a0b10', '#5b8def', '#a78bfa', '#22d3ee'],
    noir: ['#000000', '#09090b', '#00ff88', '#fafafa'],
    aurora: ['#0f0a1a', '#8b5cf6', '#d946ef', '#f97316'],
    daylight: ['#faf9f7', '#e07a2f', '#c45d3e', '#1a1a1a'],
  }
  const colors = swatches[themeName]
  return (
    <div className="flex rounded-md overflow-hidden" style={{ border: '1px solid var(--border-subtle)' }}>
      {colors.map((c, i) => (
        <div
          key={i}
          style={{ backgroundColor: c, width: 14, height: 20 }}
        />
      ))}
    </div>
  )
}
