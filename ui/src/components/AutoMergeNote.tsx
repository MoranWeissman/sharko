import { useEffect, useState } from 'react'
import { api } from '@/services/api'

// Story G2 (episode 2, take 2): the dialogs used to say "Auto-merge follows
// your global GitOps setting." That was true, but it did not say what the
// setting IS, and the first-run wizard's "Set Up & Auto-Merge" button only
// merges the one setup pull request — it never turns the setting on. So a
// user who picked auto-merge in the wizard read that line and expected the
// register pull request to merge by itself, and it did not.
//
// This note reads the same setting the server uses when it decides whether
// to merge (GET /config → gitops.pr_auto_merge) and says plainly what will
// happen. The exact sentences are pinned in AutoMergeNote.test.tsx.

export const AUTO_MERGE_ON_TEXT = 'This pull request merges by itself.'
export const AUTO_MERGE_OFF_TEXT = 'This pull request waits for you to merge it.'
export const AUTO_MERGE_ON_TEXT_MANY = 'These pull requests merge by themselves.'
export const AUTO_MERGE_OFF_TEXT_MANY = 'These pull requests wait for you to merge them.'
// Shown while the setting is loading, or if Sharko could not read it.
export const AUTO_MERGE_UNKNOWN_TEXT = 'Whether this pull request merges by itself is set in'
export const AUTO_MERGE_UNKNOWN_TEXT_MANY = 'Whether these pull requests merge by themselves is set in'
export const AUTO_MERGE_CHANGE_TEXT = 'Change this in'
export const AUTO_MERGE_LINK_TEXT = 'GitOps settings'

interface AutoMergeNoteProps {
  /** Text size class; the dialogs differ (text-xs / text-sm). */
  className?: string
  /** True when the action opens one pull request per item (adopt). */
  many?: boolean
}

export function AutoMergeNote({ className = 'text-sm', many = false }: AutoMergeNoteProps) {
  const [autoMerge, setAutoMerge] = useState<boolean | null>(null)

  useEffect(() => {
    let cancelled = false
    // Wrapped so a missing or failing config call only leaves the note in
    // its "not known" form — it never breaks the dialog around it.
    Promise.resolve()
      .then(() => api.getConfig())
      .then((cfg) => {
        if (cancelled) return
        const v = cfg?.gitops?.pr_auto_merge
        setAutoMerge(typeof v === 'boolean' ? v : null)
      })
      .catch(() => {
        if (!cancelled) setAutoMerge(null)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const link = (
    <a href="/settings?section=gitops" className="underline hover:text-[#0a2a4a] dark:hover:text-gray-300">
      {AUTO_MERGE_LINK_TEXT}
    </a>
  )

  let lead: string
  let tail: string
  if (autoMerge === null) {
    lead = many ? AUTO_MERGE_UNKNOWN_TEXT_MANY : AUTO_MERGE_UNKNOWN_TEXT
    tail = ''
  } else {
    if (autoMerge) {
      lead = many ? AUTO_MERGE_ON_TEXT_MANY : AUTO_MERGE_ON_TEXT
    } else {
      lead = many ? AUTO_MERGE_OFF_TEXT_MANY : AUTO_MERGE_OFF_TEXT
    }
    tail = AUTO_MERGE_CHANGE_TEXT
  }

  return (
    <p className={`${className} text-[#5a8aaa] dark:text-gray-500`} data-testid="auto-merge-note">
      {tail ? `${lead} ${tail} ` : `${lead} `}
      {link}.
    </p>
  )
}
