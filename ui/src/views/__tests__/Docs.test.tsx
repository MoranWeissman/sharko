import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Docs } from '@/views/Docs';

vi.mock('@/services/api', () => ({
  api: {
    docsList: vi.fn().mockResolvedValue([
      { slug: 'overview', title: 'Overview', order: 1 },
      { slug: 'features', title: 'Features', order: 2 },
      { slug: 'managing-addons', title: 'Managing Add-ons', order: 3 },
      { slug: 'values-guide', title: 'Values Guide', order: 4 },
      { slug: 'troubleshooting', title: 'Troubleshooting', order: 5 },
    ]),
    docsGet: vi.fn().mockImplementation((slug: string) => {
      const contents: Record<string, string> = {
        'overview': '# Overview\n\nArgoCD Addons Platform is a platform for managing addons.',
        'features': '# Features\n\nHere is a quick tour of what AAP offers.',
        'managing-addons': '# Managing Add-ons\n\nHow to manage your addons.',
        'values-guide': '# Values Guide\n\nConfiguring values.',
        'troubleshooting': '# Troubleshooting\n\nCommon issues.',
      }
      return Promise.resolve({ slug, content: contents[slug] ?? `# ${slug}` })
    }),
  },
}))

function renderDocs() {
  return render(<Docs />);
}

describe('Docs', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the Overview page by default', async () => {
    renderDocs();
    await waitFor(() => {
      expect(screen.getByText('Documentation')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Overview', level: 1 })).toBeInTheDocument();
    });
    expect(screen.getByText(/ArgoCD Addons Platform/)).toBeInTheDocument();
  });

  it('renders all navigation links', async () => {
    renderDocs();
    await waitFor(() => {
      expect(screen.getByText('Overview')).toBeInTheDocument();
    });
    expect(screen.getByText('Features')).toBeInTheDocument();
    expect(screen.getByText('Managing Add-ons')).toBeInTheDocument();
    expect(screen.getByText('Values Guide')).toBeInTheDocument();
    expect(screen.getByText('Troubleshooting')).toBeInTheDocument();
  });

  it('does not render removed pages', async () => {
    renderDocs();
    await waitFor(() => {
      expect(screen.getByText('Overview')).toBeInTheDocument();
    });
    // These pages should not exist in nav
    expect(screen.queryByText('Architecture')).not.toBeInTheDocument();
    expect(screen.queryByText('Adding a Cluster')).not.toBeInTheDocument();
  });

  it('navigates to another doc page when clicked', async () => {
    const user = userEvent.setup();
    renderDocs();

    await waitFor(() => {
      expect(screen.getByText('Features')).toBeInTheDocument();
    });

    await user.click(screen.getByText('Features'));

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Features', level: 1 })).toBeInTheDocument();
    });
    expect(screen.getByText(/quick tour/)).toBeInTheDocument();
  });
});
