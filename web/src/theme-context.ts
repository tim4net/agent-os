import { createContext, useContext } from 'react'

export type ThemeName = 'default' | 'noir' | 'aurora' | 'daylight'

export interface ThemeContextValue {
  theme: ThemeName
  setTheme: (t: ThemeName) => void
}

export const ThemeContext = createContext<ThemeContextValue>({
  theme: 'default',
  setTheme: () => {},
})

export function useTheme() {
  return useContext(ThemeContext)
}

export const THEME_META: { name: ThemeName; label: string; description: string }[] = [
  {
    name: 'default',
    label: 'Gemini Dark',
    description: 'Blue/purple glass morphism — the default AgentOS look',
  },
  {
    name: 'noir',
    label: 'Noir',
    description: 'Ultra-minimal black & green — inspired by Linear and Raycast',
  },
  {
    name: 'aurora',
    label: 'Aurora',
    description: 'Deep purple gradients with sunset accents — luxury AI aesthetic',
  },
  {
    name: 'daylight',
    label: 'Daylight',
    description: 'Warm light theme with amber accents — paper and ink feel',
  },
]
