/**
 * V4EnableAddonDialog — unit tests (v4 Wave 1 Story 4.3 UI half).
 *
 * Covers the "sharpened pipeline" contract end to end from the UI's
 * point of view: preview shows the exact files (DryRunPreview), a 422
 * shows the plain-words problems list BEFORE any PR talk (never mixed
 * with a preview or a PR banner), and a successful apply shows the PR
 * reference.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { V4EnableAddonDialog } from '@/components/V4EnableAddonDialog';
import { V4AddonValidationError } from '@/services/api';

const mockEnableAddonV4 = vi.fn();
const mockDisableAddonV4 = vi.fn();
const mockAddToCatalog = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    enableAddonV4: (...args: unknown[]) => mockEnableAddonV4(...args),
    disableAddonV4: (...args: unknown[]) => mockDisableAddonV4(...args),
    api: {
      ...actual.api,
      addToCatalog: (...args: unknown[]) => mockAddToCatalog(...args),
    },
  };
});

function renderDialog(overrides: Partial<React.ComponentProps<typeof V4EnableAddonDialog>> = {}) {
  const onClose = vi.fn();
  const onApplied = vi.fn();
  const utils = render(
    <V4EnableAddonDialog
      open
      cluster="prod-eu"
      addon="cert-manager"
      mode="enable"
      onClose={onClose}
      onApplied={onApplied}
      {...overrides}
    />,
  );
  return { ...utils, onClose, onApplied };
}

beforeEach(() => {
  mockEnableAddonV4.mockReset();
  mockDisableAddonV4.mockReset();
  mockAddToCatalog.mockReset();
});

describe('V4EnableAddonDialog', () => {
  it('renders the title naming cluster and addon', () => {
    renderDialog();
    expect(screen.getByText('Enable cert-manager on prod-eu')).toBeInTheDocument();
  });

  it('preview shows the exact files to write', async () => {
    mockEnableAddonV4.mockResolvedValue({
      dry_run: {
        effective_addons: ['cert-manager'],
        files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'update', diff: '--- a\n+++ b\n' }],
        pr_title: 'sharko: enable addon cert-manager on cluster prod-eu',
        secrets_to_create: [],
      },
    });

    renderDialog();
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));

    await waitFor(() => {
      expect(screen.getByText('clusters/prod-eu.yaml')).toBeInTheDocument();
    });
    expect(mockEnableAddonV4).toHaveBeenCalledWith('prod-eu', 'cert-manager', { dry_run: true, values: undefined });
    // Confirm button appears once a clean preview is shown.
    expect(screen.getByTestId('v4-confirm')).toBeInTheDocument();
  });

  it('a 422 shows the plain-words problems list BEFORE any PR talk', async () => {
    mockEnableAddonV4.mockRejectedValue(
      new V4AddonValidationError({
        error: 'cannot enable vault on prod-eu: missing required value "server.ha.enabled"',
        cluster: 'prod-eu',
        addon: 'vault',
        problems: [
          'missing required value "server.ha.enabled" — Whether to run Vault in HA mode.',
          'missing required value "server.dataStorage" — The persistent volume Vault stores its data on.',
        ],
      }),
    );

    renderDialog({ addon: 'vault' });
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));

    await waitFor(() => {
      expect(screen.getByTestId('v4-problems')).toBeInTheDocument();
    });
    expect(screen.getByText(/missing required value "server.ha.enabled"/)).toBeInTheDocument();
    expect(screen.getByText(/missing required value "server.dataStorage"/)).toBeInTheDocument();
    expect(screen.getByText(/Nothing was written/)).toBeInTheDocument();

    // No preview, no confirm/PR button — problems come before any PR talk.
    expect(screen.queryByTestId('v4-confirm')).not.toBeInTheDocument();
    expect(screen.queryByText(/PR merged|PR opened/)).not.toBeInTheDocument();
  });

  // v4 wave 2.5 (design decision 4) — enabling now requires catalog
  // membership. Instead of leaving the operator at a dead 422, the dialog
  // offers the one-PR combo: add to catalog AND enable, together.
  it('a "not in catalog" 422 offers the add-to-catalog-and-enable combo instead of a dead end', async () => {
    mockEnableAddonV4.mockRejectedValue(
      new V4AddonValidationError({
        error: 'cannot enable cert-manager on prod-eu: cert-manager is not in your catalog',
        cluster: 'prod-eu',
        addon: 'cert-manager',
        problems: ['cert-manager is not in your catalog yet — add it before enabling it here'],
      }),
    );
    mockAddToCatalog.mockResolvedValue({
      added: ['cert-manager'],
      enabled: ['cert-manager'],
      cluster: 'prod-eu',
      pr_url: 'https://github.com/demo/sharko-addons/pull/101',
      branch: 'sharko/catalog-cert-manager',
    });

    renderDialog();
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));
    await waitFor(() => {
      expect(screen.getByTestId('v4-catalog-gate-combo')).toBeInTheDocument();
    });

    const combo = screen.getByRole('button', {
      name: /add to catalog and enable on prod-eu/i,
    });
    fireEvent.click(combo);

    await waitFor(() => {
      expect(mockAddToCatalog).toHaveBeenCalledWith({
        addons: [{ name: 'cert-manager', from_marketplace: true }],
        enable_on_cluster: 'prod-eu',
      });
    });
    await waitFor(() => {
      expect(screen.getByText(/PR opened/)).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: /view pr/i })).toHaveAttribute(
      'href',
      'https://github.com/demo/sharko-addons/pull/101',
    );
  });

  it('success shows the PR reference', async () => {
    mockEnableAddonV4
      .mockResolvedValueOnce({
        dry_run: {
          effective_addons: ['cert-manager'],
          files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'update' }],
          pr_title: 'sharko: enable addon cert-manager on cluster prod-eu',
          secrets_to_create: [],
        },
      })
      .mockResolvedValueOnce({
        pr_url: 'https://github.com/demo/sharko-addons/pull/99',
        pr_id: 99,
        merged: false,
      });

    renderDialog();
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));
    await waitFor(() => expect(screen.getByTestId('v4-confirm')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('v4-confirm'));

    await waitFor(() => {
      expect(screen.getByText(/View PR #99/)).toBeInTheDocument();
    });
    expect(mockEnableAddonV4).toHaveBeenLastCalledWith('prod-eu', 'cert-manager', { yes: true, values: undefined });
  });

  it('preview shows a non-blocking runtime-secret warning without hiding the confirm button', async () => {
    mockEnableAddonV4.mockResolvedValue({
      dry_run: {
        effective_addons: ['external-secrets'],
        files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'create' }],
        pr_title: 'sharko: enable addon external-secrets on cluster prod-eu',
        secrets_to_create: [],
      },
      warnings: [
        'heads-up: external-secrets will need the secret "Backend secret-manager credentials" later — nothing is required to install it now',
      ],
    });

    renderDialog({ addon: 'external-secrets' });
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));

    await waitFor(() => {
      expect(screen.getByTestId('v4-warnings')).toBeInTheDocument();
    });
    expect(screen.getByText(/will need the secret "Backend secret-manager credentials" later/)).toBeInTheDocument();
    // A runtime-secret warning never blocks the install — the confirm/PR
    // path stays available, unlike the 422 problems list.
    expect(screen.getByTestId('v4-confirm')).toBeInTheDocument();
    expect(screen.queryByTestId('v4-problems')).not.toBeInTheDocument();
  });

  it('the real enable response also surfaces the runtime-secret warning', async () => {
    mockEnableAddonV4
      .mockResolvedValueOnce({
        dry_run: {
          effective_addons: ['external-secrets'],
          files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'create' }],
          pr_title: 'sharko: enable addon external-secrets on cluster prod-eu',
          secrets_to_create: [],
        },
        warnings: ['heads-up: external-secrets will need the secret "Backend secret-manager credentials" later'],
      })
      .mockResolvedValueOnce({
        pr_url: 'https://github.com/demo/sharko-addons/pull/101',
        pr_id: 101,
        merged: false,
        warnings: ['heads-up: external-secrets will need the secret "Backend secret-manager credentials" later'],
      });

    renderDialog({ addon: 'external-secrets' });
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));
    await waitFor(() => expect(screen.getByTestId('v4-confirm')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('v4-confirm'));

    await waitFor(() => {
      expect(screen.getByText(/View PR #101/)).toBeInTheDocument();
    });
    expect(screen.getByTestId('v4-warnings')).toBeInTheDocument();
    expect(screen.getByText(/will need the secret "Backend secret-manager credentials" later/)).toBeInTheDocument();
  });

  it('disable mode calls disableAddonV4 and skips the values field', async () => {
    mockDisableAddonV4.mockResolvedValue({
      dry_run: {
        effective_addons: [],
        files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'update' }],
        pr_title: 'sharko: disable addon cert-manager on cluster prod-eu',
        secrets_to_create: [],
      },
    });

    renderDialog({ mode: 'disable' });
    expect(screen.queryByTestId('v4-enable-values')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /preview/i }));
    await waitFor(() => {
      expect(mockDisableAddonV4).toHaveBeenCalledWith('prod-eu', 'cert-manager', { dry_run: true });
    });
  });

  // v4-wave2 review fix — the problems header hardcoded "enable" even in
  // disable mode. disableAddonV4 can hit the same 422 problems-first path
  // (see the component's own "route it through the same problems-first
  // surface if it does" comment), so the copy must match the actual mode.
  it('disable mode: a 422 problems header says "disable", not "enable"', async () => {
    mockDisableAddonV4.mockRejectedValue(
      new V4AddonValidationError({
        error: 'cannot disable cert-manager on prod-eu: still referenced by vault',
        cluster: 'prod-eu',
        addon: 'cert-manager',
        problems: ['still referenced by vault — remove that dependency first'],
      }),
    );

    renderDialog({ mode: 'disable' });
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));

    await waitFor(() => {
      expect(screen.getByTestId('v4-problems')).toBeInTheDocument();
    });
    expect(screen.getByText("Sharko can't disable cert-manager on prod-eu yet")).toBeInTheDocument();
    expect(screen.queryByText("Sharko can't enable cert-manager on prod-eu yet")).not.toBeInTheDocument();
  });

  it('enable mode: a 422 problems header still says "enable"', async () => {
    mockEnableAddonV4.mockRejectedValue(
      new V4AddonValidationError({
        error: 'cannot enable cert-manager on prod-eu: missing required value',
        cluster: 'prod-eu',
        addon: 'cert-manager',
        problems: ['missing required value "server.ha.enabled"'],
      }),
    );

    renderDialog({ mode: 'enable' });
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));

    await waitFor(() => {
      expect(screen.getByTestId('v4-problems')).toBeInTheDocument();
    });
    expect(screen.getByText("Sharko can't enable cert-manager on prod-eu yet")).toBeInTheDocument();
  });

  it('rejects malformed YAML in the values field before calling the API', () => {
    renderDialog();
    fireEvent.change(screen.getByTestId('v4-enable-values'), { target: { value: '{not json' } });
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));

    expect(
      screen.getByText('Values must be a set of key: value pairs, e.g. installCRDs: true'),
    ).toBeInTheDocument();
    expect(mockEnableAddonV4).not.toHaveBeenCalled();
  });

  it('Cancel calls onClose', () => {
    const { onClose } = renderDialog();
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });

  // w2-q3 UI half — plain YAML values input.
  describe('values input accepts YAML, requires a mapping (w2-q3)', () => {
    it('accepts a plain YAML mapping (no braces/quotes)', async () => {
      mockEnableAddonV4.mockResolvedValue({
        dry_run: {
          effective_addons: ['cert-manager'],
          files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'update' }],
          pr_title: 'sharko: enable addon cert-manager on cluster prod-eu',
          secrets_to_create: [],
        },
      });

      renderDialog();
      fireEvent.change(screen.getByTestId('v4-enable-values'), {
        target: { value: 'installCRDs: true' },
      });
      fireEvent.click(screen.getByRole('button', { name: /preview/i }));

      await waitFor(() => {
        expect(mockEnableAddonV4).toHaveBeenCalledWith('prod-eu', 'cert-manager', {
          dry_run: true,
          values: { installCRDs: true },
        });
      });
    });

    it('still accepts JSON (JSON is valid YAML)', async () => {
      mockEnableAddonV4.mockResolvedValue({
        dry_run: {
          effective_addons: ['cert-manager'],
          files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'update' }],
          pr_title: 'sharko: enable addon cert-manager on cluster prod-eu',
          secrets_to_create: [],
        },
      });

      renderDialog();
      fireEvent.change(screen.getByTestId('v4-enable-values'), {
        target: { value: '{"installCRDs": true}' },
      });
      fireEvent.click(screen.getByRole('button', { name: /preview/i }));

      await waitFor(() => {
        expect(mockEnableAddonV4).toHaveBeenCalledWith('prod-eu', 'cert-manager', {
          dry_run: true,
          values: { installCRDs: true },
        });
      });
    });

    it('rejects a quoted string with the plain-words error — closes the live decode-error leak', () => {
      // This is the exact live bug: a quoted string like "installCRDs: true"
      // used to pass the old JSON.parse-only check (it's a valid JSON
      // string) and get forwarded to the backend, which then leaked a raw
      // Go decode error. It must now be rejected client-side.
      renderDialog();
      fireEvent.change(screen.getByTestId('v4-enable-values'), {
        target: { value: '"installCRDs: true"' },
      });
      fireEvent.click(screen.getByRole('button', { name: /preview/i }));

      expect(
        screen.getByText('Values must be a set of key: value pairs, e.g. installCRDs: true'),
      ).toBeInTheDocument();
      expect(mockEnableAddonV4).not.toHaveBeenCalled();
    });

    it('rejects an array with the plain-words error', () => {
      renderDialog();
      fireEvent.change(screen.getByTestId('v4-enable-values'), {
        target: { value: '- a\n- b' },
      });
      fireEvent.click(screen.getByRole('button', { name: /preview/i }));

      expect(
        screen.getByText('Values must be a set of key: value pairs, e.g. installCRDs: true'),
      ).toBeInTheDocument();
      expect(mockEnableAddonV4).not.toHaveBeenCalled();
    });

    it('leaves an empty values field valid — no values sent', async () => {
      mockEnableAddonV4.mockResolvedValue({
        dry_run: {
          effective_addons: ['cert-manager'],
          files_to_write: [{ path: 'clusters/prod-eu.yaml', action: 'update' }],
          pr_title: 'sharko: enable addon cert-manager on cluster prod-eu',
          secrets_to_create: [],
        },
      });

      renderDialog();
      fireEvent.click(screen.getByRole('button', { name: /preview/i }));

      await waitFor(() => {
        expect(mockEnableAddonV4).toHaveBeenCalledWith('prod-eu', 'cert-manager', {
          dry_run: true,
          values: undefined,
        });
      });
    });
  });
});
