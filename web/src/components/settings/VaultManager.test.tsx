import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { VaultManager } from './VaultManager'
import { listResources, deleteResource, type Resource } from '../../api/client'

vi.mock('../../api/client', () => ({
  listResources: vi.fn(),
  deleteResource: vi.fn(),
}))

// ResourceModal is exercised by its own test file; keep it out of the way here.
vi.mock('./ResourceModal', () => ({
  ResourceModal: () => <div data-testid="resource-modal" />,
}))

const listMock = vi.mocked(listResources)
const deleteMock = vi.mocked(deleteResource)

function cred(over: Partial<Resource> = {}): Resource {
  return {
    id: 'res-1',
    slug: 'openrouter-personal',
    kind: 'credential',
    label: 'OpenRouter Personal',
    provider: 'openrouter',
    is_secret: true,
    is_set: true,
    last4: 'abcd',
    status: 'active',
    config: {},
    ...over,
  } as Resource
}

describe('VaultManager — resource vault UI (#131)', () => {
  beforeEach(() => {
    listMock.mockReset()
    deleteMock.mockReset()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })
  afterEach(() => cleanup())

  it('shows the empty state when the vault has no resources', async () => {
    listMock.mockResolvedValue({ resources: [] })
    render(<VaultManager />)
    expect(await screen.findByText(/your vault is empty/i)).toBeInTheDocument()
  })

  it('lists loaded resources grouped by kind', async () => {
    listMock.mockResolvedValue({
      resources: [
        cred({ id: 'c1', label: 'OpenRouter', slug: 'openrouter' }),
        {
          id: 'i1', slug: 'github-app', kind: 'integration', label: 'GitHub',
          provider: 'github', is_secret: false, is_set: true, last4: '', status: 'active', config: { base_url: 'x' },
        } as Resource,
      ],
    })
    render(<VaultManager />)
    expect(await screen.findByText('Credentials')).toBeInTheDocument()
    expect(screen.getByText('Integrations')).toBeInTheDocument()
    expect(screen.getByText('OpenRouter')).toBeInTheDocument()
  })

  // NEGATIVE security test — the core vault invariant: a stored secret value
  // must NEVER be rendered to the DOM. The list endpoint only exposes is_set +
  // last4, and the UI must surface only the masked form (••••<last4>).
  it('never renders plaintext secret material — only a masked ••••<last4> form', async () => {
    const PLAINTEXT = 'sk-liv-SECRET-7890'
    // Even if some field leaked the plaintext, the UI must not print it.
    listMock.mockResolvedValue({
      resources: [cred({ id: 'c1', last4: '7890', label: 'Leaky' })],
    })
    const { container } = render(<VaultManager />)
    await screen.findByText('Leaky')
    // Masked form is present...
    expect(screen.getByText(/••••7890/)).toBeInTheDocument()
    // ...and the plaintext is nowhere in the rendered DOM.
    expect(container.textContent).not.toContain(PLAINTEXT)
    expect(container.textContent).not.toContain('SECRET')
  })

  it('shows •••••••• for a set credential whose last4 is empty', async () => {
    listMock.mockResolvedValue({
      resources: [cred({ id: 'c1', last4: '', label: 'NoTail' })],
    })
    render(<VaultManager />)
    await screen.findByText('NoTail')
    expect(screen.getByText(/••••••••/)).toBeInTheDocument()
  })

  it('shows "not set" for a credential with no secret stored', async () => {
    listMock.mockResolvedValue({
      resources: [cred({ id: 'c1', is_set: false, label: 'Unset' })],
    })
    render(<VaultManager />)
    expect(await screen.findByText(/not set/i)).toBeInTheDocument()
  })

  it('deletes a resource after confirmation and reloads the list', async () => {
    listMock
      .mockResolvedValueOnce({ resources: [cred({ id: 'c1', label: 'Doomed' })] })
      .mockResolvedValueOnce({ resources: [] })
    deleteMock.mockResolvedValue(undefined)

    render(<VaultManager />)
    await screen.findByText('Doomed')

    fireEvent.click(screen.getByRole('button', { name: /delete resource/i }))
    await waitFor(() => expect(deleteMock).toHaveBeenCalledWith('c1'))
    // List reloaded after delete → empty state.
    expect(await screen.findByText(/your vault is empty/i)).toBeInTheDocument()
  })

  it('does not delete when the confirm dialog is cancelled', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    listMock.mockResolvedValue({ resources: [cred({ id: 'c1', label: 'Safe' })] })
    render(<VaultManager />)
    await screen.findByText('Safe')

    fireEvent.click(screen.getByRole('button', { name: /delete resource/i }))
    await waitFor(() => expect(window.confirm).toHaveBeenCalled())
    expect(deleteMock).not.toHaveBeenCalled()
  })

  it('opens the add-resource modal', async () => {
    listMock.mockResolvedValue({ resources: [] })
    render(<VaultManager />)
    await screen.findByText(/your vault is empty/i)
    fireEvent.click(screen.getByRole('button', { name: /add resource/i }))
    expect(await screen.findByTestId('resource-modal')).toBeInTheDocument()
  })
})
