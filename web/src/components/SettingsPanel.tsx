import { useTheme, THEME_META, type ThemeName } from '../theme-context'

export default function SettingsPanel() {
  const { theme, setTheme } = useTheme()

  return (
    <div className="fade-in max-w-2xl mx-auto p-6">
      <h2 className="text-xl font-semibold mb-6" style={{ color: 'var(--text-primary)' }}>
        Settings
      </h2>

      {/* Theme Selector */}
      <section className="mb-8">
        <h3 className="text-sm font-medium uppercase tracking-wider mb-4" style={{ color: 'var(--text-muted)' }}>
          Theme
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
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

      {/* About */}
      <section>
        <h3 className="text-sm font-medium uppercase tracking-wider mb-4" style={{ color: 'var(--text-muted)' }}>
          About
        </h3>
        <div className="glass-card p-4">
          <p className="text-sm" style={{ color: 'var(--text-secondary)' }}>
            <strong style={{ color: 'var(--text-primary)' }}>Agent OS</strong> — Multi-agent operating system
          </p>
          <p className="text-xs mt-2" style={{ color: 'var(--text-muted)' }}>
            Stack: Go (Chi + sqlc + pgx) • React 19 + TypeScript 5 + Tailwind CSS v4 • PostgreSQL 17
          </p>
        </div>
      </section>
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
