// ManagedSecrets — the Secrets area's inventory page (the component/file
// name is not user-visible and deliberately keeps its history).
//
// Secrets-area rename (SN-3): the area has one sidebar item ("Secrets")
// and two real subpages, each with its own URL —
//
//   /secrets/connections  →  <ManagedSecrets area="connections">
//   /secrets/addons       →  <ManagedSecrets area="addons">
//
// The `area` prop scopes this page to one kind of Secret: its own title
// and description, only its own rows, only its own engine in the strip,
// and a links-that-look-like-tabs subnav (SecretsSubnav) between the two.
// Without `area` the page renders the pre-split unified list — that mode
// is unreachable from the app (every old route redirects into the area,
// see App.tsx) and exists so the existing test suites keep exercising the
// exact behavior both subpages share. Everything below the header —
// search, chips, grouping, sort, tiles/table, sessionStorage restore, row
// actions, the leftover-secret Delete flow — is one implementation either
// way.
//
// Before the split this was the page named "Secret Sync" (gitops-proud
// P4-I D1), a dense resource list built to look like ArgoCD's own
// Application list. Every secret Sharko manages — connection secrets AND
// addon-values secrets — was one row in one table: a small kind glyph,
// the identity, a small grey "kind · follows source"
// line under the identity, a cluster (when it isn't already the identity),
// a time, a status dot + word, and a row menu. Click a row to open the
// detail side panel; the row itself never grows.
//
// House-style pass (maintainer's adopted design review, three reviewers
// vs. the ArgoCD screenshot — verdict: "shaped like ArgoCD, painted like
// the opposite of ArgoCD"). Six habits, applied here first and meant to be
// copied by later resource lists:
//
//   H1 — quiet page, near-white content. The page background token and
//        this table's own surface both changed — see ui/src/index.css for
//        the token, and the table wrapper below for the surface.
//   H2 — colour lives on the status DOT only; the status WORD is plain
//        dark ink. See StatusMark.tsx.
//   H3 — one dot shape (filled circle, 4 fills) instead of 4 different
//        icon shapes. See StatusMark.tsx's StatusDot.
//   H4 — hairline dividers, not saturated-blue rings. Global border token,
//        see ui/src/index.css; this file's own boxes (table frame, chips,
//        the old Engines card) follow suit locally.
//   H5 — almost no boxes: the Engines card became the quiet strip below
//        (no ring, no card bg), the table frame is a pale ring + near-white
//        surface instead of a thick blue ring, and the filter chips are
//        pale-ring pills instead of filled blocks.
//   H6 — the row's identity (namespace/secretName) is the darkest, boldest
//        text on the row; everything else — the kind/source subline, the
//        cluster, the time, the status word — is lighter ink.
//
// H1 follow-up (maintainer: "gray doesnt suits the original colors of
// sharko... this is literally copying the colors from argocd one to
// one"): the page background and border tokens went back to a light,
// calm SHARKO blue instead of neutral grey — see ui/src/index.css. And
// a thing we were missing that ArgoCD does have: a thin (3px) status
// colour strip down the left edge of every row, copied from ArgoCD's own
// list and tile views. It reads off the exact same colour table as the
// status dot and the filter chips (StatusMark.tsx's statusStripClassName)
// so the three can never disagree, and it adds no other colour to the
// row — the status word stays plain dark ink (H2 still holds).
//
// Bugs fixed in the same pass:
//
//   B1 — filter-chip counts now follow the search box (searchFiltered),
//        not the chip filter itself — selecting a chip no longer zeroes
//        every other chip's count.
//   B2 — the footer says "Showing 1–20 of 169", never "169 of 169 shown"
//        while 20 rows are on screen; it also says when a filter has
//        narrowed the total.
//   B3 — the active chip filter, the search text, and the selected row are
//        all in the URL (via useSearchParams) — reloadable, bookmarkable,
//        back-button-safe, and the red engine error below can deep-link
//        into a filtered view of its cluster.
//   B4 — selection (the active group-by button, back when paging existed
//        the active page-size button too) no longer reuses the same
//        green/teal StatusMark uses for "in sync" — it's the navy the
//        sidebar already uses, so "selected" and "healthy" can never be
//        confused.
//   I2 (gitops-proud P4-I) — paging (and B2's "Showing 1–20 of 169"
//        footer) is gone. The chips/filters still narrow the rows; the
//        table now scrolls over every one of them under a sticky header
//        instead of breaking them into pages, and the footer states a
//        plain count instead of a range.
//
// S3 HONESTY LOCK (carried forward, non-negotiable): every row states, in
// visible text, what it's compared against — connection secrets read
// "cluster connection · follows git", addon-values secrets read "addon
// values · follows <the real backend name>" (never "the vault" unless the
// configured backend genuinely is Vault — see addon_values_secret_source
// on the API response). Never tooltip-only.
//
// S3/S8 (carried forward, reordered by G3): sorting by state uses a real
// priority order (StatusMark's statusSortRank: missing, out_of_sync,
// foreign, a FAILED check, a never-checked row, in_sync last), never the
// alphabet. A "the last check failed" reason — on BOTH kinds of row since
// P1-B, a connection row's failed check renders unknown, not out_of_sync —
// is a MAPPED, pre-written sentence from the server, never raw error text.
//
// S5 (carried forward): the values-secret Diff makes NO server call, by
// construction — it renders canned sentences from the row's own state
// field. The connection-secret Diff keeps its existing getClusterComparison
// fetch (labels only, never credentials).
//
// ─────────────────────────────────────────────────────────────────────────
// This pass (maintainer's ask: "datadog → cluster name → the secret name
// with namespace, backend type, synced/out of synced… and clicking on it
// should open the resource as it looks inside the cluster right now, read
// only"):
//
//   G1 — every row carries its own backend (row.source, from the server's
//        per-row field) instead of one label for the whole page, so
//        grouping, filtering and sorting by backend all read a real
//        per-row fact.
//   G2 — a "Group by" control: none (the default, today's flat list),
//        addon, or cluster. Grouped view is a collapsible parent line with
//        its rows under it, the same interaction AddonVersionList already
//        uses. Group by addon gives exactly his datadog → cluster →
//        secret picture; group by cluster gives one cluster's whole set,
//        both kinds together.
//   G3 — a group header may only state PLAIN SUMS of the real per-row
//        states ("12 secrets · 9 in sync · 2 out of sync"). Never a
//        rolled-up verdict, never a percentage, and never a group-level
//        "last checked" — different rows were checked at different times.
//   G4 — the detail panel gained a Resource section: the live Secret as it
//        is on the cluster right now, rendered the way ArgoCD renders one.
//        EVERY VALUE IS BLANKED BY THE SERVER — the browser never receives
//        one, and there is no field in the response a value could arrive
//        in. One live read per opened row, on the click that opened it;
//        never during a list render, never on a timer, never fanned out.
//
// ─────────────────────────────────────────────────────────────────────────
// P3-F2 — the panel rebuilt AROUND the resource.
//
// G4's live view arrived as an afterthought: it sat second-to-last in the
// panel, below the Diff; it was OUTSIDE the role guard, so a viewer got a
// permission error where a sentence belonged; it fired a read even for a
// row already known to be missing, which could only ever come back 404;
// and a failed read was a dead end with no way to try again.
//
// The panel now reads top to bottom the way ArgoCD lays out a resource —
// header, state and actions, the diff, the keys, then everything else —
// and the diff itself is two cards with ONE sentence between them: what
// this secret should be, what is actually on the cluster, and how the two
// relate. See SecretDetailContent and diffVerdictFor.
//
// The rule that decided every call in that rebuild, and the one to hold
// on to if it is ever extended: Sharko may describe the DELIVERY, never
// the secret. Where a value comes from, when it was written, which commit
// it was built from, whether the cluster has a key at all — all fine. The
// value, its length, a hash of it, or a per-key "this one matches" verdict
// the engines never actually computed — none of those, ever.
//
// ─────────────────────────────────────────────────────────────────────────
// P4-G — the columns the maintainer asked for, an honest sort order, and
// group-by fixes.
//
//   G1 — ADDON and SOURCE are real columns now, not a fact buried in a
//        subline under the name: both sort, both filter (the two new
//        selects next to Group by), and both are matched by the search
//        box. The S3 honesty lock MOVES here — a connection row's SOURCE
//        cell always reads its real compared-against fact ("git"), a
//        values row's reads the real backend name — still on every row,
//        never tooltip-only. A connection row's ADDON cell is a plain "—"
//        (it isn't an addon secret; see cell-addon below).
//
//   G2 — see internal/api/system_managed_secrets.go's addonValuesSecretSourceLabel:
//        the demo backend now gets its own SOURCE name ("the demo secrets
//        store") instead of the generic "secrets store" fallback, so
//        `make demo-big` has something real to show in the new column.
//
//   G3 — the rank table got more honest: missing first (nothing exists to
//        Sync onto — a harder stop than a wrong value Sync can overwrite),
//        then out_of_sync, then foreign, then a row whose last check
//        actually FAILED, then a row that's simply never been checked,
//        in_sync last. See StatusMark.tsx's statusSortRank for the exact
//        table and the reasoning on where foreign landed.
//
//   G4 — group-by grows up: a group holding any row that isn't in_sync
//        opens itself by default (groupHasIssues) — a click still toggles
//        either way, and that click is remembered per group key for the
//        rest of the session. Groups themselves are now explicitly ordered
//        worst-first-then-by-name (buildRowGroups' own sort), and in addon
//        view the "not an addon" connections bucket always sorts LAST,
//        never mixed in by its own state. Dark-mode "selected" also
//        stopped borrowing StatusMark's teal (it read as "in sync" next to
//        an actually in-sync row) — it's the same navy #1a3d5c the sidebar
//        already uses, in both themes, same as PaginationControls.
//
// ─────────────────────────────────────────────────────────────────────────
// P4-H — the face lane: Sharko blue back in the ink, one-line rows, and the
// full words pass (maintainer's review panel).
//
//   H1 — the nine near-grey values this page had drifted onto its own
//        (never used by any other of the app's 92 files) are gone, mapped
//        onto the SAME blue set every other page already reads off (see
//        frontend-expert.md's palette table): #0a2a4a for the darkest ink,
//        #0a3a5a for buttons/status words, #2a5a7a for body text, #3a6a8a
//        for muted text, #5a8aaa for labels/captions, #6aade0 for
//        borders/rings. The title is the house 24px (text-2xl font-bold)
//        every other list page uses, not the 16px this page had shrunk to.
//        The table frame (ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm,
//        pale-blue thead) now matches AddonListTable's own frame instead of
//        a plain-white, thin-ring look this page had drifted to alone.
//
//   H2 — rows are one line: the grey "kind" subline under the identity is
//        gone (the KeyRound/Lock glyph already says it), STATUS sits right
//        next to NAME (ArgoCD's own habit), the SOURCE column reads
//        "checked against X", the CLUSTER column fills in for both kinds
//        now, and NAME is width-capped (with the full value in a hover
//        title) so a long secret name can't push the rest of the row off
//        screen. Tighter row padding (py-1, was py-1.5) on top of losing a
//        whole text line roughly halved each row's height at the time —
//        SSF-2 (Secret Sync finish pass) loosened it back to py-2: a
//        single-line row that dense read as a data dump, not a row.
//
//   H3 — the full words pass: STATE → STATUS, CHECKED → LAST CHECKED,
//        "Missing" → "Not on the cluster" (StatusMark.tsx), "Refresh all"
//        → "Check all now" (it's been a read-only check since P1-A; the
//        words finally match), "Show:" → "Rows per page:" (Managed
//        Secrets only — PageSizeSelector's default stays "Show:" for the
//        pages that never asked to change), group-by "None" → "Flat
//        list", the search placeholder now mentions namespace (it already
//        matched it), the empty state and the Sync confirm box read as
//        plain sentences instead of log-line fragments, and the long red
//        engine-error line is now one sentence about what clicking it does
//        — the raw server text moved into a click-reachable InfoHint,
//        never lost, never hover-only. The engine strip's cadence sentence
//        (the self-repair promise) is the same: a visible InfoHint now,
//        not a hover-only title attribute.
//
//   I2 (gitops-proud P4-I) — paging is gone. "Rows per page:" above no
//        longer applies to this page — the chips and filters still narrow
//        which rows are in play, but once narrowed, the table shows every
//        one of them and scrolls, with a sticky header, instead of
//        breaking them into pages. Worst-first sort and group-by carry
//        over unchanged; they just no longer feed a page slice.
//
//   H4 — one calm permanent line under the title states what Sharko does
//        here; a grouped child row gets a thin vertical guide line so
//        addon → cluster → secret reads as a tree; and the panel's old
//        single "Last repaired" line became a short list — Sharko's
//        record — of this row's own recent actions (reads the existing
//        GET /api/v1/audit endpoint, scoped to the row's exact resource
//        key; no server change), with an honest scope note that it only
//        covers what's happened since Sharko last started.
//
// ─────────────────────────────────────────────────────────────────────────
// Walk finding #140 — NAMESPACE becomes its own column, and the two status
// words that read least like ArgoCD get renamed.
//
//   The old NAME cell rendered "namespace/name" as one string capped at
//   240px. In the demo estate, several rows share a namespace and differ
//   only in the secret name, so the capped cell truncated exactly where the
//   rows differ ("external-secrets/externa…" three times over) — the one
//   part that told the rows apart was the part that got cut.
//
//   NAMESPACE is now its own sortable column — namespaces repeat and are
//   recognizable even truncated, so it stays narrow with a hover title.
//   NAME shows only the secret's name and takes the room NAMESPACE gave
//   up; its own hover title still carries the full name, and if it still
//   has to truncate at a narrow width, the truncation eats the FRONT of
//   the string (a `direction: rtl` cell, same trick file browsers use for
//   long paths) so the tail — the part that actually tells rows apart —
//   stays visible instead of the prefix.
//
//   "In sync" → "Synced", "Not on the cluster" → "Missing" (StatusMark.tsx)
//   — ArgoCD-professional words, maintainer-locked. See StatusMark.tsx's
//   own note for the reasoning; nothing else in the five-word status table
//   moved, and the plain-English panel sentences are untouched.
//
// ─────────────────────────────────────────────────────────────────────────
// Secret Sync finish pass (SSF-2..5) — calming this one page down: looser
// rows, a real Filters control, and a drawer that says each fact once.
//
//   SSF-2 — rows go back to ~py-2 (was py-1, the H2 one-line tightening) so
//   a row reads as a row again, not a data-dump line. CLUSTER and CHECKED
//   AGAINST both get more room (150px/180px). The table frame and the
//   drawer's own boxes move off the hardcoded ring-2 Sharko-blue and onto
//   the app's own `border-border`/`bg-card` tokens — a thin, theme-aware
//   hairline instead of a thick coloured ring, in both themes, with zero
//   new hex to hand-maintain. Chips/Group-by/List-Tiles keep their strong
//   blue — those are controls, not the page's own background. The intro
//   line is shorter and states the security boundary in the maintainer's
//   own locked words.
//
//   SSF-3 — the Addon and Checked-against <select>s move into one Filters
//   popover (existing shadcn Popover, no new component system). Same two
//   testids, same state, same URL params — only where they render changed.
//
//   SSF-4 — the drawer's wide variant grows to 760px (see
//   ResourceDetailSheet); the identity used to print once in the sheet
//   title and again in Zone A — Zone A no longer repeats it, and the
//   subtitle's kind sentence is gone now that purposeSentence says the same
//   thing once, in full. "Refresh" is "Check now" everywhere on this page
//   (it already meant that — P1-A). Sync is the strong teal action only
//   when there's real drift to push; disabled Sync looks exactly as it did.
//   The comparison heading reads "Diff" for a connection row, "Comparison"
//   for a values row — it was always "Diff" for both, which read as a git
//   claim on a row that's actually compared against a secrets store.
//
//   SSF-5 — a Redacted YAML tab next to the existing view, built from the
//   SAME live read this panel already fires (no second request). Every
//   value in it is the server's own fixed mask — see SecretResource's own
//   contract — this file never has a real value to accidentally print.
//
//   SSF-8 (drawer calm-down, 2026-08-09 PM decision) — the drawer shrinks
//   back to 640px and opens on one plain screen: what this is, is it okay,
//   what it was checked against, when, and what to do about it. Everything
//   else (namespace, kind/type, commit, key list, activity) moves into
//   disclosure sections closed by default. A row that already matches its
//   source no longer opens straight into a two-column comparison box — it
//   shows the one-line result and a "View comparison" control instead;
//   every other verdict still shows the comparison automatically, same as
//   before. The YAML tab is renamed "YAML" (still redacts the same way)
//   and states "Secret values are hidden." up top. "Orphaned"/"Foreign" as
//   status words become "Not in config"/"Managed elsewhere" — the internal
//   state keys are unchanged.
//
//   SSF-9 + SSF-10 (drawer becomes a full page + balanced columns, PM's
//   approved correction after the SSF-8 screenshot review, 2026-08-09) —
//   the side drawer is retired: it held a complete task (state, actions, a
//   two-column comparison, a YAML view) and stayed crowded even at 640px.
//   Clicking a row or tile now navigates to a full page at
//   /secret-sync/<row key> (SecretDetailPage.tsx) instead of opening a
//   sheet over the list — same content, same testids (SecretDetailContent,
//   exported from this file, is what moved), just its own page with a
//   "Back to Secret Sync" link. `?row=<key>` still works as a compat
//   redirect to that page (see the effect near the top of ManagedSecrets).
//   The list's own filters/search/sort/group/view carry over to the
//   detail page as router state and back again via the Back link or
//   browser Back; expanded groups and scroll position ride in
//   sessionStorage, keyed by the list's query string (openRowDetail).
//   SSF-10: the table's NAME column no longer absorbs 100% of any extra
//   width on a wide screen (the bug that made SOURCE/CLUSTER/etc. look
//   pinned in a sea of blank space past 1280px) — every column, NAME
//   included, now carries a percentage width instead of a fixed pixel
//   one, so the whole table grows together.
// ─────────────────────────────────────────────────────────────────────────

import { Fragment, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import {
  CheckCircle,
  ChevronDown,
  ChevronsUpDown,
  ChevronUp,
  Copy,
  Filter,
  KeyRound,
  LayoutGrid,
  List,
  Loader2,
  Lock,
  RefreshCw,
  RotateCcw,
  Search,
  Trash2,
  Wrench,
  X,
} from 'lucide-react'
import {
  api,
  deleteOrphanedSecret,
  fetchAuditLog,
  getAddonValuesSecretResource,
  getConnectionSecretResource,
  getManagedSecrets,
  reconcileCluster,
  refreshAddonValuesSecret,
  resyncClusterLabels,
  syncAddonValuesSecret,
  checkAllAddonValuesSecrets,
  ApiError,
} from '@/services/api'
import type {
  AddonValuesSecretRow,
  AuditEntry,
  ConnectionComparisonView,
  ConnectionRepairView,
  ConnectionSecretRow,
  ManagedSecretsEngineInfo,
  ManagedSecretsResponse,
  OrphanedSecretRow,
  SecretResource,
} from '@/services/models'
import { CREDS_SOURCE_EKS_TOKEN } from '@/services/models'
import { InfoHint } from '@/components/InfoHint'
import { RoleGuard } from '@/components/RoleGuard'
import { AuthContext } from '@/hooks/useAuth'
import { RowActionsMenu, type RowAction } from '@/components/RowActionsMenu'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import { showToast } from '@/components/ToastNotification'
import { StatusDot, StatusMark, statusLabel, statusSortRank, statusStripClassName, toResourceStatus, type ResourceStatus } from '@/components/resource/StatusMark'
import { TimeChip } from '@/components/resource/TimeChip'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { SecretsSubnav } from '@/components/SecretsSubnav'
import { SecretTiles } from './SecretTiles'
import { ConnectionComparisonDisplay } from './ConnectionComparisonDisplay'

// ─────────────────────────────────────────────────────────────────────────────
// Unified row model — one shape for both secret kinds, hoisted at module
// scope so matchesSearch/compareRows/buildUnifiedRows all get a stable
// identity across renders — nothing here is redefined inline in JSX, so
// the page's useMemo calls actually memoize instead of re-running on
// every keystroke.
// ─────────────────────────────────────────────────────────────────────────────

// The six states, worst first (G3, gitops-proud P4-G; leftover-secrets
// S1.2 inserts "orphaned" — kept in lockstep with StatusMark's
// statusSortRank) — the order the filter chips render in and the order a
// group header lists its sums in. Declared here because both the chips
// (far below) and groupSummary (just below) read it.
export const CHIP_ORDER: ResourceStatus[] = ['missing', 'out_of_sync', 'orphaned', 'foreign', 'unknown', 'in_sync']

/**
 * The Delete confirm body (leftover-secrets S1.2, locked wording) — the
 * exact sentence the confirm dialog shows before an operator deletes one
 * orphaned leftover. Names the blast radius (namespace/name, cluster),
 * says plainly this is Sharko's own past write with nothing in git asking
 * for it anymore, and that it cannot be undone.
 */
export function deleteConfirmDescription(row: UnifiedRow | null): string {
  if (!row) return ''
  const ns = row.secretNamespace ?? ''
  const name = row.secretName ?? ''
  return `This permanently deletes secret "${ns}/${name}" from cluster "${row.cluster}". Sharko wrote it once, but nothing in git asks for it anymore. This cannot be undone.`
}

/**
 * The one sentence Sharko says about a secret somebody else created — on the
 * disabled Sync button here, and word-for-word the same sentence the server
 * returns if an API call gets past the button (internal/secrets.
 * ErrForeignSecret). One boundary, one sentence, everywhere.
 */
const FOREIGN_SYNC_REASON = 'Someone else created this one — Sharko will not touch it.'

/**
 * What "Check all now" does, in the user's words (renamed from "Refresh
 * all" in the H3 word pass — it was always a read-only check, since P1-A;
 * this makes the button's own name match). Both engines are checked and
 * neither is written to; the repairs are the engines' own job on their own
 * schedule, which is what makes this safe to press.
 */
const REFRESH_ALL_HINT =
  "Asks Sharko to check every secret against its source — connection secrets against git, addon values against your secrets store. Checking only: nothing is written. The engines' own loops do the repairs."

/** The toast a connection row's Refresh raises. The check is estate-wide by nature, so the sentence says so instead of implying one cluster was singled out. */
function connectionRefreshToast(cluster: string): string {
  return `Checking every cluster's connection secret now, ${cluster} included.`
}

/**
 * P2-D D3: the bar both quiet row warnings below use — three in a row is
 * worth a person's attention. Matches the backend's fightGaugeThreshold
 * (internal/clusterreconciler and internal/secrets), which also drives
 * sharko_reconciler_fights, so the page and the metric agree on what
 * counts as "a fight worth mentioning."
 */
const ROW_WARNING_THRESHOLD = 3

/** The connection-row panel line for D3's fight warning — only shown at ROW_WARNING_THRESHOLD or more. */
function fightWarningSentence(count: number): string {
  return `Something keeps changing this secret back — Sharko has re-applied it ${count} times in a row.`
}

/** The values-row panel line for D3's consecutive-failure warning — only shown at ROW_WARNING_THRESHOLD or more. */
function consecutiveFailuresSentence(count: number): string {
  return `The last ${count} checks of this secret failed in a row.`
}

export interface UnifiedRow {
  kind: 'connection' | 'values'
  key: string
  cluster: string
  addon?: string
  secretNamespace?: string
  secretName?: string
  state: string
  lastChecked?: string
  lastRepaired?: string
  lastRepairedDetail?: string
  lastCheckError?: string
  /**
   * fightCount (connection rows) / consecutiveFailures (values rows) — P2-D
   * D3's two quiet row warnings. Only one is ever set for a given row (a
   * row is one kind or the other); kept as two separate fields rather than
   * one shared "warningCount" so each panel sentence stays specific about
   * what it's counting instead of a generic number nobody can place.
   */
  fightCount?: number
  consecutiveFailures?: number
  /**
   * What this row is compared against, already resolved to display text —
   * the S3 honesty lock, now a PER-ROW fact (G1). It comes straight off
   * the row the server sent (row.source): "git" for a connection secret,
   * the real backend name (or the honest "secrets store" fallback) for a
   * values secret. The page-level addon_values_secret_source is only the
   * fallback for a server that predates the per-row field, and the copy
   * that is genuinely about the whole page.
   */
  sourceLabel: string
  /**
   * (P2-C1) Full branch head commit SHA the pass that produced this row's
   * state read git at. Connection rows only — values rows never get a
   * commit (their intent is the store, not git; see comparedPath's doc
   * comment). Absent when the git provider couldn't say.
   */
  comparedRevision?: string
  /** (P2-C1) The exact managed-clusters file path this row was compared against. Connection rows only. */
  comparedPath?: string
  /** (P2-C1) Full commit SHA the last successful write was built from. Connection rows only. */
  appliedRevision?: string
  /** (P2-C3) Will Sharko fix this row on its own, without a human clicking Sync. */
  selfHeals: boolean
  /** (P2-C6) Which side moved for an out-of-sync connection row: 'git' or 'cluster'. Connection rows only. */
  driftSource?: 'git' | 'cluster'
}

export function buildUnifiedRows(
  connectionRows: ConnectionSecretRow[],
  addonRows: AddonValuesSecretRow[],
  orphanedRows: OrphanedSecretRow[],
  valuesSourceLabel: string,
): UnifiedRow[] {
  const conn: UnifiedRow[] = connectionRows.map((r) => ({
    kind: 'connection',
    key: `connection-${r.cluster}`,
    cluster: r.cluster,
    secretNamespace: r.secret_namespace,
    secretName: r.secret_name,
    state: r.state,
    lastChecked: r.last_checked,
    lastRepaired: r.last_repaired,
    lastRepairedDetail: r.last_repaired_detail,
    // P1-B: connection rows now carry the same "why didn't the last check
    // finish" fact values rows already did — shares the exact rendering
    // below (the panel's lastCheckError paragraph), no per-kind branch.
    lastCheckError: r.last_check_error,
    fightCount: r.fight_count,
    sourceLabel: r.source || 'git',
    comparedRevision: r.compared_revision,
    comparedPath: r.compared_path,
    appliedRevision: r.applied_revision,
    selfHeals: r.self_heals,
    driftSource: r.drift_source,
  }))
  const values: UnifiedRow[] = addonRows.map((r) => ({
    kind: 'values',
    key: `values-${r.cluster}-${r.addon}`,
    cluster: r.cluster,
    addon: r.addon,
    secretNamespace: r.secret_namespace,
    secretName: r.secret_name,
    state: r.state,
    lastChecked: r.last_checked,
    lastRepaired: r.last_repaired,
    lastRepairedDetail: r.last_repaired_detail,
    lastCheckError: r.last_check_error,
    consecutiveFailures: r.consecutive_failures,
    sourceLabel: r.source || valuesSourceLabel,
    selfHeals: r.self_heals,
  }))
  // leftover-secrets S1.2 — orphaned rows fold in as kind 'values' on
  // purpose: they have no live secret Sharko compares against a store (the
  // whole point is the source is gone), but sharing 'values' means
  // group-by-addon already handles an orphan the same way it handles any
  // other addon-shaped row — including the "addon unknown" case, which
  // falls into the same '—' bucket a values row without an addon would.
  const orphaned: UnifiedRow[] = orphanedRows.map((r) => ({
    kind: 'values',
    key: `orphaned-${r.cluster}-${r.secret_namespace}-${r.secret_name}`,
    cluster: r.cluster,
    addon: r.addon,
    secretNamespace: r.secret_namespace,
    secretName: r.secret_name,
    state: r.state,
    lastChecked: r.last_checked,
    // The row's own real source — never the addon-values fallback label,
    // which describes a comparison this row doesn't make.
    sourceLabel: r.source,
    // Sharko never repairs an orphaned row on its own — the whole point of
    // the state is that nothing asks for it anymore.
    selfHeals: false,
  }))
  return [...conn, ...values, ...orphaned]
}

// ─────────────────────────────────────────────────────────────────────────────
// Grouping (G2/G3)
// ─────────────────────────────────────────────────────────────────────────────

export type GroupBy = 'none' | 'addon' | 'cluster'

export interface RowGroup {
  key: string
  label: string
  /** The small grey line under a group's name — what KIND of thing it is. */
  sublabel: string
  rows: UnifiedRow[]
}

/** The bucket connection secrets land in when grouping by addon: they are
 *  not an addon, and pretending otherwise would be the kind of tidy lie
 *  this page keeps refusing to tell. */
export const CONNECTIONS_GROUP_KEY = '__connections__'

/**
 * worstRankInGroup (G4) is the best (lowest = worst) statusSortRank among a
 * group's own rows — what "this group's worst problem" means for ordering
 * purposes. A group with an out-of-sync row in it is a worse group than one
 * that's all in-sync, even if the in-sync one happens to have more rows.
 */
function worstRankInGroup(rows: UnifiedRow[]): number {
  let best = Infinity
  for (const r of rows) {
    const rank = statusSortRank(r.state, !!r.lastCheckError)
    if (rank < best) best = rank
  }
  return best
}

/**
 * worstStateInGroup — same worst-of-the-group rule as worstRankInGroup, but
 * returns the STATE the winning rank belongs to, not just the number.
 * Kept as a public export (secret tiles v2 no longer calls it directly —
 * each box now paints its OWN state's strip, not a group-level one — but
 * the rank-to-state lookup stays useful wherever "what's the worst thing
 * in this group" needs a real ResourceStatus, not just a rank). Empty
 * groups default to 'in_sync' — the state that paints nothing (no group in
 * this page is ever actually empty, but a function that returns a real
 * ResourceStatus for every input is safer than one that can't).
 */
export function worstStateInGroup(rows: UnifiedRow[]): ResourceStatus {
  let best = Infinity
  let worst: ResourceStatus = 'in_sync'
  for (const r of rows) {
    const rank = statusSortRank(r.state, !!r.lastCheckError)
    if (rank < best) {
      best = rank
      worst = toResourceStatus(r.state)
    }
  }
  return worst
}

/**
 * groupHasIssues (G4) is the exact rule that decides whether a group opens
 * itself by default: does ANY row in it fail to be in_sync. Foreign,
 * missing, out_of_sync, and both "unknown" flavors all count — in_sync is
 * the one state that means "nothing here is worth a look."
 */
export function groupHasIssues(group: RowGroup): boolean {
  return group.rows.some((r) => toResourceStatus(r.state) !== 'in_sync')
}

/**
 * buildRowGroups splits already-sorted rows into groups, then orders the
 * GROUPS themselves worst-first, tie-broken by label (G4) — explicit,
 * rather than relying on "whichever group's first row happened to sort
 * highest", so the rule is the same one worstRankInGroup states and nothing
 * about row-arrival order can silently change it. In addon view only, the
 * "not an addon" connections bucket sorts LAST regardless of its own
 * state — it is never really competing with the addons for attention, it's
 * the honest leftover bucket, and burying it under an in-sync addon while a
 * failing connection secret sat at the top would undercut the whole point
 * of calling it out separately.
 */
export function buildRowGroups(rows: UnifiedRow[], groupBy: GroupBy): RowGroup[] {
  if (groupBy === 'none') return []
  const order: string[] = []
  const byKey = new Map<string, RowGroup>()

  for (const row of rows) {
    let key: string
    let label: string
    let sublabel: string
    if (groupBy === 'cluster') {
      key = `cluster-${row.cluster}`
      label = row.cluster
      sublabel = 'cluster'
    } else if (row.kind === 'values') {
      key = `addon-${row.addon}`
      label = row.addon ?? '—'
      sublabel = 'addon'
    } else {
      key = CONNECTIONS_GROUP_KEY
      label = 'Cluster connections'
      sublabel = 'not an addon — one secret per cluster'
    }

    let group = byKey.get(key)
    if (!group) {
      group = { key, label, sublabel, rows: [] }
      byKey.set(key, group)
      order.push(key)
    }
    group.rows.push(row)
  }

  const groups = order.map((k) => byKey.get(k)!)
  groups.sort((a, b) => {
    if (groupBy === 'addon') {
      const aConn = a.key === CONNECTIONS_GROUP_KEY
      const bConn = b.key === CONNECTIONS_GROUP_KEY
      if (aConn !== bConn) return aConn ? 1 : -1
    }
    const rankA = worstRankInGroup(a.rows)
    const rankB = worstRankInGroup(b.rows)
    if (rankA !== rankB) return rankA - rankB
    return a.label < b.label ? -1 : a.label > b.label ? 1 : 0
  })
  return groups
}

/**
 * groupSummary is the ONLY thing a group header is allowed to say about
 * its rows (G3): plain sums of the real per-row states, in the page's own
 * worst-first order, and only the states that are actually present.
 *
 * Deliberately absent, and not to be added later:
 *   - a rolled-up verdict ("healthy", "all good") — a group is not a
 *     thing that has a state; its rows are.
 *   - a percentage — it hides how many rows it is over.
 *   - a group-level "last checked" — different rows were checked at
 *     different times, so any single time here would be wrong for most of
 *     them. If one is ever genuinely wanted it must be the OLDEST, and it
 *     must say that in words.
 */
export function groupSummary(rows: UnifiedRow[]): string {
  const counts: Record<ResourceStatus, number> = { in_sync: 0, out_of_sync: 0, missing: 0, orphaned: 0, foreign: 0, unknown: 0 }
  for (const r of rows) counts[toResourceStatus(r.state)]++
  const parts = [`${rows.length} secret${rows.length === 1 ? '' : 's'}`]
  for (const status of CHIP_ORDER) {
    if (counts[status] > 0) parts.push(`${counts[status]} ${statusLabel(status).toLowerCase()}`)
  }
  return parts.join(' · ')
}

function matchesSearch(row: UnifiedRow, q: string): boolean {
  return (
    row.cluster.toLowerCase().includes(q) ||
    (row.addon ?? '').toLowerCase().includes(q) ||
    (row.secretName ?? '').toLowerCase().includes(q) ||
    (row.secretNamespace ?? '').toLowerCase().includes(q) ||
    // G1: the SOURCE column is a real, matched fact too.
    row.sourceLabel.toLowerCase().includes(q)
  )
}

type SortKey = 'name' | 'namespace' | 'addon' | 'cluster' | 'source' | 'state'

/**
 * sourceShortLabel (design-secret-sync-visual-pass, section 2) — the SOURCE
 * column used to repeat the sentence "compared with X" on every one of
 * 170 rows (~137px of pure waste per row, measured). The relation
 * ("compared with" — SSF-8 item 3, was "checked against") now lives once
 * in the sticky column header ("Compared with"); this cell shows just the
 * place name. Strips a leading "the " so "the demo secrets store" reads as
 * "demo secrets store" — display-only, never touches the server string
 * itself (the Source/"Compared with" filter <select> and the search box
 * still match on the full sourceLabel, only the visible option text runs
 * through this).
 */
const sourceShortLabel = (l: string) => l.replace(/^the /i, '')

// compareRows never sorts state alphabetically (S3) — it defers to
// StatusMark's statusSortRank, the same worst-first priority order ArgoCD
// uses (reordered for G3 — see StatusMark.tsx), so a click on the State
// header surfaces problems instead of burying "missing" between "in_sync"
// and "out_of_sync". The state comparison also passes whether EACH row's
// last check failed, so two "unknown" rows sort the failed one first —
// same status word, different rank.
//
// name/namespace sort independently of each other (walk finding #140) —
// each is its own column now, so each sorts on its own field instead of the
// old combined "namespace/name" string.
function compareRows(a: UnifiedRow, b: UnifiedRow, key: SortKey): number {
  switch (key) {
    case 'name': {
      const an = a.secretName ?? ''
      const bn = b.secretName ?? ''
      return an < bn ? -1 : an > bn ? 1 : 0
    }
    case 'namespace': {
      const an = a.secretNamespace ?? ''
      const bn = b.secretNamespace ?? ''
      return an < bn ? -1 : an > bn ? 1 : 0
    }
    case 'addon': {
      const aa = a.addon ?? ''
      const ba = b.addon ?? ''
      return aa < ba ? -1 : aa > ba ? 1 : 0
    }
    case 'cluster':
      return a.cluster < b.cluster ? -1 : a.cluster > b.cluster ? 1 : 0
    case 'source':
      return a.sourceLabel < b.sourceLabel ? -1 : a.sourceLabel > b.sourceLabel ? 1 : 0
    case 'state':
      return statusSortRank(a.state, !!a.lastCheckError) - statusSortRank(b.state, !!b.lastCheckError)
    default:
      return 0
  }
}

// syncGateFor is the one place that decides whether Sync makes sense for a
// row right now, and why not when it doesn't — shared by the row's ⋯ menu
// and the detail panel's own Sync button so the two never disagree.
export function syncGateFor(row: UnifiedRow): { disabled: boolean; reason?: string } {
  // leftover-secrets S1.2: an orphaned row has no source left to sync
  // FROM — Delete is the only action it offers. actionsForRow and the
  // panel both branch on row.state === 'orphaned' before ever reaching
  // this gate, but it stays a safe "no" here too rather than falling
  // through to a kind-based default that would say otherwise.
  if (row.state === 'orphaned') return { disabled: true, reason: "This secret's source isn't in config anymore — Delete is the only action." }
  // Checked first, and for both kinds of row: a secret Sharko did not create
  // is never Sharko's to write, whatever else is true about it (P1-A).
  if (row.state === 'foreign') return { disabled: true, reason: FOREIGN_SYNC_REASON }
  if (row.kind === 'connection') {
    if (row.state === 'in_sync') return { disabled: true, reason: 'Nothing to apply — this secret already matches git.' }
    if (row.state === 'missing')
      return { disabled: true, reason: "This secret hasn't been created yet — there's nothing to sync onto." }
    if (row.state === 'unknown') return { disabled: true, reason: 'Click Check now first to check this secret.' }
    return { disabled: false }
  }
  if (row.state === 'in_sync') return { disabled: true, reason: 'Nothing to push — this secret already matches its source.' }
  if (row.state === 'unknown') return { disabled: true, reason: 'Click Check now first to check this secret.' }
  return { disabled: false }
}

/**
 * Honest-labels epic (HL-1): the one place the action's NAME comes from,
 * shared by the row's ⋯ menu, the detail panel's button, and both confirm
 * boxes so they can never disagree. A connection row's action calls
 * resyncClusterLabels, which re-applies ONLY Sharko's own addon label keys —
 * it does not rebuild config, server or name — so calling it "Sync" promised
 * more than it does. An addon Secret's value really is delivered from its
 * backend, so that one keeps "Sync". The word "Repair" is reserved for a
 * later action that would genuinely rebuild the connection Secret; nothing
 * here may use it.
 */
export function syncActionLabel(kind: 'connection' | 'values'): string {
  return kind === 'connection' ? 'Re-apply addon labels' : 'Sync'
}

/** HL-1: the confirm button's shorter form of the same name — "Re-apply labels" fits a button; the addon side stays "Sync". */
export function syncConfirmButtonText(kind: 'connection' | 'values' | undefined): string {
  return kind === 'connection' ? 'Re-apply labels' : 'Sync'
}

/**
 * The Sync confirm box's description (H3 word pass, gitops-proud P4-H) —
 * used to read like two clauses stitched together with an em dash, closer
 * to a log line than a sentence someone would say out loud. It now opens
 * with the one fact that matters most before anyone clicks the button —
 * this writes, right now, and there is no pull request to review first —
 * then says what gets written, in one more plain sentence.
 */
export function syncConfirmDescription(row: UnifiedRow | null): string {
  if (!row) return ''
  const opening = `This writes the secret on cluster "${row.cluster}" now. No pull request.`
  if (row.kind === 'connection') {
    return `${opening} It copies git's addon labels onto the cluster's ArgoCD secret. The self-heal setting doesn't change.`
  }
  return `${opening} It pushes the current value from ${row.sourceLabel} onto the cluster. If the secret doesn't exist yet, this creates it.`
}

function actionsForRow(
  row: UnifiedRow,
  opts: { busy: boolean; onRefresh: () => void; onRequestSync: () => void; onRequestDelete: () => void },
): RowAction[] {
  // leftover-secrets S1.2: an orphaned row gets exactly one action —
  // Delete. No Check now (there is nothing left to check it against), no
  // Sync (syncGateFor above already refuses it). Destructive, so it
  // renders red and grouped below any safe action (RowActionsMenu's own
  // convention — see its `destructive` handling).
  if (row.state === 'orphaned') {
    return [
      {
        label: 'Delete',
        icon: <Trash2 className="h-3.5 w-3.5" />,
        onSelect: opts.onRequestDelete,
        destructive: true,
      },
    ]
  }
  const gate = syncGateFor(row)
  return [
    {
      // SSF-4 (Secret Sync finish pass): "Refresh" → "Check now" — this
      // has been a read-only check since P1-A, "Check all now" above the
      // table already says so; the row-level action now matches it.
      label: 'Check now',
      icon: <RefreshCw className="h-3.5 w-3.5" />,
      onSelect: opts.onRefresh,
      loading: opts.busy,
    },
    {
      // HL-1: per kind — a connection row's action only re-applies Sharko's
      // own addon labels, and its name says so. See syncActionLabel.
      label: syncActionLabel(row.kind),
      icon: <RotateCcw className="h-3.5 w-3.5" />,
      onSelect: opts.onRequestSync,
      disabled: gate.disabled,
      disabledReason: gate.reason,
    },
  ]
}

// ─────────────────────────────────────────────────────────────────────────────
// Filter chips — plain sums of the real per-row states (B1: computed over
// the SEARCH-filtered rows, never over the chip filter itself — that's
// what kept every chip's count honest even while one of them is active).
// An "All" chip is the only way back to everything once a chip is chosen.
// H5: pale-ring pills, never filled blocks. B4: the active chip uses the
// navy "selected" ink, never the green/amber "in sync"/"out of sync"
// status colours — active is a fact about the UI, not about a secret.
// ─────────────────────────────────────────────────────────────────────────────

function FilterChip({
  status,
  label,
  count,
  active,
  onClick,
}: {
  status: ResourceStatus | 'all'
  label: string
  count: number
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      data-testid={`filter-chip-${status}`}
      className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ring-1 transition-colors ${
        active
          ? // G4: the house navy in both themes (a lighter tint in dark
            // mode for contrast against a dark surface — same family the
            // app already uses for navy elsewhere, e.g. FirstRunWizard's
            // icons), never StatusMark's teal — "selected" is a fact about
            // the UI, not a claim that this state is healthy.
            'text-[#1a3d5c] underline decoration-2 underline-offset-4 ring-[#1a3d5c] dark:text-blue-400 dark:ring-blue-400'
          : 'text-[#2a5a7a] ring-[#6aade0] hover:ring-[#5a9dd0] dark:text-gray-400 dark:ring-gray-700 dark:hover:ring-gray-600'
      }`}
    >
      {status !== 'all' && <StatusDot status={status} />}
      {label}
      <span className={active ? 'font-semibold' : 'text-[#5a8aaa] dark:text-gray-500'}>{count}</span>
    </button>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Sortable column header — H5: lighter ink, reads as a label, not data.
// ─────────────────────────────────────────────────────────────────────────────

function SortableTh({
  label,
  sortKeyName,
  activeKey,
  dir,
  onSort,
  className,
}: {
  label: string
  sortKeyName: SortKey
  activeKey: SortKey
  dir: 'asc' | 'desc'
  onSort: (key: SortKey) => void
  className?: string
}) {
  const active = activeKey === sortKeyName
  return (
    <TableHead className={className}>
      <button
        type="button"
        onClick={() => onSort(sortKeyName)}
        data-testid={`sort-${sortKeyName}`}
        className="inline-flex items-center gap-1 text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] hover:text-teal-700 dark:text-gray-500 dark:hover:text-teal-400"
      >
        {label}
        {active ? (
          dir === 'asc' ? (
            <ChevronUp className="h-3 w-3" aria-hidden="true" />
          ) : (
            <ChevronDown className="h-3 w-3" aria-hidden="true" />
          )
        ) : (
          <ChevronsUpDown className="h-3 w-3 opacity-30" aria-hidden="true" />
        )}
      </button>
    </TableHead>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Panel action button — Refresh / Sync inside the detail panel. The info
// hint renders ONLY when the button is genuinely disabled.
// ─────────────────────────────────────────────────────────────────────────────

function PanelActionButton({
  onClick,
  disabled,
  loading,
  icon: Icon,
  label,
  reason,
  testId,
  destructive,
  strong,
}: {
  onClick: () => void
  disabled?: boolean
  loading?: boolean
  icon: typeof RefreshCw
  label: string
  reason?: string
  testId?: string
  /** leftover-secrets S1.2 — the panel's Delete button reads red, matching ConfirmationModal's own destructive styling, never the neutral Check now/Sync look. */
  destructive?: boolean
  /**
   * SSF-4 (Secret Sync finish pass) — Sync's own strong variant: the app's
   * house teal CTA (the same `bg-teal-600` every other "do the thing" button
   * in Sharko uses — see AddonCatalog's Add Addon button), used ONLY when
   * there's real drift to push. Ignored while `disabled` — a quiet,
   * disabled Sync looks exactly as it always has, so "in sync" never reads
   * as "here's a button, go press it."
   */
  strong?: boolean
}) {
  const isStrong = strong && !disabled
  return (
    <span className="inline-flex items-center gap-1">
      <button
        type="button"
        onClick={onClick}
        disabled={disabled || loading}
        data-testid={testId}
        className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-50 ${
          destructive
            ? 'border-red-300 bg-white text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-700 dark:text-red-400 dark:hover:bg-red-950'
            : isStrong
              ? 'border-transparent bg-teal-600 text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600'
              : 'border-[#6aade0] bg-white text-[#0a3a5a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600'
        }`}
      >
        {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Icon className="h-3.5 w-3.5" />}
        {label}
      </button>
      {disabled && reason && <InfoHint text={reason} label={`Why is ${label} unavailable?`} />}
    </span>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Engines quiet strip (H5 + cull) — the old boxed "Engines" card is gone:
// no ring, no card fill, just plain labels with the plain fact under each,
// separated by a thin vertical hairline, the same shape as ArgoCD's own top
// status strip. "Engines" is our internal machinery, not a user-facing
// word, so the strip is labelled by what the user is actually looking at.
//
// H3 word pass (gitops-proud P4-H): the label used to be forced into all
// caps (an "ENGINES" habit this page was trying to leave behind) — it's
// sentence case now, same as every other label on the page. The cadence
// sentence ("re-checked every 30 seconds…") used to live ONLY in a hover
// title — unreachable by touch or keyboard — so it's a visible InfoHint
// now, same affordance the row's own disabled-reason hints already use. And
// the value line reads as a sentence ("Sharko last ran a check…") instead
// of a bare fragment. The cadence is still built from the server's own
// interval_seconds, not a hardcoded string, so a config change can never
// leave the page stating a cadence that isn't true anymore.
// ─────────────────────────────────────────────────────────────────────────────

function humanizeInterval(seconds?: number): string {
  if (!seconds || seconds <= 0) return ''
  if (seconds % 60 === 0) {
    const m = seconds / 60
    return `${m} minute${m === 1 ? '' : 's'}`
  }
  return `${seconds} second${seconds === 1 ? '' : 's'}`
}

function cadenceSentence(kind: 'connection' | 'values', intervalSeconds?: number): string {
  const human = humanizeInterval(intervalSeconds)
  if (!human) return ''
  return kind === 'connection' ? `Sharko re-checks it every ${human}, and right after each merge.` : `Sharko checks it every ${human} and repairs it automatically.`
}

function EngineStat({
  label,
  kind,
  info,
  onErrorClick,
}: {
  label: string
  kind: 'connection' | 'values'
  info?: ManagedSecretsEngineInfo
  onErrorClick?: (cluster: string) => void
}) {
  const cadence = cadenceSentence(kind, info?.interval_seconds)
  // gitops-proud P4-I (D2) — the values engine's off switch. `enabled` is
  // only ever false for the addon-values engine (the connection engine has
  // no switch and always reports true) — checked wired-and-not-enabled so
  // "not running on this server at all" and "running, but an admin turned
  // it off" stay two different, honest sentences.
  const switchedOff = info?.wired && info.enabled === false
  return (
    <div className="px-5 py-1 first:pl-0">
      <div className="text-xs font-medium text-[#5a8aaa] dark:text-gray-500">{label}</div>
      {switchedOff ? (
        <div className="mt-0.5 text-sm text-[#5a8aaa] dark:text-gray-500" data-testid={`engine-off-${kind}`}>
          Addon values engine is switched off. Refresh and Sync still work row by row.
        </div>
      ) : info?.wired ? (
        <div className="mt-0.5 flex items-center gap-1 text-sm text-[#0a3a5a] dark:text-gray-200">
          <span>
            Sharko last ran a check <TimeChip iso={info.last_run} />.
          </span>
          {cadence && <InfoHint text={cadence} label={`How often does Sharko check ${label.toLowerCase()}?`} />}
        </div>
      ) : (
        <div className="mt-0.5 text-sm text-[#5a8aaa] dark:text-gray-500">Not running on this server.</div>
      )}
      {/* H3: the raw server error used to run inline as one long red
          sentence. This line now says only what clicking it does — the
          real error text (which can be long and technical, and that's
          fine; error text is allowed to carry debug detail) moved into the
          InfoHint next to it, reachable by click or keyboard, not just a
          hover. Suppressed while switched off — a stale error from before
          the engine was turned off is not something clicking it can act
          on, and the off sentence above is the one fact that matters now. */}
      {!switchedOff && info?.last_error && (
        <div className="mt-1 flex flex-wrap items-center gap-1 text-xs" data-testid={`engine-error-${kind}`}>
          {info.last_error_cluster && onErrorClick ? (
            <button
              type="button"
              onClick={() => onErrorClick(info.last_error_cluster!)}
              className="text-left text-red-700 hover:underline dark:text-red-400"
            >
              A check failed on <span className="font-medium">{info.last_error_cluster}</span> <TimeChip iso={info.last_error_at} /> — click to see it.
            </button>
          ) : (
            <span className="text-red-700 dark:text-red-400">A check failed.</span>
          )}
          <InfoHint text={info.last_error} label="What was the error?" />
        </div>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The live read (P3-F2) — one hook, so the Comparison zone and the YAML tab
// below it read the SAME fetch instead of firing two.
//
// EVERY VALUE HERE WAS BLANKED BY THE SERVER. The response carries key
// NAMES paired with a fixed mask the server put in; there is no field a
// real value could arrive in, and nothing below un-hides anything. Do not
// "improve" this by adding a reveal toggle, a copy button, or a request
// for the real content — that is a new design decision, not a refactor.
//
// COST: one live read per opened row, fired by the click that opened the
// panel. Not on a timer, not prefetched, not fanned out across rows. The
// server handler says the same thing in its own header.
//
// Four rules this hook exists to hold:
//
//   1. NEVER PREFETCH. The read fires when the panel is open on a row and
//      the reader is allowed to see it — never on a list render, never on
//      a timer, never fanned out. The server handler's own header says the
//      same thing; see internal/api/secret_resource.go.
//   2. A row already known to be MISSING fires nothing. The row state
//      already answers "is it on the cluster" — sending a read that can
//      only come back 404 is a doomed call, and it made the panel show a
//      red error for a secret whose absence is the ordinary, expected
//      thing. leftover-secrets S1.2 extends this to an ORPHANED row for a
//      different reason: the live-read endpoint is keyed by a registered
//      addon definition, and an orphan by definition no longer has one —
//      firing it would be the same doomed call for a different cause.
//   3. A reader who is not allowed to read live secrets fires nothing.
//      Before this, a viewer's panel fired the request anyway and rendered
//      the 403 as an error, which is a permission dialog dressed up as a
//      fault.
//   4. It ends. A request that never settles used to leave a spinner
//      turning forever; after LIVE_READ_TIMEOUT_MS the panel says so and
//      offers Retry.
// ─────────────────────────────────────────────────────────────────────────────

/** How long the panel waits for a live read before giving up and offering Retry. */
const LIVE_READ_TIMEOUT_MS = 15000

/** What the panel says when the wait ran out. Sharko's own sentence — the request never answered, so there is no server sentence to quote. */
const LIVE_READ_TIMEOUT_SENTENCE = 'The cluster did not answer in time.'

type LiveSecretState =
  | { status: 'skipped' }
  | { status: 'loading' }
  | { status: 'ready'; resource: SecretResource }
  | { status: 'error'; message: string; notFound: boolean }

function useLiveSecret(row: UnifiedRow | null, allowed: boolean) {
  const [state, setState] = useState<LiveSecretState>({ status: 'skipped' })
  const [attempt, setAttempt] = useState(0)

  const rowKey = row?.key ?? ''
  const kind = row?.kind
  const cluster = row?.cluster ?? ''
  const addon = row?.addon
  // Rule 2 + rule 3, in one place: these are the only reasons the read is
  // skipped, and all are facts already in hand — no request needed to
  // discover any of them. leftover-secrets S1.2 extends rule 2 to
  // 'orphaned': the endpoint a live read would hit is keyed by a
  // registered addon definition, and an orphan by definition no longer has
  // one — firing the read would be exactly the same doomed call a
  // known-missing row's read would be.
  //
  // L12 (code review): kept OUT of the effect's own dependency array on
  // purpose and latched into a ref instead. row.state can flip to/from
  // 'missing' (or 'orphaned') on its own — driven by the page's 30-second
  // background refresh — while this panel is sitting open with the SAME row
  // (rowKey unchanged). If `skip` were a reactive dependency, that flip
  // would re-fire the live read exactly like a timer would, which is the
  // one thing rule 4 in this file's own header comment exists to forbid:
  // only an explicit open (rowKey changes) or Retry (attempt changes) may
  // start a request. The ref lets the effect read the CURRENT
  // allowed/missing/orphaned facts at the instant it actually decides
  // whether to fetch — not as a reason, on its own, to run again.
  const skipRef = useRef(true)
  // Deliberate: this ref is latched during render on purpose (see the L12
  // comment above) so the effect below can read the CURRENT skip facts
  // without them being reactive dependencies. The write is idempotent
  // (same computation every render) and only ever read inside the effect,
  // never during render.
  // eslint-disable-next-line react-hooks/refs -- see comment above
  skipRef.current = !row || !allowed || row.state === 'missing' || row.state === 'orphaned'

  useEffect(() => {
    if (skipRef.current) {
      setState({ status: 'skipped' })
      return
    }
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    setState({ status: 'loading' })

    const request = kind === 'connection' ? getConnectionSecretResource(cluster) : getAddonValuesSecretResource(cluster, addon!)
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(LIVE_READ_TIMEOUT_SENTENCE)), LIVE_READ_TIMEOUT_MS)
    })

    Promise.race([request, timeout])
      .then((res) => {
        if (!cancelled) setState({ status: 'ready', resource: res as SecretResource })
      })
      .catch((err: unknown) => {
        if (cancelled) return
        // Whatever the server said, verbatim — it only ever sends
        // pre-written sentences here, never raw provider or cluster text.
        // The status code is read off the error rather than through an
        // `instanceof ApiError`: a 404 is not a fault, it is the cluster
        // saying the object is not there, and the panel says exactly that
        // instead of painting an error.
        const status = (err as { status?: number } | null)?.status
        setState({
          status: 'error',
          message: err instanceof Error ? err.message : 'Sharko could not read this secret.',
          notFound: status === 404,
        })
      })
      .finally(() => {
        if (timer) clearTimeout(timer)
      })

    return () => {
      cancelled = true
      // Cleared here too, not only in .finally: an unmount before the race
      // settles must not leave a timer running past the test that made it.
      if (timer) clearTimeout(timer)
    }
    // rowKey (a stable string) is the dependency, NOT the row object — the
    // list re-reads itself on every Refresh/Sync and hands down a brand new
    // object each time. Keying on the object would reload the panel's live
    // card every time the list refreshed behind it, which is exactly the
    // behaviour a 30-second auto-refresh would turn into a flicker.
    // skip is deliberately excluded — see skipRef's own comment above; it
    // must never be a reason for this effect to re-run on its own.
  }, [rowKey, attempt, kind, cluster, addon])

  return { live: state, retry: useCallback(() => setAttempt((a) => a + 1), []) }
}

// ─────────────────────────────────────────────────────────────────────────────
// The diff, rebuilt around the resource (P3-F2)
//
// Two cards and one sentence, the way ArgoCD shows a resource: on the left
// what this secret SHOULD be (its intent — a git file and a commit, or the
// store its values come from), on the right what IS on the cluster right
// now. The sentence above them says how the two relate, and it is chosen
// from exactly five, one per real row state.
//
// WHAT THIS DELIBERATELY DOES NOT SAY, ever: a per-key verdict. The key
// table below lists each key, where its value comes from, and whether the
// cluster has it — and stops there. Both engines compare WHOLE secrets, so
// "this key matches and that one doesn't" is a fact nothing in Sharko ever
// established; printing one would be inventing it. Do not add a tick.
// ─────────────────────────────────────────────────────────────────────────────

type DiffVerdict = 'match' | 'differ' | 'never_created' | 'could_not_look' | 'foreign' | 'orphaned'

/**
 * The panel sentence for an orphaned row's verdict line. Word pass SSF-8
 * (2026-08-09 PM decision): "Orphaned" read as jargon, so the sentence now
 * opens with the plain fact instead of the internal word — the row's own
 * status label already says "Not in config" next to it.
 */
const ORPHANED_PANEL_SENTENCE =
  'Not in config — its source in git is gone. Sharko wrote this secret once, but nothing asks for it anymore.'

/**
 * diffVerdictFor picks which of the six sentences the panel says.
 *
 * The order of the checks is the whole logic:
 *   - orphaned first: leftover-secrets S1.2's own state, and it doesn't
 *     overlap with any of the checks below (an orphaned row never carries
 *     foreign/missing/in_sync/out_of_sync at the same time), but it's
 *     checked ahead of everything else because there is nothing left to
 *     compare — no live read was fired for it either (useLiveSecret's skip
 *     rule), so none of the live-read branches below apply.
 *   - foreign next: whoever else owns this secret, that fact outranks
 *     everything else Sharko might say about it.
 *   - a row already known missing next: no read was fired, so there is
 *     nothing on the right to describe.
 *   - a live read that came back 404: the object is not there NOW, which is
 *     fresher than whatever the last check recorded.
 *   - any other failed read: Sharko genuinely could not look, and says so
 *     rather than guessing from a stale state.
 *   - otherwise the row's own state, which is a real comparison the engine
 *     performed: in sync, out of sync, or "unknown" — and unknown IS
 *     "could not look", because it means the last check never finished.
 */
function diffVerdictFor(row: UnifiedRow, live: LiveSecretState): DiffVerdict {
  if (row.state === 'orphaned') return 'orphaned'
  if (row.state === 'foreign') return 'foreign'
  if (row.state === 'missing') return 'never_created'
  if (live.status === 'error') return live.notFound ? 'never_created' : 'could_not_look'
  if (row.state === 'in_sync') return 'match'
  if (row.state === 'out_of_sync') return 'differ'
  return 'could_not_look'
}

/**
 * The six sentences. "never created" is the one that changes wording by
 * kind, and honestly so: on a values row Sync really is what creates the
 * secret, but on a connection row Sync is disabled for exactly this state
 * (there is nothing to sync onto yet) — promising it there would send a
 * reader to a button that refuses. Who fixes it is answered one line below
 * by the self-heal promise, which already knows.
 *
 * SSF-12 (PM's final UX correction): "match" and "differ" carry the fuller
 * sentence the ONE health conclusion needs — the old short forms ("Matches
 * Git." / "These differ — …") collapsed into this, so there is nowhere else
 * on the page that repeats the same fact in different words. The other four
 * sentences already say everything the conclusion needs; they're unchanged.
 */
function diffVerdictSentence(verdict: DiffVerdict, row: UnifiedRow): string {
  switch (verdict) {
    case 'match':
      // SSF-8 binding honesty rule, still true here: a values row's source
      // is NEVER called "Git" — git only ever holds a pointer for a values
      // row, the real source is row.sourceLabel (e.g. an AWS Secrets
      // Manager path). A connection row's source really is git.
      return row.kind === 'connection'
        ? 'The cluster copy matches Git. No action is needed.'
        : `The cluster copy matches ${row.sourceLabel}. No action is needed.`
    case 'differ':
      return row.kind === 'connection' ? 'The cluster copy does not match Git.' : `The cluster copy does not match ${row.sourceLabel}.`
    case 'never_created':
      return row.kind === 'values'
        ? 'This secret was never created on the cluster — Sync creates it.'
        : 'This secret was never created on the cluster.'
    case 'foreign':
      return 'Someone else created this secret — Sharko will not touch it.'
    case 'could_not_look':
      return 'Sharko could not look at the cluster just now.'
    case 'orphaned':
      return ORPHANED_PANEL_SENTENCE
  }
}

/**
 * SSF-12: the one-line promise under a "differ" conclusion — the same
 * honesty rule as the verdict sentence above: never "Git" for a values row.
 * HL-1: the connection sentence used to say "Sync will update the cluster
 * copy to match Git", which was untrue — the action re-applies only
 * Sharko's own addon label keys. It now promises exactly that and no more.
 */
function repairNoteFor(row: UnifiedRow): string {
  return row.kind === 'connection'
    ? "Re-apply addon labels puts git's addon labels back on this secret. Nothing else on it changes."
    : `Sync will update the cluster copy to match ${row.sourceLabel}.`
}

/**
 * SSF-12: which StatusDot fill (and, via statusLabel, which WORD) the
 * conclusion uses — reads off the verdict, never off row.state directly.
 * They usually agree, but not always: a live-read 404 produces
 * verdict='never_created' on a row whose own `state` field hasn't caught up
 * yet (still says the last known reconcile outcome), and a failed live read
 * produces verdict='could_not_look' on a row that reconciled fine. Deriving
 * from the verdict keeps the conclusion's word, its colour, and its
 * sentence — all three — telling the same story, always.
 */
function conclusionStatus(verdict: DiffVerdict): ResourceStatus {
  switch (verdict) {
    case 'match':
      return 'in_sync'
    case 'differ':
      return 'out_of_sync'
    case 'never_created':
      return 'missing'
    case 'foreign':
      return 'foreign'
    case 'could_not_look':
      return 'unknown'
    case 'orphaned':
      return 'orphaned'
  }
}

/**
 * SSF-12: the plain word the ONE health conclusion leads with. Only "match"
 * and "differ" get the urgent/calm words the epic spells out verbatim ("In
 * sync" / "Needs attention") — every other state reuses the exact word
 * StatusMark already uses for the SAME status on the row list (walk finding
 * #140: the row chip and this conclusion must never disagree about what a
 * state is called — one vocabulary, not two). The sentence right underneath
 * still carries the actionable detail (e.g. a values row's "Sync creates
 * it.").
 */
function conclusionLabel(verdict: DiffVerdict): string {
  if (verdict === 'match') return 'In sync'
  if (verdict === 'differ') return 'Needs attention'
  return statusLabel(conclusionStatus(verdict))
}

/** SSF-12: "Check again" once a check has actually produced a result; "Check now" only before the very first one. */
function hasCheckedBefore(row: UnifiedRow): boolean {
  return Boolean(row.lastChecked)
}

/**
 * SSF-14 item 3: provenance is not one side of the comparison — it sits
 * ABOVE it. This paints the instant the panel opens, from the row the list
 * already has (no request, nothing to wait for): which file/commit a
 * connection row was checked against, or which store a values row's
 * pointers resolve through. Never a value, and a values row never claims
 * git is what it was compared to — git only ever holds a pointer for that
 * kind of row.
 *
 * SSF-12: an orphaned row never reaches this component at all — the
 * Comparison zone doesn't render for one (there's nothing left to compare).
 */
function ComparisonProvenance({ row }: { row: UnifiedRow }) {
  if (row.kind !== 'connection') {
    return (
      <p className="text-sm text-[#3a6a8a] dark:text-gray-400" data-testid="comparison-provenance">
        Compared with {row.sourceLabel}. Git holds a pointer to where each value lives, never the value itself.
      </p>
    )
  }
  if (!row.comparedPath) {
    return (
      <p className="text-sm text-[#3a6a8a] dark:text-gray-400" data-testid="comparison-provenance">
        Sharko hasn't compared this secret against git yet.
      </p>
    )
  }
  return (
    <p className="text-sm text-[#3a6a8a] dark:text-gray-400" data-testid="comparison-provenance">
      Compared with git · <span className="font-mono text-[13px]">{row.comparedPath}</span>
      {row.comparedRevision && (
        <>
          {' '}
          · commit{' '}
          <span className="font-mono text-[13px]" title={`Full commit: ${row.comparedRevision}`}>
            {row.comparedRevision.slice(0, 7)}
          </span>
        </>
      )}
    </p>
  )
}

/**
 * The label-key namespace the reconciler stamps onto a v4 repo's ArgoCD
 * cluster Secret for each currently-ENABLED addon (mirrors
 * internal/models.V4AddonLabelPrefix — `addons.sharko.dev/<addon>:
 * enabled`). Only enabled addons ever get a key here (internal/
 * clusterreconciler.v4LabelsFor never writes a `disabled` key); a v3 repo's
 * cluster Secret instead carries the BARE `<addon>` key for the same
 * purpose (the two vocabularies deliberately never mix — see
 * V4AddonLabelPrefix's own doc comment). The match table below checks
 * both, since the frontend has no reliable signal for which repo layout is
 * active.
 */
/**
 * SSF-12 honesty rule: an addon-values secret's ONLY genuinely comparable
 * field is key PRESENCE — the key names Sharko expects vs which of them the
 * server saw on the cluster. Never a value, a hash, a length, or an
 * encoding — the server response never carries one, so nothing here can
 * derive one either. SSF-14 item 3: a real table (Key name | Expected |
 * Present on cluster | Result), listing every expected key — present and
 * missing alike, with a present (matching) key rendered quieter than a
 * missing one so a real gap still stands out.
 */
function ValuesKeyComparison({ live, onRetry }: { live: LiveSecretState; onRetry: () => void }) {
  if (live.status === 'skipped') {
    return (
      <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="resource-not-there">
        Nothing is there — this secret has not been created yet.
      </p>
    )
  }
  if (live.status === 'loading') {
    return <p className="text-sm text-[#2a5a7a] dark:text-gray-400">Reading it from the cluster…</p>
  }
  if (live.status === 'error') {
    return (
      <>
        <p className="text-sm text-red-700 dark:text-red-400" data-testid="resource-error">
          {live.message}
        </p>
        <button
          type="button"
          onClick={onRetry}
          data-testid="resource-retry"
          className="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-[#6aade0] bg-white px-2.5 py-1 text-xs font-medium text-[#0a3a5a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
        >
          <RefreshCw className="h-3 w-3" />
          Retry
        </button>
      </>
    )
  }
  const keys = live.resource.data_keys
  if (keys.length === 0) {
    return <p className="text-sm text-[#2a5a7a] dark:text-gray-400">This secret has no keys to compare.</p>
  }
  return (
    <table className="w-full text-left text-sm" data-testid="comparison-key-presence">
      <thead>
        <tr className="text-[11px] uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">
          <th className="py-1 pr-3 font-medium">Key name</th>
          <th className="py-1 pr-3 font-medium">Expected</th>
          <th className="py-1 pr-3 font-medium">Present on cluster</th>
          <th className="py-1 font-medium">Result</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-border">
        {keys.map((k) => {
          const present = k.present !== false
          return (
            <tr key={k.key}>
              <td className="break-all py-1.5 pr-3 font-mono text-[#2a5a7a] dark:text-gray-300">{k.key}</td>
              <td className="py-1.5 pr-3 text-[#0a2a4a] dark:text-gray-200">Yes</td>
              <td className="py-1.5 pr-3 text-[#0a2a4a] dark:text-gray-200">{present ? 'Yes' : 'No'}</td>
              <td className={`py-1.5 font-medium ${present ? 'text-[#3a6a8a] dark:text-gray-400' : 'text-amber-700 dark:text-amber-400'}`}>
                {present ? 'Match' : 'Missing'}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

/** The sentence a reader who cannot open live secrets sees where the right card would be. Calm, and about access — not an error. */
const LIVE_READ_NEEDS_OPERATOR = 'Reading the live secret needs operator access. What Sharko already knows about it is on the left.'

// ─────────────────────────────────────────────────────────────────────────────
// Redacted YAML (SSF-5, Secret Sync finish pass) — a second, read-only view
// of the SAME live read the Overview tab already fires (useLiveSecret,
// shared by the whole panel — see that hook's own header for the no-second-
// -request rule). This section never calls anything on its own; it only
// re-renders `live.resource` as YAML-shaped text.
//
// buildRedactedYaml never touches a real value because live.resource never
// carries one: every data key's Value is the server's fixed mask
// (SecretResourceKey's own doc comment), and every annotation value is
// either that same mask or one of the server's allow-listed provenance
// strings (a path, a commit, a timestamp — never a secret). This function
// only reformats what's already in the response; it has nothing to redact
// on its own, because the redacting already happened on the server.
// ─────────────────────────────────────────────────────────────────────────────

/** SSF-8: the single plain-words line at the top of the YAML view — the fact a reader needs before anything else on this tab. */
const YAML_VALUES_HIDDEN_SENTENCE = 'Secret values are hidden.'

/** Never say this is the live object itself — Sharko reads a fixed, safe subset of it, not everything the cluster has. */
const REDACTED_YAML_SCOPE_SENTENCE = 'This shows only the fields Sharko reads from the cluster — every value here is a placeholder, never the real one.'

function yamlBlockOrEmpty(heading: string, entries: { key: string; value: string }[]): string[] {
  if (entries.length === 0) return [`  ${heading}: {}`]
  const lines = [`  ${heading}:`]
  for (const e of entries) lines.push(`    ${e.key}: ${e.value}`)
  return lines
}

function buildRedactedYaml(resource: SecretResource): string {
  const lines: string[] = [`apiVersion: ${resource.api_version}`, `kind: ${resource.kind}`, 'metadata:', `  name: ${resource.name}`, `  namespace: ${resource.namespace}`]
  lines.push(...yamlBlockOrEmpty('labels', resource.labels))
  lines.push(...yamlBlockOrEmpty('annotations', resource.annotations))
  if (resource.secret_type) lines.push(`type: ${resource.secret_type}`)
  if (resource.data_keys.length === 0) {
    lines.push('data: {}')
  } else {
    lines.push('data:')
    for (const k of resource.data_keys) lines.push(`  ${k.key}: ${k.value}`)
  }
  return lines.join('\n')
}

function RedactedYamlSection({ row, live, onRetry }: { row: UnifiedRow; live: LiveSecretState; onRetry: () => void }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = (text: string) => {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <div data-testid="detail-yaml-view">
      <p className="mb-1 text-sm font-medium text-[#0a2a4a] dark:text-gray-200" data-testid="detail-yaml-hidden">
        {YAML_VALUES_HIDDEN_SENTENCE}
      </p>
      <p className="mb-2 text-xs text-[#3a6a8a] dark:text-gray-500" data-testid="detail-yaml-scope">
        {REDACTED_YAML_SCOPE_SENTENCE}
      </p>
      {live.status === 'skipped' && row.state === 'missing' && (
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">Nothing is there — this secret has not been created yet.</p>
      )}
      {live.status === 'skipped' && row.state === 'orphaned' && (
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">Sharko still sees it on the cluster, but there's no addon definition left to read it against.</p>
      )}
      {live.status === 'loading' && <p className="text-sm text-[#2a5a7a] dark:text-gray-400">Reading it from the cluster…</p>}
      {live.status === 'error' && (
        <>
          <p className="text-sm text-red-700 dark:text-red-400">{live.message}</p>
          <button
            type="button"
            onClick={onRetry}
            className="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-[#6aade0] bg-white px-2.5 py-1 text-xs font-medium text-[#0a3a5a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
          >
            <RefreshCw className="h-3 w-3" />
            Retry
          </button>
        </>
      )}
      {live.status === 'ready' && (
        <div className="rounded-md border border-border bg-card p-3">
          <div className="mb-2 flex items-center justify-between">
            <span className="text-[11px] font-medium uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">YAML</span>
            {/* Copies the WHOLE redacted block, never a single key — there is no per-secret or per-value copy control here. */}
            <button
              type="button"
              onClick={() => handleCopy(buildRedactedYaml(live.resource))}
              data-testid="detail-yaml-copy"
              className="inline-flex items-center gap-1 text-xs text-[#2a5a7a] hover:text-teal-700 dark:text-gray-400 dark:hover:text-teal-400"
            >
              <Copy className="h-3 w-3" aria-hidden="true" />
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
          <pre className="overflow-x-auto whitespace-pre font-mono text-[13px] text-[#0a2a4a] dark:text-gray-200" data-testid="detail-yaml-content">
            {buildRedactedYaml(live.resource)}
          </pre>
        </div>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// SSF-14 item 6 — the old "Sharko's record"/"Recent activity" accordion (a
// short list of this row's own past audit entries, fetched from the SAME
// audit log AuditViewer shows) is gone: it listed read-only checks, added
// low value in this spot, and duplicated a screen that already exists. In
// its place: one quiet link into that same audit log, pre-filtered to this
// row's cluster via the URL — AuditViewer.tsx reads its existing Cluster
// filter from `?cluster=` on mount (no new filter type, no server change).
// The row's own CURRENT problem is never hidden by this — row.lastCheckError
// and the health conclusion above still render regardless of this link.
// ─────────────────────────────────────────────────────────────────────────────

function RelatedEventsLink({ row }: { row: UnifiedRow }) {
  return (
    <Link
      to={`/audit?cluster=${encodeURIComponent(row.cluster)}`}
      data-testid="detail-related-events-link"
      className="inline-block text-sm text-teal-700 hover:underline dark:text-teal-400"
    >
      View related events
    </Link>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Detail content — rebuilt around the RESOURCE (P3-F2), the way ArgoCD
// lays out one, top to bottom:
//
//   1. identity + actions, with Check now/again and Sync right next to the
//      title — the two things a reader who just opened this page is most
//      likely to want
//   2. the ONE health conclusion (SSF-12)
//   3. the comparison: provenance, then the safe field table (SSF-14 item 3)
//   4. a quiet link into the audit log, scoped to this row's cluster
//
// The full live Secret (labels, annotations, type, key names) lives in the
// YAML tab now (SSF-14 item 4) — Overview no longer carries a second,
// YAML-shaped block of the same facts.
//
// SSF-9 (Secret Sync finish pass, stories 9+10): this used to render inside
// a ResourceDetailSheet side drawer (SecretDetailPanel). The PM's
// post-SSF-8 correction retired the drawer — it held a complete task and
// stayed crowded even at 640px — in favour of a full page
// (SecretDetailPage.tsx) at /secret-sync/<row key>. SecretDetailPage
// renders this directly, below its own Back link. The testid stays
// "secret-detail-panel" on purpose — most of the existing detail tests
// only needed their render harness updated, not their assertions.
//
// SSF-11 (release correction): the page's title moved IN here, next to the
// Check now / Sync actions, so the two sit in one header row instead of
// the title floating alone above a narrow column. See secretTitleFor.
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// The ONE health conclusion (SSF-12, PM's final UX correction) — the page
// used to say the same thing three times on the way in: the row's status
// chip ("Synced"), a one-line verdict ("Matches Git."), and a separate
// "Compared with git." sentence right under it. A reader had to read all
// three to be sure they were the same fact. This is that fact, once: one
// word ("In sync" / "Needs attention" / …), one full sentence naming the
// real source, and — only when something needs fixing — the one-line
// promise of what Sync actually does about it. "Checked …" sits right next
// to it, because "when was it checked" is one of the five questions this
// page exists to answer and it shouldn't need its own row.
//
// Walkthrough follow-up on SSF-14: this block also carries the drift-blame
// sentence (which side moved) and the self-heal promise (does the reader
// need to press Sync themselves, or will Sharko fix it on its own) — both
// restored here after SSF-14 first deleted their old home (the Resource
// details accordion) without giving them a new one. Wording and the
// conditions that decide whether they show are UNCHANGED from before;
// only the place changed.
// ─────────────────────────────────────────────────────────────────────────────

function HealthConclusion({ row, verdict }: { row: UnifiedRow; verdict: DiffVerdict }) {
  const isMatch = verdict === 'match'
  return (
    // role="status" (an implicit aria-live="polite" region): a reader using
    // a screen reader gets told when Check now/Sync flips this conclusion
    // to a different word — not just a sighted reader watching the text
    // repaint.
    <div className="space-y-1.5 border-b border-border pb-4" data-testid="detail-health-conclusion" role="status">
      <div className="flex items-center gap-2">
        {isMatch ? (
          <CheckCircle className="h-5 w-5 text-green-600 dark:text-green-500" aria-hidden="true" />
        ) : (
          <StatusDot status={conclusionStatus(verdict)} className="h-4 w-4" />
        )}
        <span className="text-xl font-semibold text-[#0a2a4a] dark:text-gray-100" data-testid="detail-conclusion-label">
          {conclusionLabel(verdict)}
        </span>
      </div>
      <p className="text-base text-[#2a5a7a] dark:text-gray-300" data-testid="diff-verdict">
        {diffVerdictSentence(verdict, row)}
      </p>
      {/* P2-C6 (restored — walkthrough follow-up on SSF-14): which side
          moved, the WHY behind the "does not match" sentence above. Same
          condition as when this lived in the now-deleted Resource details
          accordion (connection rows, out-of-sync only, only when both
          revisions are known) — only the PLACE changed, not the content or
          the condition. Sits right under the problem statement it
          explains, ahead of the repair promise. */}
      {row.kind === 'connection' && row.state === 'out_of_sync' && row.driftSource && (
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="detail-drift-source">
          {row.driftSource === 'git'
            ? 'Git moved — a newer commit changed what this secret should be.'
            : 'The cluster moved — something changed this secret outside git.'}
        </p>
      )}
      {/* The repair promise — only where Sync is really the fix (a real
          drift to push). A values row's own "never created" sentence
          already says "Sync creates it" inline, so it doesn't need this
          second line too. */}
      {verdict === 'differ' && (
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="detail-repair-note">
          {repairNoteFor(row)}
        </p>
      )}
      {/* P2-C3 (restored — walkthrough follow-up on SSF-14): does the
          reader need to press Sync themselves, or will Sharko fix this on
          its own pass — the single most important sentence on a broken
          secret for an on-call reader. Same condition as the deleted
          Resource details field (out_of_sync or missing, either row
          kind) — never shown for a healthy/match row, exactly as before.
          Sits right after the repair promise so "what Sync does" and
          "do I actually need to click it" read together. */}
      {(row.state === 'out_of_sync' || row.state === 'missing') && (
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="detail-self-heals">
          {/* HL-1: the button is named per kind now, so this points at the
              right name — see syncActionLabel. */}
          {row.selfHeals ? 'Sharko will fix this on the next pass.' : `Waiting for ${syncActionLabel(row.kind)}.`}
        </p>
      )}
      {/* SSF-14 item 7: a timestamp, so it stays at least 13px — was 12px
          (text-xs) here before. */}
      <p className="text-[13px] text-[#5a8aaa] dark:text-gray-500" data-testid="detail-checked-line">
        Checked <TimeChip iso={row.lastChecked} />
      </p>
    </div>
  )
}

/**
 * The plain-words title this panel puts at the top of its own header row —
 * the same sentence SSF-8 put in the drawer's own title bar. A connection
 * row is "{cluster} connection"; a values row is "{addon} values on
 * {cluster}"; an orphaned row with no addon on record falls back to a
 * cluster-only sentence rather than printing "undefined".
 */
export function secretTitleFor(row: UnifiedRow): string {
  return row.kind === 'connection' ? `${row.cluster} connection` : row.addon ? `${row.addon} values on ${row.cluster}` : `Secret on ${row.cluster}`
}

export function SecretDetailContent({
  row,
  onRequestSync,
  onRequestDelete,
  onChanged,
}: {
  row: UnifiedRow
  onRequestSync: (row: UnifiedRow) => void
  /** leftover-secrets S1.2 — opens the page-level Delete confirm for an orphaned row. */
  onRequestDelete: (row: UnifiedRow) => void
  onChanged: () => void
}) {
  const navigate = useNavigate()
  const [refreshing, setRefreshing] = useState(false)
  // SSF-5 (Secret Sync finish pass) — Overview vs Redacted YAML. Always
  // reopens on Overview for whichever row the panel is now showing; a tab
  // choice made on one row must not carry over to the next one it opens.
  const [detailTab, setDetailTabState] = useState<'overview' | 'yaml'>('overview')
  // SSF-8/SSF-14 — whether the comparison box is expanded. Item 2 (SSF-14):
  // this now SURVIVES a Check again — a reader who opened it stays opened,
  // and one who closed a healthy result stays closed — UNLESS the result
  // just flipped healthy -> broken, which forces it open so a new problem
  // is never hidden behind a closed toggle. See the effect below.
  const [comparisonOpen, setComparisonOpen] = useState(false)

  // S4-1: Connection-comparison check — runs once when the page opens for a
  // connection row, using the new GET /clusters/{name}/connection-comparison
  // endpoint. Re-runs whenever a DIFFERENT row is shown.
  const [connectionComparisonData, setConnectionComparisonData] = useState<ConnectionComparisonView | null>(null)
  const [connectionComparisonLoading, setConnectionComparisonLoading] = useState(false)
  const [connectionComparisonError, setConnectionComparisonError] = useState<string | null>(null)

  // S4-4 / S4-5: Connection repair state
  const [repairInProgress, setRepairInProgress] = useState(false)
  const [showRepairConfirm, setShowRepairConfirm] = useState(false)
  const [repairResult, setRepairResult] = useState<ConnectionRepairView | null>(null)
  const [recentAuditEntries, setRecentAuditEntries] = useState<AuditEntry[]>([])

  useEffect(() => {
    setConnectionComparisonData(null)
    setConnectionComparisonError(null)
    if (!row || row.kind !== 'connection') {
      setConnectionComparisonLoading(false)
      return
    }
    let cancelled = false
    setConnectionComparisonLoading(true)
    api
      .getConnectionComparison(row.cluster)
      .then((result) => {
        if (!cancelled) setConnectionComparisonData(result)
      })
      .catch((err) => {
        if (!cancelled) setConnectionComparisonError(err instanceof Error ? err.message : 'The check failed.')
      })
      .finally(() => {
        if (!cancelled) setConnectionComparisonLoading(false)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row?.key])

  const handleConnectionComparisonRetry = () => {
    if (!row || row.kind !== 'connection') return
    setConnectionComparisonError(null)
    setConnectionComparisonLoading(true)
    api
      .getConnectionComparison(row.cluster)
      .then((result) => setConnectionComparisonData(result))
      .catch((err) => setConnectionComparisonError(err instanceof Error ? err.message : 'The check failed.'))
      .finally(() => setConnectionComparisonLoading(false))
  }

  // S4-5: Fetch recent audit entries for connection rows
  useEffect(() => {
    setRecentAuditEntries([])
    if (!row || row.kind !== 'connection') {
      return
    }
    let cancelled = false
    fetchAuditLog({ cluster: row.cluster })
      .then((result) => {
        if (!cancelled && result.entries) {
          // Take the newest 5 entries
          setRecentAuditEntries(result.entries.slice(0, 5))
        }
      })
      .catch((err) => {
        // Audit fetch failure is logged but not shown as an error —
        // Recent activity is supplemental, not critical
        console.warn('Failed to fetch recent audit entries:', err)
      })
    return () => {
      cancelled = true
    }
  }, [row?.key])

  // S4-4: Repair button click handler
  const handleRepairClick = () => {
    setShowRepairConfirm(true)
  }

  // S4-4: Repair confirmation
  const handleRepairConfirm = async () => {
    if (!row || row.kind !== 'connection' || !connectionComparisonData?.compared_commit) {
      return
    }
    setShowRepairConfirm(false)
    setRepairInProgress(true)
    try {
      const result = await api.repairConnection(row.cluster, connectionComparisonData.compared_commit)
      setRepairResult(result)
      // Update the comparison data with the fresh check from the repair
      setConnectionComparisonData(result.comparison)
      // Refresh recent audit entries to show the repair action
      const auditResult = await fetchAuditLog({ cluster: row.cluster })
      if (auditResult.entries) {
        setRecentAuditEntries(auditResult.entries.slice(0, 5))
      }
      showToast(result.message, 'success')
      onChanged()
    } catch (err) {
      // S4-4: Handle 409 specifically — the branch moved or git cannot tell
      // which commit it's on. The server sends one of three sentences, all
      // deliberately worded. Show the server's sentence unchanged.
      // Do NOT auto-retry — the person decides when to check again.
      if (err instanceof ApiError && err.status === 409) {
        // Server sent one of: repairFailRevisionUnknown, repairFailRevisionMoved,
        // or repairFailRaced. All three say "nothing changed" and tell the person
        // to run the check again. Show the message as-is.
        showToast(err.message, 'warning')
      } else {
        const message = err instanceof Error ? err.message : 'The repair failed.'
        showToast(message, 'error')
      }
    } finally {
      setRepairInProgress(false)
    }
  }

  // The same role predicate RoleGuard applies below, read here so the
  // REQUEST is gated too and not just the rendering. A viewer's panel used
  // to fire the read anyway and paint the 403 as an error — a permission
  // dialog dressed up as a fault.
  const auth = useContext(AuthContext)
  const canReadLive = auth?.role === 'admin' || auth?.role === 'operator'
  const { live, retry } = useLiveSecret(row, canReadLive)

  // SSF-12's verdict, needed here (not just below, where the old code first
  // computed it) because the comparisonOpen effect right after this needs
  // it too.
  const verdict = diffVerdictFor(row, live)

  // SSF-14 item 2 — tracks the PREVIOUS row key and verdict this component
  // last saw, purely to decide comparisonOpen; nothing here reads back out
  // except that one state:
  //   - a different row opened (including first mount): always resets to
  //     Overview, and starts the comparison closed for a match, open for
  //     everything else — an unread row's first look must never hide a
  //     real problem behind a closed toggle.
  //   - the SAME row, but its verdict just flipped from match to anything
  //     else (a Check again just found a new problem): force the
  //     comparison open, overriding whatever the reader had chosen.
  //   - the SAME row, verdict unchanged or already broken before and still
  //     broken/matching after: leave comparisonOpen exactly as the reader
  //     left it.
  const prevRowKeyRef = useRef<string | undefined>(undefined)
  const prevVerdictRef = useRef<DiffVerdict | null>(null)
  useEffect(() => {
    if (prevRowKeyRef.current !== row?.key) {
      setDetailTabState('overview')
      setComparisonOpen(verdict !== 'match')
      prevRowKeyRef.current = row?.key
    } else if (prevVerdictRef.current === 'match' && verdict !== 'match') {
      setComparisonOpen(true)
    }
    prevVerdictRef.current = verdict
  }, [row?.key, verdict])

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      if (row.kind === 'connection') {
        await reconcileCluster(row.cluster)
        showToast(connectionRefreshToast(row.cluster), 'success')
      } else {
        const result = await refreshAddonValuesSecret(row.cluster, row.addon!)
        showToast(result.message, 'success')
      }
      onChanged()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to trigger refresh', 'error')
    } finally {
      setRefreshing(false)
    }
  }

  const gate = syncGateFor(row)

  const purposeSentence: ReactNode =
    row.state === 'orphaned' ? (
      <>
        Not in config anymore, on cluster <span className="font-medium">{row.cluster}</span>
        {row.addon ? (
          <>
            {' '}
            — was for addon <span className="font-medium">{row.addon}</span>
          </>
        ) : null}
        .
      </>
    ) : row.kind === 'connection' ? (
      <>
        Connects <span className="font-medium">{row.cluster}</span> to ArgoCD.
      </>
    ) : (
      <>
        Carries values for addon <span className="font-medium">{row.addon}</span> on cluster{' '}
        <span className="font-medium">{row.cluster}</span>.
      </>
    )

  // SSF-12: "Differences" only when there really are some (the "differ"
  // verdict) — every other state (including the four boundary/unknown
  // verdicts that used to borrow "Diff"/"Comparison" from row kind) gets
  // the calmer, generic "Comparison" heading. Row kind no longer decides
  // this word at all — "Diff" is gone; it always read as a claim that a
  // values row is checked against git, which was never true.
  const comparisonHeading = verdict === 'differ' ? 'Differences' : 'Comparison'

  // SSF-12: "Check again" once a check has actually produced a result;
  // "Check now" only before the very first one.
  const checkLabel = hasCheckedBefore(row) ? 'Check again' : 'Check now'

  const viewPageLabel = row.kind === 'connection' || !row.addon ? 'View cluster page' : 'View addon page'
  const viewPageHref =
    row.kind === 'connection' || !row.addon ? `/clusters/${encodeURIComponent(row.cluster)}` : `/addons/${encodeURIComponent(row.addon)}`

  return (
    <div data-testid="secret-detail-panel" className="space-y-4">
      {/* ── Identity + actions (SSF-11/SSF-12) — the title and its one-line
          purpose sit LEFT; Check now/again / Sync (or, for an orphaned row,
          the single Delete action) sit RIGHT, in the same row. flex-wrap
          alone stacks this safely once the row runs out of room. */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 space-y-1">
          <h1 className="text-2xl font-semibold leading-tight text-[#0a2a4a] dark:text-gray-100 sm:text-[28px]">{secretTitleFor(row)}</h1>
          <p className="text-base text-[#2a5a7a] dark:text-gray-300">{purposeSentence}</p>
          {/* SSF-14 item 5: "View cluster"/"View addon" moves up here, next
              to the description, as a normal secondary link — it used to
              live inside the removed Resource details section. */}
          <button
            type="button"
            onClick={() => navigate(viewPageHref)}
            data-testid="detail-view-page-link"
            className="text-sm text-teal-700 hover:underline dark:text-teal-400"
          >
            {viewPageLabel}
          </button>
        </div>
        <RoleGuard roles={['admin', 'operator']}>
          <div className="flex flex-wrap items-center gap-2">
            {/* leftover-secrets S1.2: an orphaned row gets exactly one
                action — Delete, red, opening the page-level confirm. No
                Check now (there's nothing left to check it against), no
                Sync (there's no source to sync from). */}
            {row.state === 'orphaned' ? (
              <PanelActionButton
                onClick={() => onRequestDelete(row)}
                icon={Trash2}
                label="Delete"
                testId="detail-delete"
                destructive
              />
            ) : (
              <>
                {/* SSF-12: "Check now" only before the first result ever
                    lands; "Check again" every time after — testid
                    unchanged. */}
                <PanelActionButton onClick={handleRefresh} loading={refreshing} icon={RefreshCw} label={checkLabel} testId="detail-refresh" />
                {/* SSF-12: Sync is HIDDEN entirely — not just disabled —
                    once the conclusion is "In sync". There is nothing to
                    apply, so there is no button to grey out. Every other
                    verdict keeps it, strong only when there's real drift to
                    push (!gate.disabled); when it's genuinely unavailable
                    (foreign, not checked yet, …) PanelActionButton's own
                    InfoHint says why — never an unexplained disabled
                    button. */}
                {verdict !== 'match' && (
                  <PanelActionButton
                    onClick={() => onRequestSync(row)}
                    disabled={gate.disabled}
                    icon={RotateCcw}
                    // HL-1: per kind — see syncActionLabel. The testid
                    // stays detail-sync on purpose; only the words change.
                    label={syncActionLabel(row.kind)}
                    reason={gate.reason}
                    testId="detail-sync"
                    strong={!gate.disabled}
                  />
                )}
                {/* S4-4: Repair button — only for connection rows, only when
                    the server says repair_available is true AND repair_scope
                    is not 'none' AND there's a commit on screen. */}
                {row.kind === 'connection' &&
                  connectionComparisonData?.repair_available &&
                  connectionComparisonData.repair_scope !== 'none' &&
                  connectionComparisonData.compared_commit && (
                    <PanelActionButton
                      onClick={handleRepairClick}
                      loading={repairInProgress}
                      icon={Wrench}
                      label={
                        connectionComparisonData.credential_source_type === CREDS_SOURCE_EKS_TOKEN
                          ? 'Refresh EKS connection'
                          : 'Repair connection'
                      }
                      testId="detail-repair-connection"
                      strong
                    />
                  )}
              </>
            )}
          </div>
        </RoleGuard>
      </div>

      {/* ── ONE health conclusion (SSF-12) — replaces the old three-line
          Synced / Matches Git / Compared with git trio. Visible on both
          tabs, same as before: "is it okay" and "when was it checked"
          shouldn't disappear just because a reader is looking at YAML. */}
      <HealthConclusion row={row} verdict={verdict} />

      {/* SSF-5 (Secret Sync finish pass) — Overview vs the read-only YAML
          view, same segmented-pill pattern the page's own Group by /
          List-Tiles controls already use. Resets to Overview whenever a
          different row opens (the effect above). */}
      <div className="inline-flex overflow-hidden rounded-lg ring-1 ring-[#6aade0] dark:ring-gray-700">
        <button
          type="button"
          onClick={() => setDetailTabState('overview')}
          aria-pressed={detailTab === 'overview'}
          data-testid="detail-tab-overview"
          className={`px-3 py-1.5 text-sm font-medium ${
            detailTab === 'overview'
              ? 'bg-[#1a3d5c] text-white'
              : 'bg-white text-[#2a5a7a] hover:bg-[#e0f0ff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
          }`}
        >
          Overview
        </button>
        <button
          type="button"
          onClick={() => setDetailTabState('yaml')}
          aria-pressed={detailTab === 'yaml'}
          data-testid="detail-tab-yaml"
          className={`px-3 py-1.5 text-sm font-medium ${
            detailTab === 'yaml'
              ? 'bg-[#1a3d5c] text-white'
              : 'bg-white text-[#2a5a7a] hover:bg-[#e0f0ff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
          }`}
        >
          YAML
        </button>
      </div>

      {detailTab === 'yaml' ? (
        <RoleGuard
          roles={['admin', 'operator']}
          fallback={
            <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="yaml-needs-operator">
              {LIVE_READ_NEEDS_OPERATOR}
            </p>
          }
        >
          {/* S4-2: For connection rows, the redacted YAML moved to Overview
              (below the comparison). The YAML tab now points there. For
              addon-values rows, it stays here as before. */}
          {row.kind === 'connection' ? (
            <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
              The redacted YAML for this connection is on the Overview tab, below the connection check.
            </p>
          ) : (
            <RedactedYamlSection row={row} live={live} onRetry={retry} />
          )}
        </RoleGuard>
      ) : (
        <>
          {/* The most actionable lines sit right under the conclusion —
              where a reader who just opened the page actually looks. */}
          {row.lastCheckError && (
            <p className="text-sm text-red-700 dark:text-red-400" data-testid="last-check-error">
              The last check failed: {row.lastCheckError}
            </p>
          )}
          {row.kind === 'connection' && (row.fightCount ?? 0) >= ROW_WARNING_THRESHOLD && (
            <p className="text-sm text-amber-700 dark:text-amber-400" data-testid="fight-warning">
              {fightWarningSentence(row.fightCount ?? 0)}
            </p>
          )}
          {row.kind === 'values' && (row.consecutiveFailures ?? 0) >= ROW_WARNING_THRESHOLD && (
            <p className="text-sm text-amber-700 dark:text-amber-400" data-testid="consecutive-failures-warning">
              {consecutiveFailuresSentence(row.consecutiveFailures ?? 0)}
            </p>
          )}

          {/* ── Comparison, only when useful (SSF-12/SSF-14) ───────────────
              S4-1/S4-2: For connection rows, the comparison is now the full
              connection check (inline and already open, no toggle) using the
              new GET /clusters/{name}/connection-comparison endpoint. For
              addon-values rows, it remains the key-presence check.

              An orphaned row has nothing left to compare (its source in
              git is gone — the conclusion above already says so), so no
              Comparison zone renders for it at all. */}
          {row.state !== 'orphaned' && (
            <div>
              {row.kind === 'connection' ? (
                <>
                  <h2 className="mb-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Connection check</h2>
                  <RoleGuard
                    roles={['admin', 'operator']}
                    fallback={
                      <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="live-needs-operator">
                        {LIVE_READ_NEEDS_OPERATOR}
                      </p>
                    }
                  >
                    <ConnectionComparisonDisplay
                      comparison={connectionComparisonData}
                      loading={connectionComparisonLoading}
                      error={connectionComparisonError}
                      onRetry={handleConnectionComparisonRetry}
                    />
                  </RoleGuard>

                  {/* S4-2: RedactedYamlSection moves below the comparison for
                      connection rows, so a reader sees check → differences →
                      redacted YAML, all on one scrolling page. */}
                  <div className="mt-4">
                    <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Redacted YAML</h3>
                    <RoleGuard
                      roles={['admin', 'operator']}
                      fallback={
                        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
                          {LIVE_READ_NEEDS_OPERATOR}
                        </p>
                      }
                    >
                      <RedactedYamlSection row={row} live={live} onRetry={retry} />
                    </RoleGuard>
                  </div>

                  {/* S4-5: Recent activity — the newest 5 audit entries for
                      this cluster, then a link to the full log. */}
                  {recentAuditEntries.length > 0 && (
                    <div className="mt-4">
                      <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Recent activity</h3>
                      <div className="space-y-2">
                        {recentAuditEntries.map((entry, idx) => (
                          <div key={idx} className="rounded-md border border-border bg-card p-2 text-xs" data-testid={`recent-activity-entry-${idx}`}>
                            <div className="flex items-center justify-between gap-2">
                              <span className="font-medium text-[#0a2a4a] dark:text-gray-200">{entry.event}</span>
                              <span className="text-[#5a8aaa] dark:text-gray-500">{new Date(entry.timestamp).toLocaleString()}</span>
                            </div>
                            {entry.detail && <p className="mt-1 text-[#2a5a7a] dark:text-gray-400">{entry.detail}</p>}
                          </div>
                        ))}
                        <a href={`/audit?cluster=${encodeURIComponent(row.cluster)}`} className="inline-block text-sm text-teal-700 hover:underline dark:text-teal-400" data-testid="view-full-audit-log">
                          View full audit log
                        </a>
                      </div>
                    </div>
                  )}
                </>
              ) : (
                <>
                  <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                    <h2 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">{comparisonHeading}</h2>
                    <button
                      type="button"
                      onClick={() => setComparisonOpen((open) => !open)}
                      aria-expanded={comparisonOpen}
                      data-testid="view-comparison-toggle"
                      className="inline-flex items-center rounded-md px-2 py-1 text-sm font-medium text-teal-700 hover:bg-teal-50 hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-teal-400 dark:hover:bg-teal-950/40"
                    >
                      {comparisonOpen ? 'Hide comparison' : 'View comparison'}
                    </button>
                  </div>
                  {comparisonOpen && (
                    <div className="space-y-3 rounded-md border border-border bg-card p-3">
                      <ComparisonProvenance row={row} />
                      <RoleGuard
                        roles={['admin', 'operator']}
                        fallback={
                          <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="live-needs-operator">
                            {LIVE_READ_NEEDS_OPERATOR}
                          </p>
                        }
                      >
                        <ValuesKeyComparison live={live} onRetry={retry} />
                      </RoleGuard>
                    </div>
                  )}
                </>
              )}
            </div>
          )}

          {/* ── Related events (SSF-14 item 6) — replaces the old "Recent
              activity" accordion. No new history: this is one quiet link
              into the SAME audit log AuditViewer already shows, scoped to
              this row's cluster via a URL param it now reads on mount. Any
              CURRENT problem is still visible above (the health conclusion
              and, for connection rows, the last-check-error line) — this
              link never has to be opened to see an active failure. */}
          <RelatedEventsLink row={row} />
        </>
      )}

      {/* S4-4: Repair confirmation modal */}
      {row.kind === 'connection' && (
        <ConfirmationModal
          open={showRepairConfirm}
          onClose={() => setShowRepairConfirm(false)}
          onConfirm={handleRepairConfirm}
          title={
            connectionComparisonData?.credential_source_type === CREDS_SOURCE_EKS_TOKEN
              ? `Refresh EKS connection for "${row.cluster}"?`
              : `Repair connection for "${row.cluster}"?`
          }
          description={
            connectionComparisonData?.credential_source_type === CREDS_SOURCE_EKS_TOKEN
              ? `This will refresh the short-lived sign-in token for this EKS connection to match what Sharko intends. Addon labels will be re-applied. Foreign labels, other data keys and annotations will be left alone. The self-heal setting will not be changed.`
              : connectionComparisonData?.repair_scope === 'addon_labels_only'
                ? `This will re-apply this cluster's addon labels to match git. Sharko will not read or change this connection's sign-in details. The self-heal setting will not be changed.`
                : `This will rewrite this cluster's connection to match git and this cluster's configured credentials source. Addon labels will be re-applied. Foreign labels, other data keys and annotations will be left alone. The self-heal setting will not be changed.`
          }
          confirmText={
            connectionComparisonData?.credential_source_type === CREDS_SOURCE_EKS_TOKEN ? 'Refresh connection' : 'Repair connection'
          }
          loading={repairInProgress}
        />
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// One secret row. Hoisted out of the page so the flat list and the grouped
// list render the exact same markup — a second copy would drift, and the
// two views would quietly start disagreeing about what a row says.
// ─────────────────────────────────────────────────────────────────────────────

function SecretTableRow({
  row,
  indented,
  busy,
  onSelect,
  onRefresh,
  onRequestSync,
  onRequestDelete,
}: {
  row: UnifiedRow
  /** true when the row sits under a group parent — a small left inset, nothing else changes. */
  indented?: boolean
  busy: boolean
  onSelect: () => void
  onRefresh: () => void
  onRequestSync: () => void
  /** leftover-secrets S1.2 — opens the page-level Delete confirm for an orphaned row. */
  onRequestDelete: () => void
}) {
  // P3-F2: the row opens the panel, so it has to BE a control — reachable
  // by Tab, opened by Enter or Space, and announced as a button. Before
  // this it was a click-only <tr>, which a keyboard reader could see and
  // never open.
  const openOnKey = (e: React.KeyboardEvent<HTMLTableRowElement>) => {
    if (e.key !== 'Enter' && e.key !== ' ') return
    e.preventDefault() // Space would otherwise scroll the page
    onSelect()
  }

  const identity = row.secretNamespace && row.secretName ? `${row.secretNamespace}/${row.secretName}` : '—'

  return (
    <TableRow
      data-testid={`secret-row-${row.key}`}
      onClick={onSelect}
      onKeyDown={openOnKey}
      tabIndex={0}
      role="button"
      aria-label={`Open ${identity !== '—' ? identity : row.cluster}`}
      className="cursor-pointer hover:bg-[#d6eeff] focus:outline-none focus-visible:ring-2 focus-visible:ring-[#1a3d5c] dark:hover:bg-gray-800 dark:focus-visible:ring-teal-400"
    >
      {/* H2 (gitops-proud P4-H): one line, not two. The old grey subline
          under the identity said the ROW'S KIND — the KeyRound/Lock glyph
          right next to the name already says that, so the line was
          repeating the icon in words. It's gone.

          Walk finding #140: NAME shows only the secret's NAME now — the
          namespace moved into its own column, right after this one, so a
          demo estate where several rows share a namespace and differ only
          in name is no longer truncated exactly where they differ. This
          cell still caps its width so a long name can't push
          STATUS/ADDON/CLUSTER/SOURCE off the side of the screen, but if it
          does truncate, `direction: rtl` makes the cut land at the FRONT of
          the string, not the tail — the tail is the part that actually
          tells rows apart. The full name is always in the title attribute.

          H4: a grouped child row (indented) gets a thin vertical guide —
          addon → cluster → secret should read as a tree, not just an
          indent.

          The status edge strip (copied from ArgoCD's own list and tile
          views) lives on this same cell, as a left border — a `<td>`
          always wins the collapsed-border fight for its own edge, so it
          renders reliably regardless of the table's border-collapse
          mode. Its colour is read off the exact same STATUS_META table as
          the row's own <StatusMark> dot and the filter chips, via
          statusStripClassName — it cannot disagree with the dot next to
          it. */}
      <TableCell className={`py-2 px-1.5 ${statusStripClassName(row.state)} ${indented ? 'pl-2' : ''}`}>
        <div className="flex min-w-0 items-center gap-1.5">
          {indented && <span aria-hidden="true" className="h-4 w-px shrink-0 bg-[#c0ddf0] dark:bg-gray-700" />}
          {row.kind === 'connection' ? (
            <KeyRound className="h-3.5 w-3.5 shrink-0 text-[#5a8aaa] dark:text-gray-500" aria-hidden="true" />
          ) : (
            <Lock className="h-3.5 w-3.5 shrink-0 text-[#5a8aaa] dark:text-gray-500" aria-hidden="true" />
          )}
          <span
            className="min-w-0 flex-1 truncate font-mono text-sm font-semibold text-[#0a2a4a] dark:text-white"
            style={{ direction: 'rtl', textAlign: 'left' }}
            title={row.secretName || undefined}
            data-testid="cell-name"
          >
            {row.secretName ?? '—'}
          </span>
        </div>
      </TableCell>
      {/* Namespace (walk finding #140): its own sortable column, freed from
          the old combined "namespace/name" cell. Namespaces repeat across
          rows and are recognizable even truncated, so a plain truncate +
          hover title is enough here — no need for NAME's front-truncate
          trick. */}
      <TableCell
        className="truncate py-2 px-1.5 text-sm text-[#2a5a7a] dark:text-gray-300"
        title={row.secretNamespace || undefined}
        data-testid="cell-namespace"
      >
        {row.secretNamespace ?? '—'}
      </TableCell>
      {/* Status (H2): moved up next to the name, ArgoCD's own habit of
          putting a resource's health right at the start of its row instead
          of buried at the end. */}
      <TableCell className="py-2 px-1.5">
        <StatusMark status={row.state} />
      </TableCell>
      {/*
        Addon (G1): values rows show the addon they carry values for.
        Connection rows show a plain dash — they are not addon secrets,
        matching the same "—" the identity cell above already uses for
        "nothing here", not an invented word like "n/a" or a made-up noun.
      */}
      <TableCell
        className="truncate py-2 px-1.5 text-sm text-[#2a5a7a] dark:text-gray-300"
        title={row.kind === 'values' ? row.addon : undefined}
        data-testid="cell-addon"
      >
        {row.kind === 'values' ? (row.addon ?? '—') : '—'}
      </TableCell>
      {/* Cluster (H2): both kinds print it now. It used to be left blank on
          connection rows because the identity cell already implied the
          cluster — but with the subline gone and STATUS now sitting
          between the two, that implication got harder to read at a
          glance, and grouping by addon needs a real cluster column for
          every row it shows anyway. */}
      <TableCell
        className="truncate py-2 px-1.5 text-sm text-[#2a5a7a] dark:text-gray-300"
        title={row.cluster}
        data-testid="cell-cluster"
      >
        {row.cluster}
      </TableCell>
      {/* Compared with (G1/H3, design-secret-sync-visual-pass section 2):
          the S3 honesty lock, sortable/filterable/searched on every row.
          The RELATION ("compared with" — SSF-8 item 3, was "checked
          against") now lives once in the sticky column header — this cell
          states just the place name, so the longest demo name
          (kube-prometheus-stack-grafana-admin, 35 chars) can render uncut
          at 1280px. The full sentence — the same one the panel's Resource
          details says — is one hover away. */}
      <TableCell
        className="truncate py-2 px-1.5 text-sm text-[#2a5a7a] dark:text-gray-300"
        data-testid="cell-source"
        title={row.kind === 'connection' ? 'Compared with git.' : `Compared with ${row.sourceLabel} — git only holds a pointer to it.`}
      >
        {sourceShortLabel(row.sourceLabel)}
      </TableCell>
      {/* SSF-2 follow-up (browser verification): this cell holds the h-8
          (32px) row-menu trigger — RowActionsMenu's own touch target, not
          shrunk here. py-2 on this cell (matching the text cells) pushed
          the row to 32+8+8+1=49px, well over the ~36-40px spec; the text
          cells alone only need 20+16=36px. Tighter padding here lets the
          row's height follow the button (32+4+1≈37px) instead of doubling
          up on top of it — every other cell keeps py-2. */}
      <TableCell className="py-0.5 px-1.5" onClick={(e) => e.stopPropagation()}>
        <RoleGuard roles={['admin', 'operator']}>
          <RowActionsMenu
            label={`Actions for ${row.cluster}${row.addon ? ' / ' + row.addon : ''}`}
            actions={actionsForRow(row, { busy, onRefresh, onRequestSync, onRequestDelete })}
          />
        </RoleGuard>
      </TableCell>
    </TableRow>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The collapsible group parent (G2/G3). Same interaction as
// AddonVersionList: click the line, the children appear under it.
//
// The right-hand summary is groupSummary's output and nothing else — see
// that function for what a header is and is not allowed to say.
// ─────────────────────────────────────────────────────────────────────────────

function GroupHeaderRow({ group, expanded, onToggle }: { group: RowGroup; expanded: boolean; onToggle: () => void }) {
  return (
    <TableRow className="hover:bg-transparent">
      {/* Name, Namespace, Status, Addon, Cluster, Compared with, plus the
          actions column (G1 added Addon + Source; H2 moved Status next to
          Name; walk finding #140 added Namespace; design-secret-sync-
          visual-pass removed the LAST CHECKED column — the fact lives in
          the engine strip + panel now). */}
      <TableCell colSpan={7} className="p-0">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          data-testid={`secret-group-${group.key}`}
          className="flex w-full flex-wrap items-center justify-between gap-2 bg-[#e0f0ff] px-3 py-2 text-left hover:bg-[#d6eeff] dark:bg-gray-800/60 dark:hover:bg-gray-800"
        >
          <span className="flex items-center gap-1.5">
            {expanded ? (
              <ChevronUp className="h-4 w-4 shrink-0 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
            ) : (
              <ChevronDown className="h-4 w-4 shrink-0 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
            )}
            <span className="font-semibold text-[#0a2a4a] dark:text-gray-100">{group.label}</span>
            <span className="text-[11px] text-[#5a8aaa] dark:text-gray-500">{group.sublabel}</span>
          </span>
          <span className="text-xs text-[#2a5a7a] dark:text-gray-400" data-testid={`secret-group-summary-${group.key}`}>
            {groupSummary(group.rows)}
          </span>
        </button>
      </TableCell>
    </TableRow>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The shared data fetch — the list page (ManagedSecrets, below) and the
// full detail page (SecretDetailPage.tsx, SSF-9) both need the exact same
// managed-secrets read to build their rows from: the list shows every row,
// the detail page finds its own row in the same set. Hoisted here (and
// exported) so both call the SAME load()/30-second-refresh/
// pause-while-hidden logic instead of each growing its own copy — a direct
// load or refresh of the detail page gets the same "check every 30s while
// the tab is visible" behaviour the list always had, and there is exactly
// one place that owns the polling rule.
// ─────────────────────────────────────────────────────────────────────────────

export function useManagedSecretsData() {
  const [data, setData] = useState<ManagedSecretsResponse | null>(null)
  const [loading, setLoading] = useState(true)

  // L13 (code review): latest-wins request sequencing. Two load() calls can
  // be in flight at the same time — the 30-second background tick and an
  // explicit post-Sync/post-Refresh-all reload a click just triggered are
  // the real case — and nothing guarantees responses arrive in the order
  // the requests were sent. loadSeqRef is bumped on every call; a response
  // only gets applied if it's still the most recently STARTED one when it
  // resolves.
  const loadSeqRef = useRef(0)
  const load = useCallback(() => {
    const seq = ++loadSeqRef.current
    return getManagedSecrets()
      .then((res) => {
        if (loadSeqRef.current === seq) setData(res)
      })
      .catch(() => {
        if (loadSeqRef.current === seq) setData(null)
      })
      .finally(() => {
        if (loadSeqRef.current === seq) setLoading(false)
      })
  }, [])

  useEffect(() => {
    load()
  }, [load])

  // I2 — keeps itself fresh: re-reads every 30 seconds while the tab is
  // actually visible, and pauses the moment it isn't.
  useEffect(() => {
    const REFRESH_INTERVAL_MS = 30_000
    let intervalId: ReturnType<typeof setInterval> | undefined

    const stop = () => {
      if (intervalId !== undefined) {
        clearInterval(intervalId)
        intervalId = undefined
      }
    }
    const start = () => {
      if (intervalId !== undefined) return
      intervalId = setInterval(load, REFRESH_INTERVAL_MS)
    }

    if (document.visibilityState !== 'hidden') start()

    const onVisibilityChange = () => {
      if (document.visibilityState === 'hidden') stop()
      else start()
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [load])

  return { data, loading, load }
}

// ─────────────────────────────────────────────────────────────────────────────
// The page
// ─────────────────────────────────────────────────────────────────────────────

const VALID_STATES: string[] = [...CHIP_ORDER]
const VALID_SORT_KEYS: string[] = ['name', 'namespace', 'addon', 'cluster', 'source', 'state']

/**
 * SSF-9 — expanded groups and scroll position aren't URL-backed (a Record
 * of group-key booleans and a pixel offset don't belong in a shareable
 * link the way a filter does), so they ride in sessionStorage instead,
 * keyed by the list's OWN query string — the same string carried to the
 * detail page as location.state.listSearch. Saved right before navigating
 * to a row's detail page (openRowDetail below), restored on mount if the
 * page comes back with that exact same query string. Best-effort only:
 * sessionStorage can be unavailable (private browsing) or full, and losing
 * a scroll position is a nicety missed, never a broken page.
 */
function secretSyncScrollKey(areaScope: string, query: string): string {
  return `sharko:secret-sync:scroll:${areaScope}${query}`
}
function secretSyncGroupsKey(areaScope: string, query: string): string {
  return `sharko:secret-sync:groups:${areaScope}${query}`
}

/** Which Secrets subpage this page is rendering (Secrets-area rename, SN-3). */
export type SecretsArea = 'connections' | 'addons'

// The subpages' own words — the approved names and one-sentence
// descriptions, exactly as decided. Nothing here may suggest Sharko lists
// every Kubernetes Secret in a cluster.
const AREA_HEADER: Record<SecretsArea, { title: string; description: string }> = {
  connections: {
    title: 'Cluster connections',
    description: 'Secrets Sharko uses to register clusters with Argo CD.',
  },
  addons: {
    title: 'Addon secrets',
    description: 'Secrets Sharko delivers from configured backends to addons on remote clusters.',
  },
}

export function ManagedSecrets({ area }: { area?: SecretsArea } = {}) {
  const navigate = useNavigate()
  // sessionStorage scope for this page's saved scroll/groups — the two
  // subpages must not restore each other's state when their query strings
  // happen to match (both empty is the common case). The legacy unified
  // mode keeps the pre-split key shape.
  const areaScope = area ? `${area}:` : ''
  const { data, loading, load } = useManagedSecretsData()
  // SSF-9 — the table's own scroll container (see the "max-h-[65vh]
  // overflow-y-auto" wrapper below), so openRowDetail can read its current
  // scrollTop before navigating away and the restore effect further down
  // can write it back on return.
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const [refreshingAll, setRefreshingAll] = useState(false)
  const [busyRows, setBusyRows] = useState<Record<string, boolean>>({})
  const [syncTarget, setSyncTarget] = useState<UnifiedRow | null>(null)
  const [syncing, setSyncing] = useState(false)
  // leftover-secrets S1.2 — the orphaned-row Delete confirm target, same
  // pattern as syncTarget above: set it to open the ConfirmationModal,
  // null it to close.
  const [deleteTarget, setDeleteTarget] = useState<UnifiedRow | null>(null)
  const [deleting, setDeleting] = useState(false)

  // B3 — the active chip filter, the search text, the sort, and the group
  // choice all live in the URL (?state=, ?q=, ?sort=, ?dir=, ?group=) so
  // the page can be reloaded, bookmarked, shared, and reached from
  // elsewhere (the engine error below deep-links into a filtered view of
  // one cluster) without losing state, and so the back button actually
  // goes back to the previous filter instead of out of the page. SSF-9:
  // the SELECTED row is no longer one of these — clicking a row now
  // navigates to its own full page (/secret-sync/<key>) instead of
  // writing ?row= and opening a drawer here; carrying this exact query
  // string forward (openRowDetail below) is what lets that page's "Back
  // to Secret Sync" link restore every one of these params.
  const [searchParams, setSearchParams] = useSearchParams()
  // design-secret-sync-visual-pass bug fix: a handler that changes TWO
  // params at once (e.g. clear one filter while setting another) used to
  // build the next URLSearchParams off the outer `searchParams` closure —
  // two updateParams calls in the same synchronous handler both read the
  // SAME stale snapshot, so the second call's setSearchParams overwrote the
  // first call's change instead of building on it. setSearchParams'
  // functional-updater form (same shape as React's setState updater) reads
  // the router's own up-to-date params on each call, so sequential calls
  // in one handler compose correctly instead of racing each other.
  const updateParams = useCallback(
    (mutate: (p: URLSearchParams) => void) => {
      setSearchParams(
        (prev) => {
          const params = new URLSearchParams(prev)
          mutate(params)
          return params
        },
        { replace: true },
      )
    },
    [setSearchParams],
  )

  const [stateFilter, setStateFilterState] = useState<ResourceStatus | 'all'>(() => {
    const v = searchParams.get('state')
    return v && VALID_STATES.includes(v) ? (v as ResourceStatus) : 'all'
  })
  const setStateFilter = useCallback(
    (next: ResourceStatus | 'all') => {
      setStateFilterState(next)
      updateParams((p) => {
        if (next === 'all') p.delete('state')
        else p.set('state', next)
      })
    },
    [updateParams],
  )

  const [search, setSearchState] = useState(() => searchParams.get('q') ?? '')
  const setSearch = useCallback(
    (next: string) => {
      setSearchState(next)
      updateParams((p) => {
        if (next) p.set('q', next)
        else p.delete('q')
      })
    },
    [updateParams],
  )

  // SSF-9 `?row=` compatibility: a row used to be selected by writing
  // ?row=<key> to THIS page's own URL and opening a drawer over the list.
  // Now it's a full page at /secret-sync/<key> — an old bookmark or shared
  // link carrying ?row= must still land somewhere real, so this redirects
  // it there on mount (replace, so it doesn't leave the dead ?row= URL in
  // history) rather than silently ignoring the param. Runs once: this
  // page's own code never writes ?row= anymore, so there is nothing to
  // react to on later renders.
  useEffect(() => {
    const legacyRow = searchParams.get('row')
    if (!legacyRow) return
    const rest = new URLSearchParams(searchParams)
    rest.delete('row')
    const qs = rest.toString()
    navigate(`/secret-sync/${encodeURIComponent(legacyRow)}`, { replace: true, state: { listSearch: qs } })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // G2 — "Group by" lives in the URL too (?group=addon|cluster), for the
  // same reasons the chip filter and the search text do: reloadable,
  // bookmarkable, back-button-safe. `none` is the default and is never
  // written to the URL.
  const [groupBy, setGroupByState] = useState<GroupBy>(() => {
    const v = searchParams.get('group')
    // SN-3: on the Cluster connections subpage nothing is an addon, so
    // grouping by addon would only ever produce the old "not an addon"
    // bucket — the control below doesn't offer it there, and a stale
    // ?group=addon link falls back to the flat list.
    if (v === 'addon' && area === 'connections') return 'none'
    return v === 'addon' || v === 'cluster' ? v : 'none'
  })
  const setGroupBy = useCallback(
    (next: GroupBy) => {
      setGroupByState(next)
      updateParams((p) => {
        if (next === 'none') p.delete('group')
        else p.set('group', next)
      })
    },
    [updateParams],
  )
  // Which group parents are open (G4). The DEFAULT is computed, not stored:
  // a group holding any row that isn't in_sync opens itself (see
  // groupHasIssues); an explicit click on a group's header overrides that
  // default for that one group key for the rest of the session — the
  // user's own choice always wins over the computed default, in either
  // direction (an auto-open group can be collapsed, a quiet one opened).
  // SSF-9: seeded from sessionStorage on mount so returning from a row's
  // detail page (via the Back link or browser Back, both of which remount
  // this component) comes back with whichever groups were open, not reset
  // to the computed default.
  const [groupOverrides, setGroupOverrides] = useState<Record<string, boolean>>(() => {
    try {
      const raw = window.sessionStorage.getItem(secretSyncGroupsKey(areaScope, searchParams.toString()))
      return raw ? (JSON.parse(raw) as Record<string, boolean>) : {}
    } catch {
      return {}
    }
  })
  const isGroupExpanded = useCallback(
    (group: RowGroup) => (group.key in groupOverrides ? groupOverrides[group.key] : groupHasIssues(group)),
    [groupOverrides],
  )
  const toggleGroup = useCallback((group: RowGroup) => {
    setGroupOverrides((g) => ({ ...g, [group.key]: !(group.key in g ? g[group.key] : groupHasIssues(group)) }))
  }, [])

  // G1 — Addon and Source are filterable columns, same URL-persisted
  // pattern the chip filter and search text already use. Empty string
  // means "no filter", the same convention `search` uses, and is never
  // written to the URL.
  const [addonFilter, setAddonFilterState] = useState<string>(() => searchParams.get('addon') ?? '')
  const setAddonFilter = useCallback(
    (next: string) => {
      setAddonFilterState(next)
      updateParams((p) => {
        if (next) p.set('addon', next)
        else p.delete('addon')
      })
    },
    [updateParams],
  )
  const [sourceFilter, setSourceFilterState] = useState<string>(() => searchParams.get('source') ?? '')
  const setSourceFilter = useCallback(
    (next: string) => {
      setSourceFilterState(next)
      updateParams((p) => {
        if (next) p.set('source', next)
        else p.delete('source')
      })
    },
    [updateParams],
  )

  // design-secret-sync-visual-pass, section 3 — the List | Tiles toggle.
  // URL param `view` wins when present (same B3 pattern as every other
  // control on this page — reloadable, bookmarkable, back-button-safe);
  // `sharko-secret-sync-view` in localStorage is read ONLY when the URL has
  // no `view` param, so the page reopens the way he left it while a shared
  // link with an explicit `?view=` still shows exactly what was shared.
  // `list` is the default and is never written to the URL or localStorage.
  const VIEW_STORAGE_KEY = 'sharko-secret-sync-view'
  const [view, setViewState] = useState<'list' | 'tiles'>(() => {
    const v = searchParams.get('view')
    if (v === 'tiles' || v === 'list') return v
    return window.localStorage.getItem(VIEW_STORAGE_KEY) === 'tiles' ? 'tiles' : 'list'
  })
  const setView = useCallback(
    (next: 'list' | 'tiles') => {
      setViewState(next)
      window.localStorage.setItem(VIEW_STORAGE_KEY, next)
      updateParams((p) => {
        if (next === 'list') p.delete('view')
        else p.set('view', next)
      })
    },
    [updateParams],
  )

  // The connection-vs-values narrowing. No select control (unlike
  // addon/source) — it renders as one dismissible pill instead, same as
  // every other single-value filter this page has never needed a <select>
  // for. Applied in the `filtered` memo below, same as addonFilter/
  // sourceFilter. Reached via a bookmarked/shared ?kind= link (secret
  // tiles v2 removed the box click-through that used to set it).
  // HL-2: on the canonical subpages the route itself already says which
  // kind shows, so a leftover ?kind= from an old link is ignored completely
  // — it used to fight the route (/secrets/connections?kind=values rendered
  // an empty list). The effect below also removes it from the URL. Only the
  // /secret-sync compatibility redirect (App.tsx) reads it, to pick which
  // subpage an old link lands on. Legacy unified mode (no area) keeps the
  // filter as before.
  const [kindFilter, setKindFilterState] = useState<'' | 'connection' | 'values'>(() => {
    if (area) return ''
    const v = searchParams.get('kind')
    return v === 'connection' || v === 'values' ? v : ''
  })
  useEffect(() => {
    if (area && searchParams.get('kind') !== null) updateParams((p) => p.delete('kind'))
  }, [area, searchParams, updateParams])
  const setKindFilter = useCallback(
    (next: '' | 'connection' | 'values') => {
      setKindFilterState(next)
      updateParams((p) => {
        if (next) p.set('kind', next)
        else p.delete('kind')
      })
    },
    [updateParams],
  )

  // SSF-9: sort joins the URL-backed params above — it used to be plain
  // component state, which meant navigating to a row's detail page and
  // back (an unmount/remount, not an in-place drawer close) lost whatever
  // sort the reader had picked. 'state' (worst-first, the default sort)
  // and 'asc' are never written to the URL, matching every other filter's
  // "default stays out of the URL" convention.
  const [sortKey, setSortKeyState] = useState<SortKey>(() => {
    const v = searchParams.get('sort')
    return v && VALID_SORT_KEYS.includes(v) ? (v as SortKey) : 'state'
  })
  const setSortKey = useCallback(
    (next: SortKey) => {
      setSortKeyState(next)
      updateParams((p) => {
        if (next === 'state') p.delete('sort')
        else p.set('sort', next)
      })
    },
    [updateParams],
  )
  const [sortDir, setSortDirState] = useState<'asc' | 'desc'>(() => (searchParams.get('dir') === 'desc' ? 'desc' : 'asc'))
  const setSortDir = useCallback(
    (next: 'asc' | 'desc') => {
      setSortDirState(next)
      updateParams((p) => {
        if (next === 'asc') p.delete('dir')
        else p.set('dir', next)
      })
    },
    [updateParams],
  )

  const valuesSourceLabel = data?.addon_values_secret_source || 'secrets store'
  // connectionRows/addonRows/orphanedRows are read straight off `data`
  // inside the memo callback (not hoisted to their own `?? []` consts) —
  // a `?? []` literal outside a useMemo creates a fresh array reference
  // every render, which would defeat memoizing unifiedRows on them.
  const unifiedRows = useMemo(
    () =>
      buildUnifiedRows(
        data?.cluster_connection_secrets ?? [],
        data?.addon_values_secrets ?? [],
        data?.orphaned_secrets ?? [],
        valuesSourceLabel,
      ),
    [data, valuesSourceLabel],
  )

  // SN-3: the subpage's own rows. Cluster connections shows kind
  // 'connection'; Addon secrets shows kind 'values' — which includes the
  // leftover ("orphaned") rows, folded in as 'values' on purpose exactly
  // as before (see buildUnifiedRows). Everything downstream — chips,
  // counts, filter options, the empty state, the honest footer count —
  // reads this scoped set, so nothing on one subpage counts or mentions
  // the other's rows. Legacy unified mode (no area) scopes nothing.
  const areaRows = useMemo(() => {
    if (!area) return unifiedRows
    const kind = area === 'connections' ? 'connection' : 'values'
    return unifiedRows.filter((r) => r.kind === kind)
  }, [unifiedRows, area])

  // B1 fix: search narrows the rows chip COUNTS are computed over — the
  // chip filter itself must NOT be part of that computation, or selecting
  // a chip would make every other chip (and itself, after a search) read
  // 0.
  const searchFiltered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return areaRows
    return areaRows.filter((r) => matchesSearch(r, q))
  }, [areaRows, search])

  const counts = useMemo(() => {
    const c: Record<ResourceStatus, number> = { in_sync: 0, out_of_sync: 0, missing: 0, orphaned: 0, foreign: 0, unknown: 0 }
    for (const r of searchFiltered) c[toResourceStatus(r.state)]++
    return c
  }, [searchFiltered])

  // G1 — the option lists for the Addon/Source filter selects come off
  // EVERY row (unifiedRows), not the search-narrowed set — an option
  // shouldn't vanish out from under a reader mid-search just because their
  // typing temporarily excluded every row of that addon.
  const addonOptions = useMemo(() => {
    const set = new Set<string>()
    for (const r of areaRows) if (r.addon) set.add(r.addon)
    return [...set].sort()
  }, [areaRows])
  const sourceOptions = useMemo(() => {
    const set = new Set<string>()
    for (const r of areaRows) set.add(r.sourceLabel)
    return [...set].sort()
  }, [areaRows])

  const filtered = useMemo(() => {
    let rows = searchFiltered
    if (stateFilter !== 'all') rows = rows.filter((r) => toResourceStatus(r.state) === stateFilter)
    if (addonFilter) rows = rows.filter((r) => r.addon === addonFilter)
    if (sourceFilter) rows = rows.filter((r) => r.sourceLabel === sourceFilter)
    if (kindFilter) rows = rows.filter((r) => r.kind === kindFilter)
    return rows
  }, [searchFiltered, stateFilter, addonFilter, sourceFilter, kindFilter])

  const sorted = useMemo(() => {
    const copy = [...filtered]
    copy.sort((a, b) => {
      const cmp = compareRows(a, b, sortKey)
      return sortDir === 'asc' ? cmp : -cmp
    })
    return copy
  }, [filtered, sortKey, sortDir])

  // G2 — grouping. `none` is the default and is today's flat list,
  // unchanged. The chips/filters narrow which rows are in `sorted`; the
  // table itself scrolls over every one of them (I2 — paging removed, a
  // sticky header instead of pages), so grouping no longer has to worry
  // about a group getting split in half across a page boundary either.
  const groups = useMemo(() => buildRowGroups(sorted, groupBy), [sorted, groupBy])

  const grouped = groupBy !== 'none'

  // B2 (carried through I2's paging removal) — an honest count line: says
  // the real total, and says plainly when a filter has narrowed it below
  // everything Sharko manages. Grouped, it counts groups and SAYS it
  // counts groups — a bare number with no noun would be exactly the kind
  // of quietly-wrong line this page keeps refusing to print. Secret tiles
  // v2: tiles now shares `groups`/`groupBy` with list view instead of
  // forcing its own addon grouping, so this line reads the same whichever
  // view is on screen — "None" means neither view is grouped, "Addon"/
  // "Cluster" means both are, together.
  const hasActiveFilter = stateFilter !== 'all' || search.trim() !== ''
  const summaryGrouped = grouped
  const unit = groupBy === 'addon' ? 'addons' : 'clusters'
  const summaryGroupCount = groups.length
  const secretWord = sorted.length === 1 ? 'secret' : 'secrets'
  const secretsSummary =
    sorted.length === 0
      ? hasActiveFilter
        ? `No secrets match this filter (${areaRows.length} total)`
        : 'No secrets'
      : summaryGrouped
        ? hasActiveFilter
          ? `${summaryGroupCount} ${unit}, ${sorted.length} ${secretWord} (filtered from ${areaRows.length})`
          : `${summaryGroupCount} ${unit}, ${sorted.length} ${secretWord}`
        : hasActiveFilter
          ? `${sorted.length} ${secretWord} (filtered from ${areaRows.length})`
          : `${sorted.length} ${secretWord}`

  const handleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortDir(sortDir === 'asc' ? 'desc' : 'asc')
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const handleChipClick = (status: ResourceStatus) => {
    setStateFilter(stateFilter === status ? 'all' : status)
  }

  // The red engine error names a cluster — clicking it clears any state
  // filter and searches for that cluster, so the table narrows down to
  // exactly the row(s) the error is about.
  const filterToCluster = useCallback(
    (cluster: string) => {
      setStateFilter('all')
      setSearch(cluster)
    },
    [setStateFilter, setSearch],
  )

  const handleRefreshRow = useCallback(
    async (row: UnifiedRow) => {
      setBusyRows((b) => ({ ...b, [row.key]: true }))
      try {
        if (row.kind === 'connection') {
          await reconcileCluster(row.cluster)
          showToast(connectionRefreshToast(row.cluster), 'success')
        } else {
          const result = await refreshAddonValuesSecret(row.cluster, row.addon!)
          showToast(result.message, 'success')
        }
        load()
      } catch (err) {
        showToast(err instanceof Error ? err.message : 'Failed to trigger refresh', 'error')
      } finally {
        setBusyRows((b) => {
          const next = { ...b }
          delete next[row.key]
          return next
        })
      }
    },
    [load],
  )

  // Refresh all drives the CHECK on both engines and writes nothing (P1-A
  // A3). Values secrets go to POST /secrets/check — NOT
  // triggerSecretsReconcile, which runs the pass that creates and rotates
  // secrets; a button labelled "Refresh" must never do that. Connection
  // secrets go to POST /clusters/{name}/reconcile, which is now the
  // read-only check and covers every cluster despite taking one name in its
  // path (see internal/api/clusters_reconcile.go), so one call with any
  // connection row's cluster checks them all.
  const handleRefreshAll = async () => {
    setRefreshingAll(true)
    try {
      const tasks: Promise<unknown>[] = [checkAllAddonValuesSecrets()]
      const firstConnectionRow = data?.cluster_connection_secrets?.[0]
      if (firstConnectionRow) {
        tasks.push(reconcileCluster(firstConnectionRow.cluster))
      }
      await Promise.allSettled(tasks)
    } finally {
      setRefreshingAll(false)
      load()
    }
  }

  const handleConfirmSync = async () => {
    if (!syncTarget) return
    setSyncing(true)
    try {
      if (syncTarget.kind === 'connection') {
        const result = await resyncClusterLabels(syncTarget.cluster)
        showToast(result.message, 'success')
      } else {
        const result = await syncAddonValuesSecret(syncTarget.cluster, syncTarget.addon!)
        showToast(result.message, 'success')
      }
      setSyncTarget(null)
      load()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Sync failed', 'error')
    } finally {
      setSyncing(false)
    }
  }

  // leftover-secrets S1.2 — the orphaned-row Delete, confirmed. Deletion is
  // never automatic: this only ever runs after the ConfirmationModal's
  // explicit confirm, naming the exact secret being deleted. Refetches the
  // list afterwards — the same `load()` every other write on this page
  // already calls. SSF-9: deleting from a row's own ⋯ menu happens right
  // here on the list, so there's no detail page open on the deleted row to
  // navigate away from — that case lives in SecretDetailPage.tsx instead.
  const handleConfirmDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      const result = await deleteOrphanedSecret(deleteTarget.cluster, deleteTarget.secretNamespace ?? '', deleteTarget.secretName ?? '')
      showToast(result.message || 'Secret deleted.', 'success')
      setDeleteTarget(null)
      load()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to delete secret', 'error')
    } finally {
      setDeleting(false)
    }
  }

  // SSF-9 — clicking a row (or a tile) now navigates to its own full page
  // instead of opening a drawer in place. Two things ride along:
  //   - the list's OWN query string, as router location.state.listSearch —
  //     the detail page's "Back to Secret Sync" link (and its own header)
  //     use it to land back on exactly this filtered/sorted/grouped view;
  //     browser Back needs no help (it's just history).
  //   - a best-effort save of the two things that are NOT in the URL —
  //     scroll position and which groups are expanded — into sessionStorage
  //     under that same query string, restored on the way back (the
  //     groupOverrides initializer above, and the effect below).
  const openRowDetail = useCallback(
    (row: UnifiedRow) => {
      const query = searchParams.toString()
      try {
        if (scrollContainerRef.current) {
          window.sessionStorage.setItem(secretSyncScrollKey(areaScope, query), String(scrollContainerRef.current.scrollTop))
        }
        window.sessionStorage.setItem(secretSyncGroupsKey(areaScope, query), JSON.stringify(groupOverrides))
      } catch {
        // sessionStorage can be unavailable (private browsing) — losing the
        // scroll/group restore is a nicety missed, never a broken page.
      }
      // SN-3/SN-4: on a subpage a row opens its own per-kind detail route.
      // A leftover ("orphaned") row has no addon of its own to name, so its
      // detail URL carries `namespace/name` as the last segment instead —
      // SecretDetailPage matches it back the same way. Legacy unified mode
      // keeps the old /secret-sync/<key> shape the tests pin.
      let detailHref: string
      if (!area) {
        detailHref = `/secret-sync/${encodeURIComponent(row.key)}`
      } else if (row.kind === 'connection') {
        detailHref = `/secrets/connections/${encodeURIComponent(row.cluster)}`
      } else if (row.key.startsWith('orphaned-')) {
        detailHref = `/secrets/addons/${encodeURIComponent(row.cluster)}/${encodeURIComponent(
          `${row.secretNamespace ?? ''}/${row.secretName ?? ''}`,
        )}`
      } else {
        detailHref = `/secrets/addons/${encodeURIComponent(row.cluster)}/${encodeURIComponent(row.addon ?? '')}`
      }
      navigate(detailHref, { state: { listSearch: query } })
    },
    [navigate, searchParams, groupOverrides, area, areaScope],
  )

  // SSF-9 — restores the scroll position openRowDetail saved, once the
  // rows this same query string produces are actually on screen to scroll
  // to (there's nothing to scroll while `loading` is still true). Runs
  // once per mount — this page's own filtering/sorting never changes the
  // query string without a full navigate, so there is no later moment this
  // needs to re-fire.
  useEffect(() => {
    if (loading) return
    try {
      const saved = window.sessionStorage.getItem(secretSyncScrollKey(areaScope, searchParams.toString()))
      if (saved && scrollContainerRef.current) {
        scrollContainerRef.current.scrollTop = Number(saved)
      }
    } catch {
      // sessionStorage can be unavailable — nothing to restore, nothing broken.
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading])

  return (
    <div className="space-y-5">
      {/* H1 word/face pass (gitops-proud P4-H): the h1 goes back to the
          house 24px title every other list page uses (Clusters, Addons,
          Settings, ...) — the old H5 shrink made this page read like a
          panel inside a bigger page instead of a page of its own. H4's one
          calm permanent line replaces the old "no subtitle" rule: it is
          not an explanation of the layout (which the old comment rightly
          worried would go stale) but a fact about what Sharko does here,
          which doesn't change when the table's columns do. */}
      <div>
        {/* SN-3: each subpage carries its own approved name and its own
            one-sentence description — never a rolled-up area intro. The
            legacy unified mode (test-only, unreachable from the app) keeps
            the pre-split security line under the plain area name. */}
        <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">
          {area ? AREA_HEADER[area].title : 'Secrets'}
        </h1>
        <p className="mt-1 text-sm text-[#3a6a8a] dark:text-gray-400">
          {area
            ? AREA_HEADER[area].description
            : 'Sharko keeps these secrets in sync automatically. Git defines what should exist. Values come from your secret store.'}
        </p>
      </div>

      {/* SN-3: the navigation between the two subpages — real links that
          look like tabs, directly under the title, above the page's own
          controls. */}
      {area && <SecretsSubnav />}

      {/* Engines quiet strip — see the block comment on EngineStat above. */}
      <div className="flex flex-wrap items-start justify-between gap-3 border-y border-[#6aade0] py-2 dark:border-gray-800">
        <div className="flex flex-wrap divide-x divide-[#6aade0] dark:divide-gray-800">
          {/* SN-3: each subpage shows only its own engine — the other
              engine's stats belong to the other subpage now. Legacy
              unified mode shows both, as before. */}
          {(!area || area === 'connections') && (
            <EngineStat label="Cluster connections" kind="connection" info={data?.engines.cluster_connection} onErrorClick={filterToCluster} />
          )}
          {(!area || area === 'addons') && (
            <EngineStat label="Addon values" kind="values" info={data?.engines.addon_values} onErrorClick={filterToCluster} />
          )}
        </div>
        <RoleGuard roles={['admin', 'operator']}>
          <div className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={handleRefreshAll}
              disabled={refreshingAll}
              data-testid="refresh-all"
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#6aade0] bg-white px-2.5 py-1 text-xs font-medium text-[#0a3a5a] hover:bg-[#e0f0ff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              <RefreshCw className={`h-3 w-3 ${refreshingAll ? 'animate-spin' : ''}`} />
              Check all now
            </button>
            <InfoHint text={REFRESH_ALL_HINT} label="What does Check all now do?" />
          </div>
        </RoleGuard>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <FilterChip status="all" label="All" count={searchFiltered.length} active={stateFilter === 'all'} onClick={() => setStateFilter('all')} />
        {CHIP_ORDER.map((status) => (
          <FilterChip
            key={status}
            status={status}
            label={statusLabel(status)}
            count={counts[status]}
            active={stateFilter === status}
            onClick={() => handleChipClick(status)}
          />
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 220, maxWidth: 360 }}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a] dark:text-gray-500" />
          <input
            type="text"
            placeholder="Search by cluster, addon, secret name, or namespace..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-[#6aade0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-gray-500"
          />
        </div>
        {/* The connection-vs-values narrowing (?kind=connection) has no
            select control; it shows itself as one dismissible pill (the
            FilterChip visual, navy active style) rather than a third
            <select>, same B3 pattern (reloadable, bookmarkable) as every
            other filter here. Clicking × clears it. Secret tiles v2: a box
            click no longer sets this — a box just opens the row it stands
            for — so this pill is reached only via a shared/bookmarked
            ?kind= link now. */}
        {kindFilter === 'connection' && (
          <button
            type="button"
            onClick={() => setKindFilter('')}
            data-testid="kind-filter-pill"
            className="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ring-1 text-[#1a3d5c] underline decoration-2 underline-offset-4 ring-[#1a3d5c] dark:text-blue-400 dark:ring-blue-400"
          >
            Cluster connections
            <X className="h-3 w-3" aria-hidden="true" />
          </button>
        )}
        {/* SSF-3 (Secret Sync finish pass) — Addon and Compared with used
            to be two separate <select>s sitting in the toolbar; they fold
            into one Filters popover now (existing shadcn Popover, no new
            component system) so the toolbar reads as one row of controls
            instead of a growing line of selects. Same two testids on the
            same two <select>s, same state, same URL params (?addon=,
            ?source=) — only WHERE they render changed, never what
            selecting one does. G1's own reasoning still holds: options are
            drawn from every row (unifiedRows) so the list never shrinks out
            from under a search, and design-secret-sync-visual-pass
            section 2's rule still holds too — each option's visible text
            matches the column cell, its real VALUE stays the full
            sourceLabel so the filter keeps matching the exact server
            string. */}
        <Popover>
          <PopoverTrigger asChild>
            <button
              type="button"
              data-testid="filters-button"
              aria-label="Filters"
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#6aade0] bg-white px-2.5 py-1 text-xs font-medium text-[#2a5a7a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              <Filter className="h-3.5 w-3.5" aria-hidden="true" />
              Filters
              {(addonFilter ? 1 : 0) + (sourceFilter ? 1 : 0) > 0 && (
                <span
                  className="rounded-full bg-[#1a3d5c] px-1.5 text-[10px] font-semibold text-white dark:bg-blue-500"
                  data-testid="filters-active-count"
                >
                  {(addonFilter ? 1 : 0) + (sourceFilter ? 1 : 0)}
                </span>
              )}
            </button>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-64 space-y-3">
            <label className="flex flex-col gap-1 text-xs text-[#3a6a8a] dark:text-gray-400">
              Addon
              <select
                value={addonFilter}
                onChange={(e) => setAddonFilter(e.target.value)}
                data-testid="addon-filter-select"
                className="w-full rounded-lg border border-[#6aade0] bg-white py-1 pl-2 pr-6 text-xs text-[#2a5a7a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
              >
                <option value="">All</option>
                {addonOptions.map((a) => (
                  <option key={a} value={a}>
                    {a}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-[#3a6a8a] dark:text-gray-400">
              Compared with
              <select
                value={sourceFilter}
                onChange={(e) => setSourceFilter(e.target.value)}
                data-testid="source-filter-select"
                className="w-full rounded-lg border border-[#6aade0] bg-white py-1 pl-2 pr-6 text-xs text-[#2a5a7a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
              >
                <option value="">All</option>
                {sourceOptions.map((s) => (
                  <option key={s} value={s}>
                    {sourceShortLabel(s)}
                  </option>
                ))}
              </select>
            </label>
            {(addonFilter || sourceFilter) && (
              <button
                type="button"
                onClick={() => {
                  setAddonFilter('')
                  setSourceFilter('')
                }}
                data-testid="filters-clear"
                className="text-xs text-teal-700 hover:underline dark:text-teal-400"
              >
                Clear filters
              </button>
            )}
          </PopoverContent>
        </Popover>
        {/* G2 — Group by. `None` is the default and is the flat list this
            page has always shown; the other two fold the same rows under a
            parent line (list view) or a heading (tiles view — secret tiles
            v2) you click/scroll past. Shared by BOTH views now: tiles used
            to force its own addon grouping and hide this control, but a box
            stands for one secret, not a whole addon, so tiles reads the
            same control list view does — "None" gives a flat grid of boxes,
            "Addon"/"Cluster" give the same headings list view's group
            parents would show. */}
        <div className="flex items-center gap-1.5">
          <span className="text-xs text-[#3a6a8a] dark:text-gray-400">Group by</span>
          <div className="inline-flex overflow-hidden rounded-lg ring-1 ring-[#6aade0] dark:ring-gray-700">
            {(
              [
                ['none', 'Flat list'],
                // SN-3: on Cluster connections nothing is an addon —
                // grouping by addon there could only produce the old
                // "not an addon" bucket, which the split retires, so the
                // option isn't offered. Addon secrets and legacy unified
                // mode keep all three.
                ...(area === 'connections' ? [] : ([['addon', 'Addon']] as [GroupBy, string][])),
                ['cluster', 'Cluster'],
              ] as [GroupBy, string][]
            ).map(([value, label]) => (
              <button
                key={value}
                type="button"
                onClick={() => setGroupBy(value)}
                aria-pressed={groupBy === value}
                data-testid={`group-by-${value}`}
                className={`px-2.5 py-1 text-xs font-medium ${
                  groupBy === value
                    ? // G4: navy in both themes — the app's own "selected"
                      // colour (the sidebar's #1a3d5c), never StatusMark's
                      // teal, which reads as "in sync" next to a row that
                      // genuinely is.
                      'bg-[#1a3d5c] text-white'
                    : 'bg-white text-[#2a5a7a] hover:bg-[#e0f0ff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
        </div>
        {/* design-secret-sync-visual-pass, section 3 — the List | Tiles
            toggle, far right of the toolbar row, same segmented-pill
            pattern as Group by above. */}
        <div className="ml-auto flex items-center gap-1.5">
          <div className="inline-flex overflow-hidden rounded-lg ring-1 ring-[#6aade0] dark:ring-gray-700">
            <button
              type="button"
              onClick={() => setView('list')}
              aria-pressed={view === 'list'}
              aria-label="List view"
              data-testid="view-list"
              className={`p-1.5 ${
                view === 'list'
                  ? 'bg-[#1a3d5c] text-white'
                  : 'bg-white text-[#2a5a7a] hover:bg-[#e0f0ff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
              }`}
            >
              <List className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
            <button
              type="button"
              onClick={() => setView('tiles')}
              aria-pressed={view === 'tiles'}
              aria-label="Tiles view"
              data-testid="view-tiles"
              className={`p-1.5 ${
                view === 'tiles'
                  ? 'bg-[#1a3d5c] text-white'
                  : 'bg-white text-[#2a5a7a] hover:bg-[#e0f0ff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
              }`}
            >
              <LayoutGrid className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </div>
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-24">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-[#6aade0] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
        </div>
      ) : sorted.length === 0 ? (
        <div className="rounded-xl border border-border bg-card p-6 text-center text-sm text-[#3a6a8a] shadow-sm dark:text-gray-500">
          {/* SN-3: the empty line is scoped to the subpage — with the other
              subpage possibly holding rows, "not managing any secrets yet"
              would be untrue here, so each area names its own kind. */}
          {areaRows.length === 0
            ? area === 'connections'
              ? 'No cluster connection Secrets yet.'
              : area === 'addons'
                ? 'No addon Secrets yet.'
                : 'Sharko is not managing any secrets yet.'
            : 'No secrets match this filter.'}
        </div>
      ) : view === 'tiles' ? (
        // Secret tiles v2 — the SAME `sorted` rows and `groups` list view
        // reads. SSF-9: a box now navigates to the row's own full page,
        // same as a list row does — see openRowDetail.
        <SecretTiles rows={sorted} groups={groups} onRowClick={openRowDetail} />
      ) : (
        // H1 word/face pass (gitops-proud P4-H) gave the table frame a
        // two-width Sharko-blue ring; SSF-2 (Secret Sync finish pass)
        // softened that again — a thick coloured ring read as a box
        // fighting for attention on a page that's meant to be calm, so the
        // frame now uses the app's own `border-border`/`bg-card` tokens: a
        // thin, theme-aware hairline over a near-white (light) / dark
        // neutral (dark) surface, the same "calm card" shape ClusterCard
        // already uses elsewhere. Strong blue stays on the chips, Group by,
        // and List/Tiles — those are controls, not the page's background.
        //
        // I2 (gitops-proud P4-I): paging is gone — this outer div is now
        // the SCROLL container (max-h + overflow-y-auto) instead of just an
        // overflow-hidden frame, and the header inside is `sticky top-0` so
        // it stays put while the rows underneath it scroll. Every filtered
        // row is in the DOM at once; the chips/filters above narrow which
        // rows exist, scrolling browses the rest. SSF-9: the ref lets
        // openRowDetail save the scroll position before navigating to a
        // row's detail page, and the restore effect above puts it back.
        <div
          ref={scrollContainerRef}
          className="max-h-[65vh] overflow-y-auto overflow-x-auto rounded-xl border border-border bg-card shadow-sm"
        >
          {/* design-secret-sync-visual-pass, section 1/5: the table used to
              be table-fixed with explicit PIXEL widths on every column
              except NAME, which absorbed all remaining width — the only
              way the longest demo secret name
              (kube-prometheus-stack-grafana-admin, 35 chars, 295px)
              rendered uncut at 1280px. SSF-10: that "absorb all remaining
              width" habit was the bug, not the fix — on a wide screen every
              OTHER column stayed pinned at its measured px width while NAME
              kept ballooning, so the table read as one huge blank column
              next to five narrow ones. Every column (including NAME) now
              carries a PERCENTAGE width instead, so they all grow together
              as the table widens — NAME keeps ~25-30% of the table at any
              width, same as its old measured share at 1000px, instead of
              claiming 100% of anything extra. min-w-[1000px] still keeps
              sub-1280 windows scrolling (the frame's own overflow-x-auto)
              rather than crushing the columns below readable widths; NAME's
              own `truncate` + front-truncate trick + title attribute below
              still carries a long name's full text when it does clip. */}
          <Table className="table-fixed min-w-[1000px]">
            <TableHeader className="sticky top-0 z-10 border-b border-border bg-muted">
              <TableRow className="hover:bg-transparent">
                {/* H2/H3: Status moves next to Name (ArgoCD's own habit of
                    putting a resource's health right at the start of its
                    row); the header word is "Status", never "State". Walk
                    finding #140: Namespace is now its own sortable column
                    right after Name. design-secret-sync-visual-pass: the
                    LAST CHECKED column is gone (the fact lives in the
                    engine strip above and the panel's Zone D); SOURCE
                    became CHECKED AGAINST (section 2). SSF-10: every column
                    (Name included) carries a percentage width that sums to
                    100%, so the table stays balanced instead of one column
                    absorbing all growth. */}
                <SortableTh label="Name" sortKeyName="name" activeKey={sortKey} dir={sortDir} onSort={handleSort} className="w-[27%] px-1.5" />
                <SortableTh label="Namespace" sortKeyName="namespace" activeKey={sortKey} dir={sortDir} onSort={handleSort} className="w-[12%] px-1.5" />
                <SortableTh label="Status" sortKeyName="state" activeKey={sortKey} dir={sortDir} onSort={handleSort} className="w-[12%] px-1.5" />
                <SortableTh label="Addon" sortKeyName="addon" activeKey={sortKey} dir={sortDir} onSort={handleSort} className="w-[12%] px-1.5" />
                <SortableTh label="Cluster" sortKeyName="cluster" activeKey={sortKey} dir={sortDir} onSort={handleSort} className="w-[13%] px-1.5" />
                <SortableTh label="Compared with" sortKeyName="source" activeKey={sortKey} dir={sortDir} onSort={handleSort} className="w-[20%] px-1.5" />
                <TableHead className="w-[4%] px-1.5" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {grouped
                ? groups.map((group) => (
                    <Fragment key={group.key}>
                      <GroupHeaderRow
                        group={group}
                        expanded={isGroupExpanded(group)}
                        onToggle={() => toggleGroup(group)}
                      />
                      {isGroupExpanded(group) &&
                        group.rows.map((row) => (
                          <SecretTableRow
                            key={row.key}
                            row={row}
                            indented
                            busy={!!busyRows[row.key]}
                            onSelect={() => openRowDetail(row)}
                            onRefresh={() => handleRefreshRow(row)}
                            onRequestSync={() => setSyncTarget(row)}
                            onRequestDelete={() => setDeleteTarget(row)}
                          />
                        ))}
                    </Fragment>
                  ))
                : sorted.map((row) => (
                    <SecretTableRow
                      key={row.key}
                      row={row}
                      busy={!!busyRows[row.key]}
                      // `row` here is a plain element of `sorted` (a
                      // useMemo'd array, no ref involved); the identical
                      // grouped branch above (group.rows.map, same
                      // handler, same shape) isn't flagged, so this reads
                      // as a rule false-positive tied to the ternary/JSX
                      // shape here. SSF-9: openRowDetail reads
                      // scrollContainerRef.current, but only inside the
                      // click handler it returns — never during this
                      // render — which is exactly the shape the rule can't
                      // statically tell apart from a genuine render-time
                      // ref read.
                      // eslint-disable-next-line react-hooks/refs -- see comment above
                      onSelect={() => openRowDetail(row)}
                      onRefresh={() => handleRefreshRow(row)}
                      onRequestSync={() => setSyncTarget(row)}
                      onRequestDelete={() => setDeleteTarget(row)}
                    />
                  ))}
            </TableBody>
          </Table>
        </div>
      )}

      <div className="flex items-center justify-between">
        <span className="text-xs text-[#3a6a8a] dark:text-gray-400" data-testid="secrets-summary">
          {secretsSummary}
        </span>
      </div>

      <ConfirmationModal
        open={syncTarget !== null}
        onClose={() => setSyncTarget(null)}
        onConfirm={handleConfirmSync}
        title={
          // HL-1: the connection confirm carries the action's real name.
          syncTarget?.kind === 'connection'
            ? `Re-apply addon labels on "${syncTarget.cluster}"?`
            : syncTarget?.kind === 'values'
              ? `Sync secret for cluster "${syncTarget.cluster}", addon "${syncTarget.addon}"?`
              : 'Sync?'
        }
        description={syncConfirmDescription(syncTarget)}
        confirmText={syncConfirmButtonText(syncTarget?.kind)}
        loading={syncing}
      />

      {/* leftover-secrets S1.2 — the orphaned-row Delete confirm. Same
          pattern as Sync above: a page-level target state, a destructive
          ConfirmationModal, and cancel does nothing at all — no request is
          made unless this confirms. */}
      <ConfirmationModal
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleConfirmDelete}
        title={
          deleteTarget
            ? `Delete secret "${deleteTarget.secretNamespace ?? ''}/${deleteTarget.secretName ?? ''}"?`
            : 'Delete?'
        }
        description={deleteConfirmDescription(deleteTarget)}
        confirmText="Delete"
        destructive
        loading={deleting}
      />
    </div>
  )
}

export default ManagedSecrets
