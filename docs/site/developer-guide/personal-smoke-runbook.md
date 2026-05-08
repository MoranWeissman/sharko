# Personal Smoke Runbook

> **Verified:** The mechanical Track A + Track B B.1–B.6 portions were last executed end-to-end on 2026-05-08 by `./scripts/sharko-dev.sh smoke` (which forwards to `scripts/smoke.sh` after auto-extracting credentials) against image `sharko:e2e` built from commit `96567f0e` (dev/v1.24-cleanup tip — V124-8.1 + V124-8.2 merged). 47/47 PASS in 14 s of script time, including the auto-extract of `$ADMIN_PW` and `$TOKEN`. The `argocd-token` subcommand (V124-9) was end-to-end validated on 2026-05-09 against the same `kind-sharko-e2e` cluster: cold-start (apiKey patch + argocd-server restart + port-forward recovery + login + token generate), eval-via-pipe `--export`, quiet mode, idempotency (1.7 s second-run vs 34 s cold), `--service-account` (patches `accounts.sharko` + `argocd-rbac-cm` policy.csv + token generate), and bearer-token usability against `https://localhost:18080/api/v1/account`. The hand-walked baseline that the scripts encode was last executed end-to-end on 2026-05-06 against image `sharko:runbook-verify` built locally from commit `95b51cad` (dev/v1.24-cleanup, V124-3.6+3.7+3.8 merged); Track B B.7 (ArgoCD UI) was walked manually then. Track B B.8 (Git connect → init → register cluster → addon → ArgoCD sync) requires a real GitHub PAT and remains script-exempt — it is marked with a warning in the section itself.

A hands-on, checkbox-driven smoke pass for the Sharko maintainer. This is **not** a reference doc — it's a list you literally check off, top to bottom, while running the product yourself.

If you want background on the test pyramid and what each layer is for, read [Testing Guide](testing-guide.md). This runbook is the in-the-moment companion: open it in one window, terminal + browser in the others, and walk it.

---

## Quick reference — `sharko-dev.sh` subcommand cheatsheet

The maintainer-DX automation is a single entry point with subcommand dispatch (V124-8.1). Use this as your first stop before walking the manual sections below.

| Scenario | Command |
|---|---|
| Bring up env from nothing (kind + ArgoCD + Sharko) | `./scripts/sharko-dev.sh up` |
| Install Sharko on existing kind cluster | `./scripts/sharko-dev.sh install` |
| Rebuild after a code change (existing install) | `./scripts/sharko-dev.sh rebuild` |
| Rebuild but auto-install if missing | `./scripts/sharko-dev.sh rebuild --auto-install` |
| Cleanup helm release (preserves cluster + ArgoCD) | `./scripts/sharko-dev.sh reset --yes` |
| Full teardown (deletes kind cluster) | `./scripts/sharko-dev.sh down --yes` |
| Get the current admin password | `./scripts/sharko-dev.sh creds` |
| Capture admin password into `$ADMIN_PW` | `eval "$(./scripts/sharko-dev.sh creds --export)"` |
| Login + capture `$ADMIN_PW` and `$TOKEN` | `eval "$(./scripts/sharko-dev.sh login --export)"` |
| Rotate admin password (verifies V124-7) | `./scripts/sharko-dev.sh rotate` |
| Generate ArgoCD account token (for wizard step 3) | `./scripts/sharko-dev.sh argocd-token` |
| Capture `$ARGOCD_TOKEN` + `$ARGOCD_URL` into shell | `eval "$(./scripts/sharko-dev.sh argocd-token --export)"` |
| Run the full smoke suite | `./scripts/sharko-dev.sh smoke` |
| Show current env state | `./scripts/sharko-dev.sh status` |
| Per-subcommand help | `./scripts/sharko-dev.sh <cmd> --help` |

!!! tip "Sourcing model — use eval-via-pipe, not `source`"
    `./scripts/sharko-dev.sh login --export` prints **only** `export` lines so you can run:
    ```bash
    eval "$(./scripts/sharko-dev.sh login --export)"
    ```
    This avoids any `set -e` / `set -u` leak into your interactive shell. The legacy `source scripts/dev-rebuild.sh` still works (V124-8.2 fixed the leak) but eval-via-pipe is the recommended pattern going forward.

---

## Why this exists

You've shipped 23 versions of Sharko. You've never personally driven the product end-to-end. The only way to find the ten-thousand-paper-cut bugs that unit tests and CI cannot catch is to be the user for two hours.

The goal of one full pass:

- One pass through **Track A** (Docker demo mode, Layer 5) — about 30 minutes.
- One pass through **Track B** (kind + ArgoCD, Layer 6) — about 60–90 minutes.
- File every rough edge into the [Bug Log](#bug-log-template) at the bottom. You're seeding the v1.24 hotfix bundle.

!!! note "Bugs found in the first smoke pass (2026-05-01) — historical reference"
    These were the three bugs found in the very first hands-on smoke pass and are listed here for context. Keep an eye out for regressions, but do not expect to reproduce them as-is.

    1. **Login page footer shows wrong version** (BUG-001) — fixed in the v1.24 hotfix bundle (V124-2.1).
    2. **`GET /api/v1/clusters` returns 500** with raw filesystem error string leaking to the UI (BUG-002) — fixed in the v1.24 hotfix bundle (V124-2.2).
    3. **Cluster list page goes blank after ~30 s** when the underlying error clears (BUG-003) — UI white-screen on background-refresh failure. Recovery still requires nav-and-back or hard refresh; tracked separately, not yet closed in the v1.24 hotfix bundle.

---

## What "good" looks like vs what to flag

A bug worth filing is anything in column B. Anything in column A is normal demo-mode quirk territory — note it but don't escalate.

| Surface | A — Expected demo quirk | B — Flag as bug |
|---|---|---|
| API | 404 on a path you guessed wrong | 500 on any `GET` against a known-registered route |
| API | 401 with a clean JSON error body when token is missing | 401 with HTML, stack trace, or empty body |
| API | 403 from a viewer-tier account on a write endpoint | 403 from an admin on a read endpoint |
| UI | "0 clusters" when nothing is registered | Blank page, broken layout, or raw backend error string visible |
| UI | A spinner that resolves within 2 seconds | A spinner that never resolves, or a page that flashes content then blanks |
| UI | Toast saying "saved" then disappearing | Console errors in DevTools, even if the page looks fine |
| CLI | `sharko foo --help` shows usage | `sharko foo --help` panics, shows nothing, or errors |
| CLI | A subcommand that says "not implemented yet" cleanly | A subcommand that segfaults or prints raw Go errors |

---

## What to do when you find a bug

Three steps, every time. This is the minimum payload that makes a bug actionable later:

1. **Capture the request/response** — for API, copy the full `curl` (with headers) and the response body. For UI, screenshot the page and open DevTools → Network → copy the failing request as cURL.
2. **Grab the relevant logs** — `docker logs sharko-smoke 2>&1 | tail -50` for Track A; `kubectl logs -n sharko deploy/sharko --tail=50` for Track B.
3. **Paste both into the [Bug Log](#bug-log-template)** below with the surface + severity + repro steps.

---

## Time budget

- **Track A (Demo mode)** — 30 minutes, first run. ~15 minutes once familiar.
- **Track B (Kind + ArgoCD)** — 60–90 minutes, first run, including kind/ArgoCD download time. ~30 minutes once images are cached.
- **Total** — about 2 hours of focused work for a complete first pass.

---

# Track A — Demo mode (Layer 5)

Self-contained, no cluster, no Helm. Boots in under 5 seconds. Use this to find the cheap bugs first.

!!! tip "Now automated by `./scripts/sharko-dev.sh smoke`"
    The mechanical CLI + API sweep portions of Track A (A.4 API checklist + A.6 CLI sweep) are codified into the smoke phases. The canonical entry is the subcommand dispatcher (V124-8.1):
    ```bash
    ./scripts/sharko-dev.sh smoke
    ```
    The dispatcher auto-extracts `$ADMIN_PW` and `$TOKEN` (via `creds` + `login`) if they're not already exported, then forwards to `scripts/smoke.sh` which walks the same set of checks against your kind cluster in ~30 seconds. The manual steps below remain for understanding/troubleshooting and for the human-only portions (A.5 UI sweep). See [Automation scripts](#automation-scripts) at the bottom of this doc.

    The wrappers forward to `scripts/dev-rebuild.sh` and `scripts/smoke.sh` — direct invocation of those still works for back-compat.

!!! warning "`admin/admin` is demo-mode only"
    Track A uses `admin/admin` because the demo container ships pre-seeded with that credential — it's a fixed-string default for friction-free local runs only. **Real Helm installs do NOT accept `admin/admin`.** They generate a random bootstrap password on first start (or accept an operator-supplied one). For real K8s installs see [Initial Credentials](../operator/installation.md#initial-credentials) in the operator install guide, and walk Track B (which uses the real bootstrap flow) instead.

## A.1 Prereqs

- [ ] Docker Desktop / Colima / Rancher Desktop is running

  ```bash
  docker version
  ```

  **Expected:** Both client and server versions print. **Flag if:** server section is missing — Docker daemon is not running.

- [ ] `jq` is installed

  ```bash
  brew install jq && jq --version
  ```

- [ ] `curl` is available (default on macOS)

  ```bash
  curl --version | head -1
  ```

- [ ] `hurl` is installed (optional but recommended — the existing testing guide didn't mention this prereq)

  ```bash
  brew install hurl && hurl --version
  ```

## A.2 Pull and run

You'll do this twice: once with the latest pre-release tag (what the docs are written against), once with `latest` (what users actually pull).

- [ ] Pull the v1.23.0-pre.0 image

  ```bash
  docker pull --platform linux/amd64 ghcr.io/moranweissman/sharko:1.23.0-pre.0
  ```

  **Expected:** image downloads or "Image is up to date". **Flag if:** `manifest unknown` or platform error — the multi-arch manifest is broken.

- [ ] Pull the `:latest` image

  ```bash
  docker pull --platform linux/amd64 ghcr.io/moranweissman/sharko:latest
  ```

  !!! note "Apple Silicon"
      `--platform linux/amd64` is **required** on M-series Macs. The Sharko image is single-arch (amd64). Without the flag Docker will pull but may fail to start, or run under emulation with surprising performance.

- [ ] Start the container in demo mode

  ```bash
  docker rm -f sharko-smoke 2>/dev/null
  docker run --rm -d \
    --platform linux/amd64 \
    --name sharko-smoke \
    -p 18080:8080 \
    ghcr.io/moranweissman/sharko:latest \
    sharko serve --demo --port 8080
  ```

  **Expected:** prints a container ID. **Flag if:** exits immediately — check `docker logs sharko-smoke`.

## A.3 Wait for ready

- [ ] Poll the health endpoint until 200

  ```bash
  for i in $(seq 1 30); do
    if curl -fsS http://localhost:18080/api/v1/health > /dev/null; then
      echo "ready after ${i}s"; break
    fi
    sleep 1
  done
  ```

  **Expected:** "ready after Ns" (typically 2–5 s). **Flag if:** loop times out at 30 s — the binary isn't binding to 8080 inside the container.

## A.4 API smoke checklist

Set the host once and grab a token. Keep this terminal open for the whole pass.

- [ ] Set the host variable

  ```bash
  HOST=http://localhost:18080
  ```

- [ ] `GET /api/v1/health`

  ```bash
  curl -fsS $HOST/api/v1/health | jq .
  ```

  **Expected:** 200, JSON with at least `status: "healthy"` and a `version` field.
  **Flag if:** `version` field is missing, empty, or doesn't match the image tag.

- [ ] `POST /api/v1/auth/login` (admin/admin — **demo mode only**, see callout at top of Track A)

  ```bash
  TOKEN=$(curl -fsS -X POST $HOST/api/v1/auth/login \
    -H 'content-type: application/json' \
    -d '{"username":"admin","password":"admin"}' | jq -r .token)
  echo "admin token: ${TOKEN:0:24}…"
  ```

  **Expected:** non-empty token string printed. **Flag if:** `null`, empty, or the login endpoint returns 4xx — demo seeding failed. **In a real Helm install** this would 401 — see [Initial Credentials](../operator/installation.md#initial-credentials).

- [ ] `GET /api/v1/catalog/addons`

  ```bash
  curl -fsS $HOST/api/v1/catalog/addons -H "authorization: Bearer $TOKEN" | jq '.addons | length'
  ```

  **Expected:** 200, a number around 45 (the embedded curated catalog). **Flag if:** 0, missing, or 500 — catalog embed broken.

- [ ] `GET /api/v1/clusters` — **KNOWN BUG**

  ```bash
  curl -sS -o /tmp/clusters.json -w '%{http_code}\n' $HOST/api/v1/clusters \
    -H "authorization: Bearer $TOKEN"
  cat /tmp/clusters.json | jq . 2>/dev/null || cat /tmp/clusters.json
  ```

  **Expected (correct behavior):** 200 with `{"clusters":[]}` or seeded demo clusters.
  **Currently observed:** 500 with raw `reading managed-clusters.yaml: file not found`.
  **Flag if:** still 500 → confirm bug #2 is still present. **If 200 →** the bug was fixed; mark it done in the bug list at the top.

- [ ] `GET /api/v1/config`

  ```bash
  curl -fsS $HOST/api/v1/config -H "authorization: Bearer $TOKEN" | jq .
  ```

  **Expected:** 200, JSON containing the demo provider and ArgoCD connection state. **Flag if:** the `argocd` field shows `connected: false` in demo mode.

- [ ] `GET /api/v1/audit`

  ```bash
  curl -fsS "$HOST/api/v1/audit?limit=10" -H "authorization: Bearer $TOKEN" | jq '.entries | length'
  ```

  **Expected:** 200, an array (may be empty on a fresh boot). **Flag if:** 500 or non-array shape.

- [ ] `GET /api/v1/operations/{id}` — there is no list endpoint, only per-ID lookup. Pick a fake ID and confirm the 404 path is clean.

  ```bash
  curl -sS -o /dev/null -w '%{http_code}\n' \
    $HOST/api/v1/operations/does-not-exist -H "authorization: Bearer $TOKEN"
  ```

  **Expected:** 404 with a JSON error body (verify with the same curl without `-o /dev/null`). **Flag if:** 500 or HTML.

- [ ] `GET /api/v1/notifications`

  ```bash
  curl -fsS $HOST/api/v1/notifications -H "authorization: Bearer $TOKEN" | jq .
  ```

  **Expected:** 200, JSON list (may be empty). **Flag if:** 500 or 404 (the route is registered).
  Note: there is **no** `/api/v1/notifications/providers` route — that path was a guess. If you wanted notification *channel* providers, those live under settings, not a separate endpoint.

- [ ] `POST /api/v1/init`

  ```bash
  curl -sS -X POST $HOST/api/v1/init \
    -H "authorization: Bearer $TOKEN" \
    -H 'content-type: application/json' \
    -d '{}' -w '\nstatus: %{http_code}\n'
  ```

  **Expected: ?** — first-pass behavior unknown. Most likely returns 409 ("already initialized") in demo mode, or 200 with a status object. Anything 5xx → flag.

- [ ] `GET /api/v1/fleet/status`

  ```bash
  curl -fsS $HOST/api/v1/fleet/status -H "authorization: Bearer $TOKEN" | jq .
  ```

  **Expected: ?** — first-pass behavior unknown. Should return a fleet rollup object (cluster counts, healthy/degraded). 2xx is good; 5xx flag.

- [ ] `GET /api/v1/repo/status`

  ```bash
  curl -fsS $HOST/api/v1/repo/status -H "authorization: Bearer $TOKEN" | jq .
  ```

  **Expected:** 200 with `{"initialized": true|false}`. **Flag if:** 500 or missing field.

- [ ] `GET /api/v1/users/me`

  ```bash
  curl -fsS $HOST/api/v1/users/me -H "authorization: Bearer $TOKEN" | jq .
  ```

  **Expected:** 200, current user object with `username: "admin"` and `role: "admin"`. **Flag if:** the response leaks a password hash or token.

- [ ] `POST /api/v1/auth/logout`

  ```bash
  curl -sS -X POST $HOST/api/v1/auth/logout \
    -H "authorization: Bearer $TOKEN" -w '\nstatus: %{http_code}\n'
  ```

  **Expected: ?** — first-pass behavior unknown. Most likely 200 / 204 with empty body. 5xx flag.

- [ ] Re-issue token for the rest of the pass

  ```bash
  TOKEN=$(curl -fsS -X POST $HOST/api/v1/auth/login \
    -H 'content-type: application/json' \
    -d '{"username":"admin","password":"admin"}' | jq -r .token)
  ```

  **Expected:** new token issued. **Flag if:** logout invalidated session in a way that prevents re-login.

## A.5 UI smoke checklist

Open <http://localhost:18080> in a real browser. Open DevTools → Console + Network. Watch both as you click.

For every page below the rubric is the same:

> **Expected:** page renders, no console errors (red), no network calls returning 4xx/5xx that the page doesn't recover from, no error toasts.
> **Flag if:** raw backend error string visible in the UI, blank page after error, broken layout, missing data where data should obviously exist, version mismatch in footer/header.

- [ ] **Login page** — verify the footer version string. **KNOWN BUG #1.**
  - Compare footer version against `curl $HOST/api/v1/health | jq .version`.
  - If they don't match → confirm bug. If they match → mark bug fixed.

- [ ] **Login submit** — admin/admin (**demo mode only**) → land on Dashboard.

- [ ] **Dashboard** — stats cards, attention items, PR widget all render. No "loading…" stuck spinners.

- [ ] **Catalog / Browse** — addon cards render, filters work, click-through opens detail.

- [ ] **AddonDetail** (click into one addon) — README renders, version picker populates, values editor opens.

- [ ] **Marketplace tab** on the Catalog page:
  - [ ] Browse (cards grid)
  - [ ] Search (filter by text)
  - [ ] Paste URL (paste a chart URL, validate)

- [ ] **Clusters page** — **KNOWN BUG #2 + #3.**
  - Watch for raw error string (bug #2) and the ~30-second blank-out (bug #3).
  - Try clicking around without refreshing — does navigating away and back recover?

- [ ] **Cluster Detail** — deep-link directly to a cluster route (e.g. `/clusters/demo-cluster`) even if list is empty. Should render a 404-style "not found" page, **not** a 500 or blank.

- [ ] **Audit log viewer** — entries render, filters work, SSE stream opens (Network tab → `audit/stream`).

- [ ] **Settings → Connections** — list, add modal opens, validation works.

- [ ] **Settings → Catalog Sources** — list embedded + remote sources, refresh button works.

- [ ] **Settings → Notifications** — channel config UI loads.

- [ ] **Settings → AI** — provider config form renders, "Test" button works (will likely fail in demo without keys — should fail cleanly with a toast, not a console error).

- [ ] **Observability / Dashboards** — embedded dashboards page renders, "+ Add" works.

- [ ] **Diagnose modal** — somewhere in the UI (Cluster Detail → Diagnose button). Modal opens, runs probes, renders results.

- [ ] **Logout** — returns to login page, token cleared from local storage (DevTools → Application → Local Storage).

- [ ] **Re-login as `qa/sharko`** (viewer-tier user)
  - Login succeeds.
  - Write buttons (Add Cluster, Add Addon, Delete, etc.) are hidden or disabled.
  - Read pages all work.
  - Try to hit a write endpoint via the URL bar / clicking a hidden button anyway → should get a clean 403 toast, not a crash.

## A.6 CLI smoke checklist

The container ships the `sharko` binary; `--help` should respond on every subcommand. We're not testing functionality, just that the CLI doesn't panic.

!!! warning "One gotcha before you run any CLI in the container"

    **`localhost` from `docker exec` means the container, not your host.** Your `-p 18080:8080` port mapping does NOT apply inside the container — it only forwards traffic from the host network into the container. From inside the container the binary is bound to `:8080`, so any CLI call that talks to the API needs `--server http://localhost:8080`. Using `--server http://localhost:18080` from inside the container will fail with connection-refused (no process is listening on host port 18080 from the container's perspective).

    Quick rule: **inside the container `localhost:8080`, from the host `localhost:18080`.**

    Example (the only `-it` call in this section — `login` needs a real TTY for the password prompt):

    ```bash
    docker exec -it sharko-smoke sharko login \
      --username admin --password admin \
      --server http://localhost:8080
    ```

!!! info "Authoritative source for command names"
    The list below is the cobra-registered command set, captured from the running binary. **It is not derived from filenames in `cmd/sharko/`** — cobra registers commands under names that don't always match their source-file paths (e.g. the file is `unadopt.go` but the command is `unadopt-cluster`; the file is `secrets.go` but it registers two commands, `secret-status` and `refresh-secrets`).

    To regenerate this list authoritatively if the image version drifts:

    ```bash
    docker pull --platform linux/amd64 ghcr.io/moranweissman/sharko:<tag>
    docker run --rm -d --platform linux/amd64 --name sharko-doc-verify -p 18099:8080 \
      ghcr.io/moranweissman/sharko:<tag> sharko serve --demo --port 8080
    sleep 5
    docker exec sharko-doc-verify sharko --help \
      | sed -n '/Available Commands:/,/^Flags:/p'
    docker rm -f sharko-doc-verify
    ```

    Verified against `ghcr.io/moranweissman/sharko:1.23.0-pre.0` on 2026-05-02. Re-verify with `sharko --help` if the image tag has drifted before relying on the names below.

!!! tip "`-it` vs `-i` on `docker exec`"
    The `-t` flag allocates a TTY. For non-interactive `--help` sweeps that just print and exit, `-t` can cause stair-step output on some terminals (each newline is rendered as `\r\n` and lines accumulate column offset). Use `-i` only for these calls. Keep `-it` ONLY for the `sharko login` step below — that one prompts for a password and needs a real TTY.

- [ ] Top-level help

  ```bash
  docker exec -i sharko-smoke sharko --help
  ```

  **Expected:** usage block with the full subcommand list. **Flag if:** panic or empty output.

- [ ] Version

  ```bash
  docker exec -i sharko-smoke sharko version
  ```

  **Expected:** version matches the image tag and matches `/api/v1/health`'s `version` field. **Flag if:** mismatch (this is bug #1 in CLI form too).

- [ ] Walk every subcommand's `--help`

  The list below is every command cobra registers (verified against v1.23.0-pre.0). If `sharko` adds or removes a command, update this loop **and** the verification block above.

  ```bash
  for cmd in add-addon add-cluster add-clusters adopt \
             completion configure-addon connect describe-addon \
             discover init list-addons list-clusters \
             login pr refresh-secrets remove-addon remove-cluster \
             reset-admin secret-status serve status token \
             unadopt-cluster update-cluster upgrade-addon upgrade-addons \
             user validate version; do
    echo "=== $cmd ==="
    docker exec -i sharko-smoke sharko $cmd --help 2>&1 | head -5
    echo
  done
  ```

  **Expected:** every subcommand prints its own usage block. **Flag if:** any panics, prints "unknown command" for one in the list above, or shows raw Go errors.

- [ ] PR subcommands

  ```bash
  docker exec -i sharko-smoke sharko pr --help
  docker exec -i sharko-smoke sharko pr list --help
  docker exec -i sharko-smoke sharko pr wait --help
  ```

- [ ] Token subcommands

  ```bash
  docker exec -i sharko-smoke sharko token --help
  docker exec -i sharko-smoke sharko token create --help
  docker exec -i sharko-smoke sharko token list --help
  docker exec -i sharko-smoke sharko token revoke --help
  ```

## A.7 Teardown

- [ ] Stop and remove the container

  ```bash
  docker rm -f sharko-smoke
  ```

  **Expected:** container ID echoed back. **Flag if:** error — leftover state can break the next pass.

---

# Track B — Kind + ArgoCD (Layer 6)

Real K8s, real ArgoCD, real Helm chart. Catches integration bugs that Track A cannot. Budget 60–90 minutes; first run pulls a few GB of images.

!!! tip "Now automated by `./scripts/sharko-dev.sh` (V124-8.1)"
    Track B sections B.1 (prereqs check), B.2 (kind + Helm install/rebuild), B.3 (port-forward + health), B.4 (extract bootstrap admin credential + login), and B.5 (API sweep + V124-4 regression pins + Go E2E suite) are codified under the subcommand dispatcher:

    1. **From nothing** → `./scripts/sharko-dev.sh up` (kind cluster + ArgoCD + Sharko + port-forward + creds extraction, end-to-end).
    2. **Existing install, code change** → `./scripts/sharko-dev.sh rebuild` (forwards to `scripts/dev-rebuild.sh` and refreshes the creds cache).
    3. **Capture creds** → `eval "$(./scripts/sharko-dev.sh login --export)"` (no `source`, no `set -e` leak).
    4. **Run smoke** → `./scripts/sharko-dev.sh smoke` (auto-extracts creds if missing, then forwards to `scripts/smoke.sh`).

    Total runtime ~30 s on a warm machine after the first build. The manual steps below remain for understanding/troubleshooting and for the human-only portions (**B.7 ArgoCD UI**, **B.8 deep flow with real GitHub PAT**) which the scripts intentionally do not automate. See [Automation scripts](#automation-scripts) at the bottom of this doc.

    The wrappers forward to `scripts/dev-rebuild.sh` and `scripts/smoke.sh` — direct invocation of those still works for back-compat.

!!! info "Verified by execution"
    Every command in Track B was personally executed end-to-end on **2026-05-06** against a kind cluster built from this commit, with ArgoCD `stable` manifests and a locally-built Sharko image. Outputs shown in **Observed:** lines are the actual outputs captured during that run. The previous version of this section was authored by reading code without execution and shipped four wrong commands; see [BUG-015](#bug-log-template) for the postmortem.

    If you find an "Expected:" line that does not match what you observe, file a bug — the runbook (not your install) is wrong.

## B.1 Prereqs

- [ ] Docker is running

  ```bash
  docker version --format '{{.Server.Version}}'
  ```

  **Expected:** prints a version (e.g. `28.3.2`). **Flag if:** error — Docker daemon is not running.

- [ ] kind is installed (v0.20+)

  ```bash
  brew install kind && kind version
  ```

  **Expected:** `kind v0.20.0` or newer.

- [ ] kubectl is installed

  ```bash
  brew install kubectl && kubectl version --client
  ```

  **Expected:** Client Version `v1.28+` (this runbook was verified against `v1.30.1`).

- [ ] helm is installed

  ```bash
  brew install helm && helm version --short
  ```

  **Expected:** `v3.16+` (verified against `v3.16.4`).

- [ ] You're in the Sharko repo root (the e2e setup script is at `tests/e2e/setup.sh`)

  ```bash
  pwd  # should end in /sharko
  ls tests/e2e/setup.sh
  ```

## B.2 Spin up the environment

You have two options: the script (preferred) or the manual sequence (fallback if the script breaks). The script creates a cluster called `sharko-e2e`. If you already have a `sharko-e2e` cluster running (e.g. from a previous Track B pass) and want to keep it isolated, run the manual sequence with a different cluster name (the runbook itself was verified against `sharko-runbook-verify`).

### Option 1 — `tests/e2e/setup.sh` (preferred)

What the script does, in order (verified by reading `tests/e2e/setup.sh` and executing the same sequence by hand):

1. `kind create cluster --name sharko-e2e --wait 60s`
2. `kubectl create namespace argocd`
3. `kubectl apply --server-side --force-conflicts -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml`
4. `kubectl wait --for=condition=available --timeout=120s deployment/argocd-server -n argocd`
5. `docker build -t sharko:e2e .` (builds the image from your working tree)
6. `kind load docker-image sharko:e2e --name sharko-e2e`
7. `helm install sharko charts/sharko/ --namespace sharko --create-namespace --set image.repository=sharko --set image.tag=e2e --set image.pullPolicy=Never`
8. `kubectl wait --for=condition=available --timeout=60s deployment/sharko -n sharko`

!!! note "Why `--server-side` for the ArgoCD manifest?"
    Step 3 uses `kubectl apply --server-side --force-conflicts`. The ApplicationSet CRD that ships with ArgoCD has metadata that exceeds the 256 KB size limit of the `kubectl.kubernetes.io/last-applied-configuration` annotation that client-side `kubectl apply` writes. Server-side apply doesn't use that annotation. Older versions of `setup.sh` used plain `kubectl apply` and silently failed on the CRD step; this was fixed in V124-3.6. If you ever see `Request entity too large` or a missing CRD after step 3, you're running an old script — pull `main`.

Run it:

- [ ] Run the setup script

  ```bash
  bash tests/e2e/setup.sh
  ```

  **Expected:** ends with `E2E environment ready`. Total time: 3–5 minutes on a warm machine, ~10 minutes on a cold machine because the kindest/node + ArgoCD images need to be pulled.
  **Flag if:** any step fails — and switch to the manual sequence below to isolate which step is broken.

### Option 2 — manual sequence (fallback / parallel cluster)

Run each step individually so you can see exactly where it breaks. Substitute `sharko-e2e` with whatever cluster name you want (e.g. `sharko-runbook-verify` if you're walking the runbook to verify it).

- [ ] Create kind cluster

  ```bash
  kind create cluster --name sharko-e2e --wait 60s
  ```

  **Observed (verify run):** "Ready after 17s 💚" then `Set kubectl context to "kind-sharko-e2e"`.

- [ ] Install ArgoCD with **server-side apply** (see note above)

  ```bash
  kubectl create namespace argocd
  kubectl apply --server-side --force-conflicts -n argocd \
    -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
  kubectl wait --for=condition=available --timeout=180s deployment/argocd-server -n argocd
  ```

  **Observed:** `Warning: resource customresourcedefinitions/applicationsets.argoproj.io is missing the kubectl.kubernetes.io/last-applied-configuration annotation` printed during apply (this is expected and harmless — it's the warning you would have hit as a hard failure under client-side apply). Then `deployment.apps/argocd-server condition met`. Total: ~30s on a warm machine, longer on first run because of image pulls.

- [ ] Build and sideload Sharko image

  ```bash
  docker build -t sharko:e2e .
  kind load docker-image sharko:e2e --name sharko-e2e
  ```

  **Observed:** ~80 seconds (UI bundle ~23s, Go build ~60s) then "loading…" then completion. The `version` field will be `dev` because `cmd/sharko/main.go` defaults to `-X main.version=dev` when not built via GoReleaser.

- [ ] Install Sharko via Helm

  ```bash
  helm install sharko charts/sharko/ \
    --namespace sharko --create-namespace \
    --set image.repository=sharko \
    --set image.tag=e2e \
    --set image.pullPolicy=Never
  ```

  **Observed:** `STATUS: deployed`, `REVISION: 1`. Note: helm prints two `WARNING: Kubernetes configuration file is group-readable` lines if your kubeconfig has loose perms — harmless, not a Sharko bug.

- [ ] Wait for the Sharko deployment

  ```bash
  kubectl wait --for=condition=available --timeout=120s deployment/sharko -n sharko
  ```

  **Observed:** `deployment.apps/sharko condition met` within ~15s on a warm machine.

## B.3 Port-forward and verify

- [ ] Confirm pods are running and grab their **actual** label

  ```bash
  kubectl get pods -n sharko --show-labels
  ```

  **Observed:**

  ```
  NAME                     READY   STATUS    RESTARTS   AGE   LABELS
  sharko-64c68b75d-sn6p6   1/1     Running   0          13s   app.kubernetes.io/instance=sharko,app.kubernetes.io/name=sharko,pod-template-hash=64c68b75d
  ```

  !!! warning "The pod label is `app.kubernetes.io/name=sharko`, NOT `app=sharko`"
      A previous version of this runbook told you to use `kubectl logs -l app=sharko -n sharko`. That selector matches **zero pods**. The actual label set by the Helm chart is `app.kubernetes.io/name=sharko` (and `app.kubernetes.io/instance=sharko`). Use either — or use the deployment name directly: `kubectl logs -n sharko deployment/sharko`.

- [ ] Confirm ArgoCD pods are running too

  ```bash
  kubectl get pods -n argocd
  ```

  **Expected:** all `argocd-*` pods Running. The application-controller is a StatefulSet (`argocd-application-controller-0`); the rest are Deployments. **Flag if:** any pod CrashLoopBackOff or ImagePullBackOff.

- [ ] Port-forward Sharko

  ```bash
  kubectl port-forward svc/sharko 8080:80 -n sharko &
  PF_SHARKO=$!
  sleep 3
  curl -fsS http://localhost:8080/api/v1/health | jq .
  ```

  **Observed:**

  ```json
  {
    "mode": "Kubernetes",
    "status": "healthy",
    "version": "dev"
  }
  ```

  **Flag if:** port-forward dies, or the response shape is missing `mode` / `status` / `version`. Note the `version` field is `dev` for locally-built images — that's correct, it only carries a real semver when GoReleaser builds the image.

## B.4 Get the bootstrap admin credential

!!! info "How bootstrap credentials work in real K8s (V124-3.8)"
    On first install with no operator-supplied password, Sharko auto-generates a 16-character bootstrap password. As of V124-3.8 the credential is **logged ONCE to the pod's stdout** in a clearly-marked block, and **the `admin.initialPassword` key is then removed from the Secret** so a pod restart does not re-emit it.

    This means: **`kubectl get secret sharko -o jsonpath='{.data.admin\.initialPassword}'` returns empty.** That command was valid in earlier Sharko versions and is still in stale third-party docs — don't trust it. The only retrieval path is `kubectl logs`. If you missed the log line (e.g. log retention rolled it off), you must `helm uninstall && helm install` for a fresh password, OR use one of the operator-supplied paths in the [Initial Credentials](../operator/installation.md#initial-credentials) section of the operator install guide.

- [ ] Pull the bootstrap credential out of the pod logs

  ```bash
  kubectl logs -n sharko deployment/sharko | grep -A4 "BOOTSTRAP ADMIN"
  ```

  **Observed:**

  ```json
  {"time":"2026-05-05T22:47:24.522777546Z","level":"INFO","msg":"=== BOOTSTRAP ADMIN CREDENTIAL ==="}
  {"time":"2026-05-05T22:47:24.522786171Z","level":"INFO","msg":"bootstrap admin generated","username":"admin","password":"Tm02xfabCP8MM1p9"}
  {"time":"2026-05-05T22:47:24.522788796Z","level":"INFO","msg":"This is the only time this credential will be shown. Store it securely."}
  {"time":"2026-05-05T22:47:24.522790171Z","level":"INFO","msg":"=== END BOOTSTRAP ADMIN CREDENTIAL ==="}
  ```

  **Flag if:** the block isn't there. Most likely you've already restarted the pod (the marker is removed after first log) or the install was made with an operator-supplied password (in which case use that). Run `helm uninstall sharko -n sharko && helm install ...` to start fresh.

- [ ] Extract the password into a shell var for the rest of the pass

  ```bash
  ADMIN_PW=$(kubectl logs -n sharko deployment/sharko \
    | grep '"bootstrap admin generated"' | head -1 \
    | sed -E 's/.*"password":"([^"]+)".*/\1/')
  echo "$ADMIN_PW"
  ```

  **Expected:** prints the 16-char password (e.g. `Tm02xfabCP8MM1p9`). **Flag if:** empty — the grep / sed broke. Inspect with `kubectl logs -n sharko deployment/sharko | grep -c "bootstrap admin generated"` (should be exactly `1`).

## B.5 API smoke against real K8s

Same shape as Track A's API checklist (A.4), but against `http://localhost:8080` and using the bootstrap admin password from B.4.

- [ ] Set host + login

  ```bash
  HOST=http://localhost:8080
  TOKEN=$(curl -fsS -X POST $HOST/api/v1/auth/login \
    -H 'content-type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PW\"}" | jq -r .token)
  echo "token: ${TOKEN:0:24}…"
  ```

  **Observed:** `token: 396c20fc6ba8a3e3adc29a1f…` (24-char prefix of the bearer token).
  **Flag if:** 401 — re-extract `$ADMIN_PW` (B.4) and confirm it matches what's in the logs.

- [ ] `GET /api/v1/clusters` against real K8s

  ```bash
  curl -sS -o /tmp/r.json -w '%{http_code}\n' $HOST/api/v1/clusters \
    -H "authorization: Bearer $TOKEN"
  cat /tmp/r.json | jq .
  ```

  **Observed:**

  ```
  503
  {
    "error": "Service Unavailable",
    "op": "get_active_git_provider"
  }
  ```

  **Expected on a fresh cluster (no Git connection configured yet):** 503 with the **sanitized JSON above** (V124-2.10 + V124-3.2). The `op` field is the operation the handler was attempting — useful for triage, never leaks internals. **Flag if:** 500 with a raw error string in plain text — that's a regression of the V124-2.10 sanitization.
  **Once you've configured a Git connection in B.7,** this endpoint should return 200 with `{"clusters":[]}` (or the in-cluster record after init).

- [ ] Sweep the read endpoints that should always be 200 even without a Git connection

  ```bash
  bash -c '
  for path in /api/v1/config /api/v1/audit /api/v1/repo/status /api/v1/users/me \
              /api/v1/fleet/status /api/v1/notifications /api/v1/catalog/addons; do
    code=$(curl -sS -o /tmp/r.json -w "%{http_code}" $HOST$path -H "authorization: Bearer $TOKEN")
    echo "$code  $path"
    [ "$code" -ge 400 ] && (cat /tmp/r.json | jq . 2>/dev/null || cat /tmp/r.json) && echo
  done'
  ```

  !!! warning "zsh quoting gotcha"
      The literal `\n` inside `for path in ... \n ...; do` parses on `zsh` as `\` + `n` and breaks the loop. The `bash -c '...'` wrapper above is what was actually verified. If you copy-paste a multi-line shell-loop that uses backslash continuations, run it under `bash` (or pipe it into `sh`), not `zsh`.

  **Observed:**

  ```
  200  /api/v1/config
  200  /api/v1/audit
  200  /api/v1/repo/status
  200  /api/v1/users/me
  200  /api/v1/fleet/status
  200  /api/v1/notifications
  200  /api/v1/catalog/addons
  ```

  **Flag if:** any non-2xx — copy the output into the bug log.

  Spot-check the response shapes:

  - `/api/v1/config` returns `{"argocd":{"connected":false},"gitops":{...},"repo_paths":{...}}` — `connected: false` is correct on a fresh cluster.
  - `/api/v1/repo/status` returns `{"initialized":false,"reason":"no_connection"}`.
  - `/api/v1/users/me` returns `{"username":"admin","role":"admin","has_github_token":false}`. **Flag if:** the response leaks a password hash, raw token, or any field beginning with `admin.password`.
  - `/api/v1/fleet/status` returns counts (`total_clusters: 0`, `git_unavailable: true`, `argo_unavailable: true` on a fresh install). The `argo_unavailable` field is true even though ArgoCD itself is up — Sharko reads ArgoCD via the connection, and there's no connection yet.
  - `/api/v1/catalog/addons` returns `{"addons":[...]}` with ~45 entries (the embedded curated catalog).

- [ ] `POST /api/v1/init` — what happens before a Git connection is configured

  ```bash
  curl -sS -X POST $HOST/api/v1/init \
    -H "authorization: Bearer $TOKEN" \
    -H 'content-type: application/json' \
    -d '{}' -w '\nstatus: %{http_code}\n'
  ```

  **Observed:**

  ```json
  {"error":"no active ArgoCD connection: no active connection configured"}
  status: 502
  ```

  **Expected:** 502 with sanitized JSON error (V124-3.2 classifies upstream-dependency unavailability as 502, not 500). **Flag if:** 500 with a raw error or stack trace, or HTML.

- [ ] `POST /api/v1/connections/` (note trailing slash) with an invalid body — verify the V124-3.3 fix (validation errors return 400, not 500)

  ```bash
  curl -sS -o /tmp/r.json -w '%{http_code}\n' -X POST $HOST/api/v1/connections/ \
    -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' -d '{}'
  cat /tmp/r.json | jq .
  ```

  **Expected:** 400 with a JSON error explaining the missing fields. **Flag if:** 500 — V124-3.3 has regressed.

  !!! note "Trailing slash matters on `/connections`"
      `GET /api/v1/connections` (no trailing slash) returns 301 → `/api/v1/connections/`. Tools that don't follow redirects (e.g. `curl` without `-L`) will see the 301 instead of the body. Use the trailing slash directly, or pass `-L` to follow.

## B.6 Run the existing Go E2E suite

The e2e tests log in with the bootstrap admin credential, walk a few read endpoints, and verify the contract. As of V124-3.7 they read credentials from env vars (defaults to `admin`/`admin` only in the demo image — fails on real K8s without the override).

- [ ] Run the build-tagged tests with the bootstrap creds

  ```bash
  SHARKO_E2E_URL=http://localhost:8080 \
    SHARKO_E2E_USERNAME=admin \
    SHARKO_E2E_PASSWORD="$ADMIN_PW" \
    go test -tags e2e ./tests/e2e/... -v -timeout 5m
  ```

  **Observed:**

  ```
  === RUN   TestHealthEndpoint
  --- PASS: TestHealthEndpoint (0.01s)
  === RUN   TestLoginAndAuth
  --- PASS: TestLoginAndAuth (0.14s)
  === RUN   TestRepoStatus
      e2e_test.go:139: repo is not initialized (expected for fresh install): reason=no_connection
  --- PASS: TestRepoStatus (0.12s)
  PASS
  ok  	github.com/MoranWeissman/sharko/tests/e2e	2.403s
  ```

  **Flag if:** `TestLoginAndAuth` fails with 401 — your `ADMIN_PW` doesn't match the running pod (most likely you bounced the pod since extracting it; restart the install).
  **Flag if:** any test hangs past 5 minutes — port-forward died or the deployment crash-looped.

## B.7 Real ArgoCD UI

- [ ] Port-forward ArgoCD

  ```bash
  kubectl port-forward svc/argocd-server 8090:443 -n argocd &
  PF_ARGOCD=$!
  sleep 3
  ```

  **Observed:** `Forwarding from 127.0.0.1:8090 -> 8080` (note: ArgoCD's container listens on 8080 internally; the `:443` in the svc port maps to that).

- [ ] Get the ArgoCD admin password

  ```bash
  kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d; echo
  ```

  **Observed:** prints a random ~16-char password (e.g. `M9t5l8UeSocMqTro`).
  **Flag if:** secret missing — ArgoCD didn't finish bootstrap; re-check `kubectl get pods -n argocd` and rewait.

- [ ] Reach the UI on `https://localhost:8090`

  ```bash
  curl -sk -o /dev/null -w '%{http_code}\n' https://localhost:8090/
  ```

  **Observed:** `200` (the response body is the React shell of the ArgoCD UI). Open <https://localhost:8090> in a real browser, accept the self-signed cert, login as `admin` + the password above.

  **Expected:** ArgoCD UI loads, "Applications" view is empty (no Sharko apps yet — none registered).

- [ ] Leave this tab open. After you do B.8 (the deep flow) you'll come back here to verify Sharko-managed apps appear.

## B.8 Try a real flow (operator-interaction required)

This is the deepest E2E test. The point is to take Sharko all the way through its core loop: connect Git → init repo → register cluster → add addon → watch ArgoCD sync.

!!! warning "This section was NOT fully executed during runbook authoring"
    Steps in B.8 require a real GitHub repo + a personal access token with `repo` scope. The runbook author did not have a throwaway smoke-test repo with PAT available at authoring time, so B.1–B.7 were verified end-to-end but B.8 was only inspected from the API contract and code.

    **Operator: when you walk B.8, treat any deviation from the steps below as either a bug OR an out-of-date runbook step. File it either way.**

You have two choices for the Git repo:

- **Your own GitHub fork** of a sample addons repo (recommended — full control).
- **A throwaway repo** you create just for this pass, e.g. `https://github.com/<you>/sharko-smoke-addons` (empty repo with a `main` branch is fine — Sharko will scaffold).

There's no public test repo guaranteed to be there forever; use one of the above.

### Step 3: ArgoCD connection

Before the GitHub connection, the wizard's step 3 needs an ArgoCD bearer token. The full flow (port-forward → patch `argocd-cm` to enable `apiKey` capability → restart argocd-server → re-establish port-forward → `argocd login` → `argocd account generate-token`) is codified into a single subcommand (V124-9):

```bash
eval "$(./scripts/sharko-dev.sh argocd-token --export)"
echo "$ARGOCD_TOKEN"   # paste this into the wizard token field
echo "$ARGOCD_URL"     # paste this as the server URL
```

Use `--service-account` to generate against a dedicated `sharko` ArgoCD account (recommended for production-like setups; the script also patches `argocd-rbac-cm` to grant `role:admin`):

```bash
eval "$(./scripts/sharko-dev.sh argocd-token --service-account --export)"
```

The subcommand is idempotent — second run reuses the existing port-forward and skips the patch+restart if the capability is already enabled (~1–2 s vs ~30 s on cold-start).

- [ ] In the Sharko UI (<http://localhost:8080>), Settings → Connections → add a new GitHub connection
  - URL: your fork
  - PAT: a token with `repo` scope
  - Click Test → expect green checkmark.

  **If "Test" returns a 5xx error in the UI:** capture the response body and pod logs (`kubectl logs -n sharko deployment/sharko --tail=50`) — the V124-3.3 fix should give you a 400 with a clean validation error for malformed input, but a 502 from a real GitHub auth failure is also expected.

- [ ] Initialize the repo via UI (or `POST /api/v1/init` from CLI) — Sharko should commit its bootstrap files.

  **Expected:** PR opens against your fork (in tier-2 PR mode) or a direct commit lands (in tier-1 service mode). Configuration depends on the connection settings.
  **Flag if:** silent failure, hung spinner, or commit attribution is wrong (must be your PAT identity, not `github-actions[bot]`).

- [ ] Register a managed cluster — for kind smoke, you can register the in-cluster kubernetes API:
  - Name: `kind-self`
  - Server: `https://kubernetes.default.svc`
  - Provider: in-cluster

- [ ] Add an addon — pick something simple from the catalog (`metrics-server` or `cert-manager`).

  **Expected:** PR opens, applicationset entry generated, sharko shows it in the UI.

- [ ] Switch to the ArgoCD tab (B.7). Refresh. **Expected:** the Sharko-managed application is visible. Click into it; status is `Synced` / `Healthy` (may take 1–2 minutes).
  **Flag if:** never appears, or stuck `OutOfSync` indefinitely.

## B.9 Teardown

- [ ] Kill port-forwards

  ```bash
  kill $PF_SHARKO $PF_ARGOCD 2>/dev/null
  ```

  **Observed:** silent exit. **Flag if:** the next `lsof -i :8080,:8090` shows the ports still bound — orphan kubectl process.

- [ ] Tear down the cluster

  ```bash
  bash tests/e2e/teardown.sh
  ```

  Or directly:

  ```bash
  kind delete cluster --name sharko-e2e
  ```

  **Observed:** `Deleting cluster "sharko-e2e" ...` then `Deleted nodes`. **Flag if:** error — manually inspect with `kind get clusters`. Leftover state will block the next pass.

  !!! tip "If you ran the manual sequence with a different cluster name"
      Use that name in `kind delete cluster --name <your-name>`. Verify cleanup with `kind get clusters` — the cluster you used should not appear in the output.

---

# Bug log template

Fill this in as you go. One block per bug. Paste the whole filled block back into the chat at the end of the pass — that's the v1.24 hotfix triage list.

```
### BUG-XXX: <one-line title>
- Surface:    [UI | API | CLI | Helm chart | docs]
- Severity:   [low | med | high]
- Track:      [A — demo | B — kind | both]
- Endpoint / page: <path or URL>
- Repro steps:
  1.
  2.
  3.
- Observed:
  <raw response, console error, screenshot path>
- Expected:
  <one sentence>
- Logs (last 50 lines):
  ```
  <docker logs sharko-smoke 2>&1 | tail -50  OR  kubectl logs deploy/sharko -n sharko --tail=50>
  ```
- Note:
  <free-form thought, suspected root cause, related bugs>
```

Pre-seeded entries from today's pass — verify these and update status:

```
### BUG-001: Login page footer shows wrong version
- Surface: UI
- Severity: low
- Track: A
- Endpoint / page: /login (footer)
- Repro: Open the login page; compare footer version string to GET /api/v1/health → version.
- Observed (2026-05-01): mismatch.
- Expected: footer version equals running binary version.

### BUG-002: GET /api/v1/clusters returns 500 with raw filesystem error
- Surface: API + UI
- Severity: high
- Track: A
- Endpoint: GET /api/v1/clusters
- Repro: docker run --demo, then curl /api/v1/clusters with a valid bearer.
- Observed (2026-05-01): 500, body contains "reading managed-clusters.yaml: file not found". UI also shows the raw string verbatim.
- Expected: 200 with empty array (or seeded demo clusters). Even if file is missing, handler should return [] not 500. UI must never render raw backend error strings.

### BUG-003: Cluster list page goes blank after ~30 s
- Surface: UI
- Severity: med
- Track: A
- Page: /clusters
- Repro: Open the page during the BUG-002 error window; wait ~30 s for the underlying error to clear.
- Observed (2026-05-01): page blanks; recovery requires clicking Dashboard then back, or hard refresh.
- Expected: page re-fetches and renders empty / populated state without manual nav.
```

---

# Automation scripts

The maintainer-DX tooling is built around a single subcommand dispatcher with two underlying helper scripts. All three live in `scripts/` and are intentionally distinct from `scripts/upgrade.sh` (which targets the released-Helm-chart flow, not the local-build flow).

## `scripts/sharko-dev.sh` — single entry, 12 subcommands (V124-8.1 + V124-9, canonical)

The maintainer's primary entry point. Subcommand dispatch (like `git` / `kubectl`) so each scenario gets its own one-liner with `--help`:

```bash
./scripts/sharko-dev.sh help              # full subcommand list
./scripts/sharko-dev.sh up                # bring up env from nothing
./scripts/sharko-dev.sh install           # install on existing cluster
./scripts/sharko-dev.sh rebuild           # rebuild after code change
./scripts/sharko-dev.sh creds             # fetch admin password (smart fallback chain)
./scripts/sharko-dev.sh login --export    # eval-via-pipe: ADMIN_PW + TOKEN
./scripts/sharko-dev.sh rotate            # rotate password (verifies V124-7)
./scripts/sharko-dev.sh argocd-token --export   # eval-via-pipe: ARGOCD_TOKEN + ARGOCD_URL (V124-9)
./scripts/sharko-dev.sh smoke             # auto-extracts creds, runs smoke
./scripts/sharko-dev.sh status            # current env state
./scripts/sharko-dev.sh reset --yes       # uninstall (preserves cluster)
./scripts/sharko-dev.sh down --yes        # full teardown
```

The `creds` subcommand has a five-path fallback chain (V124-6.3 secret → cache → current pod logs → previous pod logs → error with recovery hints) so the password is retrievable in every state the maintainer hits during V124-3 through V124-7. The `rotate` subcommand also asserts V124-7's secret-rotation behavior — the new password must land in `sharko-initial-admin-secret` or the command exits non-zero.

The `argocd-token` subcommand (V124-9) codifies the 8-command apiKey gauntlet for Sharko's wizard step 3: it reuses any live port-forward to `argocd-server`, patches `argocd-cm` to enable the `apiKey` capability if missing, restarts `argocd-server` and re-establishes the port-forward, runs `argocd login` against `localhost:18080` with the bootstrap admin password, then `argocd account generate-token` for either `admin` (default) or a dedicated `sharko` service account (`--service-account`). Idempotent — second run skips the patch+restart and finishes in ~1–2 s.

Sourcing model: **eval-via-pipe**, not `source`. The `--export` flag prints ONLY export lines so:
```bash
eval "$(./scripts/sharko-dev.sh login --export)"
```
captures `$ADMIN_PW` and `$TOKEN` cleanly with no `set -e` / `set -u` leak risk. The `-q` / `--quiet` mode prints only the secret/token for piping.

## `scripts/dev-rebuild.sh` — kind local-build inner-loop (V124-5.1, also forwarded)

The original rebuild script. `./scripts/sharko-dev.sh rebuild` forwards to it; direct invocation also still works.

```bash
source scripts/dev-rebuild.sh    # exports $ADMIN_PW and $TOKEN into your shell (legacy — eval-via-pipe via sharko-dev.sh login is preferred)
./scripts/dev-rebuild.sh         # prints the export commands instead
./scripts/dev-rebuild.sh --auto-install  # if no helm release, fall back to sharko-dev.sh install (V124-8.2)
./scripts/dev-rebuild.sh -h      # show built-in help
```

Pipeline (six steps): pre-flight → `docker build` → `kind load` → `kubectl rollout restart` → bootstrap-password extraction → port-forward + login.

**V124-3.8 gotcha (read this once, then forget it):** the bootstrap admin password is logged to the pod's stdout exactly ONCE, on first install. Subsequent `rollout restart` cycles do NOT re-emit it. The script handles this by:

1. Polling the new pod's logs (first install case).
2. Falling back to the previous pod's logs (`kubectl logs --previous`).
3. Falling back to a cache file (`~/.sharko-dev-pw`, override with `SHARKO_DEV_PW_CACHE`).
4. If all three fail, printing recovery instructions (`helm uninstall + tests/e2e/setup.sh`).

The cache file is written 0600 on first successful extraction.

Configurable via env: `KIND_CLUSTER_NAME` (default `sharko-e2e`), `SHARKO_NAMESPACE` (`sharko`), `SHARKO_LOCAL_PORT` (`8080`), `IMAGE_TAG` (`e2e`).

## `scripts/smoke.sh` — Track A + Track B mechanical sweep (V124-5.2, also forwarded)

Replaces Track A's A.4 + A.6 and Track B's B.5 + B.6. `./scripts/sharko-dev.sh smoke` forwards to it after auto-extracting credentials; direct invocation also still works (with `$ADMIN_PW` and `$TOKEN` already exported).

```bash
./scripts/sharko-dev.sh smoke    # canonical: auto-extracts creds, then forwards
./scripts/smoke.sh               # direct: requires $ADMIN_PW + $TOKEN already set
./scripts/smoke.sh -v            # verbose: full curl bodies on failure + go test -v
./scripts/smoke.sh -h            # show built-in help
```

Five sequential phases, all PASS/FAIL with an exit code:

1. **Pre-flight** — kubectl context, deployment availability, port-forward, `/api/v1/health` 200.
2. **CLI sweep** — discovers cobra subcommands by exec'ing `sharko --help` in the pod, then runs `--help` on each one.
3. **API sweep** — read endpoints that should always 200 on a fresh cluster (no Git connection needed): `/health`, `/config`, `/audit`, `/repo/status`, `/users/me`, `/fleet/status`, `/notifications`, `/catalog/addons`, `/catalog/sources`, `/providers`. Asserts 200 + valid JSON + expected top-level key.
4. **V124-4 regression pins** — POSTs the four write endpoints from the V124-4 fix bundle with empty `{}` bodies and asserts the post-fix status codes (BUG-017 → 400, BUG-018 → 503, BUG-019 → 400, BUG-020 → 404 with `code=endpoint_not_found`). Each check carries the BUG ID for traceability back to the relevant commit.
5. **Go E2E suite** — runs `go test -tags e2e ./tests/e2e/...` with `SHARKO_E2E_USERNAME=admin` + `SHARKO_E2E_PASSWORD=$ADMIN_PW`.

The script intentionally does NOT automate B.7 (ArgoCD UI) or B.8 (the deep Git → init → register → addon → sync flow) — those need human eyes and a real GitHub PAT.

# Cross-reference

- [Testing Guide](testing-guide.md) — the reference doc this runbook complements (test layers, patterns, command cheatsheet)
- [Catalog Scan Runbook](catalog-scan-runbook.md) — the operational doc for the daily scanner bot
- `scripts/sharko-dev.sh` — single-entry maintainer DX dispatcher (V124-8.1, canonical)
- `scripts/dev-rebuild.sh` and `scripts/smoke.sh` — the underlying helper scripts (V124-5; still callable directly for back-compat)
- `scripts/upgrade.sh` — the released-Helm-chart upgrade verifier (different flow, do not confuse)
- `tests/e2e/setup.sh` and `tests/e2e/teardown.sh` — the scripts Track B leans on for cluster bringup/teardown
- `internal/api/router.go` — source of truth for which routes actually exist (don't guess paths from memory)
