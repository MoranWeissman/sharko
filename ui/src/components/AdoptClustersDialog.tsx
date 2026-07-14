import { useState, useEffect, useCallback } from 'react'
import {
  CheckCircle,
  XCircle,
  Loader2,
  GitMerge,
  ExternalLink,
} from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { testClusterConnection, adoptClusters, isTestClusterUnavailable } from '@/services/api'
import type { Cluster, AdoptResult, VerifyResult } from '@/services/models'
import { CHECK_PERMISSIONS_LABEL } from '@/components/ClusterActionHints'

interface AdoptClustersDialogProps {
  open: boolean
  onClose: () => void
  clusters: Cluster[]
  onSuccess: () => void
  onDiagnose: (clusterName: string) => void
}

type VerificationState = 'pending' | 'verifying' | 'passed' | 'failed' | 'not-verified'

interface ClusterVerification {
  cluster: Cluster
  state: VerificationState
  result?: VerifyResult & { reachable?: boolean; platform?: string }
  selected: boolean
}

export function AdoptClustersDialog({
  open,
  onClose,
  clusters,
  onSuccess,
  onDiagnose,
}: AdoptClustersDialogProps) {
  const [verifications, setVerifications] = useState<ClusterVerification[]>([])
  const [phase, setPhase] = useState<'verifying' | 'review' | 'adopting' | 'done'>('verifying')
  const [adoptResults, setAdoptResults] = useState<AdoptResult[]>([])
  const [adoptError, setAdoptError] = useState<string | null>(null)
  // Bumped every time the dialog opens a fresh verification run. The
  // verification effect below keys off THIS instead of `phase` directly:
  // `phase` starts at 'verifying' (its default), so on the very first
  // adopt of a session `setPhase('verifying')` is a same-value no-op and
  // React never treats `[phase]` as "changed" — the verification effect
  // silently never fires. An incrementing id always changes, so it's a
  // reliable trigger regardless of what `phase` happened to be before.
  const [verifyRunId, setVerifyRunId] = useState(0)

  // Initialize verifications when dialog opens
  useEffect(() => {
    if (!open || clusters.length === 0) return
    const initial: ClusterVerification[] = clusters.map((c) => ({
      cluster: c,
      state: 'pending',
      selected: true,
    }))
    setVerifications(initial)
    setPhase('verifying')
    setVerifyRunId((n) => n + 1)
    setAdoptResults([])
    setAdoptError(null)
  }, [open, clusters])

  // Run verifications sequentially for each fresh run triggered above.
  useEffect(() => {
    if (phase !== 'verifying') return
    if (verifications.length === 0) return

    let cancelled = false

    async function runVerifications() {
      for (let i = 0; i < verifications.length; i++) {
        if (cancelled) return
        // Mark as verifying
        setVerifications((prev) => {
          const next = [...prev]
          next[i] = { ...next[i], state: 'verifying' }
          return next
        })

        try {
          const result = await testClusterConnection(verifications[i].cluster.name)
          if (cancelled) return

          // F14: Credentials-optional contract — distinguish informational
          // cases (test unavailable OR credentials not found) from genuine
          // verification failures. The backend (adopt.go:103-139) documents
          // that credentials are OPTIONAL: "A failed credential lookup is
          // the NORMAL case, not a fatal one — skip verification instead of
          // failing the adoption."
          const isCredentialsNotFound = (msg: string | undefined): boolean => {
            if (!msg) return false
            const lower = msg.toLowerCase()
            return (
              lower.includes('secret') && lower.includes('not found') ||
              lower.includes('credential') && (lower.includes('not found') || lower.includes('unavailable')) ||
              lower.includes('no credentials available')
            )
          }

          // Case (a): Test unavailable OR credentials not found → informational,
          // not blocking. Keep cluster selected and adoptable.
          if (isTestClusterUnavailable(result)) {
            setVerifications((prev) => {
              const next = [...prev]
              next[i] = {
                ...next[i],
                state: 'not-verified',
                result: {
                  success: false,
                  stage: 'unavailable',
                  error_message: result.error,
                  duration_ms: 0,
                  reachable: false,
                },
                selected: true,  // Keep selected
              }
              return next
            })
            continue
          }

          // Case (a) continued: Credentials not found in error message → also
          // informational, per the credentials-optional contract.
          const errorMsg = result.error_message || ''
          if (!result.success && isCredentialsNotFound(errorMsg)) {
            setVerifications((prev) => {
              const next = [...prev]
              next[i] = {
                ...next[i],
                state: 'not-verified',
                result,
                selected: true,  // Keep selected
              }
              return next
            })
            continue
          }

          // Case (b): Genuine reachable-but-broken verification → real failure.
          const passed = result.reachable !== false && result.success !== false
          setVerifications((prev) => {
            const next = [...prev]
            next[i] = {
              ...next[i],
              state: passed ? 'passed' : 'failed',
              result,
              selected: passed,
            }
            return next
          })
        } catch (err) {
          if (cancelled) return
          setVerifications((prev) => {
            const next = [...prev]
            next[i] = {
              ...next[i],
              state: 'failed',
              result: {
                success: false,
                stage: 'connectivity',
                error_message: err instanceof Error ? err.message : 'Verification failed',
                duration_ms: 0,
                reachable: false,
              },
              selected: false,
            }
            return next
          })
        }
      }
      if (!cancelled) {
        setPhase('review')
      }
    }

    void runVerifications()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [verifyRunId])

  const toggleCluster = useCallback((index: number) => {
    // F16: Single-cluster case (clusters.length === 1) doesn't show checkboxes,
    // so this function won't be called. But as a defensive measure, prevent
    // toggling off the single cluster.
    if (clusters.length === 1) return
    setVerifications((prev) => {
      const next = [...prev]
      next[index] = { ...next[index], selected: !next[index].selected }
      return next
    })
  }, [clusters.length])

  const selectedClusters = verifications.filter((v) => v.selected)
  const passedCount = verifications.filter((v) => v.state === 'passed').length
  const failedCount = verifications.filter((v) => v.state === 'failed').length

  const handleConfirmAdoption = useCallback(async () => {
    const names = selectedClusters.map((v) => v.cluster.name)
    if (names.length === 0) return
    setPhase('adopting')
    setAdoptError(null)
    try {
      // F15: Let the backend handle credentials-optional adoption. Selected
      // clusters include informational-not-verified ones (F14) — the backend
      // (adopt.go:103-139) skips verification when credentials are unavailable
      // and proceeds with adoption anyway.
      const response = await adoptClusters({
        clusters: names,
      })
      setAdoptResults(response.results)

      // F2: Check if ANY result has status === 'failed'. If so, surface the
      // errors and do NOT proceed to done/refetch. The server returns HTTP 200
      // even for all-failed (207 only for mixed), so we must inspect status.
      const anyFailed = response.results.some((r) => r.status === 'failed')
      if (anyFailed) {
        // Show the results (errors included) but stay in 'review' so the user
        // sees what failed. Do NOT call onSuccess() — that refetches and
        // would give false success feedback.
        setPhase('review')
      } else {
        // All results are success|partial — proceed to done.
        setPhase('done')
        onSuccess()
      }
    } catch (err) {
      setAdoptError(err instanceof Error ? err.message : 'Adoption failed')
      setPhase('review')
    }
  }, [selectedClusters, onSuccess])

  const handleClose = useCallback(() => {
    if (phase === 'adopting') return // prevent closing during adoption
    onClose()
  }, [phase, onClose])

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose() }}>
      <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Adopt Clusters</DialogTitle>
          <DialogDescription>
            {phase === 'verifying' && 'Verifying cluster connectivity...'}
            {phase === 'review' && 'Review verification results and confirm adoption.'}
            {phase === 'adopting' && 'Adopting clusters...'}
            {phase === 'done' && 'Adoption complete.'}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          {/* Verification Progress Table */}
          <div className="overflow-x-auto rounded-lg ring-2 ring-[#6aade0] dark:ring-gray-700">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                <tr>
                  {/* F16: Hide checkbox column when clusters.length === 1 (single-pick path) */}
                  {clusters.length > 1 && (phase === 'review' || phase === 'adopting') && (
                    <th className="px-4 py-2 w-8"></th>
                  )}
                  <th className="px-4 py-2">Cluster</th>
                  <th className="px-4 py-2">Server URL</th>
                  <th className="px-4 py-2">Verification</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700 bg-[#f0f7ff] dark:bg-gray-800">
                {verifications.map((v, idx) => (
                  <>
                    <tr key={v.cluster.name} className={v.selected ? '' : 'opacity-50'}>
                      {/* F16: Hide checkbox column when clusters.length === 1 */}
                      {clusters.length > 1 && (phase === 'review' || phase === 'adopting') && (
                        <td className="px-4 py-2">
                          <input
                            type="checkbox"
                            checked={v.selected}
                            disabled={phase === 'adopting'}
                            onChange={() => toggleCluster(idx)}
                            className="rounded border-[#5a9dd0] dark:border-gray-600"
                          />
                        </td>
                      )}
                      <td className="px-4 py-2 font-medium text-[#0a2a4a] dark:text-gray-100">
                        {v.cluster.name}
                      </td>
                      <td className="px-4 py-2 font-mono text-xs text-[#3a6a8a] dark:text-gray-400 max-w-[200px] truncate" title={v.cluster.server_url}>
                        {v.cluster.server_url ?? '--'}
                      </td>
                      <td className="px-4 py-2">
                        {v.state === 'pending' && (
                          <span className="text-xs text-[#5a8aaa] dark:text-gray-500">Pending</span>
                        )}
                        {v.state === 'verifying' && (
                          <span className="inline-flex items-center gap-1 text-xs text-blue-600 dark:text-blue-400">
                            <Loader2 className="h-3 w-3 animate-spin" />
                            Verifying...
                          </span>
                        )}
                        {v.state === 'passed' && (
                          <span className="inline-flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                            <CheckCircle className="h-3 w-3" />
                            Reachable
                            {v.result?.server_version && (
                              <span className="ml-1 font-mono text-[#3a6a8a] dark:text-gray-500">
                                v{v.result.server_version}
                              </span>
                            )}
                          </span>
                        )}
                        {v.state === 'not-verified' && (
                          <span className="inline-flex items-center gap-1 text-xs text-blue-600 dark:text-blue-400">
                            <CheckCircle className="h-3 w-3" />
                            Not verified
                          </span>
                        )}
                        {v.state === 'failed' && (
                          <span className="inline-flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
                            <XCircle className="h-3 w-3" />
                            Unreachable
                          </span>
                        )}
                      </td>
                    </tr>
                    {/* F17: Error message in full-width row below the cluster row */}
                    {(v.state === 'failed' || v.state === 'not-verified') && v.result?.error_message && (
                      <tr key={`${v.cluster.name}-error`} className={v.selected ? '' : 'opacity-50'}>
                        <td colSpan={clusters.length > 1 && (phase === 'review' || phase === 'adopting') ? 4 : 3} className="px-4 py-2 bg-[#e8f4ff] dark:bg-gray-900">
                          <div className="space-y-1">
                            <p className={`text-xs ${v.state === 'failed' ? 'text-red-500 dark:text-red-400' : 'text-[#2a5a7a] dark:text-gray-400'}`}>
                              {v.state === 'not-verified'
                                ? `Not verified — connectivity will be checked when a secret-bearing addon needs it. (${v.result.error_message})`
                                : v.result.error_message
                              }
                            </p>
                            {v.state === 'failed' && (
                              <button
                                type="button"
                                onClick={() => onDiagnose(v.cluster.name)}
                                className="text-xs font-medium text-blue-600 underline hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300"
                              >
                                {CHECK_PERMISSIONS_LABEL}
                              </button>
                            )}
                          </div>
                        </td>
                      </tr>
                    )}
                  </>
                ))}
              </tbody>
            </table>
          </div>

          {/* Summary — connectivity checks during review phase */}
          {phase === 'review' && (
            <div className="flex items-center gap-4 text-sm text-[#2a5a7a] dark:text-gray-400">
              <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                <CheckCircle className="h-4 w-4" />
                {passedCount} reachable
              </span>
              {failedCount > 0 && (
                <span className="inline-flex items-center gap-1 text-red-500 dark:text-red-400">
                  <XCircle className="h-4 w-4" />
                  {failedCount} unreachable
                </span>
              )}
              <span className="ml-auto text-[#3a6a8a] dark:text-gray-500">
                {selectedClusters.length} selected for adoption
              </span>
            </div>
          )}

          {/* Auto-merge is now a global setting — no per-flow checkbox. */}
          {(phase === 'review') && (
            <p className="text-xs text-[#5a8aaa] dark:text-gray-500">
              Auto-merge follows your{' '}
              <a href="/settings?section=gitops" className="underline hover:text-[#0a2a4a] dark:hover:text-gray-300">
                global GitOps setting
              </a>
              .
            </p>
          )}

          {/* Adoption error */}
          {adoptError && (
            <p className="text-sm text-red-600 dark:text-red-400">{adoptError}</p>
          )}

          {/* Adoption results — shown in 'done' (all succeeded) or 'review' (some/all failed) */}
          {(phase === 'done' || (phase === 'review' && adoptResults.length > 0)) && (
            <div className="space-y-2">
              {adoptResults.map((r) => {
                const isSuccess = r.status === 'success' || r.status === 'partial'
                const isFailed = r.status === 'failed'
                return (
                  <div
                    key={r.name}
                    className={`flex items-center justify-between rounded-md px-3 py-2 text-sm ${
                      isSuccess
                        ? 'bg-green-50 text-green-800 ring-1 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800'
                        : 'bg-red-50 text-red-800 ring-1 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800'
                    }`}
                  >
                    <span className="inline-flex items-center gap-1.5">
                      {isSuccess ? <CheckCircle className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
                      {r.name}
                    </span>
                    {isSuccess && r.git?.pr_url && (
                      <a
                        href={r.git.pr_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 text-xs font-medium underline"
                      >
                        <ExternalLink className="h-3 w-3" />
                        PR
                      </a>
                    )}
                    {isSuccess && !r.git?.pr_url && (
                      <span className="text-xs">Adopted</span>
                    )}
                    {isFailed && r.error && (
                      <span className="text-xs">{r.error}</span>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        <DialogFooter className="flex-wrap gap-2">
          {phase !== 'done' && (
            <button
              type="button"
              onClick={handleClose}
              disabled={phase === 'adopting'}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              Cancel
            </button>
          )}
          {phase === 'review' && (
            <button
              type="button"
              onClick={handleConfirmAdoption}
              disabled={selectedClusters.length === 0}
              className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              <GitMerge className="h-4 w-4" />
              {/* F16: Don't show count for single-cluster case (implicitly selected) */}
              {clusters.length === 1 ? 'Confirm Adoption' : `Confirm Adoption (${selectedClusters.length})`}
            </button>
          )}
          {phase === 'adopting' && (
            <button
              type="button"
              disabled
              className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white opacity-50"
            >
              <Loader2 className="h-4 w-4 animate-spin" />
              Adopting...
            </button>
          )}
          {phase === 'done' && (
            <button
              type="button"
              onClick={handleClose}
              className="rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              Close
            </button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
