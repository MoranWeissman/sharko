import { useState, useEffect, useCallback, useContext } from 'react'
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
import { testClusterConnection, adoptClusters } from '@/services/api'
import { AuthContext } from '@/hooks/useAuth'
import type { Cluster, AdoptResult, VerifyResult } from '@/services/models'

interface AdoptClustersDialogProps {
  open: boolean
  onClose: () => void
  clusters: Cluster[]
  onSuccess: () => void
  onDiagnose: (clusterName: string) => void
}

type VerificationState = 'pending' | 'verifying' | 'passed' | 'failed'

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
  const authCtx = useContext(AuthContext)
  const [verifications, setVerifications] = useState<ClusterVerification[]>([])
  const [phase, setPhase] = useState<'verifying' | 'review' | 'adopting' | 'done'>('verifying')
  const [autoMerge, setAutoMerge] = useState(false)
  const [adoptResults, setAdoptResults] = useState<AdoptResult[]>([])
  const [adoptError, setAdoptError] = useState<string | null>(null)

  const isAutoMergeDisabled = authCtx?.role === 'operator' || authCtx?.role === 'viewer'

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
    setAdoptResults([])
    setAdoptError(null)
    setAutoMerge(false)
  }, [open, clusters])

  // Run verifications sequentially when phase is 'verifying'
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
  }, [phase])

  const toggleCluster = useCallback((index: number) => {
    setVerifications((prev) => {
      const next = [...prev]
      next[index] = { ...next[index], selected: !next[index].selected }
      return next
    })
  }, [])

  const selectedClusters = verifications.filter((v) => v.selected)
  const passedCount = verifications.filter((v) => v.state === 'passed').length
  const failedCount = verifications.filter((v) => v.state === 'failed').length

  const handleConfirmAdoption = useCallback(async () => {
    const names = selectedClusters.map((v) => v.cluster.name)
    if (names.length === 0) return
    setPhase('adopting')
    setAdoptError(null)
    try {
      const response = await adoptClusters({
        clusters: names,
        auto_merge: autoMerge,
      })
      setAdoptResults(response.results)
      setPhase('done')
      onSuccess()
    } catch (err) {
      setAdoptError(err instanceof Error ? err.message : 'Adoption failed')
      setPhase('review')
    }
  }, [selectedClusters, autoMerge, onSuccess])

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
                  {(phase === 'review' || phase === 'adopting') && (
                    <th className="px-4 py-2 w-8"></th>
                  )}
                  <th className="px-4 py-2">Cluster</th>
                  <th className="px-4 py-2">Server URL</th>
                  <th className="px-4 py-2">Verification</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700 bg-[#f0f7ff] dark:bg-gray-800">
                {verifications.map((v, idx) => (
                  <tr key={v.cluster.name} className={v.selected ? '' : 'opacity-50'}>
                    {(phase === 'review' || phase === 'adopting') && (
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
                          Passed
                          {v.result?.server_version && (
                            <span className="ml-1 font-mono text-[#3a6a8a] dark:text-gray-500">
                              v{v.result.server_version}
                            </span>
                          )}
                        </span>
                      )}
                      {v.state === 'failed' && (
                        <div className="space-y-1">
                          <span className="inline-flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
                            <XCircle className="h-3 w-3" />
                            Failed
                          </span>
                          <p className="text-xs text-red-500 dark:text-red-400">
                            {v.result?.error_message ?? 'Verification failed'}
                          </p>
                          <button
                            type="button"
                            onClick={() => onDiagnose(v.cluster.name)}
                            className="text-xs font-medium text-blue-600 underline hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300"
                          >
                            Diagnose
                          </button>
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Summary */}
          {phase === 'review' && (
            <div className="flex items-center gap-4 text-sm text-[#2a5a7a] dark:text-gray-400">
              <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                <CheckCircle className="h-4 w-4" />
                {passedCount} passed
              </span>
              {failedCount > 0 && (
                <span className="inline-flex items-center gap-1 text-red-500 dark:text-red-400">
                  <XCircle className="h-4 w-4" />
                  {failedCount} failed
                </span>
              )}
              <span className="ml-auto text-[#3a6a8a] dark:text-gray-500">
                {selectedClusters.length} selected for adoption
              </span>
            </div>
          )}

          {/* Auto-merge option */}
          {(phase === 'review') && (
            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                id="adopt-auto-merge"
                checked={autoMerge}
                disabled={isAutoMergeDisabled}
                onChange={(e) => setAutoMerge(e.target.checked)}
                className="rounded border-[#5a9dd0] dark:border-gray-600 disabled:opacity-50"
              />
              <label
                htmlFor="adopt-auto-merge"
                className={`text-sm font-medium ${isAutoMergeDisabled ? 'text-[#5a8aaa] dark:text-gray-500' : 'text-[#0a3a5a] dark:text-gray-300'}`}
              >
                Merge PR automatically
              </label>
              {isAutoMergeDisabled && (
                <span className="text-xs text-[#5a8aaa] dark:text-gray-500">(admin only)</span>
              )}
            </div>
          )}

          {/* Adoption error */}
          {adoptError && (
            <p className="text-sm text-red-600 dark:text-red-400">{adoptError}</p>
          )}

          {/* Done state — show results */}
          {phase === 'done' && adoptResults.length > 0 && (
            <div className="space-y-2">
              {adoptResults.map((r) => (
                <div
                  key={r.cluster}
                  className={`flex items-center justify-between rounded-md px-3 py-2 text-sm ${
                    r.success
                      ? 'bg-green-50 text-green-800 ring-1 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800'
                      : 'bg-red-50 text-red-800 ring-1 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800'
                  }`}
                >
                  <span className="inline-flex items-center gap-1.5">
                    {r.success ? <CheckCircle className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
                    {r.cluster}
                  </span>
                  {r.success && r.pr_url && (
                    <a
                      href={r.pr_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1 text-xs font-medium underline"
                    >
                      <ExternalLink className="h-3 w-3" />
                      PR
                    </a>
                  )}
                  {r.success && !r.pr_url && (
                    <span className="text-xs">Adopted</span>
                  )}
                  {!r.success && r.error && (
                    <span className="text-xs">{r.error}</span>
                  )}
                </div>
              ))}
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
              Confirm Adoption ({selectedClusters.length})
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
