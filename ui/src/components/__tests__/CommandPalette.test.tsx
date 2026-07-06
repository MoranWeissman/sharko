import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CommandPalette } from '@/components/CommandPalette';

// V2-cleanup-61.4 (G2): the palette used to be a hand-rolled `fixed`
// backdrop + modal with a manual window `keydown` listener for Escape and
// no focus trap / ARIA dialog semantics. It's now built on the shadcn/Radix
// Dialog primitive. These tests pin the externally-observable behavior:
// Cmd+K opens it, Escape closes it, and it exposes real dialog semantics.

vi.mock('@/services/api', () => ({
  api: {
    getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
    getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
  },
}));

function renderPalette() {
  return render(
    <MemoryRouter>
      <CommandPalette />
    </MemoryRouter>,
  );
}

function pressCmdK() {
  fireEvent.keyDown(window, { key: 'k', metaKey: true });
}

describe('CommandPalette — dialog behavior (V2-cleanup-61.4, G2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('is not rendered until opened', () => {
    renderPalette();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('opens on Cmd+K as a real dialog with a search input', async () => {
    renderPalette();
    pressCmdK();

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
    expect(screen.getByPlaceholderText('Search pages, clusters, addons...')).toBeInTheDocument();
  });

  it('closes on Escape', async () => {
    renderPalette();
    pressCmdK();

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('navigates to a page result on click and closes', async () => {
    renderPalette();
    pressCmdK();

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText('Clusters'));

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });
});
