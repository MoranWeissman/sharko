import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  SecretsProviderSection,
  canonicalizeProviderType,
} from '@/views/settings/SecretsProviderSection'
import { VALID_PROVIDER_TYPES } from '@/generated/provider-types'
import { showToast } from '@/components/ToastNotification'

/*
 * V2-cleanup-55.2 — the dropdown collapses the generated alias strings
 * (ui/src/generated/provider-types.ts, one entry per string the backend
 * factory accepts) into ONE row per real backend. The drift guard moved
 * with it: CANONICAL_TYPE in the component is a total Record over the
 * generated union, so a new factory arm still breaks the TypeScript
 * build until it's mapped to a row, and the canonicalizeProviderType
 * test below proves every generated string lands on a rendered row.
 *
 * Cases covered:
 *   1. Dropdown renders exactly the 5 canonical rows plus "None"; alias
 *      strings are NOT separate rows; azure/gcp are disabled stubs
 *   2. Every generated provider string canonicalizes onto a rendered row
 *      (successor of the V125-1-13.7 count regression test)
 *   3. Selecting "argocd" sets provider_type=argocd and hides the AWS
 *      Region + shared Prefix inputs
 *   4. Save flow sends {provider: {type: "argocd"}} via api.updateConnection
 *   5. Regression: existing None / aws-sm / k8s-secrets cases still work
 *   6. A connection stored under an alias selects the canonical row and
 *      re-saves the canonical value
 *   7. Bug B pin: the saved prefix round-trips into the form even though
 *      GET /providers omits it (form hydrates from the connection)
 */

const getProvidersMock = vi.fn()
const updateConnectionMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getProviders: () => getProvidersMock(),
    updateConnection: (name: string, data: unknown) => updateConnectionMock(name, data),
  },
}))

vi.mock('@/components/ToastNotification', () => ({
  showToast: vi.fn(),
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

type TestConnection = typeof sampleConnection & {
  provider?: { type: string; region?: string; prefix?: string }
}

function setupHook(connections: TestConnection[] = [sampleConnection]) {
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
    vi.mocked(showToast).mockReset()

    // Default: getProviders returns "no provider configured" (fresh install).
    getProvidersMock.mockResolvedValue({
      configured_provider: null,
      available_types: [],
    })
    updateConnectionMock.mockResolvedValue({})
  })

  it('renders exactly 5 canonical rows plus None — aliases collapsed, azure/gcp disabled (V2-cleanup-55.2 Bug A)', async () => {
    setupHook()
    render(<SecretsProviderSection />)

    // The dropdown is the only <select> in the section.
    const select = await screen.findByRole('combobox')
    const options = Array.from(select.querySelectorAll('option'))

    // One row per real backend + the leading "None". Rendering one row
    // per accepted STRING (11 aliases) was the live Bug A.
    expect(options.map(o => o.value)).toEqual([
      '',
      'argocd',
      'aws-sm',
      'k8s-secrets',
      'azure',
      'gcp',
    ])
    expect(options[0].textContent).toBe('None')

    // Clean human labels, no alias noise.
    const byValue = Object.fromEntries(options.map(o => [o.value, o]))
    expect(byValue['aws-sm'].textContent).toBe('AWS Secrets Manager')
    expect(byValue['k8s-secrets'].textContent).toBe('Kubernetes Secrets')

    // Alias strings must NOT be their own rows.
    for (const alias of [
      'aws-secrets-manager',
      'kubernetes',
      'azure-kv',
      'azure-key-vault',
      'gcp-sm',
      'google-secret-manager',
    ]) {
      expect(byValue[alias]).toBeUndefined()
    }

    // Azure / GCP are server-side stubs (constructors unconditionally
    // return "not yet implemented"): shown, but disabled and labelled.
    expect(byValue['azure'].disabled).toBe(true)
    expect(byValue['azure'].textContent).toMatch(/not yet supported/i)
    expect(byValue['gcp'].disabled).toBe(true)
    expect(byValue['gcp'].textContent).toMatch(/not yet supported/i)

    // Functional backends stay enabled.
    expect(byValue['argocd'].disabled).toBe(false)
    expect(byValue['aws-sm'].disabled).toBe(false)
    expect(byValue['k8s-secrets'].disabled).toBe(false)
  })

  it('every generated provider string canonicalizes onto a rendered row (drift guard)', async () => {
    setupHook()
    render(<SecretsProviderSection />)

    const select = await screen.findByRole('combobox')
    const renderedValues = Array.from(select.querySelectorAll('option')).map(o => o.value)

    // Successor of the V125-1-13.7 count regression: a new arm in the
    // backend factory regenerates VALID_PROVIDER_TYPES; CANONICAL_TYPE
    // (a total Record over that union) then fails to compile until the
    // new string is mapped, and this test proves the mapping lands on a
    // row that actually renders.
    for (const t of VALID_PROVIDER_TYPES) {
      const canonical = canonicalizeProviderType(t)
      expect(canonical, `generated type ${t} must map to a canonical row`).not.toBe('')
      expect(renderedValues).toContain(canonical)
    }
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

  // V2-cleanup-55.2 Bug A — alias collapse must not orphan existing data:
  // a connection saved under an alias string selects the canonical row,
  // and re-saving writes the canonical value.
  it('a connection stored under an alias selects the canonical row and re-saves the canonical value', async () => {
    setupHook([{
      ...sampleConnection,
      provider: { type: 'kubernetes', prefix: 'team-' },
    }])
    const user = userEvent.setup()
    render(<SecretsProviderSection />)

    const select = await screen.findByRole('combobox') as HTMLSelectElement
    // Stored alias "kubernetes" lands on the canonical k8s-secrets row.
    await waitFor(() => expect(select.value).toBe('k8s-secrets'))
    // Its stored prefix hydrates alongside.
    expect(screen.getByPlaceholderText(/prepended to cluster name/i)).toHaveValue('team-')

    await user.click(screen.getByRole('button', { name: /Save Provider/i }))
    await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
    const body = updateConnectionMock.mock.calls[0][1] as { provider: { type: string; prefix?: string } }
    // Canonical value is what gets saved — the alias is retired on rewrite.
    expect(body.provider.type).toBe('k8s-secrets')
    expect(body.provider.prefix).toBe('team-')
  })

  // L7 (V2-cleanup-60.5) — an unrecognized stored provider type must never
  // silently hydrate as "None": that made a subsequent Save (even one the
  // user thought was unrelated) persist `provider: undefined`, wiping the
  // backend's config. The guard: show it as a disabled "keep as-is" row and
  // pass the raw stored type straight through on Save unless the user
  // actively picks something else.
  describe('Unknown stored provider type guard (L7)', () => {
    it('shows a disabled "keep as-is" row instead of silently defaulting to None', async () => {
      setupHook([{
        ...sampleConnection,
        provider: { type: 'some-future-backend', region: 'eu-west-1' },
      }])
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox') as HTMLSelectElement
      await waitFor(() => expect(select.value).toBe('__unknown__'))

      const unknownOption = Array.from(select.querySelectorAll('option'))
        .find((o) => o.value === '__unknown__')
      expect(unknownOption).toBeDefined()
      expect(unknownOption?.disabled).toBe(true)
      expect(unknownOption?.textContent).toMatch(/some-future-backend/)
      expect(unknownOption?.textContent).toMatch(/keep as-is/i)

      // Warning copy is visible, not the generic "None" helper text.
      expect(screen.getByText(/isn't one Sharko's UI recognizes/i)).toBeInTheDocument()
    })

    it('Save with the unknown row still selected passes the raw stored type through unchanged (no wipe)', async () => {
      setupHook([{
        ...sampleConnection,
        provider: { type: 'some-future-backend', region: 'eu-west-1', prefix: 'x-' },
      }])
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox') as HTMLSelectElement
      await waitFor(() => expect(select.value).toBe('__unknown__'))

      await user.click(screen.getByRole('button', { name: /Save Provider/i }))
      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))

      const body = updateConnectionMock.mock.calls[0][1] as { provider?: { type: string; region?: string; prefix?: string } }
      // The exact original type is written back — NOT the sentinel, and
      // NOT undefined (which would have wiped the stored config).
      expect(body.provider).toBeDefined()
      expect(body.provider?.type).toBe('some-future-backend')
      expect(body.provider?.region).toBe('eu-west-1')
      expect(body.provider?.prefix).toBe('x-')
    })

    it('actively picking None after an unknown type clears the sentinel and saves provider undefined', async () => {
      setupHook([{
        ...sampleConnection,
        provider: { type: 'some-future-backend' },
      }])
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox') as HTMLSelectElement
      await waitFor(() => expect(select.value).toBe('__unknown__'))

      // The user actively picks None — a deliberate choice, not a silent default.
      await user.selectOptions(select, '')
      expect(select.value).toBe('')

      await user.click(screen.getByRole('button', { name: /Save Provider/i }))
      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      const body = updateConnectionMock.mock.calls[0][1] as { provider?: unknown }
      expect(body.provider).toBeUndefined()
    })

    it('actively picking a real backend after an unknown type saves the new canonical value', async () => {
      setupHook([{
        ...sampleConnection,
        provider: { type: 'some-future-backend' },
      }])
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox') as HTMLSelectElement
      await waitFor(() => expect(select.value).toBe('__unknown__'))

      await user.selectOptions(select, 'argocd')
      expect(select.value).toBe('argocd')

      await user.click(screen.getByRole('button', { name: /Save Provider/i }))
      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      const body = updateConnectionMock.mock.calls[0][1] as { provider?: { type: string } }
      expect(body.provider?.type).toBe('argocd')
    })
  })

  // V2-cleanup-55.2 Bug B — the maintainer saved a prefix, came back, and
  // the field was empty although the value WAS stored. Root cause: GET
  // /api/v1/providers builds configured_provider without the prefix field
  // (internal/api/system.go handleGetProviders — providerDisplay()
  // computes it but it never enters the JSON), and the form used to
  // hydrate from that payload. The form now hydrates from the
  // connection's own stored provider block, which round-trips prefix.
  describe('Prefix round-trip (V2-cleanup-55.2 Bug B)', () => {
    it('stored prefix hydrates the field even though GET /providers omits it', async () => {
      // Mirror the LIVE server payload: no prefix key at all.
      getProvidersMock.mockResolvedValue({
        configured_provider: { type: 'aws-sm', region: 'eu-west-1', status: 'connected' },
        available_types: ['aws-sm', 'k8s-secrets'],
      })
      setupHook([{
        ...sampleConnection,
        provider: { type: 'aws-sm', region: 'eu-west-1', prefix: 'clusters/' },
      }])
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox') as HTMLSelectElement
      await waitFor(() => expect(select.value).toBe('aws-sm'))
      expect(screen.getByPlaceholderText(/eu-west-1/i)).toHaveValue('eu-west-1')
      // The pin: prefix displays although /providers never sent it.
      expect(screen.getByPlaceholderText(/prepended to cluster name/i)).toHaveValue('clusters/')
    })

    it('save with prefix → reload → field shows it (full round-trip)', async () => {
      // Step 1: fresh connection with no provider; user configures one.
      setupHook([sampleConnection])
      const user = userEvent.setup()
      const first = render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox')
      await user.selectOptions(select, 'aws-sm')
      await user.type(screen.getByPlaceholderText(/eu-west-1/i), 'us-east-1')
      await user.type(screen.getByPlaceholderText(/prepended to cluster name/i), 'k8s-')
      await user.click(screen.getByRole('button', { name: /Save Provider/i }))

      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      const body = updateConnectionMock.mock.calls[0][1] as {
        provider: { type: string; region?: string; prefix?: string }
      }
      expect(body.provider).toEqual({ type: 'aws-sm', region: 'us-east-1', prefix: 'k8s-' })

      // Step 2: simulate navigating away and back — a fresh mount where
      // the connection now carries the saved provider block, while
      // /providers (live server shape) still omits prefix.
      first.unmount()
      getProvidersMock.mockResolvedValue({
        configured_provider: { type: 'aws-sm', region: 'us-east-1', status: 'connected' },
        available_types: ['aws-sm', 'k8s-secrets'],
      })
      setupHook([{ ...sampleConnection, provider: body.provider }])
      render(<SecretsProviderSection />)

      const select2 = await screen.findByRole('combobox') as HTMLSelectElement
      await waitFor(() => expect(select2.value).toBe('aws-sm'))
      expect(screen.getByPlaceholderText(/prepended to cluster name/i)).toHaveValue('k8s-')
      expect(screen.getByPlaceholderText(/eu-west-1/i)).toHaveValue('us-east-1')
    })
  })

  // V2-cleanup-53.2 — Save Provider must give explicit feedback. Live bug:
  // the PUT succeeded (200) but the maintainer saw "it glitches and nothing
  // happens" — refreshConnections() flipped the shared loading flag, the
  // whole section swapped to LoadingState, and the tiny inline chip was
  // lost in the flash.
  describe('Save feedback (V2-cleanup-53.2)', () => {
    it('successful save fires the app-wide success toast and shows the inline Saved chip', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox')
      await user.selectOptions(select, 'argocd')
      await user.click(screen.getByRole('button', { name: /Save Provider/i }))

      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      expect(showToast).toHaveBeenCalledWith('Secrets provider saved', 'success')
      expect(await screen.findByText('Saved')).toBeInTheDocument()
      // No error text on the happy path.
      expect(screen.queryByText(/failed/i)).toBeNull()
    })

    it('failed save surfaces the server error inline near the button and fires no success toast', async () => {
      setupHook()
      updateConnectionMock.mockRejectedValue(new Error('provider validation failed: unknown region'))
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox')
      await user.selectOptions(select, 'aws-sm')
      await user.click(screen.getByRole('button', { name: /Save Provider/i }))

      expect(
        await screen.findByText('provider validation failed: unknown region'),
      ).toBeInTheDocument()
      expect(showToast).not.toHaveBeenCalled()
      expect(screen.queryByText('Saved')).toBeNull()
      // Button is re-enabled so the user can retry.
      expect(screen.getByRole('button', { name: /Save Provider/i })).toBeEnabled()
    })

    it('Save button is disabled while the PUT is in flight', async () => {
      setupHook()
      let resolveSave: (v: unknown) => void = () => {}
      updateConnectionMock.mockImplementation(
        () => new Promise((resolve) => { resolveSave = resolve }),
      )
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const select = await screen.findByRole('combobox')
      await user.selectOptions(select, 'argocd')
      const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
      await user.click(saveBtn)

      expect(saveBtn).toBeDisabled()
      resolveSave({})
      await waitFor(() => expect(saveBtn).toBeEnabled())
    })

    it('regression: a connections refresh does NOT blank the form when a connection already exists', async () => {
      // loading=true with a populated connections list is exactly the
      // post-save refreshConnections() state that used to swap the whole
      // section to LoadingState and swallow the confirmation.
      useConnectionsMock.mockReturnValue({
        connections: [sampleConnection],
        activeConnection: sampleConnection.name,
        loading: true,
        error: null,
        setActiveConnection: vi.fn(),
        refreshConnections: refreshConnectionsMock,
      })
      render(<SecretsProviderSection />)

      expect(screen.queryByText(/Loading secrets provider/i)).toBeNull()
      expect(await screen.findByRole('button', { name: /Save Provider/i })).toBeInTheDocument()
    })

    it('initial load (no connection data yet) still shows the loading state', () => {
      useConnectionsMock.mockReturnValue({
        connections: [],
        activeConnection: null,
        loading: true,
        error: null,
        setActiveConnection: vi.fn(),
        refreshConnections: refreshConnectionsMock,
      })
      render(<SecretsProviderSection />)

      expect(screen.getByText(/Loading secrets provider/i)).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /Save Provider/i })).toBeNull()
    })
  })
})
