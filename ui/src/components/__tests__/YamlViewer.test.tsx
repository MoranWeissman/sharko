import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { YamlViewer } from '@/components/YamlViewer';

const sampleYaml = `name: my-addon
version: 1.2.3
enabled: true
replicas: 3
config:
  timeout: 30
  retries: 5
`;

describe('YamlViewer', () => {
  it('renders raw YAML by default', () => {
    render(<YamlViewer yaml={sampleYaml} title="Test Values" />);
    expect(screen.getByText('Test Values')).toBeInTheDocument();
    // The Raw button should be active
    const rawButton = screen.getByText('Raw');
    expect(rawButton.className).toContain('bg-teal-500');
    // Content should be rendered (check for a key from the YAML)
    expect(screen.getByText(/my-addon/)).toBeInTheDocument();
  });

  it('toggles to tree view when Tree button is clicked', () => {
    render(<YamlViewer yaml={sampleYaml} />);
    const treeButton = screen.getByText('Tree');
    fireEvent.click(treeButton);
    // Tree button should now be active
    expect(treeButton.className).toContain('bg-teal-500');
    // Should render tree nodes — keys have ":" appended, so use partial match
    expect(screen.getByText(/^name/)).toBeInTheDocument();
    expect(screen.getByText(/^config/)).toBeInTheDocument();
  });

  it('has a copy button', () => {
    render(<YamlViewer yaml={sampleYaml} />);
    const copyButton = screen.getByLabelText('Copy YAML');
    expect(copyButton).toBeInTheDocument();
  });
});

describe('YamlViewer with a file that holds only comments', () => {
  const commentsOnly =
    '# Helm values for reloader on spoke-1 only.\n# An empty file means no cluster-specific overrides.\n'

  it('says the file has only comments and shows it as it is, never "Unable to parse YAML"', () => {
    const { container } = render(<YamlViewer yaml={commentsOnly} defaultView="tree" />);
    expect(container.textContent).toContain(
      'This file has only comments, no settings yet. Showing the file as it is:',
    );
    expect(container.textContent).toContain('# An empty file means no cluster-specific overrides.');
    expect(container.textContent).not.toContain('Unable to parse YAML');
  });

  it('still says "Unable to parse YAML" for a file that really is broken', () => {
    const { container } = render(<YamlViewer yaml={'a: [unclosed\n'} defaultView="tree" />);
    expect(container.textContent).toContain('Unable to parse YAML');
  });
});
