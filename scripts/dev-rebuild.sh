#!/usr/bin/env bash
#
# scripts/dev-rebuild.sh — kind + local-build dev rebuild with credential auto-extract
#
# THIS IS NOT scripts/upgrade.sh. The two scripts target different flows:
#   * scripts/upgrade.sh   — released-Helm-chart flow (helm upgrade from OCI registry,
#                            for verifying a published version against your cluster).
#   * scripts/dev-rebuild.sh (this file) — local-build flow (docker build → kind load →
#                            kubectl rollout restart) for the maintainer's inner-loop
#                            personal-smoke runbook (Track B).
#
# Use this script when you want to rebuild the working-tree code into the kind
# cluster's pod and re-extract the bootstrap admin credential + a fresh login
# token. After it runs, $ADMIN_PW and $TOKEN are ready for scripts/smoke.sh.
#
# USAGE
#   source scripts/dev-rebuild.sh    # exports $ADMIN_PW and $TOKEN into your shell
#   ./scripts/dev-rebuild.sh         # prints the export commands for you to copy
#   ./scripts/dev-rebuild.sh -h      # show this help
#
# ENV VARS (override defaults)
#   KIND_CLUSTER_NAME    (default: sharko-e2e)        — kind cluster name
#   SHARKO_NAMESPACE     (default: sharko)            — k8s namespace running Sharko
#   SHARKO_LOCAL_PORT    (default: 8080)              — host port for the kubectl port-forward
#   IMAGE_TAG            (default: e2e)               — docker image tag to build/load
#   SHARKO_DEV_PW_CACHE  (default: ~/.sharko-dev-pw)  — file caching the bootstrap password
#
# WHAT IT DOES
#   1. Pre-flight: docker daemon up, kind cluster exists, helm release exists
#   2. docker build -t sharko:$IMAGE_TAG .
#   3. kind load docker-image sharko:$IMAGE_TAG --name $KIND_CLUSTER_NAME
#   4. kubectl rollout restart -n $SHARKO_NAMESPACE deployment/sharko
#   5. kubectl rollout status (wait until new pod ready)
#   6. Obtain bootstrap admin password — IMPORTANT (V124-3.8):
#         The password is logged ONCE on FIRST install only. Subsequent pod
#         restarts do NOT re-emit it. The script tries, in order:
#           (a) the new pod's logs (first install case)
#           (b) the previous pod's logs (kubectl logs --previous)
#           (c) the cache file ($SHARKO_DEV_PW_CACHE, default ~/.sharko-dev-pw)
#         On first successful extraction the password is written to the cache so
#         later rebuilds can reuse it. To start fresh: helm uninstall + reinstall.
#   7. Restart the localhost:$SHARKO_LOCAL_PORT port-forward (kill any old one)
#   8. POST /api/v1/auth/login → extract bearer token (also verifies the password)
#   9. Either export ADMIN_PW + TOKEN (sourced) or print the export commands
#
# IDEMPOTENT: rerun safely. Existing port-forwards are killed before being restarted.
#
# FLAGS
#   --auto-install   if deployment/sharko is missing, fall back to running
#                    `./scripts/sharko-dev.sh install` instead of erroring out
#   -h | --help      show this help and exit
#

# IMPORTANT — V124-8.2 / BUG-026 fix:
# Do NOT `set -e` at the top level. When a user runs `source dev-rebuild.sh`,
# `set -e` leaks errexit into their interactive shell — any subsequent
# non-zero command (e.g. `grep` finding no match) closes the terminal.
# Error handling is done explicitly per-command via `_exit_or_return $?` (see
# below) so we don't need errexit at all.

# ---- detect sourced vs direct invocation ----
# When sourced, BASH_SOURCE[0] != $0. When run as ./script, they're equal.
if [[ "${BASH_SOURCE[0]}" != "${0}" ]]; then
    SOURCED=1
else
    SOURCED=0
fi

# Helper: exit-or-return depending on invocation mode. Sourced scripts must
# NOT call `exit` because that would kill the user's interactive shell.
#
# V124-8.2 note: an earlier version called `kill -INT $$` as a fallback when
# `return` failed (i.e., when sourced from outside a function). That fallback
# was removed because it's strictly more dangerous than the failure mode it
# tried to prevent — it can SIGINT the user's interactive shell. Callers are
# expected to chain `_exit_or_return $?` with `return $code 2>/dev/null` so
# the sourced flow propagates the failure naturally.
_exit_or_return() {
    local code="${1:-1}"
    if [ "$SOURCED" = "1" ]; then
        return "$code" 2>/dev/null || true
        # If `return` failed (we're not in a function context), the caller's
        # subsequent `return $code 2>/dev/null || exit $code` chain handles it.
    else
        exit "$code"
    fi
}

# ---- arg handling ----
AUTO_INSTALL=0
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            sed -n '2,52p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
            _exit_or_return 0
            return 0 2>/dev/null || true
            ;;
        --auto-install)
            AUTO_INSTALL=1
            ;;
    esac
done

# ---- defaults ----
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-sharko-e2e}"
SHARKO_NAMESPACE="${SHARKO_NAMESPACE:-sharko}"
SHARKO_LOCAL_PORT="${SHARKO_LOCAL_PORT:-8080}"
IMAGE_TAG="${IMAGE_TAG:-e2e}"
SHARKO_REMOTE_PORT="${SHARKO_REMOTE_PORT:-80}"

echo "Sharko dev-rebuild"
echo "  cluster:    ${KIND_CLUSTER_NAME}"
echo "  namespace:  ${SHARKO_NAMESPACE}"
echo "  port:       localhost:${SHARKO_LOCAL_PORT}"
echo "  image tag:  sharko:${IMAGE_TAG}"
echo

# ---- pre-flight checks ----
if ! command -v docker >/dev/null 2>&1; then
    echo "[FAIL] docker not in PATH" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
if ! docker info >/dev/null 2>&1; then
    echo "[FAIL] docker daemon not running (try: open -a Docker  /  colima start)" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
if ! command -v kind >/dev/null 2>&1; then
    echo "[FAIL] kind not in PATH (brew install kind)" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
if ! kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
    echo "[FAIL] kind cluster '${KIND_CLUSTER_NAME}' not found." >&2
    echo "       Bring it up first: bash tests/e2e/setup.sh" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
if ! command -v kubectl >/dev/null 2>&1; then
    echo "[FAIL] kubectl not in PATH" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
if ! kubectl get deployment -n "${SHARKO_NAMESPACE}" sharko >/dev/null 2>&1; then
    if [ "$AUTO_INSTALL" = "1" ]; then
        # V124-8.2: auto-install fallback. Forward to sharko-dev.sh install,
        # which knows how to docker build, kind load, helm install, and start
        # the port-forward. This makes `dev-rebuild.sh --auto-install` a
        # one-shot "get me back to a running env" recovery command.
        echo "[INFO] deployment/sharko missing — --auto-install requested" >&2
        script_dir_local="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
        if [ ! -x "${script_dir_local}/sharko-dev.sh" ]; then
            echo "[FAIL] --auto-install needs ${script_dir_local}/sharko-dev.sh (not found/executable)" >&2
            _exit_or_return 1
            return 1 2>/dev/null || exit 1
        fi
        if ! "${script_dir_local}/sharko-dev.sh" install; then
            echo "[FAIL] auto-install failed; cannot continue with rebuild" >&2
            _exit_or_return 1
            return 1 2>/dev/null || exit 1
        fi
        # After install, the deployment exists and the image was just built.
        # We can either stop here (install already did the heavy lifting) or
        # continue with the normal rebuild flow which would just re-do the
        # docker build + rollout restart. Stop here to save time.
        echo "[OK] auto-install complete; deployment/sharko is now ready."
        echo "     For a code-change rebuild from here, re-run dev-rebuild.sh."
        _exit_or_return 0
        return 0 2>/dev/null || exit 0
    fi
    echo "[FAIL] deployment/sharko not found in namespace '${SHARKO_NAMESPACE}'." >&2
    echo "       Install first: ./scripts/sharko-dev.sh install" >&2
    echo "       Or pass --auto-install to fall back automatically." >&2
    echo "       Manual install:" >&2
    echo "         helm install sharko charts/sharko/ \\" >&2
    echo "           --namespace ${SHARKO_NAMESPACE} --create-namespace \\" >&2
    echo "           --set image.repository=sharko --set image.tag=${IMAGE_TAG} \\" >&2
    echo "           --set image.pullPolicy=Never" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi

# ---- 1. docker build ----
echo "[1/6] docker build -t sharko:${IMAGE_TAG} ."
if ! docker build -t "sharko:${IMAGE_TAG}" . >/tmp/sharko-dev-rebuild-build.log 2>&1; then
    echo "[FAIL] docker build failed. Last 20 lines:" >&2
    tail -20 /tmp/sharko-dev-rebuild-build.log >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
echo "      ok"

# ---- 2. kind load ----
echo "[2/6] kind load docker-image sharko:${IMAGE_TAG} --name ${KIND_CLUSTER_NAME}"
if ! kind load docker-image "sharko:${IMAGE_TAG}" --name "${KIND_CLUSTER_NAME}" >/dev/null 2>&1; then
    echo "[FAIL] kind load failed" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
echo "      ok"

# ---- 3. rollout restart ----
# V124-8.2: explicit error check (was implicit via `set -e` before).
echo "[3/6] kubectl rollout restart -n ${SHARKO_NAMESPACE} deployment/sharko"
if ! kubectl rollout restart -n "${SHARKO_NAMESPACE}" deployment/sharko >/dev/null 2>&1; then
    echo "[FAIL] kubectl rollout restart failed" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi

# ---- 4. wait for rollout ----
echo "[4/6] kubectl rollout status (timeout 120s)"
if ! kubectl rollout status -n "${SHARKO_NAMESPACE}" deployment/sharko --timeout=120s >/dev/null 2>&1; then
    echo "[FAIL] rollout did not finish within 120s. Recent pod logs:" >&2
    kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko --tail=20 >&2 || true
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
echo "      ok"

# ---- 5. extract the bootstrap admin password ----
# IMPORTANT (V124-3.8 semantics): the bootstrap admin password is logged ONCE
# on FIRST install only. On subsequent pod restarts (which is what `kubectl
# rollout restart` triggers) the new pod does NOT re-emit the credential —
# admin.initialPassword has already been wiped from the K8s Secret.
#
# Strategy:
#   a) If the new pod has just-emitted the line (first install), read it.
#   b) Otherwise, try the previous pod's logs (`kubectl logs --previous`) —
#      sometimes the kubelet still has them from the immediately-prior pod.
#   c) Otherwise, fall back to the maintainer's cache file (~/.sharko-dev-pw).
#   d) If all three fail, tell the user how to recover (helm uninstall + reinstall).
#
# The slog handler in cmd/sharko/serve.go is JSONHandler so the log line looks like:
#   {"...","msg":"bootstrap admin generated","username":"admin","password":"<16char>"}
# We also grep the text-handler form (password=abc) for forward-compat.

PW_CACHE="${SHARKO_DEV_PW_CACHE:-${HOME}/.sharko-dev-pw}"

extract_pw_from_log() {
    local raw="$1"
    local pw
    # JSON handler form
    pw=$(printf '%s' "$raw" | sed -nE 's/.*"password":"([^"]+)".*/\1/p')
    # Text handler fallback
    if [ -z "$pw" ]; then
        pw=$(printf '%s' "$raw" | sed -nE 's/.*password=([^ ]+).*/\1/p')
    fi
    printf '%s' "$pw"
}

echo "[5/6] extracting bootstrap admin password"
admin_pw=""

# (a) Poll the current pod's logs (first-install case)
for i in $(seq 1 8); do
    raw=$(kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko 2>/dev/null \
        | grep "bootstrap admin generated" | head -1 || true)
    if [ -n "$raw" ]; then
        admin_pw=$(extract_pw_from_log "$raw")
        if [ -n "$admin_pw" ]; then
            echo "      ok (from current pod logs, length ${#admin_pw})"
            printf '%s' "$admin_pw" > "$PW_CACHE" 2>/dev/null || true
            chmod 600 "$PW_CACHE" 2>/dev/null || true
            break
        fi
    fi
    sleep 2
done

# (b) Try the previous pod's logs
if [ -z "$admin_pw" ]; then
    raw=$(kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko --previous 2>/dev/null \
        | grep "bootstrap admin generated" | head -1 || true)
    if [ -n "$raw" ]; then
        admin_pw=$(extract_pw_from_log "$raw")
        if [ -n "$admin_pw" ]; then
            echo "      ok (from previous pod logs, length ${#admin_pw})"
            printf '%s' "$admin_pw" > "$PW_CACHE" 2>/dev/null || true
            chmod 600 "$PW_CACHE" 2>/dev/null || true
        fi
    fi
fi

# (c) Fall back to cache (V124-3.8: password is logged ONCE only)
if [ -z "$admin_pw" ] && [ -r "$PW_CACHE" ]; then
    admin_pw=$(cat "$PW_CACHE")
    if [ -n "$admin_pw" ]; then
        echo "      ok (from cache ${PW_CACHE}, length ${#admin_pw} — will verify with login below)"
    fi
fi

# (d) Give up
if [ -z "$admin_pw" ]; then
    echo "[FAIL] could not obtain bootstrap admin password." >&2
    echo "       V124-3.8 logs the credential ONCE on first install only;" >&2
    echo "       subsequent pod restarts do NOT re-emit it." >&2
    echo "       Recovery options:" >&2
    echo "         (1) helm uninstall sharko -n ${SHARKO_NAMESPACE} && bash tests/e2e/setup.sh" >&2
    echo "         (2) Set the password manually: echo '<pw>' > ${PW_CACHE} && chmod 600 ${PW_CACHE}" >&2
    echo "       Recent log tail (current pod):" >&2
    kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko --tail=15 >&2 || true
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi

# ---- 6. restart port-forward + login ----
echo "[6/6] port-forward + login"

# Kill any existing kubectl port-forward bound to our namespace+port. Use a
# pattern that matches both `kubectl port-forward svc/sharko 8080:80` and the
# port-only spelling.
pkill -f "kubectl port-forward.*${SHARKO_NAMESPACE}.*${SHARKO_LOCAL_PORT}:" 2>/dev/null || true
sleep 1

kubectl port-forward -n "${SHARKO_NAMESPACE}" svc/sharko \
    "${SHARKO_LOCAL_PORT}:${SHARKO_REMOTE_PORT}" >/tmp/sharko-dev-rebuild-pf.log 2>&1 &
PF_PID=$!
disown 2>/dev/null || true

# Wait until the port is actually accepting connections (max 10s)
ready=0
for _ in $(seq 1 10); do
    if curl -sS -o /dev/null --max-time 1 "http://localhost:${SHARKO_LOCAL_PORT}/api/v1/health" 2>/dev/null; then
        ready=1
        break
    fi
    sleep 1
done
if [ "$ready" = "0" ]; then
    echo "[FAIL] port-forward did not become reachable on localhost:${SHARKO_LOCAL_PORT}" >&2
    echo "       port-forward log:" >&2
    cat /tmp/sharko-dev-rebuild-pf.log >&2 || true
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi

# Login
login_body=$(printf '{"username":"admin","password":"%s"}' "$admin_pw")
token=$(curl -sS -X POST \
    -H "Content-Type: application/json" \
    -d "$login_body" \
    "http://localhost:${SHARKO_LOCAL_PORT}/api/v1/auth/login" 2>/dev/null \
    | python3 -c "import sys,json
try:
    d = json.load(sys.stdin)
    print(d.get('token',''))
except Exception:
    pass" 2>/dev/null || true)

if [ -z "$token" ]; then
    echo "[FAIL] login failed — admin password may be wrong, or /api/v1/auth/login is unreachable" >&2
    _exit_or_return 1
    return 1 2>/dev/null || exit 1
fi
echo "      login ok (token prefix: ${token:0:24}...)"

# ---- emit creds ----
echo
if [ "$SOURCED" = "1" ]; then
    export ADMIN_PW="$admin_pw"
    export TOKEN="$token"
    echo "[done] \$ADMIN_PW and \$TOKEN exported into your shell."
    echo "       Next: ./scripts/smoke.sh"
else
    echo "[done] Run these in your shell to export the credentials:"
    echo
    echo "  export ADMIN_PW=$admin_pw"
    echo "  export TOKEN=$token"
    echo
    echo "Then: ./scripts/smoke.sh"
fi
