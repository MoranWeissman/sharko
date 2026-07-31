import { ExternalLink, Info } from 'lucide-react'

/**
 * Marketplace honesty strip (v4 wave 2.5, design decision 5). Sits at the
 * top of the Marketplace so the two commitments are said once, plainly,
 * before anyone browses a single card:
 *
 *   1. Who keeps this list — the Sharko project, in the open. Links to the
 *      curated file itself and the way to propose a change.
 *   2. What "curated" actually means — Sharko checks the entry is correct
 *      (right chart address, sane defaults). It does NOT audit the
 *      software. The safety call is your org's, made at the approval PR.
 *
 * Plain sentences only — no jargon, nothing implying an automated
 * approval or trust bypass exists (there isn't one; approval is the only
 * door into an org's catalog, for every source, forever).
 */
export function MarketplaceHonestyStrip() {
  return (
    <div
      data-testid="marketplace-honesty-strip"
      className="rounded-lg bg-[#f0f7ff] ring-2 ring-[#6aade0] p-4 dark:ring-gray-700 dark:bg-gray-800"
    >
      <div className="flex items-start gap-3">
        <Info className="mt-0.5 h-5 w-5 shrink-0 text-teal-600 dark:text-teal-400" aria-hidden="true" />
        <div className="space-y-2 text-sm text-[#2a5a7a] dark:text-gray-300">
          <p>
            This list is kept by the Sharko project, in the open.{' '}
            <a
              href="https://github.com/MoranWeissman/sharko/blob/main/templates/bootstrap/configuration/addons-catalog.yaml"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-0.5 font-medium text-teal-700 underline hover:no-underline dark:text-teal-400"
            >
              See the curated file <ExternalLink className="h-3 w-3" aria-hidden="true" />
            </a>{' '}
            or{' '}
            <a
              href="https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-0.5 font-medium text-teal-700 underline hover:no-underline dark:text-teal-400"
            >
              propose an addon <ExternalLink className="h-3 w-3" aria-hidden="true" />
            </a>
            .
          </p>
          <p className="font-medium text-[#0a3a5a] dark:text-gray-100">
            Curated means correct, not audited.
          </p>
          <p>
            Sharko checks that each entry is right — the chart address
            works, the defaults are sane. Sharko does not vet the software
            itself. Whether an addon is safe for your org is your call,
            made when you review the pull request that adds it to your
            catalog.
          </p>
        </div>
      </div>
    </div>
  )
}

export default MarketplaceHonestyStrip
