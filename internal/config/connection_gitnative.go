package config

import (
	"log/slog"
	"os"

	"github.com/MoranWeissman/sharko/internal/models"
)

// V3 C2: connection config is now git-native for its NON-SECRET fields.
//
// Like V3 C1 (server settings), Helm/git-declared env vars are authoritative
// (git wins) for the fields they declare, and Sharko reconciles the stored
// connection toward those env values — a runtime UI edit to a declared field
// is reclaimed on the next reconcile. Fields that are NOT env-declared keep
// their runtime value (API authoritative, back-compat), exactly like C1's
// undeclared-key behavior.
//
// The whole Connection is persisted as ONE encrypted JSON blob in the
// sharko-connections Secret, so "git wins on the non-secret fields" is a
// FIELD-LEVEL MERGE, not a blob overwrite: the declared non-secret fields of
// the ACTIVE/default connection are overwritten from env, while the encrypted
// SECRET material — git Token/PAT and ArgoCD Token — is PRESERVED untouched.
// Secret material is NEVER sourced from these git-native env vars and NEVER
// rendered into values.yaml or a ConfigMap; it stays in the encrypted Secret,
// entered once via the UI.
//
// Scope is the ACTIVE (or default) connection ONLY. Sharko holds a LIST of
// connections; "Helm declares the connection" only coheres for the single
// active one. When there is no active connection yet (fresh install, token
// not entered), reconcile is a no-op — there is nothing to merge onto, and we
// never fabricate a half-connection with no credentials.
//
// Env keys (all optional; unset ⇒ that field is undeclared ⇒ runtime value
// persists). Bool values that are neither "true" nor "false" are treated as
// undeclared with a slog.Warn (lenient — never crash boot on a typo).
const (
	envConnGitProvider     = "SHARKO_CONN_GIT_PROVIDER"
	envConnGitRepoURL      = "SHARKO_CONN_GIT_REPO_URL"
	envConnGitOwner        = "SHARKO_CONN_GIT_OWNER"
	envConnGitRepo         = "SHARKO_CONN_GIT_REPO"
	envConnGitOrganization = "SHARKO_CONN_GIT_ORGANIZATION"
	envConnGitProject      = "SHARKO_CONN_GIT_PROJECT"
	envConnGitRepository   = "SHARKO_CONN_GIT_REPOSITORY"

	envConnArgocdServerURL = "SHARKO_CONN_ARGOCD_SERVER_URL"
	envConnArgocdNamespace = "SHARKO_CONN_ARGOCD_NAMESPACE"
	envConnArgocdInsecure  = "SHARKO_CONN_ARGOCD_INSECURE"

	envConnProviderType      = "SHARKO_CONN_PROVIDER_TYPE"
	envConnProviderRegion    = "SHARKO_CONN_PROVIDER_REGION"
	envConnProviderPrefix    = "SHARKO_CONN_PROVIDER_PREFIX"
	envConnProviderNamespace = "SHARKO_CONN_PROVIDER_NAMESPACE"
	envConnProviderRoleARN   = "SHARKO_CONN_PROVIDER_ROLE_ARN"

	envConnAddonSecretProviderType      = "SHARKO_CONN_ADDON_SECRET_PROVIDER_TYPE"
	envConnAddonSecretProviderRegion    = "SHARKO_CONN_ADDON_SECRET_PROVIDER_REGION"
	envConnAddonSecretProviderPrefix    = "SHARKO_CONN_ADDON_SECRET_PROVIDER_PREFIX"
	envConnAddonSecretProviderNamespace = "SHARKO_CONN_ADDON_SECRET_PROVIDER_NAMESPACE"
	envConnAddonSecretProviderRoleARN   = "SHARKO_CONN_ADDON_SECRET_PROVIDER_ROLE_ARN"

	envConnGitOpsBaseBranch      = "SHARKO_CONN_GITOPS_BASE_BRANCH"
	envConnGitOpsBranchPrefix    = "SHARKO_CONN_GITOPS_BRANCH_PREFIX"
	envConnGitOpsCommitPrefix    = "SHARKO_CONN_GITOPS_COMMIT_PREFIX"
	envConnGitOpsPRAutoMerge     = "SHARKO_CONN_GITOPS_PR_AUTO_MERGE"
	envConnGitOpsHostClusterName = "SHARKO_CONN_GITOPS_HOST_CLUSTER_NAME"
	envConnGitOpsDefaultAddons   = "SHARKO_CONN_GITOPS_DEFAULT_ADDONS"
)

// desiredStringFromEnv returns (value, declared). An unset/empty env var is
// undeclared — the caller keeps the runtime value.
func desiredStringFromEnv(key string) (string, bool) {
	raw := os.Getenv(key)
	if raw == "" {
		return "", false
	}
	return raw, true
}

// desiredBoolFromEnv returns (value, declared). Non-"true"/"false" values are
// treated as undeclared with a warning — lenient, never crash on a typo.
func desiredBoolFromEnv(key string) (bool, bool) {
	raw := os.Getenv(key)
	if raw == "" {
		return false, false
	}
	switch raw {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		slog.Warn("malformed bool in connection git-native env, treating as undeclared",
			"env_key", key, "value", raw)
		return false, false
	}
}

// setString overwrites *dst with the env-declared value when declared and
// different, flagging change. Preserves the runtime value when undeclared.
func setString(dst *string, env string, changed *bool) {
	if v, declared := desiredStringFromEnv(env); declared && *dst != v {
		*dst = v
		*changed = true
	}
}

// mergeProviderFromEnv applies declared non-secret provider fields onto pc.
// Returns the (possibly newly allocated) provider pointer and whether the set
// of declared fields is non-empty. The pointer is allocated lazily so an
// undeclared provider block stays nil (back-compat).
//
// M2 FIX: Require `type` when ANY provider field is declared. If `type` is
// empty but other fields are set, warn and SKIP the provider block (lenient —
// per the git-native malformed-value contract: warn + fall back, never persist
// garbage, never crash boot).
func mergeProviderFromEnv(pc *models.ProviderConfig, typeEnv, regionEnv, prefixEnv, namespaceEnv, roleARNEnv string, changed *bool) *models.ProviderConfig {
	typeVal, tD := desiredStringFromEnv(typeEnv)
	_, rD := desiredStringFromEnv(regionEnv)
	_, pD := desiredStringFromEnv(prefixEnv)
	_, nD := desiredStringFromEnv(namespaceEnv)
	_, aD := desiredStringFromEnv(roleARNEnv)
	if !tD && !rD && !pD && !nD && !aD {
		return pc // nothing declared — leave as-is (possibly nil)
	}
	// M2: Require type when any field is declared
	if !tD || typeVal == "" {
		// Partial provider block — warn with field names, skip entirely
		var declaredFields []string
		if rD {
			declaredFields = append(declaredFields, "region")
		}
		if pD {
			declaredFields = append(declaredFields, "prefix")
		}
		if nD {
			declaredFields = append(declaredFields, "namespace")
		}
		if aD {
			declaredFields = append(declaredFields, "role_arn")
		}
		if len(declaredFields) > 0 {
			slog.Warn("partial provider block in connection git-native env (type missing), skipping provider merge",
				"declared_fields", declaredFields)
		}
		return pc // skip — don't persist a typeless provider
	}
	if pc == nil {
		pc = &models.ProviderConfig{}
		*changed = true
	}
	setString(&pc.Type, typeEnv, changed)
	setString(&pc.Region, regionEnv, changed)
	setString(&pc.Prefix, prefixEnv, changed)
	setString(&pc.Namespace, namespaceEnv, changed)
	setString(&pc.RoleARN, roleARNEnv, changed)
	return pc
}

// MergeConnectionFromEnv applies the git-declared NON-SECRET env fields onto
// conn in place, PRESERVING the encrypted secret material (Git.Token, Git.PAT,
// Argocd.Token). Returns true when any field changed (so the caller can decide
// whether to persist). Pure and side-effect-free apart from mutating conn —
// safe to unit-test without a Store.
//
// SECURITY: this function never reads a git token, PAT, or ArgoCD token from
// env, and never writes those fields. The secret fields on conn are left
// exactly as they were loaded (decrypted) from the Secret, so the subsequent
// re-encrypt on save round-trips them unchanged.
func MergeConnectionFromEnv(conn *models.Connection) bool {
	if conn == nil {
		return false
	}
	changed := false

	// --- GitRepoConfig (non-secret fields only; Token/PAT preserved) ---
	if v, declared := desiredStringFromEnv(envConnGitProvider); declared {
		if string(conn.Git.Provider) != v {
			conn.Git.Provider = models.GitProviderType(v)
			changed = true
		}
	}
	setString(&conn.Git.RepoURL, envConnGitRepoURL, &changed)
	setString(&conn.Git.Owner, envConnGitOwner, &changed)
	setString(&conn.Git.Repo, envConnGitRepo, &changed)
	setString(&conn.Git.Organization, envConnGitOrganization, &changed)
	setString(&conn.Git.Project, envConnGitProject, &changed)
	setString(&conn.Git.Repository, envConnGitRepository, &changed)

	// --- ArgocdConfig (non-secret; Token preserved) ---
	setString(&conn.Argocd.ServerURL, envConnArgocdServerURL, &changed)
	setString(&conn.Argocd.Namespace, envConnArgocdNamespace, &changed)
	if v, declared := desiredBoolFromEnv(envConnArgocdInsecure); declared && conn.Argocd.Insecure != v {
		conn.Argocd.Insecure = v
		changed = true
	}

	// --- Cluster-test provider (all fields non-secret; role_arn is an
	// identifier, not a credential) ---
	conn.Provider = mergeProviderFromEnv(conn.Provider,
		envConnProviderType, envConnProviderRegion, envConnProviderPrefix,
		envConnProviderNamespace, envConnProviderRoleARN, &changed)

	// --- Addon-secret provider (separate backend, non-secret fields) ---
	conn.AddonSecretProvider = mergeProviderFromEnv(conn.AddonSecretProvider,
		envConnAddonSecretProviderType, envConnAddonSecretProviderRegion, envConnAddonSecretProviderPrefix,
		envConnAddonSecretProviderNamespace, envConnAddonSecretProviderRoleARN, &changed)

	// --- GitOps settings ---
	_, bbD := desiredStringFromEnv(envConnGitOpsBaseBranch)
	_, bpD := desiredStringFromEnv(envConnGitOpsBranchPrefix)
	_, cpD := desiredStringFromEnv(envConnGitOpsCommitPrefix)
	_, hcD := desiredStringFromEnv(envConnGitOpsHostClusterName)
	_, daD := desiredStringFromEnv(envConnGitOpsDefaultAddons)
	pam, pamD := desiredBoolFromEnv(envConnGitOpsPRAutoMerge)
	if bbD || bpD || cpD || hcD || daD || pamD {
		if conn.GitOps == nil {
			conn.GitOps = &models.GitOpsSettings{}
			changed = true
		}
		setString(&conn.GitOps.BaseBranch, envConnGitOpsBaseBranch, &changed)
		setString(&conn.GitOps.BranchPrefix, envConnGitOpsBranchPrefix, &changed)
		setString(&conn.GitOps.CommitPrefix, envConnGitOpsCommitPrefix, &changed)
		setString(&conn.GitOps.HostClusterName, envConnGitOpsHostClusterName, &changed)
		setString(&conn.GitOps.DefaultAddons, envConnGitOpsDefaultAddons, &changed)
		if pamD {
			if conn.GitOps.PRAutoMerge == nil || *conn.GitOps.PRAutoMerge != pam {
				v := pam
				conn.GitOps.PRAutoMerge = &v
				changed = true
			}
		}
	}

	return changed
}

// ReconcileConnectionFromEnv reconciles the ACTIVE/default connection toward
// the git-declared non-secret env fields (git wins), preserving encrypted
// secret material. Returns (changed, err): changed is true when the stored
// connection was updated (so the caller can re-run ReinitializeFromConnection
// to pick up the merged live config).
//
// No-op (changed=false, nil) when no connection is active — there is nothing
// to merge onto, and Sharko never fabricates a connection without credentials.
// A read error is returned so the caller can log it; a settings typo is NOT an
// error (handled leniently inside MergeConnectionFromEnv).
//
// M1 FIX: The merge is now atomic via MergeConnectionFromEnvAtomic, so a
// token rotated via the UI during this reconcile is NOT clobbered. The merge
// operates on the freshest connection state (loaded inside the store's lock),
// not on a stale earlier Get.
func ReconcileConnectionFromEnv(store Store) (bool, error) {
	if store == nil {
		return false, nil
	}
	activeName, err := store.GetActiveConnection()
	if err != nil {
		return false, err
	}
	if activeName == "" {
		return false, nil // no active connection — nothing to reconcile
	}

	// M1 FIX: Use the atomic merge method so the non-secret env fields are
	// merged onto the FRESH connection load inside the store's lock, not onto
	// a stale copy from an earlier Get. This prevents the lost-update race
	// where a UI token rotation lands between our Get and our Save.
	changed, err := store.MergeConnectionFromEnvAtomic(activeName)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil // already converged — no Secret write, no churn
	}

	slog.Info("connection reconciled toward git-declared env values (git wins on non-secret fields)",
		"connection", activeName)
	return true, nil
}
