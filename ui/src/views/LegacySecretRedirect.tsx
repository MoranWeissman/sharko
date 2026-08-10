// LegacySecretRedirect — Secrets-area rename (SN-1). The old detail URL
// /secret-sync/:rowKey keeps working as a redirect to the new per-kind
// detail routes:
//
//   connection-<cluster>            → /secrets/connections/<cluster>
//   values-<cluster>-<addon>        → /secrets/addons/<cluster>/<addon>
//   orphaned-<cluster>-<ns>-<name>  → /secrets/addons/<cluster>/<ns%2Fname>
//   anything else                   → /secrets/connections
//
// A `connection-` key needs no lookup: everything after the prefix IS the
// cluster name. The other two prefixes cannot be split by text alone —
// cluster names, addon names, namespaces and secret names all legally
// contain hyphens (`values-prod-eu-cert-manager` could split two ways) —
// so this reads the same managed-secrets list the pages themselves read
// and matches the whole key exactly. The key formats here mirror
// buildUnifiedRows in ManagedSecrets.tsx, which is where they are built.
//
// A key that matches nothing (a stale link, a secret that's gone) lands on
// the Cluster connections inventory — somewhere real, never a blank page,
// never a crash. Same for a failed fetch.

import { useEffect, useState } from 'react'
import { Navigate, useLocation, useParams } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import { getManagedSecrets } from '@/services/api'

const FALLBACK = '/secrets/connections'

export function LegacySecretDetailRedirect() {
  const { rowKey = '' } = useParams<{ rowKey: string }>()
  const location = useLocation()
  const [resolved, setResolved] = useState<string | null>(null)

  const needsLookup = rowKey.startsWith('values-') || rowKey.startsWith('orphaned-')

  useEffect(() => {
    if (!needsLookup) return
    let cancelled = false
    getManagedSecrets()
      .then((res) => {
        if (cancelled) return
        for (const r of res.addon_values_secrets ?? []) {
          if (`values-${r.cluster}-${r.addon}` === rowKey) {
            setResolved(`/secrets/addons/${encodeURIComponent(r.cluster)}/${encodeURIComponent(r.addon)}`)
            return
          }
        }
        for (const r of res.orphaned_secrets ?? []) {
          if (`orphaned-${r.cluster}-${r.secret_namespace}-${r.secret_name}` === rowKey) {
            setResolved(
              `/secrets/addons/${encodeURIComponent(r.cluster)}/${encodeURIComponent(
                `${r.secret_namespace}/${r.secret_name}`,
              )}`,
            )
            return
          }
        }
        setResolved(FALLBACK)
      })
      .catch(() => {
        if (!cancelled) setResolved(FALLBACK)
      })
    return () => {
      cancelled = true
    }
  }, [rowKey, needsLookup])

  let target: string | null
  if (rowKey.startsWith('connection-')) {
    target = `/secrets/connections/${encodeURIComponent(rowKey.slice('connection-'.length))}`
  } else if (!needsLookup) {
    target = FALLBACK
  } else {
    target = resolved
  }

  if (!target) {
    return (
      <div className="flex items-center justify-center h-full min-h-[200px]">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  // `replace` so browser Back never bounces between the old URL and the
  // new one. Router state (e.g. listSearch from the in-page ?row= compat
  // redirect) rides along so the final page's back link still restores
  // the list it came from.
  return <Navigate to={target} replace state={location.state} />
}

export default LegacySecretDetailRedirect
