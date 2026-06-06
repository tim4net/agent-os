import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { VersionChip } from './VersionChip'
import { hasVersion, type AgentVersion } from '../../api/agentVersion'

function v(current: string, source: AgentVersion['source']): AgentVersion {
  return { current, source, checked_at: '2026-06-06T12:00:00Z' }
}

describe('hasVersion', () => {
  it('is true only for a non-empty version with a known source', () => {
    expect(hasVersion(v('1.85.1', 'openapi'))).toBe(true)
    expect(hasVersion(v('2026.6.1', 'hello-ok'))).toBe(true)
  })

  it('is false for null/empty/unknown', () => {
    expect(hasVersion(null)).toBe(false)
    expect(hasVersion(undefined)).toBe(false)
    expect(hasVersion(v('', 'unknown'))).toBe(false)
    // empty current even with a non-unknown source is not a real version
    expect(hasVersion(v('', 'health'))).toBe(false)
    // a source of 'unknown' is never a real version even if current somehow set
    expect(hasVersion(v('x', 'unknown'))).toBe(false)
  })
})

describe('VersionChip', () => {
  it('renders the version prefixed with v when known', () => {
    render(<VersionChip version={v('1.85.1', 'openapi')} />)
    expect(screen.getByText('v1.85.1')).toBeTruthy()
  })

  it('exposes provenance in the title for a known version', () => {
    render(<VersionChip version={v('2026.6.1', 'hello-ok')} />)
    const el = screen.getByText('v2026.6.1')
    expect(el.getAttribute('title')).toContain('gateway')
    expect(el.getAttribute('title')).toContain('2026.6.1')
  })

  it('renders a muted placeholder (not a version) when unknown', () => {
    render(<VersionChip version={v('', 'unknown')} />)
    expect(screen.getByText('v —')).toBeTruthy()
    // must NOT fabricate a version number
    expect(screen.queryByText(/v\d/)).toBeNull()
  })

  it('renders a muted placeholder when version is null', () => {
    render(<VersionChip version={null} />)
    expect(screen.getByText('v —')).toBeTruthy()
  })

  it('renders a loading affordance (no version text) while probing', () => {
    render(<VersionChip version={null} loading />)
    expect(screen.getByLabelText('Loading version')).toBeTruthy()
    expect(screen.queryByText('v —')).toBeNull()
    expect(screen.queryByText(/v\d/)).toBeNull()
  })
})
