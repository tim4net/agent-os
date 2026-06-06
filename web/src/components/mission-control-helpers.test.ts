import { describe, it, expect } from 'vitest'
import { tenantLabel } from './mission-control-helpers'

describe('tenantLabel', () => {
  it('maps the walled internal "dayjob" key to "Work" — never leaks raw', () => {
    expect(tenantLabel('dayjob')).toBe('Work')
    // the raw internal key must never be the display string
    expect(tenantLabel('dayjob')).not.toBe('dayjob')
  })

  it('maps "personal" to "Personal"', () => {
    expect(tenantLabel('personal')).toBe('Personal')
  })

  it('renders empty tenant as "System"', () => {
    expect(tenantLabel('')).toBe('System')
  })

  it('title-cases any other tenant as a sensible fallback', () => {
    expect(tenantLabel('staging')).toBe('Staging')
  })
})
