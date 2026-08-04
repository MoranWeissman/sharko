// ManagedSecretsSummaryLine — the System page's one quiet line about
// managed secrets (S1). The maintainer's call: the System page's job is
// how Sharko itself is set up and doing — a resource list of every secret
// doesn't belong bolted onto that, the same way Managed Clusters and
// Addons each get their own page. This line states the one fact that
// matters and links to the full page (/secrets, see ManagedSecrets.tsx).
//
// Reuses the same GET /api/v1/system/managed-secrets call the full page
// makes — a cheap, already-in-memory read (see internal/api/system_managed_secrets.go),
// not a second heavy fetch.

import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getManagedSecrets } from '@/services/api'

export function ManagedSecretsSummaryLine() {
  const [text, setText] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getManagedSecrets()
      .then((res) => {
        if (cancelled) return
        const rows = [...res.cluster_connection_secrets, ...res.addon_values_secrets]
        const total = rows.length
        // "missing" and "out_of_sync" both mean "this secret needs a Sync"
        // — grouped together for one plain count here; the full page shows
        // each state separately.
        const outOfSync = rows.filter((r) => r.state === 'out_of_sync' || r.state === 'missing').length
        if (total === 0) {
          setText('Sharko is not managing any secrets yet.')
          return
        }
        const secretWord = total === 1 ? 'secret' : 'secrets'
        setText(
          outOfSync === 0
            ? `Sharko manages ${total} ${secretWord} — all in sync.`
            : `Sharko manages ${total} ${secretWord} — ${outOfSync} out of sync.`,
        )
      })
      .catch(() => setText(null))
    return () => {
      cancelled = true
    }
  }, [])

  if (!text) return null

  return (
    <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
      {text}{' '}
      <Link
        to="/secrets"
        className="font-medium text-[#1a4a6a] underline-offset-2 hover:underline dark:text-blue-300"
      >
        View Managed Secrets
      </Link>
    </p>
  )
}

export default ManagedSecretsSummaryLine
