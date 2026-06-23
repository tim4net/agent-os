import { useEffect, useState, type ReactNode } from 'react'
import {
  DensityContext,
  DENSITY_STORAGE_KEY,
  DEFAULT_DENSITY,
  type DensityName,
} from './density-context'

const VALID_DENSITIES: DensityName[] = ['cozy', 'compact', 'comfortable']

/** Read + validate the persisted density. Falls back to the default. */
function readStoredDensity(): DensityName {
  if (typeof window === 'undefined') return DEFAULT_DENSITY
  const stored = localStorage.getItem(DENSITY_STORAGE_KEY)
  return VALID_DENSITIES.includes(stored as DensityName)
    ? (stored as DensityName)
    : DEFAULT_DENSITY
}

/** Reflect the density on <html> so index.css selectors can react to it. */
function applyDensity(density: DensityName) {
  if (typeof document === 'undefined') return
  document.documentElement.setAttribute('data-density', density)
}

export function DensityProvider({ children }: { children: ReactNode }) {
  const [density, setDensityState] = useState<DensityName>(readStoredDensity)

  useEffect(() => {
    applyDensity(density)
    localStorage.setItem(DENSITY_STORAGE_KEY, density)
  }, [density])

  const setDensity = (d: DensityName) => {
    setDensityState(d)
  }

  return (
    <DensityContext.Provider value={{ density, setDensity }}>
      {children}
    </DensityContext.Provider>
  )
}
