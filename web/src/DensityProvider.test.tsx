import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DensityProvider } from './DensityProvider'
import { DENSITY_STORAGE_KEY, useDensity } from './density-context'
import type { DensityName } from './density-context'

/** Probe component that exposes the context value + a control button. */
function Probe() {
  const { density, setDensity } = useDensity()
  return (
    <div>
      <span data-testid="current">{density}</span>
      <button onClick={() => setDensity('compact' as DensityName)}>go-compact</button>
      <button onClick={() => setDensity('comfortable' as DensityName)}>go-comfortable</button>
      <button onClick={() => setDensity('cozy' as DensityName)}>go-cozy</button>
    </div>
  )
}

function renderProvider() {
  return render(
    <DensityProvider>
      <Probe />
    </DensityProvider>,
  )
}

describe('DensityProvider', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-density')
  })

  afterEach(() => {
    cleanup()
    localStorage.clear()
    document.documentElement.removeAttribute('data-density')
  })

  it('defaults to cozy when nothing is persisted', () => {
    renderProvider()
    expect(screen.getByTestId('current').textContent).toBe('cozy')
    expect(document.documentElement.getAttribute('data-density')).toBe('cozy')
  })

  it('restores the persisted density on mount (survives reload)', () => {
    localStorage.setItem(DENSITY_STORAGE_KEY, 'compact')
    renderProvider()
    expect(screen.getByTestId('current').textContent).toBe('compact')
    expect(document.documentElement.getAttribute('data-density')).toBe('compact')
  })

  it('falls back to cozy when persisted value is invalid', () => {
    localStorage.setItem(DENSITY_STORAGE_KEY, 'ultra-wide')
    renderProvider()
    expect(screen.getByTestId('current').textContent).toBe('cozy')
    expect(document.documentElement.getAttribute('data-density')).toBe('cozy')
  })

  it('applies and persists a new density when changed', () => {
    renderProvider()
    // change to comfortable
    fireEvent.click(screen.getByText('go-comfortable'))
    expect(screen.getByTestId('current').textContent).toBe('comfortable')
    expect(document.documentElement.getAttribute('data-density')).toBe('comfortable')
    expect(localStorage.getItem(DENSITY_STORAGE_KEY)).toBe('comfortable')

    // change to compact
    fireEvent.click(screen.getByText('go-compact'))
    expect(screen.getByTestId('current').textContent).toBe('compact')
    expect(document.documentElement.getAttribute('data-density')).toBe('compact')
    expect(localStorage.getItem(DENSITY_STORAGE_KEY)).toBe('compact')
  })

  it('writes the data-density attribute to documentElement', () => {
    renderProvider()
    fireEvent.click(screen.getByText('go-compact'))
    expect(document.documentElement.getAttribute('data-density')).toBe('compact')
    fireEvent.click(screen.getByText('go-cozy'))
    expect(document.documentElement.getAttribute('data-density')).toBe('cozy')
  })
})
