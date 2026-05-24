package providers

// Three sibling ProviderConfig types — addon-secret material, cluster-
// test connectivity, and cluster-registration sink. Each addresses
// exactly one mechanism so the compiler enforces the separation that
// field-level overload could not.
//
// Canonical factories: NewAddonSecretProvider(AddonSecretProviderConfig)
// and NewClusterTestProvider(ClusterTestProviderConfig). The
// ClusterRegistrationSourceConfig is reserved for the cluster
// reconciler wiring.

// AddonSecretProviderConfig configures the backend that supplies addon-secret
// material for credential rotation + tiered git flow. ONE OF the configured
// backend types reads/decrypts secrets and pushes them into Sharko-managed
// remote-cluster K8s Secrets via the secrets reconciler.
//
// Mechanism scope: addon-secrets ONLY. Does NOT serve cluster-connectivity
// test creds (use ClusterTestProviderConfig) nor cluster-registration
// sink config (use ClusterRegistrationSourceConfig). Field semantics
// intentionally do NOT overlap with the other two configs.
type AddonSecretProviderConfig struct {
	// Type selects the backend. Valid values:
	//   "vault"                          — HashiCorp Vault (future; current code has no vault factory)
	//   "aws-sm" / "aws-secrets-manager" — AWS Secrets Manager
	//   "k8s-secrets" / "kubernetes"     — Kubernetes Secrets in the configured Namespace
	//   "gcp-sm" / "google-secret-manager" — GCP Secret Manager (stub today; not implemented)
	//   "azure-kv" / "azure-key-vault"   — Azure Key Vault (stub today; not implemented)
	//   ""                                — no addon-secret backend configured (reconciler no-ops)
	// Note: "argocd" is REJECTED — it's a cluster-creds backend, not an addon-secret backend.
	Type string

	// Namespace is the K8s namespace where the "k8s-secrets" backend reads/
	// writes addon-secret K8s Secrets. Default "sharko". Used ONLY by the
	// "k8s-secrets" / "kubernetes" backend; ignored by all other backends.
	Namespace string

	// Region is the AWS region for the "aws-sm" / "aws-secrets-manager"
	// backend. Used ONLY by aws-sm; ignored by all other backends.
	Region string

	// Prefix is an optional secret-name prefix for the "aws-sm" backend
	// (e.g. "clusters/" → fully-qualified secret name "clusters/<name>").
	// Used ONLY by aws-sm; ignored by all other backends.
	Prefix string

	// RoleARN is the default IAM role to assume for STS-based EKS token
	// generation when an aws-sm structured-EKS secret omits its own roleArn.
	// Used ONLY by aws-sm; ignored by all other backends.
	RoleARN string
}

// ClusterTestProviderConfig configures the backend that resolves cluster
// CONNECTIVITY credentials — i.e. the kubeconfig used to verify a target
// cluster is reachable + auth works + RBAC is sufficient (the POST
// /api/v1/clusters/{name}/test surface, the dashboard "Verified" state).
//
// Scope: argocd-only. Sharko's canonical cluster-connectivity backend
// reads ArgoCD's cluster Secret in the argocd namespace (the same
// secret the reconciler writes). Legacy aws-sm / k8s-secrets / gcp-sm /
// azure-kv cluster-credentials backends are retired here; addon-secret
// consumers of those backends remain via NewAddonSecretProvider.
//
// Future: IAMRoleARN (EKS IAM token minting), ExecPluginAllowList
// (when/if exec-plugin auth becomes supported).
type ClusterTestProviderConfig struct {
	// Type selects the backend. Valid values:
	//   "argocd"  — ArgoCDProvider (reads cluster Secrets from ArgoCD namespace)
	//   ""        — auto-default: argocd when running in-cluster, error otherwise
	Type string

	// ArgoCDNamespace is the typed source for the argocd namespace.
	// Used ONLY by the "argocd" backend. When empty, falls back to:
	//   1. SHARKO_ARGOCD_NAMESPACE env var (DEPRECATED — emits slog.Warn,
	//      removal slated for the next minor release)
	//   2. hardcoded "argocd" default
	ArgoCDNamespace string
}

// ClusterRegistrationSourceConfig configures where Sharko WRITES
// cluster registration material. The struct exists, gets parsed from
// env, and is stashed on the application context for the cluster
// reconciler. Default behavior (Type:"") is "no cluster registration
// sink configured".
type ClusterRegistrationSourceConfig struct {
	// Type selects the registration sink. Valid values:
	//   "argocd"  — ArgoCD cluster Secrets in ArgoCDNamespace
	//   ""        — none (no reconciler wired)
	Type string

	// ArgoCDNamespace is the K8s namespace where Sharko writes ArgoCD cluster
	// registration Secrets. Used ONLY by the "argocd" sink. When empty,
	// defaults to "argocd" (matches the standard ArgoCD install location).
	//
	// Conceptually distinct from ClusterTestProviderConfig.ArgoCDNamespace:
	// M2 READS from that namespace (where ArgoCD's own secrets live); M3
	// WRITES to it (where Sharko's reconciler creates registration Secrets).
	// On standard installs the value is the same ("argocd") for both, but
	// separating the fields lets non-standard installs (e.g. ArgoCD in one
	// ns, Sharko-managed reg secrets in another) be expressed cleanly.
	ArgoCDNamespace string
}
