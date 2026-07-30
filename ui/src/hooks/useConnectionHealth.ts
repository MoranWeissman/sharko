import { useState, useEffect, useCallback } from 'react'
import { api } from '@/services/api'

export type ConnectionHealthState = 'idle' | 'testing' | 'ok' | 'error'

export interface ConnectionHealth {
  git: ConnectionHealthState
  argocd: ConnectionHealthState
  vault: ConnectionHealthState
  gitMessage?: string
  argocdMessage?: string
  vaultMessage?: string
  loading: boolean
  /** True when at least one of git/argocd/vault is currently failing. */
  anyFailing: boolean
  /** Plain-words messages for every connection currently failing. */
  failingMessages: string[]
  refresh: () => void
}

// useConnectionHealth aggregates the health of the three connections a
// running Sharko install depends on — Git, ArgoCD, and the secrets/
// cluster-credentials provider ("vault" in plain terms) — behind one hook.
// It reuses the same test endpoints the Connections settings page calls
// (POST /connections/test for git+argocd, POST /providers/test for the
// vault/provider), so a failing connection can be surfaced anywhere in the
// app (e.g. cluster Diagnostics) without duplicating the test logic
// (v4-wave2 8.1 — "a failing connection surfaces in diagnostics, not just
// on the connections page").
//
// `enabled` (default true) gates the automatic fetch-on-mount: pass false
// when the caller only wants the health check to run once a section
// actually becomes visible (e.g. a tab), so every page that might one day
// render this hook doesn't fire two extra network requests on every mount.
export function useConnectionHealth(enabled = true): ConnectionHealth {
  const [git, setGit] = useState<ConnectionHealthState>('idle')
  const [argocd, setArgocd] = useState<ConnectionHealthState>('idle')
  const [vault, setVault] = useState<ConnectionHealthState>('idle')
  const [gitMessage, setGitMessage] = useState<string | undefined>()
  const [argocdMessage, setArgocdMessage] = useState<string | undefined>()
  const [vaultMessage, setVaultMessage] = useState<string | undefined>()
  const [loading, setLoading] = useState(enabled)

  const refresh = useCallback(() => {
    setLoading(true)
    setGit('testing')
    setArgocd('testing')
    setVault('testing')

    api
      .testConnection()
      .then((res) => {
        setGit(res.git.status === 'ok' ? 'ok' : 'error')
        setGitMessage(res.git.message)
        setArgocd(res.argocd.status === 'ok' ? 'ok' : 'error')
        setArgocdMessage(res.argocd.message)
      })
      .catch((err: unknown) => {
        setGit('error')
        setArgocd('error')
        const msg = err instanceof Error ? err.message : undefined
        setGitMessage(msg)
        setArgocdMessage(msg)
      })

    api
      .testProvider()
      .then((res) => {
        setVault(res.status === 'connected' || res.status === 'ok' ? 'ok' : 'error')
        setVaultMessage(res.message)
      })
      .catch((err: unknown) => {
        setVault('error')
        setVaultMessage(err instanceof Error ? err.message : undefined)
      })
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    if (!enabled) return
    refresh()
  }, [enabled, refresh])

  const failingMessages: string[] = []
  if (git === 'error') failingMessages.push(gitMessage || "Sharko can't reach your Git host.")
  if (argocd === 'error') failingMessages.push(argocdMessage || "Sharko can't reach ArgoCD.")
  if (vault === 'error') failingMessages.push(vaultMessage || "Sharko can't reach your secrets store.")

  return {
    git,
    argocd,
    vault,
    gitMessage,
    argocdMessage,
    vaultMessage,
    loading,
    anyFailing: failingMessages.length > 0,
    failingMessages,
    refresh,
  }
}
