// ConnectionSection — the ArgoCD "Test ArgoCD" button shows the sentence the
// server sent, word for word (L13).
//
// The needs-https sentence comes in two forms. The catalog (and so the
// generated file) holds the form without an address. When Sharko knows the
// https address, the server sends the same sentence with that address in it,
// and the browser must show that one, not the catalog copy. Browser code may
// not retype a server sentence, so the address form is built here from the
// generated one.

import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ConnectionSection } from '@/views/settings/ConnectionSection'
import { CONNECTION_FAILURE_MESSAGES } from '@/generated/connection-sentences'

const testCredentialsMock = vi.fn()

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: [],
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/services/api', () => ({
  api: {
    testCredentials: (payload: unknown) => testCredentialsMock(payload),
    testConnection: vi.fn().mockResolvedValue({ git: { status: 'ok' }, argocd: { status: 'ok' } }),
    getRepoStatus: vi.fn().mockResolvedValue(null),
    createConnection: vi.fn(),
    updateConnection: vi.fn(),
  },
  initRepo: vi.fn(),
  getOperation: vi.fn(),
  operationHeartbeat: vi.fn(),
}))

const NEEDS_HTTPS = CONNECTION_FAILURE_MESSAGES.connectionFailureArgoCDNeedsHTTPS
const ADDRESS_FORM = NEEDS_HTTPS.replace('so it starts with https://', 'to https://argocd.example.test:8443')

function answerArgocd(message: string) {
  testCredentialsMock.mockResolvedValueOnce({
    git: { status: 'not_tested' },
    argocd: { status: 'error', message },
  })
}

describe('ConnectionSection — ArgoCD needs https (L13)', () => {
  beforeEach(() => {
    testCredentialsMock.mockReset()
  })

  it('the address form really differs from the catalog form', () => {
    // If the server ever rewords the sentence so the replace above finds
    // nothing, the two tests below would test the same text twice.
    expect(NEEDS_HTTPS).toMatch(/^ArgoCD answers only over https at this address\./)
    expect(ADDRESS_FORM).not.toBe(NEEDS_HTTPS)
    expect(ADDRESS_FORM).toContain('https://argocd.example.test:8443 and test again.')
  })

  it('shows the catalog sentence word for word', async () => {
    answerArgocd(NEEDS_HTTPS)
    render(<ConnectionSection />)
    fireEvent.click(screen.getByRole('button', { name: /Test ArgoCD/i }))
    expect(await screen.findByText(NEEDS_HTTPS)).toBeInTheDocument()
  })

  it('shows the sentence with the https address the server sent, not the catalog copy', async () => {
    answerArgocd(ADDRESS_FORM)
    render(<ConnectionSection />)
    fireEvent.click(screen.getByRole('button', { name: /Test ArgoCD/i }))
    expect(await screen.findByText(ADDRESS_FORM)).toBeInTheDocument()
    expect(screen.queryByText(NEEDS_HTTPS)).toBeNull()
  })
})

describe('ConnectionSection — ArgoCD tried to send Sharko to plain http (LO-7 R2)', () => {
  beforeEach(() => {
    testCredentialsMock.mockReset()
  })

  it('shows its own sentence word for word, not the general one', async () => {
    const redirectToHTTP = CONNECTION_FAILURE_MESSAGES.connectionFailureArgoCDRedirectToHTTP
    // The exact words are pinned on the server (Go test) and come here
    // through the generated file; browser code may not retype them.
    expect(redirectToHTTP).toMatch(/^ArgoCD tried to send Sharko from https to plain http\. /)
    answerArgocd(redirectToHTTP)
    render(<ConnectionSection />)
    fireEvent.click(screen.getByRole('button', { name: /Test ArgoCD/i }))
    expect(await screen.findByText(redirectToHTTP)).toBeInTheDocument()
    expect(
      screen.queryByText(CONNECTION_FAILURE_MESSAGES.connectionFailureArgoCDUnknown),
    ).toBeNull()
  })
})
