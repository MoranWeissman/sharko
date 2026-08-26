// Package envreg is the authoritative registry of Sharko's SHARKO_*
// configuration settings.
//
// # Why a registry, and why it is not just a table of names
//
// Before this package there was no registry at all. Seventy names were
// read from non-test Go across the tree, and the only guard was a test
// that compared documented names against "anything that looks like a read
// site" — where a string in a shell script counted, so a shell-local
// variable could prove a server setting existed.
//
// A registry that is only a list of names is decorative by construction:
// it is its own evidence. So every entry here carries the file that
// really reads it, and registry_test.go opens that file and checks the
// literal. A name is justified by its kind and its reader, not by being
// in the table.
//
// # Dependencies
//
// Standard library only, on purpose. envreg is a leaf: auth, settings,
// security and catalog/signing all read the environment and none of them
// import internal/config today, so a registry that pulled in anything
// would create an import cycle the first time it was used from one of
// them.
//
// # Rejecting unknown names, which used to be listed here as blocked
//
// It is no longer blocked. A SHARKO_ name the registry has never heard
// of now stops the server at startup, so a misspelled setting is told
// about instead of quietly ignored. Kubernetes service links were the
// blocker — kubelet injects SHARKO_ names derived from the Service name
// that Sharko never reads — and they are handled by their SHAPE rather
// than by a list of names, so an install under any release name behaves
// the same. unknown.go holds the whole rule, including what is
// deliberately out of its reach: the unprefixed names the chart also
// sets (CONNECTION_SECRET_NAME, ARGOCD_NAMESPACE, APP_ENVIRONMENTS,
// GITOPS_ACTIONS_ENABLED, the AI_* family) stay unguarded this round.
//
// # The listen port, which used to be listed above as untouched
//
// It is no longer untouched. SHARKO_HTTP_PORT is now the setting, parsed
// strictly, and SHARKO_PORT is a registered deprecated alias for it. See
// httpport.go for the whole rule; the short version is that the parse no
// longer discards its error, so "80x" stops the server instead of being
// read as 80, and a SHARKO_PORT holding the URI Kubernetes injects is
// ignored in silence rather than treated as anybody's configuration.
package envreg

// Kind says what a setting IS, which is a product statement, not a fact
// about the code. The kind decides what the guards demand of it.
type Kind string

const (
	// Production — an operator may set this on a real installation. Its
	// reader must be non-test production code.
	Production Kind = "production"

	// DeprecatedAlias — a name that was published before and still
	// works, resolving to a canonical Production setting. Using it warns
	// once at startup.
	DeprecatedAlias Kind = "deprecated-alias"

	// TestHarness — read only by the test harness. Its reader must be a
	// _test.go file or live under tests/. Documenting one is honest;
	// pretending it is a server setting is not.
	TestHarness Kind = "test-harness"

	// Internal — read by production code but not an operator-facing
	// knob: development switches and rig-only escape hatches.
	Internal Kind = "internal"
)

// Setting is one registered SHARKO_* name.
type Setting struct {
	// Name is the environment variable name, exactly.
	Name string

	// Kind decides which guards apply. See the Kind constants.
	Kind Kind

	// Summary is one plain line saying what the setting does.
	Summary string

	// Default is the value the reader falls back to when the variable is
	// unset. Empty means the reader has no default of its own.
	Default string

	// Secret marks a setting whose value must never be logged, echoed in
	// an error, or written to an audit entry.
	Secret bool

	// AliasOf names the canonical setting a DeprecatedAlias resolves to.
	// Empty for every other kind.
	AliasOf string

	// ReaderFile is the repo-relative path of the file that really reads
	// this name. registry_test.go opens it and checks that the quoted
	// literal is there, so an entry cannot rot into a claim with no
	// reader behind it.
	ReaderFile string
}

// Reader file constants, so a rename shows up as one edit rather than
// forty near-identical strings drifting apart.
const (
	readerServe       = "cmd/sharko/serve.go"
	readerHTTPPort    = "internal/envreg/httpport.go"
	readerClient      = "cmd/sharko/client.go"
	readerAuthStore   = "internal/auth/store.go"
	readerConnGit     = "internal/config/connection_gitnative.go"
	readerCatalogSrc  = "internal/config/catalog_sources.go"
	readerSettings    = "internal/settings/store.go"
	readerTrust       = "internal/catalog/signing/trust.go"
	readerRouter      = "internal/api/router.go"
	readerTieredGit   = "internal/api/tiered_git.go"
	readerHarnessMain = "tests/e2e/harness/sharko_helm.go"
	readerHarnessGit  = "tests/e2e/harness/gitfake_helm.go"
	readerGiteaLive   = "tests/e2e/lifecycle/gitea_live_test.go"
)

// settings is the registry.
//
// Every entry is a claim with a file behind it. Adding a name here does
// NOT make it exist — registry_test.go opens ReaderFile and checks the
// literal, refuses a reader under tests/ or scripts/ for a production
// setting, and refuses this file as anybody's reader.
var settings = []Setting{
	// ---------------------------------------------------------------
	// Server basics
	// ---------------------------------------------------------------
	{Name: "SHARKO_HTTP_PORT", Kind: Production, Summary: "TCP port the server listens on.", Default: "8080", ReaderFile: readerHTTPPort},
	{Name: "SHARKO_CONFIG", Kind: Production, Summary: "Path to the server configuration file.", ReaderFile: readerServe},
	{Name: "SHARKO_STATIC_DIR", Kind: Production, Summary: "Directory the built UI is served from.", ReaderFile: readerServe},
	{Name: "SHARKO_LOG_LEVEL", Kind: Production, Summary: "Log level: debug, info, warn or error.", Default: "info", ReaderFile: readerServe},
	{Name: "SHARKO_NAMESPACE", Kind: Production, Summary: "Kubernetes namespace Sharko runs in and stores its own state in.", ReaderFile: readerServe},
	{Name: "SHARKO_CONFIG_DIR", Kind: Production, Summary: "Directory the CLI keeps its own config in.", ReaderFile: readerClient},
	{Name: "SHARKO_CORS_ORIGIN", Kind: Production, Summary: "Allowed CORS origin. Setting it to * removes the same-origin restriction entirely.", ReaderFile: readerRouter},
	{Name: "SHARKO_TRUSTED_PROXIES", Kind: Production, Summary: "IP addresses and CIDR ranges whose forwarding headers are believed when resolving the client address. Empty means no proxy is trusted.", ReaderFile: "internal/api/clientip.go"},
	{Name: "SHARKO_URL_ALLOWLIST", Kind: Production, Summary: "Extra hosts the outbound URL guard may reach.", ReaderFile: "internal/security/url_guard.go"},

	// ---------------------------------------------------------------
	// Credentials and auth
	// ---------------------------------------------------------------
	{Name: "SHARKO_ENCRYPTION_KEY", Kind: Production, Secret: true, Summary: "Key used to encrypt stored credentials.", ReaderFile: readerServe},
	{Name: "SHARKO_AUTH_USER", Kind: Production, Summary: "Username for the single local account used when no user store is configured.", ReaderFile: readerAuthStore},
	{Name: "SHARKO_AUTH_PASSWORD", Kind: Production, Secret: true, Summary: "Password for that single local account.", ReaderFile: readerAuthStore},
	{Name: "SHARKO_BOOTSTRAP_ADMIN_PASSWORD", Kind: Production, Secret: true, Summary: "Password given to the admin account created on first startup.", ReaderFile: readerAuthStore},
	{Name: "SHARKO_SECRET_NAME", Kind: Production, Summary: "Name of the Kubernetes Secret holding Sharko's own state.", Default: "sharko", ReaderFile: readerAuthStore},
	{Name: "SHARKO_INITIAL_ADMIN_FILE", Kind: Production, Summary: "File the first admin credentials are read from.", ReaderFile: "internal/auth/initial_admin.go"},
	{Name: "SHARKO_API_TOKENS_FILE", Kind: Production, Summary: "File API tokens are persisted to when no Kubernetes Secret is available.", ReaderFile: "internal/auth/token_persistence.go"},
	{Name: "SHARKO_WRITE_INITIAL_ADMIN_SECRET", Kind: Production, Summary: "Whether the initial admin credentials are written back to a Kubernetes Secret.", ReaderFile: readerAuthStore},
	{Name: "SHARKO_WEBHOOK_SECRET", Kind: Production, Secret: true, Summary: "Shared secret for the HMAC signature on the Git webhook.", ReaderFile: "internal/api/webhooks.go"},

	// ---------------------------------------------------------------
	// ArgoCD and cluster registration
	// ---------------------------------------------------------------
	// Deprecated, but NOT a DeprecatedAlias: what replaced it is the
	// clusterTest.argocdNamespace Helm value, which is typed
	// configuration and not another environment variable. Registering it
	// as an alias would name a canonical setting that does not exist.
	{Name: "SHARKO_ARGOCD_NAMESPACE", Kind: Production, Summary: "Namespace ArgoCD runs in. Deprecated: set the clusterTest.argocdNamespace Helm value instead. Warns once at startup; removal slated for v1.26.", Default: "argocd", ReaderFile: "internal/providers/argocd_provider.go"},
	{Name: "SHARKO_CLUSTER_REG_TYPE", Kind: Production, Summary: "Source Sharko reads the cluster list from.", ReaderFile: readerServe},
	{Name: "SHARKO_CLUSTER_REG_ARGOCD_NAMESPACE", Kind: Production, Summary: "Namespace the cluster-registration source reads ArgoCD cluster Secrets from.", ReaderFile: readerServe},
	{Name: "SHARKO_REPO_PATH_MANAGED_CLUSTERS", Kind: Production, Summary: "Path to managed-clusters.yaml inside the GitOps repository.", Default: "configuration/managed-clusters.yaml", ReaderFile: readerServe},
	{Name: "SHARKO_CLUSTER_STATUS_CACHE_TTL", Kind: Production, Summary: "How long a computed cluster status is cached.", ReaderFile: "internal/observations/cache.go"},
	{Name: "SHARKO_CONNECTIVITY_CHECK", Kind: Production, Summary: "Whether the startup connectivity check runs.", Default: "true", ReaderFile: readerServe},
	{Name: "SHARKO_AUTO_REMEDIATE", Kind: Production, Summary: "Whether Sharko applies its own automatic remediations.", Default: "true", ReaderFile: readerServe},

	// ---------------------------------------------------------------
	// Reconcilers and background loops
	// ---------------------------------------------------------------
	{Name: "SHARKO_PR_POLL_INTERVAL", Kind: Production, Summary: "How often open pull requests are polled for merge.", ReaderFile: "internal/prtracker/tracker.go"},
	{Name: "SHARKO_SECRET_RECONCILE_INTERVAL", Kind: Production, Summary: "How often addon secrets are reconciled onto remote clusters.", Default: "5m", ReaderFile: readerServe},
	{Name: "SHARKO_SETTINGS_RECONCILE_INTERVAL", Kind: Production, Summary: "How often the settings store is reconciled against the environment.", Default: "60s", ReaderFile: readerServe},
	{Name: "SHARKO_CONNECTION_CHECK_INTERVAL", Kind: Production, Summary: "How often a connection's reconciliation state is recomputed.", Default: "60s", ReaderFile: readerServe},
	{Name: "SHARKO_CONNECTION_CREDENTIAL_CHECK_INTERVAL", Kind: Production, Summary: "How often the background credential-drift check runs.", Default: "15m", ReaderFile: readerServe},

	// ---------------------------------------------------------------
	// Catalog
	// ---------------------------------------------------------------
	{Name: "SHARKO_CATALOG_URLS", Kind: Production, Summary: "Third-party addon catalog URLs to merge in.", ReaderFile: readerCatalogSrc},
	{Name: "SHARKO_CATALOG_URLS_ALLOW_PRIVATE", Kind: Production, Summary: "Whether a catalog URL may resolve to a private address.", ReaderFile: readerCatalogSrc},
	{Name: "SHARKO_CATALOG_REFRESH_INTERVAL", Kind: Production, Summary: "How often third-party catalogs are refetched.", ReaderFile: readerCatalogSrc},
	{Name: "SHARKO_CATALOG_FRESHNESS_ENABLED", Kind: Production, Summary: "Whether the catalog freshness checker runs.", Default: "true", ReaderFile: readerServe},
	{Name: "SHARKO_CATALOG_FRESHNESS_INTERVAL", Kind: Production, Summary: "How often catalog freshness is checked.", Default: "24h", ReaderFile: readerServe},
	{Name: "SHARKO_CATALOG_TRUSTED_IDENTITIES", Kind: Production, Summary: "Signer identities the catalog trust policy accepts.", ReaderFile: readerTrust},
	{Name: "SHARKO_CATALOG_TRUSTED_WORKFLOW_REF", Kind: Production, Summary: "Workflow reference the catalog trust policy requires on a signature.", ReaderFile: readerTrust},
	{Name: "SHARKO_SIGSTORE_TUF_CACHE", Kind: Production, Summary: "Directory the Sigstore TUF root is cached in.", Default: "/tmp/sigstore-tuf", ReaderFile: "internal/catalog/signing/tufroot.go"},

	// ---------------------------------------------------------------
	// Connection (git-native). All read through named constants in
	// connection_gitnative.go — which is why the read scan matches any
	// quoted literal and not just os.Getenv( calls.
	// ---------------------------------------------------------------
	{Name: "SHARKO_CONN_GIT_PROVIDER", Kind: Production, Summary: "Git provider backing the connection.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GIT_REPO_URL", Kind: Production, Summary: "GitOps repository URL.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GIT_OWNER", Kind: Production, Summary: "Repository owner.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GIT_REPO", Kind: Production, Summary: "Repository name.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GIT_ORGANIZATION", Kind: Production, Summary: "Azure DevOps organization.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GIT_PROJECT", Kind: Production, Summary: "Azure DevOps project.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GIT_REPOSITORY", Kind: Production, Summary: "Azure DevOps repository.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ARGOCD_SERVER_URL", Kind: Production, Summary: "ArgoCD server URL on the connection.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ARGOCD_NAMESPACE", Kind: Production, Summary: "ArgoCD namespace on the connection.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ARGOCD_INSECURE", Kind: Production, Summary: "Whether the ArgoCD server's TLS certificate is not verified.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_PROVIDER_TYPE", Kind: Production, Summary: "Cluster-credentials provider type.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_PROVIDER_REGION", Kind: Production, Summary: "Cluster-credentials provider region.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_PROVIDER_PREFIX", Kind: Production, Summary: "Path prefix the cluster-credentials provider looks under.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_PROVIDER_NAMESPACE", Kind: Production, Summary: "Namespace the cluster-credentials provider reads Secrets from.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_PROVIDER_ROLE_ARN", Kind: Production, Summary: "Role the cluster-credentials provider assumes.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ADDON_SECRET_PROVIDER_TYPE", Kind: Production, Summary: "Addon-secret provider type.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ADDON_SECRET_PROVIDER_REGION", Kind: Production, Summary: "Addon-secret provider region.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ADDON_SECRET_PROVIDER_PREFIX", Kind: Production, Summary: "Path prefix the addon-secret provider looks under.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ADDON_SECRET_PROVIDER_NAMESPACE", Kind: Production, Summary: "Namespace the addon-secret provider reads Secrets from.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_ADDON_SECRET_PROVIDER_ROLE_ARN", Kind: Production, Summary: "Role the addon-secret provider assumes.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GITOPS_BASE_BRANCH", Kind: Production, Summary: "Branch pull requests target.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GITOPS_BRANCH_PREFIX", Kind: Production, Summary: "Prefix on branches Sharko creates.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GITOPS_COMMIT_PREFIX", Kind: Production, Summary: "Prefix on commit messages Sharko writes.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GITOPS_PR_AUTO_MERGE", Kind: Production, Summary: "Whether Sharko merges its own pull requests.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GITOPS_HOST_CLUSTER_NAME", Kind: Production, Summary: "Name of the cluster Sharko itself runs on.", ReaderFile: readerConnGit},
	{Name: "SHARKO_CONN_GITOPS_DEFAULT_ADDONS", Kind: Production, Summary: "Addons enabled on a newly registered cluster.", ReaderFile: readerConnGit},

	// ---------------------------------------------------------------
	// Settings store
	// ---------------------------------------------------------------
	{Name: "SHARKO_ALLOW_INLINE_CREDENTIALS", Kind: Production, Summary: "Whether a connection may carry credentials inline instead of by reference.", Default: "false", ReaderFile: readerSettings},
	{Name: "SHARKO_PROBE_MODE", Kind: Production, Summary: "Whether background probing of connections is on.", Default: "false", ReaderFile: readerSettings},

	// ---------------------------------------------------------------
	// Internal — read by production code, not an operator knob
	// ---------------------------------------------------------------
	{Name: "SHARKO_DEV_MODE", Kind: Internal, Summary: "Development switch that relaxes the tiered Git attribution rules.", ReaderFile: readerTieredGit},

	// PROVISIONAL KIND — awaiting a product ruling. This is read by
	// internal/service/connection.go, production code, and it is set on
	// the live playground deployment, but its name says e2e and what it
	// does is widen a host allowlist. Internal is the conservative
	// reading: not an operator-facing knob. If the product owner rules
	// it a supported setting, change the kind to Production and document
	// it. A kind label is a product statement, not a code fact.
	{Name: "SHARKO_E2E_GIT_HOSTS_ALLOWLIST", Kind: Internal, Summary: "Extra Git hosts a connection may point at, for test rigs. PROVISIONAL KIND.", ReaderFile: "internal/service/connection.go"},

	// PROVISIONAL KIND — awaiting a product ruling. Read by
	// internal/verify/config.go, production code, and it names a
	// namespace Sharko creates on a CUSTOMER cluster during the
	// connectivity test. Production is the conservative reading: an
	// operator whose cluster policy forbids that namespace name has to
	// be able to change it. If the product owner rules it internal,
	// change the kind and stop documenting it.
	{Name: "SHARKO_TEST_NAMESPACE", Kind: Production, Summary: "Namespace the connectivity test creates on the target cluster. PROVISIONAL KIND.", Default: "sharko-test", ReaderFile: "internal/verify/config.go"},

	// ---------------------------------------------------------------
	// Test harness — readers under tests/, and the guards REQUIRE that.
	// These two are the live hole rule 3 closes: neither file is named
	// *_test.go, so before this registry they counted as production
	// readers and could prove a documented setting existed.
	// ---------------------------------------------------------------
	{Name: "SHARKO_E2E_IMAGE_TAG", Kind: TestHarness, Summary: "Image tag the kind-backed e2e harness deploys Sharko from.", ReaderFile: readerHarnessMain},
	{Name: "SHARKO_GITFAKE_IMAGE_TAG", Kind: TestHarness, Summary: "Image tag the e2e harness deploys the in-cluster git fake from.", ReaderFile: readerHarnessGit},

	// The live-Gitea suite. These five are genuinely implemented — a
	// contributor exports them to point the suite at a real server — and
	// the developer guide documents them honestly. Their only reader is
	// a build-tagged test, and naming it here is what keeps that claim
	// checkable: delete or rename the suite and TestRule3 fails.
	{Name: "SHARKO_E2E_GITEA_URL", Kind: TestHarness, Summary: "Server URL the live Gitea e2e suite runs against.", ReaderFile: readerGiteaLive},
	{Name: "SHARKO_E2E_GITEA_TOKEN", Kind: TestHarness, Secret: true, Summary: "API token for that Gitea server.", ReaderFile: readerGiteaLive},
	{Name: "SHARKO_E2E_GITEA_OWNER", Kind: TestHarness, Summary: "Repository owner the live Gitea e2e suite runs against.", ReaderFile: readerGiteaLive},
	{Name: "SHARKO_E2E_GITEA_REPO", Kind: TestHarness, Summary: "Repository name the live Gitea e2e suite runs against.", ReaderFile: readerGiteaLive},
	{Name: "SHARKO_E2E_GITEA_BASE", Kind: TestHarness, Summary: "API base path when that Gitea server does not use the default.", ReaderFile: readerGiteaLive},

	// ---------------------------------------------------------------
	// Deprecated aliases
	// ---------------------------------------------------------------
	//
	// There is exactly one, and it is not here by choice. SHARKO_PORT was
	// the published name for the listen port, so it has to keep working;
	// it is also a name Kubernetes injects into Sharko's own Pod holding
	// a URI, so it cannot simply be parsed as a port either. httpport.go
	// holds the whole rule.
	//
	// Nothing else is aliased, deliberately. The SHARKO_CONN_* family
	// makes git authoritative for a field, so reviving
	// SHARKO_GITOPS_BASE_BRANCH as an alias would silently hand a field
	// to git for an operator who only wanted to change a branch prefix.
	{Name: "SHARKO_PORT", Kind: DeprecatedAlias, AliasOf: "SHARKO_HTTP_PORT", ReaderFile: readerHTTPPort,
		Summary: "Former name for SHARKO_HTTP_PORT. Still works and warns; a value Kubernetes injected is ignored."},
}

// Registry returns a copy of every registered setting, sorted by name.
func Registry() []Setting {
	out := make([]Setting, len(settings))
	copy(out, settings)
	sortByName(out)
	return out
}

// Get returns the registered setting by name.
func Get(name string) (Setting, bool) {
	for _, s := range settings {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}

// RegistryFile is this package's registry source file, repo-relative.
//
// It exists so the guards can EXCLUDE it. Every registered name appears
// here as a quoted literal, so counting this file as a read site would
// make the registry its own evidence — the identical failure that made
// the first configuration allowlist decorative.
const RegistryFile = "internal/envreg/registry.go"

func sortByName(in []Setting) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Name < in[j-1].Name; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
