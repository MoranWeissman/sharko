# Personal Smoke Runbook

A hands-on, checkbox-driven smoke pass for the Sharko maintainer. This is **not** a reference doc — it's a list you literally check off, top to bottom, while running the product yourself.

If you want background on the test pyramid and what each layer is for, read [Testing Guide](testing-guide.md). This runbook is the in-the-moment companion: open it in one window, terminal + browser in the others, and walk it.

---

## Why this exists

You've shipped 23 versions of Sharko. You've never personally driven the product end-to-end. The only way to find the ten-thousand-paper-cut bugs that unit tests and CI cannot catch is to be the user for two hours.

The goal of one full pass:

- One pass through **Track A** (Docker demo mode, Layer 5) — about 30 minutes.
- One pass through **Track B** (kind + ArgoCD, Layer 6) — about 60–90 minutes.
- File every rough edge into the [Bug Log](#bug-log-template) at the bottom. You're seeding the v1.24 hotfix bundle.

!!! warning "Known bugs as of 2026-05-01"
    Three bugs have already been observed in the live product within the first 15 minutes of testing today. **Verify each is still present** as you walk the runbook — if any is fixed, mark it done.

    1. **Login page footer shows wrong version** (UI). The footer renders a stale or hardcoded version string instead of the running binary's version.
    2. **`GET /api/v1/clusters` returns 500** with raw error `reading managed-clusters.yaml: file not found`. The error string also leaks to the UI verbatim. (Backend bug + UI safety bug — should be a clean empty list, not a 500.)
    3. **Cluster list page goes blank after ~30 s** when the underlying error clears. Recovers only on Dashboard click or hard refresh — the list view doesn't re-fetch on its own.

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

- [ ] `POST /api/v1/auth/login` (admin/admin)

  ```bash
  TOKEN=$(curl -fsS -X POST $HOST/api/v1/auth/login \
    -H 'content-type: application/json' \
    -d '{"username":"admin","password":"admin"}' | jq -r .token)
  echo "admin token: ${TOKEN:0:24}…"
  ```

  **Expected:** non-empty token string printed. **Flag if:** `null`, empty, or the login endpoint returns 4xx — demo seeding failed.

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

- [ ] **Login submit** — admin/admin → land on Dashboard.

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

- [ ] Top-level help

  ```bash
  docker exec -it sharko-smoke sharko --help
  ```

  **Expected:** usage block with the full subcommand list. **Flag if:** panic or empty output.

- [ ] Version

  ```bash
  docker exec -it sharko-smoke sharko version
  ```

  **Expected:** version matches the image tag and matches `/api/v1/health`'s `version` field. **Flag if:** mismatch (this is bug #1 in CLI form too).

- [ ] Walk every subcommand's `--help`

  ```bash
  for cmd in login version init status \
             add-cluster remove-cluster update-cluster list-clusters \
             add-clusters discover adopt unadopt \
             add-addon remove-addon upgrade-addon upgrade-addons \
             pr token user secrets validate connect reset-admin serve; do
    echo "=== $cmd ==="
    docker exec -it sharko-smoke sharko $cmd --help 2>&1 | head -5
    echo
  done
  ```

  **Expected:** every subcommand prints its own usage block. **Flag if:** any panics, prints "unknown command" for one in the list above, or shows raw Go errors.

- [ ] PR subcommands

  ```bash
  docker exec -it sharko-smoke sharko pr --help
  docker exec -it sharko-smoke sharko pr list --help
  docker exec -it sharko-smoke sharko pr wait --help
  ```

- [ ] Token subcommands

  ```bash
  docker exec -it sharko-smoke sharko token --help
  docker exec -it sharko-smoke sharko token create --help
  docker exec -it sharko-smoke sharko token list --help
  docker exec -it sharko-smoke sharko token revoke --help
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

## B.1 Prereqs

- [ ] Docker is running (`docker version`)

- [ ] kind is installed

  ```bash
  brew install kind && kind version
  ```

- [ ] kubectl is installed

  ```bash
  brew install kubectl && kubectl version --client
  ```

- [ ] helm is installed

  ```bash
  brew install helm && helm version
  ```

- [ ] You're in the Sharko repo root (the e2e setup script is at `tests/e2e/setup.sh`)

  ```bash
  pwd  # should end in /sharko
  ls tests/e2e/setup.sh
  ```

## B.2 Spin up the environment

You have two options: the script (preferred) or the manual sequence (fallback if the script breaks). Both end with Sharko + ArgoCD running in a kind cluster called `sharko-e2e`.

### Option 1 — `tests/e2e/setup.sh` (preferred)

What the script does, in order:

1. `kind create cluster --name sharko-e2e --wait 60s`
2. `kubectl create namespace argocd && kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml`
3. `kubectl wait --for=condition=available --timeout=120s deployment/argocd-server -n argocd`
4. `docker build -t sharko:e2e .` (builds the image from your working tree)
5. `kind load docker-image sharko:e2e --name sharko-e2e`
6. `helm install sharko charts/sharko/ --namespace sharko --create-namespace --set image.repository=sharko --set image.tag=e2e --set image.pullPolicy=Never`
7. `kubectl wait --for=condition=available --timeout=60s deployment/sharko -n sharko`

Run it:

- [ ] Run the setup script

  ```bash
  bash tests/e2e/setup.sh
  ```

  **Expected:** ends with `E2E environment ready`. Total time: 3–5 minutes on a warm machine, up to 10 minutes if pulling ArgoCD images for the first time.
  **Flag if:** any step fails — and switch to the manual sequence below to isolate which step is broken.

### Option 2 — manual sequence (fallback)

Run each step individually so you can see exactly where it breaks:

- [ ] Create kind cluster

  ```bash
  kind create cluster --name sharko-e2e --wait 60s
  ```

- [ ] Install ArgoCD

  ```bash
  kubectl create namespace argocd
  kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
  kubectl wait --for=condition=available --timeout=120s deployment/argocd-server -n argocd
  ```

- [ ] Build and sideload Sharko image

  ```bash
  docker build -t sharko:e2e .
  kind load docker-image sharko:e2e --name sharko-e2e
  ```

- [ ] Install Sharko via Helm

  ```bash
  helm install sharko charts/sharko/ \
    --namespace sharko --create-namespace \
    --set image.repository=sharko \
    --set image.tag=e2e \
    --set image.pullPolicy=Never
  ```

- [ ] Wait for the Sharko deployment

  ```bash
  kubectl wait --for=condition=available --timeout=60s deployment/sharko -n sharko
  ```

## B.3 Port-forward and verify

- [ ] Confirm pods are running

  ```bash
  kubectl get pods -A | grep -E 'argocd|sharko'
  ```

  **Expected:** all argocd-* pods Running, sharko pod Running. **Flag if:** any pod CrashLoopBackOff or ImagePullBackOff.

- [ ] Port-forward Sharko

  ```bash
  kubectl port-forward svc/sharko 8080:80 -n sharko &
  PF_SHARKO=$!
  sleep 3
  curl -fsS http://localhost:8080/api/v1/health | jq .
  ```

  **Expected:** 200 with healthy status. **Flag if:** port-forward dies, or the version field doesn't match `e2e` build expectations.

- [ ] Get the admin password (chart-generated)

  ```bash
  kubectl get secret sharko -n sharko -o jsonpath='{.data.admin\.initialPassword}' | base64 -d; echo
  ```

  **Expected:** prints a random password (different from `admin`). **Flag if:** secret missing or empty.

## B.4 API smoke against real K8s

Same checklist as A.4, but against `http://localhost:8080` and using the chart-generated admin password.

- [ ] Set host + login

  ```bash
  HOST=http://localhost:8080
  ADMIN_PW=$(kubectl get secret sharko -n sharko -o jsonpath='{.data.admin\.initialPassword}' | base64 -d)
  TOKEN=$(curl -fsS -X POST $HOST/api/v1/auth/login \
    -H 'content-type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PW\"}" | jq -r .token)
  echo "token: ${TOKEN:0:24}…"
  ```

- [ ] `GET /api/v1/clusters` against real ArgoCD

  ```bash
  curl -fsS $HOST/api/v1/clusters -H "authorization: Bearer $TOKEN" | jq .
  ```

  **Expected:** 200 with `{"clusters":[]}` or just the in-cluster `kubernetes.default.svc` host record (depending on adoption state). **Now this should NOT 500** — kind has no `managed-clusters.yaml` issue because real config-map storage is used. **Flag if:** still 500 — bug #2 isn't actually file-system-specific, it's an unconditional handler bug.

- [ ] `GET /api/v1/config`, `/api/v1/audit`, `/api/v1/repo/status`, `/api/v1/users/me`, `/api/v1/fleet/status`, `/api/v1/notifications`, `/api/v1/catalog/addons` — same expectations as Track A. Walk them with one block:

  ```bash
  for path in /api/v1/config /api/v1/audit /api/v1/repo/status /api/v1/users/me \
              /api/v1/fleet/status /api/v1/notifications /api/v1/catalog/addons; do
    code=$(curl -sS -o /tmp/r.json -w '%{http_code}' $HOST$path -H "authorization: Bearer $TOKEN")
    echo "$code  $path"
    [ "$code" -ge 400 ] && cat /tmp/r.json | jq . 2>/dev/null || true
  done
  ```

  **Expected:** all 200. **Flag if:** any non-2xx — copy the output into the bug log.

- [ ] `GET /api/v1/init` / `POST /api/v1/init` — the real interesting endpoint on a fresh cluster

  ```bash
  curl -sS -X POST $HOST/api/v1/init \
    -H "authorization: Bearer $TOKEN" \
    -H 'content-type: application/json' \
    -d '{}' -w '\nstatus: %{http_code}\n'
  ```

  **Expected: ?** — on a fresh kind cluster with no Git connection configured, this likely returns 4xx ("connection required") with a clean JSON error. 500 → flag.

## B.5 Run the existing Go E2E suite

- [ ] Run the build-tagged tests

  ```bash
  SHARKO_E2E_URL=http://localhost:8080 go test -tags e2e ./tests/e2e/... -v -timeout 5m
  ```

  **Expected:** 3 tests pass (`TestHealthEndpoint`, `TestLoginAndAuth`, `TestRepoStatus`). **Flag if:** any fail or hang past 5 minutes.

## B.6 Real ArgoCD UI

- [ ] Port-forward ArgoCD

  ```bash
  kubectl port-forward svc/argocd-server 8090:443 -n argocd &
  PF_ARGOCD=$!
  sleep 3
  ```

- [ ] Get the ArgoCD admin password

  ```bash
  kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d; echo
  ```

- [ ] Open <https://localhost:8090> in a browser. Accept the self-signed cert. Login as `admin` + the password above.

  **Expected:** ArgoCD UI loads, "Applications" view is empty (no Sharko apps yet — none registered).

- [ ] Leave this tab open. After you do B.7 you'll come back to verify Sharko-managed apps appear here.

## B.7 Try a real flow

This is the deepest E2E test. The point is to take Sharko all the way through its core loop: connect Git → register cluster → add addon → watch ArgoCD sync.

You have two choices for the Git repo:

- **Your own GitHub fork** of a sample addons repo (recommended — full control).
- **A throwaway repo** you create just for this pass, e.g. `https://github.com/<you>/sharko-smoke-addons`.

There's no public test repo guaranteed to be there forever; use one of the above.

- [ ] In the Sharko UI (<http://localhost:8080>), Settings → Connections → add a new GitHub connection
  - URL: your fork
  - PAT: a token with `repo` scope
  - Click Test → expect green checkmark.

- [ ] Initialize the repo via UI (or `POST /api/v1/init` from CLI) — Sharko should commit its bootstrap files.

  **Expected:** PR opens against your fork (in tier-2 PR mode) or a direct commit lands (in tier-1 service mode).
  **Flag if:** silent failure, hung spinner, or commit attribution is wrong.

- [ ] Register a managed cluster — for kind smoke, you can register the in-cluster kubernetes API:
  - Name: `kind-self`
  - Server: `https://kubernetes.default.svc`
  - Provider: in-cluster

- [ ] Add an addon — pick something simple from the catalog (`metrics-server` or `cert-manager`).

  **Expected:** PR opens, applicationset entry generated, sharko shows it in the UI.

- [ ] Switch to the ArgoCD tab (B.6). Refresh. **Expected:** the Sharko-managed application is visible. Click into it; status is `Synced` / `Healthy` (may take 1–2 minutes).
  **Flag if:** never appears, or stuck `OutOfSync` indefinitely.

## B.8 Teardown

- [ ] Kill port-forwards

  ```bash
  kill $PF_SHARKO $PF_ARGOCD 2>/dev/null
  ```

- [ ] Tear down the cluster

  ```bash
  bash tests/e2e/teardown.sh
  ```

  Or directly:

  ```bash
  kind delete cluster --name sharko-e2e
  ```

  **Expected:** "Deleted nodes". **Flag if:** error — manually inspect with `kind get clusters`.

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

# Cross-reference

- [Testing Guide](testing-guide.md) — the reference doc this runbook complements (test layers, patterns, command cheatsheet)
- [Catalog Scan Runbook](catalog-scan-runbook.md) — the operational doc for the daily scanner bot
- `tests/e2e/setup.sh` and `tests/e2e/teardown.sh` — the scripts Track B leans on
- `internal/api/router.go` — source of truth for which routes actually exist (don't guess paths from memory)
