# shellcheck shell=bash
#
# scripts/lib/sharko-kube.sh — shared kind-kubeconfig targeting for the dev loop
#
# Single source of truth for "talk to the kind cluster, never the user's current
# kubectl context". The whole dev loop spans several scripts (sharko-dev.sh,
# dev-rebuild.sh, smoke.sh); they all `source` this file so every cluster-
# operating call goes through one kubeconfig that points ONLY at
# kind-${KIND_CLUSTER_NAME}. The user's current kubectl context is never read
# and never mutated — we do NOT run `kubectl config use-context`.
#
# WHAT IT PROVIDES
#   ensure_kind_kubeconfig   resolve SHARKO_KIND_KUBECONFIG (once per process)
#   kctl                     kubectl forced onto the kind kubeconfig
#   khelm                    helm forced onto the kind kubeconfig
#   kind_cluster_exists      0 if kind-${KIND_CLUSTER_NAME} is present
#
# ENV VARS (honored verbatim if already set)
#   KIND_CLUSTER_NAME        default: sharko-e2e
#   SHARKO_KIND_KUBECONFIG   path to a kubeconfig for the target kind cluster.
#                            If you export it, we honor it verbatim and NEVER
#                            regenerate or delete it. Leave unset and we derive
#                            it from `kind get kubeconfig --name <cluster>` into
#                            a per-run temp file we own and clean up on exit.
#
# SOURCING MODEL
#   . "${SCRIPT_DIR}/lib/sharko-kube.sh"
#   Defining functions only — no side effects on source (we do NOT call
#   ensure_kind_kubeconfig here). Safe to source multiple times. Pure
#   POSIX-bash; needs only kind/kubectl/helm at call time.

# ---- double-source guard ----
[ -n "${_SHARKO_KUBE_LIB_LOADED:-}" ] && return 0
_SHARKO_KUBE_LIB_LOADED=1

# ---- defaults (honored verbatim if the caller already set them) ----
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-sharko-e2e}"

# SHARKO_KIND_KUBECONFIG holds the resolved path. If the user already exported it
# we honor it verbatim (and never delete it on exit). Otherwise
# ensure_kind_kubeconfig fills it from `kind get kubeconfig` into a per-run temp
# file that we own and clean up on exit.
SHARKO_KIND_KUBECONFIG="${SHARKO_KIND_KUBECONFIG:-}"
# Only a path WE create gets auto-removed on exit; a user-provided override does not.
_SHARKO_KIND_KC_OWNED="${_SHARKO_KIND_KC_OWNED:-}"

# _sharko_kube_cleanup: EXIT trap — remove the kubeconfig temp file we created.
# Safe to call multiple times; only touches a file this process owns. Never
# deletes a user-provided SHARKO_KIND_KUBECONFIG.
_sharko_kube_cleanup() {
    [ -n "${_SHARKO_KIND_KC_OWNED}" ] && rm -f "${_SHARKO_KIND_KC_OWNED}" 2>/dev/null
}
trap _sharko_kube_cleanup EXIT

# kind_cluster_exists: 0 if the configured cluster is present, 1 otherwise.
kind_cluster_exists() {
    kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"
}

# ensure_kind_kubeconfig: make SHARKO_KIND_KUBECONFIG point at a kubeconfig for
# kind-${KIND_CLUSTER_NAME}. Runs at most once per process (cached path).
#
#   - If SHARKO_KIND_KUBECONFIG is already non-empty (user override or a previous
#     call in this process), honor it verbatim and return 0 (we do NOT regenerate
#     or delete a user-provided path).
#   - Otherwise run `kind get kubeconfig --name ${KIND_CLUSTER_NAME}` into a
#     per-run temp file (mode 0600), cache its path, and mark it for cleanup.
#   - If the kind cluster is missing / kubeconfig fetch fails, fail with the
#     standard "not found" message on stderr and return non-zero.
#
# Every cluster-operating call should run this before its first kctl()/khelm().
ensure_kind_kubeconfig() {
    # Already resolved (user override or a previous call in this process)?
    if [ -n "${SHARKO_KIND_KUBECONFIG}" ]; then
        return 0
    fi

    if ! kind_cluster_exists; then
        printf '[FAIL] kind cluster '\''%s'\'' not found — run: ./scripts/sharko-dev.sh up\n' \
            "${KIND_CLUSTER_NAME}" >&2
        return 1
    fi

    local kc="${TMPDIR:-/tmp}/sharko-dev-kc-${KIND_CLUSTER_NAME}.$$.yaml"
    if ! kind get kubeconfig --name "${KIND_CLUSTER_NAME}" > "$kc" 2>/dev/null; then
        rm -f "$kc" 2>/dev/null
        printf '[FAIL] kind cluster '\''%s'\'' not found — run: ./scripts/sharko-dev.sh up\n' \
            "${KIND_CLUSTER_NAME}" >&2
        return 1
    fi
    chmod 600 "$kc" 2>/dev/null || true

    SHARKO_KIND_KUBECONFIG="$kc"
    export SHARKO_KIND_KUBECONFIG
    _SHARKO_KIND_KC_OWNED="$kc"   # we created it → clean up on exit
    return 0
}

# kctl: run kubectl against the target kind cluster's own kubeconfig, ignoring
# whatever context the user is currently on. Requires ensure_kind_kubeconfig to
# have run first. Never switches the user's current context.
kctl() {
    KUBECONFIG="${SHARKO_KIND_KUBECONFIG}" kubectl "$@"
}

# khelm: run helm against the target kind cluster's own kubeconfig, ignoring
# whatever context the user is currently on. Mirrors kctl() for the cluster-
# operating helm calls. Requires ensure_kind_kubeconfig to have run first.
# Never switches the user's current context.
khelm() {
    KUBECONFIG="${SHARKO_KIND_KUBECONFIG}" helm "$@"
}
