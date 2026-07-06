#!/usr/bin/env bash
#
# scripts/eks-live-test.sh — EKS live-test harness (V2-cleanup-62.1)
#
# One throwaway EKS cluster, one command up, one command down, built so the
# maintainer can prove Sharko's eks-token credential path against a REAL EKS
# cluster instead of only unit-test fakes. Style-matched to
# scripts/sharko-dev.sh (subcommand dispatch, guard style, plain output).
#
# WHAT THIS DOES NOT DO
#   - It never touches the local kind dev environment or ArgoCD running there.
#   - It never merges anything into ~/.kube/config or changes your current
#     kubectl context (V2-cleanup-47 lesson — same rule as sharko-dev.sh).
#   - It never runs unless SHARKO_EKS_TEST_ACCOUNT_ID is set AND matches the
#     live `aws sts get-caller-identity` account. This is the ONLY guard
#     standing between this script and accidentally hitting the wrong AWS
#     account — every subcommand that talks to AWS re-checks it.
#   - No AWS account number is ever hardcoded here. You provide your own via
#     the env var below; nothing about your account is committed anywhere.
#
# USAGE
#   ./scripts/eks-live-test.sh <subcommand> [flags]
#   ./scripts/eks-live-test.sh help
#   ./scripts/eks-live-test.sh <subcommand> --help
#
# SUBCOMMANDS — phase 1 (spoke only; hub on local kind)
#   preflight       tool + account + profile guard checks (no AWS mutation)
#   create          eksctl create cluster (one nodegroup, one small node)
#   role-setup      throwaway IAM role + EKS access entry (assume-role proof)
#   token-check     aws eks get-token + live connectivity check
#   register-help   exact values to paste into Sharko's Register Cluster UI
#   teardown        SPOKE-ONLY teardown (kept as-is; see env-down for both)
#   status          BOTH clusters' state + combined running cost estimate
#
# SUBCOMMANDS — phase 2 / V2-cleanup-62.3 (hub on EKS, zero stored AWS keys)
#   hub-up          EKS hub cluster + ArgoCD + Sharko (repo Helm chart, ghcr
#                   image) + EKS Pod Identity + gitops/aws-sm connection
#   spoke-connect   access entries on the existing spoke + register it in the
#                   HUB Sharko via API (creds_source=eks-token + role_arn)
#   api-smoke       scripted API pass: providers/clusters/podinfo enable/
#                   Synced+Healthy/cluster test/fleet — PASS/FAIL per step
#   env-up          ONE CLICK: preflight -> spoke -> hub-up -> spoke-connect
#                   -> api-smoke -> handover block
#   env-down        FULL teardown of the hub (+ --all: spoke + role + secret)
#
# ENV VARS (override defaults)
#   SHARKO_EKS_TEST_ACCOUNT_ID   REQUIRED. Your AWS account ID. No default —
#                                the script refuses to run without it.
#   SHARKO_GITHUB_TOKEN          REQUIRED by hub-up/env-up. GitHub token for
#                                the gitops repo (never committed anywhere).
#   SHARKO_GITOPS_REPO_URL       REQUIRED by hub-up/env-up. The gitops repo
#                                the hub Sharko manages (https://github.com/...).
#   EKS_TEST_CLUSTER_NAME        default: sharko-eks-live-test   (the spoke)
#   EKS_TEST_HUB_CLUSTER_NAME    default: sharko-eks-live-hub    (the hub)
#   EKS_TEST_REGION              default: eu-west-1
#   EKS_TEST_NODE_TYPE           default: t3.small   (spoke node)
#   EKS_TEST_HUB_NODE_TYPE       default: t3.medium  (hub node — see hub-up --help)
#   EKS_TEST_K8S_VERSION         default: "" (let eksctl pick its current default)
#   EKS_TEST_KUBECONFIG          default: ${TMPDIR:-/tmp}/<cluster-name>.kubeconfig
#   EKS_TEST_HUB_KUBECONFIG      default: ${TMPDIR:-/tmp}/<hub-name>.kubeconfig
#   SHARKO_EKS_TEST_ROLE_NAME    default: sharko-eks-live-test-role
#   SHARKO_EKS_HUB_ROLE_NAME     default: sharko-eks-live-hub-role
#   EKS_TEST_SHARKO_IMAGE_TAG    default: v2.3.0  (ghcr image tag for the hub)
#   EKS_TEST_HUB_SHARKO_PORT     default: 8090  (local port-forward to hub Sharko;
#                                distinct from the kind dev env's 8080)
#   EKS_TEST_HUB_ARGOCD_PORT     default: 18090 (local port-forward to hub ArgoCD)
#   EKS_TEST_PODINFO_VERSION     default: 6.7.1 (api-smoke's catalog entry)
#   AWS_PROFILE                 (standard aws var) refused if it matches a
#                                work-account naming pattern — see preflight.
#

# Deliberately NO `set -e` — same reasoning as sharko-dev.sh: explicit
# per-subcommand error handling, no errexit leak if ever sourced by mistake.

# ---- defaults (overridable via env) ----
CLUSTER_NAME="${EKS_TEST_CLUSTER_NAME:-sharko-eks-live-test}"
REGION="${EKS_TEST_REGION:-eu-west-1}"
NODE_TYPE="${EKS_TEST_NODE_TYPE:-t3.small}"
K8S_VERSION="${EKS_TEST_K8S_VERSION:-}"
KUBECONFIG_PATH="${EKS_TEST_KUBECONFIG:-${TMPDIR:-/tmp}/${CLUSTER_NAME}.kubeconfig}"
ROLE_NAME="${SHARKO_EKS_TEST_ROLE_NAME:-sharko-eks-live-test-role}"
ACCESS_POLICY_ARN="arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
NODEGROUP_NAME="ng-live-test"

# ---- hub defaults (V2-cleanup-62.3) ----
HUB_CLUSTER_NAME="${EKS_TEST_HUB_CLUSTER_NAME:-sharko-eks-live-hub}"
HUB_NODE_TYPE="${EKS_TEST_HUB_NODE_TYPE:-t3.medium}"
HUB_KUBECONFIG_PATH="${EKS_TEST_HUB_KUBECONFIG:-${TMPDIR:-/tmp}/${HUB_CLUSTER_NAME}.kubeconfig}"
HUB_ROLE_NAME="${SHARKO_EKS_HUB_ROLE_NAME:-sharko-eks-live-hub-role}"
HUB_POLICY_NAME="sharko-eks-live-hub-policy"   # inline policy on the hub role
HUB_NODEGROUP_NAME="ng-live-hub"
HUB_NAMESPACE="sharko"
HUB_LOCAL_PORT="${EKS_TEST_HUB_SHARKO_PORT:-8090}"
HUB_ARGOCD_LOCAL_PORT="${EKS_TEST_HUB_ARGOCD_PORT:-18090}"
SHARKO_IMAGE_REPO="${EKS_TEST_SHARKO_IMAGE_REPO:-ghcr.io/moranweissman/sharko}"
SHARKO_IMAGE_TAG="${EKS_TEST_SHARKO_IMAGE_TAG:-v2.3.0}"
PODINFO_VERSION="${EKS_TEST_PODINFO_VERSION:-6.7.1}"
HUB_HOST="http://localhost:${HUB_LOCAL_PORT}"
HUB_CONNECTION_NAME="eks-live-hub"
# ArgoCD's in-cluster service URL — what the hub Sharko connection stores.
HUB_ARGOCD_URL="https://argocd-server.argocd.svc.cluster.local"
# Same pinned install source as scripts/sharko-dev.sh up_argocd_only().
ARGOCD_MANIFEST_URL="https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml"

# Repo root — derived from script location so cwd doesn't matter (the hub
# Sharko is installed from the repo's OWN Helm chart at charts/sharko/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Rough, non-authoritative cost inputs — for the "this spends real money"
# reminder and the `status` estimate only. Check AWS Billing for the truth.
EKS_CONTROL_PLANE_HOURLY="0.10"
NODE_HOURLY_ESTIMATE="0.02"       # t3.small on-demand, eu-west-1, approximate
HUB_NODE_HOURLY_ESTIMATE="0.042"  # t3.medium on-demand, eu-west-1, approximate

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

log_ok()   { printf '%s %s\n' "$OK_MARK"   "$*" >&2; }
log_info() { printf '%s %s\n' "$INFO_MARK" "$*" >&2; }
log_warn() { printf '%s %s\n' "$WARN_MARK" "$*" >&2; }
log_fail() { printf '%s %s\n' "$FAIL_MARK" "$*" >&2; }

# ascii_safe: normalize a string headed for an AWS API VALUE field
# (--description, --secret-string, a --tags value, Key=/Value= tag pairs,
# etc.) down to plain ASCII. AWS validates fields like IAM role
# --description against a strict ASCII/Latin-1 regex — a typographic
# em-dash (—), en-dash (–), or curly quote typed by a human (or pasted
# from an LLM) gets silently REJECTED by the API mid-flow. This exact
# class of bug hit LIVE twice: first in the phase-1 spoke role-setup
# (V2-cleanup-62.1, fixed), then reintroduced in the hub role-setup
# (V2-cleanup-62.3) from an older base. One fix point, used everywhere,
# kills the class instead of patching each recurrence.
#
# Dependency-free: sed + tr only, LC_ALL=C throughout so both operate on
# raw bytes regardless of the caller's locale.
#
# Usage: safe=$(ascii_safe "$raw")
# RULE: every AWS API-bound VALUE string (--description, --secret-string,
# --tags, Key=/Value=) goes through this before being passed to aws/eksctl.
# Log/echo/help text shown to the human is display-only and does NOT need
# to go through this.
ascii_safe() {
    local s="$1"
    # Known typographic sequences -> ASCII equivalents (byte-exact UTF-8
    # matches, done BEFORE the generic strip below so they convert cleanly
    # instead of being deleted).
    s=$(LC_ALL=C printf '%s' "$s" | LC_ALL=C sed \
        -e 's/\xe2\x80\x94/-/g' \
        -e 's/\xe2\x80\x93/-/g' \
        -e "s/\xe2\x80\x98/'/g" \
        -e "s/\xe2\x80\x99/'/g" \
        -e 's/\xe2\x80\x9c/"/g' \
        -e 's/\xe2\x80\x9d/"/g')
    # Anything else non-ASCII: strip outright.
    s=$(LC_ALL=C printf '%s' "$s" | LC_ALL=C tr -d '\200-\377')
    printf '%s' "$s"
}

# confirm_or_abort: prompt for y/N unless --yes is in effect.
# Usage: confirm_or_abort <yes_flag> "<prompt>"
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

# ---- tool preflight ----
preflight_tools() {
    local missing=()
    local t
    for t in eksctl aws kubectl; do
        command -v "$t" >/dev/null 2>&1 || missing+=("$t")
    done
    if [ ${#missing[@]} -gt 0 ]; then
        log_fail "missing required tools: ${missing[*]}"
        echo "       install hints (macOS):" >&2
        for t in "${missing[@]}"; do
            case "$t" in
                eksctl) echo "         brew tap weaveworks/tap && brew install weaveworks/tap/eksctl" >&2 ;;
                aws)    echo "         brew install awscli" >&2 ;;
                kubectl) echo "         brew install kubernetes-cli" >&2 ;;
            esac
        done
        return 1
    fi
    return 0
}

# ---- account / profile guard ----
# The single hard stop that keeps this script from ever hitting a work AWS
# account. Every AWS-mutating subcommand calls this before doing anything.
account_guard() {
    if [ -z "${SHARKO_EKS_TEST_ACCOUNT_ID:-}" ]; then
        log_fail "SHARKO_EKS_TEST_ACCOUNT_ID is not set."
        echo "       This script refuses to run without it — it is the only guard" >&2
        echo "       that stops it from accidentally targeting the wrong AWS account." >&2
        echo "       Set it to YOUR OWN AWS account ID and try again:" >&2
        echo "         export SHARKO_EKS_TEST_ACCOUNT_ID=<your-12-digit-account-id>" >&2
        return 1
    fi

    case "${AWS_PROFILE:-}" in
        feedlot|feedlot-*|sensehub|sensehub-*|swine|swine-*)
            log_fail "AWS_PROFILE='${AWS_PROFILE}' matches a work-account naming pattern."
            echo "       Refusing to run — this harness is for your personal AWS account only." >&2
            echo "       Unset AWS_PROFILE or point it at your personal profile and try again." >&2
            return 1
            ;;
    esac

    local caller_account
    caller_account=$(aws sts get-caller-identity --query 'Account' --output text 2>&1)
    local rc=$?
    if [ $rc -ne 0 ]; then
        log_fail "aws sts get-caller-identity failed:"
        echo "$caller_account" >&2
        return 1
    fi
    if [ "$caller_account" != "$SHARKO_EKS_TEST_ACCOUNT_ID" ]; then
        log_fail "live AWS account (${caller_account}) does not match SHARKO_EKS_TEST_ACCOUNT_ID (${SHARKO_EKS_TEST_ACCOUNT_ID})."
        echo "       Refusing to continue — wrong-account protection." >&2
        return 1
    fi

    log_ok "AWS account verified: ${caller_account} (matches SHARKO_EKS_TEST_ACCOUNT_ID)"
    return 0
}

# cluster_exists: 0 if $CLUSTER_NAME exists in $REGION, 1 otherwise.
cluster_exists() {
    eksctl get cluster --name "$CLUSTER_NAME" --region "$REGION" >/dev/null 2>&1
}

# hub_cluster_exists: 0 if $HUB_CLUSTER_NAME exists in $REGION, 1 otherwise.
hub_cluster_exists() {
    eksctl get cluster --name "$HUB_CLUSTER_NAME" --region "$REGION" >/dev/null 2>&1
}

# hkctl: kubectl against the HUB cluster's own kubeconfig. Never touches
# ~/.kube/config or the current context (V2-cleanup-47 rule).
hkctl() {
    kubectl --kubeconfig="$HUB_KUBECONFIG_PATH" "$@"
}

# hub_preflight_tools: tools the hub lifecycle needs beyond the base set.
# helm    — installs Sharko from the repo's own chart
# python3 — safe JSON building/parsing for the Sharko API calls
# curl    — Sharko API + GHCR reachability probe
# argocd  — mints the ArgoCD API token for the hub connection
hub_preflight_tools() {
    local missing=()
    local t
    for t in eksctl aws kubectl helm python3 curl argocd; do
        command -v "$t" >/dev/null 2>&1 || missing+=("$t")
    done
    if [ ${#missing[@]} -gt 0 ]; then
        log_fail "missing required tools: ${missing[*]}"
        echo "       install hints (macOS):" >&2
        for t in "${missing[@]}"; do
            case "$t" in
                eksctl)  echo "         brew tap weaveworks/tap && brew install weaveworks/tap/eksctl" >&2 ;;
                aws)     echo "         brew install awscli" >&2 ;;
                kubectl) echo "         brew install kubernetes-cli" >&2 ;;
                helm)    echo "         brew install helm" >&2 ;;
                python3) echo "         brew install python3" >&2 ;;
                curl)    echo "         (curl is built-in on macOS / brew install curl)" >&2 ;;
                argocd)  echo "         brew install argocd" >&2 ;;
            esac
        done
        return 1
    fi
    return 0
}

# require_hub_env: hub-up/env-up refuse to start without the two env vars
# the hub Sharko cannot function without. Never auto-extracts anything.
require_hub_env() {
    local ok=0
    if [ -z "${SHARKO_GITOPS_REPO_URL:-}" ]; then
        log_fail "SHARKO_GITOPS_REPO_URL is not set."
        echo "       The hub Sharko needs a gitops repo to manage. Point it at the" >&2
        echo "       SAME repo your local kind Sharko uses (reusing it is fine — the" >&2
        echo "       hub just becomes another consumer), or a dedicated test repo:" >&2
        echo "         export SHARKO_GITOPS_REPO_URL=https://github.com/<owner>/<repo>" >&2
        ok=1
    fi
    if [ -z "${SHARKO_GITHUB_TOKEN:-}" ]; then
        log_fail "SHARKO_GITHUB_TOKEN is not set."
        echo "       The hub Sharko needs a GitHub token with repo scope for the" >&2
        echo "       gitops repo. If your gh CLI is logged in as the right user," >&2
        echo "       this one-liner copies its token (run it yourself — this script" >&2
        echo "       never reads your credentials automatically):" >&2
        echo "         export SHARKO_GITHUB_TOKEN=\"\$(gh auth token)\"" >&2
        echo "       (The local kind Sharko stores its copy AES-256-GCM-encrypted in" >&2
        echo "       the sharko-connections Secret, so there is no kubectl one-liner" >&2
        echo "       to extract it from there — use the gh CLI or your own PAT.)" >&2
        ok=1
    fi
    return $ok
}

# json_build: build a JSON object safely from KEY=VALUE pairs passed via env.
# Usage: payload=$(J_USERNAME=admin J_PASSWORD="$pw" json_build username=J_USERNAME password=J_PASSWORD)
# Values never pass through shell string interpolation into JSON — python
# does the quoting, so tokens with any special characters survive intact.
json_build() {
    python3 -c '
import json, os, sys
out = {}
for arg in sys.argv[1:]:
    key, envvar = arg.split("=", 1)
    out[key] = os.environ.get(envvar, "")
print(json.dumps(out))
' "$@"
}

# json_field <file> <dotted.path>: print a JSON field (or empty on any miss).
# List indices are numeric path segments. Booleans print as the JSON literals
# true/false, and null prints as empty (NOT Python's True/False/None — callers
# compare against plain "true"/"false"/empty, so this must not leak Python
# repr). Numbers print as-is. Dicts/lists print as compact JSON. Routed
# through python3 -c/json.loads (python3 is already a hard dependency of this
# script) instead of a regex/grep parser, so nested dotted paths and
# non-string JSON types (bool/number/null) all resolve correctly instead of
# silently coming back empty.
json_field() {
    python3 - "$1" "$2" <<'PY' 2>/dev/null
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    for k in sys.argv[2].split('.'):
        d = d[int(k)] if isinstance(d, list) else d[k]
    if d is None:
        pass
    elif isinstance(d, bool):
        print('true' if d else 'false')
    elif isinstance(d, (dict, list)):
        print(json.dumps(d))
    else:
        print(d)
except Exception:
    pass
PY
}

# hub_api <method> <path> [json-body]: authenticated call to the hub Sharko.
# Prints the HTTP status code; response body lands in $HUB_API_BODY.
# Requires $HUB_TOKEN (set by hub_sharko_login).
HUB_API_BODY=""

# hub_api_init: create the shared response file IN THE PARENT SHELL. This must
# be called (in the parent, never inside `$( )`) by every entry point before
# its first hub_api call. Why it matters: hub_api is always invoked as
# `code=$(hub_api ...)`, i.e. inside a command-substitution subshell. If the
# response-file path were created lazily inside hub_api, the mktemp would run
# in that throwaway subshell and the parent's HUB_API_BODY would stay empty —
# so every later `json_field "$HUB_API_BODY" ...` in the parent reads nothing
# and every API assertion silently comes back blank (the live-run bug this
# fixes: type/status/creds_source/success all empty in env-up and api-smoke).
hub_api_init() {
    if [ -z "$HUB_API_BODY" ] || [ ! -f "$HUB_API_BODY" ]; then
        HUB_API_BODY="$(mktemp "${TMPDIR:-/tmp}/sharko-hub-api.XXXXXX")"
    fi
}

hub_api() {
    local method="$1"
    local path="$2"
    local body="${3:-}"
    # Safety net only — the normal path is hub_api_init() in the parent shell.
    # If this fires, it fires inside the `code=$(hub_api ...)` subshell and the
    # assignment is lost to the parent; hub_api_init is what keeps it working.
    if [ -z "$HUB_API_BODY" ]; then
        HUB_API_BODY="$(mktemp "${TMPDIR:-/tmp}/sharko-hub-api.XXXXXX")"
    fi
    local -a args=(-sS --max-time 60 -X "$method"
        -H "Authorization: Bearer ${HUB_TOKEN:-}"
        -H "Content-Type: application/json"
        -o "$HUB_API_BODY" -w '%{http_code}')
    if [ -n "$body" ]; then
        args+=(-d "$body")
    fi
    curl "${args[@]}" "${HUB_HOST}${path}" 2>/dev/null || echo "000"
}

# hub_admin_password: read the hub Sharko bootstrap admin password from the
# dedicated sharko-initial-admin-secret (the chart's default writeInitialSecret
# path — mirrors ArgoCD's argocd-initial-admin-secret). Polls up to 60s
# because the pod writes it shortly after first boot.
hub_admin_password() {
    local i pw
    for i in $(seq 1 12); do
        pw=$(hkctl get secret -n "$HUB_NAMESPACE" sharko-initial-admin-secret \
            -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)
        if [ -n "$pw" ]; then
            printf '%s' "$pw"
            return 0
        fi
        [ "$i" -lt 12 ] && sleep 5
    done
    return 1
}

# hub_pf_alive / hub_pf_start: port-forward to the HUB Sharko on
# $HUB_LOCAL_PORT (default 8090 — deliberately NOT 8080, so the local kind
# dev env's port-forward is never disturbed).
hub_pf_alive() {
    curl -sS -o /dev/null --max-time 2 "${HUB_HOST}/api/v1/health" 2>/dev/null
}

hub_pf_start() {
    if hub_pf_alive; then
        return 0
    fi
    pkill -f "port-forward.*svc/sharko ${HUB_LOCAL_PORT}:" 2>/dev/null || true
    sleep 1
    hkctl port-forward -n "$HUB_NAMESPACE" svc/sharko \
        "${HUB_LOCAL_PORT}:80" >/tmp/sharko-hub-pf.log 2>&1 &
    disown 2>/dev/null || true
    local i
    for i in $(seq 1 30); do
        if hub_pf_alive; then
            return 0
        fi
        sleep 1
    done
    log_fail "port-forward to hub Sharko did not come up on localhost:${HUB_LOCAL_PORT}"
    cat /tmp/sharko-hub-pf.log >&2 2>/dev/null || true
    return 1
}

# hub_sharko_login: sets $HUB_TOKEN from POST /api/v1/auth/login on the hub.
HUB_TOKEN=""
hub_sharko_login() {
    local pw
    pw=$(hub_admin_password)
    if [ -z "$pw" ]; then
        log_fail "could not read the hub admin password (secret sharko-initial-admin-secret in namespace ${HUB_NAMESPACE})"
        return 1
    fi
    local payload resp
    payload=$(J_U=admin J_P="$pw" json_build username=J_U password=J_P)
    resp=$(curl -sS --max-time 10 -X POST -H "Content-Type: application/json" \
        -d "$payload" "${HUB_HOST}/api/v1/auth/login" 2>/dev/null || true)
    HUB_TOKEN=$(printf '%s' "$resp" | python3 -c \
        'import json,sys
try: print(json.load(sys.stdin).get("token",""))
except Exception: pass' 2>/dev/null || true)
    if [ -z "$HUB_TOKEN" ]; then
        log_fail "hub Sharko login failed (no token). First 200 chars of response:"
        printf '         %s\n' "$(printf '%s' "$resp" | head -c 200)" >&2
        return 1
    fi
    return 0
}

# spoke_endpoint_and_ca: prints "endpoint<TAB>caData" for the spoke cluster
# straight from aws eks describe-cluster (no kubeconfig required).
spoke_endpoint_and_ca() {
    aws eks describe-cluster --name "$CLUSTER_NAME" --region "$REGION" \
        --query '[cluster.endpoint, cluster.certificateAuthority.data]' \
        --output text 2>/dev/null
}

# ensure_access_entry <cluster> <principal-arn>: idempotent access entry +
# AmazonEKSClusterAdminPolicy association (cluster-wide scope — fine for a
# throwaway test, not for production). Same tolerance pattern as role-setup.
ensure_access_entry() {
    local target_cluster="$1"
    local principal_arn="$2"
    local ae_err ae_rc
    ae_err=$(aws eks create-access-entry \
        --cluster-name "$target_cluster" --region "$REGION" \
        --principal-arn "$principal_arn" --type STANDARD 2>&1)
    ae_rc=$?
    if [ "$ae_rc" -ne 0 ] && ! printf '%s' "$ae_err" | grep -qi "ResourceInUseException\|already exists"; then
        log_fail "aws eks create-access-entry failed (${target_cluster} <- ${principal_arn}):"
        echo "$ae_err" >&2
        return 1
    fi
    local ap_err ap_rc
    ap_err=$(aws eks associate-access-policy \
        --cluster-name "$target_cluster" --region "$REGION" \
        --principal-arn "$principal_arn" \
        --policy-arn "$ACCESS_POLICY_ARN" \
        --access-scope type=cluster 2>&1)
    ap_rc=$?
    if [ "$ap_rc" -ne 0 ] && ! printf '%s' "$ap_err" | grep -qi "already"; then
        log_fail "aws eks associate-access-policy failed (${target_cluster} <- ${principal_arn}):"
        echo "$ap_err" >&2
        return 1
    fi
    return 0
}

# ghcr_image_pullable: 0 if the Sharko image is anonymously pullable from
# GHCR, 1 otherwise (private package / network problem). Uses the registry
# token + manifest endpoints directly — no docker daemon needed.
ghcr_image_pullable() {
    local repo_path="${SHARKO_IMAGE_REPO#ghcr.io/}"
    local tok
    tok=$(curl -fsSL --max-time 15 \
        "https://ghcr.io/token?scope=repository:${repo_path}:pull" 2>/dev/null \
        | python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("token",""))
except Exception: pass' 2>/dev/null || true)
    [ -n "$tok" ] || return 1
    curl -fsSL --max-time 15 -o /dev/null \
        -H "Authorization: Bearer ${tok}" \
        -H "Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json" \
        "https://ghcr.io/v2/${repo_path}/manifests/${SHARKO_IMAGE_TAG}" 2>/dev/null
}

# =====================================================================
# Subcommand: preflight
# =====================================================================
do_preflight() {
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh preflight — guard checks before spending any money

Checks (in order):
  1. eksctl / aws / kubectl present
  2. SHARKO_EKS_TEST_ACCOUNT_ID is set
  3. AWS_PROFILE does not match a work-account naming pattern
  4. Live \`aws sts get-caller-identity\` account matches SHARKO_EKS_TEST_ACCOUNT_ID

Prints a cost estimate and a "this spends real money" reminder on success.
Makes no AWS resource changes.

Usage: ./scripts/eks-live-test.sh preflight [--help]
EOF
                return 0
                ;;
        esac
    done

    log_info "preflight: checking tools"
    preflight_tools || return 1
    log_ok "eksctl / aws / kubectl present"

    log_info "preflight: checking AWS account guard"
    account_guard || return 1

    echo
    log_info "cost estimate: EKS control plane ~\$${EKS_CONTROL_PLANE_HOURLY}/hr + 1x ${NODE_TYPE} node ~\$${NODE_HOURLY_ESTIMATE}/hr"
    log_warn "this spends real money on your AWS account. A half-day test (~4-6 hrs) costs roughly \$1-2 total."
    log_warn "run 'teardown' as soon as you're done — nothing here auto-expires."
    echo
    log_ok "preflight: OK — safe to run 'create'"
    return 0
}

# =====================================================================
# Subcommand: create
# =====================================================================
do_create() {
    local yes=0
    local spot=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh create — create the throwaway EKS test cluster

Creates cluster '${CLUSTER_NAME}' in region '${REGION}': one managed
nodegroup, one ${NODE_TYPE} node, public API endpoint (default), tagged as a
throwaway test cluster. Refuses if the cluster already exists (never creates
a second one). Takes about 15-20 minutes.

Kubeconfig is written ONLY to a temp path (${KUBECONFIG_PATH}) via eksctl's
--kubeconfig flag — it is NEVER merged into ~/.kube/config and your current
kubectl context is left untouched.

Flags:
  --yes     skip the "this costs money" confirmation prompt
  --spot    use a Spot instance for the node (cheaper, can be reclaimed —
            fine for a short test; default is on-demand for reliability)
  --help    this help

Usage: ./scripts/eks-live-test.sh create [--yes] [--spot]
EOF
                return 0
                ;;
            --yes|-y) yes=1 ;;
            --spot) spot=1 ;;
        esac
    done

    account_guard || return 1

    if cluster_exists; then
        log_fail "cluster '${CLUSTER_NAME}' already exists in ${REGION} — refusing to create a second one."
        echo "       Run: $0 status     to inspect it" >&2
        echo "       Run: $0 teardown   to remove it first" >&2
        return 1
    fi

    log_warn "about to create a REAL EKS cluster in AWS account ${SHARKO_EKS_TEST_ACCOUNT_ID}, region ${REGION}."
    log_warn "this takes ~15-20 minutes and costs real money (see: $0 preflight)."
    confirm_or_abort "$yes" "Continue?" || return 1

    local tags="sharko:purpose=live-test,sharko:throwaway=true,sharko:cluster=${CLUSTER_NAME}"
    tags=$(ascii_safe "$tags")

    local -a cmd=(eksctl create cluster
        --name "$CLUSTER_NAME"
        --region "$REGION"
        --nodegroup-name "$NODEGROUP_NAME"
        --nodes 1 --nodes-min 1 --nodes-max 1
        --node-type "$NODE_TYPE"
        --managed
        --kubeconfig "$KUBECONFIG_PATH"
        --tags "$tags"
    )
    if [ -n "$K8S_VERSION" ]; then
        cmd+=(--version "$K8S_VERSION")
    fi
    if [ "$spot" = "1" ]; then
        cmd+=(--spot)
    fi

    log_info "running: ${cmd[*]}"
    if ! "${cmd[@]}"; then
        log_fail "eksctl create cluster failed — see output above."
        echo "       Check the CloudFormation console, or:" >&2
        echo "         aws cloudformation describe-stacks --region ${REGION} | grep -i ${CLUSTER_NAME}" >&2
        echo "       If a partial stack was left behind, run: $0 teardown" >&2
        return 1
    fi
    chmod 600 "$KUBECONFIG_PATH" 2>/dev/null || true

    echo
    log_ok "cluster '${CLUSTER_NAME}' created"
    echo "       Kubeconfig (NOT merged into ~/.kube/config): ${KUBECONFIG_PATH}"
    echo "       Inspect it yourself:  KUBECONFIG=${KUBECONFIG_PATH} kubectl get nodes"
    echo "       Next: $0 token-check          (prove the eks-token path)"
    echo "             $0 role-setup           (optional — assume-role proof)"
    echo "             $0 register-help        (paste values into Sharko's UI)"
    return 0
}

# =====================================================================
# Subcommand: role-setup
# =====================================================================
do_role_setup() {
    local yes=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh role-setup — throwaway IAM role + EKS access entry

Creates a throwaway IAM role ('${ROLE_NAME}') that your own AWS account can
assume, then grants it cluster-admin access on '${CLUSTER_NAME}' via an EKS
access entry. This proves the PRODUCTION-SHAPED scenario: a token minted for
an identity that did NOT create the cluster (exactly Sharko's getEKSToken
assume-role hop — internal/providers/aws_auth.go — and exactly how every
real cross-account setup works). IAM roles are free; this costs nothing
beyond the cluster already running.

Idempotent: reuses the role and access entry if they already exist.

Steps:
  1. aws iam create-role (trust policy: your account root may assume it +
     tag its session — sts:AssumeRole + sts:TagSession, matching what the
     hub's Pod Identity role needs to assume this role for real)
  2. aws eks create-access-entry (registers the role as a cluster principal)
  3. aws eks associate-access-policy (grants AmazonEKSClusterAdminPolicy,
     cluster-wide scope — fine for a throwaway test, not for production)

Prints the role ARN — paste it into:
  ./scripts/eks-live-test.sh token-check --role-arn <arn>
to prove the assume-role hop end-to-end.

Flags:
  --yes    skip confirmation prompt
  --help   this help

Usage: ./scripts/eks-live-test.sh role-setup [--yes]
EOF
                return 0
                ;;
            --yes|-y) yes=1 ;;
        esac
    done

    account_guard || return 1

    if ! cluster_exists; then
        log_fail "cluster '${CLUSTER_NAME}' not found in ${REGION} — run 'create' first."
        return 1
    fi

    confirm_or_abort "$yes" \
        "This creates a throwaway IAM role ('${ROLE_NAME}') + EKS access entry in account ${SHARKO_EKS_TEST_ACCOUNT_ID}. Continue?" \
        || return 1

    local role_arn
    role_arn=$(aws iam get-role --role-name "$ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null || true)

    # Trust policy is always (re-)written below, whether the role already
    # exists or not. A throwaway role from a previous run predates the
    # sts:TagSession fix (V2-cleanup-62.3 live-run fixes) and "reusing" it
    # unchanged would silently keep producing the exact AccessDenied on
    # sts:TagSession this fix exists to close.
    local trust_file
    trust_file="$(mktemp)"
    trap 'rm -f "$trust_file"' RETURN
    cat > "$trust_file" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {"AWS": "arn:aws:iam::${SHARKO_EKS_TEST_ACCOUNT_ID}:root"},
      "Action": ["sts:AssumeRole", "sts:TagSession"]
    }
  ]
}
EOF
    # Trust policy is intentionally broad (whole account root) — this is
    # a throwaway, single-account personal test, not a production role.
    if [ -n "$role_arn" ] && [ "$role_arn" != "None" ]; then
        log_info "role '${ROLE_NAME}' already exists — reusing (refreshing trust policy)"
        if ! aws iam update-assume-role-policy \
            --role-name "$ROLE_NAME" \
            --policy-document "file://${trust_file}" \
            >/dev/null; then
            log_fail "aws iam update-assume-role-policy (${ROLE_NAME}) failed"
            return 1
        fi
    else
        log_info "creating IAM role '${ROLE_NAME}'"
        local role_description
        role_description=$(ascii_safe "Sharko EKS live-test throwaway role (V2-cleanup-62.1) - safe to delete any time")
        if ! aws iam create-role \
            --role-name "$ROLE_NAME" \
            --assume-role-policy-document "file://${trust_file}" \
            --description "$role_description" \
            --tags Key=sharko:purpose,Value=live-test Key=sharko:throwaway,Value=true \
            >/dev/null; then
            log_fail "aws iam create-role failed"
            return 1
        fi
        role_arn=$(aws iam get-role --role-name "$ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null)
        log_ok "IAM role created"
    fi

    if [ -z "$role_arn" ] || [ "$role_arn" = "None" ]; then
        log_fail "could not resolve role ARN for '${ROLE_NAME}'"
        return 1
    fi

    log_info "creating EKS access entry for ${role_arn}"
    local ae_err ae_rc
    ae_err=$(aws eks create-access-entry \
        --cluster-name "$CLUSTER_NAME" --region "$REGION" \
        --principal-arn "$role_arn" --type STANDARD 2>&1)
    ae_rc=$?
    if [ "$ae_rc" -ne 0 ] && ! printf '%s' "$ae_err" | grep -qi "ResourceInUseException\|already exists"; then
        log_fail "aws eks create-access-entry failed:"
        echo "$ae_err" >&2
        return 1
    fi
    log_ok "access entry present"

    log_info "associating AmazonEKSClusterAdminPolicy (cluster-wide scope)"
    local ap_err ap_rc
    ap_err=$(aws eks associate-access-policy \
        --cluster-name "$CLUSTER_NAME" --region "$REGION" \
        --principal-arn "$role_arn" \
        --policy-arn "$ACCESS_POLICY_ARN" \
        --access-scope type=cluster 2>&1)
    ap_rc=$?
    if [ "$ap_rc" -ne 0 ] && ! printf '%s' "$ap_err" | grep -qi "already"; then
        log_fail "aws eks associate-access-policy failed:"
        echo "$ap_err" >&2
        return 1
    fi
    log_ok "access policy associated"

    echo
    log_ok "role-setup complete"
    echo "       Role ARN: ${BOLD}${role_arn}${RESET}"
    echo "       Prove the assume-role hop:"
    echo "         $0 token-check --role-arn ${role_arn}"
    echo "       (teardown deletes this role + access entry automatically)"
    return 0
}

# =====================================================================
# Subcommand: token-check
# =====================================================================
do_token_check() {
    local role_arn=""
    local arg
    while [ $# -gt 0 ]; do
        arg="$1"
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh token-check — prove the eks-token path against the real cluster

Runs exactly what Sharko's eks-token credential path does under the hood
(internal/providers/aws_auth.go getEKSToken): \`aws eks get-token\`, optionally
assuming a role first, then uses the minted token to call the real cluster
API (\`kubectl get nodes\`) — proving the token is not just well-formed but
actually accepted.

Without --role-arn: tests your own (caller) identity — the baseline path.
With --role-arn <arn>: tests the assume-role hop (run './eks-live-test.sh
role-setup' first) — the production-shaped scenario where the identity using
the token did NOT create the cluster.

Flags:
  --role-arn <arn>   assume this role before minting the token
  --help             this help

Usage: ./scripts/eks-live-test.sh token-check [--role-arn <arn>]
EOF
                return 0
                ;;
            --role-arn) shift; role_arn="${1:-}" ;;
        esac
        shift
    done

    account_guard || return 1

    if ! cluster_exists; then
        log_fail "cluster '${CLUSTER_NAME}' not found in ${REGION} — run 'create' first."
        return 1
    fi
    if [ ! -r "$KUBECONFIG_PATH" ]; then
        log_fail "kubeconfig not found at ${KUBECONFIG_PATH} — run 'create' first."
        return 1
    fi

    local -a token_cmd=(aws eks get-token --cluster-name "$CLUSTER_NAME" --region "$REGION")
    if [ -n "$role_arn" ]; then
        log_info "minting token via assumed role: ${role_arn}"
        token_cmd+=(--role-arn "$role_arn")
    else
        log_info "minting token as caller identity (no assumed role)"
    fi

    local raw_token token_rc
    raw_token=$("${token_cmd[@]}" --query 'status.token' --output text 2>&1)
    token_rc=$?
    if [ "$token_rc" -ne 0 ] || [ -z "$raw_token" ] || [ "$raw_token" = "None" ]; then
        log_fail "aws eks get-token failed:"
        echo "$raw_token" >&2
        return 1
    fi
    log_ok "token minted (aws eks get-token succeeded, exactly Sharko's getEKSToken code path)"

    log_info "verifying the token against the live API server"
    local server ca_b64
    server=$(kubectl --kubeconfig="$KUBECONFIG_PATH" config view --minify --raw \
        -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)
    ca_b64=$(kubectl --kubeconfig="$KUBECONFIG_PATH" config view --minify --raw \
        -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' 2>/dev/null)
    if [ -z "$server" ] || [ -z "$ca_b64" ]; then
        log_warn "could not read server/CA from ${KUBECONFIG_PATH} — skipping live connectivity check"
        log_ok "token-check: token mint verified, connectivity NOT verified"
        return 0
    fi

    local ca_file
    ca_file="$(mktemp)"
    trap 'rm -f "$ca_file"' RETURN
    printf '%s' "$ca_b64" | base64 -d > "$ca_file" 2>/dev/null

    if kubectl --server="$server" --certificate-authority="$ca_file" --token="$raw_token" \
        get nodes --request-timeout=10s >/dev/null 2>&1; then
        log_ok "live connectivity verified — the minted token authenticated against the real API server"
        if [ -n "$role_arn" ]; then
            log_ok "assume-role hop proven: an identity that did NOT create this cluster can reach it"
        fi
    else
        log_fail "token was minted but the API server rejected it (or is unreachable)."
        echo "       If you used --role-arn, did you run 'role-setup' first?" >&2
        echo "       (the assumed role needs an EKS access entry to be authorized)" >&2
        return 1
    fi
    return 0
}

# =====================================================================
# Subcommand: register-help
# =====================================================================
do_register_help() {
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh register-help — exact values for Sharko's Register Cluster UI

Prints the fields to paste into the Register Cluster dialog
(ui/src/views/ClustersOverview.tsx), grounded against the actual UI fields
and the backend RegisterClusterRequest struct (internal/orchestrator/types.go).
Makes no AWS or Sharko calls.

Usage: ./scripts/eks-live-test.sh register-help [--help]
EOF
                return 0
                ;;
        esac
    done

    cat <<EOF
${BOLD}Prerequisite — Secrets Provider connection${RESET}
The eks-token credential source is only resolved through a configured
Secrets Provider connection (Sharko Settings -> Secrets Provider), and it
MUST be type ${BOLD}aws-sm${RESET} (AWS Secrets Manager). The k8s-secrets backend does
NOT support eks-token today — verified in code: only
internal/providers/aws_sm.go sniffs the structured-EKS secret JSON and mints
an STS token; internal/providers/k8s_secrets.go only reads a raw
"kubeconfig" data key. If your Secrets Provider is k8s-secrets, switch it to
aws-sm (Region: ${REGION}) before registering this cluster.

${BOLD}Prerequisite — create the AWS Secrets Manager secret${RESET}
Sharko reads this secret's JSON on every credential fetch (field names are
the real Go struct tags, internal/providers/aws_sm.go structuredEKSSecret —
NOTE: docs/site/operator/configuration.md's "Format 2" example uses
different field names (server/ca/cluster_name/role_arn) that do NOT match
this struct (host/caData/clusterName/roleArn) — that doc page has a
pre-existing bug; use the fields below, not that doc, until it's fixed.

EOF
    local server ca_b64
    if [ -r "$KUBECONFIG_PATH" ]; then
        server=$(kubectl --kubeconfig="$KUBECONFIG_PATH" config view --minify --raw \
            -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)
        ca_b64=$(kubectl --kubeconfig="$KUBECONFIG_PATH" config view --minify --raw \
            -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' 2>/dev/null)
    fi
    server="${server:-<run: create first, then re-run register-help>}"
    ca_b64="${ca_b64:-<run: create first, then re-run register-help>}"

    cat <<EOF
  aws secretsmanager create-secret --region ${REGION} \\
    --name "${CLUSTER_NAME}" \\
    --secret-string '{
      "clusterName": "${CLUSTER_NAME}",
      "host": "${server}",
      "caData": "${ca_b64}",
      "region": "${REGION}"
    }'

  (leave out "roleArn" for Leg 1. For Leg 2 — the assume-role proof — run
  'role-setup' first, then update the secret with:
  aws secretsmanager put-secret-value --region ${REGION} --secret-id "${CLUSTER_NAME}" \\
    --secret-string '{ ..., "roleArn": "<arn printed by role-setup>" }'
  Sharko re-reads the secret on every fetch — no re-registration needed.)

${BOLD}Leg 1 — Register Cluster dialog (baseline, no assumed role)${RESET}
  Registration mode:        Direct
  Cluster name:              ${CLUSTER_NAME}
  Credential source:         "Amazon EKS — generate a token from cloud identity"
                              (this sets creds_source=eks-token; Provider is
                              auto-set to "eks" by the UI, nothing to type)
  Region (optional):         ${REGION}
  Role ARN (optional):       leave blank for this leg — there's no role to
                              assume yet; see Leg 2 for how to supply one
  Secret Path (optional):    leave blank (defaults to the cluster name, which
                              matches the secret you just created above)

${BOLD}Leg 2 — prove the assume-role hop${RESET}
  1. Run: $0 role-setup                 (creates the throwaway role + access entry)
  2. Supply the printed role ARN to Sharko either way:
       a) UI: re-register (or edit) the cluster and paste it into the
          Register Cluster dialog's "Role ARN" field. Persisted as role_arn
          on the cluster's managed-clusters.yaml entry and used at token
          mint time.
       b) Secret: put it into the SECRET's "roleArn" field instead (see
          above) — no re-registration needed, Sharko re-reads the secret
          on every fetch.
     Precedence if both are set: the secret's roleArn wins over the
     per-cluster role_arn from the UI, which wins over the connection-level
     provider default.
  3. In Sharko, click "Test cluster" (or trigger any addon deploy) again.
     This now exercises the assume-role hop for real, through Sharko's own
     registration + credential-fetch code.
EOF
    return 0
}

# =====================================================================
# Subcommand: teardown
# =====================================================================
do_teardown() {
    local yes=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh teardown — SPOKE teardown + leftover-billing scan

NOTE: this covers the phase-1 spoke only — it does NOT touch the hub
cluster from 'hub-up'. For the hub (or everything at once) use:
  $0 env-down            # hub only
  $0 env-down --all      # hub + spoke + role + SM secret

Deletes (best-effort, idempotent — safe to re-run):
  1. EKS access entry for the throwaway role (if present)
  2. The cluster itself (eksctl delete cluster --wait)
  3. The throwaway IAM role (if present)
  4. The local kubeconfig temp file

Then scans for anything still billing, tagged sharko:cluster=${CLUSTER_NAME}
or eksctl's own alpha.eksctl.io/cluster-name tag: CloudFormation stacks, EC2
instances, ELBs, EIPs, and a leftover IAM role. Prints loud LEFTOVER warnings
for anything found instead of silently succeeding.

Flags:
  --yes    skip confirmation prompt
  --help   this help

Usage: ./scripts/eks-live-test.sh teardown [--yes]
EOF
                return 0
                ;;
            --yes|-y) yes=1 ;;
        esac
    done

    account_guard || return 1

    confirm_or_abort "$yes" \
        "This will DELETE cluster '${CLUSTER_NAME}' + the throwaway IAM role in account ${SHARKO_EKS_TEST_ACCOUNT_ID}. Continue?" \
        || return 1

    local role_arn
    role_arn=$(aws iam get-role --role-name "$ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null || true)

    if [ -n "$role_arn" ] && [ "$role_arn" != "None" ] && cluster_exists; then
        log_info "deleting EKS access entry for ${role_arn}"
        aws eks delete-access-entry --cluster-name "$CLUSTER_NAME" --region "$REGION" \
            --principal-arn "$role_arn" >/dev/null 2>&1 || true
    fi

    if cluster_exists; then
        log_info "eksctl delete cluster --name ${CLUSTER_NAME} --region ${REGION} --wait"
        if ! eksctl delete cluster --name "$CLUSTER_NAME" --region "$REGION" --wait; then
            log_fail "eksctl delete cluster failed — see output above; re-run teardown to retry."
        else
            log_ok "cluster deleted"
        fi
    else
        log_info "cluster '${CLUSTER_NAME}' not present (already torn down)"
    fi

    if [ -n "$role_arn" ] && [ "$role_arn" != "None" ]; then
        log_info "deleting IAM role '${ROLE_NAME}'"
        if aws iam delete-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
            log_ok "IAM role deleted"
        else
            log_warn "could not delete IAM role '${ROLE_NAME}' — it may still have attached policies; check manually"
        fi
    else
        log_info "IAM role '${ROLE_NAME}' not present (already torn down)"
    fi

    log_info "removing local kubeconfig ${KUBECONFIG_PATH}"
    rm -f "$KUBECONFIG_PATH"

    echo
    log_info "scanning for anything still billing..."
    local leftovers=0

    local stacks
    stacks=$(aws cloudformation list-stacks --region "$REGION" \
        --stack-status-filter CREATE_COMPLETE UPDATE_COMPLETE ROLLBACK_COMPLETE DELETE_FAILED \
        --query "StackSummaries[?starts_with(StackName, 'eksctl-${CLUSTER_NAME}-')].StackName" \
        --output text 2>/dev/null || true)
    if [ -n "$stacks" ]; then
        log_warn "LEFTOVER: CloudFormation stack(s) still present: ${stacks}"
        leftovers=$((leftovers + 1))
    fi

    local tagged
    tagged=$(aws resourcegroupstaggingapi get-resources --region "$REGION" \
        --tag-filters "Key=sharko:cluster,Values=${CLUSTER_NAME}" \
        --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null || true)
    if [ -n "$tagged" ]; then
        log_warn "LEFTOVER: tagged resource(s) still present (sharko:cluster=${CLUSTER_NAME}): ${tagged}"
        leftovers=$((leftovers + 1))
    fi

    local eksctl_tagged
    eksctl_tagged=$(aws resourcegroupstaggingapi get-resources --region "$REGION" \
        --tag-filters "Key=alpha.eksctl.io/cluster-name,Values=${CLUSTER_NAME}" \
        --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null || true)
    if [ -n "$eksctl_tagged" ]; then
        log_warn "LEFTOVER: eksctl-tagged resource(s) still present: ${eksctl_tagged}"
        leftovers=$((leftovers + 1))
    fi

    if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
        log_warn "LEFTOVER: IAM role '${ROLE_NAME}' still exists"
        leftovers=$((leftovers + 1))
    fi

    if cluster_exists; then
        log_warn "LEFTOVER: cluster '${CLUSTER_NAME}' still shows up in 'eksctl get cluster'"
        leftovers=$((leftovers + 1))
    fi

    echo
    if [ "$leftovers" -eq 0 ]; then
        log_ok "teardown complete — nothing left billing"
    else
        log_warn "teardown finished with ${leftovers} leftover warning(s) above — check the AWS console"
        return 1
    fi
    return 0
}

# =====================================================================
# Subcommand: status
# =====================================================================
do_status() {
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh status — BOTH clusters' state + combined rough cost

Reports each of '${CLUSTER_NAME}' (spoke) and '${HUB_CLUSTER_NAME}' (hub):
exists? node state? per-cluster running-cost estimate — plus the combined
rough cost across whatever is running. NOT authoritative; check AWS Billing
for the real number.

Exit code: 0 if at least one cluster exists, 1 if neither does.

Usage: ./scripts/eks-live-test.sh status [--help]
EOF
                return 0
                ;;
        esac
    done

    account_guard || return 1

    # status_cluster_block <name> <kubeconfig> <node-hourly>: prints one
    # cluster's block; echoes its hourly rate to stdout-var via the global
    # STATUS_HOURLY (0 when not running) so the combined line can sum.
    STATUS_HOURLY="0"
    status_cluster_block() {
        local cname="$1"
        local kcfg="$2"
        local node_hourly="$3"
        STATUS_HOURLY="0"

        echo "  cluster name:   ${cname}"
        if ! eksctl get cluster --name "$cname" --region "$REGION" >/dev/null 2>&1; then
            printf '  cluster:        %snot found%s\n' "$RED" "$RESET"
            return 1
        fi
        printf '  cluster:        %sexists%s\n' "$GREEN" "$RESET"
        STATUS_HOURLY=$(awk -v cp="$EKS_CONTROL_PLANE_HOURLY" -v node="$node_hourly" \
            'BEGIN { printf "%.3f", cp + node }')

        local created_at
        created_at=$(aws eks describe-cluster --name "$cname" --region "$REGION" \
            --query 'cluster.createdAt' --output text 2>/dev/null || true)
        if [ -n "$created_at" ] && [ "$created_at" != "None" ]; then
            echo "  created at:     ${created_at}"
            local now_ts start_ts hours
            now_ts=$(date +%s)
            start_ts=$(date -j -u -f '%Y-%m-%dT%H:%M:%S' "${created_at%%+*}" +%s 2>/dev/null \
                || date -u -d "${created_at}" +%s 2>/dev/null \
                || echo "")
            if [ -n "$start_ts" ]; then
                hours=$(awk -v s="$start_ts" -v n="$now_ts" 'BEGIN { printf "%.1f", (n - s) / 3600 }')
                local cost
                cost=$(awk -v h="$hours" -v rate="$STATUS_HOURLY" \
                    'BEGIN { printf "%.2f", h * rate }')
                echo "  running for:    ~${hours} hour(s)"
                echo "  rough cost:     ~\$${cost} (control plane + 1 node — NOT authoritative, check AWS Billing)"
            fi
        fi

        if [ -r "$kcfg" ]; then
            local node_line
            node_line=$(kubectl --kubeconfig="$kcfg" get nodes --no-headers --request-timeout=10s 2>/dev/null)
            if [ -n "$node_line" ]; then
                local ready_count total_count
                total_count=$(printf '%s\n' "$node_line" | grep -c .)
                ready_count=$(printf '%s\n' "$node_line" | grep -c ' Ready ')
                echo "  nodes:          ${ready_count}/${total_count} Ready"
            else
                printf '  nodes:          %sunreachable via %s%s\n' "$YELLOW" "$kcfg" "$RESET"
            fi
        else
            printf '  nodes:          %skubeconfig not found at %s%s\n' "$YELLOW" "$kcfg" "$RESET"
        fi
        return 0
    }

    echo "${BOLD}EKS live-test status${RESET}"
    echo "=============================="
    echo "  region:         ${REGION}"
    echo
    echo "${BOLD}Spoke${RESET}"
    local spoke_up=0 spoke_rate="0"
    if status_cluster_block "$CLUSTER_NAME" "$KUBECONFIG_PATH" "$NODE_HOURLY_ESTIMATE"; then
        spoke_up=1
    fi
    spoke_rate="$STATUS_HOURLY"
    if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
        echo "  role-setup:     done (${ROLE_NAME} exists)"
    else
        echo "  role-setup:     not run"
    fi

    echo
    echo "${BOLD}Hub${RESET}"
    local hub_up=0 hub_rate="0"
    if status_cluster_block "$HUB_CLUSTER_NAME" "$HUB_KUBECONFIG_PATH" "$HUB_NODE_HOURLY_ESTIMATE"; then
        hub_up=1
    fi
    hub_rate="$STATUS_HOURLY"
    if aws iam get-role --role-name "$HUB_ROLE_NAME" >/dev/null 2>&1; then
        echo "  hub role:       exists (${HUB_ROLE_NAME})"
    else
        echo "  hub role:       not created"
    fi

    echo
    local combined
    combined=$(awk -v a="$spoke_rate" -v b="$hub_rate" 'BEGIN { printf "%.2f", a + b }')
    echo "${BOLD}Combined${RESET}"
    echo "  running now:    ~\$${combined}/hr across both clusters (rough — check AWS Billing)"
    if [ "$spoke_up" = "0" ] && [ "$hub_up" = "0" ]; then
        echo "  nothing running. Bring up: $0 create | $0 env-up"
        echo
        return 1
    fi
    echo
    return 0
}

# =====================================================================
# Hub internals (V2-cleanup-62.3) — pieces of hub-up, factored so each
# phase is testable/inspectable on its own and the flow reads top-down.
# =====================================================================

# hub_iam_setup: the scoped IAM role the hub Sharko (and the hub ArgoCD)
# runs as, via EKS Pod Identity. ZERO AWS keys anywhere in the hub — that
# is the entire point of this phase. Idempotent.
#
# Trust: pods.eks.amazonaws.com (sts:AssumeRole + sts:TagSession) — the
# EKS Pod Identity service principal (per eksctl docs; a pre-existing role
# used with `eksctl create podidentityassociation --role-arn` must carry
# exactly this trust).
#
# Permissions (inline policy ${HUB_POLICY_NAME}):
#   - secretsmanager Get/Describe/Create/Put on sharko-* secrets in ${REGION}
#     (ListSecrets is account-wide because AWS does not support resource-level
#     scoping for that action — read-only names listing, acceptable here)
#   - eks:DescribeCluster + eks:ListClusters (discovery/registration reads)
#   - sts:AssumeRole + sts:TagSession on the phase-1 spoke role only (the
#     per-cluster role_arn hop that registration exercises). TagSession is
#     required in addition to AssumeRole: EKS Pod Identity sessions carry
#     session tags, and AWS rejects the assume with AccessDenied on
#     sts:TagSession if the hub role's policy grants AssumeRole alone.
hub_iam_setup() {
    local hub_role_arn
    hub_role_arn=$(aws iam get-role --role-name "$HUB_ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null || true)

    if [ -n "$hub_role_arn" ] && [ "$hub_role_arn" != "None" ]; then
        log_info "hub role '${HUB_ROLE_NAME}' already exists — reusing"
    else
        log_info "creating hub IAM role '${HUB_ROLE_NAME}' (trust: pods.eks.amazonaws.com)"
        local trust_file
        trust_file="$(mktemp)"
        cat > "$trust_file" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {"Service": "pods.eks.amazonaws.com"},
      "Action": ["sts:AssumeRole", "sts:TagSession"]
    }
  ]
}
EOF
        local create_rc=0
        local hub_role_description
        hub_role_description=$(ascii_safe "Sharko hub-on-EKS live-test Pod Identity role (V2-cleanup-62.3) - safe to delete any time")
        aws iam create-role \
            --role-name "$HUB_ROLE_NAME" \
            --assume-role-policy-document "file://${trust_file}" \
            --description "$hub_role_description" \
            --tags Key=sharko:purpose,Value=live-test Key=sharko:throwaway,Value=true \
            >/dev/null || create_rc=1
        rm -f "$trust_file"
        if [ "$create_rc" -ne 0 ]; then
            log_fail "aws iam create-role (${HUB_ROLE_NAME}) failed"
            return 1
        fi
        hub_role_arn=$(aws iam get-role --role-name "$HUB_ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null)
        log_ok "hub IAM role created"
    fi

    if [ -z "$hub_role_arn" ] || [ "$hub_role_arn" = "None" ]; then
        log_fail "could not resolve role ARN for '${HUB_ROLE_NAME}'"
        return 1
    fi

    log_info "attaching scoped inline policy '${HUB_POLICY_NAME}' (sharko-* secrets, eks reads, assume ${ROLE_NAME})"
    local policy_file
    policy_file="$(mktemp)"
    cat > "$policy_file" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SharkoSecretsScoped",
      "Effect": "Allow",
      "Action": [
        "secretsmanager:GetSecretValue",
        "secretsmanager:DescribeSecret",
        "secretsmanager:CreateSecret",
        "secretsmanager:PutSecretValue"
      ],
      "Resource": "arn:aws:secretsmanager:${REGION}:${SHARKO_EKS_TEST_ACCOUNT_ID}:secret:sharko-*"
    },
    {
      "Sid": "SharkoSecretsList",
      "Effect": "Allow",
      "Action": ["secretsmanager:ListSecrets"],
      "Resource": "*"
    },
    {
      "Sid": "SharkoEKSRead",
      "Effect": "Allow",
      "Action": ["eks:DescribeCluster", "eks:ListClusters"],
      "Resource": "*"
    },
    {
      "Sid": "SharkoAssumeSpokeTestRole",
      "Effect": "Allow",
      "Action": ["sts:AssumeRole", "sts:TagSession"],
      "Resource": "arn:aws:iam::${SHARKO_EKS_TEST_ACCOUNT_ID}:role/${ROLE_NAME}"
    }
  ]
}
EOF
    local put_rc=0
    aws iam put-role-policy \
        --role-name "$HUB_ROLE_NAME" \
        --policy-name "$HUB_POLICY_NAME" \
        --policy-document "file://${policy_file}" >/dev/null || put_rc=1
    rm -f "$policy_file"
    if [ "$put_rc" -ne 0 ]; then
        log_fail "aws iam put-role-policy (${HUB_POLICY_NAME}) failed"
        return 1
    fi
    log_ok "hub role ready: ${hub_role_arn}"
    HUB_ROLE_ARN="$hub_role_arn"
    return 0
}

# hub_pod_identity_setup: eks-pod-identity-agent addon + one association per
# service account that needs AWS credentials on the hub:
#   - sharko/sharko                                (Sharko's SDK default chain)
#   - argocd/argocd-application-controller          } ArgoCD's argocd-k8s-auth
#   - argocd/argocd-server                          } exec plugin mints EKS
#   - argocd/argocd-applicationset-controller       } tokens for the spoke
# Associations are EKS-side metadata — neither the namespace nor the service
# account has to exist yet, so this runs BEFORE ArgoCD/Sharko are installed
# and their very first pods come up with credentials injected. Idempotent.
hub_pod_identity_setup() {
    log_info "installing eks-pod-identity-agent addon on '${HUB_CLUSTER_NAME}'"
    local addon_err addon_rc
    addon_err=$(eksctl create addon --cluster "$HUB_CLUSTER_NAME" --region "$REGION" \
        --name eks-pod-identity-agent 2>&1)
    addon_rc=$?
    if [ "$addon_rc" -ne 0 ] && ! printf '%s' "$addon_err" | grep -qi "already"; then
        log_fail "eksctl create addon eks-pod-identity-agent failed:"
        echo "$addon_err" >&2
        return 1
    fi
    # Best-effort wait for the agent daemonset — pods only need it when they
    # first ask the SDK for credentials (long after this), so a slow rollout
    # is a warn, not a fail.
    if ! hkctl rollout status daemonset/eks-pod-identity-agent -n kube-system \
        --timeout=120s >/dev/null 2>&1; then
        log_warn "eks-pod-identity-agent daemonset not ready after 120s — continuing (pods need it only at first credential use)"
    fi
    log_ok "eks-pod-identity-agent addon present"

    local ns_sa
    for ns_sa in \
        "${HUB_NAMESPACE}/sharko" \
        "argocd/argocd-application-controller" \
        "argocd/argocd-server" \
        "argocd/argocd-applicationset-controller"; do
        local ns="${ns_sa%%/*}"
        local sa="${ns_sa#*/}"
        log_info "pod identity association: ${ns}/${sa} -> ${HUB_ROLE_NAME}"
        local pia_err pia_rc
        pia_err=$(eksctl create podidentityassociation \
            --cluster "$HUB_CLUSTER_NAME" --region "$REGION" \
            --namespace "$ns" \
            --service-account-name "$sa" \
            --role-arn "$HUB_ROLE_ARN" 2>&1)
        pia_rc=$?
        if [ "$pia_rc" -ne 0 ] && ! printf '%s' "$pia_err" | grep -qi "already exists\|ResourceInUseException"; then
            log_fail "eksctl create podidentityassociation (${ns}/${sa}) failed:"
            echo "$pia_err" >&2
            return 1
        fi
    done
    log_ok "pod identity associations in place (sharko + 3 ArgoCD service accounts)"
    return 0
}

# hub_install_argocd: same pinned manifest + server-side apply as
# scripts/sharko-dev.sh up_argocd_only(), targeted at the hub kubeconfig.
hub_install_argocd() {
    local n
    n=$(hkctl get deployment -n argocd argocd-server \
        -o jsonpath='{.status.availableReplicas}' 2>/dev/null || true)
    if [ -n "$n" ] && [ "$n" -ge 1 ] 2>/dev/null; then
        log_ok "ArgoCD already installed on the hub"
        return 0
    fi
    log_info "installing ArgoCD on the hub (server-side apply for large CRDs)"
    hkctl create namespace argocd >/dev/null 2>&1 || true
    if ! hkctl apply --server-side --force-conflicts -n argocd -f "$ARGOCD_MANIFEST_URL"; then
        log_fail "ArgoCD manifest apply failed"
        return 1
    fi
    log_info "waiting for argocd-server (timeout 300s)"
    if ! hkctl wait --for=condition=available --timeout=300s deployment/argocd-server -n argocd; then
        log_fail "argocd-server did not become available within 300s"
        return 1
    fi
    log_ok "ArgoCD ready on the hub"
    return 0
}

# hub_install_sharko: install Sharko from THE REPO'S OWN Helm chart
# (charts/sharko) with the published ghcr image — this is the "the chart
# installs on real EKS" product claim under test. Values grounded against
# charts/sharko/values.yaml:
#   image.repository / image.tag       — published image, pinned tag
#   clusterRegSource.type=argocd       — SHARKO_CLUSTER_REG_TYPE env; enables
#   clusterRegSource.argocdNamespace   — the cluster-Secret reconciler
#   imagePullSecrets[0].name           — only when GHCR is not anonymously
#                                        pullable (see below)
# The chart's service account defaults to "sharko" (release name "sharko" →
# sharko.fullname "sharko" → sharko.serviceAccountName "sharko"), which is
# exactly the name hub_pod_identity_setup bound — no serviceAccount values
# needed, and NO AWS keys are passed anywhere.
hub_install_sharko() {
    local -a pull_args=()

    log_info "checking whether ${SHARKO_IMAGE_REPO}:${SHARKO_IMAGE_TAG} is anonymously pullable from GHCR"
    if ghcr_image_pullable; then
        log_ok "image is publicly pullable — no imagePullSecret needed"
    else
        log_warn "image is NOT anonymously pullable (private GHCR package, or network issue)"
        if ! command -v gh >/dev/null 2>&1; then
            log_fail "gh CLI not found — needed to mint an imagePullSecret for the private package."
            echo "       Install it (brew install gh && gh auth login), or make the" >&2
            echo "       ghcr.io package public, then re-run." >&2
            return 1
        fi
        local gh_token gh_user
        gh_token=$(gh auth token 2>/dev/null || true)
        if [ -z "$gh_token" ]; then
            log_fail "gh auth token returned nothing — run 'gh auth login' first."
            return 1
        fi
        gh_user=$(gh api user -q .login 2>/dev/null || echo "token")
        log_info "creating imagePullSecret 'sharko-ghcr' in namespace '${HUB_NAMESPACE}' from your gh auth token"
        hkctl create namespace "$HUB_NAMESPACE" >/dev/null 2>&1 || true
        if ! hkctl create secret docker-registry sharko-ghcr \
            -n "$HUB_NAMESPACE" \
            --docker-server=ghcr.io \
            --docker-username="$gh_user" \
            --docker-password="$gh_token" \
            --dry-run=client -o yaml | hkctl apply -f - >/dev/null; then
            log_fail "creating imagePullSecret failed"
            return 1
        fi
        pull_args+=(--set "imagePullSecrets[0].name=sharko-ghcr")
        log_ok "imagePullSecret in place"
    fi

    log_info "helm install sharko from ${REPO_ROOT}/charts/sharko (image ${SHARKO_IMAGE_REPO}:${SHARKO_IMAGE_TAG})"
    if ! helm --kubeconfig "$HUB_KUBECONFIG_PATH" upgrade --install sharko \
        "${REPO_ROOT}/charts/sharko" \
        --namespace "$HUB_NAMESPACE" --create-namespace \
        --set image.repository="$SHARKO_IMAGE_REPO" \
        --set image.tag="$SHARKO_IMAGE_TAG" \
        --set clusterRegSource.type=argocd \
        --set clusterRegSource.argocdNamespace=argocd \
        "${pull_args[@]}" >/tmp/sharko-hub-helm.log 2>&1; then
        log_fail "helm install failed (last 20 lines):"
        tail -20 /tmp/sharko-hub-helm.log >&2
        return 1
    fi
    log_ok "helm install complete"

    log_info "waiting for deployment/sharko rollout (timeout 180s)"
    if ! hkctl rollout status -n "$HUB_NAMESPACE" deployment/sharko --timeout=180s >/dev/null 2>&1; then
        log_fail "deployment/sharko did not become ready within 180s"
        hkctl get pods -n "$HUB_NAMESPACE" >&2 || true
        hkctl logs -n "$HUB_NAMESPACE" deployment/sharko --tail=20 >&2 2>/dev/null || true
        return 1
    fi
    log_ok "Sharko running on the hub"
    return 0
}

# hub_argocd_token: mint an ArgoCD API token for the hub Sharko connection.
# Mirrors scripts/sharko-dev.sh do_argocd_token (the proven apiKey gauntlet):
# argocd-cm apiKey capability, argocd-rbac-cm role:admin grant (an admin
# apiKey token has sub "admin:apiKey" and is NOT covered by the admin
# password-session bypass), argocd-server restart when patched, port-forward,
# argocd login, generate-token. Sets $HUB_ARGOCD_TOKEN.
HUB_ARGOCD_TOKEN=""
hub_argocd_token() {
    local patched=0
    local current_caps
    current_caps=$(hkctl get configmap argocd-cm -n argocd \
        -o 'jsonpath={.data.accounts\.admin}' 2>/dev/null || true)
    if printf '%s' "$current_caps" | grep -q "apiKey"; then
        log_info "apiKey capability already enabled for account 'admin'"
    else
        log_info "patching argocd-cm: accounts.admin=\"apiKey, login\""
        if ! hkctl patch configmap argocd-cm -n argocd --type merge \
            -p '{"data":{"accounts.admin":"apiKey, login"}}' >/dev/null 2>&1; then
            log_fail "kubectl patch configmap argocd-cm failed"
            return 1
        fi
        patched=1
    fi

    local current_policy
    current_policy=$(hkctl get configmap argocd-rbac-cm -n argocd \
        -o 'jsonpath={.data.policy\.csv}' 2>/dev/null || true)
    local account_rule="g, admin, role:admin"
    if printf '%s' "$current_policy" | grep -qxF "$account_rule"; then
        log_info "argocd-rbac-cm already grants admin role:admin"
    else
        local new_policy
        if [ -z "$current_policy" ]; then
            new_policy="$account_rule"
        else
            new_policy="${current_policy}
${account_rule}"
        fi
        local patch_json
        patch_json=$(NEW_POLICY="$new_policy" python3 -c '
import json, os
print(json.dumps({"data": {"policy.csv": os.environ["NEW_POLICY"]}}))
' 2>/dev/null)
        if [ -z "$patch_json" ]; then
            log_fail "failed to render argocd-rbac-cm patch JSON"
            return 1
        fi
        if ! printf '%s' "$patch_json" | hkctl patch configmap argocd-rbac-cm \
            -n argocd --type merge --patch-file=/dev/stdin >/dev/null 2>&1; then
            log_fail "kubectl patch configmap argocd-rbac-cm failed"
            return 1
        fi
        log_ok "patched argocd-rbac-cm to grant admin role:admin"
    fi

    if [ "$patched" = "1" ]; then
        log_info "restarting argocd-server (apiKey capability change)"
        if ! hkctl rollout restart -n argocd deployment/argocd-server >/dev/null 2>&1; then
            log_fail "kubectl rollout restart deployment/argocd-server failed"
            return 1
        fi
        if ! hkctl rollout status -n argocd deployment/argocd-server --timeout=120s >/dev/null 2>&1; then
            log_fail "argocd-server rollout did not complete within 120s"
            return 1
        fi
    fi

    log_info "starting port-forward localhost:${HUB_ARGOCD_LOCAL_PORT} -> svc/argocd-server:443"
    pkill -f "port-forward.*svc/argocd-server ${HUB_ARGOCD_LOCAL_PORT}:" 2>/dev/null || true
    sleep 1
    hkctl port-forward -n argocd svc/argocd-server \
        "${HUB_ARGOCD_LOCAL_PORT}:443" >/tmp/sharko-hub-argocd-pf.log 2>&1 &
    disown 2>/dev/null || true
    local i pf_ok=0
    for i in $(seq 1 15); do
        if curl -sS -o /dev/null --connect-timeout 2 -k \
            "https://localhost:${HUB_ARGOCD_LOCAL_PORT}/healthz" >/dev/null 2>&1; then
            pf_ok=1
            break
        fi
        sleep 1
    done
    if [ "$pf_ok" != "1" ]; then
        log_fail "port-forward to hub argocd-server did not come up on localhost:${HUB_ARGOCD_LOCAL_PORT}"
        cat /tmp/sharko-hub-argocd-pf.log >&2 2>/dev/null || true
        return 1
    fi

    local admin_pw
    admin_pw=$(hkctl get secret -n argocd argocd-initial-admin-secret \
        -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)
    if [ -z "$admin_pw" ]; then
        log_fail "argocd-initial-admin-secret not found on the hub"
        return 1
    fi

    log_info "argocd login localhost:${HUB_ARGOCD_LOCAL_PORT} as admin"
    local login_out login_rc
    login_out=$(argocd login "localhost:${HUB_ARGOCD_LOCAL_PORT}" \
        --username admin --password "$admin_pw" \
        --insecure --grpc-web 2>&1)
    login_rc=$?
    if [ "$login_rc" -ne 0 ]; then
        log_fail "argocd login failed (rc=${login_rc}):"
        printf '%s\n' "$login_out" | head -5 >&2
        return 1
    fi

    log_info "argocd account generate-token --account admin"
    local token gen_rc
    token=$(argocd account generate-token --account admin 2>&1)
    gen_rc=$?
    if [ "$gen_rc" -ne 0 ] || [ -z "$token" ]; then
        log_fail "argocd account generate-token failed (rc=${gen_rc}):"
        printf '%s\n' "$token" | head -5 >&2
        return 1
    fi
    token=$(printf '%s' "$token" | tr -d '[:space:]')
    if [ -z "$token" ]; then
        log_fail "generated ArgoCD token is empty after trim"
        return 1
    fi
    HUB_ARGOCD_TOKEN="$token"
    log_ok "ArgoCD API token minted for the hub connection"
    return 0
}

# hub_configure_sharko: the API-driven seeding of the hub Sharko. Grounded
# mechanism (internal/service/connection.go + internal/api/init.go):
#   1. POST /api/v1/connections/  — one connection carrying git (repo URL +
#      SHARKO_GITHUB_TOKEN), argocd (in-cluster URL + minted token), and
#      provider {type: aws-sm, region} with NO key fields — the aws-sm
#      backend uses the SDK default credential chain, which inside the pod
#      resolves to EKS Pod Identity. Saving the connection hot-reloads the
#      credentials provider + reconciler (ReinitializeFromConnection) — no
#      pod restart needed.
#   2. POST /api/v1/connections/active — make it the active connection.
#   3. Repo wiring, depending on GET /api/v1/repo/status:
#      - not initialized  → POST /api/v1/init (bootstrap_argocd+auto_merge)
#        and poll the async operation. Init scaffolds the repo, registers it
#        in ArgoCD, applies the root app, waits for sync.
#      - initialized but the HUB's ArgoCD has no bootstrap app → Sharko's
#        POST /init deliberately refuses to re-bootstrap an initialized repo
#        (RepoStatePartial → fail; V2-cleanup-51 guard), so THIS SCRIPT does
#        the two things init would have done: create the ArgoCD repository
#        credential Secret and apply the repo's own root-app.yaml. (Product
#        gap worth knowing: a fresh Sharko install pointed at an existing
#        gitops repo has no API path to adopt it — reported with 62.3.)
#   4. GET /api/v1/providers — assert the aws-sm provider reports
#      "connected" with zero stored keys. That line is the proof.
hub_configure_sharko() {
    hub_api_init
    hub_pf_start || return 1
    hub_sharko_login || return 1
    log_ok "logged in to the hub Sharko as admin"

    # ---- 1. connection ----
    log_info "creating connection '${HUB_CONNECTION_NAME}' (git + argocd + aws-sm provider, NO AWS keys)"
    local payload code
    payload=$(CN="$HUB_CONNECTION_NAME" REPO_URL="$SHARKO_GITOPS_REPO_URL" \
        GIT_TOKEN="$SHARKO_GITHUB_TOKEN" AT="$HUB_ARGOCD_TOKEN" \
        AURL="$HUB_ARGOCD_URL" R="$REGION" python3 -c '
import json, os
e = os.environ
print(json.dumps({
    "name": e["CN"],
    "description": "hub-on-EKS live test (V2-cleanup-62.3) - AWS identity via EKS Pod Identity, zero stored keys",
    "git": {"provider": "github", "repo_url": e["REPO_URL"], "token": e["GIT_TOKEN"]},
    "argocd": {"server_url": e["AURL"], "token": e["AT"], "namespace": "argocd"},
    "provider": {"type": "aws-sm", "region": e["R"]},
    "gitops": {"pr_auto_merge": True},
    "set_as_default": True,
}))')
    code=$(hub_api POST /api/v1/connections/ "$payload")
    case "$code" in
        200|201) log_ok "connection created" ;;
        409)     log_info "connection already exists — reusing" ;;
        *)
            if grep -qi "already exists" "$HUB_API_BODY" 2>/dev/null; then
                log_info "connection already exists — reusing"
            else
                log_fail "POST /api/v1/connections/ returned ${code}:"
                head -c 300 "$HUB_API_BODY" >&2 2>/dev/null || true
                echo >&2
                return 1
            fi
            ;;
    esac

    payload=$(J_CN="$HUB_CONNECTION_NAME" json_build connection_name=J_CN)
    code=$(hub_api POST /api/v1/connections/active "$payload")
    if [ "$code" != "200" ]; then
        log_fail "POST /api/v1/connections/active returned ${code}:"
        head -c 300 "$HUB_API_BODY" >&2 2>/dev/null || true
        echo >&2
        return 1
    fi
    log_ok "connection '${HUB_CONNECTION_NAME}' is active (provider hot-reloaded, no restart needed)"

    # ---- 2. repo wiring ----
    code=$(hub_api GET /api/v1/repo/status)
    local initialized synced
    initialized=$(json_field "$HUB_API_BODY" initialized)
    synced=$(json_field "$HUB_API_BODY" bootstrap_synced)
    log_info "repo status: initialized=${initialized:-?} bootstrap_synced=${synced:-?}"

    if [ "$initialized" != "true" ]; then
        log_info "repo not initialized — running Sharko's own init (scaffold + ArgoCD repo + root app)"
        payload='{"bootstrap_argocd": true, "auto_merge": true}'
        code=$(hub_api POST /api/v1/init "$payload")
        if [ "$code" != "200" ] && [ "$code" != "202" ]; then
            log_fail "POST /api/v1/init returned ${code}:"
            head -c 300 "$HUB_API_BODY" >&2 2>/dev/null || true
            echo >&2
            return 1
        fi
        local op_id
        op_id=$(json_field "$HUB_API_BODY" operation_id)
        if [ -z "$op_id" ]; then
            log_fail "init did not return an operation_id"
            return 1
        fi
        log_info "init operation ${op_id} started — polling (up to 15 min)"
        local i op_status=""
        for i in $(seq 1 90); do
            sleep 10
            code=$(hub_api GET "/api/v1/operations/${op_id}")
            op_status=$(json_field "$HUB_API_BODY" status)
            case "$op_status" in
                completed) break ;;
                failed)
                    log_fail "init operation failed: $(json_field "$HUB_API_BODY" error)"
                    return 1
                    ;;
            esac
        done
        if [ "$op_status" != "completed" ]; then
            log_fail "init operation did not complete within 15 min (last status: ${op_status:-unknown})"
            return 1
        fi
        log_ok "repo initialized + ArgoCD bootstrapped by Sharko init"
    elif [ "$synced" = "true" ]; then
        log_ok "repo initialized and bootstrap app already Synced — nothing to wire"
    else
        log_info "repo is initialized but this hub's ArgoCD has no healthy bootstrap — wiring it manually"
        log_info "(Sharko's POST /init refuses to re-bootstrap an initialized repo — known product gap)"

        # 2a. ArgoCD repository credential Secret (the shape ArgoCD's
        # declarative setup documents: secret-type=repository label).
        log_info "creating ArgoCD repository credential Secret for ${SHARKO_GITOPS_REPO_URL}"
        if ! hkctl create secret generic sharko-gitops-repo \
            -n argocd \
            --from-literal=type=git \
            --from-literal=url="$SHARKO_GITOPS_REPO_URL" \
            --from-literal=username=sharko \
            --from-literal=password="$SHARKO_GITHUB_TOKEN" \
            --dry-run=client -o yaml | hkctl apply -f - >/dev/null; then
            log_fail "creating the ArgoCD repository Secret failed"
            return 1
        fi
        hkctl label secret sharko-gitops-repo -n argocd \
            "argocd.argoproj.io/secret-type=repository" --overwrite >/dev/null 2>&1 || true
        log_ok "ArgoCD repository credential in place"

        # 2b. Apply the repo's OWN root-app.yaml (written to the repo root by
        # Sharko init back when the repo was first initialized — a raw fetch
        # of the committed file still carries UNRESOLVED template placeholders
        # on disk; see the substitution step below for why and how).
        log_info "fetching root-app.yaml from the gitops repo and applying it to the hub ArgoCD"
        local owner_repo
        owner_repo=$(RURL="$SHARKO_GITOPS_REPO_URL" python3 -c '
import os, urllib.parse
u = urllib.parse.urlparse(os.environ["RURL"])
parts = [p for p in u.path.strip("/").removesuffix(".git").split("/") if p]
print("/".join(parts[:2]))')
        if [ -z "$owner_repo" ]; then
            log_fail "could not parse owner/repo from SHARKO_GITOPS_REPO_URL"
            return 1
        fi
        local root_app_file
        root_app_file="$(mktemp "${TMPDIR:-/tmp}/sharko-root-app.XXXXXX.yaml")"
        if ! curl -fsSL --max-time 30 \
            -H "Authorization: Bearer ${SHARKO_GITHUB_TOKEN}" \
            -H "Accept: application/vnd.github.raw+json" \
            "https://api.github.com/repos/${owner_repo}/contents/root-app.yaml?ref=${EKS_TEST_GITOPS_BRANCH:-main}" \
            -o "$root_app_file"; then
            log_fail "could not fetch root-app.yaml from github.com/${owner_repo} (branch ${EKS_TEST_GITOPS_BRANCH:-main})"
            rm -f "$root_app_file"
            return 1
        fi

        # The repo's root-app.yaml is the Helm-templated
        # templates/bootstrap/root-app.yaml (see internal/orchestrator/init.go)
        # and carries UNRESOLVED {{ .Values.repoURL }} / {{ .Values.targetRevision }}
        # placeholders on disk — Sharko's own init only resolves them at
        # commit time on first bootstrap, not on every read. kubectl apply
        # chokes on the literal "{{ ... }}" text, so resolve them here the
        # same way Sharko does, then fail loudly if anything got missed.
        #
        # Escape sed replacement metacharacters (backslash first, then & and
        # the | delimiter) so a URL/branch value can never be misread as a
        # sed backreference or "whole match" token.
        local esc_repo_url esc_branch
        esc_repo_url=$(printf '%s' "$SHARKO_GITOPS_REPO_URL" | sed -e 's/\\/\\\\/g' -e 's/&/\\\&/g' -e 's/|/\\|/g')
        esc_branch=$(printf '%s' "${EKS_TEST_GITOPS_BRANCH:-main}" | sed -e 's/\\/\\\\/g' -e 's/&/\\\&/g' -e 's/|/\\|/g')
        local subst_file
        subst_file="$(mktemp "${TMPDIR:-/tmp}/sharko-root-app-subst.XXXXXX.yaml")"
        if ! sed -e "s|{{ \.Values\.repoURL }}|${esc_repo_url}|g" \
            -e "s|{{ \.Values\.targetRevision }}|${esc_branch}|g" \
            "$root_app_file" > "$subst_file"; then
            log_fail "sed substitution of root-app.yaml placeholders failed"
            rm -f "$root_app_file" "$subst_file"
            return 1
        fi
        mv "$subst_file" "$root_app_file"
        if grep -q '{{' "$root_app_file"; then
            log_fail "root-app.yaml still has unresolved template placeholder(s) after substitution:"
            grep -n '{{' "$root_app_file" >&2
            rm -f "$root_app_file"
            return 1
        fi
        log_ok "root-app.yaml placeholders resolved (repoURL, targetRevision)"

        if ! hkctl apply -n argocd -f "$root_app_file" >/dev/null; then
            log_fail "kubectl apply of root-app.yaml failed"
            rm -f "$root_app_file"
            return 1
        fi
        rm -f "$root_app_file"
        log_ok "root app applied"

        log_info "waiting for application 'cluster-addons-bootstrap' to reach Synced/Healthy (up to 5 min)"
        local i app_state=""
        for i in $(seq 1 30); do
            app_state=$(hkctl get application cluster-addons-bootstrap -n argocd \
                -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null || true)
            if [ "$app_state" = "Synced/Healthy" ]; then
                break
            fi
            sleep 10
        done
        if [ "$app_state" = "Synced/Healthy" ]; then
            log_ok "bootstrap app Synced/Healthy on the hub"
        else
            log_warn "bootstrap app is '${app_state:-not found}' after 5 min — continuing; check the ArgoCD UI"
        fi
    fi

    # ---- 3. the zero-keys proof ----
    code=$(hub_api GET /api/v1/providers)
    local ptype pstatus
    ptype=$(json_field "$HUB_API_BODY" configured_provider.type)
    pstatus=$(json_field "$HUB_API_BODY" configured_provider.status)
    if [ "$ptype" = "aws-sm" ] && [ "$pstatus" = "connected" ]; then
        log_ok "Secrets Provider: aws-sm (${REGION}) reports CONNECTED — with zero stored AWS keys (Pod Identity)"
    else
        log_fail "Secrets Provider check: type='${ptype}' status='${pstatus}' (want aws-sm/connected)"
        echo "       error: $(json_field "$HUB_API_BODY" configured_provider.error)" >&2
        return 1
    fi
    return 0
}

# =====================================================================
# Subcommand: hub-up (V2-cleanup-62.3)
# =====================================================================
do_hub_up() {
    local yes=0
    local resume=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh hub-up — EKS hub: ArgoCD + Sharko from the repo Helm chart

Creates cluster '${HUB_CLUSTER_NAME}' (${REGION}, 1x ${HUB_NODE_TYPE}) and
builds the full hub on it:
  1. eksctl create cluster (kubeconfig ONLY at ${HUB_KUBECONFIG_PATH};
     never merged into ~/.kube/config)
  2. eks-pod-identity-agent addon
  3. Scoped IAM role '${HUB_ROLE_NAME}' + Pod Identity associations for
     Sharko AND ArgoCD's controller/server/appset service accounts —
     ZERO AWS keys are stored anywhere in the hub
  4. ArgoCD (same pinned manifest as sharko-dev.sh)
  5. Sharko from charts/sharko with image ${SHARKO_IMAGE_REPO}:${SHARKO_IMAGE_TAG}
     (imagePullSecret auto-created from 'gh auth token' ONLY if the GHCR
     package is not anonymously pullable)
  6. ArgoCD API token mint + one Sharko connection via API:
     git = \$SHARKO_GITOPS_REPO_URL + \$SHARKO_GITHUB_TOKEN,
     Secrets Provider = aws-sm ${REGION} with NO keys (SDK default chain
     -> Pod Identity), repo bootstrap wired on the hub ArgoCD

Node sizing: 1x ${HUB_NODE_TYPE} (~\$${HUB_NODE_HOURLY_ESTIMATE}/hr) — ArgoCD non-HA +
Sharko need ~4 GB headroom. 2x t3.small costs the same per hour but splits
memory 2+2 GB, which crowds ArgoCD's application-controller; one t3.medium
keeps everything co-located. Override with EKS_TEST_HUB_NODE_TYPE.

Required env: SHARKO_EKS_TEST_ACCOUNT_ID, SHARKO_GITHUB_TOKEN,
SHARKO_GITOPS_REPO_URL (see the top-of-file header).

Flags:
  --yes      skip the "this costs money" confirmation prompt
  --resume   the hub cluster already exists (e.g. a prior run died partway
             through steps 2-7) — do NOT refuse; skip step 1 (cluster
             create), just refresh the kubeconfig via 'eksctl utils
             write-kubeconfig', and run steps 2-7 normally. Every one of
             those steps is already idempotent (role/addon/association
             reuse, helm upgrade --install, "already exists" tolerance),
             so resuming just finishes what didn't complete.
  --help     this help

Usage: ./scripts/eks-live-test.sh hub-up [--yes] [--resume]
EOF
                return 0
                ;;
            --yes|-y) yes=1 ;;
            --resume) resume=1 ;;
        esac
    done

    hub_preflight_tools || return 1
    account_guard || return 1
    require_hub_env || return 1

    if hub_cluster_exists; then
        if [ "$resume" != "1" ]; then
            log_fail "hub cluster '${HUB_CLUSTER_NAME}' already exists in ${REGION} — refusing to create a second one."
            echo "       Run: $0 status          to inspect it" >&2
            echo "       Run: $0 env-down        to remove it first" >&2
            echo "       Run: $0 hub-up --resume to finish provisioning it instead" >&2
            return 1
        fi
        log_warn "hub cluster exists — resuming provisioning on it"
    fi

    log_warn "about to create a REAL EKS hub cluster in account ${SHARKO_EKS_TEST_ACCOUNT_ID}, region ${REGION}."
    log_warn "hub cost: control plane ~\$${EKS_CONTROL_PLANE_HOURLY}/hr + 1x ${HUB_NODE_TYPE} ~\$${HUB_NODE_HOURLY_ESTIMATE}/hr (2x t3.small would cost the same ~\$0.042/hr — see --help for why one t3.medium)."
    log_warn "with the phase-1 spoke also running, the whole env is ~\$0.26/hr. Takes ~15-20 min."
    confirm_or_abort "$yes" "Continue?" || return 1

    # ---- [1/7] cluster ----
    if [ "$resume" = "1" ] && hub_cluster_exists; then
        log_info "[1/7] hub cluster already exists — refreshing kubeconfig only"
        if ! eksctl utils write-kubeconfig \
            --cluster "$HUB_CLUSTER_NAME" --region "$REGION" \
            --kubeconfig "$HUB_KUBECONFIG_PATH"; then
            log_fail "eksctl utils write-kubeconfig failed — cannot resume without a working hub kubeconfig."
            return 1
        fi
        chmod 600 "$HUB_KUBECONFIG_PATH" 2>/dev/null || true
        log_ok "hub kubeconfig refreshed (${HUB_KUBECONFIG_PATH})"
    else
        local tags="sharko:purpose=live-test,sharko:throwaway=true,sharko:cluster=${HUB_CLUSTER_NAME}"
        tags=$(ascii_safe "$tags")
        local -a cmd=(eksctl create cluster
            --name "$HUB_CLUSTER_NAME"
            --region "$REGION"
            --nodegroup-name "$HUB_NODEGROUP_NAME"
            --nodes 1 --nodes-min 1 --nodes-max 1
            --node-type "$HUB_NODE_TYPE"
            --managed
            --kubeconfig "$HUB_KUBECONFIG_PATH"
            --tags "$tags"
        )
        if [ -n "$K8S_VERSION" ]; then
            cmd+=(--version "$K8S_VERSION")
        fi
        log_info "[1/7] running: ${cmd[*]}"
        if ! "${cmd[@]}"; then
            log_fail "eksctl create cluster failed — see output above."
            echo "       If a partial stack was left behind, run: $0 env-down" >&2
            return 1
        fi
        chmod 600 "$HUB_KUBECONFIG_PATH" 2>/dev/null || true
        log_ok "hub cluster created (kubeconfig: ${HUB_KUBECONFIG_PATH})"
    fi

    # ---- [2/7] IAM role (before associations reference it) ----
    log_info "[2/7] scoped IAM role"
    hub_iam_setup || return 1

    # ---- [3/7] pod identity (BEFORE ArgoCD/Sharko so first pods get creds) ----
    log_info "[3/7] EKS Pod Identity (agent addon + associations)"
    hub_pod_identity_setup || return 1

    # ---- [4/7] ArgoCD ----
    log_info "[4/7] ArgoCD"
    hub_install_argocd || return 1

    # ---- [5/7] Sharko from the repo chart ----
    log_info "[5/7] Sharko (repo Helm chart, published image)"
    hub_install_sharko || return 1

    # ---- [6/7] ArgoCD API token ----
    log_info "[6/7] ArgoCD API token"
    hub_argocd_token || return 1

    # ---- [7/7] configure Sharko via its API ----
    log_info "[7/7] Sharko connection + repo wiring via API"
    hub_configure_sharko || return 1

    echo
    log_ok "hub is up: Sharko + ArgoCD on '${HUB_CLUSTER_NAME}', zero stored AWS keys"
    echo "       Sharko UI:  ${HUB_HOST}  (port-forward already running)"
    echo "       Admin pw:   KUBECONFIG=${HUB_KUBECONFIG_PATH} kubectl get secret sharko-initial-admin-secret -n ${HUB_NAMESPACE} -o jsonpath='{.data.password}' | base64 -d"
    echo "       Next:       $0 spoke-connect      (wire the phase-1 spoke into this hub)"
    return 0
}

# =====================================================================
# Subcommand: spoke-connect (V2-cleanup-62.3)
# =====================================================================
do_spoke_connect() {
    local yes=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh spoke-connect — register the phase-1 spoke in the HUB Sharko

Wires the EXISTING spoke cluster '${CLUSTER_NAME}' into the hub:
  1. Ensures the phase-1 test role '${ROLE_NAME}' + its access entry exist
     (same as 'role-setup' — free, idempotent)
  2. EKS access entry on the spoke for the hub's Pod Identity role
     '${HUB_ROLE_NAME}' (+ AmazonEKSClusterAdminPolicy, cluster-wide —
     fine for a throwaway)
  3. Ensures the AWS Secrets Manager secret '${CLUSTER_NAME}' exists
     (reuses phase 1's; creates it from the live cluster endpoint/CA if
     missing — no roleArn inside, so the per-cluster role_arn below is
     what token minting actually uses)
  4. Registers the spoke in the HUB Sharko via
     POST /api/v1/clusters with creds_source=eks-token AND
     role_arn=<the phase-1 role> — deliberately exercising the per-cluster
     role_arn field (the #466 fix) from the API side

Flags:
  --yes     skip confirmation prompt
  --help    this help

Usage: ./scripts/eks-live-test.sh spoke-connect [--yes]
EOF
                return 0
                ;;
            --yes|-y) yes=1 ;;
        esac
    done

    hub_preflight_tools || return 1
    account_guard || return 1

    if ! cluster_exists; then
        log_fail "spoke cluster '${CLUSTER_NAME}' not found in ${REGION} — run '$0 create' first (or '$0 env-up' for the full flow)."
        return 1
    fi
    if ! hub_cluster_exists; then
        log_fail "hub cluster '${HUB_CLUSTER_NAME}' not found in ${REGION} — run '$0 hub-up' first."
        return 1
    fi
    if [ ! -r "$HUB_KUBECONFIG_PATH" ]; then
        log_fail "hub kubeconfig not found at ${HUB_KUBECONFIG_PATH} — re-run '$0 hub-up' (or: aws eks update-kubeconfig --name ${HUB_CLUSTER_NAME} --region ${REGION} --kubeconfig ${HUB_KUBECONFIG_PATH})"
        return 1
    fi
    case "$CLUSTER_NAME" in
        sharko-*) : ;;
        *)
            log_fail "spoke name '${CLUSTER_NAME}' does not start with 'sharko-' — the hub role's SecretsManager policy only covers sharko-* secrets."
            return 1
            ;;
    esac

    confirm_or_abort "$yes" \
        "This wires spoke '${CLUSTER_NAME}' into the hub Sharko (access entries + SM secret + API registration). Continue?" \
        || return 1

    # ---- 1. phase-1 role + access entry (idempotent reuse of role-setup) ----
    log_info "[1/4] ensuring phase-1 role '${ROLE_NAME}' + spoke access entry"
    do_role_setup --yes >/dev/null 2>&1 || do_role_setup --yes || return 1
    local spoke_role_arn
    spoke_role_arn=$(aws iam get-role --role-name "$ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null || true)
    if [ -z "$spoke_role_arn" ] || [ "$spoke_role_arn" = "None" ]; then
        log_fail "could not resolve the phase-1 role ARN ('${ROLE_NAME}')"
        return 1
    fi
    log_ok "phase-1 role ready: ${spoke_role_arn}"

    # ---- 2. hub role access entry on the spoke ----
    local hub_role_arn
    hub_role_arn=$(aws iam get-role --role-name "$HUB_ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null || true)
    if [ -z "$hub_role_arn" ] || [ "$hub_role_arn" = "None" ]; then
        log_fail "hub role '${HUB_ROLE_NAME}' not found — run '$0 hub-up' first."
        return 1
    fi
    log_info "[2/4] access entry on '${CLUSTER_NAME}' for the hub role"
    ensure_access_entry "$CLUSTER_NAME" "$hub_role_arn" || return 1
    log_ok "hub role can reach the spoke directly"

    # ---- 3. SM secret (reuse phase 1's, create if missing) ----
    log_info "[3/4] AWS Secrets Manager secret '${CLUSTER_NAME}'"
    if aws secretsmanager describe-secret --region "$REGION" \
        --secret-id "$CLUSTER_NAME" >/dev/null 2>&1; then
        log_ok "secret already exists (phase 1) — reusing"
        log_info "note: if phase 1's Leg 2 added a roleArn INSIDE the secret, that wins precedence over the per-cluster role_arn (both point at '${ROLE_NAME}' here, so the hop is the same either way)"
    else
        log_info "secret missing — creating it from the live cluster endpoint/CA"
        local ep_ca server ca_b64
        ep_ca=$(spoke_endpoint_and_ca)
        server=$(printf '%s' "$ep_ca" | awk '{print $1}')
        ca_b64=$(printf '%s' "$ep_ca" | awk '{print $2}')
        if [ -z "$server" ] || [ -z "$ca_b64" ] || [ "$server" = "None" ]; then
            log_fail "aws eks describe-cluster did not return endpoint/CA for '${CLUSTER_NAME}'"
            return 1
        fi
        local secret_json
        secret_json=$(CN="$CLUSTER_NAME" H="$server" CA="$ca_b64" R="$REGION" \
            json_build clusterName=CN host=H caData=CA region=R)
        secret_json=$(ascii_safe "$secret_json")
        local secret_description
        secret_description=$(ascii_safe "Sharko EKS live-test structured EKS secret (V2-cleanup-62) - safe to delete any time")
        if ! aws secretsmanager create-secret --region "$REGION" \
            --name "$CLUSTER_NAME" \
            --description "$secret_description" \
            --secret-string "$secret_json" >/dev/null; then
            log_fail "aws secretsmanager create-secret failed"
            return 1
        fi
        log_ok "secret created (structured EKS JSON, no roleArn — per-cluster role_arn drives the hop)"
    fi

    # ---- 4. register via the hub Sharko API ----
    log_info "[4/4] registering '${CLUSTER_NAME}' in the hub Sharko (creds_source=eks-token, role_arn=${ROLE_NAME})"
    hub_api_init
    hub_pf_start || return 1
    hub_sharko_login || return 1

    local payload code
    payload=$(CN="$CLUSTER_NAME" R="$REGION" RA="$spoke_role_arn" python3 -c '
import json, os
e = os.environ
print(json.dumps({
    "name": e["CN"],
    "provider": "eks",
    "creds_source": "eks-token",
    "region": e["R"],
    "role_arn": e["RA"],
    "auto_merge": True,
}))')
    code=$(hub_api POST /api/v1/clusters "$payload")
    case "$code" in
        200|201)
            log_ok "registration accepted (HTTP ${code})"
            ;;
        409)
            log_info "cluster already registered — continuing"
            ;;
        *)
            if grep -qi "already" "$HUB_API_BODY" 2>/dev/null; then
                log_info "cluster already registered — continuing"
            else
                log_fail "POST /api/v1/clusters returned ${code}:"
                head -c 400 "$HUB_API_BODY" >&2 2>/dev/null || true
                echo >&2
                return 1
            fi
            ;;
    esac

    log_info "waiting for '${CLUSTER_NAME}' to appear in GET /api/v1/clusters (PR merge + reconciler; up to 3 min)"
    local i found=""
    for i in $(seq 1 18); do
        code=$(hub_api GET /api/v1/clusters)
        if [ "$code" = "200" ]; then
            found=$(python3 - "$HUB_API_BODY" "$CLUSTER_NAME" <<'PY' 2>/dev/null
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    for c in d.get("clusters", []):
        if c.get("name") == sys.argv[2]:
            print(c.get("creds_source", "?"))
            break
except Exception:
    pass
PY
)
            [ -n "$found" ] && break
        fi
        sleep 10
    done
    if [ -z "$found" ]; then
        log_fail "spoke did not appear in the cluster list within 3 min — check the hub UI / PR state"
        return 1
    fi
    log_ok "spoke registered: creds_source=${found}"

    echo
    log_ok "spoke-connect complete"
    echo "       Next: $0 api-smoke      (the scripted API pass, podinfo included)"
    return 0
}

# =====================================================================
# Subcommand: api-smoke (V2-cleanup-62.3)
# =====================================================================
# A lean dedicated pass rather than a reuse of scripts/smoke.sh — grounded
# decision: smoke.sh is hard-wired to the kind dev env (sources
# scripts/lib/sharko-kube.sh, execs the CLI inside the kind pod, runs the
# Go e2e suite against localhost:8080). What 62.3 needs is API-shaped and
# hub-kubeconfig-shaped, so a purpose-built ~8-step pass is smaller than
# parameterizing all of that.
do_api_smoke() {
    local app_timeout="${EKS_TEST_APP_TIMEOUT:-600}"
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh api-smoke — scripted API pass against the HUB Sharko

Steps (each prints PASS/FAIL; the command exits non-zero on any failure):
  1. login to the hub Sharko
  2. GET  /providers          — aws-sm connected (zero-keys proof)
  3. GET  /clusters           — spoke present, creds_source=eks-token
  4. ensure podinfo in the catalog (POST /addons if missing, auto_merge)
  5. POST /clusters/${CLUSTER_NAME}/addons/podinfo (yes + auto_merge)
  6. poll the hub ArgoCD until podinfo-${CLUSTER_NAME} is Synced/Healthy
     (timeout \${EKS_TEST_APP_TIMEOUT:-600}s)
  7. POST /clusters/${CLUSTER_NAME}/test — success + every step passes
  8. GET  /fleet/status       — spoke present with connection fields

Usage: ./scripts/eks-live-test.sh api-smoke [--help]
EOF
                return 0
                ;;
        esac
    done

    hub_preflight_tools || return 1
    account_guard || return 1
    if ! hub_cluster_exists || [ ! -r "$HUB_KUBECONFIG_PATH" ]; then
        log_fail "hub cluster/kubeconfig not available — run '$0 hub-up' first."
        return 1
    fi

    local total=0 failed=0
    smoke_pass() { total=$((total+1)); printf '  %s %s\n' "${GREEN}[PASS]${RESET}" "$1"; }
    smoke_fail() {
        total=$((total+1)); failed=$((failed+1))
        printf '  %s %s\n' "${RED}[FAIL]${RESET}" "$1"
        if [ -n "${2:-}" ]; then
            printf '         %s\n' "$2"
        fi
    }

    echo "${BOLD}Sharko hub-on-EKS API smoke (V2-cleanup-62.3)${RESET}"
    echo "=============================================="
    echo "  hub:    ${HUB_CLUSTER_NAME} (${HUB_HOST})"
    echo "  spoke:  ${CLUSTER_NAME}"
    echo

    # ---- 1. login ----
    hub_api_init
    if hub_pf_start && hub_sharko_login; then
        smoke_pass "login as admin"
    else
        smoke_fail "login as admin"
        echo
        echo "  Result: ${RED}FAIL${RESET} (cannot continue without auth)"
        return 1
    fi

    local code

    # ---- 2. providers ----
    code=$(hub_api GET /api/v1/providers)
    local ptype pstatus
    ptype=$(json_field "$HUB_API_BODY" configured_provider.type)
    pstatus=$(json_field "$HUB_API_BODY" configured_provider.status)
    if [ "$code" = "200" ] && [ "$ptype" = "aws-sm" ] && [ "$pstatus" = "connected" ]; then
        smoke_pass "GET /providers — aws-sm connected (zero stored keys, Pod Identity)"
    else
        smoke_fail "GET /providers — ${code}, type='${ptype}', status='${pstatus}' (want aws-sm/connected)" \
            "$(json_field "$HUB_API_BODY" configured_provider.error)"
    fi

    # ---- 3. clusters ----
    code=$(hub_api GET /api/v1/clusters)
    local creds=""
    if [ "$code" = "200" ]; then
        creds=$(python3 - "$HUB_API_BODY" "$CLUSTER_NAME" <<'PY' 2>/dev/null
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    for c in d.get("clusters", []):
        if c.get("name") == sys.argv[2]:
            print(c.get("creds_source", ""))
            break
except Exception:
    pass
PY
)
    fi
    if [ "$creds" = "eks-token" ]; then
        smoke_pass "GET /clusters — '${CLUSTER_NAME}' present, creds_source=eks-token"
    else
        smoke_fail "GET /clusters — ${code}, '${CLUSTER_NAME}' creds_source='${creds:-missing}' (want eks-token)" \
            "run '$0 spoke-connect' if the spoke is not registered yet"
    fi

    # ---- 4. podinfo in catalog ----
    code=$(hub_api GET /api/v1/addons/list)
    local has_podinfo=""
    if [ "$code" = "200" ]; then
        has_podinfo=$(python3 - "$HUB_API_BODY" <<'PY' 2>/dev/null
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    print("yes" if any(a.get("name") == "podinfo" for a in d.get("applicationsets", [])) else "")
except Exception:
    pass
PY
)
    fi
    if [ "$has_podinfo" = "yes" ]; then
        smoke_pass "podinfo already in the catalog"
    else
        local payload
        payload=$(V="$PODINFO_VERSION" python3 -c '
import json, os
print(json.dumps({
    "name": "podinfo",
    "chart": "podinfo",
    "repo_url": "https://stefanprodan.github.io/podinfo",
    "version": os.environ["V"],
    "namespace": "podinfo",
    "auto_merge": True,
}))')
        code=$(hub_api POST /api/v1/addons "$payload")
        case "$code" in
            200|201|202)
                smoke_pass "POST /addons — podinfo ${PODINFO_VERSION} added to the catalog (auto_merge)"
                ;;
            409)
                smoke_pass "podinfo already in catalog — reusing"
                ;;
            *)
                if grep -qi "already" "$HUB_API_BODY" 2>/dev/null; then
                    smoke_pass "podinfo already in catalog — reusing"
                else
                    smoke_fail "POST /addons (podinfo) — ${code}" "$(head -c 200 "$HUB_API_BODY" 2>/dev/null || true)"
                fi
                ;;
        esac
    fi

    # ---- 5. enable podinfo on the spoke ----
    code=$(hub_api POST "/api/v1/clusters/${CLUSTER_NAME}/addons/podinfo" \
        '{"yes": true, "auto_merge": true}')
    local en_status
    en_status=$(json_field "$HUB_API_BODY" status)
    if [ "$code" = "200" ] && [ "$en_status" = "success" ]; then
        smoke_pass "POST /clusters/${CLUSTER_NAME}/addons/podinfo — status=success"
    elif grep -qi "already" "$HUB_API_BODY" 2>/dev/null; then
        smoke_pass "podinfo already enabled on '${CLUSTER_NAME}' (idempotent re-run)"
    else
        smoke_fail "enable podinfo — ${code}, status='${en_status}'" \
            "$(head -c 200 "$HUB_API_BODY" 2>/dev/null || true)"
    fi

    # ---- 6. poll ArgoCD for Synced/Healthy ----
    local app_name="podinfo-${CLUSTER_NAME}"
    log_info "polling hub ArgoCD for application '${app_name}' Synced/Healthy (timeout ${app_timeout}s)"
    local waited=0 app_state=""
    while [ "$waited" -lt "$app_timeout" ]; do
        app_state=$(hkctl get application "$app_name" -n argocd \
            -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null || true)
        if [ "$app_state" = "Synced/Healthy" ]; then
            break
        fi
        sleep 15
        waited=$((waited + 15))
    done
    if [ "$app_state" = "Synced/Healthy" ]; then
        smoke_pass "ArgoCD app '${app_name}' is Synced/Healthy (a real workload deployed to the spoke over eks-token)"
    else
        smoke_fail "ArgoCD app '${app_name}' is '${app_state:-not found}' after ${app_timeout}s" \
            "inspect: KUBECONFIG=${HUB_KUBECONFIG_PATH} kubectl get application -n argocd"
    fi

    # ---- 7. cluster test ----
    code=$(hub_api POST "/api/v1/clusters/${CLUSTER_NAME}/test" '{}')
    local t_success t_failed_steps
    t_success=$(json_field "$HUB_API_BODY" success)
    t_failed_steps=$(python3 - "$HUB_API_BODY" <<'PY' 2>/dev/null
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    bad = [s.get("name", "?") for s in (d.get("steps") or []) if s.get("status") == "fail"]
    print(",".join(bad))
except Exception:
    print("parse-error")
PY
)
    if [ "$code" = "200" ] && [ "$t_success" = "true" ] && [ -z "$t_failed_steps" ]; then
        smoke_pass "POST /clusters/${CLUSTER_NAME}/test — success, all steps pass"
    else
        smoke_fail "cluster test — ${code}, success='${t_success}', failing steps: ${t_failed_steps:-n/a}" \
            "$(json_field "$HUB_API_BODY" error_message)"
    fi

    # ---- 8. fleet status ----
    code=$(hub_api GET /api/v1/fleet/status)
    local fleet_conn=""
    if [ "$code" = "200" ]; then
        fleet_conn=$(python3 - "$HUB_API_BODY" "$CLUSTER_NAME" <<'PY' 2>/dev/null
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    for c in d.get("clusters", []):
        if c.get("name") == sys.argv[2]:
            print(c.get("connection_status", ""))
            break
except Exception:
    pass
PY
)
    fi
    if [ -n "$fleet_conn" ]; then
        smoke_pass "GET /fleet/status — '${CLUSTER_NAME}' present, connection_status=${fleet_conn} (creds_source=eks-token per step 3)"
    else
        smoke_fail "GET /fleet/status — ${code}, spoke missing or no connection_status field"
    fi

    # ---- summary ----
    echo
    echo "${BOLD}Summary${RESET}"
    if [ "$failed" -eq 0 ]; then
        echo "  Result: ${GREEN}PASS${RESET} ($((total - failed))/${total})"
        return 0
    fi
    echo "  Result: ${RED}FAIL${RESET} ($((total - failed))/${total} passed, ${failed} failed)"
    return 1
}

# =====================================================================
# Subcommand: env-up (V2-cleanup-62.3) — the ONE CLICK
# =====================================================================
do_env_up() {
    local yes=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh env-up — one click: the full hub-on-EKS simulation

Sequence: preflight -> spoke create (if missing) -> hub-up -> spoke-connect
-> api-smoke -> handover block (port-forwards, passwords, GIF shot-list,
running cost).

If the hub cluster already exists but namespace '${HUB_NAMESPACE}' is
missing (a prior hub-up died partway through), env-up detects the
half-provisioned hub and re-runs hub-up with --resume automatically —
no manual cleanup needed. If the namespace is present, the hub is treated
as fully provisioned and reused as-is.

From nothing this takes ~35-45 min (two EKS control planes are the slow
part) and the env costs ~\$0.26/hr while it runs. Run '$0 env-down' the
moment you are done — nothing auto-expires.

Required env: SHARKO_EKS_TEST_ACCOUNT_ID, SHARKO_GITHUB_TOKEN,
SHARKO_GITOPS_REPO_URL.

Flags:
  --yes     skip every confirmation prompt (true one-click)
  --help    this help

Usage: ./scripts/eks-live-test.sh env-up [--yes]
EOF
                return 0
                ;;
            --yes|-y) yes=1 ;;
        esac
    done

    hub_preflight_tools || return 1
    account_guard || return 1
    require_hub_env || return 1

    log_warn "env-up creates/uses TWO EKS clusters (~\$0.26/hr total) and takes ~35-45 min from nothing."
    confirm_or_abort "$yes" "Continue?" || return 1

    local -a passthru=()
    [ "$yes" = "1" ] && passthru+=(--yes)

    # spoke first — its role/secret are inputs to hub policy + registration.
    if cluster_exists; then
        log_ok "spoke '${CLUSTER_NAME}' already exists — reusing (phase-1 cluster)"
    else
        log_info "spoke '${CLUSTER_NAME}' missing — creating it first (~15-20 min)"
        do_create "${passthru[@]}" || return 1
    fi

    if hub_cluster_exists; then
        if hkctl get ns "$HUB_NAMESPACE" >/dev/null 2>&1; then
            log_ok "hub '${HUB_CLUSTER_NAME}' already exists and namespace '${HUB_NAMESPACE}' is present — reusing (fully provisioned)"
        else
            log_warn "hub '${HUB_CLUSTER_NAME}' exists but namespace '${HUB_NAMESPACE}' is missing — half-provisioned hub from a prior run, resuming"
            do_hub_up "${passthru[@]}" --resume || return 1
        fi
    else
        do_hub_up "${passthru[@]}" || return 1
    fi

    do_spoke_connect "${passthru[@]}" || return 1
    do_api_smoke || return 1

    # ---- handover block ----
    echo
    echo "${BOLD}=== Handover — your env is up ===${RESET}"
    echo
    echo "  ${BOLD}Sharko UI (hub)${RESET}"
    echo "    KUBECONFIG=${HUB_KUBECONFIG_PATH} kubectl port-forward -n ${HUB_NAMESPACE} svc/sharko ${HUB_LOCAL_PORT}:80"
    echo "    open ${HUB_HOST}   (login: admin)"
    echo "    password: KUBECONFIG=${HUB_KUBECONFIG_PATH} kubectl get secret sharko-initial-admin-secret -n ${HUB_NAMESPACE} -o jsonpath='{.data.password}' | base64 -d"
    echo
    echo "  ${BOLD}ArgoCD UI (hub)${RESET}"
    echo "    KUBECONFIG=${HUB_KUBECONFIG_PATH} kubectl port-forward -n argocd svc/argocd-server ${HUB_ARGOCD_LOCAL_PORT}:443"
    echo "    open https://localhost:${HUB_ARGOCD_LOCAL_PORT}   (login: admin)"
    echo "    password: KUBECONFIG=${HUB_KUBECONFIG_PATH} kubectl get secret argocd-initial-admin-secret -n argocd -o jsonpath='{.data.password}' | base64 -d"
    echo
    echo "  ${BOLD}State${RESET}"
    echo "    spoke '${CLUSTER_NAME}' registered over eks-token + per-cluster role_arn; podinfo Synced/Healthy"
    echo "    zero AWS keys stored anywhere on the hub (EKS Pod Identity end-to-end)"
    echo
    echo "  ${BOLD}GIF shot-list${RESET}"
    echo "    docs/site/developer-guide/eks-live-test-runbook.md — Part 3 has the ~30-60s sequence"
    echo
    echo "  ${BOLD}Money${RESET}"
    echo "    ~\$0.26/hr while this runs (2 control planes + 2 nodes). When done:"
    echo "    $0 env-down            # hub only"
    echo "    $0 env-down --all      # hub + spoke + role + SM secret (everything)"
    return 0
}

# =====================================================================
# Subcommand: env-down (V2-cleanup-62.3) — full teardown
# =====================================================================

# sweep_cluster_leftovers <cluster-name>: billing-leftover scan for one
# cluster (same pattern as teardown's inline scan). Echoes the number of
# leftover findings; prints loud LEFTOVER warnings for each.
sweep_cluster_leftovers() {
    local cname="$1"
    local leftovers=0

    local stacks
    stacks=$(aws cloudformation list-stacks --region "$REGION" \
        --stack-status-filter CREATE_COMPLETE UPDATE_COMPLETE ROLLBACK_COMPLETE DELETE_FAILED \
        --query "StackSummaries[?starts_with(StackName, 'eksctl-${cname}-')].StackName" \
        --output text 2>/dev/null || true)
    if [ -n "$stacks" ]; then
        log_warn "LEFTOVER: CloudFormation stack(s) still present for ${cname}: ${stacks}"
        leftovers=$((leftovers + 1))
    fi

    local tagged
    tagged=$(aws resourcegroupstaggingapi get-resources --region "$REGION" \
        --tag-filters "Key=sharko:cluster,Values=${cname}" \
        --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null || true)
    if [ -n "$tagged" ]; then
        log_warn "LEFTOVER: tagged resource(s) still present (sharko:cluster=${cname}): ${tagged}"
        leftovers=$((leftovers + 1))
    fi

    local eksctl_tagged
    eksctl_tagged=$(aws resourcegroupstaggingapi get-resources --region "$REGION" \
        --tag-filters "Key=alpha.eksctl.io/cluster-name,Values=${cname}" \
        --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null || true)
    if [ -n "$eksctl_tagged" ]; then
        log_warn "LEFTOVER: eksctl-tagged resource(s) still present for ${cname}: ${eksctl_tagged}"
        leftovers=$((leftovers + 1))
    fi

    if eksctl get cluster --name "$cname" --region "$REGION" >/dev/null 2>&1; then
        log_warn "LEFTOVER: cluster '${cname}' still shows up in 'eksctl get cluster'"
        leftovers=$((leftovers + 1))
    fi

    echo "$leftovers"
}

do_env_down() {
    local yes=0
    local all=0
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help)
                cat <<EOF
eks-live-test.sh env-down — full hub teardown (+ --all for everything)

Default (hub only), best-effort and idempotent:
  1. Pod Identity associations (sharko + 3 ArgoCD service accounts)
  2. The hub's access entry on the spoke (if the spoke still exists)
  3. eksctl delete cluster '${HUB_CLUSTER_NAME}' --wait
  4. The scoped inline policy + hub role '${HUB_ROLE_NAME}'
  5. The hub kubeconfig temp file + local port-forwards

With --all, ALSO tears down phase 1 (everything this harness ever made):
  6. The spoke cluster '${CLUSTER_NAME}' + phase-1 role (same as 'teardown')
  7. The AWS Secrets Manager secret '${CLUSTER_NAME}'
     (--force-delete-without-recovery — it only holds endpoint/CA data)

Ends with a leftover-billing sweep over BOTH clusters. Plain 'teardown'
keeps its spoke-only meaning — nothing breaks for phase-1 users.

Flags:
  --yes     skip confirmation prompt
  --all     also remove the spoke + phase-1 role + SM secret
  --help    this help

Usage: ./scripts/eks-live-test.sh env-down [--yes] [--all]
EOF
                return 0
                ;;
            --yes|-y) yes=1 ;;
            --all) all=1 ;;
        esac
    done

    account_guard || return 1

    local scope_msg="the HUB (cluster '${HUB_CLUSTER_NAME}' + role + pod identity)"
    if [ "$all" = "1" ]; then
        scope_msg="EVERYTHING: hub AND spoke clusters, both IAM roles, the SM secret"
    fi
    confirm_or_abort "$yes" \
        "This will DELETE ${scope_msg} in account ${SHARKO_EKS_TEST_ACCOUNT_ID}. Continue?" \
        || return 1

    local hub_role_arn
    hub_role_arn=$(aws iam get-role --role-name "$HUB_ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null || true)

    # ---- 1. pod identity associations ----
    if hub_cluster_exists; then
        log_info "deleting pod identity associations on '${HUB_CLUSTER_NAME}'"
        local ns_sa
        for ns_sa in \
            "${HUB_NAMESPACE}/sharko" \
            "argocd/argocd-application-controller" \
            "argocd/argocd-server" \
            "argocd/argocd-applicationset-controller"; do
            eksctl delete podidentityassociation \
                --cluster "$HUB_CLUSTER_NAME" --region "$REGION" \
                --namespace "${ns_sa%%/*}" \
                --service-account-name "${ns_sa#*/}" >/dev/null 2>&1 || true
        done
        log_ok "pod identity associations removed (best-effort)"
    fi

    # ---- 2. hub access entry on the spoke ----
    if [ -n "$hub_role_arn" ] && [ "$hub_role_arn" != "None" ] && cluster_exists; then
        log_info "deleting the hub role's access entry on '${CLUSTER_NAME}'"
        aws eks delete-access-entry --cluster-name "$CLUSTER_NAME" --region "$REGION" \
            --principal-arn "$hub_role_arn" >/dev/null 2>&1 || true
    fi

    # ---- 3. hub cluster ----
    if hub_cluster_exists; then
        log_info "eksctl delete cluster --name ${HUB_CLUSTER_NAME} --region ${REGION} --wait"
        if ! eksctl delete cluster --name "$HUB_CLUSTER_NAME" --region "$REGION" --wait; then
            log_fail "eksctl delete cluster (hub) failed — see output above; re-run env-down to retry."
        else
            log_ok "hub cluster deleted"
        fi
    else
        log_info "hub cluster '${HUB_CLUSTER_NAME}' not present (already torn down)"
    fi

    # ---- 4. hub role (inline policy first — a role with policies won't delete) ----
    if [ -n "$hub_role_arn" ] && [ "$hub_role_arn" != "None" ]; then
        log_info "deleting inline policy + hub IAM role '${HUB_ROLE_NAME}'"
        aws iam delete-role-policy --role-name "$HUB_ROLE_NAME" \
            --policy-name "$HUB_POLICY_NAME" >/dev/null 2>&1 || true
        if aws iam delete-role --role-name "$HUB_ROLE_NAME" >/dev/null 2>&1; then
            log_ok "hub IAM role deleted"
        else
            log_warn "could not delete IAM role '${HUB_ROLE_NAME}' — check manually"
        fi
    else
        log_info "hub IAM role '${HUB_ROLE_NAME}' not present (already torn down)"
    fi

    # ---- 5. local artifacts ----
    log_info "removing hub kubeconfig + killing hub port-forwards"
    rm -f "$HUB_KUBECONFIG_PATH"
    pkill -f "port-forward.*svc/sharko ${HUB_LOCAL_PORT}:" 2>/dev/null || true
    pkill -f "port-forward.*svc/argocd-server ${HUB_ARGOCD_LOCAL_PORT}:" 2>/dev/null || true

    # ---- 6+7. --all: spoke + role + SM secret ----
    if [ "$all" = "1" ]; then
        log_info "--all: tearing down the spoke (cluster + phase-1 role)"
        do_teardown --yes || true

        log_info "--all: deleting AWS Secrets Manager secret '${CLUSTER_NAME}'"
        if aws secretsmanager delete-secret --region "$REGION" \
            --secret-id "$CLUSTER_NAME" \
            --force-delete-without-recovery >/dev/null 2>&1; then
            log_ok "SM secret deleted"
        else
            log_info "SM secret '${CLUSTER_NAME}' not present (already deleted)"
        fi
    fi

    # ---- leftover sweep over both clusters ----
    echo
    log_info "scanning for anything still billing (both clusters)..."
    local leftovers=0 n
    n=$(sweep_cluster_leftovers "$HUB_CLUSTER_NAME")
    leftovers=$((leftovers + n))
    if [ "$all" = "1" ]; then
        n=$(sweep_cluster_leftovers "$CLUSTER_NAME")
        leftovers=$((leftovers + n))
        if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
            log_warn "LEFTOVER: IAM role '${ROLE_NAME}' still exists"
            leftovers=$((leftovers + 1))
        fi
        if aws secretsmanager describe-secret --region "$REGION" --secret-id "$CLUSTER_NAME" >/dev/null 2>&1; then
            log_warn "LEFTOVER: SM secret '${CLUSTER_NAME}' still exists"
            leftovers=$((leftovers + 1))
        fi
    fi
    if aws iam get-role --role-name "$HUB_ROLE_NAME" >/dev/null 2>&1; then
        log_warn "LEFTOVER: IAM role '${HUB_ROLE_NAME}' still exists"
        leftovers=$((leftovers + 1))
    fi

    echo
    if [ "$leftovers" -eq 0 ]; then
        log_ok "env-down complete — nothing left billing"
        if [ "$all" != "1" ] && cluster_exists; then
            log_info "the spoke '${CLUSTER_NAME}' is still running (~\$0.12/hr) — 'env-down --all' or 'teardown' removes it"
        fi
    else
        log_warn "env-down finished with ${leftovers} leftover warning(s) above — check the AWS console"
        return 1
    fi
    return 0
}

# =====================================================================
# Subcommand: selfcheck (hidden — maintainer/CI tripwire, not a live-test
# operation; deliberately NOT listed in usage() below)
#
# Guards the ascii_safe() rule above: no --description or --secret-string
# line in THIS file may ever contain a non-ASCII byte again. Run it any
# time after editing this script, or wire it into CI if a scripts-lint
# job is ever added.
# =====================================================================
do_selfcheck() {
    local self="${BASH_SOURCE[0]}"
    local bad
    bad=$(LC_ALL=C grep -nE '^[[:space:]]*--(description|secret-string)[[:space:]]+["$]' "$self" \
        | LC_ALL=C grep '[^ -~]' || true)
    if [ -n "$bad" ]; then
        log_fail "non-ASCII byte(s) found on a --description/--secret-string line:"
        printf '%s\n' "$bad" >&2
        echo "       Route the value through ascii_safe() before passing it to aws/eksctl." >&2
        return 1
    fi
    log_ok "selfcheck: no non-ASCII bytes on any --description/--secret-string line"
    return 0
}

# =====================================================================
# usage / help
# =====================================================================
usage() {
    cat <<EOF
${BOLD}Sharko EKS live-test harness${RESET}

Usage: ./scripts/eks-live-test.sh <subcommand> [flags]

${BOLD}Phase 1 — spoke only (hub on local kind)${RESET}
  preflight       Guard checks before spending any money (no AWS mutation)
  create          Create the throwaway spoke '${CLUSTER_NAME}' (~15-20 min)
  role-setup      Optional — throwaway IAM role + access entry (assume-role proof)
  token-check     Prove the eks-token path against the real cluster
  register-help   Exact values to paste into Sharko's Register Cluster UI
  teardown        Delete the SPOKE + role + leftover scan (spoke-only, as always)

${BOLD}Phase 2 — the full hub-on-EKS simulation (V2-cleanup-62.3)${RESET}
  hub-up          EKS hub: ArgoCD + Sharko (repo chart, ghcr image) + Pod
                  Identity — zero stored AWS keys (--resume: finish
                  provisioning a half-provisioned hub instead of refusing)
  spoke-connect   Wire the spoke into the hub Sharko via API (eks-token +
                  per-cluster role_arn)
  api-smoke       Scripted API pass (podinfo end-to-end) — PASS/FAIL per step
  env-up          ONE CLICK: preflight -> spoke -> hub -> connect -> smoke
                  (auto-resumes a half-provisioned hub if found)
  env-down        Full hub teardown; --all also removes spoke + role + secret

${BOLD}Either phase${RESET}
  status          Both clusters' state + combined rough running cost

${BOLD}Help${RESET}
  help            this message
  <subcmd> --help per-subcommand help

${BOLD}Required env vars${RESET}
  SHARKO_EKS_TEST_ACCOUNT_ID   your AWS account ID — REQUIRED everywhere,
                                no default
  SHARKO_GITHUB_TOKEN          REQUIRED by hub-up/env-up (gitops repo token)
  SHARKO_GITOPS_REPO_URL       REQUIRED by hub-up/env-up (gitops repo URL)

${BOLD}Configuration${RESET} (env vars; defaults shown)
  EKS_TEST_CLUSTER_NAME       ${CLUSTER_NAME}
  EKS_TEST_HUB_CLUSTER_NAME   ${HUB_CLUSTER_NAME}
  EKS_TEST_REGION             ${REGION}
  EKS_TEST_NODE_TYPE          ${NODE_TYPE}
  EKS_TEST_HUB_NODE_TYPE      ${HUB_NODE_TYPE}
  EKS_TEST_KUBECONFIG         ${KUBECONFIG_PATH}
  EKS_TEST_HUB_KUBECONFIG     ${HUB_KUBECONFIG_PATH}
  SHARKO_EKS_TEST_ROLE_NAME   ${ROLE_NAME}
  SHARKO_EKS_HUB_ROLE_NAME    ${HUB_ROLE_NAME}
  EKS_TEST_SHARKO_IMAGE_TAG   ${SHARKO_IMAGE_TAG}
  EKS_TEST_HUB_SHARKO_PORT    ${HUB_LOCAL_PORT}
  EKS_TEST_HUB_ARGOCD_PORT    ${HUB_ARGOCD_LOCAL_PORT}
  EKS_TEST_PODINFO_VERSION    ${PODINFO_VERSION}
EOF
}

# =====================================================================
# Dispatcher
# =====================================================================
main() {
    local subcmd="${1:-help}"
    case "$subcmd" in
        preflight)
            shift; do_preflight "$@"; return $?
            ;;
        create)
            shift; do_create "$@"; return $?
            ;;
        role-setup)
            shift; do_role_setup "$@"; return $?
            ;;
        token-check)
            shift; do_token_check "$@"; return $?
            ;;
        register-help)
            shift; do_register_help "$@"; return $?
            ;;
        teardown)
            shift; do_teardown "$@"; return $?
            ;;
        hub-up)
            shift; do_hub_up "$@"; return $?
            ;;
        spoke-connect)
            shift; do_spoke_connect "$@"; return $?
            ;;
        api-smoke)
            shift; do_api_smoke "$@"; return $?
            ;;
        env-up)
            shift; do_env_up "$@"; return $?
            ;;
        env-down)
            shift; do_env_down "$@"; return $?
            ;;
        status)
            shift; do_status "$@"; return $?
            ;;
        selfcheck)
            shift; do_selfcheck "$@"; return $?
            ;;
        help|--help|-h|"")
            usage
            return 0
            ;;
        *)
            log_fail "unknown subcommand: $subcmd"
            echo >&2
            usage >&2
            return 1
            ;;
    esac
}

main "$@"
