import { useState, useEffect, useCallback } from 'react'
import { api } from '@/services/api'
import { CONNECTION_FAILURE_MESSAGES, type ConnectionFailureMessageId } from '@/generated/connection-sentences'

// The sentences that tell "AWS answered and turned Sharko's login down",
// "Sharko got no answer from AWS" (ep4 serious 6) and "Sharko has no AWS login
// at all" (ep4 round R, R2) apart. The page shows the generated sentence for
// these ids, so the words live in one place.
export const AWS_LOGIN_MESSAGE_IDS = [
  'connectionFailureVaultAWSLoginRefused',
  'connectionFailureVaultAWSUnreachable',
  'connectionFailureVaultAWSNoLogin',
  'connectionFailureSecretsAWSLoginRefused',
  'connectionFailureSecretsAWSUnreachable',
  'connectionFailureSecretsAWSNoLogin',
] as const satisfies readonly ConnectionFailureMessageId[]

export function isAWSLoginMessageId(id: string | undefined): id is (typeof AWS_LOGIN_MESSAGE_IDS)[number] {
  return id !== undefined && (AWS_LOGIN_MESSAGE_IDS as readonly string[]).includes(id)
}

// The "no answer from AWS" ids. Only these point at Settings → Secrets
// Provider, because that page holds the region and nothing else. It cannot
// change Sharko's AWS login, so a refused login or no login at all does not
// send people there (ep4 round R, R1).
export const AWS_UNREACHABLE_MESSAGE_IDS = [
  'connectionFailureVaultAWSUnreachable',
  'connectionFailureSecretsAWSUnreachable',
] as const satisfies readonly ConnectionFailureMessageId[]

export function isAWSUnreachableMessageId(id: string | undefined): boolean {
  return id !== undefined && (AWS_UNREACHABLE_MESSAGE_IDS as readonly string[]).includes(id)
}

export type ConnectionHealthState = 'idle' | 'testing' | 'ok' | 'error'

export interface ConnectionHealth {
  git: ConnectionHealthState
  argocd: ConnectionHealthState
  vault: ConnectionHealthState
  gitMessage?: string
  argocdMessage?: string
  vaultMessage?: string
  /** The server's id for the secrets store sentence (message_id), when it sent one. */
  vaultMessageId?: string
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
  const [vaultMessageId, setVaultMessageId] = useState<string | undefined>()
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
        setVaultMessageId(res.message_id)
      })
      .catch((err: unknown) => {
        setVault('error')
        setVaultMessage(err instanceof Error ? err.message : undefined)
        setVaultMessageId(undefined)
      })
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    if (!enabled) return
    refresh()
  }, [enabled, refresh])

  // The fallback is only reached when the request itself threw and there is
  // no server message to render at all. Story P2c found the three sentences
  // that used to be typed here had ALREADY drifted: they carried a full stop
  // where the server puts a dash and a next step, so the banner said something
  // no build of Sharko has ever sent. They come from the generated contract
  // now — the server's own wording for "the connection failed and there is no
  // more specific reason", which is exactly this case.
  const failingMessages: string[] = []
  if (git === 'error') failingMessages.push(gitMessage || CONNECTION_FAILURE_MESSAGES.connectionFailureGitUnknown)
  if (argocd === 'error')
    failingMessages.push(argocdMessage || CONNECTION_FAILURE_MESSAGES.connectionFailureArgoCDUnknown)
  if (vault === 'error') {
    // For the AWS sentences, show the generated sentence picked by message_id.
    const vaultSentence = isAWSLoginMessageId(vaultMessageId)
      ? CONNECTION_FAILURE_MESSAGES[vaultMessageId]
      : vaultMessage
    failingMessages.push(vaultSentence || CONNECTION_FAILURE_MESSAGES.connectionFailureSecretsUnknown)
  }

  return {
    git,
    argocd,
    vault,
    gitMessage,
    argocdMessage,
    vaultMessage,
    vaultMessageId,
    loading,
    anyFailing: failingMessages.length > 0,
    failingMessages,
    refresh,
  }
}
