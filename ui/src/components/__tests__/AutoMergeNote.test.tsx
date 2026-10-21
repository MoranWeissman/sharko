import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { AutoMergeNote } from '@/components/AutoMergeNote'

// Story G2 (episode 2, take 2). The dialogs used to say "Auto-merge follows
// your global GitOps setting." without saying what that setting is. The
// note now reads the setting and says plainly what will happen. These tests
// pin every sentence by exact text and ban the old one.

const mockGetConfig = vi.fn()
vi.mock('@/services/api', () => ({
  api: {
    getConfig: (...args: unknown[]) => mockGetConfig(...args),
  },
}))

const OLD_TEXT = 'Auto-merge follows your global GitOps setting.'

function noteText(): string {
  return screen.getByTestId('auto-merge-note').textContent ?? ''
}

describe('AutoMergeNote (story G2)', () => {
  beforeEach(() => {
    mockGetConfig.mockReset()
  })

  it('says the pull request merges by itself when the switch is on', async () => {
    mockGetConfig.mockResolvedValue({ gitops: { pr_auto_merge: true } })
    render(<AutoMergeNote />)
    await waitFor(() =>
      expect(noteText()).toBe('This pull request merges by itself. Change this in GitOps settings.'),
    )
    expect(noteText()).not.toContain(OLD_TEXT)
  })

  it('says the pull request waits for you when the switch is off', async () => {
    mockGetConfig.mockResolvedValue({ gitops: { pr_auto_merge: false } })
    render(<AutoMergeNote />)
    await waitFor(() =>
      expect(noteText()).toBe('This pull request waits for you to merge it. Change this in GitOps settings.'),
    )
    expect(noteText()).not.toContain(OLD_TEXT)
  })

  it('uses the plural sentences for many pull requests', async () => {
    mockGetConfig.mockResolvedValue({ gitops: { pr_auto_merge: false } })
    const { unmount } = render(<AutoMergeNote many />)
    await waitFor(() =>
      expect(noteText()).toBe('These pull requests wait for you to merge them. Change this in GitOps settings.'),
    )
    unmount()

    mockGetConfig.mockResolvedValue({ gitops: { pr_auto_merge: true } })
    render(<AutoMergeNote many />)
    await waitFor(() =>
      expect(noteText()).toBe('These pull requests merge by themselves. Change this in GitOps settings.'),
    )
  })

  it('points to the settings without guessing when the setting cannot be read', async () => {
    mockGetConfig.mockRejectedValue(new Error('boom'))
    render(<AutoMergeNote />)
    await waitFor(() => expect(mockGetConfig).toHaveBeenCalled())
    expect(noteText()).toBe('Whether this pull request merges by itself is set in GitOps settings.')
    expect(noteText()).not.toContain(OLD_TEXT)
  })

  it('uses the plural "not known" sentence for many pull requests', async () => {
    mockGetConfig.mockResolvedValue({})
    render(<AutoMergeNote many />)
    await waitFor(() => expect(mockGetConfig).toHaveBeenCalled())
    expect(noteText()).toBe('Whether these pull requests merge by themselves is set in GitOps settings.')
  })

  it('links to the GitOps settings section', async () => {
    mockGetConfig.mockResolvedValue({ gitops: { pr_auto_merge: false } })
    render(<AutoMergeNote />)
    const link = await screen.findByRole('link', { name: 'GitOps settings' })
    expect(link).toHaveAttribute('href', '/settings?section=gitops')
  })
})
