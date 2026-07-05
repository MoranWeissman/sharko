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
# SUBCOMMANDS
#   preflight       tool + account + profile guard checks (no AWS mutation)
#   create          eksctl create cluster (one nodegroup, one small node)
#   role-setup      throwaway IAM role + EKS access entry (assume-role proof)
#   token-check     aws eks get-token + live connectivity check
#   register-help   exact values to paste into Sharko's Register Cluster UI
#   teardown        eksctl delete cluster + role/access-entry + leftover scan
#   status          cluster state + running cost estimate
#
# ENV VARS (override defaults)
#   SHARKO_EKS_TEST_ACCOUNT_ID   REQUIRED. Your AWS account ID. No default —
#                                the script refuses to run without it.
#   EKS_TEST_CLUSTER_NAME        default: sharko-eks-live-test
#   EKS_TEST_REGION              default: eu-west-1
#   EKS_TEST_NODE_TYPE           default: t3.small
#   EKS_TEST_K8S_VERSION         default: "" (let eksctl pick its current default)
#   EKS_TEST_KUBECONFIG          default: ${TMPDIR:-/tmp}/<cluster-name>.kubeconfig
#   SHARKO_EKS_TEST_ROLE_NAME    default: sharko-eks-live-test-role
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

# Rough, non-authoritative cost inputs — for the "this spends real money"
# reminder and the `status` estimate only. Check AWS Billing for the truth.
EKS_CONTROL_PLANE_HOURLY="0.10"
NODE_HOURLY_ESTIMATE="0.02"   # t3.small on-demand, eu-west-1, approximate

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
  1. aws iam create-role (trust policy: your account root may assume it)
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

    if [ -n "$role_arn" ] && [ "$role_arn" != "None" ]; then
        log_info "role '${ROLE_NAME}' already exists — reusing"
    else
        log_info "creating IAM role '${ROLE_NAME}'"
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
      "Action": "sts:AssumeRole"
    }
  ]
}
EOF
        # Trust policy is intentionally broad (whole account root) — this is
        # a throwaway, single-account personal test, not a production role.
        if ! aws iam create-role \
            --role-name "$ROLE_NAME" \
            --assume-role-policy-document "file://${trust_file}" \
            --description "Sharko EKS live-test throwaway role (V2-cleanup-62.1) — safe to delete any time" \
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
  Role ARN (optional):       LEAVE BLANK — see "known gap" below, this field
                              currently has zero effect no matter what you type
  Secret Path (optional):    leave blank (defaults to the cluster name, which
                              matches the secret you just created above)

${BOLD}Leg 2 — prove the assume-role hop${RESET}
  1. Run: $0 role-setup                 (creates the throwaway role + access entry)
  2. Put the printed role ARN into the SECRET's "roleArn" field (see above) —
     NOT into the UI's Role ARN field (it does nothing, see below).
  3. In Sharko, click "Test cluster" (or trigger any addon deploy) again.
     Sharko re-fetches the secret on every credential resolution, so this
     now exercises the assume-role hop for real, through Sharko's own code.

${BOLD}Known gap — report this, do not fix it here${RESET}
  The Register Cluster dialog's "Role ARN" field (ui/src/views/ClustersOverview.tsx,
  sent as JSON key "role_arn") has NO matching field in the backend's
  RegisterClusterRequest struct (internal/orchestrator/types.go). Go's
  json.Decode silently drops unknown fields, so whatever the maintainer types
  into that box on registration is thrown away — it is never read, never
  stored, never used. The only per-cluster role ARN that actually takes
  effect is the "roleArn" key inside the AWS Secrets Manager secret payload
  itself (see Leg 2 above). This is a real UI/backend mismatch, not a
  live-test artifact — flag it to the tech lead / product owner.
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
eks-live-test.sh teardown — full teardown + leftover-billing scan

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
eks-live-test.sh status — cluster state + rough running-cost estimate

Reports whether '${CLUSTER_NAME}' exists, its node state, and a rough
(non-authoritative) running-cost estimate based on cluster age. Check AWS
Billing for the real number.

Usage: ./scripts/eks-live-test.sh status [--help]
EOF
                return 0
                ;;
        esac
    done

    account_guard || return 1

    echo "${BOLD}EKS live-test cluster status${RESET}"
    echo "=============================="
    echo "  cluster name:   ${CLUSTER_NAME}"
    echo "  region:         ${REGION}"

    if ! cluster_exists; then
        printf '  cluster:        %snot found%s\n' "$RED" "$RESET"
        echo
        echo "  Run: $0 create   to create it."
        return 1
    fi
    printf '  cluster:        %sexists%s\n' "$GREEN" "$RESET"

    local created_at
    created_at=$(aws eks describe-cluster --name "$CLUSTER_NAME" --region "$REGION" \
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
            cost=$(awk -v h="$hours" -v cp="$EKS_CONTROL_PLANE_HOURLY" -v node="$NODE_HOURLY_ESTIMATE" \
                'BEGIN { printf "%.2f", h * (cp + node) }')
            echo "  running for:    ~${hours} hour(s)"
            echo "  rough cost:     ~\$${cost} (control plane + 1 node — NOT authoritative, check AWS Billing)"
        fi
    fi

    if [ -r "$KUBECONFIG_PATH" ]; then
        local node_line
        node_line=$(kubectl --kubeconfig="$KUBECONFIG_PATH" get nodes --no-headers --request-timeout=10s 2>/dev/null)
        if [ -n "$node_line" ]; then
            local ready_count total_count
            total_count=$(printf '%s\n' "$node_line" | grep -c .)
            ready_count=$(printf '%s\n' "$node_line" | grep -c ' Ready ')
            echo "  nodes:          ${ready_count}/${total_count} Ready"
        else
            printf '  nodes:          %sunreachable via %s%s\n' "$YELLOW" "$KUBECONFIG_PATH" "$RESET"
        fi
    else
        printf '  nodes:          %skubeconfig not found at %s%s\n' "$YELLOW" "$KUBECONFIG_PATH" "$RESET"
    fi

    if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
        echo "  role-setup:     done (${ROLE_NAME} exists)"
    else
        echo "  role-setup:     not run"
    fi

    echo
    return 0
}

# =====================================================================
# usage / help
# =====================================================================
usage() {
    cat <<EOF
${BOLD}Sharko EKS live-test harness${RESET}

Usage: ./scripts/eks-live-test.sh <subcommand> [flags]

${BOLD}Lifecycle${RESET}
  preflight       Guard checks before spending any money (no AWS mutation)
  create          Create the throwaway cluster '${CLUSTER_NAME}' (~15-20 min)
  role-setup      Optional — throwaway IAM role + access entry (assume-role proof)
  token-check     Prove the eks-token path against the real cluster
  register-help   Exact values to paste into Sharko's Register Cluster UI
  teardown        Delete everything + scan for anything still billing
  status          Cluster state + rough running-cost estimate

${BOLD}Help${RESET}
  help            this message
  <subcmd> --help per-subcommand help

${BOLD}Required env var${RESET}
  SHARKO_EKS_TEST_ACCOUNT_ID   your AWS account ID — REQUIRED, no default,
                                every AWS-touching subcommand refuses without it

${BOLD}Configuration${RESET} (env vars; defaults shown)
  EKS_TEST_CLUSTER_NAME    ${CLUSTER_NAME}
  EKS_TEST_REGION          ${REGION}
  EKS_TEST_NODE_TYPE       ${NODE_TYPE}
  EKS_TEST_KUBECONFIG      ${KUBECONFIG_PATH}
  SHARKO_EKS_TEST_ROLE_NAME ${ROLE_NAME}
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
        status)
            shift; do_status "$@"; return $?
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
