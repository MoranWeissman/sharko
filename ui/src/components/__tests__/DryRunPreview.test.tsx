import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { DryRunPreview } from '@/components/AddAddonFlow';
import type { DryRunResult } from '@/services/models';

describe('DryRunPreview', () => {
  it('renders with min-w-0 containment classes for wide content', () => {
    // Use placeholder account ID per content policy
    const longDiff = `- roleArn: arn:aws:iam::000000000000:role/sharko-old-very-long-role-name-that-extends-beyond-modal-width
+ roleArn: arn:aws:iam::000000000000:role/sharko-new-very-long-role-name-that-also-extends-way-past-the-modal-border`;

    const result: DryRunResult = {
      pr_title: 'Update cluster config',
      files_to_write: [
        {
          path: 'clusters/prod-eu.yaml',
          action: 'update',
          diff: longDiff,
        },
      ],
    };

    const { container } = render(<DryRunPreview result={result} />);

    // Preview header renders
    expect(screen.getByText('Preview')).toBeInTheDocument();
    expect(screen.getByText('Update cluster config')).toBeInTheDocument();

    // File path renders
    expect(screen.getByText('clusters/prod-eu.yaml')).toBeInTheDocument();

    // Diff content renders with the long line (check a substring)
    expect(screen.getByText(/000000000000:role\/sharko-old-very-long-role-name/)).toBeInTheDocument();
    expect(screen.getByText(/000000000000:role\/sharko-new-very-long-role-name/)).toBeInTheDocument();

    // Check that min-w-0 classes are present on the container chain
    // The outer div (Preview panel) should have min-w-0
    const previewPanel = container.querySelector('.ring-2.ring-\\[\\#6aade0\\]');
    expect(previewPanel?.className).toContain('min-w-0');

    // The diff container should have overflow-x-auto and min-w-0
    const diffContainer = container.querySelector('.overflow-x-auto');
    expect(diffContainer?.className).toContain('min-w-0');
  });

  it('renders create action with label and no diff dump', () => {
    const result: DryRunResult = {
      pr_title: 'Add new cluster',
      files_to_write: [
        {
          path: 'clusters/dev-us.yaml',
          action: 'create',
        },
      ],
    };

    render(<DryRunPreview result={result} />);

    expect(screen.getByText('clusters/dev-us.yaml')).toBeInTheDocument();
    expect(screen.getByText('(new file)')).toBeInTheDocument();
  });

  it('renders delete action with label', () => {
    const result: DryRunResult = {
      pr_title: 'Remove cluster',
      files_to_write: [
        {
          path: 'clusters/old-cluster.yaml',
          action: 'delete',
        },
      ],
    };

    render(<DryRunPreview result={result} />);

    expect(screen.getByText('clusters/old-cluster.yaml')).toBeInTheDocument();
    expect(screen.getByText('(removed)')).toBeInTheDocument();
  });

  it('renders secrets with <redacted> placeholder', () => {
    const result: DryRunResult = {
      pr_title: 'Update secrets',
      files_to_write: [
        {
          path: 'addons/cert-manager.yaml',
          action: 'update',
          diff: '- apiKey: <redacted>\n+ apiKey: <redacted>',
        },
      ],
      secrets_to_create: ['cert-manager-secret'],
    };

    const { container } = render(<DryRunPreview result={result} />);

    expect(screen.getByText('cert-manager-secret')).toBeInTheDocument();
    // <redacted> appears multiple times, so just verify it's in the document
    const diffContainer = container.querySelector('.overflow-x-auto');
    expect(diffContainer?.textContent).toContain('<redacted>');
  });

  it('renders effective_addons when present', () => {
    const result: DryRunResult = {
      pr_title: 'Register addon',
      files_to_write: [],
      effective_addons: ['cert-manager', 'external-dns'],
    };

    render(<DryRunPreview result={result} />);

    expect(screen.getByText(/cert-manager, external-dns/)).toBeInTheDocument();
  });
});
