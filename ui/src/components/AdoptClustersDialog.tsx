import { useState, useEffect, useCallback, useMemo } from 'react'
import {
  CheckCircle,
  XCircle,
  AlertTriangle,
  Clock,
  Loader2,
  GitMerge,
  ExternalLink,
  Eye,
} from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { testClusterConnection, adoptClusters, isTestClusterUnavailable, isCredentialLookupFailure, ApiError } from '@/services/api'
import type { Cluster, AdoptResult, AdoptWarning, AdoptBlocker, VerifyResult, DryRunResult } from '@/services/models'
import { adoptWarningsOf, adoptBlockersOf } from '@/lib/adoptFindings'
import { withV } from '@/lib/versionLabel'
import { CHECK_PERMISSIONS_LABEL } from '@/components/ClusterActionHints'
import { DryRunPreview } from '@/components/AddAddonFlow'
import { AutoMergeNote } from '@/components/AutoMergeNote'
import { PRLink } from '@/components/PRFeedback'

interface AdoptClustersDialogProps {
  open: boolean
  onClose: () => void
  clusters: Cluster[]
  onSuccess: () => void
  onDiagnose: (clusterName: string) => void
}

type VerificationState = 'pending' | 'verifying' | 'passed' | 'failed' | 'not-verified'

// Words on the screen, pinned by exact text in AdoptClustersDialog.test.tsx.
// An adopt whose pull request is still open is NOT done: Sharko takes
// ownership only after the merge. So the dialog never says "Adoption
// complete." or "Adopted" for it.
export const ADOPT_WAITING_FOR_MERGE = 'Pull request opened, waiting for merge'
export const ADOPT_DONE_ALL_MERGED = 'Adoption complete.'
export const ADOPT_DONE_WAITING = 'Pull request opened, waiting for merge.'
export const ADOPT_DONE_NOT_ALL = 'Not every cluster finished. Read each one below.'
export const ADOPT_NOT_FINISHED = 'Not finished yet'
export const ADOPT_NEW_WARNINGS =
  'There is something new to read. Tick each warning below, then confirm again.'
// The server answered, but named no cluster at all. Nothing was opened, so
// the dialog must not say a pull request is waiting.
export const ADOPT_NO_RESULTS = 'Nothing was adopted. Sharko sent back no answer for any cluster. Try again.'
// Used when a 409 other_pr_open answer carries no sentence of its own.
export const ADOPT_OTHER_PR_OPEN_FALLBACK = 'A pull request for this cluster is already open. Merge or close it first.'
export function adoptBlockedText(blockedClusters: string[]): string {
  if (blockedClusters.length === 1) {
    return `"${blockedClusters[0]}" cannot be adopted yet. See the reasons below.`
  }
  return `${blockedClusters.length} of the clusters cannot be adopted yet. See the reasons below.`
}

// A warning or blocker, with the cluster it belongs to. The ids can repeat
// from one cluster to the next, so the key is cluster + id. The message is
// part of the key too: two warnings that share an id are still two things
// to read, and each gets its own tick. An empty cluster means the server did
// not say which cluster, so it counts for every selected one.
export interface FindingFor<T> {
  cluster: string
  item: T
}
const findingKey = (cluster: string, id: string, message = '') =>
  `${cluster}\u0000${id}\u0000${message}`
const warningKey = (w: FindingFor<AdoptWarning>) => findingKey(w.cluster, w.item.id, w.item.message)
const blockerKey = (b: FindingFor<AdoptBlocker>) => findingKey(b.cluster, b.item.id, b.item.message)

// Add the findings of `incoming` to `prev`. A cluster named in `replaceFor`
// gets exactly what `incoming` has for it; every other cluster keeps what it
// had. Each finding appears once.
function mergeFindings<T>(
  prev: FindingFor<T>[],
  incoming: FindingFor<T>[],
  keyOf: (f: FindingFor<T>) => string,
  replaceFor?: Set<string>,
): FindingFor<T>[] {
  const kept = replaceFor ? prev.filter((f) => !replaceFor.has(f.cluster)) : prev
  const seen = new Set(kept.map(keyOf))
  const out = [...kept]
  for (const f of incoming) {
    const k = keyOf(f)
    if (seen.has(k)) continue
    seen.add(k)
    out.push(f)
  }
  return out
}

// The warning ids to send with a real adopt. An id goes only when every
// selected cluster has been answered by the server and, on each of them, every
// copy of that warning is ticked (or the cluster does not have it). A warning
// with no cluster counts for every selected cluster.
export function ackIdsFor(
  names: string[],
  warnings: FindingFor<AdoptWarning>[],
  ticked: Set<string>,
  answered: Set<string>,
): string[] {
  if (!names.every((n) => answered.has(n))) {
    // Some cluster's warnings are not known yet: send only ids that no
    // other cluster could share, which is none. The server answers 409 with
    // each cluster's list, and the user reads them first.
    return []
  }
  const ids = new Set<string>()
  for (const w of warnings) {
    if ((w.cluster === '' || names.includes(w.cluster)) && ticked.has(warningKey(w))) ids.add(w.item.id)
  }
  return Array.from(ids).filter((id) =>
    warnings
      .filter((w) => w.item.id === id && (w.cluster === '' || names.includes(w.cluster)))
      .every((w) => ticked.has(warningKey(w))),
  )
}

// How one cluster's adopt ended, read from the server's answer. `merged` is
// true only when the pull request merged and the adopt really finished; a
// missing `merged` reads as false.
type AdoptOutcome = 'done' | 'waiting' | 'partial' | 'failed' | 'skipped'
function adoptOutcomeOf(r: AdoptResult): AdoptOutcome {
  if (r.status === 'failed') return 'failed'
  if (r.status === 'partial') return 'partial'
  if (r.status === 'skipped') return 'skipped'
  return r.merged === true ? 'done' : 'waiting'
}

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
  const [phase, setPhase] = useState<'verifying' | 'review' | 'previewing' | 'preview-ready' | 'adopting' | 'done'>('verifying')
  const [adoptResults, setAdoptResults] = useState<AdoptResult[]>([])
  const [adoptError, setAdoptError] = useState<string | null>(null)
  const [previewResult, setPreviewResult] = useState<DryRunResult | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  // Every reason an adopt cannot go ahead, and every warning the user must
  // tick first. Both come from the server, per cluster.
  const [blockers, setBlockers] = useState<FindingFor<AdoptBlocker>[]>([])
  const [warnings, setWarnings] = useState<FindingFor<AdoptWarning>[]>([])
  const [ticked, setTicked] = useState<Set<string>>(new Set())
  // The clusters the server has given its full list of warnings for. A
  // warning id is sent only when every selected cluster was answered and has
  // that warning ticked (or does not have it). The server takes one id for
  // every cluster in the request, so a tick on one cluster must never accept
  // the same warning on a cluster whose warnings the user has not seen.
  const [answeredClusters, setAnsweredClusters] = useState<Set<string>>(new Set())
  // The link of an open pull request of the other kind (409 other_pr_open).
  const [adoptErrorPR, setAdoptErrorPR] = useState<{ url: string; id?: number } | null>(null)
  // Bumped every time the dialog opens a fresh verification run. The
  // verification effect below keys off THIS instead of `phase` directly:
  // `phase` starts at 'verifying' (its default), so on the very first
  // adopt of a session `setPhase('verifying')` is a same-value no-op and
  // React never treats `[phase]` as "changed" — the verification effect
  // silently never fires. An incrementing id always changes, so it's a
  // reliable trigger regardless of what `phase` happened to be before.
  const [verifyRunId, setVerifyRunId] = useState(0)

  // Start over only when the dialog opens or the set of clusters changes.
  // Keyed by the names, not by the array: a parent that builds a new array
  // on every render must not wipe the ticks and the step the user is on.
  const clustersKey = clusters.map((c) => c.name).join('\u0000')
  const serverURLOf = useMemo(() => {
    const m = new Map<string, string | undefined>()
    for (const c of clusters) m.set(c.name, c.server_url)
    return m
  }, [clusters])

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
    setPreviewResult(null)
    setPreviewError(null)
    setBlockers([])
    setWarnings([])
    setTicked(new Set())
    setAnsweredClusters(new Set())
    setAdoptErrorPR(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, clustersKey])

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
          // cases (test unavailable OR the credential lookup failed) from
          // genuine verification failures. The backend
          // (internal/orchestrator/adopt.go) documents that credentials are
          // OPTIONAL: "A failed credential lookup is the NORMAL case, not a
          // fatal one — skip verification instead of failing the adoption."
          //
          // ASKED OF A TYPED FIELD. This used to be answered by lower-casing
          // the server's error_message and hunting five phrases in it — see
          // isCredentialLookupFailure in services/api for what that cost and
          // why it had stopped working entirely.

          // Case (a): Test unavailable OR the credential lookup failed → informational,
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

          // Case (a) continued: the credential lookup itself failed → also
          // informational, per the credentials-optional contract. Nothing was
          // asked of the cluster, so nothing is known against it.
          if (isCredentialLookupFailure(result)) {
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

  // Only the clusters still selected count. A blocker or a warning of a
  // cluster the user unticked does not hold Confirm back.
  const selectedNames = new Set(selectedClusters.map((v) => v.cluster.name))
  const counts = (cluster: string) => cluster === '' || selectedNames.has(cluster)
  const activeBlockers = blockers.filter((b) => counts(b.cluster))
  const activeWarnings = warnings.filter((w) => counts(w.cluster))

  // Read the warnings and blockers out of an answer and keep them on screen.
  // Each cluster in the answer gets what the answer says for it; the others
  // keep theirs. A warning the user already ticked stays ticked.
  const takeFindings = useCallback((results: AdoptResult[]) => {
    const incomingBlockers: FindingFor<AdoptBlocker>[] = []
    const incomingWarnings: FindingFor<AdoptWarning>[] = []
    for (const r of results) {
      for (const b of adoptBlockersOf(r)) incomingBlockers.push({ cluster: r.name, item: b })
      for (const w of adoptWarningsOf(r)) incomingWarnings.push({ cluster: r.name, item: w })
    }
    const answered = new Set(results.map((r) => r.name))
    setBlockers((prev) => mergeFindings(prev, incomingBlockers, blockerKey, answered))
    setWarnings((prev) => mergeFindings(prev, incomingWarnings, warningKey, answered))
    setAnsweredClusters((prev) => new Set([...prev, ...answered]))
    return { incomingBlockers, incomingWarnings }
  }, [])

  const toggleTick = useCallback((key: string) => {
    setTicked((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const allTicked = activeWarnings.every((w) => ticked.has(warningKey(w)))
  const canConfirm = selectedClusters.length > 0 && allTicked && activeBlockers.length === 0

  const handlePreview = useCallback(async () => {
    const names = selectedClusters.map((v) => v.cluster.name)
    if (names.length === 0) return
    setPhase('previewing')
    setPreviewError(null)
    let answered = false
    try {
      const response = await adoptClusters({
        clusters: names,
        dry_run: true,
      })
      answered = true
      const { incomingBlockers } = takeFindings(response.results)
      // The server puts the preview on the first result's `dry_run` field.
      const first = response.results[0]
      const preview = first?.dry_run ?? first?.preview
      if (!preview || incomingBlockers.length > 0) {
        // Say why. Every reason the server gave is on screen in the review
        // step, each with its cluster; this line names the clusters.
        const blocked = Array.from(new Set(incomingBlockers.map((b) => b.cluster)))
        const failed = response.results.find((r) => r.status === 'failed' && r.error)
        throw new Error(
          blocked.length > 0
            ? adoptBlockedText(blocked)
            : failed?.error ?? 'No preview data returned',
        )
      }
      setPreviewResult(preview)
      setPhase('preview-ready')
    } catch (err) {
      setPreviewError(err instanceof Error ? err.message : 'Preview failed')
      // The server sends blockers only inside each cluster's result (read
      // above). A preview that failed without an answer says nothing about
      // blockers, so the old ones no longer stand.
      if (!answered) {
        setBlockers((prev) => prev.filter((b) => !names.includes(b.cluster)))
      }
      setPhase('review')
    }
  }, [selectedClusters, takeFindings])

  const handleConfirmAdoption = useCallback(async () => {
    const names = selectedClusters.map((v) => v.cluster.name)
    if (names.length === 0) return
    setPhase('adopting')
    setAdoptError(null)
    setAdoptErrorPR(null)
    try {
      // F15: Let the backend handle credentials-optional adoption. Selected
      // clusters include informational-not-verified ones (F14) — the backend
      // (adopt.go:103-139) skips verification when credentials are unavailable
      // and proceeds with adoption anyway.
      // Send the warnings the user ticked. One id covers that warning on
      // every cluster in the request, so an id goes only when it is ticked
      // on EVERY selected cluster that has it, and every selected cluster
      // has been answered by the server. Otherwise the id stays out and the
      // server asks again, naming the clusters still to read.
      const ackIds = ackIdsFor(names, warnings, ticked, answeredClusters)
      const response = await adoptClusters({
        clusters: names,
        acknowledged_findings: ackIds,
      })
      if (!Array.isArray(response.results) || response.results.length === 0) {
        setAdoptResults([])
        setAdoptError(ADOPT_NO_RESULTS)
        setPhase('review')
        return
      }
      setAdoptResults(response.results)
      const { incomingBlockers: nextBlockers } = takeFindings(response.results)

      // F2: Check if ANY result has status === 'failed'. If so, surface the
      // errors and do NOT proceed to done/refetch. The server returns HTTP 200
      // even for all-failed (207 only for mixed), so we must inspect status.
      const anyFailed = response.results.some((r) => r.status === 'failed')
      if (anyFailed || nextBlockers.length > 0) {
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
      // 409: the server found a warning the user has not ticked yet. Show it
      // and let them tick it, instead of a bare error.
      const apiErr = err as { status?: unknown; code?: unknown; body?: unknown } | null
      if (apiErr && apiErr.status === 409 && apiErr.code === 'other_pr_open') {
        // A pull request of the other kind (take over) is already open for
        // this cluster. Show the server's sentence and a link to it.
        const body = (apiErr.body ?? {}) as { pr_url?: unknown; pr_number?: unknown }
        const message = err instanceof Error && err.message ? err.message : ADOPT_OTHER_PR_OPEN_FALLBACK
        setAdoptError(message)
        if (typeof body.pr_url === 'string' && body.pr_url !== '') {
          setAdoptErrorPR({
            url: body.pr_url,
            id: typeof body.pr_number === 'number' && body.pr_number > 0 ? body.pr_number : undefined,
          })
        }
        setPhase('review')
        return
      }
      if (err instanceof ApiError && err.status === 409) {
        const body = err.body as { warnings?: unknown; results?: AdoptResult[] }
        const fromResults = Array.isArray(body.results) ? body.results : []
        const fromBody = adoptWarningsOf({ warnings: body.warnings as AdoptResult['warnings'] })
        const merged: FindingFor<AdoptWarning>[] = []
        for (const r of fromResults) for (const w of adoptWarningsOf(r)) merged.push({ cluster: r.name, item: w })
        if (merged.length === 0) {
          for (const w of fromBody) merged.push({ cluster: names.length === 1 ? names[0] : '', item: w })
        }
        if (merged.length > 0) {
          // The 409 names only the clusters still missing a tick, each with
          // its full list. Those clusters get exactly the new list, so an old
          // copy of a warning whose words changed does not stay next to the
          // new one. Every other cluster keeps its warnings and its ticks.
          const named = new Set(merged.map((w) => w.cluster).filter((c) => c !== ''))
          setWarnings((prev) => mergeFindings(prev, merged, warningKey, named))
          // A cluster the 409 does not name had nothing left to tick for
          // the ids sent, so its warnings are known too.
          setAnsweredClusters((prev) => new Set([...prev, ...names]))
          setAdoptError(ADOPT_NEW_WARNINGS)
          setPhase('review')
          return
        }
      }
      setAdoptError(err instanceof Error ? err.message : 'Adoption failed')
      setPhase('review')
    }
  }, [selectedClusters, onSuccess, warnings, ticked, answeredClusters, takeFindings])

  // The line under the title on the done screen. "Adoption complete." only
  // when every cluster's pull request merged and the adopt really finished.
  const outcomes = adoptResults.map(adoptOutcomeOf)
  const doneText =
    outcomes.length > 0 && outcomes.every((o) => o === 'done')
      ? ADOPT_DONE_ALL_MERGED
      : outcomes.some((o) => o === 'partial' || o === 'failed' || o === 'skipped')
        ? ADOPT_DONE_NOT_ALL
        : ADOPT_DONE_WAITING

  const handleBackToReview = useCallback(() => {
    setPhase('review')
    setPreviewResult(null)
    setPreviewError(null)
  }, [])

  const handleClose = useCallback(() => {
    if (phase === 'adopting' || phase === 'previewing') return // prevent closing during operations
    onClose()
  }, [phase, onClose])

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose() }}>
      <DialogContent className="max-w-2xl max-h-[90%] flex flex-col">
        <DialogHeader className="shrink-0">
          <DialogTitle>Adopt Clusters</DialogTitle>
          <DialogDescription>
            {phase === 'verifying' && 'Verifying cluster connectivity...'}
            {phase === 'review' && 'Review verification results and confirm adoption.'}
            {phase === 'previewing' && 'Generating preview...'}
            {phase === 'preview-ready' && 'Review the changes that will be made.'}
            {phase === 'adopting' && 'Adopting clusters...'}
            {phase === 'done' && doneText}
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 space-y-4 overflow-y-auto py-2">
          {/* What stops an adopt, and what to read first. In the review step
              too, so a failed preview is never silent. */}
          {previewError && phase === 'review' && (
            <div
              role="alert"
              className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
            >
              <XCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
              <p>{previewError}</p>
            </div>
          )}
          {(phase === 'review' || phase === 'preview-ready') && activeBlockers.length > 0 && (
            <div
              role="alert"
              data-testid="adopt-blockers"
              className="rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
            >
              <p className="font-medium">Fix these first:</p>
              <ul className="mt-1 list-disc space-y-1 pl-5">
                {activeBlockers.map((b) => (
                  <li key={blockerKey(b)}>
                    {clusters.length > 1 && b.cluster ? `${b.cluster}: ` : ''}
                    {b.item.message}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {(phase === 'review' || phase === 'preview-ready') && activeWarnings.length > 0 && (
            <div
              data-testid="adopt-warnings"
              className="space-y-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
            >
              <p className="font-medium">Read each of these and tick it to go ahead:</p>
              {activeWarnings.map((w) => {
                const key = warningKey(w)
                return (
                  <label key={key} className="flex items-start gap-2">
                    <input
                      type="checkbox"
                      data-testid={`adopt-warning-${w.item.id}`}
                      data-cluster={w.cluster}
                      checked={ticked.has(key)}
                      onChange={() => toggleTick(key)}
                      className="mt-1 rounded border-amber-500"
                    />
                    <span>
                      {clusters.length > 1 && w.cluster ? <strong>{w.cluster}: </strong> : null}
                      {w.item.message}
                      {w.item.application_sets && w.item.application_sets.length > 0 && (
                        <span className="block text-xs">
                          ApplicationSets: {w.item.application_sets.join(', ')}
                        </span>
                      )}
                      {w.item.applications && w.item.applications.length > 0 && (
                        <span className="block text-xs">
                          Applications: {w.item.applications.join(', ')}
                        </span>
                      )}
                    </span>
                  </label>
                )
              })}
            </div>
          )}

          {/* Preview section */}
          {(phase === 'previewing' || phase === 'preview-ready') && (
            <div className="space-y-3">
              {phase === 'previewing' && (
                <div
                  role="status"
                  className="flex items-center gap-2 rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] p-3 text-sm text-[#0a3a5a] dark:ring-gray-700 dark:bg-gray-900 dark:text-gray-300"
                >
                  <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden="true" />
                  <span>Generating preview...</span>
                </div>
              )}
              {phase === 'preview-ready' && previewResult && (
                <DryRunPreview result={previewResult} />
              )}
              {previewError && (
                <div
                  role="alert"
                  className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
                >
                  <XCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                  <p>{previewError}</p>
                </div>
              )}
            </div>
          )}

          {/* Verification Progress Table */}
          {(phase === 'verifying' || phase === 'review' || phase === 'adopting' || phase === 'done') && (
            <>
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
                            aria-label={`Select ${v.cluster.name}`}
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
                      <td className="px-4 py-2 font-mono text-xs text-[#3a6a8a] dark:text-gray-400 max-w-[200px] truncate" title={serverURLOf.get(v.cluster.name) ?? v.cluster.server_url}>
                        {serverURLOf.get(v.cluster.name) ?? v.cluster.server_url ?? '--'}
                      </td>
                      <td className="px-4 py-2">
                        {v.state === 'pending' && (
                          <span className="text-xs text-[#5a8aaa] dark:text-gray-500">Pending</span>
                        )}
                        {v.state === 'verifying' && (
                          <span className="inline-flex items-center gap-1 text-sm text-blue-600 dark:text-blue-400">
                            <Loader2 className="h-3 w-3 animate-spin" />
                            Verifying...
                          </span>
                        )}
                        {v.state === 'passed' && (
                          <span className="inline-flex items-center gap-1 text-sm text-green-600 dark:text-green-400">
                            <CheckCircle className="h-3 w-3" />
                            Reachable
                            {v.result?.server_version && (
                              <span className="ml-1 font-mono text-[#3a6a8a] dark:text-gray-500">
                                {withV(v.result.server_version)}
                              </span>
                            )}
                          </span>
                        )}
                        {v.state === 'not-verified' && (
                          <span className="inline-flex items-center gap-1 text-sm text-blue-600 dark:text-blue-400">
                            <CheckCircle className="h-3 w-3" />
                            Not verified
                          </span>
                        )}
                        {v.state === 'failed' && (
                          <span className="inline-flex items-center gap-1 text-sm text-red-600 dark:text-red-400">
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

          {/* Auto-merge is a global setting — no per-flow checkbox. The note says what it is set to (story G2). */}
          {(phase === 'review') && (
            <AutoMergeNote className="text-xs" many />
          )}

          {/* Adoption error */}
          {adoptError && (
            <p className="text-sm text-red-600 dark:text-red-400" data-testid="adopt-error">{adoptError}</p>
          )}
          {adoptError && adoptErrorPR && (
            <div data-testid="adopt-error-pr-link">
              <PRLink url={adoptErrorPR.url} id={adoptErrorPR.id} className="text-red-700 dark:text-red-300" />
            </div>
          )}

          {/* Adoption results — shown in 'done' (all succeeded) or 'review' (some/all failed) */}
          {(phase === 'done' || (phase === 'review' && adoptResults.length > 0)) && (
            <div className="space-y-2">
              {adoptResults.map((r) => {
                const outcome = adoptOutcomeOf(r)
                const prURL = r.git?.pr_url
                const tone =
                  outcome === 'done'
                    ? 'bg-green-50 text-green-800 ring-1 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800'
                    : outcome === 'waiting'
                      ? 'bg-sky-50 text-sky-900 ring-1 ring-sky-200 dark:bg-sky-900/20 dark:text-sky-200 dark:ring-sky-800'
                      : outcome === 'failed'
                        ? 'bg-red-50 text-red-800 ring-1 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800'
                        : 'bg-amber-50 text-amber-900 ring-1 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-200 dark:ring-amber-800'
                const icon =
                  outcome === 'done' ? <CheckCircle className="h-4 w-4" />
                    : outcome === 'waiting' ? <Clock className="h-4 w-4" />
                      : outcome === 'failed' ? <XCircle className="h-4 w-4" />
                        : <AlertTriangle className="h-4 w-4" />
                // What the server said about this cluster, in its words.
                const detail = outcome === 'failed' ? r.error : (r.message || r.error)
                return (
                  <div
                    key={r.name}
                    data-testid={`adopt-result-${r.name}`}
                    data-outcome={outcome}
                    className={`rounded-md px-3 py-2 text-sm ${tone}`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="inline-flex items-center gap-1.5">
                        {icon}
                        {r.name}
                      </span>
                      {outcome === 'done' && <span className="text-xs">Adopted</span>}
                      {outcome === 'waiting' && (
                        <span className="text-xs">
                          {ADOPT_WAITING_FOR_MERGE}
                          {prURL && (
                            <>
                              {' '}
                              <a
                                href={prURL}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="inline-flex items-center gap-1 font-medium underline"
                              >
                                <ExternalLink className="h-3 w-3" />
                                View pull request
                              </a>
                            </>
                          )}
                        </span>
                      )}
                      {(outcome === 'partial' || outcome === 'skipped') && (
                        <span className="text-xs">{ADOPT_NOT_FINISHED}</span>
                      )}
                      {outcome === 'done' && prURL && (
                        <a
                          href={prURL}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 text-xs font-medium underline"
                        >
                          <ExternalLink className="h-3 w-3" />
                          PR
                        </a>
                      )}
                    </div>
                    {detail && <p className="mt-1 text-xs">{detail}</p>}
                  </div>
                )
              })}
            </div>
          )}
            </>
          )}
        </div>

        <DialogFooter className="shrink-0 flex-wrap gap-2">
          {phase !== 'done' && (
            <button
              type="button"
              onClick={handleClose}
              disabled={phase === 'adopting' || phase === 'previewing'}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              Cancel
            </button>
          )}
          {phase === 'review' && (
            <>
              <button
                type="button"
                onClick={handlePreview}
                disabled={selectedClusters.length === 0}
                className="inline-flex items-center gap-2 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                <Eye className="h-4 w-4" />
                Preview changes
              </button>
              <button
                type="button"
                onClick={handleConfirmAdoption}
                disabled={!canConfirm}
                className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
              >
                <GitMerge className="h-4 w-4" />
                {/* F16: Don't show count for single-cluster case (implicitly selected) */}
                {clusters.length === 1 ? 'Confirm Adoption' : `Confirm Adoption (${selectedClusters.length})`}
              </button>
            </>
          )}
          {phase === 'previewing' && (
            <button
              type="button"
              disabled
              className="inline-flex items-center gap-2 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
            >
              <Loader2 className="h-4 w-4 animate-spin" />
              Previewing...
            </button>
          )}
          {phase === 'preview-ready' && (
            <>
              <button
                type="button"
                onClick={handleBackToReview}
                className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                Back
              </button>
              <button
                type="button"
                onClick={handleConfirmAdoption}
                disabled={!canConfirm}
                className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
              >
                <GitMerge className="h-4 w-4" />
                {clusters.length === 1 ? 'Confirm Adoption' : `Confirm Adoption (${selectedClusters.length})`}
              </button>
            </>
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
