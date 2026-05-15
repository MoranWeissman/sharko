import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { SecretsProviderSection } from '@/views/settings/SecretsProviderSection'
import { VALID_PROVIDER_TYPES } from '@/generated/provider-types'

/*
 * V125-1-13.7 — Settings → SecretsProviderSection dropdown is now driven
 * by ui/src/generated/provider-types.ts (auto-generated from
 * internal/providers/provider.go::New). The "argocd missing from dropdown"
 * regression that V125-1-10.7 hand-fixed cannot recur: the dropdown
 * literally cannot drift from the backend factory now, and a CI check
 * ("Provider Types Up To Date") fails if the generator output is stale.
 *
 * Cases covered (in order of acceptance criteria):
 *   1. Dropdown renders one option per VALID_PROVIDER_TYPES entry plus
 *      a leading "None" — the count regression test for V125-1-13.7
 *   2. Each generated provider type appears as an <option value="...">
 *   3. Selecting "argocd" sets provider_type=argocd and hides the AWS
 *      Region + shared Prefix inputs
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

  it('renders one dropdown option per VALID_PROVIDER_TYPES entry plus None', async () => {
    setupHook()
    render(<SecretsProviderSection />)

    // The dropdown is the only <select> in the section.
    const select = await screen.findByRole('combobox')
    const options = Array.from(select.querySelectorAll('option')).map(o => ({
      value: o.value,
      label: o.textContent ?? '',
    }))

    // The dropdown is driven by the generated const — its length is the
    // generated set + the leading "None" option. This is the count
    // regression that would have CAUGHT V125-1-10.7's missing 'argocd':
    // any new arm in providers.New()'s switch that's regenerated will
    // automatically be reflected here, and any stale generated file is
    // caught by the "Provider Types Up To Date" CI check.
    expect(options).toHaveLength(VALID_PROVIDER_TYPES.length + 1)
    expect(options[0]).toEqual({ value: '', label: 'None' })

    // Every generated type must appear as an <option value="...">.
    const renderedValues = options.slice(1).map(o => o.value)
    for (const t of VALID_PROVIDER_TYPES) {
      expect(renderedValues).toContain(t)
    }

    // Backwards-compat sanity: the well-known stable types are present.
    expect(renderedValues).toContain('argocd')
    expect(renderedValues).toContain('aws-sm')
    expect(renderedValues).toContain('k8s-secrets')
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
