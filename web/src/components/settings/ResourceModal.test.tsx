import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { ResourceModal } from './ResourceModal'
import { createResource, updateResource, type Resource } from '../../api/client'

vi.mock('../../api/client', () => ({
  createResource: vi.fn(),
  updateResource: vi.fn(),
}))

const createMock = vi.mocked(createResource)
const updateMock = vi.mocked(updateResource)

function baseResource(over: Partial<Resource> = {}): Resource {
  return {
    id: 'res-9',
    slug: 'anthropic-prod',
    kind: 'credential',
    label: 'Anthropic Prod',
    provider: 'anthropic',
    is_secret: true,
    is_set: true,
    last4: 'wxyz',
    status: 'active',
    config: {},
    ...over,
  } as Resource
}

function fillSlug(value: string) {
  fireEvent.change(screen.getByPlaceholderText(/e\.g\. openrouter-personal/i), { target: { value } })
}

describe('ResourceModal — add / rotate credential (#131)', () => {
  beforeEach(() => {
    createMock.mockReset()
    updateMock.mockReset()
  })
  afterEach(() => cleanup())

  it('renders the secret field as type=password so it is not shoulder-surfed', () => {
    render(<ResourceModal onClose={() => {}} onSaved={() => {}} />)
    const secretInput = screen.getByPlaceholderText(/paste api key or secret token/i)
    expect(secretInput).toHaveAttribute('type', 'password')
  })

  it('creates a credential with the entered secret via createResource', async () => {
    createMock.mockResolvedValue(baseResource())
    const onSaved = vi.fn()
    render(<ResourceModal onClose={() => {}} onSaved={onSaved} />)

    fillSlug('openrouter-personal')
    fireEvent.change(screen.getByPlaceholderText(/paste api key or secret token/i), {
      target: { value: 'sk-secret-create' },
    })
    fireEvent.click(screen.getByRole('button', { name: /save resource/i }))

    await waitFor(() => expect(createMock).toHaveBeenCalledTimes(1))
    expect(createMock).toHaveBeenCalledWith(
      expect.objectContaining({
        slug: 'openrouter-personal',
        kind: 'credential',
        secret: 'sk-secret-create',
      }),
    )
    expect(onSaved).toHaveBeenCalled()
  })

  it('rotates the secret on edit via updateResource; blank leaves it unchanged', async () => {
    updateMock.mockResolvedValue(baseResource())
    const onSaved = vi.fn()
    render(<ResourceModal resource={baseResource()} onClose={() => {}} onSaved={onSaved} />)

    // Edit title is "Edit Resource" and the secret placeholder changes.
    expect(screen.getByText(/edit resource/i)).toBeInTheDocument()
    const rotate = screen.getByPlaceholderText(/leave blank to keep unchanged/i)
    fireEvent.change(rotate, { target: { value: 'sk-rotated-new' } })
    fireEvent.click(screen.getByRole('button', { name: /save resource/i }))

    await waitFor(() => expect(updateMock).toHaveBeenCalledTimes(1))
    expect(updateMock).toHaveBeenCalledWith(
      'res-9',
      expect.objectContaining({ secret: 'sk-rotated-new' }),
    )
    expect(onSaved).toHaveBeenCalled()
  })

  it('leaves the stored secret unchanged when the field is left blank on edit', async () => {
    updateMock.mockResolvedValue(baseResource())
    render(<ResourceModal resource={baseResource()} onClose={() => {}} onSaved={() => {}} />)

    // Submit without touching the (blank) secret field.
    fireEvent.click(screen.getByRole('button', { name: /save resource/i }))
    await waitFor(() => expect(updateMock).toHaveBeenCalledTimes(1))
    // secret must be undefined (=> "keep current value") — never an empty string
    // that would clear the credential.
    const call = updateMock.mock.calls[0][1] as { secret?: string }
    expect(call.secret).toBeUndefined()
  })

  it('rejects an invalid slug (uppercase) without calling createResource', async () => {
    render(<ResourceModal onClose={() => {}} onSaved={() => {}} />)
    fillSlug('Bad_Slug')
    fireEvent.click(screen.getByRole('button', { name: /save resource/i }))
    // Validation runs before any network call; nothing is created.
    await waitFor(() => expect(createMock).not.toHaveBeenCalled())
  })
})
