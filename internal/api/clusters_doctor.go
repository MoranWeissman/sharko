package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/capabilities"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	"github.com/MoranWeissman/sharko/internal/verify"
)

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
)

// Check statuses (doctorCheck.Status).
const (
	doctorStatusPass          = "pass"
	doctorStatusFail          = "fail"
	doctorStatusNotApplicable = "not-applicable"
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
	Status string `json:"status" enums:"pass,fail,not-applicable"`
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
// @Description Runs up to four real-attempt checks against the named cluster's
// @Description connection and returns a structured pass/fail/not-applicable
// @Description verdict per check, each with a plain-English fix on failure:
// @Description (1) can Sharko read the cluster's connection credentials,
// @Description (2) can Sharko read every provider path an enabled addon's
// @Description secrets need, (3) if a cross-account IAM role is in play, can
// @Description Sharko assume it, and (4) does the cluster itself accept the
// @Description resulting token (reuses the existing Stage-1 secret CRUD cycle).
// @Description Every check is a real attempt, never IAM policy simulation, and
// @Description read-only except check 4, which reuses Stage-1's existing
// @Description create/read/delete canary secret. The whole run is bounded to
// @Description about 30 seconds.
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

	checks := []doctorCheck{credCheck, secretsCheck, assumeCheck, accessCheck}
	resp := doctorClusterResponse{Checks: checks, Overall: doctorOverallStatus(checks)}

	slog.Info("[cluster-doctor] complete", "name", name, "overall", resp.Overall)
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_doctor_run",
		Resource: fmt.Sprintf("cluster:%s", name),
		Detail:   fmt.Sprintf("overall=%s", resp.Overall),
	})

	writeJSON(w, http.StatusOK, resp)
}

// doctorOverallStatus rolls up the four checks: "fail" when nothing that
// ran passed, "partial" when some passed and some failed, "pass" when
// nothing failed (regardless of how many checks were not-applicable).
func doctorOverallStatus(checks []doctorCheck) string {
	hasFail := false
	hasPass := false
	for _, c := range checks {
		switch c.Status {
		case doctorStatusFail:
			hasFail = true
		case doctorStatusPass:
			hasPass = true
		}
	}
	switch {
	case hasFail && hasPass:
		return doctorOverallPartial
	case hasFail:
		return doctorOverallFail
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

	catalogData, err := gp.GetFileContent(cctx, s.repoPaths.Catalog, s.gitopsCfg.BaseBranch)
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

	clusterData, err := gp.GetFileContent(cctx, s.repoPaths.ManagedClusters, s.gitopsCfg.BaseBranch)
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
		return doctorCheck{
			ID:     doctorCheckAssumeRole,
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("Sharko could not assume role %q: %s", roleARN, err.Error()),
			Fix:    fmt.Sprintf("In AWS, check that role %q trusts Sharko's own IAM identity in its trust policy, and that Sharko's identity has sts:AssumeRole permission on it.", roleARN),
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
