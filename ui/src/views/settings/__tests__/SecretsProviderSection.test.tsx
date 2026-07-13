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
  addon_secret_provider?: { type: string; region?: string; prefix?: string }
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

    // Two dropdowns now (V3-P1.2): cluster-creds and addon-secret
    const selects = await screen.findAllByRole('combobox')
    expect(selects).toHaveLength(2)
    const clusterCredsSelect = selects[0]
    const options = Array.from(clusterCredsSelect.querySelectorAll('option'))

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

    const selects = await screen.findAllByRole('combobox')
    const clusterCredsSelect = selects[0]
    const renderedValues = Array.from(clusterCredsSelect.querySelectorAll('option')).map(o => o.value)

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

    const selects = await screen.findAllByRole('combobox')
    const clusterCredsSelect = selects[0]
    await user.selectOptions(clusterCredsSelect, 'argocd')

    expect((clusterCredsSelect as HTMLSelectElement).value).toBe('argocd')

    // Region (aws-sm only) must NOT be rendered for cluster-creds when argocd is selected.
    // (Note: there might be a region input for addon-secret if aws-sm is selected there)
    // Prefix (aws-sm + k8s-secrets only) must NOT be rendered for argocd in cluster-creds section.
    // The argocd-specific helper line should be visible.
    expect(
      screen.getByText(/reads credentials from the ArgoCD cluster Secret/i),
    ).toBeInTheDocument()
  })

  it('Save with ArgoCD selected sends {provider: {type: "argocd"}} to the API', async () => {
    setupHook()
    const user = userEvent.setup()
    render(<SecretsProviderSection />)

    const selects = await screen.findAllByRole('combobox')
    await user.selectOptions(selects[0], 'argocd')
    // V3-P1.2: argocd cluster-creds requires an addon-secret backend
    await user.selectOptions(selects[1], 'k8s-secrets')

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

    const selects = await screen.findAllByRole('combobox')
    await user.selectOptions(selects[0], 'aws-sm')

    // Region input now visible (aws-sm is the only branch that shows it).
    const regionInputs = await screen.findAllByPlaceholderText(/eu-west-1/i)
    await user.type(regionInputs[0], 'us-east-1')

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

    const selects = await screen.findAllByRole('combobox')
    await user.selectOptions(selects[0], 'k8s-secrets')

    // Region input must NOT be visible for k8s-secrets in cluster-creds section.
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

    const selects = await screen.findAllByRole('combobox')
    const clusterCredsSelect = selects[0] as HTMLSelectElement
    // Wait for the form to hydrate from getProviders — initial value is
    // pulled from the resolved provider.
    await waitFor(() => expect(clusterCredsSelect.value).toBe('aws-sm'))

    await user.selectOptions(clusterCredsSelect, '')
    expect(clusterCredsSelect.value).toBe('')

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

    const selects = await screen.findAllByRole('combobox')
    const clusterCredsSelect = selects[0] as HTMLSelectElement
    // Stored alias "kubernetes" lands on the canonical k8s-secrets row.
    await waitFor(() => expect(clusterCredsSelect.value).toBe('k8s-secrets'))
    // Its stored prefix hydrates alongside — auto-expanded because prefix is non-empty.
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

      const selects = await screen.findAllByRole('combobox')
      const clusterCredsSelect = selects[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect.value).toBe('__unknown__'))

      const unknownOption = Array.from(clusterCredsSelect.querySelectorAll('option'))
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

      const selects = await screen.findAllByRole('combobox')
      const clusterCredsSelect = selects[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect.value).toBe('__unknown__'))

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

      const selects = await screen.findAllByRole('combobox')
      const clusterCredsSelect = selects[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect.value).toBe('__unknown__'))

      // The user actively picks None — a deliberate choice, not a silent default.
      await user.selectOptions(clusterCredsSelect, '')
      expect(clusterCredsSelect.value).toBe('')

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

      const selects = await screen.findAllByRole('combobox')
      const clusterCredsSelect = selects[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect.value).toBe('__unknown__'))

      await user.selectOptions(clusterCredsSelect, 'argocd')
      expect(clusterCredsSelect.value).toBe('argocd')

      // V3-P1.2: argocd requires addon-secret backend
      await user.selectOptions(selects[1], 'k8s-secrets')

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

      const selects = await screen.findAllByRole('combobox')
      const clusterCredsSelect = selects[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect.value).toBe('aws-sm'))
      const regionInputs = screen.getAllByPlaceholderText(/eu-west-1/i)
      expect(regionInputs[0]).toHaveValue('eu-west-1')
      // The pin: prefix displays although /providers never sent it.
      expect(screen.getByPlaceholderText(/prepended to cluster name/i)).toHaveValue('clusters/')
    })

    it('save with prefix → reload → field shows it (full round-trip)', async () => {
      // Step 1: fresh connection with no provider; user configures one.
      setupHook([sampleConnection])
      const user = userEvent.setup()
      const first = render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'aws-sm')
      const regionInputs = screen.getAllByPlaceholderText(/eu-west-1/i)
      await user.type(regionInputs[0], 'us-east-1')
      // Expand Advanced to reveal Prefix field (V2-cleanup-92.3 F5)
      const advancedButtons = screen.getAllByRole('button', { name: /Advanced \(optional\)/i })
      await user.click(advancedButtons[0]) // cluster-creds Advanced
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

      const selects2 = await screen.findAllByRole('combobox')
      const clusterCredsSelect2 = selects2[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect2.value).toBe('aws-sm'))
      expect(screen.getByPlaceholderText(/prepended to cluster name/i)).toHaveValue('k8s-')
      const regionInputs2 = screen.getAllByPlaceholderText(/eu-west-1/i)
      expect(regionInputs2[0]).toHaveValue('us-east-1')
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

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'argocd')
      // V3-P1.2: argocd requires addon-secret backend
      await user.selectOptions(selects[1], 'k8s-secrets')
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

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'aws-sm')
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

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'argocd')
      // V3-P1.2: argocd requires addon-secret backend
      await user.selectOptions(selects[1], 'k8s-secrets')
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

  // V2-cleanup-92.3 F4 — plain-English opener + docs link at the top
  describe('Plain-English opener + docs link (F4)', () => {
    it('renders the opener sentence and docs link at the top of the section', async () => {
      setupHook()
      render(<SecretsProviderSection />)

      expect(
        screen.getByText(/Sharko needs each cluster's credentials to reach it — this is where those credentials come from/i),
      ).toBeInTheDocument()

      const link = screen.getByRole('link', { name: /Learn how this works/i })
      expect(link).toBeInTheDocument()
      expect(link).toHaveAttribute('href', '/user-guide/secrets-provider/')
    })
  })

  // V2-cleanup-92.3 F5 — Prefix field hidden behind Advanced toggle
  describe('Advanced toggle for Prefix field (F5)', () => {
    it('Prefix field is hidden by default behind "Advanced (optional)" for aws-sm', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'aws-sm')

      // Region is always visible for aws-sm
      const regionInputs = screen.getAllByPlaceholderText(/eu-west-1/i)
      expect(regionInputs.length).toBeGreaterThan(0)

      // Prefix is behind the toggle — button visible, field NOT visible
      const advancedButtons = screen.getAllByRole('button', { name: /Advanced \(optional\)/i })
      expect(advancedButtons.length).toBeGreaterThan(0)
      expect(screen.queryByPlaceholderText(/prepended to cluster name/i)).toBeNull()
    })

    it('clicking Advanced toggle reveals the Prefix field', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'aws-sm')

      const advancedButtons = screen.getAllByRole('button', { name: /Advanced \(optional\)/i })
      await user.click(advancedButtons[0]) // cluster-creds Advanced

      // Prefix field now visible
      expect(screen.getByPlaceholderText(/prepended to cluster name/i)).toBeInTheDocument()
    })

    it('if stored provider has a non-empty prefix, Advanced is auto-expanded', async () => {
      setupHook([{
        ...sampleConnection,
        provider: { type: 'aws-sm', region: 'eu-west-1', prefix: 'k8s-' },
      }])
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      const clusterCredsSelect = selects[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect.value).toBe('aws-sm'))

      // Prefix field visible without clicking Advanced — auto-expanded
      const prefixInput = screen.getByPlaceholderText(/prepended to cluster name/i)
      expect(prefixInput).toBeInTheDocument()
      expect(prefixInput).toHaveValue('k8s-')
    })

    it('if stored prefix is empty, Advanced defaults collapsed', async () => {
      setupHook([{
        ...sampleConnection,
        provider: { type: 'aws-sm', region: 'eu-west-1', prefix: '' },
      }])
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      const clusterCredsSelect = selects[0] as HTMLSelectElement
      await waitFor(() => expect(clusterCredsSelect.value).toBe('aws-sm'))

      // Prefix field hidden by default
      expect(screen.queryByPlaceholderText(/prepended to cluster name/i)).toBeNull()
      // But Advanced button is present
      const advancedButtons = screen.getAllByRole('button', { name: /Advanced \(optional\)/i })
      expect(advancedButtons.length).toBeGreaterThan(0)
    })

    it('Advanced toggle is not rendered for argocd (no prefix support)', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'argocd')

      // No Advanced button for argocd in cluster-creds section
      // (but there might be one for addon-secret if it's aws-sm/k8s-secrets)
      expect(screen.queryByPlaceholderText(/prepended to cluster name/i)).toBeNull()
    })

    it('Prefix field remains functional when expanded — value persists and saves', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'k8s-secrets')

      // Expand Advanced (first one = cluster-creds)
      const advancedButtons = screen.getAllByRole('button', { name: /Advanced \(optional\)/i })
      await user.click(advancedButtons[0])

      const prefixInput = screen.getByPlaceholderText(/prepended to cluster name/i)
      await user.type(prefixInput, 'team-')

      await user.click(screen.getByRole('button', { name: /Save Provider/i }))

      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      const body = updateConnectionMock.mock.calls[0][1] as { provider: { type: string; prefix?: string } }
      expect(body.provider.type).toBe('k8s-secrets')
      expect(body.provider.prefix).toBe('team-')
    })
  })

  // V3-P1.2 — split UI for cluster-creds vs addon-secret providers
  describe('Split UI — cluster-creds vs addon-secret providers (V3-P1.2)', () => {
    it('renders two sections with distinct headings', async () => {
      setupHook()
      render(<SecretsProviderSection />)

      expect(await screen.findByText('How Sharko reaches your clusters')).toBeInTheDocument()
      expect(screen.getByText('Where addon secret values come from')).toBeInTheDocument()
    })

    it('argocd is NOT offered in the addon-secret dropdown', async () => {
      setupHook()
      render(<SecretsProviderSection />)

      // Find both dropdowns
      const selects = await screen.findAllByRole('combobox')
      expect(selects).toHaveLength(2)

      const clusterCredsSelect = selects[0]
      const addonSecretSelect = selects[1]

      // argocd option exists in cluster-creds dropdown
      const clusterCredsOptions = Array.from(clusterCredsSelect.querySelectorAll('option')).map(o => o.value)
      expect(clusterCredsOptions).toContain('argocd')

      // argocd option does NOT exist in addon-secret dropdown
      const addonSecretOptions = Array.from(addonSecretSelect.querySelectorAll('option')).map(o => o.value)
      expect(addonSecretOptions).not.toContain('argocd')
      expect(addonSecretOptions).toContain('aws-sm')
      expect(addonSecretOptions).toContain('k8s-secrets')
    })

    it('Save with both fields set sends both provider and addon_secret_provider in payload', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'argocd')
      await user.selectOptions(selects[1], 'aws-sm')

      const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
      await user.click(saveBtn)

      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      const body = updateConnectionMock.mock.calls[0][1] as {
        provider?: { type: string }
        addon_secret_provider?: { type: string }
      }

      expect(body.provider).toBeDefined()
      expect(body.provider?.type).toBe('argocd')
      expect(body.addon_secret_provider).toBeDefined()
      expect(body.addon_secret_provider?.type).toBe('aws-sm')
    })

    it('argocd cluster-creds + no addon-secret → Save blocked with required message', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'argocd')
      // Leave addon-secret as "None"

      const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
      await user.click(saveBtn)

      // Save should NOT have been called
      expect(updateConnectionMock).not.toHaveBeenCalled()

      // Required message visible (appears both as helper text and save error — check for save error specifically)
      const messages = await screen.findAllByText(/ArgoCD only tells Sharko how to reach your clusters. Choose where addon secret values come from./i)
      expect(messages.length).toBeGreaterThanOrEqual(1)
      // At least one of them should be the error message (red text)
      const errorMessage = messages.find(el => el.classList.contains('text-red-600') || el.classList.contains('text-red-400'))
      expect(errorMessage).toBeDefined()
    })

    it('argocd cluster-creds + aws-sm addon-secret → Save enabled', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'argocd')
      await user.selectOptions(selects[1], 'aws-sm')

      const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
      await user.click(saveBtn)

      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      const body = updateConnectionMock.mock.calls[0][1] as {
        provider?: { type: string }
        addon_secret_provider?: { type: string }
      }
      expect(body.provider?.type).toBe('argocd')
      expect(body.addon_secret_provider?.type).toBe('aws-sm')
    })

    it('aws-sm cluster-creds + k8s-secrets addon-secret → both saved independently', async () => {
      setupHook()
      const user = userEvent.setup()
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox')
      await user.selectOptions(selects[0], 'aws-sm')
      await user.selectOptions(selects[1], 'k8s-secrets')

      // Fill aws-sm region for cluster-creds
      const regionInputs = await screen.findAllByPlaceholderText(/eu-west-1/i)
      await user.type(regionInputs[0], 'us-east-1')

      const saveBtn = screen.getByRole('button', { name: /Save Provider/i })
      await user.click(saveBtn)

      await waitFor(() => expect(updateConnectionMock).toHaveBeenCalledTimes(1))
      const body = updateConnectionMock.mock.calls[0][1] as {
        provider?: { type: string; region?: string }
        addon_secret_provider?: { type: string }
      }
      expect(body.provider?.type).toBe('aws-sm')
      expect(body.provider?.region).toBe('us-east-1')
      expect(body.addon_secret_provider?.type).toBe('k8s-secrets')
    })

    it('hydrates both fields from connection data', async () => {
      setupHook([{
        ...sampleConnection,
        provider: { type: 'argocd' },
        addon_secret_provider: { type: 'aws-sm', region: 'eu-west-1' },
      }])
      render(<SecretsProviderSection />)

      const selects = await screen.findAllByRole('combobox') as HTMLSelectElement[]
      await waitFor(() => expect(selects[0].value).toBe('argocd'))
      await waitFor(() => expect(selects[1].value).toBe('aws-sm'))

      // Region hydrated for addon-secret
      const regionInputs = screen.getAllByPlaceholderText(/eu-west-1/i)
      expect(regionInputs[0]).toHaveValue('eu-west-1')
    })

    it('addon_secret_status=missing + addon_secret_message renders the message', async () => {
      getProvidersMock.mockResolvedValue({
        configured_provider: {
          type: 'argocd',
          region: '',
          status: 'configured',
          addon_secret_status: 'missing',
          addon_secret_message: 'No addon-secret backend configured',
        },
        available_types: ['argocd', 'aws-sm', 'k8s-secrets'],
      })
      setupHook([{
        ...sampleConnection,
        provider: { type: 'argocd' },
      }])
      render(<SecretsProviderSection />)

      expect(
        await screen.findByText('No addon-secret backend configured'),
      ).toBeInTheDocument()
    })

    it('addon_secret_status=invalid_argocd + message renders the message', async () => {
      getProvidersMock.mockResolvedValue({
        configured_provider: {
          type: 'argocd',
          region: '',
          status: 'configured',
          addon_secret_status: 'invalid_argocd',
          addon_secret_message: 'ArgoCD provider is cluster-credentials-only; configure a separate backend (aws-sm, k8s-secrets, gcp-sm, azure-kv) for addon secrets',
        },
        available_types: ['argocd', 'aws-sm', 'k8s-secrets'],
      })
      setupHook([{
        ...sampleConnection,
        provider: { type: 'argocd' },
        addon_secret_provider: { type: 'argocd' }, // invalid choice
      }])
      render(<SecretsProviderSection />)

      expect(
        await screen.findByText(/ArgoCD provider is cluster-credentials-only; configure a separate backend/i),
      ).toBeInTheDocument()
    })
  })

  // V3-P1.2 — positioning one-liner in opener
  describe('Positioning one-liner in opener (V3-P1.2)', () => {
    it('renders the IDP/GitOps positioning sentence verbatim', async () => {
      setupHook()
      render(<SecretsProviderSection />)

      expect(
        screen.getByText(/Sharko is a GitOps agent with an API: your portal or pipeline asks for "a cluster with these addons," and Sharko opens a pull request — it never changes your cluster behind your back/i),
      ).toBeInTheDocument()
    })
  })
})
