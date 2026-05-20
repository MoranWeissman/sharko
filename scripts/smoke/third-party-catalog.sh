#!/usr/bin/env bash
#
# scripts/smoke/third-party-catalog.sh — V124-1.5 validation script
#
# Asserts that the third-party catalog fetcher (V123-1) wired into a
# RUNNING Sharko instance is healthy for a specific configured source URL.
#
# Companion to docs/site/operator/catalog-sources-smoke.md. The runbook
# walks the steps a human eyeballs; this script encodes the API
# assertions so an operator (or a cron, or a CI job against a long-lived
# test instance) can get a yes/no answer in one invocation.
#
# USAGE
#   SHARKO_URL=http://localhost:8080 \
#   ADMIN_PW=... \
#   SHARKO_THIRDPARTY_URL='https://gist.githubusercontent.com/.../catalog.yaml' \
#     ./scripts/smoke/third-party-catalog.sh
#
# ENV VARS
#   SHARKO_URL              (required)  base URL of the Sharko instance, e.g. http://localhost:8080
#   ADMIN_PW                (required)  admin password used to obtain a bearer token via /api/v1/auth/login
#   SHARKO_THIRDPARTY_URL   (required)  the EXACT third-party URL configured in SHARKO_CATALOG_URLS to assert against
#   SHARKO_EXPECT_VERIFIED  (optional)  "true" to require verified=true (signed source); default "false"
#   SHARKO_FORCE_REFRESH    (optional)  "true" to POST /catalog/sources/refresh before checking; default "false"
#
# EXIT CODES
#   0 — all assertions passed
#   1 — at least one assertion failed
#   2 — usage error (missing env, bad args, prerequisites)
#
# DEPENDENCIES
#   curl, jq
#
# WHAT IT ASSERTS
#   GET /api/v1/catalog/sources returns 200 with a JSON array.
#   The array contains an "embedded" record with status=ok, verified=true.
#   The array contains a record whose url == $SHARKO_THIRDPARTY_URL with:
#     - status == "ok"
#     - entry_count > 0
#     - last_fetched != null AND parses as RFC3339
#     - verified matches $SHARKO_EXPECT_VERIFIED (when SHARKO_EXPECT_VERIFIED=true)
#

set -u

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

PASSED=0
FAILED=0

record_pass() {
    PASSED=$((PASSED + 1))
    printf "  %s %s\n" "$PASS_MARK" "$1"
}
record_fail() {
    FAILED=$((FAILED + 1))
    printf "  %s %s\n" "$FAIL_MARK" "$1"
    if [ -n "${2:-}" ]; then
        printf "         %s\n" "$2"
    fi
}
record_info() {
    printf "  %s %s\n" "$INFO_MARK" "$1"
}

# ---- arg handling ----
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "unknown arg: $arg" >&2
            echo "usage: SHARKO_URL=... ADMIN_PW=... SHARKO_THIRDPARTY_URL=... $0" >&2
            exit 2
            ;;
    esac
done

# ---- prerequisite checks ----
for cmd in curl jq; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "${FAIL_MARK} required command not found in PATH: $cmd" >&2
        exit 2
    fi
done

if [ -z "${SHARKO_URL:-}" ]; then
    echo "${FAIL_MARK} SHARKO_URL is required (e.g. http://localhost:8080)" >&2
    exit 2
fi
if [ -z "${ADMIN_PW:-}" ]; then
    echo "${FAIL_MARK} ADMIN_PW is required" >&2
    exit 2
fi
if [ -z "${SHARKO_THIRDPARTY_URL:-}" ]; then
    echo "${FAIL_MARK} SHARKO_THIRDPARTY_URL is required — the exact URL configured in SHARKO_CATALOG_URLS" >&2
    exit 2
fi

EXPECT_VERIFIED="${SHARKO_EXPECT_VERIFIED:-false}"
FORCE_REFRESH="${SHARKO_FORCE_REFRESH:-false}"

# ---- header ----
echo "${BOLD}Sharko third-party catalog smoke (V124-1.5)${RESET}"
echo "==========================================="
echo "  sharko url:     ${SHARKO_URL}"
echo "  third-party url: ${SHARKO_THIRDPARTY_URL}"
echo "  expect verified: ${EXPECT_VERIFIED}"
echo "  force refresh:   ${FORCE_REFRESH}"
echo

# ---- step 1: login ----
echo "${BOLD}[1/4] Login${RESET}"
login_resp=$(curl -sS --max-time 10 -X POST "${SHARKO_URL}/api/v1/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PW}\"}" 2>/dev/null) || {
    record_fail "POST /api/v1/auth/login failed (network/connectivity)"
    exit 1
}
TOKEN=$(printf '%s' "$login_resp" | jq -r '.token // empty' 2>/dev/null || true)
if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
    record_fail "login returned no token" "response: $(printf '%s' "$login_resp" | head -c 200)"
    exit 1
fi
record_pass "obtained admin bearer token"
echo

# ---- step 2 (optional): force refresh ----
if [ "$FORCE_REFRESH" = "true" ]; then
    echo "${BOLD}[2/4] Force refresh${RESET}"
    refresh_body=$(mktemp)
    refresh_code=$(curl -sS -o "$refresh_body" -w '%{http_code}' --max-time 65 \
        -X POST -H "Authorization: Bearer ${TOKEN}" \
        "${SHARKO_URL}/api/v1/catalog/sources/refresh" 2>/dev/null || echo "000")
    if [ "$refresh_code" = "200" ]; then
        record_pass "POST /api/v1/catalog/sources/refresh → 200"
    elif [ "$refresh_code" = "403" ]; then
        record_fail "POST /api/v1/catalog/sources/refresh → 403 (the logged-in user lacks the catalog.sources.refresh action)"
        rm -f "$refresh_body"
        exit 1
    else
        record_fail "POST /api/v1/catalog/sources/refresh → ${refresh_code}" "$(head -c 200 "$refresh_body")"
        rm -f "$refresh_body"
        exit 1
    fi
    rm -f "$refresh_body"
    echo
else
    echo "${BOLD}[2/4] Force refresh${RESET}"
    record_info "skipped (SHARKO_FORCE_REFRESH != true)"
    echo
fi

# ---- step 3: GET /catalog/sources + validate envelope ----
echo "${BOLD}[3/4] GET /api/v1/catalog/sources${RESET}"
sources_body=$(mktemp)
sources_code=$(curl -sS -o "$sources_body" -w '%{http_code}' --max-time 10 \
    -H "Authorization: Bearer ${TOKEN}" \
    "${SHARKO_URL}/api/v1/catalog/sources" 2>/dev/null || echo "000")

if [ "$sources_code" != "200" ]; then
    record_fail "GET /api/v1/catalog/sources → ${sources_code}" "$(head -c 200 "$sources_body")"
    rm -f "$sources_body"
    exit 1
fi
record_pass "GET /api/v1/catalog/sources → 200"

# JSON well-formed?
if ! jq empty "$sources_body" >/dev/null 2>&1; then
    record_fail "response is not valid JSON" "$(head -c 200 "$sources_body")"
    rm -f "$sources_body"
    exit 1
fi
record_pass "response is valid JSON"

# Top-level array?
if [ "$(jq -r 'type' "$sources_body")" != "array" ]; then
    record_fail "response is not a JSON array (got $(jq -r 'type' "$sources_body"))"
    rm -f "$sources_body"
    exit 1
fi
record_pass "response top-level is an array"

# Embedded record present + ok + verified?
emb_status=$(jq -r '.[] | select(.url == "embedded") | .status' "$sources_body" 2>/dev/null || true)
emb_verified=$(jq -r '.[] | select(.url == "embedded") | .verified' "$sources_body" 2>/dev/null || true)
if [ "$emb_status" = "ok" ] && [ "$emb_verified" = "true" ]; then
    record_pass "embedded source present: status=ok, verified=true"
else
    record_fail "embedded source unexpected state" "status='${emb_status}' verified='${emb_verified}'"
fi
echo

# ---- step 4: third-party assertions ----
echo "${BOLD}[4/4] Third-party source assertions${RESET}"
# Pull the exact record for the configured URL.
tp_record=$(jq --arg url "$SHARKO_THIRDPARTY_URL" '.[] | select(.url == $url)' "$sources_body" 2>/dev/null || true)

if [ -z "$tp_record" ] || [ "$tp_record" = "null" ]; then
    record_fail "no record for url=${SHARKO_THIRDPARTY_URL} — is the env var configured AND is the URL spelled identically?"
    record_info "configured URLs in the response:"
    jq -r '.[] | "    - " + .url' "$sources_body" | sed 's/^/  /'
    rm -f "$sources_body"
    exit 1
fi
record_pass "third-party record found"

# status == ok
tp_status=$(printf '%s' "$tp_record" | jq -r '.status')
if [ "$tp_status" = "ok" ]; then
    record_pass "status: ok"
elif [ "$tp_status" = "stale" ]; then
    record_fail "status: stale (the latest fetch failed; last-known-good entries are being served)" \
        "check Sharko logs for a WARN line with the matching source_fp"
elif [ "$tp_status" = "failed" ]; then
    record_fail "status: failed (no usable snapshot)" \
        "check Sharko logs for a WARN line with the matching source_fp"
else
    record_fail "status: unexpected value '${tp_status}'"
fi

# entry_count > 0
tp_count=$(printf '%s' "$tp_record" | jq -r '.entry_count')
if [ -n "$tp_count" ] && [ "$tp_count" -gt 0 ] 2>/dev/null; then
    record_pass "entry_count: ${tp_count}"
else
    record_fail "entry_count: ${tp_count} (expected > 0)"
fi

# last_fetched != null and parses as RFC3339
tp_last=$(printf '%s' "$tp_record" | jq -r '.last_fetched')
if [ "$tp_last" = "null" ] || [ -z "$tp_last" ]; then
    record_fail "last_fetched: null (the source has never successfully fetched)"
else
    # Lightweight RFC3339 sanity check via jq's fromdate (covers Z and offset forms).
    if printf '%s' "$tp_last" | jq -R 'fromdate' >/dev/null 2>&1; then
        record_pass "last_fetched: ${tp_last}"
    else
        record_fail "last_fetched: '${tp_last}' is not a parseable RFC3339 timestamp"
    fi
fi

# verified
tp_verified=$(printf '%s' "$tp_record" | jq -r '.verified')
if [ "$EXPECT_VERIFIED" = "true" ]; then
    if [ "$tp_verified" = "true" ]; then
        tp_issuer=$(printf '%s' "$tp_record" | jq -r '.issuer // "(none)"')
        record_pass "verified: true (issuer: ${tp_issuer})"
    else
        record_fail "verified: ${tp_verified} (SHARKO_EXPECT_VERIFIED=true requires true)" \
            "is there a .bundle sidecar at <url>.bundle AND is its identity in SHARKO_CATALOG_TRUSTED_IDENTITIES?"
    fi
else
    record_info "verified: ${tp_verified} (SHARKO_EXPECT_VERIFIED!=true, so this is not asserted)"
fi

rm -f "$sources_body"
echo

# ---- summary ----
echo "${BOLD}Summary${RESET}"
TOTAL=$((PASSED + FAILED))
if [ "$FAILED" = "0" ]; then
    echo "  Result: ${GREEN}PASS${RESET} (${PASSED}/${TOTAL})"
    exit 0
else
    echo "  Result: ${RED}FAIL${RESET} (${PASSED}/${TOTAL} passed, ${FAILED} failed)"
    exit 1
fi
