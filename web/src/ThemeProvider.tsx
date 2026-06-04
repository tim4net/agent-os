import { useEffect, useState, type ReactNode } from 'react'
import { ThemeContext, type ThemeName } from './theme-context'

const STORAGE_KEY = 'agent-os-theme'

const THEME_CSS: Record<ThemeName, string> = {
  default: '',
  noir: '/themes/noir.css',
  aurora: '/themes/aurora.css',
  daylight: '/themes/daylight.css',
}

let activeLink: HTMLLinkElement | null = null

function applyThemeCSS(theme: ThemeName) {
  // Remove previous theme stylesheet
  if (activeLink) {
    activeLink.remove()
    activeLink = null
  }

  if (theme !== 'default' && THEME_CSS[theme]) {
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.href = THEME_CSS[theme]
    link.dataset.themeSheet = theme
    document.head.appendChild(link)
    activeLink = link
  }

  // Set data-theme attribute for any CSS selectors that use it
  document.documentElement.setAttribute('data-theme', theme)
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemeName>(() => {
    if (typeof window === 'undefined') return 'default'
    return (localStorage.getItem(STORAGE_KEY) as ThemeName) || 'default'
  })

  useEffect(() => {
    applyThemeCSS(theme)
    localStorage.setItem(STORAGE_KEY, theme)
  }, [theme])

  const setTheme = (t: ThemeName) => {
    setThemeState(t)
  }

  return (
    <ThemeContext.Provider value={{ theme, setTheme }}>
      {children}
    </ThemeContext.Provider>
  )
}
