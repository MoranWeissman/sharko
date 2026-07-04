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
// /api/v1/clusters/{name}/test surface, the dashboard "Verified" state)
// AND the kubeconfig used when REGISTERING a cluster from a secret backend
// (creds_source=secret-kubeconfig / eks-token on POST /api/v1/clusters).
//
// Scope (V2-cleanup-53.1): argocd, aws-sm, and k8s-secrets. The aws-sm and
// k8s-secrets cluster-credentials arms were retired in the V125-1-10.x
// redesign and RESTORED by V2-cleanup-53.1 — the secret-backend registration
// path (creds_source=secret-kubeconfig / eks-token) is an advertised core
// feature and must reach the configured backend. gcp-sm / azure-kv stay
// retired (stubs; addon-secret consumers only via NewAddonSecretProvider).
//
// Future: ExecPluginAllowList (when/if exec-plugin auth becomes supported).
type ClusterTestProviderConfig struct {
	// Type selects the backend. Valid values:
	//   "argocd"                          — ArgoCDProvider (reads cluster Secrets from ArgoCD namespace)
	//   "aws-sm" / "aws-secrets-manager"  — AWS Secrets Manager (kubeconfig or EKS-JSON secrets)
	//   "k8s-secrets" / "kubernetes"      — Kubernetes Secrets in Namespace (key "kubeconfig")
	//   ""                                — auto-default: argocd when running in-cluster, error otherwise
	Type string

	// ArgoCDNamespace is the typed source for the argocd namespace.
	// Used ONLY by the "argocd" backend. When empty, falls back to:
	//   1. SHARKO_ARGOCD_NAMESPACE env var (DEPRECATED — emits slog.Warn,
	//      removal slated for the next minor release)
	//   2. hardcoded "argocd" default
	//
	// NEVER populate this from the connection-level provider Namespace field —
	// that slot is addon-secrets-shaped (V125-1-10.8 cross-contamination guard;
	// see ClusterTestConfigFromConnection).
	ArgoCDNamespace string

	// Region is the AWS region for the "aws-sm" backend.
	// Used ONLY by aws-sm; ignored by all other backends.
	Region string

	// Prefix is an optional secret-name prefix for the "aws-sm" backend
	// (e.g. "clusters/" → fully-qualified secret name "clusters/<name>").
	// Used ONLY by aws-sm; ignored by all other backends.
	Prefix string

	// RoleARN is the default IAM role to assume for STS-based EKS token
	// generation when an aws-sm structured-EKS secret omits its own roleArn.
	// Used ONLY by aws-sm; ignored by all other backends.
	RoleARN string

	// Namespace is the K8s namespace where the "k8s-secrets" backend reads
	// cluster kubeconfig Secrets (secret name = cluster name, data key
	// "kubeconfig"). Default "sharko" — same convention as the addon-secret
	// k8s-secrets backend. Used ONLY by k8s-secrets; ignored by all other
	// backends. Semantically DISTINCT from ArgoCDNamespace.
	Namespace string
}

// ClusterTestConfigFromConnection maps the connection-level provider block
// (Settings → Secrets Provider) into the typed ClusterTestProviderConfig.
// This is the SINGLE source of truth for the fan-through — both boot-time
// wiring (cmd/sharko/serve.go) and the hot-reload path
// (api.Server.ReinitializeFromConnection) call it, so the two can never
// drift (the V2-cleanup-53.1 bug was exactly this drift: only "argocd"
// fanned through, so aws-sm/k8s-secrets registrations silently fell back
// to the ArgoCD provider).
//
// V125-1-10.8 cross-contamination guard, preserved: the connection-level
// namespace is NEVER copied into ArgoCDNamespace. For the "argocd" type the
// namespace parameter is ignored entirely — an empty ArgoCDNamespace lets
// resolveArgoCDNamespaceTyped fall back through SHARKO_ARGOCD_NAMESPACE →
// "argocd". For "k8s-secrets" the namespace flows into the (distinct)
// Namespace field, matching the addon-side k8s-secrets convention.
//
// Unknown / retired types (gcp-sm, azure-kv, ...) and the empty type return
// a zero config so the factory's auto-default path decides — identical to
// the pre-53.1 behavior for those types.
func ClusterTestConfigFromConnection(provType, region, prefix, namespace, roleARN string) ClusterTestProviderConfig {
	switch provType {
	case "argocd":
		return ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: ""}
	case "aws-sm", "aws-secrets-manager":
		return ClusterTestProviderConfig{
			Type:    provType,
			Region:  region,
			Prefix:  prefix,
			RoleARN: roleARN,
		}
	case "k8s-secrets", "kubernetes":
		return ClusterTestProviderConfig{
			Type:      provType,
			Namespace: namespace,
		}
	default:
		return ClusterTestProviderConfig{}
	}
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
