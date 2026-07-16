package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/capabilities"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// selfManagedConnectionsDocURL is the public, clickable location of the
// self-managed-connections operator guide (V2-cleanup-90.1, review finding
// part of M6/L4). Duplicated as a small unexported constant in
// internal/orchestrator (which cannot import this package) rather than
// exported from one place, since the two packages must not share an import
// edge — matches the base every other in-app readthedocs link uses (see
// e.g. ui/src/components/ClusterIdentityPanel.tsx).
const selfManagedConnectionsDocURL = "https://sharko.readthedocs.io/en/latest/operator/self-managed-connections/"

// Connection doctor (V2-cleanup-88.4) — an attempt-based permission
// preflight for a cluster's connection. Unlike the IAM diagnose tool
// (internal/diagnose), every check here is a REAL attempt against the real
// system (fetch, read, assume, write) — never policy simulation — and every
// failure carries a plain-English, non-developer-facing fix.
//
// The four checks are independent verdicts in one response, not a
// short-circuiting pipeline: a failure in an earlier check does not abort
// the request, so the caller always gets the fullest picture available.
// The one real dependency is checkClusterAccess, which needs the
// credentials fetched by checkConnectionCredentials to build a client at
// all — when those are missing it reports "not-applicable" rather than
// failing outright.

// doctorRunTimeout bounds the ENTIRE doctor run (all four checks
// combined) so the endpoint can never hang a caller.
const doctorRunTimeout = 30 * time.Second

// doctorCheckTimeout bounds a single check. Each check derives its context
// from the run-level context, so the run-level deadline always wins when
// it's sooner — this is a per-check ceiling, not an extension of the total
// budget.
const doctorCheckTimeout = 10 * time.Second

// Check IDs (doctorCheck.ID). Stable — the UI story (88.5) dispatches
// copy/icons off these values.
const (
	doctorCheckConnectionCredentials = "connection-credentials"
	doctorCheckAddonSecretPaths      = "addon-secret-paths"
	doctorCheckAssumeRole            = "assume-role"
	doctorCheckClusterAccess         = "cluster-access"
	// doctorCheckSecretOwnership is the fifth check (V2-cleanup-89.5): does
	// this cluster's ArgoCD cluster Secret carry a foreign ArgoCD tracking
	// marker, meaning another Application renders it from Git and could
	// fight Sharko over the addon labels it writes. Not-applicable for
	// Sharko-managed connections (Sharko is the Secret's sole writer there)
	// and for self-managed connections with no Secret yet.
	doctorCheckSecretOwnership = "secret-ownership"
	// doctorCheckConnectivityApp is the sixth check (V3 BUG-1): does the
	// connectivity-check ApplicationSet have a stale selector that doesn't
	// match the connectivity-check label Sharko is now writing? A managed
	// cluster labeled for connectivity-check but with no generated
	// connectivity-check Application is the signature of this drift.
	// Not-applicable for clusters without the connectivity-check label and
	// for clusters with real addons deployed (the check app intentionally
	// yields to real addons).
	doctorCheckConnectivityApp = "connectivity-app-drift"
)

// Check statuses (doctorCheck.Status). "warn" (V2-cleanup-90.1) is
// additive — a check can be worse than pass but not bad enough to fail the
// whole connection outright; currently only check 5 (secret-ownership) ever
// returns it, for a soft-confidence foreign-owner signal.
const (
	doctorStatusPass          = "pass"
	doctorStatusFail          = "fail"
	doctorStatusNotApplicable = "not-applicable"
	doctorStatusWarn          = "warn"
)

// Overall verdicts (doctorClusterResponse.Overall).
const (
	doctorOverallPass    = "pass"
	doctorOverallFail    = "fail"
	doctorOverallPartial = "partial"
)

// doctorCheck is one attempt-based check's structured verdict.
type doctorCheck struct {
	ID     string `json:"id" example:"connection-credentials"`
	Status string `json:"status" enums:"pass,fail,not-applicable,warn"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// doctorClusterResponse is the full response for POST /clusters/{name}/doctor.
type doctorClusterResponse struct {
	Checks  []doctorCheck `json:"checks"`
	Overall string        `json:"overall" enums:"pass,fail,partial"`
}

// handleDoctorCluster godoc
//
// @Summary Run the connection doctor
// @Description Runs up to six real-attempt checks against the named cluster's
// @Description connection and returns a structured pass/fail/warn/not-applicable
// @Description verdict per check, each with a plain-English fix on failure or
// @Description warning: (1) can Sharko read the cluster's connection credentials,
// @Description (2) can Sharko read every provider path an enabled addon's
// @Description secrets need, (3) if a cross-account IAM role is in play, can
// @Description Sharko assume it, (4) does the cluster itself accept the
// @Description resulting token (reuses the existing Stage-1 secret CRUD cycle),
// @Description and (5) for a self-managed connection, is its ArgoCD cluster
// @Description Secret free of a tracking marker that may belong to another
// @Description application — a verified tracking-id match against this exact
// @Description Secret fails the check, a weaker signal (a mismatched
// @Description tracking-id or only the app.kubernetes.io/instance label, which
// @Description a plain Helm release also stamps) warns instead of failing —
// @Description not-applicable for Sharko-managed connections, and (6) for a
// @Description cluster labeled sharko.dev/connectivity-check: enabled, does the
// @Description expected connectivity-check Application exist in ArgoCD — missing
// @Description when labeled is the signature of a stale ApplicationSet selector
// @Description (sharko.io → sharko.dev label rename drift). Every check is a
// @Description real attempt, never IAM policy simulation, and read-only except
// @Description check 4, which reuses Stage-1's existing create/read/delete
// @Description canary secret. The whole run is bounded to about 30 seconds.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} doctorClusterResponse "Doctor verdict"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /clusters/{name}/doctor [post]
// handleDoctorCluster handles POST /api/v1/clusters/{name}/doctor.
func (s *Server) handleDoctorCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.doctor") {
		return
	}

	name := r.PathValue("name")
	slog.Info("[cluster-doctor] starting", "name", name)

	ctx, cancel := context.WithTimeout(r.Context(), doctorRunTimeout)
	defer cancel()

	credCheck, creds := s.doctorCheckCredentials(ctx, name)
	secretsCheck := s.doctorCheckAddonSecretPaths(ctx, name)
	assumeCheck := s.doctorCheckAssumeRole(ctx, name)
	accessCheck := s.doctorCheckClusterAccess(ctx, name, creds, assumeCheck.Status == doctorStatusPass)
	ownershipCheck := s.doctorCheckSecretOwnership(ctx, name)
	connectivityAppCheck := s.doctorCheckConnectivityApp(ctx, name)

	checks := []doctorCheck{credCheck, secretsCheck, assumeCheck, accessCheck, ownershipCheck, connectivityAppCheck}
	resp := doctorClusterResponse{Checks: checks, Overall: doctorOverallStatus(checks)}

	slog.Info("[cluster-doctor] complete", "name", name, "overall", resp.Overall)
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_doctor_run",
		Resource: fmt.Sprintf("cluster:%s", name),
		Detail:   fmt.Sprintf("overall=%s", resp.Overall),
	})

	writeJSON(w, http.StatusOK, resp)
}

// doctorOverallStatus rolls up all checks: "fail" when nothing that ran
// passed, "partial" when some passed and some failed, "pass" when nothing
// failed (regardless of how many checks were not-applicable). V2-cleanup-90.1
// extends (does not replace) that pre-existing fail/pass logic: a "warn" on
// any check also pulls an otherwise-clean run down to "partial" — a warning
// is weaker than a failure, so it never turns a run into "fail", but it is
// not nothing either, so an all-pass-plus-warn run should not read as a
// clean "pass".
func doctorOverallStatus(checks []doctorCheck) string {
	hasFail := false
	hasPass := false
	hasWarn := false
	for _, c := range checks {
		switch c.Status {
		case doctorStatusFail:
			hasFail = true
		case doctorStatusPass:
			hasPass = true
		case doctorStatusWarn:
			hasWarn = true
		}
	}
	switch {
	case hasFail && hasPass:
		return doctorOverallPartial
	case hasFail:
		return doctorOverallFail
	case hasWarn:
		return doctorOverallPartial
	default:
		return doctorOverallPass
	}
}

// doctorCheckCredentials is check 1: can Sharko fetch the cluster's
// connection credentials from its configured source? Reuses
// fetchClusterCredentials — the exact same routed-fetch helper the Test
// handler uses — building no new fetch logic. Returns the fetched
// credentials so doctorCheckClusterAccess can reuse them without a second
// fetch.
func (s *Server) doctorCheckCredentials(ctx context.Context, name string) (doctorCheck, *providers.Kubeconfig) {
	if s.credProvider() == nil {
		return doctorCheck{
			ID:     doctorCheckConnectionCredentials,
			Status: doctorStatusFail,
			Detail: "Sharko has no secrets backend or ArgoCD connection configured, so it cannot read any cluster's connection credentials.",
			Fix:    "Configure a secrets backend (Vault / AWS Secrets Manager / Kubernetes Secrets) or the built-in ArgoCD connection in Settings -> Connections.",
		}, nil
	}

	cctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	creds, err := s.fetchClusterCredentials(cctx, name)
	if err != nil {
		var argoErr *providers.ArgoCDProviderError
		if errors.As(err, &argoErr) {
			return doctorCheck{
				ID:     doctorCheckConnectionCredentials,
				Status: doctorStatusFail,
				Detail: fmt.Sprintf("Sharko could not read connection credentials for cluster %q: %s", name, argoErr.Detail),
				Fix:    doctorFixForArgoCDError(argoErr),
			}, nil
		}
		return doctorCheck{
			ID:     doctorCheckConnectionCredentials,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Sharko could not read connection credentials for cluster %q: %s", name, err.Error()),
			Fix:    "Check that the cluster is registered and its credentials still exist at the configured source (secret path or ArgoCD cluster Secret), then try again.",
		}, nil
	}

	return doctorCheck{
		ID:     doctorCheckConnectionCredentials,
		Status: doctorStatusPass,
		Detail: fmt.Sprintf("Sharko can read the connection credentials for cluster %q.", name),
	}, creds
}

// doctorFixForArgoCDError maps the stable ArgoCDProviderError codes
// (V2-cleanup-88.2) to a plain-English, non-developer-facing fix.
func doctorFixForArgoCDError(argoErr *providers.ArgoCDProviderError) string {
	switch argoErr.Code {
	case providers.ArgoCDProviderCodeIAMRequired:
		return "Give Sharko's own AWS identity (IRSA / EKS Pod Identity) permission to mint a token for this cluster — sts:AssumeRole on the role this connection names, or direct EKS access if no role is set. If no AWS region could be found, set one on the ArgoCD cluster Secret or the cluster's region label."
	case providers.ArgoCDProviderCodeExecUnsupported:
		return "Re-register this cluster's connection with a supported auth method (bearer token, client certificate, or AWS IAM) — Sharko never runs exec-plugin binaries."
	case providers.ArgoCDProviderCodeUnsupportedAuth:
		return "Re-register this cluster's connection — its ArgoCD cluster Secret has no recognized authentication field (bearer token, client certificate, or AWS IAM)."
	default:
		return "Re-check this cluster's connection in Settings -> Connections."
	}
}

// doctorCheckAddonSecretPaths is check 2: for each addon enabled on this
// cluster whose catalog entry declares a secrets block, can Sharko read
// every provider path it references? Read-only — GetSecretValue never
// writes. Reuses the same catalog + managed-clusters read/parse the secrets
// reconciler uses (internal/secrets/reconciler.go) and the same
// providers.NewAddonSecretProvider factory the reconciler is built from;
// this function builds no new fetch logic of its own.
func (s *Server) doctorCheckAddonSecretPaths(ctx context.Context, clusterName string) doctorCheck {
	cctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil || gp == nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: "Sharko has no active Git connection, so it cannot tell which addons are enabled on this cluster.",
			Fix:    "Connect a Git repository in Settings -> Connections, then run the doctor again.",
		}
	}

	catalogData, err := gp.GetFileContent(cctx, s.repoPaths.Catalog, s.gitopsConfig().BaseBranch)
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: "Sharko could not read the addon catalog from Git: " + err.Error(),
			Fix:    "Check that the addon catalog file still exists on the configured branch.",
		}
	}
	parser := config.NewParser()
	catalog, err := parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: "Sharko could not parse the addon catalog: " + err.Error(),
			Fix:    "Fix the YAML in the addon catalog file and try again.",
		}
	}

	clusterData, err := gp.GetFileContent(cctx, s.repoPaths.ManagedClusters, s.gitopsConfig().BaseBranch)
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: "Sharko could not read the managed-clusters file from Git: " + err.Error(),
			Fix:    "Check that the managed-clusters file still exists on the configured branch.",
		}
	}
	clusters, err := parser.ParseClusterAddons(clusterData)
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: "Sharko could not parse the managed-clusters file: " + err.Error(),
			Fix:    "Fix the YAML in the managed-clusters file and try again.",
		}
	}

	var cluster *models.Cluster
	for i := range clusters {
		if clusters[i].Name == clusterName {
			cluster = &clusters[i]
			break
		}
	}
	if cluster == nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusNotApplicable,
			Detail: fmt.Sprintf("Cluster %q is not in the managed-clusters file, so it has no addons enabled.", clusterName),
		}
	}

	enabledAddons := parser.GetEnabledAddons(*cluster, catalog)
	enabledByName := make(map[string]bool, len(enabledAddons))
	for _, ea := range enabledAddons {
		if ea.Enabled {
			enabledByName[ea.AddonName] = true
		}
	}

	type secretPathRef struct {
		addon, key, path string
	}
	var refs []secretPathRef
	for _, entry := range catalog {
		if !enabledByName[entry.Name] || len(entry.Secrets) == 0 {
			continue
		}
		for _, secretRef := range entry.Secrets {
			for key, path := range secretRef.Keys {
				refs = append(refs, secretPathRef{addon: entry.Name, key: key, path: path})
			}
		}
	}
	if len(refs) == 0 {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusNotApplicable,
			Detail: "No addon enabled on this cluster declares any secrets, so there is nothing to check.",
		}
	}

	cfg := s.addonSecretCfg()
	if cfg == nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("This cluster has %d addon secret path(s) to check, but Sharko has no addon-secret provider configured.", len(refs)),
			Fix:    "Configure a secrets backend (Vault / AWS Secrets Manager / Kubernetes Secrets) for addon secrets in Settings -> Connections.",
		}
	}
	secretProvider, err := s.getDoctorAddonSecretProviderFn()(*cfg)
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: "Sharko could not build the addon-secret provider: " + err.Error(),
			Fix:    "Check the addon-secret provider configuration in Settings -> Connections.",
		}
	}

	var firstFailure *secretPathRef
	failures := 0
	for i := range refs {
		if _, err := secretProvider.GetSecretValue(cctx, refs[i].path); err != nil {
			failures++
			if firstFailure == nil {
				firstFailure = &refs[i]
			}
		}
	}
	if failures > 0 {
		// V3 E1: surface the secrets-backend read failure as a k8s Warning
		// event. The message names the cluster and the addon and a count —
		// never the secret path or key (those can leak the layout of the
		// backend) and never the secret value.
		s.emitWarning(events.ReasonAWSSecretsGetFailed,
			fmt.Sprintf("Secrets backend read failed for cluster %q: Sharko could not read %d of %d addon secret path(s) (first failing addon: %q).", clusterName, failures, len(refs), firstFailure.addon))
		return doctorCheck{
			ID:     doctorCheckAddonSecretPaths,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Sharko cannot read %d of %d addon secret path(s). First failure: addon %q needs key %q at path %q.", failures, len(refs), firstFailure.addon, firstFailure.key, firstFailure.path),
			Fix:    fmt.Sprintf("Check that the path %q exists in the secrets backend and that Sharko's identity has permission to read it.", firstFailure.path),
		}
	}

	return doctorCheck{
		ID:     doctorCheckAddonSecretPaths,
		Status: doctorStatusPass,
		Detail: fmt.Sprintf("Sharko can read all %d addon secret path(s) needed by this cluster's enabled addons.", len(refs)),
	}
}

// getDoctorAssumeRoleFn lazily builds (and caches, via sync.Once) the
// Server's AssumeRole test function, backed by
// capabilities.NewAssumeRoleChecker().Check. Lazy rather than wired in
// NewServer so Server literals built directly by table-driven tests
// (bypassing NewServer) still work; tests in this package may also pre-set
// s.doctorAssumeRoleFn directly to inject a fake attempt — mirrors
// getAWSDetector / getHubPlatformDetector in capabilities.go exactly.
func (s *Server) getDoctorAssumeRoleFn() func(ctx context.Context, roleARN, region string) error {
	s.doctorAssumeRoleOnce.Do(func() {
		if s.doctorAssumeRoleFn == nil {
			s.doctorAssumeRoleFn = capabilities.NewAssumeRoleChecker().Check
		}
	})
	return s.doctorAssumeRoleFn
}

// getDoctorAddonSecretProviderFn lazily builds (and caches, via sync.Once)
// the Server's addon-secret provider factory, backed by
// providers.NewAddonSecretProvider — same lazy-init and test-injection
// rationale as the other doctor seams.
func (s *Server) getDoctorAddonSecretProviderFn() func(providers.AddonSecretProviderConfig) (providers.SecretProvider, error) {
	s.doctorAddonSecretProviderOnce.Do(func() {
		if s.doctorAddonSecretProviderFn == nil {
			s.doctorAddonSecretProviderFn = providers.NewAddonSecretProvider
		}
	})
	return s.doctorAddonSecretProviderFn
}

// doctorCheckAssumeRole is check 3: when a cross-account IAM role is in
// play for this cluster's connection, does a real STS AssumeRole with
// Sharko's own identity succeed? "not-applicable" when no role is
// involved at all.
func (s *Server) doctorCheckAssumeRole(ctx context.Context, clusterName string) doctorCheck {
	cctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	roleARN, region := s.doctorResolveRoleInPlay(cctx, clusterName)
	if roleARN == "" {
		return doctorCheck{
			ID:     doctorCheckAssumeRole,
			Status: doctorStatusNotApplicable,
			Detail: "This cluster's connection does not use a cross-account IAM role, so there is nothing to assume.",
		}
	}

	if err := s.getDoctorAssumeRoleFn()(cctx, roleARN, region); err != nil {
		// V3 E1: surface the AWS assume-role failure as a k8s Warning event.
		// The message names the cluster only — never the role ARN (it embeds
		// an AWS account id) and never the raw error string.
		s.emitWarning(events.ReasonAWSAssumeRoleFailed,
			fmt.Sprintf("AWS assume-role failed while checking cluster %q: Sharko's identity could not assume the cluster's IAM role.", clusterName))
		return doctorCheck{
			ID:     doctorCheckAssumeRole,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Sharko could not assume role %q: %s", roleARN, err.Error()),
			Fix:    verify.AssumeRoleHint(err),
		}
	}

	return doctorCheck{
		ID:     doctorCheckAssumeRole,
		Status: doctorStatusPass,
		Detail: fmt.Sprintf("Sharko successfully assumed role %q.", roleARN),
	}
}

// doctorResolveRoleInPlay determines the cross-account IAM role ARN (and a
// best-effort region hint) in play for this cluster's connection, WITHOUT
// fetching or minting any credentials — read-only introspection reused
// only by the assume-role check. Checked in order:
//
//  1. The per-cluster role_arn stored on the cluster's managed-clusters.yaml
//     record (V2-cleanup-62.2) — set for eks-token / secret-kubeconfig
//     backend registrations.
//  2. A role ARN embedded in the cluster's own ArgoCD cluster Secret
//     (awsAuthConfig.roleARN, or execProviderConfig's --role-arn flag —
//     V2-cleanup-88.2). role_arn on the register request is REJECTED
//     outright for inline-kubeconfig registrations (see
//     orchestrator/role_arn_stamp_test.go), so this is the ONLY place a
//     role lives for a cluster discovered from an existing ArgoCD install.
//
// Returns roleARN == "" when no role is involved in this connection at all.
func (s *Server) doctorResolveRoleInPlay(ctx context.Context, clusterName string) (roleARN, region string) {
	_, credsSource, perClusterRoleARN := s.credentialRouting(ctx, clusterName)
	if perClusterRoleARN != "" {
		return perClusterRoleARN, ""
	}

	router := s.credsRouter()
	if router == nil {
		return "", ""
	}

	var argoProvider *providers.ArgoCDProvider
	if ap, ok := router.Backend.(*providers.ArgoCDProvider); ok {
		// Single-path short circuit, mirroring ClusterCredsRouter.Fetch:
		// the configured backend IS the ArgoCD reader.
		argoProvider = ap
	} else if router.ArgoCDReaderFn != nil &&
		(credsSource == models.CredsSourceInlineKubeconfig || credsSource == "") {
		// Inline / unknown-source clusters route to the ArgoCD reader
		// (see ClusterCredsRouter.Fetch) — the same reader instance holds
		// the cluster Secret a role would be parsed from.
		if reader, err := router.ArgoCDReaderFn(); err == nil {
			if ap, ok := reader.(*providers.ArgoCDProvider); ok {
				argoProvider = ap
			}
		}
	}
	if argoProvider == nil {
		return "", ""
	}

	arn, rgn, ok, err := argoProvider.ResolveRoleARN(clusterName)
	if err != nil || !ok {
		return "", ""
	}
	return arn, rgn
}

// getDoctorK8sClientFn lazily builds (and caches, via sync.Once) the
// Server's kubeconfig-to-client builder, backed by
// remoteclient.NewClientFromKubeconfig — same lazy-init and test-injection
// rationale as getDoctorAssumeRoleFn / getAWSDetector.
func (s *Server) getDoctorK8sClientFn() func(kubeconfig []byte) (kubernetes.Interface, error) {
	s.doctorK8sClientOnce.Do(func() {
		if s.doctorK8sClientFn == nil {
			s.doctorK8sClientFn = remoteclient.NewClientFromKubeconfig
		}
	})
	return s.doctorK8sClientFn
}

// doctorCheckClusterAccess is check 4: does the cluster itself accept the
// credentials Sharko holds? Reuses verify.Stage1 — the SAME secret CRUD
// cycle the Test handler runs — rather than inventing a second write
// probe. "not-applicable" when check 1 could not produce credentials to
// test with at all. When Stage1 fails immediately after a successful
// assume-role check, the fix message names the L6/L12 distinction: AWS
// accepted the role, but the cluster's own access control does not yet
// trust it.
func (s *Server) doctorCheckClusterAccess(ctx context.Context, clusterName string, creds *providers.Kubeconfig, roleAssumeSucceeded bool) doctorCheck {
	if creds == nil {
		return doctorCheck{
			ID:     doctorCheckClusterAccess,
			Status: doctorStatusNotApplicable,
			Detail: "Skipped — Sharko could not read this cluster's connection credentials (see the first check above).",
		}
	}

	cctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	client, err := s.getDoctorK8sClientFn()(creds.Raw)
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckClusterAccess,
			Status: doctorStatusFail,
			Detail: "Sharko could not build a Kubernetes client from this cluster's credentials: " + err.Error(),
			Fix:    "Check that the cluster's stored credentials still point at a reachable API server.",
		}
	}

	result := verify.Stage1(cctx, client, verify.TestNamespace())
	if result.Success {
		return doctorCheck{
			ID:     doctorCheckClusterAccess,
			Status: doctorStatusPass,
			Detail: fmt.Sprintf("Sharko created, read back, and deleted a test Secret on cluster %q — the connection works end to end.", clusterName),
		}
	}

	fix := "Check Sharko's RBAC permissions on this cluster (the Diagnose tool gives a namespace-level permission breakdown)."
	if roleAssumeSucceeded {
		fix = "The role works in AWS, but the cluster doesn't trust it yet — add an EKS access entry (or aws-auth mapping) for this role."
	}
	return doctorCheck{
		ID:     doctorCheckClusterAccess,
		Status: doctorStatusFail,
		Detail: fmt.Sprintf("Sharko's connection test on cluster %q failed: %s", clusterName, result.ErrorMessage),
		Fix:    fix,
	}
}

// doctorCheckSecretOwnership is check 5 (V2-cleanup-89.5, refined by
// V2-cleanup-90.1): for a self-managed connection (connectionManagedBy:
// user), does its ArgoCD cluster Secret carry a tracking marker that may
// belong to another application — i.e. could it ALSO be rendered from Git
// by another ArgoCD Application, or is it just a plain Helm-installed
// secret carrying Helm's own release label? Reuses
// argosecrets.Manager.GetSecretOwnership — ONE Get that derives both the
// managed-by label and the foreign-tracking-owner signal from the same
// object, replacing the pre-90.1 two-Get pattern (GetManagedByLabel +
// GetTrackingOwner) that cost an extra API round trip and left a race
// window between the two reads. A verified (hard-confidence) tracking-id
// match fails the check exactly as before; a weaker (soft-confidence)
// signal — a mismatched tracking-id or a label-only match, which is also
// what a plain Helm release stamps — warns instead of failing, so a
// Helm-only user no longer sees a scary false-positive FAIL.
// Not-applicable for a Sharko-managed connection (Sharko is the Secret's
// sole writer there, so a foreign marker is a different, out-of-scope
// problem) and for a self-managed connection whose Secret the user hasn't
// created yet.
func (s *Server) doctorCheckSecretOwnership(ctx context.Context, clusterName string) doctorCheck {
	if s.argoSecretManager == nil {
		return doctorCheck{
			ID:     doctorCheckSecretOwnership,
			Status: doctorStatusNotApplicable,
			Detail: "Sharko has no ArgoCD cluster-secret manager configured, so it cannot inspect this cluster's connection secret.",
		}
	}

	cctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	ownership, found, err := s.argoSecretManager.GetSecretOwnership(cctx, clusterName)
	if err != nil {
		// GetSecretOwnership already treats a missing Secret as found=false,
		// err=nil (see below), so any error reaching here is a REAL read
		// failure — permission, timeout, or something else — never the
		// missing-secret case. The fix must name the actual problem instead
		// of the misleading "secret still exists" advice.
		return doctorCheck{
			ID:     doctorCheckSecretOwnership,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Sharko could not read cluster %q's ArgoCD connection secret: %s", clusterName, err.Error()),
			Fix:    fmt.Sprintf("Sharko couldn't read the secret: %s — check Sharko's RBAC on the argocd namespace.", err.Error()),
		}
	}
	if !found {
		return doctorCheck{
			ID:     doctorCheckSecretOwnership,
			Status: doctorStatusNotApplicable,
			Detail: fmt.Sprintf("Cluster %q has no ArgoCD connection secret yet, so there is nothing to check for foreign ownership.", clusterName),
		}
	}
	if ownership.ManagedBy == argosecrets.ManagedByValue {
		return doctorCheck{
			ID:     doctorCheckSecretOwnership,
			Status: doctorStatusNotApplicable,
			Detail: fmt.Sprintf("Cluster %q's connection secret is managed by Sharko directly — foreign-ownership checks only apply to self-managed (user-owned) connections.", clusterName),
		}
	}
	if !ownership.ForeignOwnerFound {
		return doctorCheck{
			ID:     doctorCheckSecretOwnership,
			Status: doctorStatusPass,
			Detail: fmt.Sprintf("Cluster %q's connection secret carries no tracking markers from another application.", clusterName),
		}
	}
	if ownership.ForeignOwnerConfidence == argosecrets.ConfidenceHard {
		return doctorCheck{
			ID:     doctorCheckSecretOwnership,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Cluster %q's connection secret is rendered by ArgoCD application %q — that application can overwrite Sharko's addon labels on it.", clusterName, ownership.ForeignOwnerAppName),
			Fix:    fmt.Sprintf("In application %q's manifest, make sure it doesn't define Sharko's addon labels and doesn't use the Replace sync option, or they will fight over this secret. See %s.", ownership.ForeignOwnerAppName, selfManagedConnectionsDocURL),
		}
	}
	return doctorCheck{
		ID:     doctorCheckSecretOwnership,
		Status: doctorStatusWarn,
		Detail: fmt.Sprintf("Cluster %q's connection secret may be managed by ArgoCD application or Helm release %q — the signal isn't strong enough to be sure it's ArgoCD.", clusterName, ownership.ForeignOwnerAppName),
		Fix:    fmt.Sprintf("If an ArgoCD application named %q renders this secret from Git, make sure its manifest doesn't define Sharko's addon labels and doesn't use the Replace sync option. See %s.", ownership.ForeignOwnerAppName, selfManagedConnectionsDocURL),
	}
}

// doctorCheckConnectivityApp is check 6 (V3 BUG-1): for a managed cluster
// labeled for connectivity-check, does the expected connectivity-check
// Application exist in ArgoCD? When a cluster's ArgoCD Secret carries
// sharko.dev/connectivity-check: enabled but ArgoCD has no generated
// connectivity-check-<cluster> Application, the most likely cause is a stale
// connectivity-check ApplicationSet selector that still matches on the
// pre-rename sharko.io/connectivity-check label — the appset never converged
// after the V2-cleanup-59 label rename. Not-applicable for clusters without
// the connectivity-check label and for clusters with real addons deployed
// (the placeholder check app intentionally yields to real addons, so a
// labeled cluster with no check app + a real addon is healthy, not drift).
func (s *Server) doctorCheckConnectivityApp(ctx context.Context, clusterName string) doctorCheck {
	if s.argoSecretManager == nil {
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusNotApplicable,
			Detail: "Sharko has no ArgoCD cluster-secret manager configured, so it cannot inspect this cluster's labels.",
		}
	}

	cctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	// Read the cluster's ArgoCD Secret to check its labels. GetSecretOwnership
	// doesn't expose labels, so use the manager's client to Get the full Secret.
	k8sClient, namespace, ok := s.k8sClientAndNamespace()
	if !ok {
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusNotApplicable,
			Detail: "Sharko has no Kubernetes client configured, so it cannot inspect this cluster's labels.",
		}
	}

	secret, err := k8sClient.CoreV1().Secrets(namespace).Get(cctx, clusterName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return doctorCheck{
				ID:     doctorCheckConnectivityApp,
				Status: doctorStatusNotApplicable,
				Detail: fmt.Sprintf("Cluster %q has no ArgoCD connection secret yet, so there is nothing to check.", clusterName),
			}
		}
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Sharko could not read cluster %q's ArgoCD connection secret: %s", clusterName, err.Error()),
			Fix:    fmt.Sprintf("Sharko couldn't read the secret: %s — check Sharko's RBAC on the argocd namespace.", err.Error()),
		}
	}

	// Check if the cluster Secret has the connectivity-check label.
	// Use the models.HasConnectivityCheckLabel helper which recognizes
	// both canonical and legacy keys.
	if !models.HasConnectivityCheckLabel(secret.Labels) {
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusNotApplicable,
			Detail: fmt.Sprintf("Cluster %q is not labeled for connectivity-check, so no check application is expected.", clusterName),
		}
	}

	// The cluster is labeled for the check. Query ArgoCD for the expected
	// connectivity-check-<cluster> Application.
	checkAppName := "connectivity-check-" + clusterName

	// Get the ArgoCD client and list all Applications, then search for the
	// specific check app. This matches the pattern connectivity_status.go uses.
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusFail,
			Detail: "Sharko has no active ArgoCD connection, so it cannot check for the connectivity-check application.",
			Fix:    "Configure an ArgoCD connection in Settings -> Connections, then run the doctor again.",
		}
	}

	apps, err := ac.ListApplications(cctx)
	if err != nil {
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Sharko could not list ArgoCD applications: %s", err.Error()),
			Fix:    "Check Sharko's RBAC permissions on ArgoCD — the service account must be able to list applications.",
		}
	}

	// Search for the connectivity-check application.
	var checkApp *models.ArgocdApplication
	for i := range apps {
		if apps[i].Name == checkAppName {
			checkApp = &apps[i]
			break
		}
	}

	if checkApp != nil {
		// The app exists — no drift detected.
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusPass,
			Detail: fmt.Sprintf("Cluster %q is labeled for connectivity-check and the expected application %q exists in ArgoCD.", clusterName, checkAppName),
		}
	}

	// The cluster is labeled but the app does NOT exist. This is the drift
	// signature from the grounding. However, check one more thing: does the
	// cluster have any real addon applications deployed? If yes, the
	// connectivity-check app intentionally yielded and this is NOT drift.
	// Check for any non-system addon app (regardless of health) — if present,
	// it means the check app should be absent.
	hasAnyAddon := false
	for i := range apps {
		app := &apps[i]
		// Skip system apps using the orchestrator package's helper. System apps
		// include the bootstrap root and any connectivity-check apps.
		if orchestrator.IsSharkoSystemApp(app.Name) {
			continue
		}
		// Check if this app targets our cluster via name-suffix matching
		// (the clusterHasHealthyAddon predicate pattern from connectivity_status.go).
		// Name-suffix matching is sufficient for this drift guard.
		if strings.HasSuffix(app.Name, "-"+clusterName) {
			hasAnyAddon = true
			break
		}
	}

	if hasAnyAddon {
		// The cluster has a real addon deployed, so the check app correctly
		// yielded — not drift, not applicable.
		return doctorCheck{
			ID:     doctorCheckConnectivityApp,
			Status: doctorStatusNotApplicable,
			Detail: fmt.Sprintf("Cluster %q is labeled for connectivity-check, but a real addon application is deployed so the check app correctly yielded.", clusterName),
		}
	}

	// The cluster is labeled, has no real addons, and the check app is
	// missing — this is the drift signature. Warn with a plain-English fix.
	return doctorCheck{
		ID:     doctorCheckConnectivityApp,
		Status: doctorStatusWarn,
		Detail: fmt.Sprintf("Cluster %q is labeled sharko.dev/connectivity-check: enabled, but the expected connectivity-check application is not present in ArgoCD — likely a stale ApplicationSet selector.", clusterName),
		Fix:    "Re-apply the current bootstrap templates to this hub cluster to refresh the connectivity-check ApplicationSet selector from sharko.io/connectivity-check (legacy) to sharko.dev/connectivity-check (current). The bootstrapped templates live in templates/bootstrap/.",
	}
}
