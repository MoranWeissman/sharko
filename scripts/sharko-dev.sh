#!/usr/bin/env bash
#
# scripts/sharko-dev.sh — Sharko maintainer dev-loop tool (V124-8.1)
#
# Single entry point with subcommand dispatch (like git/kubectl). Codifies
# every flow the maintainer has been pasting by hand: kind+ArgoCD bring-up,
# helm install, rebuild, reset, password extraction (smart fallback chain),
# login + token export, password rotation + verification, smoke runs, status
# checks, full teardown.
#
# USAGE
#   ./scripts/sharko-dev.sh <subcommand> [flags]
#   ./scripts/sharko-dev.sh help              # full subcommand list
#   ./scripts/sharko-dev.sh <subcommand> --help
#
# SOURCING MODEL
#   Do NOT `source` this script. Use eval-via-pipe instead:
#       eval "$(./scripts/sharko-dev.sh login --export)"
#   This avoids any set -e / set -u leak into the user's interactive shell.
#   The --export flag prints ONLY export lines so eval-via-pipe is clean.
#
# ENV VARS (override defaults)
#   KIND_CLUSTER_NAME    default: sharko-e2e
#   SHARKO_NAMESPACE     default: sharko
#   SHARKO_LOCAL_PORT    default: 8080
#   IMAGE_TAG            default: e2e
#   SHARKO_DEV_PW_CACHE  default: ~/.sharko-dev-pw  (mode 0600)
#

# Deliberately NO `set -e` at top level. We use explicit error handling per
# subcommand. set -e at the top would (a) make `source`-ing this script leak
# errexit into the user's shell — V124-5 / BUG-026 footgun — and (b) make
# subcommand error handling brittle when curl/kubectl have non-fatal failures
# the dispatcher needs to inspect.

# ---- defaults (overridable via env) ----
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-sharko-e2e}"
SHARKO_NAMESPACE="${SHARKO_NAMESPACE:-sharko}"
SHARKO_LOCAL_PORT="${SHARKO_LOCAL_PORT:-8080}"
SHARKO_REMOTE_PORT="${SHARKO_REMOTE_PORT:-80}"
IMAGE_TAG="${IMAGE_TAG:-e2e}"
SHARKO_DEV_PW_CACHE="${SHARKO_DEV_PW_CACHE:-${HOME}/.sharko-dev-pw}"
HOST="http://localhost:${SHARKO_LOCAL_PORT}"

# Repo root — derived from script location so cwd doesn't matter.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ---- color (TTY-detected) ----
if [ -t 1 ]; then
    RED=$'\033[31m'
    GREEN=$'\033[32m'
    YELLOW=$'\033[33m'
    BLUE=$'\033[34m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    RED=""
    GREEN=""
    YELLOW=""
    BLUE=""
    BOLD=""
    RESET=""
fi

OK_MARK="${GREEN}[OK]${RESET}"
INFO_MARK="${BLUE}[INFO]${RESET}"
WARN_MARK="${YELLOW}[WARN]${RESET}"
FAIL_MARK="${RED}[FAIL]${RESET}"

# ---- log helpers ----
log_ok()   { printf '%s %s\n' "$OK_MARK"   "$*"; }
log_info() { printf '%s %s\n' "$INFO_MARK" "$*"; }
log_warn() { printf '%s %s\n' "$WARN_MARK" "$*" >&2; }
log_fail() { printf '%s %s\n' "$FAIL_MARK" "$*" >&2; }

# ---- pre-flight tool check ----
preflight_tools() {
    local missing=()
    local t
    for t in kubectl kind docker helm python3 curl; do
        command -v "$t" >/dev/null 2>&1 || missing+=("$t")
    done
    if [ ${#missing[@]} -gt 0 ]; then
        log_fail "missing required tools: ${missing[*]}"
        echo "       install hints (macOS):" >&2
        for t in "${missing[@]}"; do
            case "$t" in
                kubectl) echo "         brew install kubernetes-cli" >&2 ;;
                kind)    echo "         brew install kind" >&2 ;;
                docker)  echo "         install Docker Desktop or colima" >&2 ;;
                helm)    echo "         brew install helm" >&2 ;;
                python3) echo "         brew install python3" >&2 ;;
                curl)    echo "         (curl is built-in on macOS / brew install curl)" >&2 ;;
            esac
        done
        return 1
    fi
    return 0
}

# ---- shared helpers ----

# kind_cluster_exists: 0 if the configured cluster is present, 1 otherwise.
kind_cluster_exists() {
    kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"
}

# helm_release_exists: 0 if the sharko helm release is installed in the
# configured namespace, 1 otherwise.
helm_release_exists() {
    helm list -n "${SHARKO_NAMESPACE}" -q 2>/dev/null | grep -qx "sharko"
}

# port_forward_alive: 0 if something is listening on $SHARKO_LOCAL_PORT.
port_forward_alive() {
    (echo > "/dev/tcp/127.0.0.1/${SHARKO_LOCAL_PORT}") 2>/dev/null
}

# kill_port_forward: best-effort kill of any kubectl port-forward bound to
# our namespace+port. Always returns 0 (no-op if nothing to kill).
kill_port_forward() {
    pkill -f "kubectl port-forward.*${SHARKO_NAMESPACE}.*${SHARKO_LOCAL_PORT}:" 2>/dev/null || true
    sleep 1
}

# start_port_forward: starts a backgrounded kubectl port-forward and waits up
# to 30s for /api/v1/health to return 200. Returns 0 on success, 1 on failure.
start_port_forward() {
    kill_port_forward
    kubectl port-forward -n "${SHARKO_NAMESPACE}" svc/sharko \
        "${SHARKO_LOCAL_PORT}:${SHARKO_REMOTE_PORT}" >/tmp/sharko-dev-pf.log 2>&1 &
    disown 2>/dev/null || true

    local i
    for i in $(seq 1 30); do
        if curl -sS -o /dev/null --max-time 1 "${HOST}/api/v1/health" 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    log_fail "port-forward did not become reachable on localhost:${SHARKO_LOCAL_PORT}"
    cat /tmp/sharko-dev-pf.log >&2 2>/dev/null || true
    return 1
}

# extract_pw_from_log: parse the bootstrap-admin-generated log line in either
# the JSON-handler form ("password":"...") or text-handler form (password=...).
extract_pw_from_log() {
    local raw="$1"
    local pw
    pw=$(printf '%s' "$raw" | sed -nE 's/.*"password":"([^"]+)".*/\1/p' | head -1)
    if [ -z "$pw" ]; then
        pw=$(printf '%s' "$raw" | sed -nE 's/.*password=([^ ]+).*/\1/p' | head -1)
    fi
    printf '%s' "$pw"
}

# write_pw_cache: persist the password to the cache file with mode 0600.
write_pw_cache() {
    local pw="$1"
    if [ -n "$pw" ]; then
        printf '%s' "$pw" > "${SHARKO_DEV_PW_CACHE}" 2>/dev/null || true
        chmod 600 "${SHARKO_DEV_PW_CACHE}" 2>/dev/null || true
    fi
}

# confirm_or_abort: prompt for y/N unless --yes is in $@. Returns 0 to
# proceed, 1 to abort.
#
# Usage:  confirm_or_abort <yes_flag> "<prompt>"
#   yes_flag — "1" if --yes was passed (skip prompt), "0" otherwise
confirm_or_abort() {
    local yes_flag="$1"
    local prompt="$2"
    if [ "$yes_flag" = "1" ]; then
        return 0
    fi
    printf '%s [y/N] ' "$prompt"
    local reply=""
    read -r reply
    case "$reply" in
        y|Y|yes|YES) return 0 ;;
        *) echo "aborted."; return 1 ;;
    esac
}

# =====================================================================
# Subcommand: do_up
# End-to-end "from nothing to running": kind + ArgoCD + Sharko + creds.
# =====================================================================
do_up() {
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh up — bring up dev environment from nothing

Creates kind cluster (if missing), installs ArgoCD (if missing), then
forwards to 'install' to build + load + helm install Sharko.

Idempotent: re-running on a partially-up environment skips work that's
already done.

Usage: ./scripts/sharko-dev.sh up [--help]
EOF
                return 0
                ;;
        esac
    done

    log_info "bringing up dev environment (cluster=${KIND_CLUSTER_NAME}, namespace=${SHARKO_NAMESPACE})"

    # 1. kind cluster
    if kind_cluster_exists; then
        log_ok "kind cluster '${KIND_CLUSTER_NAME}' already exists"
    else
        log_info "creating kind cluster '${KIND_CLUSTER_NAME}'"
        if ! kind create cluster --name "${KIND_CLUSTER_NAME}" --wait 60s; then
            log_fail "kind create cluster failed"
            return 1
        fi
        log_ok "kind cluster created"
    fi

    # 2. ArgoCD
    if kubectl get namespace argocd >/dev/null 2>&1 \
       && kubectl get deployment -n argocd argocd-server >/dev/null 2>&1; then
        log_ok "ArgoCD already installed"
    else
        log_info "installing ArgoCD (server-side apply for large CRDs)"
        kubectl create namespace argocd >/dev/null 2>&1 || true
        if ! kubectl apply --server-side --force-conflicts -n argocd \
             -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml; then
            log_fail "ArgoCD manifest apply failed"
            return 1
        fi
        log_info "waiting for argocd-server (timeout 180s)"
        if ! kubectl wait --for=condition=available --timeout=180s deployment/argocd-server -n argocd; then
            log_fail "argocd-server did not become available within 180s"
            return 1
        fi
        log_ok "ArgoCD ready"
    fi

    # 3. Forward to install
    do_install || return $?

    echo
    log_ok "dev environment is up — verify with: ./scripts/sharko-dev.sh status"
    return 0
}

# =====================================================================
# Subcommand: do_install
# Build, kind-load, helm install on an existing kind cluster.
# =====================================================================
do_install() {
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh install — install Sharko on an existing kind cluster

Steps: docker daemon check, docker build, kind load, helm install,
rollout wait, port-forward start, bootstrap password extraction.

Idempotent: if the helm release already exists, exits with a hint to
use 'rebuild' instead.

Usage: ./scripts/sharko-dev.sh install [--help]
EOF
                return 0
                ;;
        esac
    done

    # 0. cluster present?
    if ! kind_cluster_exists; then
        log_fail "kind cluster '${KIND_CLUSTER_NAME}' not found"
        echo "       Run: ./scripts/sharko-dev.sh up" >&2
        return 1
    fi

    # 1. docker daemon up?
    if ! docker info >/dev/null 2>&1; then
        log_fail "docker daemon not running (try: open -a Docker / colima start)"
        return 1
    fi

    # 2. already installed?
    if helm_release_exists; then
        log_warn "helm release 'sharko' already exists in namespace '${SHARKO_NAMESPACE}'"
        echo "       To rebuild after a code change: ./scripts/sharko-dev.sh rebuild" >&2
        echo "       To start over: ./scripts/sharko-dev.sh reset && ./scripts/sharko-dev.sh install" >&2
        return 1
    fi

    # 3. docker build
    log_info "docker build -t sharko:${IMAGE_TAG} . (cwd=${REPO_ROOT})"
    if ! (cd "${REPO_ROOT}" && docker build -t "sharko:${IMAGE_TAG}" .) >/tmp/sharko-dev-build.log 2>&1; then
        log_fail "docker build failed (last 20 lines):"
        tail -20 /tmp/sharko-dev-build.log >&2
        return 1
    fi
    log_ok "image built"

    # 4. kind load
    log_info "kind load docker-image sharko:${IMAGE_TAG} --name ${KIND_CLUSTER_NAME}"
    if ! kind load docker-image "sharko:${IMAGE_TAG}" --name "${KIND_CLUSTER_NAME}" >/dev/null 2>&1; then
        log_fail "kind load failed"
        return 1
    fi
    log_ok "image loaded into kind"

    # 5. helm install
    log_info "helm install sharko in namespace '${SHARKO_NAMESPACE}'"
    if ! helm install sharko "${REPO_ROOT}/charts/sharko/" \
         --namespace "${SHARKO_NAMESPACE}" --create-namespace \
         --set image.repository=sharko \
         --set image.tag="${IMAGE_TAG}" \
         --set image.pullPolicy=Never >/tmp/sharko-dev-helm.log 2>&1; then
        log_fail "helm install failed (last 20 lines):"
        tail -20 /tmp/sharko-dev-helm.log >&2
        return 1
    fi
    log_ok "helm install complete"

    # 6. rollout wait
    log_info "kubectl rollout status (timeout 120s)"
    if ! kubectl rollout status -n "${SHARKO_NAMESPACE}" deployment/sharko --timeout=120s >/dev/null 2>&1; then
        log_fail "deployment/sharko did not become ready within 120s"
        kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko --tail=20 >&2 || true
        return 1
    fi
    log_ok "deployment ready"

    # 7. start port-forward
    log_info "starting port-forward localhost:${SHARKO_LOCAL_PORT} -> svc/sharko:${SHARKO_REMOTE_PORT}"
    if ! start_port_forward; then
        return 1
    fi
    log_ok "port-forward up; /api/v1/health: 200"

    # 8. extract creds (best-effort — first install logs the password to stdout)
    log_info "extracting bootstrap admin password"
    do_creds --quiet >/dev/null 2>&1 || log_warn "could not auto-extract password — try: ./scripts/sharko-dev.sh creds"

    echo
    log_ok "Sharko installed (image: sharko:${IMAGE_TAG})"
    echo "       Port-forward: ${HOST}"
    echo "       Capture creds: eval \"\$(./scripts/sharko-dev.sh login --export)\""
    return 0
}

# =====================================================================
# Subcommand: do_rebuild
# Forwards to scripts/dev-rebuild.sh; refreshes creds cache after.
# =====================================================================
do_rebuild() {
    local auto_install=0
    local forwarded_args=()
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh rebuild — rebuild Sharko after a code change

Forwards to scripts/dev-rebuild.sh (V124-5.1) which does docker build,
kind load, kubectl rollout restart, then port-forward + login.
Refreshes ~/.sharko-dev-pw on success.

Flags:
  --auto-install   if no helm release exists, fall back to 'install'
                   instead of erroring out
  --help           this help

Other flags are passed through to dev-rebuild.sh.

Usage: ./scripts/sharko-dev.sh rebuild [--auto-install]
EOF
                return 0
                ;;
            --auto-install)
                auto_install=1
                ;;
            *)
                forwarded_args+=("$arg")
                ;;
        esac
    done

    # If no helm release, either install or punt to user.
    if ! helm_release_exists; then
        if [ "$auto_install" = "1" ]; then
            log_info "no helm release found — auto-falling back to install"
            do_install || return $?
            return 0
        fi
        log_fail "no helm release 'sharko' found in namespace '${SHARKO_NAMESPACE}'"
        echo "       Run: ./scripts/sharko-dev.sh install" >&2
        echo "       Or:  ./scripts/sharko-dev.sh rebuild --auto-install" >&2
        return 1
    fi

    if [ ! -x "${SCRIPT_DIR}/dev-rebuild.sh" ]; then
        log_fail "dev-rebuild.sh not found or not executable: ${SCRIPT_DIR}/dev-rebuild.sh"
        return 1
    fi

    # Forward. dev-rebuild.sh handles its own pre-flight + error reporting.
    "${SCRIPT_DIR}/dev-rebuild.sh" "${forwarded_args[@]}"
    local rc=$?
    if [ $rc -ne 0 ]; then
        return $rc
    fi

    # Refresh creds cache after a successful rebuild — dev-rebuild.sh writes
    # ~/.sharko-dev-pw on its own, but call do_creds quietly to verify the
    # password is still retrievable through one of the documented paths.
    do_creds --quiet >/dev/null 2>&1 || log_warn "creds refresh after rebuild failed — try: ./scripts/sharko-dev.sh creds"
    return 0
}

# =====================================================================
# Subcommand: do_reset
# Cleanup helm release + secrets but PRESERVE kind cluster + ArgoCD.
# =====================================================================
do_reset() {
    local yes=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh reset — uninstall Sharko but keep kind + ArgoCD

Removes: helm release 'sharko', secrets (sharko, sharko-connections,
sharko-initial-admin-secret), the password cache, and any stale
port-forward.

Preserves: the kind cluster and the ArgoCD installation.

Flags:
  --yes    skip confirmation prompt
  --help   this help

Usage: ./scripts/sharko-dev.sh reset [--yes]
EOF
                return 0
                ;;
            --yes|-y)
                yes=1
                ;;
        esac
    done

    confirm_or_abort "$yes" \
        "This will uninstall Sharko (helm release + secrets + cache) but keep the kind cluster. Continue?" \
        || return 1

    log_info "helm uninstall sharko -n ${SHARKO_NAMESPACE}"
    helm uninstall sharko -n "${SHARKO_NAMESPACE}" >/dev/null 2>&1 || true

    log_info "kubectl delete secrets (sharko, sharko-connections, sharko-initial-admin-secret)"
    kubectl delete secret -n "${SHARKO_NAMESPACE}" \
        sharko sharko-connections sharko-initial-admin-secret \
        --ignore-not-found=true >/dev/null 2>&1 || true

    log_info "removing password cache ${SHARKO_DEV_PW_CACHE}"
    rm -f "${SHARKO_DEV_PW_CACHE}"

    log_info "killing any port-forward on localhost:${SHARKO_LOCAL_PORT}"
    kill_port_forward

    echo
    log_ok "reset complete"
    echo "       To bring Sharko back up: ./scripts/sharko-dev.sh install"
    return 0
}

# =====================================================================
# Subcommand: do_creds
# Smart fallback chain: V124-6.3 secret -> cache -> current pod logs ->
# previous pod logs -> error with recovery hints.
# =====================================================================
do_creds() {
    local mode="default"   # default | export | quiet
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh creds — extract the current admin password

Smart fallback chain:
  1. kubectl get secret sharko-initial-admin-secret (V124-6.3 — primary)
  2. ~/.sharko-dev-pw cache file
  3. Current pod logs (kubectl logs)
  4. Previous pod logs (kubectl logs --previous)
  5. Error with recovery hints

On success the cache file is refreshed (mode 0600).

Output modes:
  default       human-readable success line + hint
  --export      ONLY 'export ADMIN_PW=...' (for: eval "\$(... --export)")
  -q|--quiet    ONLY the plaintext password (for piping)
  --help        this help

Usage: ./scripts/sharko-dev.sh creds [--export | --quiet]
EOF
                return 0
                ;;
            --export) mode="export" ;;
            -q|--quiet) mode="quiet" ;;
        esac
    done

    local pw=""
    local source=""

    # Path 1: V124-6.3 secret (best — persistent across rotations as of V124-7)
    pw=$(kubectl get secret -n "${SHARKO_NAMESPACE}" sharko-initial-admin-secret \
        -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)
    if [ -n "$pw" ]; then
        source="secret"
    fi

    # Path 2: cache file
    if [ -z "$pw" ] && [ -r "${SHARKO_DEV_PW_CACHE}" ]; then
        pw=$(cat "${SHARKO_DEV_PW_CACHE}" 2>/dev/null || true)
        if [ -n "$pw" ]; then
            source="cache"
        fi
    fi

    # Path 3: current pod logs
    if [ -z "$pw" ]; then
        local raw
        raw=$(kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko 2>/dev/null \
            | grep "bootstrap admin generated" | head -1 || true)
        if [ -n "$raw" ]; then
            pw=$(extract_pw_from_log "$raw")
            if [ -n "$pw" ]; then
                source="logs"
            fi
        fi
    fi

    # Path 4: previous pod logs
    if [ -z "$pw" ]; then
        local raw
        raw=$(kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko --previous 2>/dev/null \
            | grep "bootstrap admin generated" | head -1 || true)
        if [ -n "$raw" ]; then
            pw=$(extract_pw_from_log "$raw")
            if [ -n "$pw" ]; then
                source="previous-logs"
            fi
        fi
    fi

    # Path 5: failure with recovery hints
    if [ -z "$pw" ]; then
        log_fail "could not retrieve admin password through any of:"
        echo "         1. kubectl get secret sharko-initial-admin-secret (V124-6.3)" >&2
        echo "         2. cache file ${SHARKO_DEV_PW_CACHE}" >&2
        echo "         3. current pod logs (bootstrap line)" >&2
        echo "         4. previous pod logs (kubectl logs --previous)" >&2
        echo "       Recovery options:" >&2
        echo "         ./scripts/sharko-dev.sh rotate              # generate a new password" >&2
        echo "         ./scripts/sharko-dev.sh reset --yes && ./scripts/sharko-dev.sh install   # full reset" >&2
        return 1
    fi

    # Refresh cache unless that's where we got it from.
    if [ "$source" != "cache" ]; then
        write_pw_cache "$pw"
    fi

    case "$mode" in
        export)
            printf 'export ADMIN_PW=%s\n' "$pw"
            ;;
        quiet)
            printf '%s\n' "$pw"
            ;;
        *)
            log_ok "admin password retrieved (path: ${source})"
            printf '       Password: %s\n' "$pw"
            echo "       Capture into your shell:  eval \"\$(./scripts/sharko-dev.sh creds --export)\""
            ;;
    esac
    return 0
}

# =====================================================================
# Subcommand: do_login
# POSTs /api/v1/auth/login, extracts the bearer token.
# =====================================================================
do_login() {
    local mode="default"
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh login — login as admin and extract bearer token

Uses \$ADMIN_PW if set; otherwise calls 'creds' to fetch it. POSTs to
/api/v1/auth/login on localhost:${SHARKO_LOCAL_PORT}, parses the
response, and emits ADMIN_PW + TOKEN.

Output modes:
  default       human-readable success + hint
  --export      ONLY 'export ADMIN_PW=...; export TOKEN=...' (for eval-via-pipe)
  -q|--quiet    ONLY the token (for piping)
  --help        this help

Usage: ./scripts/sharko-dev.sh login [--export | --quiet]
EOF
                return 0
                ;;
            --export) mode="export" ;;
            -q|--quiet) mode="quiet" ;;
        esac
    done

    # Get the password.
    local pw="${ADMIN_PW:-}"
    if [ -z "$pw" ]; then
        pw=$(do_creds --quiet 2>/dev/null || true)
    fi
    if [ -z "$pw" ]; then
        log_fail "no admin password available"
        echo "       Try: ./scripts/sharko-dev.sh creds   to diagnose" >&2
        return 1
    fi

    # Port-forward up?
    if ! port_forward_alive; then
        log_fail "port-forward to localhost:${SHARKO_LOCAL_PORT} is not alive"
        echo "       Run: ./scripts/sharko-dev.sh install   (or rebuild) to bring it back" >&2
        return 1
    fi

    # POST /api/v1/auth/login
    local body
    body=$(printf '{"username":"admin","password":"%s"}' "$pw")
    local response
    response=$(curl -sS -X POST \
        -H "Content-Type: application/json" \
        -d "$body" \
        --max-time 10 \
        "${HOST}/api/v1/auth/login" 2>/dev/null || true)

    local token
    token=$(printf '%s' "$response" | python3 -c "import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('token', ''))
except Exception:
    pass" 2>/dev/null || true)

    if [ -z "$token" ]; then
        log_fail "login failed (no token in response)"
        echo "       Possible causes:" >&2
        echo "         - admin password is wrong (try: ./scripts/sharko-dev.sh rotate)" >&2
        echo "         - rate-limit lockout (wait 60s and retry)" >&2
        echo "         - port-forward returning HTML (try: ./scripts/sharko-dev.sh status)" >&2
        echo "       Raw response (first 200 chars):" >&2
        printf '         %s\n' "$(printf '%s' "$response" | head -c 200)" >&2
        return 1
    fi

    case "$mode" in
        export)
            printf 'export ADMIN_PW=%s\n' "$pw"
            printf 'export TOKEN=%s\n' "$token"
            ;;
        quiet)
            printf '%s\n' "$token"
            ;;
        *)
            log_ok "logged in as admin"
            printf '       Token: %s...\n' "${token:0:20}"
            echo "       Capture into your shell:  eval \"\$(./scripts/sharko-dev.sh login --export)\""
            ;;
    esac
    return 0
}

# =====================================================================
# Subcommand: do_rotate
# Rotates admin password via 'sharko reset-admin' and verifies V124-7's
# secret-rotation behavior (the new password should land in the secret).
# =====================================================================
do_rotate() {
    local mode="default"
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh rotate — rotate the admin password

Runs 'sharko reset-admin' inside the deployment pod, parses the new
password from stdout, refreshes the cache, asserts the
sharko-initial-admin-secret has been updated to match (V124-7
behavior — secret rotates, not just deletes), then re-logs in.

If the secret/password mismatch is detected after rotation, the
command exits non-zero (V124-7 regression alarm).

Output modes:
  default       human-readable success + hint
  --export      ONLY 'export ADMIN_PW=...; export TOKEN=...'
  -q|--quiet    ONLY the new token
  --help        this help

Usage: ./scripts/sharko-dev.sh rotate [--export | --quiet]
EOF
                return 0
                ;;
            --export) mode="export" ;;
            -q|--quiet) mode="quiet" ;;
        esac
    done

    if ! kubectl get deployment -n "${SHARKO_NAMESPACE}" sharko >/dev/null 2>&1; then
        log_fail "deployment/sharko not found in namespace '${SHARKO_NAMESPACE}'"
        return 1
    fi

    log_info "running 'sharko reset-admin' in deployment/sharko"
    local raw
    raw=$(kubectl exec -n "${SHARKO_NAMESPACE}" deployment/sharko -- \
        sharko reset-admin --namespace "${SHARKO_NAMESPACE}" --secret sharko 2>&1)
    local rc=$?
    if [ $rc -ne 0 ]; then
        log_fail "sharko reset-admin returned non-zero ($rc)"
        printf '%s\n' "$raw" >&2
        return 1
    fi

    # Parse the new password. V124-7 prints lines like:
    #   "New password: <plaintext>"  or  "password: <plaintext>"
    # Try the most explicit pattern first, then a couple of fallbacks.
    local new_pw=""
    new_pw=$(printf '%s\n' "$raw" | grep -oE 'New password: [A-Za-z0-9_\-]+' | head -1 | awk '{print $3}')
    if [ -z "$new_pw" ]; then
        new_pw=$(printf '%s\n' "$raw" | grep -oE '[Pp]assword: [A-Za-z0-9_\-]+' | head -1 | awk '{print $2}')
    fi
    if [ -z "$new_pw" ]; then
        # JSON form (slog handler)
        new_pw=$(printf '%s\n' "$raw" | sed -nE 's/.*"password":"([^"]+)".*/\1/p' | head -1)
    fi
    if [ -z "$new_pw" ]; then
        log_fail "could not parse new password from reset-admin output"
        echo "       Raw output (first 30 lines):" >&2
        printf '%s\n' "$raw" | head -30 >&2
        return 1
    fi
    log_ok "new password generated (length ${#new_pw})"

    # Refresh cache.
    write_pw_cache "$new_pw"

    # V124-7 verification: the secret should now contain the new password.
    # Give k8s a brief moment for the secret update to propagate.
    sleep 1
    local secret_pw
    secret_pw=$(kubectl get secret -n "${SHARKO_NAMESPACE}" sharko-initial-admin-secret \
        -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)

    if [ -z "$secret_pw" ]; then
        log_fail "V124-7 regression: sharko-initial-admin-secret has no .data.password after rotation"
        return 1
    fi
    if [ "$secret_pw" != "$new_pw" ]; then
        log_fail "V124-7 regression: secret password does not match rotated password"
        echo "       secret has length ${#secret_pw}, rotated password has length ${#new_pw}" >&2
        return 1
    fi
    log_ok "V124-7 verified: sharko-initial-admin-secret matches new password"

    # Re-login with the new password.
    log_info "re-logging in with new password"
    ADMIN_PW="$new_pw"
    export ADMIN_PW
    local token=""
    token=$(do_login --quiet 2>/dev/null || true)
    if [ -z "$token" ]; then
        log_fail "login with new password failed"
        return 1
    fi
    log_ok "re-login succeeded (token prefix: ${token:0:20}...)"

    case "$mode" in
        export)
            printf 'export ADMIN_PW=%s\n' "$new_pw"
            printf 'export TOKEN=%s\n' "$token"
            ;;
        quiet)
            printf '%s\n' "$token"
            ;;
        *)
            log_ok "rotation complete"
            printf '       New password: %s\n' "$new_pw"
            printf '       Token: %s...\n' "${token:0:20}"
            echo "       Capture into your shell:  eval \"\$(./scripts/sharko-dev.sh rotate --export)\""
            ;;
    esac
    return 0
}

# =====================================================================
# Subcommand: do_smoke
# Forwards to scripts/smoke.sh after auto-extracting creds if missing.
# =====================================================================
do_smoke() {
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh smoke — run the personal-smoke test suite

Forwards to scripts/smoke.sh (V124-5.2). If \$ADMIN_PW or \$TOKEN are
not set in the environment, this command will auto-extract them via
'creds' + 'login' first.

All other flags are passed through to smoke.sh (-v for verbose, etc.).

Usage: ./scripts/sharko-dev.sh smoke [smoke.sh-flags]
EOF
                return 0
                ;;
        esac
    done

    if [ ! -x "${SCRIPT_DIR}/smoke.sh" ]; then
        log_fail "smoke.sh not found or not executable: ${SCRIPT_DIR}/smoke.sh"
        return 1
    fi

    # Auto-extract creds if either is missing.
    if [ -z "${ADMIN_PW:-}" ] || [ -z "${TOKEN:-}" ]; then
        log_info "auto-extracting credentials (ADMIN_PW and/or TOKEN unset)"
        local creds_export
        creds_export=$(do_login --export 2>&1)
        local rc=$?
        if [ $rc -ne 0 ]; then
            log_fail "auto-login failed; cannot run smoke without creds"
            printf '%s\n' "$creds_export" >&2
            return 1
        fi
        # Eval the export lines into our process so smoke.sh inherits them.
        eval "$creds_export"
        export ADMIN_PW TOKEN
    fi

    "${SCRIPT_DIR}/smoke.sh" "$@"
    return $?
}

# =====================================================================
# Subcommand: do_status
# One-shot env state check.
# =====================================================================
do_status() {
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh status — show current dev-env state

Reports: kind cluster, Sharko deployment, port-forward, /api/v1/health,
admin password retrievability, current TOKEN validity, ArgoCD readiness.

Exit code: 0 if all green, 1 if anything is broken.

Usage: ./scripts/sharko-dev.sh status
EOF
                return 0
                ;;
        esac
    done

    local exit_rc=0

    echo "${BOLD}Sharko dev environment status${RESET}"
    echo "=============================="

    # Cluster
    if kind_cluster_exists; then
        local current_ctx
        current_ctx=$(kubectl config current-context 2>/dev/null || echo "?")
        local expected_ctx="kind-${KIND_CLUSTER_NAME}"
        if [ "$current_ctx" = "$expected_ctx" ]; then
            printf '  cluster:        %skind-%s%s (current context)\n' "$GREEN" "${KIND_CLUSTER_NAME}" "$RESET"
        else
            printf '  cluster:        %skind-%s%s (current ctx is %s — kubectl will not target sharko)\n' \
                "$YELLOW" "${KIND_CLUSTER_NAME}" "$RESET" "$current_ctx"
            exit_rc=1
        fi
    else
        printf '  cluster:        %skind-%s not found%s\n' "$RED" "${KIND_CLUSTER_NAME}" "$RESET"
        exit_rc=1
    fi

    # Sharko deployment
    local avail
    avail=$(kubectl get deployment -n "${SHARKO_NAMESPACE}" sharko \
        -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "")
    local desired
    desired=$(kubectl get deployment -n "${SHARKO_NAMESPACE}" sharko \
        -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")
    if [ -n "$avail" ] && [ "$avail" -ge 1 ] 2>/dev/null; then
        local pod
        pod=$(kubectl get pod -n "${SHARKO_NAMESPACE}" -l app.kubernetes.io/name=sharko \
            -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "?")
        printf '  Sharko:         %s%s %s/%s ready%s\n' "$GREEN" "$pod" "$avail" "${desired:-?}" "$RESET"
    else
        printf '  Sharko:         %sdeployment not Available in ns=%s%s\n' "$RED" "${SHARKO_NAMESPACE}" "$RESET"
        exit_rc=1
    fi

    # Port-forward
    if port_forward_alive; then
        printf '  port-forward:   %slocalhost:%s (alive)%s\n' "$GREEN" "${SHARKO_LOCAL_PORT}" "$RESET"
    else
        printf '  port-forward:   %sno listener on localhost:%s%s\n' "$RED" "${SHARKO_LOCAL_PORT}" "$RESET"
        exit_rc=1
    fi

    # /api/v1/health
    # Note: curl -w '%{http_code}' always prints a code (000 on connection
    # failure). Don't add `|| echo 000` — that doubles the output.
    local health_code
    health_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
        "${HOST}/api/v1/health" 2>/dev/null)
    [ -z "$health_code" ] && health_code="000"
    if [ "$health_code" = "200" ]; then
        printf '  /api/v1/health: %s200%s\n' "$GREEN" "$RESET"
    else
        printf '  /api/v1/health: %s%s%s\n' "$RED" "$health_code" "$RESET"
        exit_rc=1
    fi

    # Admin password retrievability — repeat the fallback chain logic
    local pw_path=""
    if kubectl get secret -n "${SHARKO_NAMESPACE}" sharko-initial-admin-secret >/dev/null 2>&1 \
       && [ -n "$(kubectl get secret -n "${SHARKO_NAMESPACE}" sharko-initial-admin-secret \
            -o jsonpath='{.data.password}' 2>/dev/null)" ]; then
        pw_path="secret (V124-6.3)"
    elif [ -r "${SHARKO_DEV_PW_CACHE}" ] && [ -s "${SHARKO_DEV_PW_CACHE}" ]; then
        pw_path="cache (${SHARKO_DEV_PW_CACHE})"
    elif kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko 2>/dev/null \
         | grep -q "bootstrap admin generated"; then
        pw_path="current pod logs"
    elif kubectl logs -n "${SHARKO_NAMESPACE}" deployment/sharko --previous 2>/dev/null \
         | grep -q "bootstrap admin generated"; then
        pw_path="previous pod logs"
    fi
    if [ -n "$pw_path" ]; then
        printf '  admin password: %sretrievable via %s%s\n' "$GREEN" "$pw_path" "$RESET"
    else
        printf '  admin password: %snot retrievable (run: rotate or reset+install)%s\n' "$RED" "$RESET"
        exit_rc=1
    fi

    # Current TOKEN validity
    if [ -n "${TOKEN:-}" ]; then
        local me_code
        me_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
            -H "Authorization: Bearer ${TOKEN}" \
            "${HOST}/api/v1/users/me" 2>/dev/null)
        [ -z "$me_code" ] && me_code="000"
        if [ "$me_code" = "200" ]; then
            printf '  current TOKEN:  %svalid (last 20 chars: ...%s)%s\n' "$GREEN" "${TOKEN: -20}" "$RESET"
        elif [ "$me_code" = "401" ]; then
            printf '  current TOKEN:  %sstale (401 — re-run: login)%s\n' "$YELLOW" "$RESET"
        else
            printf '  current TOKEN:  %sunexpected status %s on /users/me%s\n' "$YELLOW" "$me_code" "$RESET"
        fi
    else
        printf '  current TOKEN:  %s$TOKEN unset (run: login --export)%s\n' "$YELLOW" "$RESET"
    fi

    # ArgoCD
    local argo_avail
    argo_avail=$(kubectl get deployment -n argocd argocd-server \
        -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "")
    if [ -n "$argo_avail" ] && [ "$argo_avail" -ge 1 ] 2>/dev/null; then
        printf '  ArgoCD:         %sargocd-server %s/1 ready%s\n' "$GREEN" "$argo_avail" "$RESET"
    else
        printf '  ArgoCD:         %snot ready (run: up)%s\n' "$YELLOW" "$RESET"
    fi

    echo
    if [ $exit_rc -eq 0 ]; then
        log_ok "all green"
    else
        log_warn "issues detected (see lines marked above)"
    fi
    return $exit_rc
}

# =====================================================================
# Subcommand: do_down
# Full teardown: delete kind cluster + cache + port-forward.
# =====================================================================
do_down() {
    local yes=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
sharko-dev.sh down — full teardown

Deletes the kind cluster '${KIND_CLUSTER_NAME}' and ALL associated state
(Sharko, ArgoCD, the password cache, any port-forward).

Flags:
  --yes    skip confirmation prompt
  --help   this help

Usage: ./scripts/sharko-dev.sh down [--yes]
EOF
                return 0
                ;;
            --yes|-y)
                yes=1
                ;;
        esac
    done

    confirm_or_abort "$yes" \
        "This will DELETE the kind cluster '${KIND_CLUSTER_NAME}' and ALL state. Continue?" \
        || return 1

    log_info "killing port-forward"
    kill_port_forward

    if kind_cluster_exists; then
        log_info "kind delete cluster --name ${KIND_CLUSTER_NAME}"
        if ! kind delete cluster --name "${KIND_CLUSTER_NAME}"; then
            log_fail "kind delete cluster failed"
            return 1
        fi
    else
        log_info "kind cluster '${KIND_CLUSTER_NAME}' not present (already torn down)"
    fi

    log_info "removing password cache ${SHARKO_DEV_PW_CACHE}"
    rm -f "${SHARKO_DEV_PW_CACHE}"

    echo
    log_ok "teardown complete"
    return 0
}

# =====================================================================
# usage / help
# =====================================================================
usage() {
    cat <<EOF
${BOLD}Sharko maintainer dev-loop tool${RESET}

Usage: ./scripts/sharko-dev.sh <subcommand> [flags]

${BOLD}Lifecycle${RESET}
  up            Bring up env from nothing (kind + ArgoCD + Sharko + port-forward)
  install       Install Sharko on existing kind cluster (build, load, helm install)
  rebuild       Rebuild after a code change (existing install required)
  reset         Cleanup helm release + secrets (preserves kind cluster + ArgoCD)
  down          Full teardown (deletes kind cluster)

${BOLD}Credentials${RESET}
  creds         Get current admin password (smart fallback chain)
  login         Login + extract bearer token
  rotate        Rotate admin password (also verifies V124-7 secret rotation)

${BOLD}Operations${RESET}
  smoke         Run smoke tests (auto-extracts creds if missing)
  status        Show current env state (cluster, Sharko, creds, token)

${BOLD}Help${RESET}
  help          this message
  <subcmd> --help    per-subcommand help

${BOLD}Sourcing model${RESET} — avoid \`source\`. Use eval-via-pipe:

  eval "\$(./scripts/sharko-dev.sh login --export)"
  # exports ADMIN_PW and TOKEN into your shell, no set-e leak risk

${BOLD}Configuration${RESET} (env vars; defaults shown)
  KIND_CLUSTER_NAME    ${KIND_CLUSTER_NAME}
  SHARKO_NAMESPACE     ${SHARKO_NAMESPACE}
  SHARKO_LOCAL_PORT    ${SHARKO_LOCAL_PORT}
  IMAGE_TAG            ${IMAGE_TAG}
  SHARKO_DEV_PW_CACHE  ${SHARKO_DEV_PW_CACHE}
EOF
}

# =====================================================================
# Dispatcher
# =====================================================================
main() {
    local cmd="${1:-help}"
    case "$cmd" in
        up|install|rebuild|reset|creds|login|rotate|smoke|status|down)
            shift
            preflight_tools || return 1
            "do_${cmd}" "$@"
            return $?
            ;;
        help|--help|-h|"")
            usage
            return 0
            ;;
        *)
            log_fail "unknown subcommand: $cmd"
            echo >&2
            usage >&2
            return 1
            ;;
    esac
}

main "$@"
