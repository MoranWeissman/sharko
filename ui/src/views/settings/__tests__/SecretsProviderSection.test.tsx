import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { SecretsProviderSection } from '@/views/settings/SecretsProviderSection'

/*
 * V125-1-10.7 — Settings → SecretsProviderSection dropdown widening.
 *
 * Story 10.7 added 'argocd' as a fourth option in the provider dropdown so
 * admins can flip from k8s-secrets / aws-sm to argocd via the UI without
 * editing a stored connection by hand. The argocd type carries no extra
 * inputs (no Region, no Prefix, no Namespace, no RoleARN) — the backend
 * uses the in-cluster argocd namespace verbatim.
 *
 * Cases covered (in order of acceptance criteria):
 *   1. Dropdown renders 4 options: None, ArgoCD, AWS Secrets Manager, Kubernetes Secrets
 *   2. Selecting "ArgoCD" sets provider_type=argocd in the form
 *   3. Selecting "ArgoCD" hides the AWS-only Region field and the
 *      shared Prefix field
 *   4. Save flow sends {provider: {type: "argocd"}} via api.updateConnection
 *   5. Regression: existing None / aws-sm / k8s-secrets cases still work
 */

const getProvidersMock = vi.fn()
const updateConnectionMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getProviders: () => getProvidersMock(),
    updateConnection: (name: string, data: unknown) => updateConnectionMock(name, data),
  },
}))

const refreshConnectionsMock = vi.fn()
const useConnectionsMock = vi.fn()

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => useConnectionsMock(),
}))

const sampleConnection = {
  name: 'default',
  description: '',
  git_provider: 'github',
  git_repo_identifier: 'owner/repo',
  git_token_masked: '***',
  argocd_server_url: 'https://argocd.example.com',
  argocd_token_masked: '***',
  argocd_namespace: 'argocd',
  is_default: true,
  is_active: true,
}

function setupHook(connections: typeof sampleConnection[] = [sampleConnection]) {
  useConnectionsMock.mockReturnValue({
    connections,
    activeConnection: connections[0]?.name ?? null,
    loading: false,
    error: null,
    setActiveConnection: vi.fn(),
    refreshConnections: refreshConnectionsMock,
  })
}

describe('SecretsProviderSection', () => {
  beforeEach(() => {
    getProvidersMock.mockReset()
    updateConnectionMock.mockReset()
    refreshConnectionsMock.mockReset()
    useConnectionsMock.mockReset()

    // Default: getProviders returns "no provider configured" (fresh install).
    getProvidersMock.mockResolvedValue({
      configured_provider: null,
      available_types: [],
    })
    updateConnectionMock.mockResolvedValue({})
  })

  it('renders 4 dropdown options including the new argocd entry', async () => {
    setupHook()
    render(<SecretsProviderSection />)

    // The dropdown is the only <select> in the section.
    const select = await screen.findByRole('combobox')
    const options = Array.from(select.querySelectorAll('option')).map(o => ({
      value: o.value,
      label: o.textContent ?? '',
    }))

    expect(options).toHaveLength(4)
    expect(options[0]).toEqual({ value: '', label: 'None' })
    // argocd appears second so it is the visually obvious default for fresh installs.
    expect(options[1].value).toBe('argocd')
    expect(options[1].label).toContain('ArgoCD')
    expect(options[2].value).toBe('aws-sm')
    expect(options[3].value).toBe('k8s-secrets')
  })

  it('selecting ArgoCD sets provider_type=argocd and hides AWS-only + Prefix fields', async () => {
    setupHook()
    const user = userEvent.setup()
    render(<SecretsProviderSection />)

    const select = await screen.findByRole('combobox')
    await user.selectOptions(select, 'argocd')

    expect((select as HTMLSelectElement).value).toBe('argocd')

    // Region (aws-sm only) must NOT be rendered.
    expect(screen.queryByLabelText(/^Region$/)).toBeNull()
    // Prefix (aws-sm + k8s-secrets only) must NOT be rendered for argocd.
    expect(screen.queryByText(/^Prefix/)).toBeNull()

    // The argocd-specific helper line should be visible.
    expect(
      screen.getByText(/reads credentials from the ArgoCD cluster Secret/i),
    ).toBeInTheDocument()
  })

  it('Save with ArgoCD selected sends {provider: {type: "argocd"}} to the API', async () => {
    setupHook()
    const user = userEvent.setup()
    render(<SecretsProviderSection />)

    const select = await screen.findByRole('combobox')
    await user.selectOptions(select, 'argocd')

    const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
    await user.click(saveBtn)

    await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))

    const [name, body] = updateConnectionMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(name).toBe('default')

    const provider = body.provider as { type: string; region?: string; prefix?: string }
    expect(provider).toBeDefined()
    expect(provider.type).toBe('argocd')
    // No extra inputs are surfaced for argocd — region / prefix must be undefined.
    expect(provider.region).toBeUndefined()
    expect(provider.prefix).toBeUndefined()
  })

  it('regression: selecting aws-sm shows the Region input and saves type=aws-sm', async () => {
    setupHook()
    const user = userEvent.setup()
    render(<SecretsProviderSection />)

    const select = await screen.findByRole('combobox')
    await user.selectOptions(select, 'aws-sm')

    // Region input now visible (aws-sm is the only branch that shows it).
    const region = await screen.findByPlaceholderText(/eu-west-1/i)
    await user.type(region, 'us-east-1')

    const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
    await user.click(saveBtn)

    await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
    const body = updateConnectionMock.mock.calls[0][1] as { provider: { type: string; region?: string } }
    expect(body.provider.type).toBe('aws-sm')
    expect(body.provider.region).toBe('us-east-1')
  })

  it('regression: selecting k8s-secrets does not show the Region input and saves type=k8s-secrets', async () => {
    setupHook()
    const user = userEvent.setup()
    render(<SecretsProviderSection />)

    const select = await screen.findByRole('combobox')
    await user.selectOptions(select, 'k8s-secrets')

    // Region input must NOT be visible for k8s-secrets.
    expect(screen.queryByPlaceholderText(/eu-west-1/i)).toBeNull()

    const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
    await user.click(saveBtn)

    await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
    const body = updateConnectionMock.mock.calls[0][1] as { provider: { type: string } }
    expect(body.provider.type).toBe('k8s-secrets')
  })

  it('regression: selecting None saves with provider undefined', async () => {
    // Seed the form with a non-empty provider so we can verify that selecting
    // None clears it on save.
    getProvidersMock.mockResolvedValue({
      configured_provider: { type: 'aws-sm', region: 'us-east-1', status: 'configured' },
      available_types: ['aws-sm', 'k8s-secrets', 'argocd'],
    })
    setupHook()
    const user = userEvent.setup()
    render(<SecretsProviderSection />)

    const select = await screen.findByRole('combobox') as HTMLSelectElement
    // Wait for the form to hydrate from getProviders — initial value is
    // pulled from the resolved provider.
    await waitFor(() => expect(select.value).toBe('aws-sm'))

    await user.selectOptions(select, '')
    expect(select.value).toBe('')

    const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
    await user.click(saveBtn)

    await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
    const body = updateConnectionMock.mock.calls[0][1] as { provider?: unknown }
    expect(body.provider).toBeUndefined()
  })
})
