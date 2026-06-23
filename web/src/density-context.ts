import { createContext, useContext } from 'react'

/**
 * Density / layout preference.
 *
 * Tailwind v4's spacing scale is rem-based (`--spacing: 0.25rem`), so scaling
 * the root font-size cascades through every padding, gap, and text utility in
 * the app. `DensityProvider` sets the `data-density` attribute on
 * `<html>` which `index.css` maps to a root font-size, giving a genuine,
 * app-wide density change with a single knob.
 */
export type DensityName = 'cozy' | 'compact' | 'comfortable'

export const DENSITY_STORAGE_KEY = 'agent-os-density'

export interface DensityContextValue {
  density: DensityName
  setDensity: (d: DensityName) => void
}

export const DensityContext = createContext<DensityContextValue>({
  density: 'cozy',
  setDensity: () => {},
})

export function useDensity() {
  return useContext(DensityContext)
}

export const DEFAULT_DENSITY: DensityName = 'cozy'

export const DENSITY_META: { name: DensityName; label: string; description: string }[] = [
  {
    name: 'compact',
    label: 'Compact',
    description: 'Tighter spacing — fits more on screen, ideal for dense dashboards.',
  },
  {
    name: 'cozy',
    label: 'Cozy',
    description: 'Balanced default — comfortable spacing and readable text.',
  },
  {
    name: 'comfortable',
    label: 'Comfortable',
    description: 'Roomier spacing — generous padding for relaxed reading.',
  },
]
