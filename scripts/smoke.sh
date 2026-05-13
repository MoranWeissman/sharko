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
#   5 sequential phases, PASS/FAIL per check, exit 0 if all pass else 1.
#   INFO/SKIP rows do NOT cause a non-zero exit.
#
# SCOPE
#   This script is the lightweight pre-flight (~20s): CLI sweep + read sweep
#   + write-endpoint regression pins + cached Go test invocation. The
#   comprehensive cluster lifecycle / orphan-cascade / multi-cluster
#   scenarios formerly in Phases 6+7 now live in tests/e2e/lifecycle/
#   (see V2 Epic 7-1) and run via `make test-e2e`.
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
