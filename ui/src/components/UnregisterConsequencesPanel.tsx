/**
 * UnregisterConsequencesPanel — story 6.5, "unregister with your eyes open".
 *
 * Dropped into the top of the remove-cluster confirmation, this fetches and
 * reads out what removing the cluster will actually do: what leaves the
 * repo, what happens to its ArgoCD connection, what is deployed there right
 * now, and which ApplicationSets may react to labels a takeover carried
 * over. It writes nothing.
 *
 * The copy is the server's, verbatim. That is deliberate: the same
 * sentences have to appear for someone using the API or reading the docs,
 * and re-wording them here is how those three drift apart.
 */
import { useEffect, useState } from 'react'
import { AlertTriangle, Info, Loader2 } from 'lucide-react'
import { unregisterConsequences } from '@/services/api'
import type { UnregisterConsequencesResponse } from '@/services/models'

interface Props {
  clusterName: string
  /** Only fetches while the confirmation is actually on screen. */
  open: boolean
}

export function UnregisterConsequencesPanel({ clusterName, open }: Props) {
  const [data, setData] = useState<UnregisterConsequencesResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) {
      setData(null)
      setError(null)
      return
    }
    setLoading(true)
    setError(null)
    unregisterConsequences(clusterName)
      .then(setData)
      .catch((e) => setError(e instanceof Error ? e.message : 'Could not work out what this would do'))
      .finally(() => setLoading(false))
  }, [open, clusterName])

  if (loading) {
    return (
      <div
        role="status"
        className="flex items-center gap-2 rounded-md bg-[#f0f7ff] p-3 text-sm text-[#0a3a5a] dark:bg-gray-900 dark:text-gray-300"
      >
        <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden="true" />
        <span>Working out what this would do…</span>
      </div>
    )
  }

  if (error) {
    // Failing to list the consequences must never read as "there are none".
    return (
      <div
        role="alert"
        data-testid="unregister-consequences-error"
        className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
      >
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <p>
          Sharko could not work out what removing this cluster would do ({error}). That is not the
          same as "nothing" — check what is running on it before you go ahead.
        </p>
      </div>
    )
  }

  if (!data) return null

  return (
    <div className="space-y-2" data-testid="unregister-consequences">
      <p className="text-sm font-medium text-[#0a2a4a] dark:text-gray-200">{data.summary}</p>
      <ul className="space-y-2">
        {data.consequences.map((c) => (
          <li
            key={c.id}
            data-testid={`unregister-consequence-${c.id}`}
            data-severity={c.severity}
            className={`flex gap-2 rounded-md px-3 py-2.5 text-sm ${
              c.severity === 'warning'
                ? 'bg-amber-50 text-amber-900 dark:bg-amber-900/20 dark:text-amber-300'
                : 'bg-[#f0f7ff] text-[#3a6a8a] dark:bg-gray-800 dark:text-gray-400'
            }`}
          >
            {c.severity === 'warning' ? (
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            ) : (
              <Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            )}
            <div>
              <p className="font-medium text-[#0a2a4a] dark:text-gray-200">{c.title}</p>
              <p className="mt-0.5">{c.detail}</p>
              <p className="mt-0.5">{c.what_it_means}</p>
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}
