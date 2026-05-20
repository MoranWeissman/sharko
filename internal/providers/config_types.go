package providers

// This file holds the three sibling ProviderConfig types introduced by the
// V125-1-11 sprint (BUG-OVERLOAD-DIAGNOSIS.md Section 4). They replaced the
// field-overloaded providers.Config struct that V125-1-10.8 had to work
// around with the SHARKO_ARGOCD_NAMESPACE env hack. Each type addresses
// exactly one mechanism — addon-secret material, cluster-test connectivity,
// or cluster-registration sink — so the compiler enforces the separation
// that field-level overload could not.
//
// V125-1-11.6 retired the old providers.Config struct + the providers.New /
// providers.NewSecretProvider compat dispatchers. These three types are now
// the only ProviderConfig shapes in the codebase. The canonical factories
// are: NewAddonSecretProvider(AddonSecretProviderConfig) and
// NewClusterTestProvider(ClusterTestProviderConfig). The
// ClusterRegistrationSourceConfig is still consumer-less in v1.25 — V125-1-8
// reconciler will materialize it.

// AddonSecretProviderConfig configures the backend that supplies addon-secret
// material for credential rotation + tiered git flow. ONE OF the configured
// backend types reads/decrypts secrets and pushes them into Sharko-managed
// remote-cluster K8s Secrets via the secrets reconciler.
//
// Mechanism scope (V125-1-11 split): addon-secrets ONLY. Does NOT serve
// cluster-connectivity-test creds (use ClusterTestProviderConfig) nor cluster-
// registration sink config (use ClusterRegistrationSourceConfig).
//
// Field semantics intentionally do NOT overlap with the other two configs —
// the 3-type split exists precisely so the compiler enforces this separation
// (closing the V125-1-10.8 ProviderConfig.Namespace cross-contamination smell).
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
// V125-1-11 scope: argocd-only. Sharko's canonical cluster-connectivity
// backend reads ArgoCD's cluster Secret in the argocd namespace (the same
// secret the V125-1-8 reconciler will write). Legacy aws-sm / k8s-secrets /
// gcp-sm / azure-kv cluster-credentials backends (per provider.go's deprecated
// doc comment) are retired in Story 11.6 along with the providers.New compat
// shim — one cycle earlier than the doc comment promised, but acceptable in
// v1.25 pre-release per planning doc OQ #6.
//
// Future post-v1.x: IAMRoleARN (EKS IAM token minting on the cluster-test
// side), ExecPluginAllowList (when/if exec-plugin auth becomes supported).
type ClusterTestProviderConfig struct {
	// Type selects the backend. Valid values:
	//   "argocd"  — ArgoCDProvider (reads cluster Secrets from ArgoCD namespace)
	//   ""        — auto-default: argocd when running in-cluster, error otherwise
	Type string

	// ArgoCDNamespace is the canonical replacement for the V125-1-10.8
	// SHARKO_ARGOCD_NAMESPACE env-var workaround. Used ONLY by the "argocd"
	// backend. When empty, falls back to:
	//   1. SHARKO_ARGOCD_NAMESPACE env var (DEPRECATED — emits slog.Warn,
	//      removal slated for v1.26 per planning doc OQ #4)
	//   2. hardcoded "argocd" default
	ArgoCDNamespace string
}

// ClusterRegistrationSourceConfig configures where Sharko WRITES cluster
// registration material. Today vestigial — V125-1-8 will materialize the
// reconciler that consumes this config. Story 11.5 pre-wires the parsing +
// stash so V125-1-8 doesn't need to re-touch ProviderConfig.
//
// V125-1-11 scope: parsing + Helm chart values.yaml block only. No consumer
// wired this sprint; the struct exists, gets parsed from env, and is stashed
// on the application context. Default behavior (Type:"") is "no cluster
// registration sink configured" — identical to pre-V125-1-11 behavior since
// the reconciler doesn't exist yet.
type ClusterRegistrationSourceConfig struct {
	// Type selects the registration sink. Valid values:
	//   "argocd"  — ArgoCD cluster Secrets in ArgoCDNamespace
	//   ""        — none (pre-V125-1-8 behavior; reconciler doesn't exist yet)
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
