/**
 * V4EnableAddonDialog — enable/disable one addon on one cluster through
 * the v4-format endpoints (POST/DELETE
 * /api/v1/v4/clusters/{name}/addons/{addon} — v4 Wave 1 Story 4.3's
 * "sharpened pipeline"). Distinct flow from the v3 bulk toggle-and-apply
 * card (ClusterDetail's "Manage Addons"): each v4 write is validated
 * BEFORE any branch or PR exists, so this dialog previews one addon at a
 * time rather than staging a batch.
 *
 * State machine: 'form' (enable only — optional JSON values) → preview
 * (dry_run:true) → either 'preview' (files_to_write, via the shared
 * DryRunPreview) or 'problems' (a 422 — the plain-English problems list,
 * shown in place of the preview, never alongside a "here's your PR"
 * message) → 'applying' → 'result' (PRResultBanner with the PR
 * reference). Disable skips the values step and goes straight to
 * preview.
 */
import { useState } from 'react'
import { AlertTriangle, Loader2, Sparkles, Trash2 } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { DryRunPreview } from '@/components/AddAddonFlow'
import { PRResultBanner } from '@/components/PRFeedback'
import {
  enableAddonV4,
  disableAddonV4,
  V4AddonValidationError,
} from '@/services/api'
import type { V4GitResult } from '@/services/models'

type Phase = 'form' | 'previewing' | 'preview' | 'problems' | 'applying' | 'result' | 'error'

export interface V4EnableAddonDialogProps {
  open: boolean
  cluster: string
  addon: string
  mode: 'enable' | 'disable'
  onClose: () => void
  /** Called once the write actually applies (PR opened or auto-merged) — lets the parent refetch. */
  onApplied?: (result: V4GitResult) => void
}

export function V4EnableAddonDialog({
  open,
  cluster,
  addon,
  mode,
  onClose,
  onApplied,
}: V4EnableAddonDialogProps) {
  const [phase, setPhase] = useState<Phase>('form')
  const [valuesText, setValuesText] = useState('')
  const [valuesError, setValuesError] = useState<string | null>(null)
  const [preview, setPreview] = useState<V4GitResult | null>(null)
  const [problems, setProblems] = useState<string[]>([])
  const [result, setResult] = useState<V4GitResult | null>(null)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)

  const reset = () => {
    setPhase('form')
    setValuesText('')
    setValuesError(null)
    setPreview(null)
    setProblems([])
    setResult(null)
    setErrorMessage(null)
  }

  const handleClose = () => {
    reset()
    onClose()
  }

  const parseValues = (): Record<string, unknown> | undefined => {
    if (mode !== 'enable' || !valuesText.trim()) return undefined
    try {
      const parsed = JSON.parse(valuesText)
      setValuesError(null)
      return parsed
    } catch {
      setValuesError('Values must be valid JSON (e.g. {"installCRDs": true}).')
      return undefined
    }
  }

  const runPreview = async () => {
    if (mode === 'enable' && valuesText.trim()) {
      try {
        JSON.parse(valuesText)
      } catch {
        setValuesError('Values must be valid JSON (e.g. {"installCRDs": true}).')
        return
      }
    }
    setPhase('previewing')
    setErrorMessage(null)
    try {
      if (mode === 'enable') {
        const values = parseValues()
        const res = await enableAddonV4(cluster, addon, { dry_run: true, values })
        setPreview(res)
        setPhase('preview')
      } else {
        const res = await disableAddonV4(cluster, addon, { dry_run: true })
        setPreview(res)
        setPhase('preview')
      }
    } catch (e: unknown) {
      if (e instanceof V4AddonValidationError) {
        setProblems(e.problems)
        setPhase('problems')
        return
      }
      setErrorMessage(e instanceof Error ? e.message : 'Failed to generate preview')
      setPhase('error')
    }
  }

  const handleConfirm = async () => {
    setPhase('applying')
    setErrorMessage(null)
    try {
      if (mode === 'enable') {
        const values = parseValues()
        const res = await enableAddonV4(cluster, addon, { yes: true, values })
        setResult(res)
        setPhase('result')
        onApplied?.(res)
      } else {
        const res = await disableAddonV4(cluster, addon, { yes: true })
        setResult(res)
        setPhase('result')
        onApplied?.(res)
      }
    } catch (e: unknown) {
      if (e instanceof V4AddonValidationError) {
        // Shouldn't happen post-preview (preview already validated), but
        // route it through the same problems-first surface if it does.
        setProblems(e.problems)
        setPhase('problems')
        return
      }
      setErrorMessage(e instanceof Error ? e.message : 'Failed to apply change')
      setPhase('error')
    }
  }

  const title = mode === 'enable' ? `Enable ${addon} on ${cluster}` : `Disable ${addon} on ${cluster}`

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose() }}>
      <DialogContent className="max-w-lg bg-[#f0f7ff] dark:bg-gray-800" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle className="text-[#0a2a4a] dark:text-gray-100">{title}</DialogTitle>
        </DialogHeader>

        <p className="text-sm text-[#3a6a8a] dark:text-gray-400">
          {mode === 'enable'
            ? 'This writes clusters/' + cluster + '.yaml (v4 data-file format). Every required value and declared secret is checked before anything is written.'
            : 'The addon entry stays in clusters/' + cluster + '.yaml with enabled: false — re-enabling later is a one-word change.'}
        </p>

        {phase === 'form' && mode === 'enable' && (
          <div className="space-y-2">
            <label className="block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
              Values (optional JSON)
            </label>
            <textarea
              data-testid="v4-enable-values"
              className="w-full min-h-[100px] rounded-md bg-[#e8f4ff] p-2 font-mono text-xs text-[#0a2a4a] ring-2 ring-[#6aade0] focus:ring-teal-500 dark:bg-gray-900 dark:text-gray-200 dark:ring-gray-700"
              placeholder='{"installCRDs": true}'
              value={valuesText}
              onChange={(e) => { setValuesText(e.target.value); setValuesError(null) }}
            />
            {valuesError && (
              <p className="text-sm text-red-600 dark:text-red-400">{valuesError}</p>
            )}
          </div>
        )}

        {phase === 'previewing' && (
          <div className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400">
            <Loader2 className="h-4 w-4 animate-spin" />
            Checking required values and secrets…
          </div>
        )}

        {/* 422 — the problems list, shown BEFORE any PR talk, in place of a preview. */}
        {phase === 'problems' && (
          <div
            data-testid="v4-problems"
            className="rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-700 dark:bg-amber-950/30"
          >
            <p className="flex items-center gap-2 text-sm font-semibold text-amber-800 dark:text-amber-300">
              <AlertTriangle className="h-4 w-4 shrink-0" />
              Sharko can&apos;t enable {addon} on {cluster} yet
            </p>
            <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-amber-800 dark:text-amber-300">
              {problems.map((p, i) => (
                <li key={i}>{p}</li>
              ))}
            </ul>
            <p className="mt-2 text-sm text-amber-700 dark:text-amber-400">
              Nothing was written — no branch, no pull request.
            </p>
          </div>
        )}

        {phase === 'preview' && preview?.dry_run && (
          <DryRunPreview result={preview.dry_run} />
        )}

        {phase === 'applying' && (
          <div className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400">
            <Loader2 className="h-4 w-4 animate-spin" />
            Opening pull request…
          </div>
        )}

        {phase === 'result' && result && (
          <PRResultBanner
            result={result}
            mergedMessage={mode === 'enable' ? 'PR merged — addon enabled' : 'PR merged — addon disabled'}
            openMessage={mode === 'enable' ? 'PR opened — addon enables once it merges' : 'PR opened — addon disables once it merges'}
          />
        )}

        {phase === 'error' && errorMessage && (
          <p className="text-sm text-red-600 dark:text-red-400">{errorMessage}</p>
        )}

        <DialogFooter className="gap-2">
          {(phase === 'form' || phase === 'error') && (
            <button
              type="button"
              onClick={runPreview}
              className="inline-flex items-center gap-1.5 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              {mode === 'enable' ? <Sparkles className="h-3.5 w-3.5" /> : <Trash2 className="h-3.5 w-3.5" />}
              Preview
            </button>
          )}
          {phase === 'preview' && (
            <button
              type="button"
              data-testid="v4-confirm"
              onClick={handleConfirm}
              className="inline-flex items-center gap-1.5 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              Confirm — open PR
            </button>
          )}
          {phase === 'problems' && (
            <button
              type="button"
              onClick={() => setPhase('form')}
              className="rounded-md border border-amber-400 bg-amber-50 px-4 py-2 text-sm font-medium text-amber-800 hover:bg-amber-100 dark:border-amber-600 dark:bg-amber-900/20 dark:text-amber-300"
            >
              Back
            </button>
          )}
          <button
            type="button"
            onClick={handleClose}
            className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            {phase === 'result' ? 'Done' : 'Cancel'}
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
