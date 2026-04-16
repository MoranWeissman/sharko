# Addon Upgrade UX Improvements

## 1. Bootstrap app health visibility

**Problem:** Bootstrap health only shown on dashboard banner. Needs to be in Observability too.

**Plan:**
- [ ] Add bootstrap app card/section in Observability view
- [ ] Show: health status, sync status, last sync time, linked to ArgoCD UI

**Files:**
- `ui/src/views/Observability.tsx`
- `internal/service/observability.go` (if needs more data)

---

## 2. Version search in upgrade picker

**Problem:** User may know exact version they want (e.g. 0.12.5). Scrolling through "show more" is tedious.

**Plan:**
- [ ] Add search input at top of UpgradeVersionList: "Jump to version..."
- [ ] As user types, filter versions OR show "Analyze X.Y.Z" if exact match
- [ ] Even if version not in top 20, let user analyze it (backend supports any version)

**Files:**
- `ui/src/views/AddonDetail.tsx` (UpgradeVersionList component)

---

## 3. Smart upgrade recommendations "out of the box"

**Problem:** User sees latest version first, but may want "next stable" or "security-recommended". Currently showing latest + scrolling down through all versions.

**Plan:**
- [ ] Backend: Add endpoint `GET /upgrade/{addon}/recommendations` that returns:
  - `next_patch`: next patch version from current (e.g. 0.12.1 → 0.12.5)
  - `next_minor`: next minor from current (e.g. 0.12.1 → 0.13.x latest)
  - `latest_stable`: latest non-prerelease version
  - `security_recommended`: latest version (AI-enabled: ask AI for recommendation)
- [ ] Frontend: Add "Recommended Versions" section above the full version list
- [ ] Each recommendation has its own "Analyze" + "Upgrade" buttons

**Files:**
- `internal/api/upgrade.go` (new handler)
- `internal/service/upgrade.go` (recommendation logic)
- `ui/src/views/AddonDetail.tsx` (new RecommendedVersions component)
- `ui/src/services/api.ts`

---

## 4. Header "Upgrade" button not working

**Problem:** AddonDetail has an "Upgrade" button in the header next to "Remove" that does nothing when clicked.

**Plan:**
- [ ] Find the button, make it navigate to the Upgrade tab (`setActiveSection('upgrade')`)
- [ ] Or remove the button entirely if it's redundant with the nav panel

**Files:**
- `ui/src/views/AddonDetail.tsx`

---

## 5. Mandatory analyze before upgrade (UI-enforced only)

**Problem:** Users can upgrade without analyzing first, potentially missing conflicts or breaking changes.

**Plan:**
- [ ] In UI: remove the direct "Upgrade" button from version list
- [ ] Only show "Analyze" — after analysis, show "Upgrade to X" inside the analysis results (already exists)
- [ ] Note: API and CLI still allow direct upgrade without analyze (backward compat)

**Files:**
- `ui/src/views/AddonDetail.tsx` (UpgradeVersionList component)

---

## 6. Better upgrade progress indication

**Problem:** During upgrade, only shows "Upgrading to X.Y.Z..." spinner. User doesn't know what's happening.

**Plan:**
- [ ] Show step-by-step progress during upgrade:
  1. "Creating pull request..."
  2. "PR created: [link]. Waiting for merge..."
  3. "Waiting for ArgoCD sync..."
  4. "Deployed successfully!"
- [ ] After upgrade completes, auto-refresh addon detail to show new version
- [ ] On success, show a celebratory success banner with version before → after

**Files:**
- `ui/src/views/AddonDetail.tsx` (upgrade flow)
- Backend may need SSE/polling endpoint for upgrade status

---

## 7. Handle PR merge conflicts (405 Base branch modified)

**Problem:** User got "merge failed: 405 Base branch was modified. Review and try the merge again" when upgrading an addon. Auto-merge failed because another PR merged first.

**Plan:**
- [ ] Backend: On 405 merge conflict, retry merge up to 3 times with 2s backoff
- [ ] If still failing after retries, update the PR branch with latest main (rebase/merge), then retry merge
- [ ] Return clear error message if all retries fail, linking to PR for manual review

**Files:**
- `internal/orchestrator/git_helpers.go` (commitViaPR / merge logic)
- `internal/gitprovider/github_write.go` (merge PR method)

---

## 8. Ask AI button on addon upgrade errors

**Problem:** When addon upgrade fails, user sees an error but has no easy way to investigate.

**Plan:**
- [ ] Add "Ask AI" button to upgrade error display
- [ ] Pre-fill prompt: "Addon {name} upgrade to {version} failed with error: {error}. Why and how do I fix it?"
- [ ] AI should use tools to check PR status, ArgoCD app status, related apps

**Files:**
- `ui/src/views/AddonDetail.tsx` (upgrade error display)
