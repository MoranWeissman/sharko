#!/usr/bin/env bash
#
# scripts/smoke.sh — Sharko personal-smoke automation
#
# Codifies docs/site/developer-guide/personal-smoke-runbook.md Track A + Track B
# B.1-B.5 (the mechanical parts). The human-only sections (B.6 ArgoCD UI, B.7
# browser pass) are out of scope and intentionally NOT automated.
#
# USAGE
#   ./scripts/smoke.sh              # zero-setup: auto-fetches a fresh token
#                                   # on every run from ~/.sharko-dev-pw
#   ./scripts/smoke.sh -v           # verbose: show full curl bodies on failure + go test -v
#   ./scripts/smoke.sh -h           # show this help
#
# Auth: smoke.sh always logs in fresh at startup via /api/v1/auth/login.
# Reads admin password from $ADMIN_PW env or ~/.sharko-dev-pw file. If
# neither is set, hints to run `./scripts/sharko-dev.sh ready` (which
# bootstraps the dev env including the password file).
#
# ENV VARS
#   ADMIN_PW          (optional)               — admin password (falls back to ~/.sharko-dev-pw)
#   KIND_CLUSTER_NAME (default: sharko-e2e)    — kind cluster name (informational)
#   SHARKO_NAMESPACE  (default: sharko)         — k8s namespace
#   SHARKO_LOCAL_PORT (default: 8080)           — host port the kubectl port-forward listens on
#
# OUTPUT
#   7 sequential phases, PASS/FAIL per check, exit 0 if all pass else 1.
#   INFO/SKIP rows do NOT cause a non-zero exit.
#

set -u

# ---- arg handling ----
VERBOSE=0
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            sed -n '2,28p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        -v|--verbose)
            VERBOSE=1
            ;;
        *)
            echo "unknown arg: $arg" >&2
            echo "usage: $0 [-v|-h]" >&2
            exit 2
            ;;
    esac
done

# ---- color (TTY-aware) ----
if [ -t 1 ]; then
    RED=$'\033[31m'
    GREEN=$'\033[32m'
    YELLOW=$'\033[33m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    RED=""
    GREEN=""
    YELLOW=""
    BOLD=""
    RESET=""
fi

PASS_MARK="${GREEN}[PASS]${RESET}"
FAIL_MARK="${RED}[FAIL]${RESET}"
INFO_MARK="${YELLOW}[INFO]${RESET}"
SKIP_MARK="${YELLOW}[SKIP]${RESET}"

# ---- defaults ----
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-sharko-e2e}"
SHARKO_NAMESPACE="${SHARKO_NAMESPACE:-sharko}"
SHARKO_LOCAL_PORT="${SHARKO_LOCAL_PORT:-8080}"
HOST="http://localhost:${SHARKO_LOCAL_PORT}"

# ---- counters ----
TOTAL=0
PASSED=0
FAILED=0
START_TIME=$(date +%s)

# ---- result helpers ----
record_pass() {
    TOTAL=$((TOTAL+1))
    PASSED=$((PASSED+1))
    printf "  %s %s\n" "$PASS_MARK" "$1"
}
record_fail() {
    TOTAL=$((TOTAL+1))
    FAILED=$((FAILED+1))
    printf "  %s %s\n" "$FAIL_MARK" "$1"
    if [ -n "${2:-}" ] && [ "$VERBOSE" = "1" ]; then
        printf "         body: %s\n" "$2"
    fi
}
record_info() {
    printf "  %s %s\n" "$INFO_MARK" "$1"
}
record_skip() {
    printf "  %s %s\n" "$SKIP_MARK" "$1"
}

# ---- sharko_login() ----
# Reads a fresh Sharko bearer token by POSTing /api/v1/auth/login.
# Password source: $ADMIN_PW env var → ~/.sharko-dev-pw file → error.
# Echoes the token to stdout; returns non-zero on failure.
sharko_login() {
    local pw
    if [ -n "${ADMIN_PW:-}" ]; then
        pw="$ADMIN_PW"
    elif [ -f "${HOME}/.sharko-dev-pw" ]; then
        pw="$(cat "${HOME}/.sharko-dev-pw")"
    else
        echo "no admin password found — set \$ADMIN_PW or run './scripts/sharko-dev.sh ready' to bootstrap" >&2
        return 1
    fi

    local resp
    resp=$(curl -sS --max-time 10 -X POST "${HOST}/api/v1/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"username\":\"admin\",\"password\":\"${pw}\"}" 2>/dev/null) || {
        echo "login request failed (network?)" >&2
        return 1
    }

    local token
    token=$(printf '%s' "$resp" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["token"])' 2>/dev/null) || {
        echo "login response malformed or auth failed: ${resp}" >&2
        return 1
    }
    [ -n "$token" ] || { echo "login returned empty token" >&2; return 1; }
    printf '%s' "$token"
}

# ---- auth bootstrap (V124-5.6: always refresh) ----
# Smoke is a fresh test run by definition — start from a known-good auth
# state every time. No probing, no inherited shell state. If anyone needs
# to test smoke.sh against a specific pre-minted token, that's a different
# script (this one always uses admin via /api/v1/auth/login).
if ! TOKEN=$(sharko_login); then
    echo "${FAIL_MARK} smoke.sh: could not obtain a Sharko auth token." >&2
    echo "       Set \$ADMIN_PW or run './scripts/sharko-dev.sh ready'" >&2
    echo "       to populate ~/.sharko-dev-pw, then retry." >&2
    exit 1
fi

# ---- header ----
echo "${BOLD}Sharko personal smoke (V124-5.6)${RESET}"
echo "================================="
echo "  cluster:    ${KIND_CLUSTER_NAME}"
echo "  namespace:  ${SHARKO_NAMESPACE}"
echo "  host:       ${HOST}"
echo

# =====================================================================
# Phase 1 — Pre-flight checks
# =====================================================================
echo "${BOLD}[1/5] Pre-flight checks${RESET}"

# kubectl context
if ctx=$(kubectl config current-context 2>/dev/null) && [ -n "$ctx" ]; then
    record_pass "kubectl context: $ctx"
else
    record_fail "kubectl context not set"
fi

# Deployment available
avail=$(kubectl get deployment -n "${SHARKO_NAMESPACE}" sharko \
    -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "0")
if [ -n "$avail" ] && [ "$avail" -ge 1 ] 2>/dev/null; then
    pod=$(kubectl get pod -n "${SHARKO_NAMESPACE}" -l app.kubernetes.io/name=sharko \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "?")
    record_pass "deployment ready: ${pod} (${avail} replica)"
else
    record_fail "deployment/sharko not Available in namespace ${SHARKO_NAMESPACE}"
fi

# Port-forward alive (TCP probe)
if (echo > "/dev/tcp/127.0.0.1/${SHARKO_LOCAL_PORT}") 2>/dev/null; then
    record_pass "port-forward alive: localhost:${SHARKO_LOCAL_PORT}"
else
    record_fail "no listener on localhost:${SHARKO_LOCAL_PORT} (run dev-rebuild.sh)"
fi

# /api/v1/health 200
health_code=$(curl -sS -o /tmp/sharko-smoke-health.json -w '%{http_code}' \
    --max-time 5 "${HOST}/api/v1/health" 2>/dev/null || echo "000")
if [ "$health_code" = "200" ]; then
    record_pass "/api/v1/health: 200"
else
    record_fail "/api/v1/health returned ${health_code}" "$(cat /tmp/sharko-smoke-health.json 2>/dev/null || true)"
fi
echo

# =====================================================================
# Phase 2 — CLI sweep (Track A.6 codified)
# =====================================================================
echo "${BOLD}[2/5] CLI sweep${RESET}"

# Get the cobra-registered subcommand list. Use kubectl exec on the deployment;
# kubectl resolves to a live pod automatically. The Sharko image installs the
# binary at /usr/local/bin/sharko (resolvable as `sharko` via $PATH).
sub_help=$(kubectl exec -n "${SHARKO_NAMESPACE}" deployment/sharko -- sharko --help 2>/dev/null || true)
if [ -z "$sub_help" ]; then
    record_fail "could not exec sharko --help in pod"
    SUBCOMMANDS=""
else
    # Parse "Available Commands:" section. Each line is "  name   short-desc".
    # Skip "completion" (cobra-injected, --help works but it's noisy and not
    # interesting). Skip "help" (built-in alias for --help).
    SUBCOMMANDS=$(printf '%s\n' "$sub_help" \
        | awk '/^Available Commands:/{flag=1;next} /^Flags:/{flag=0} flag && /^[[:space:]]+[a-z]/ {print $1}' \
        | grep -vE '^(completion|help)$' \
        | sort -u)
fi

if [ -n "$SUBCOMMANDS" ]; then
    # shellcheck disable=SC2086
    n_cmds=$(printf '%s\n' $SUBCOMMANDS | wc -l | tr -d ' ')
    record_info "discovered ${n_cmds} subcommands"
    for cmd in $SUBCOMMANDS; do
        out=$(kubectl exec -n "${SHARKO_NAMESPACE}" deployment/sharko -- sharko "$cmd" --help 2>&1 || true)
        # Pass if non-empty and contains "Usage:" (cobra help marker).
        if printf '%s' "$out" | grep -q "Usage:"; then
            record_pass "$cmd --help"
        else
            record_fail "$cmd --help (no Usage: marker)" "$(printf '%s' "$out" | head -3 | tr '\n' ' ')"
        fi
    done
fi
echo

# =====================================================================
# Phase 3 — API sweep (read endpoints — runbook B.5)
# =====================================================================
echo "${BOLD}[3/5] API sweep (read endpoints)${RESET}"

# Endpoint list — derived from internal/api/router.go GET /api/v1/* registrations.
# These endpoints MUST return 200 + JSON regardless of Git-connection state.
# The runbook B.5 verifies exactly this set on a fresh kind cluster (no Git
# connection, no init), so they are the safe "always 200" floor.
#
# Endpoints intentionally NOT in this list because they are 503 by design on a
# fresh cluster (require an active Git provider): /dashboard/stats,
# /dashboard/attention, /dashboard/pull-requests, /clusters, /prs.
#
# Format: "PATH|TOP_LEVEL_KEY"  — TOP_LEVEL_KEY is one expected key in the
# response JSON when the body is a dict. Use "*" to skip key-presence
# assertion (e.g. when the response is a JSON array).
API_ENDPOINTS=(
    "/api/v1/health|status"
    "/api/v1/config|*"
    "/api/v1/audit|*"
    "/api/v1/repo/status|initialized"
    "/api/v1/users/me|username"
    "/api/v1/fleet/status|*"
    "/api/v1/notifications|*"
    "/api/v1/catalog/addons|addons"
    "/api/v1/catalog/sources|*"
    "/api/v1/providers|*"
)

for entry in "${API_ENDPOINTS[@]}"; do
    path="${entry%|*}"
    expected_key="${entry#*|}"
    body_file=$(mktemp)
    code=$(curl -sS -o "$body_file" -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer ${TOKEN}" \
        "${HOST}${path}" 2>/dev/null || echo "000")

    if [ "$code" != "200" ]; then
        record_fail "GET ${path} → ${code}" "$(head -c 200 "$body_file" 2>/dev/null || true)"
        rm -f "$body_file"
        continue
    fi

    # Validate JSON
    if ! python3 -c "import sys,json; json.load(open('$body_file'))" >/dev/null 2>&1; then
        record_fail "GET ${path} → 200 but not JSON" "$(head -c 200 "$body_file" 2>/dev/null || true)"
        rm -f "$body_file"
        continue
    fi

    # Validate top-level key if specified
    if [ "$expected_key" != "*" ]; then
        has_key=$(python3 -c "import sys,json
try:
    d=json.load(open('$body_file'))
    if isinstance(d, dict):
        print('1' if '$expected_key' in d else '0')
    else:
        print('0')
except Exception:
    print('0')" 2>/dev/null || echo "0")
        if [ "$has_key" != "1" ]; then
            record_fail "GET ${path} → 200 JSON but missing key '${expected_key}'" "$(head -c 200 "$body_file" 2>/dev/null || true)"
            rm -f "$body_file"
            continue
        fi
    fi

    record_pass "GET ${path} → 200 JSON"
    rm -f "$body_file"
done
echo

# =====================================================================
# Phase 4 — V124-4 regression pins (write endpoints with empty bodies)
# =====================================================================
echo "${BOLD}[4/5] V124-4 regression pins${RESET}"

# These four pins guard the V124-4 fixes against future regression. Each one
# has a BUG ID in the message for traceability back to the runbook + commits.
#
# Note the two distinct error codes at play:
#   * 400 Bad Request — handler-level required-field validation
#                       (V124-4.2/3/5 fixes; previously 502 or silent accept).
#   * 503 Service Unavailable — provider not configured at runtime
#                               (V124-4.1 fix; previously 501 Not Implemented).
#   * 404 endpoint_not_found  — unknown /api/v1/* path
#                               (V124-4.4 fix; previously 200 SPA HTML fall-through).

post_check() {
    local label="$1"
    local path="$2"
    local body="$3"
    local want_codes="$4"   # space-separated list of acceptable HTTP codes
    local want_substr="${5:-}"  # optional: a substring expected somewhere in the response body

    local body_file
    body_file=$(mktemp)
    local code
    code=$(curl -sS -o "$body_file" -w '%{http_code}' --max-time 10 \
        -X POST -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${TOKEN}" \
        -d "$body" \
        "${HOST}${path}" 2>/dev/null || echo "000")

    local code_ok=0
    for c in $want_codes; do
        if [ "$code" = "$c" ]; then
            code_ok=1
            break
        fi
    done

    if [ "$code_ok" = "0" ]; then
        record_fail "${label} → got ${code}, want one of [${want_codes}]" "$(head -c 200 "$body_file" 2>/dev/null || true)"
        rm -f "$body_file"
        return 1
    fi

    if [ -n "$want_substr" ]; then
        if ! grep -q "$want_substr" "$body_file" 2>/dev/null; then
            record_fail "${label} → ${code} but body missing '${want_substr}'" "$(head -c 200 "$body_file" 2>/dev/null || true)"
            rm -f "$body_file"
            return 1
        fi
    fi

    record_pass "${label} → ${code}"
    rm -f "$body_file"
    return 0
}

# BUG-017 (V124-4.2) — POST /api/v1/connections/ with {} must 400 not silently accept
post_check "BUG-017 POST /connections/ {}" \
    "/api/v1/connections/" \
    '{}' \
    "400"

# BUG-018 (V124-4.1) — POST /api/v1/clusters when no provider configured must
# 503 (was 501 pre-fix). On a fresh kind cluster with no Git connection there
# is no provider, so 503 is the expected "fixed" state. If the maintainer has
# configured a Git connection + provider, this could legitimately succeed (201)
# or hit a different validation error (400). Only fail loudly on the regression
# (501 Not Implemented).
body_file=$(mktemp)
code=$(curl -sS -o "$body_file" -w '%{http_code}' --max-time 10 \
    -X POST -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${TOKEN}" \
    -d '{"name":"smoke-pin","server":"https://example.invalid"}' \
    "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")
case "$code" in
    503)
        record_pass "BUG-018 POST /clusters {name+server} → 503 (provider not configured, V124-4.1 fix in effect)"
        ;;
    501)
        record_fail "BUG-018 POST /clusters {name+server} → 501 (V124-4.1 REGRESSED — should be 503)" \
            "$(head -c 200 "$body_file" 2>/dev/null || true)"
        ;;
    400|409|201|200)
        record_info "BUG-018 POST /clusters → ${code} (provider IS configured — pin only meaningful pre-Git-connection)"
        TOTAL=$((TOTAL+1)); PASSED=$((PASSED+1))
        ;;
    *)
        record_fail "BUG-018 POST /clusters → unexpected ${code}" \
            "$(head -c 200 "$body_file" 2>/dev/null || true)"
        ;;
esac
rm -f "$body_file"

# BUG-019 (V124-4.3) — POST /api/v1/addons with {} must 400 (was 502 pre-fix)
post_check "BUG-019 POST /addons {}" \
    "/api/v1/addons" \
    '{}' \
    "400"

# BUG-020 (V124-4.4) — POST /api/v1/notifications/providers with {} must 404
# (the route does NOT exist — pre-fix it returned 200 SPA HTML). The fix is the
# /api/v1/ catch-all that returns structured 404 JSON with code=endpoint_not_found.
post_check "BUG-020 POST /notifications/providers {} (unknown path)" \
    "/api/v1/notifications/providers" \
    '{}' \
    "404" \
    "endpoint_not_found"

echo

# =====================================================================
# Phase 5 — Go E2E suite (B.6)
# =====================================================================
echo "${BOLD}[5/5] Go E2E suite${RESET}"

# Locate the repo root by walking up from this script. The Go test must be
# invoked from the module root (or anywhere inside the module — `go test
# ./tests/e2e/...` works from the root).
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "${script_dir}/.." && pwd)

if [ ! -d "${repo_root}/tests/e2e" ]; then
    record_fail "tests/e2e directory not found under ${repo_root}"
elif ! command -v go >/dev/null 2>&1; then
    record_skip "Go E2E suite (go not in PATH)"
else
    go_log=$(mktemp)
    pushd "$repo_root" >/dev/null
    export SHARKO_E2E_URL="${HOST}"
    export SHARKO_E2E_USERNAME=admin
    export SHARKO_E2E_PASSWORD="${ADMIN_PW}"
    if [ "$VERBOSE" = "1" ]; then
        go test -tags e2e ./tests/e2e/... -v -timeout 5m 2>&1 | tee "$go_log"
        e2e_rc=${PIPESTATUS[0]}
    else
        go test -tags e2e ./tests/e2e/... -timeout 5m >"$go_log" 2>&1
        e2e_rc=$?
    fi
    popd >/dev/null

    if [ "$e2e_rc" = "0" ]; then
        # Pull the summary line ("ok  pkg  Ns") for the report.
        summary=$(grep -E '^(ok|PASS)' "$go_log" | tail -1)
        record_pass "tests/e2e: PASS (${summary})"
    else
        record_fail "tests/e2e: FAIL (exit ${e2e_rc})"
        if [ "$VERBOSE" != "1" ]; then
            echo "         tail of go test output:"
            tail -20 "$go_log" | sed 's/^/         /'
        fi
    fi
    rm -f "$go_log"
fi

echo

# =====================================================================
# Phase 6 — Orphan-cascade E2E (V124-5.3 / V125-1-7.x regression pin)
# =====================================================================
# Exercises the full orphan-delete recovery surface introduced in V125-1-7:
#   register (kubeconfig) → close PR → wait for orphan surface → DELETE orphan → assert clean
#
# PRE-CONDITIONS (all required; any failure → SKIP group, not FAIL):
#   1. A kind target cluster is reachable via kubectl --context kind-<TARGET>
#   2. gh CLI is installed + authenticated
#   3. Sharko server is already reachable (Phase 1 gated this)
#
# IDEMPOTENCY: cluster name includes a short timestamp suffix so repeated
# runs in the same session cannot collide.
#
# Override the target cluster name with ORPHAN_TARGET_CLUSTER env var.
# The default matches what `sharko-dev.sh kind-target create 1` produces.
echo "${BOLD}[6/7] Orphan-cascade E2E (V125-1-7.x)${RESET}"

ORPHAN_TARGET_CLUSTER="${ORPHAN_TARGET_CLUSTER:-sharko-target-1}"
ORPHAN_CTX="kind-${ORPHAN_TARGET_CLUSTER}"
ORPHAN_SKIP=0  # 0 = run, 1 = skip/abort without failing

# ---- Pre-flight: target cluster reachable? ----
if ! kubectl --context "${ORPHAN_CTX}" get nodes >/dev/null 2>&1; then
    record_skip "orphan-cascade: kind target '${ORPHAN_TARGET_CLUSTER}' not reachable — run: ./scripts/sharko-dev.sh kind-target create 1"
    ORPHAN_SKIP=1
fi

# ---- Pre-flight: gh CLI available + authenticated? ----
if [ "$ORPHAN_SKIP" = "0" ] && ! command -v gh >/dev/null 2>&1; then
    record_skip "orphan-cascade: gh CLI not installed — cannot close PR (install: https://cli.github.com)"
    ORPHAN_SKIP=1
fi
if [ "$ORPHAN_SKIP" = "0" ] && ! gh auth status >/dev/null 2>&1; then
    record_skip "orphan-cascade: gh CLI not authenticated — run: gh auth login"
    ORPHAN_SKIP=1
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    record_pass "orphan-cascade pre-flight: target '${ORPHAN_TARGET_CLUSTER}' reachable + gh authenticated"

    # ---- Step 1: Create SA + ClusterRoleBinding (idempotent) ----
    kubectl --context "${ORPHAN_CTX}" create serviceaccount sharko-smoke-register -n default 2>/dev/null || true
    kubectl --context "${ORPHAN_CTX}" create clusterrolebinding sharko-smoke-register-admin \
        --clusterrole=cluster-admin \
        --serviceaccount=default:sharko-smoke-register 2>/dev/null || true

    # ---- Step 2: Generate 1h bearer token ----
    OC_TOKEN=$(kubectl --context "${ORPHAN_CTX}" -n default create token sharko-smoke-register --duration=1h 2>/dev/null || true)
    if [ -z "$OC_TOKEN" ]; then
        record_fail "orphan-cascade: failed to generate bearer token for ${ORPHAN_TARGET_CLUSTER}"
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # Derive the Docker-network-internal server URL so in-cluster sharko + argocd
    # can reach the target over the kind bridge (127.0.0.1:<port> is host-only).
    OC_SERVER_IP=$(docker inspect "${ORPHAN_TARGET_CLUSTER}-control-plane" \
        --format '{{ .NetworkSettings.Networks.kind.IPAddress }}' 2>/dev/null || true)
    if [ -z "$OC_SERVER_IP" ]; then
        record_fail "orphan-cascade: could not get Docker-network IP for ${ORPHAN_TARGET_CLUSTER}-control-plane"
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    OC_SERVER="https://${OC_SERVER_IP}:6443"
    OC_CA=$(kubectl --context "${ORPHAN_CTX}" config view --raw --minify \
        -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' 2>/dev/null || true)
    if [ -z "$OC_CA" ]; then
        record_fail "orphan-cascade: could not extract certificate-authority-data for ${ORPHAN_TARGET_CLUSTER}"
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # Build the kubeconfig YAML (bearer-token auth — kubeconfig provider v1.25+).
    OC_KUBECONFIG=$(cat <<KUBECONFIG_EOF
apiVersion: v1
kind: Config
current-context: smoke-orphan
clusters:
- name: smoke-orphan
  cluster:
    server: ${OC_SERVER}
    certificate-authority-data: ${OC_CA}
contexts:
- name: smoke-orphan
  context:
    cluster: smoke-orphan
    user: smoke-orphan
users:
- name: smoke-orphan
  user:
    token: ${OC_TOKEN}
KUBECONFIG_EOF
)
    record_pass "orphan-cascade: kubeconfig assembled (server=${OC_SERVER})"

    # ---- Step 3: POST /api/v1/clusters — register in manual-merge mode ----
    # Name uniqueness: seconds-since-epoch tail keeps names short and sortable.
    ORPHAN_TS=$(date +%s | tail -c 7)
    ORPHAN_NAME="smoke-orphan-${ORPHAN_TS}"

    # Build JSON payload safely via python3 to handle YAML escaping.
    reg_body_file=$(mktemp)
    python3 -c "
import json, sys
kc = sys.stdin.read()
payload = {
    'name': '${ORPHAN_NAME}',
    'provider': 'kubeconfig',
    'kubeconfig': kc,
    'addons': {}
}
print(json.dumps(payload))
" <<< "$OC_KUBECONFIG" > "$reg_body_file" 2>/dev/null

    reg_resp_file=$(mktemp)
    reg_code=$(curl -sS -o "$reg_resp_file" -w '%{http_code}' --max-time 30 \
        -X POST -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${TOKEN}" \
        -d "@${reg_body_file}" \
        "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")
    rm -f "$reg_body_file"

    if [ "$reg_code" = "201" ]; then
        record_pass "orphan-cascade: POST /clusters → 201 (name=${ORPHAN_NAME})"
    else
        record_fail "orphan-cascade: POST /clusters → ${reg_code} (expected 201)" \
            "$(head -c 300 "$reg_resp_file" 2>/dev/null || true)"
        rm -f "$reg_resp_file"
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # ---- Step 4: Extract PR number + URL from registration response ----
    ORPHAN_PR_ID=$(python3 -c "
import json, sys
try:
    d = json.load(open('${reg_resp_file}'))
    print(d.get('git', {}).get('pr_id', ''))
except Exception:
    print('')
" 2>/dev/null || echo "")

    ORPHAN_PR_URL=$(python3 -c "
import json, sys
try:
    d = json.load(open('${reg_resp_file}'))
    print(d.get('git', {}).get('pr_url', ''))
except Exception:
    print('')
" 2>/dev/null || echo "")
    rm -f "$reg_resp_file"

    if [ -z "$ORPHAN_PR_ID" ] || [ "$ORPHAN_PR_ID" = "0" ]; then
        record_fail "orphan-cascade: registration response missing git.pr_id (got: '${ORPHAN_PR_ID}') — is Sharko configured for manual-merge mode?"
        ORPHAN_SKIP=1
    else
        record_pass "orphan-cascade: PR created (pr_id=${ORPHAN_PR_ID} url=${ORPHAN_PR_URL})"
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # ---- Step 5: Close the PR via gh CLI — triggers the orphan path ----
    # Derive OWNER/REPO from the PR URL: https://github.com/OWNER/REPO/pull/NNN
    ORPHAN_REPO=$(python3 -c "
import re
url = '${ORPHAN_PR_URL}'
m = re.search(r'github\.com/([^/]+/[^/]+)/pull/', url)
print(m.group(1) if m else '')
" 2>/dev/null || echo "")

    if [ -z "$ORPHAN_REPO" ]; then
        record_fail "orphan-cascade: cannot derive OWNER/REPO from PR URL '${ORPHAN_PR_URL}' — is this a GitHub connection?"
        ORPHAN_SKIP=1
    elif gh pr close "${ORPHAN_PR_ID}" \
            --repo "${ORPHAN_REPO}" \
            --comment "smoke.sh orphan-cascade test (V124-5.6) — closing to trigger orphan detection" \
            >/dev/null 2>&1; then
        record_pass "orphan-cascade: PR #${ORPHAN_PR_ID} closed on ${ORPHAN_REPO}"
    else
        record_fail "orphan-cascade: gh pr close #${ORPHAN_PR_ID} on ${ORPHAN_REPO} failed"
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # ---- Step 6: Poll /clusters until cluster surfaces in orphan_registrations ----
    # pending-PR poller default interval is 30s; poll for up to 90s (covers worst-case
    # interval + processing lag).
    record_info "orphan-cascade: polling for ${ORPHAN_NAME} in orphan_registrations (up to 90s)..."

    ORPHAN_POLL_MAX=90
    ORPHAN_POLL_INTERVAL=5
    ORPHAN_POLL_ELAPSED=0
    ORPHAN_DETECTED=0

    while [ "$ORPHAN_POLL_ELAPSED" -lt "$ORPHAN_POLL_MAX" ]; do
        poll_file=$(mktemp)
        poll_code=$(curl -sS -o "$poll_file" -w '%{http_code}' --max-time 10 \
            -H "Authorization: Bearer ${TOKEN}" \
            "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")

        if [ "$poll_code" = "200" ]; then
            in_orphan=$(python3 -c "
import json
try:
    d = json.load(open('${poll_file}'))
    names = [o.get('cluster_name','') for o in d.get('orphan_registrations', [])]
    print('1' if '${ORPHAN_NAME}' in names else '0')
except Exception:
    print('0')
" 2>/dev/null || echo "0")
            rm -f "$poll_file"
            if [ "$in_orphan" = "1" ]; then
                ORPHAN_DETECTED=1
                break
            fi
        else
            rm -f "$poll_file"
        fi

        sleep "${ORPHAN_POLL_INTERVAL}"
        ORPHAN_POLL_ELAPSED=$((ORPHAN_POLL_ELAPSED + ORPHAN_POLL_INTERVAL))
    done

    if [ "$ORPHAN_DETECTED" = "1" ]; then
        record_pass "orphan-cascade: ${ORPHAN_NAME} surfaced in orphan_registrations (${ORPHAN_POLL_ELAPSED}s)"
    else
        record_fail "orphan-cascade: ${ORPHAN_NAME} did NOT appear in orphan_registrations within ${ORPHAN_POLL_MAX}s"
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # ---- Step 7: Assert cluster placement (orphan only; not in pending or managed) ----
    assert_file=$(mktemp)
    assert_code=$(curl -sS -o "$assert_file" -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer ${TOKEN}" \
        "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")

    if [ "$assert_code" = "200" ]; then
        not_in_pending=$(python3 -c "
import json
try:
    d = json.load(open('${assert_file}'))
    names = [p.get('cluster_name','') for p in d.get('pending_registrations', [])]
    print('0' if '${ORPHAN_NAME}' in names else '1')
except Exception:
    print('0')
" 2>/dev/null || echo "0")

        not_in_managed=$(python3 -c "
import json
try:
    d = json.load(open('${assert_file}'))
    names = [c.get('name','') for c in d.get('clusters', [])]
    print('0' if '${ORPHAN_NAME}' in names else '1')
except Exception:
    print('0')
" 2>/dev/null || echo "0")
        rm -f "$assert_file"

        if [ "$not_in_pending" = "1" ]; then
            record_pass "orphan-cascade: ${ORPHAN_NAME} NOT in pending_registrations"
        else
            record_fail "orphan-cascade: ${ORPHAN_NAME} still in pending_registrations (should have moved to orphan)"
        fi
        if [ "$not_in_managed" = "1" ]; then
            record_pass "orphan-cascade: ${ORPHAN_NAME} NOT in managed clusters"
        else
            record_fail "orphan-cascade: ${ORPHAN_NAME} unexpectedly in managed clusters"
        fi
    else
        rm -f "$assert_file"
        record_fail "orphan-cascade: GET /clusters → ${assert_code} during placement assert"
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # ---- Step 8: DELETE /api/v1/clusters/{name}/orphan — V125-1-7.x regression pin ----
    # V125-1-7.1 fixed 500 in the handler; V125-1-7.2 fixed 500 from missing
    # Content-Type on ArgoCD calls; V125-1-7.3 fixed 404 from unescaped colons
    # in server URL path segments. Any non-204 here is a regression.
    del_file=$(mktemp)
    del_code=$(curl -sS -o "$del_file" -w '%{http_code}' --max-time 15 \
        -X DELETE \
        -H "Authorization: Bearer ${TOKEN}" \
        "${HOST}/api/v1/clusters/${ORPHAN_NAME}/orphan" 2>/dev/null || echo "000")
    del_body=$(head -c 300 "$del_file" 2>/dev/null || true)
    rm -f "$del_file"

    if [ "$del_code" = "204" ]; then
        record_pass "orphan-cascade: DELETE /clusters/${ORPHAN_NAME}/orphan → 204 (V125-1-7.x pin: OK)"
    else
        case "$del_code" in
            500)
                record_fail "orphan-cascade: DELETE → 500 — V125-1-7.x REGRESSED (check op tag in body)" "$del_body" ;;
            404)
                record_fail "orphan-cascade: DELETE → 404 — cluster not found in ArgoCD (URL escape regression? see V125-1-7.3)" "$del_body" ;;
            400)
                record_fail "orphan-cascade: DELETE → 400 — orphan guard rejected (cluster classified as managed or pending?)" "$del_body" ;;
            *)
                record_fail "orphan-cascade: DELETE → ${del_code} (unexpected)" "$del_body" ;;
        esac
        ORPHAN_SKIP=1
    fi
fi

if [ "$ORPHAN_SKIP" = "0" ]; then
    # ---- Step 9: Assert orphan_registrations no longer contains this cluster ----
    ca_file=$(mktemp)
    ca_code=$(curl -sS -o "$ca_file" -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer ${TOKEN}" \
        "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")

    if [ "$ca_code" = "200" ]; then
        still_orphan=$(python3 -c "
import json
try:
    d = json.load(open('${ca_file}'))
    names = [o.get('cluster_name','') for o in d.get('orphan_registrations', [])]
    print('1' if '${ORPHAN_NAME}' in names else '0')
except Exception:
    print('1')
" 2>/dev/null || echo "1")
        rm -f "$ca_file"

        if [ "$still_orphan" = "0" ]; then
            record_pass "orphan-cascade: ${ORPHAN_NAME} removed from orphan_registrations — full cascade verified"
        else
            record_fail "orphan-cascade: ${ORPHAN_NAME} still in orphan_registrations after DELETE — ArgoCD delete may have failed silently"
        fi
    else
        rm -f "$ca_file"
        record_fail "orphan-cascade: GET /clusters → ${ca_code} during post-delete assert"
    fi
fi

# ---- Step 10: Cleanup SA + CRB (best-effort — never affect PASS/FAIL count) ----
if kubectl --context "${ORPHAN_CTX}" get nodes >/dev/null 2>&1; then
    kubectl --context "${ORPHAN_CTX}" delete clusterrolebinding sharko-smoke-register-admin 2>/dev/null || true
    kubectl --context "${ORPHAN_CTX}" delete serviceaccount sharko-smoke-register -n default 2>/dev/null || true
fi

echo

# =====================================================================
# Phase 7 — Full Cluster Lifecycle E2E (V124-5.7)
# =====================================================================
# Exercises the full managed-cluster lifecycle (auto-merge mode contrast
# with Phase 6's manual-mode orphan path):
#   register → wait-for-managed → GET details → POST /test → DELETE → assert clean
#
# PRE-CONDITIONS (all required; any failure → SKIP group, not FAIL):
#   1. A kind target cluster (default sharko-target-2 — separate from
#      Phase 6's sharko-target-1 so both phases can coexist in one run)
#   2. Sharko server reachable (Phase 1 already gates this)
#   3. Sharko server running in auto-merge mode (PRAutoMerge: true).
#      Auto-merge is governed by global gitops config — NOT a per-request
#      flag — so we detect at runtime via git.merged in the registration
#      response and SKIP gracefully if the running server is in manual mode
#      (Phase 6 already covers the manual-mode path).
#
# IDEMPOTENCY: cluster name has a short timestamp suffix.
#
# Override the target cluster name with LIFECYCLE_TARGET_CLUSTER env var.
# To create both phase 6 + phase 7 targets in one shot:
#   ./scripts/sharko-dev.sh kind-target create 2
echo "${BOLD}[7/7] Full cluster lifecycle E2E (V124-5.7)${RESET}"

LIFECYCLE_TARGET_CLUSTER="${LIFECYCLE_TARGET_CLUSTER:-sharko-target-2}"
LIFECYCLE_CTX="kind-${LIFECYCLE_TARGET_CLUSTER}"
LIFECYCLE_SKIP=0  # 0 = run, 1 = skip/abort without failing

# ---- Pre-flight: target cluster reachable? ----
if ! kubectl --context "${LIFECYCLE_CTX}" get nodes >/dev/null 2>&1; then
    record_skip "lifecycle: kind target '${LIFECYCLE_TARGET_CLUSTER}' not reachable — run: ./scripts/sharko-dev.sh kind-target create 2"
    LIFECYCLE_SKIP=1
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    record_pass "lifecycle pre-flight: target '${LIFECYCLE_TARGET_CLUSTER}' reachable"

    # ---- Step 1: Create SA + ClusterRoleBinding (idempotent) ----
    # Distinct SA name from Phase 6 (sharko-smoke-register) so coexistent runs
    # don't tread on each other's RBAC.
    kubectl --context "${LIFECYCLE_CTX}" create serviceaccount sharko-smoke-lifecycle -n default 2>/dev/null || true
    kubectl --context "${LIFECYCLE_CTX}" create clusterrolebinding sharko-smoke-lifecycle-admin \
        --clusterrole=cluster-admin \
        --serviceaccount=default:sharko-smoke-lifecycle 2>/dev/null || true

    # ---- Step 2: Generate 1h bearer token ----
    LC_TOKEN=$(kubectl --context "${LIFECYCLE_CTX}" -n default create token sharko-smoke-lifecycle --duration=1h 2>/dev/null || true)
    if [ -z "$LC_TOKEN" ]; then
        record_fail "lifecycle: failed to generate bearer token for ${LIFECYCLE_TARGET_CLUSTER}"
        LIFECYCLE_SKIP=1
    fi
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # Derive the Docker-network-internal server URL — same reasoning as Phase 6.
    LC_SERVER_IP=$(docker inspect "${LIFECYCLE_TARGET_CLUSTER}-control-plane" \
        --format '{{ .NetworkSettings.Networks.kind.IPAddress }}' 2>/dev/null || true)
    if [ -z "$LC_SERVER_IP" ]; then
        record_fail "lifecycle: could not get Docker-network IP for ${LIFECYCLE_TARGET_CLUSTER}-control-plane"
        LIFECYCLE_SKIP=1
    fi
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    LC_SERVER="https://${LC_SERVER_IP}:6443"
    LC_CA=$(kubectl --context "${LIFECYCLE_CTX}" config view --raw --minify \
        -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' 2>/dev/null || true)
    if [ -z "$LC_CA" ]; then
        record_fail "lifecycle: could not extract certificate-authority-data for ${LIFECYCLE_TARGET_CLUSTER}"
        LIFECYCLE_SKIP=1
    fi
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # Build the kubeconfig YAML (bearer-token auth — kubeconfig provider v1.25+).
    LC_KUBECONFIG=$(cat <<KUBECONFIG_EOF
apiVersion: v1
kind: Config
current-context: smoke-lifecycle
clusters:
- name: smoke-lifecycle
  cluster:
    server: ${LC_SERVER}
    certificate-authority-data: ${LC_CA}
contexts:
- name: smoke-lifecycle
  context:
    cluster: smoke-lifecycle
    user: smoke-lifecycle
users:
- name: smoke-lifecycle
  user:
    token: ${LC_TOKEN}
KUBECONFIG_EOF
)
    record_pass "lifecycle: kubeconfig assembled (server=${LC_SERVER})"

    # ---- Step 3: POST /api/v1/clusters — register ----
    # Auto-merge mode is server-config (not per-request), so we register and
    # then inspect git.merged in the response to decide whether to continue
    # the lifecycle assertions.
    LC_TS=$(date +%s | tail -c 7)
    LIFECYCLE_NAME="smoke-lifecycle-${LC_TS}"

    reg_body_file=$(mktemp)
    python3 -c "
import json, sys
kc = sys.stdin.read()
payload = {
    'name': '${LIFECYCLE_NAME}',
    'provider': 'kubeconfig',
    'kubeconfig': kc,
    'addons': {}
}
print(json.dumps(payload))
" <<< "$LC_KUBECONFIG" > "$reg_body_file" 2>/dev/null

    reg_resp_file=$(mktemp)
    reg_code=$(curl -sS -o "$reg_resp_file" -w '%{http_code}' --max-time 60 \
        -X POST -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${TOKEN}" \
        -d "@${reg_body_file}" \
        "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")
    rm -f "$reg_body_file"

    case "$reg_code" in
        201|207)
            record_pass "lifecycle: POST /clusters → ${reg_code} (name=${LIFECYCLE_NAME})"
            ;;
        *)
            record_fail "lifecycle: POST /clusters → ${reg_code} (expected 201 or 207)" \
                "$(head -c 300 "$reg_resp_file" 2>/dev/null || true)"
            rm -f "$reg_resp_file"
            LIFECYCLE_SKIP=1
            ;;
    esac
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # ---- Step 4: Detect auto-merge vs manual-merge from response ----
    # The orchestrator returns git.merged=true under auto-merge, leaves a
    # git.pr_id with merged=false under manual-mode. Phase 7 only exercises
    # the auto-merge path (Phase 6 covers manual-mode → orphan).
    LC_MERGED=$(python3 -c "
import json
try:
    d = json.load(open('${reg_resp_file}'))
    g = d.get('git') or {}
    print('1' if g.get('merged') else '0')
except Exception:
    print('0')
" 2>/dev/null || echo "0")
    rm -f "$reg_resp_file"

    if [ "$LC_MERGED" != "1" ]; then
        record_skip "lifecycle: registration PR not auto-merged (git.merged=false) — sharko is in manual-merge mode (PRAutoMerge: false). Phase 6 covers manual-mode flow; Phase 7 requires auto-merge."
        LIFECYCLE_SKIP=1
    else
        record_pass "lifecycle: registration PR auto-merged (git.merged=true)"
    fi
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # ---- Step 5: Poll /clusters until cluster appears with managed=true ----
    # ArgoCD secret reconciler + cluster-addons.yaml load lag — give it ~60s.
    record_info "lifecycle: polling for ${LIFECYCLE_NAME} as managed cluster (up to 60s)..."

    LC_POLL_MAX=60
    LC_POLL_INTERVAL=2
    LC_POLL_ELAPSED=0
    LC_MANAGED=0

    while [ "$LC_POLL_ELAPSED" -lt "$LC_POLL_MAX" ]; do
        poll_file=$(mktemp)
        poll_code=$(curl -sS -o "$poll_file" -w '%{http_code}' --max-time 10 \
            -H "Authorization: Bearer ${TOKEN}" \
            "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")

        if [ "$poll_code" = "200" ]; then
            is_managed=$(python3 -c "
import json
try:
    d = json.load(open('${poll_file}'))
    for c in d.get('clusters', []):
        if c.get('name','') == '${LIFECYCLE_NAME}' and c.get('managed') is True:
            print('1'); break
    else:
        print('0')
except Exception:
    print('0')
" 2>/dev/null || echo "0")
            rm -f "$poll_file"
            if [ "$is_managed" = "1" ]; then
                LC_MANAGED=1
                break
            fi
        else
            rm -f "$poll_file"
        fi

        sleep "${LC_POLL_INTERVAL}"
        LC_POLL_ELAPSED=$((LC_POLL_ELAPSED + LC_POLL_INTERVAL))
    done

    if [ "$LC_MANAGED" = "1" ]; then
        record_pass "lifecycle: ${LIFECYCLE_NAME} surfaced as managed (${LC_POLL_ELAPSED}s)"
    else
        record_fail "lifecycle: ${LIFECYCLE_NAME} did NOT appear as managed within ${LC_POLL_MAX}s — sync/reconciler lag or git-merge gap"
        LIFECYCLE_SKIP=1
    fi
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # ---- Step 6: GET /clusters/{name} — assert detail body shape ----
    detail_file=$(mktemp)
    detail_code=$(curl -sS -o "$detail_file" -w '%{http_code}' --max-time 15 \
        -H "Authorization: Bearer ${TOKEN}" \
        "${HOST}/api/v1/clusters/${LIFECYCLE_NAME}" 2>/dev/null || echo "000")

    if [ "$detail_code" = "200" ]; then
        # ClusterDetailResponse: {cluster: {name, server, server_version, managed, ...}, addons: [...]}
        detail_server=$(python3 -c "
import json
try:
    d = json.load(open('${detail_file}'))
    print((d.get('cluster') or {}).get('server',''))
except Exception:
    print('')
" 2>/dev/null || echo "")
        detail_version=$(python3 -c "
import json
try:
    d = json.load(open('${detail_file}'))
    print((d.get('cluster') or {}).get('server_version',''))
except Exception:
    print('')
" 2>/dev/null || echo "")
        rm -f "$detail_file"

        if [ "$detail_server" = "$LC_SERVER" ]; then
            record_pass "lifecycle: GET /clusters/${LIFECYCLE_NAME} → cluster.server matches (${detail_server})"
        else
            record_fail "lifecycle: GET /clusters/${LIFECYCLE_NAME} → cluster.server='${detail_server}' (expected '${LC_SERVER}')"
        fi
        if [ -n "$detail_version" ]; then
            record_pass "lifecycle: GET /clusters/${LIFECYCLE_NAME} → cluster.server_version present (${detail_version})"
        else
            # ServerVersion is populated from ArgoCD's view of the cluster — empty is
            # a soft signal (timing or ArgoCD not yet synced), not a hard regression.
            record_info "lifecycle: cluster.server_version empty — ArgoCD may not have synced cluster info yet"
        fi
    else
        rm -f "$detail_file"
        record_fail "lifecycle: GET /clusters/${LIFECYCLE_NAME} → ${detail_code} (expected 200)"
        LIFECYCLE_SKIP=1
    fi
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # ---- Step 7: POST /clusters/{name}/test — connectivity probe ----
    # Returns 200 with {reachable: bool, success: bool, server_version, ...} on
    # success, or 503 if no credProvider is configured (kubeconfig-registered
    # clusters in dev-only mode have no AWS-SM bridge → soft-skip on 503).
    test_file=$(mktemp)
    test_code=$(curl -sS -o "$test_file" -w '%{http_code}' --max-time 30 \
        -X POST -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${TOKEN}" \
        "${HOST}/api/v1/clusters/${LIFECYCLE_NAME}/test" 2>/dev/null || echo "000")
    test_body=$(head -c 300 "$test_file" 2>/dev/null || true)

    if [ "$test_code" = "200" ]; then
        reachable=$(python3 -c "
import json
try:
    d = json.load(open('${test_file}'))
    print('1' if d.get('reachable') else '0')
except Exception:
    print('0')
" 2>/dev/null || echo "0")
        rm -f "$test_file"
        if [ "$reachable" = "1" ]; then
            record_pass "lifecycle: POST /clusters/${LIFECYCLE_NAME}/test → 200 reachable=true"
        else
            record_fail "lifecycle: POST /clusters/${LIFECYCLE_NAME}/test → 200 but reachable=false" "$test_body"
        fi
    elif [ "$test_code" = "503" ]; then
        rm -f "$test_file"
        record_skip "lifecycle: POST /clusters/${LIFECYCLE_NAME}/test → 503 (no credentials provider configured — kubeconfig-only dev mode does not bridge to credProvider)"
    else
        rm -f "$test_file"
        record_fail "lifecycle: POST /clusters/${LIFECYCLE_NAME}/test → ${test_code}" "$test_body"
    fi
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # ---- Step 8: DELETE /clusters/{name} — full removal (cleanup=all + yes=true) ----
    # The handler requires {"yes": true} confirmation. Default cleanup=all
    # removes git config + ArgoCD entry + cluster Secret. Expect 200 (or 207
    # for partial — still treated as success: the cluster was deleted, with
    # one or more cleanup substeps degraded).
    del_file=$(mktemp)
    del_code=$(curl -sS -o "$del_file" -w '%{http_code}' --max-time 60 \
        -X DELETE -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${TOKEN}" \
        -d '{"yes":true,"cleanup":"all"}' \
        "${HOST}/api/v1/clusters/${LIFECYCLE_NAME}" 2>/dev/null || echo "000")
    del_body=$(head -c 300 "$del_file" 2>/dev/null || true)
    rm -f "$del_file"

    case "$del_code" in
        200|207)
            record_pass "lifecycle: DELETE /clusters/${LIFECYCLE_NAME} → ${del_code}"
            ;;
        *)
            record_fail "lifecycle: DELETE /clusters/${LIFECYCLE_NAME} → ${del_code} (expected 200 or 207)" "$del_body"
            LIFECYCLE_SKIP=1
            ;;
    esac
fi

if [ "$LIFECYCLE_SKIP" = "0" ]; then
    # ---- Step 9: Poll /clusters until cluster disappears from managed list ----
    # Reconciler/git-merge lag — give it ~30s.
    record_info "lifecycle: polling for ${LIFECYCLE_NAME} removal from managed list (up to 30s)..."

    LC_DEL_MAX=30
    LC_DEL_INTERVAL=2
    LC_DEL_ELAPSED=0
    LC_GONE=0

    while [ "$LC_DEL_ELAPSED" -lt "$LC_DEL_MAX" ]; do
        gone_file=$(mktemp)
        gone_code=$(curl -sS -o "$gone_file" -w '%{http_code}' --max-time 10 \
            -H "Authorization: Bearer ${TOKEN}" \
            "${HOST}/api/v1/clusters" 2>/dev/null || echo "000")

        if [ "$gone_code" = "200" ]; then
            still=$(python3 -c "
import json
try:
    d = json.load(open('${gone_file}'))
    names = [c.get('name','') for c in d.get('clusters', [])]
    print('1' if '${LIFECYCLE_NAME}' in names else '0')
except Exception:
    print('1')
" 2>/dev/null || echo "1")
            rm -f "$gone_file"
            if [ "$still" = "0" ]; then
                LC_GONE=1
                break
            fi
        else
            rm -f "$gone_file"
        fi

        sleep "${LC_DEL_INTERVAL}"
        LC_DEL_ELAPSED=$((LC_DEL_ELAPSED + LC_DEL_INTERVAL))
    done

    if [ "$LC_GONE" = "1" ]; then
        record_pass "lifecycle: ${LIFECYCLE_NAME} removed from managed list (${LC_DEL_ELAPSED}s)"
    else
        record_fail "lifecycle: ${LIFECYCLE_NAME} still in managed list after ${LC_DEL_MAX}s — git-merge or reconciler lag?"
    fi

    # ---- Step 9b: Belt-and-suspenders — assert ArgoCD cluster Secret cleaned up ----
    # The cluster Secret lives in the management cluster's argocd namespace
    # (where sharko + argocd run), NOT in the target cluster. Use the
    # KIND_CLUSTER_NAME-derived context. If the management context isn't
    # configured locally (e.g. CI proxies to sharko via port-forward but not
    # kubectl), this becomes a soft INFO instead of a fail.
    MGMT_CTX="kind-${KIND_CLUSTER_NAME}"
    if kubectl --context "${MGMT_CTX}" get nodes >/dev/null 2>&1; then
        if kubectl --context "${MGMT_CTX}" get secret -n argocd "${LIFECYCLE_NAME}" >/dev/null 2>&1; then
            record_fail "lifecycle: ArgoCD cluster Secret '${LIFECYCLE_NAME}' still exists in argocd namespace on ${MGMT_CTX}"
        else
            record_pass "lifecycle: ArgoCD cluster Secret '${LIFECYCLE_NAME}' removed from argocd namespace on ${MGMT_CTX}"
        fi
    else
        record_info "lifecycle: management context '${MGMT_CTX}' not reachable — skipping ArgoCD Secret cleanup assert"
    fi
fi

# ---- Step 10: Cleanup SA + CRB on the target (best-effort — no PASS/FAIL impact) ----
if kubectl --context "${LIFECYCLE_CTX}" get nodes >/dev/null 2>&1; then
    kubectl --context "${LIFECYCLE_CTX}" delete clusterrolebinding sharko-smoke-lifecycle-admin 2>/dev/null || true
    kubectl --context "${LIFECYCLE_CTX}" delete serviceaccount sharko-smoke-lifecycle -n default 2>/dev/null || true
fi

echo

# ---- summary ----
END_TIME=$(date +%s)
RUNTIME=$((END_TIME - START_TIME))

echo "${BOLD}Summary${RESET}"
echo "  Total checks: ${TOTAL}"
if [ "$FAILED" = "0" ]; then
    echo "  Result:       ${GREEN}PASS${RESET} (${PASSED}/${TOTAL})"
else
    echo "  Result:       ${RED}FAIL${RESET} (${PASSED}/${TOTAL} passed, ${FAILED} failed)"
fi
echo "  Runtime:      ${RUNTIME}s"

if [ "$FAILED" = "0" ]; then
    exit 0
else
    exit 1
fi
