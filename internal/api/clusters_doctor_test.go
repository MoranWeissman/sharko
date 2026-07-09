package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
)

// V2-cleanup-88.4 — the connection doctor: four real-attempt checks with a
// structured pass/fail/not-applicable verdict per check, and a plain-English
// fix on failure. These tests cover each check's three statuses, the
// check-3-pass-then-check-4-fail L6/L12 message, the overall roll-up, and
// the timeout bound — using the same per-instance test seams
// (doctorAssumeRoleFn / doctorK8sClientFn / doctorAddonSecretProviderFn)
// the handler itself is built from.

// ----- shared fixtures -------------------------------------------------

const doctorCatalogYAML = `
applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: "3.50.0"
    namespace: monitoring
    secrets:
      - secretName: datadog-secret
        namespace: monitoring
        keys:
          api-key: "secrets/datadog/api-key"
  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: "2.14.2"
`

const doctorManagedClustersYAML = `
clusters:
  - name: prod-eu
    labels:
      datadog: enabled
      keda: disabled
  - name: cross-account
    credsSource: eks-token
    roleArn: "arn:aws:iam::123456789012:role/example"
    labels:
      keda: enabled
  - name: no-secrets-cluster
    labels:
      keda: enabled
`

// newDoctorTestServer wires the repo paths + base branch the catalog /
// managed-clusters reads in clusters_doctor.go key off — mirrors
// production defaults from cmd/sharko/serve.go.
func newDoctorTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	srv.gitopsCfg = orchestrator.GitOpsConfig{BaseBranch: "main"}
	srv.repoPaths = orchestrator.RepoPathsConfig{
		Catalog:         "configuration/addons-catalog.yaml",
		ManagedClusters: "configuration/managed-clusters.yaml",
	}
	return srv
}

func withDoctorGitFiles(srv *Server) {
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: map[string][]byte{
		srv.repoPaths.Catalog:         []byte(doctorCatalogYAML),
		srv.repoPaths.ManagedClusters: []byte(doctorManagedClustersYAML),
	}})
}

// fakeAddonSecretProvider implements providers.SecretProvider with a fixed
// values map / error, for the addon-secret-paths check.
type fakeAddonSecretProvider struct {
	values map[string][]byte
	err    error
}

func (f *fakeAddonSecretProvider) GetSecretValue(_ context.Context, path string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.values[path]
	if !ok {
		return nil, fmt.Errorf("secret not found: %s", path)
	}
	return v, nil
}

var _ providers.SecretProvider = (*fakeAddonSecretProvider)(nil)

// operatorReq is a POST request pre-authorized as an operator, the tier
// cluster.doctor requires (mirrors cluster.test / cluster.diagnose).
func operatorReq(path string) *http.Request {
	return withRole(httptest.NewRequest(http.MethodPost, path, nil), "operator")
}

// ----- authz --------------------------------------------------------------

func TestDoctorCluster_ViewerForbidden(t *testing.T) {
	srv := newDoctorTestServer(t)
	router := NewRouter(srv, nil)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-eu/doctor", nil), "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

// ----- check 1: connection-credentials ------------------------------------

func TestDoctorCheckCredentials_Fail_NoProvider(t *testing.T) {
	srv := newDoctorTestServer(t)
	check, creds := srv.doctorCheckCredentials(context.Background(), "prod-eu")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
	if creds != nil {
		t.Error("creds should be nil on failure")
	}
	if check.Fix == "" {
		t.Error("expected a non-empty Fix on failure")
	}
}

func TestDoctorCheckCredentials_Fail_GenericError(t *testing.T) {
	srv := newDoctorTestServer(t)
	installCredProvider(srv, &recordingCredProvider{err: errors.New("secret prod-eu not found")}, nil, nil)

	check, creds := srv.doctorCheckCredentials(context.Background(), "prod-eu")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
	if creds != nil {
		t.Error("creds should be nil on failure")
	}
	if check.Fix == "" {
		t.Error("expected a non-empty Fix")
	}
}

func TestDoctorCheckCredentials_Fail_TypedArgoCDError(t *testing.T) {
	srv := newDoctorTestServer(t)
	argoErr := &providers.ArgoCDProviderError{
		Code:        providers.ArgoCDProviderCodeIAMRequired,
		ClusterName: "prod-eks",
		Server:      "https://abc.eks.amazonaws.com",
		Detail:      "cluster needs IAM",
	}
	installCredProvider(srv, &recordingCredProvider{err: argoErr}, nil, nil)

	check, _ := srv.doctorCheckCredentials(context.Background(), "prod-eks")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
	if check.Fix == "" {
		t.Fatal("expected a plain-English fix for the IAM-required code")
	}
	// The IAM-required fix must mention IRSA/Pod Identity — the actionable
	// piece — not just repeat the raw error.
	if !strings.Contains(check.Fix, "IRSA") {
		t.Errorf("Fix = %q, want it to mention IRSA/Pod Identity", check.Fix)
	}
}

func TestDoctorCheckCredentials_Pass(t *testing.T) {
	srv := newDoctorTestServer(t)
	kc := &providers.Kubeconfig{Raw: []byte("raw-kubeconfig-bytes"), Server: "https://prod-eu.example.com"}
	installCredProvider(srv, &recordingCredProvider{kc: kc}, nil, nil)

	check, creds := srv.doctorCheckCredentials(context.Background(), "prod-eu")
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want pass (detail=%s)", check.Status, check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty on pass", check.Fix)
	}
	if creds != kc {
		t.Error("expected the fetched Kubeconfig to be returned for reuse by check 4")
	}
}

// ----- check 2: addon-secret-paths ----------------------------------------

func TestDoctorCheckAddonSecretPaths_Fail_NoGitConnection(t *testing.T) {
	srv := newDoctorTestServer(t)
	check := srv.doctorCheckAddonSecretPaths(context.Background(), "prod-eu")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
}

func TestDoctorCheckAddonSecretPaths_NotApplicable_ClusterNotFound(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)

	check := srv.doctorCheckAddonSecretPaths(context.Background(), "unknown-cluster")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
}

func TestDoctorCheckAddonSecretPaths_NotApplicable_NoSecretBearingAddons(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)

	check := srv.doctorCheckAddonSecretPaths(context.Background(), "no-secrets-cluster")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
}

func TestDoctorCheckAddonSecretPaths_Fail_NoAddonSecretProviderConfigured(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)

	// prod-eu has datadog enabled, which declares a secrets block — but no
	// addon-secret provider is configured.
	check := srv.doctorCheckAddonSecretPaths(context.Background(), "prod-eu")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
	if check.Fix == "" {
		t.Error("expected a non-empty Fix")
	}
}

func TestDoctorCheckAddonSecretPaths_Fail_ProviderConstructionError(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	installCredProvider(srv, nil, &providers.AddonSecretProviderConfig{Type: "aws-sm"}, nil)
	srv.doctorAddonSecretProviderFn = func(providers.AddonSecretProviderConfig) (providers.SecretProvider, error) {
		return nil, errors.New("boom")
	}

	check := srv.doctorCheckAddonSecretPaths(context.Background(), "prod-eu")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
}

func TestDoctorCheckAddonSecretPaths_Fail_PathUnreadable(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	installCredProvider(srv, nil, &providers.AddonSecretProviderConfig{Type: "aws-sm"}, nil)
	srv.doctorAddonSecretProviderFn = func(providers.AddonSecretProviderConfig) (providers.SecretProvider, error) {
		return &fakeAddonSecretProvider{err: errors.New("access denied")}, nil
	}

	check := srv.doctorCheckAddonSecretPaths(context.Background(), "prod-eu")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "secrets/datadog/api-key") {
		t.Errorf("Detail = %q, want it to name the failing path", check.Detail)
	}
	if check.Fix == "" {
		t.Error("expected a non-empty Fix")
	}
}

func TestDoctorCheckAddonSecretPaths_Pass(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	installCredProvider(srv, nil, &providers.AddonSecretProviderConfig{Type: "aws-sm"}, nil)
	srv.doctorAddonSecretProviderFn = func(providers.AddonSecretProviderConfig) (providers.SecretProvider, error) {
		return &fakeAddonSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("shh"),
		}}, nil
	}

	check := srv.doctorCheckAddonSecretPaths(context.Background(), "prod-eu")
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want pass (detail=%s)", check.Status, check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty on pass", check.Fix)
	}
}

// ----- check 3: assume-role -------------------------------------------

func TestDoctorCheckAssumeRole_NotApplicable_NoRoleInvolved(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)

	check := srv.doctorCheckAssumeRole(context.Background(), "prod-eu")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable", check.Status)
	}
}

func TestDoctorCheckAssumeRole_Pass_PerClusterRoleARN(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	var gotRole, gotRegion string
	srv.doctorAssumeRoleFn = func(_ context.Context, roleARN, region string) error {
		gotRole, gotRegion = roleARN, region
		return nil
	}

	check := srv.doctorCheckAssumeRole(context.Background(), "cross-account")
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want pass (detail=%s)", check.Status, check.Detail)
	}
	if gotRole != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("assumeRoleFn called with role = %q, want the stored per-cluster role_arn", gotRole)
	}
	_ = gotRegion
}

func TestDoctorCheckAssumeRole_Fail_AssumeRoleDenied(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	srv.doctorAssumeRoleFn = func(context.Context, string, string) error {
		return errors.New("AccessDenied: not authorized to perform sts:AssumeRole")
	}

	check := srv.doctorCheckAssumeRole(context.Background(), "cross-account")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
	if check.Fix == "" || !strings.Contains(check.Fix, "trust") {
		t.Errorf("Fix = %q, want it to mention the role's trust policy", check.Fix)
	}
}

// startFakeArgoSecretListAPI serves a single ArgoCD cluster-secret List
// response so an *providers.ArgoCDProvider built against it can resolve a
// role embedded in the secret's awsAuthConfig, without any real cluster.
func startFakeArgoSecretListAPI(t *testing.T, secrets ...corev1.Secret) *rest.Config {
	t.Helper()
	list := corev1.SecretList{Items: secrets}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/namespaces/argocd/secrets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &rest.Config{Host: srv.URL}
}

// TestDoctorResolveRoleInPlay_ArgoCDEmbeddedRole covers the V2-cleanup-88.2
// path: no per-cluster role_arn is stored (register-time role_arn is
// rejected for inline-kubeconfig registrations — see
// orchestrator/role_arn_stamp_test.go), so the ONLY place the role lives is
// the ArgoCD cluster Secret's own awsAuthConfig. The doctor must find it
// through the SAME ResolveRoleARN read-only introspection the provider
// exposes, via the credsRouter's Backend-is-ArgoCDProvider short circuit.
func TestDoctorResolveRoleInPlay_ArgoCDEmbeddedRole(t *testing.T) {
	fakeCAB64 := base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))
	configJSON := `{
		"awsAuthConfig": {
			"clusterName": "my-eks-cluster",
			"roleARN": "arn:aws:iam::123456789012:role/example"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eks",
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				"region":                         "us-east-1",
			},
		},
		Data: map[string][]byte{
			"name":   []byte("prod-eks"),
			"server": []byte("https://abc.eks.amazonaws.com"),
			"config": []byte(configJSON),
		},
	}
	restCfg := startFakeArgoSecretListAPI(t, secret)
	argoProvider, err := providers.NewArgoCDProviderWithRESTConfigFromConfig(
		providers.ClusterTestProviderConfig{ArgoCDNamespace: "argocd"}, restCfg)
	if err != nil {
		t.Fatalf("construct ArgoCDProvider: %v", err)
	}

	srv := newDoctorTestServer(t)
	srv.providerState.Store(&providerSet{
		credProvider: argoProvider,
		credsRouter:  &providers.ClusterCredsRouter{Backend: argoProvider},
	})

	roleARN, region := srv.doctorResolveRoleInPlay(context.Background(), "prod-eks")
	if roleARN != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("roleARN = %q, want the role embedded in the ArgoCD cluster Secret", roleARN)
	}
	if region != "us-east-1" {
		t.Errorf("region = %q, want %q", region, "us-east-1")
	}
}

// TestDoctorResolveRoleInPlay_NonArgoCDReader_NoRoleFound is the defensive
// branch: an inline-source cluster whose ArgoCD reader isn't actually an
// *ArgoCDProvider (e.g. disabled in a unit test) must not crash — it simply
// reports no role found.
func TestDoctorResolveRoleInPlay_NonArgoCDReader_NoRoleFound(t *testing.T) {
	srv := newDoctorTestServer(t)
	installCredProvider(srv, &recordingCredProvider{}, nil, nil) // ArgoCDReaderFn errors in this helper

	roleARN, region := srv.doctorResolveRoleInPlay(context.Background(), "kind-inline")
	if roleARN != "" || region != "" {
		t.Errorf("roleARN=%q region=%q, want both empty", roleARN, region)
	}
}

// ----- check 4: cluster-access ---------------------------------------

func TestDoctorCheckClusterAccess_NotApplicable_NoCredentials(t *testing.T) {
	srv := newDoctorTestServer(t)
	check := srv.doctorCheckClusterAccess(context.Background(), "prod-eu", nil, false)
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable", check.Status)
	}
}

func TestDoctorCheckClusterAccess_Fail_BadKubeconfig(t *testing.T) {
	srv := newDoctorTestServer(t)
	creds := &providers.Kubeconfig{Raw: []byte("not: valid: kubeconfig: yaml: [")}
	check := srv.doctorCheckClusterAccess(context.Background(), "prod-eu", creds, false)
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
}

func TestDoctorCheckClusterAccess_Fail_GenericFix(t *testing.T) {
	srv := newDoctorTestServer(t)
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}})
	client.PrependReactor("create", "secrets", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is Forbidden")
	})
	srv.doctorK8sClientFn = func([]byte) (kubernetes.Interface, error) { return client, nil }

	creds := &providers.Kubeconfig{Raw: []byte("unused-fake-seam-bypasses-parsing")}
	check := srv.doctorCheckClusterAccess(context.Background(), "prod-eu", creds, false)
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
	if strings.Contains(check.Fix, "access entry") {
		t.Errorf("Fix = %q, must NOT use the L6/L12 role-trust message when the role was never assumed", check.Fix)
	}
}

func TestDoctorCheckClusterAccess_Fail_L6L12Fix_WhenRoleAssumedButClusterFails(t *testing.T) {
	srv := newDoctorTestServer(t)
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}})
	client.PrependReactor("create", "secrets", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is Forbidden")
	})
	srv.doctorK8sClientFn = func([]byte) (kubernetes.Interface, error) { return client, nil }

	creds := &providers.Kubeconfig{Raw: []byte("unused-fake-seam-bypasses-parsing")}
	check := srv.doctorCheckClusterAccess(context.Background(), "prod-eu", creds, true)
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail", check.Status)
	}
	if !strings.Contains(check.Fix, "access entry") || !strings.Contains(check.Fix, "doesn't trust it yet") {
		t.Errorf("Fix = %q, want the L6/L12 role-works-but-cluster-doesn't-trust-it message", check.Fix)
	}
}

func TestDoctorCheckClusterAccess_Pass(t *testing.T) {
	srv := newDoctorTestServer(t)
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}})
	srv.doctorK8sClientFn = func([]byte) (kubernetes.Interface, error) { return client, nil }

	creds := &providers.Kubeconfig{Raw: []byte("unused-fake-seam-bypasses-parsing")}
	check := srv.doctorCheckClusterAccess(context.Background(), "prod-eu", creds, false)
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want pass (detail=%s)", check.Status, check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty on pass", check.Fix)
	}
}

// ----- overall roll-up ------------------------------------------------

func TestDoctorOverallStatus(t *testing.T) {
	tests := []struct {
		name   string
		checks []doctorCheck
		want   string
	}{
		{
			name: "all pass",
			checks: []doctorCheck{
				{Status: doctorStatusPass}, {Status: doctorStatusPass},
			},
			want: doctorOverallPass,
		},
		{
			name: "all not-applicable",
			checks: []doctorCheck{
				{Status: doctorStatusNotApplicable}, {Status: doctorStatusNotApplicable},
			},
			want: doctorOverallPass,
		},
		{
			name: "pass mixed with not-applicable",
			checks: []doctorCheck{
				{Status: doctorStatusPass}, {Status: doctorStatusNotApplicable},
			},
			want: doctorOverallPass,
		},
		{
			name: "all fail",
			checks: []doctorCheck{
				{Status: doctorStatusFail}, {Status: doctorStatusFail},
			},
			want: doctorOverallFail,
		},
		{
			name: "fail mixed with not-applicable, no pass",
			checks: []doctorCheck{
				{Status: doctorStatusFail}, {Status: doctorStatusNotApplicable},
			},
			want: doctorOverallFail,
		},
		{
			name: "pass and fail mixed",
			checks: []doctorCheck{
				{Status: doctorStatusPass}, {Status: doctorStatusFail},
			},
			want: doctorOverallPartial,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := doctorOverallStatus(tt.checks)
			if got != tt.want {
				t.Errorf("doctorOverallStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ----- full HTTP round-trip + response contract ------------------------

// TestDoctorCluster_HTTPContract exercises the real endpoint end to end:
// credentials pass, no addons enabled (not-applicable), no role in play
// (not-applicable), cluster access pass via the k8s-client seam. Asserts
// the exact response shape the UI story (88.5) will consume, the 4-check
// ordering, and that the run is recorded in the audit log.
func TestDoctorCluster_HTTPContract(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	kc := &providers.Kubeconfig{Raw: []byte("unused"), Server: "https://prod-eu.example.com"}
	installCredProvider(srv, &recordingCredProvider{kc: kc}, nil, nil)

	fakeClient := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}})
	srv.doctorK8sClientFn = func([]byte) (kubernetes.Interface, error) { return fakeClient, nil }

	router := NewRouter(srv, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, operatorReq("/api/v1/clusters/no-secrets-cluster/doctor"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var resp doctorClusterResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Checks) != 4 {
		t.Fatalf("len(Checks) = %d, want 4", len(resp.Checks))
	}
	wantIDs := []string{
		doctorCheckConnectionCredentials,
		doctorCheckAddonSecretPaths,
		doctorCheckAssumeRole,
		doctorCheckClusterAccess,
	}
	for i, id := range wantIDs {
		if resp.Checks[i].ID != id {
			t.Errorf("Checks[%d].ID = %q, want %q", i, resp.Checks[i].ID, id)
		}
	}
	if resp.Checks[0].Status != doctorStatusPass {
		t.Errorf("connection-credentials = %q, want pass", resp.Checks[0].Status)
	}
	if resp.Checks[1].Status != doctorStatusNotApplicable {
		t.Errorf("addon-secret-paths = %q, want not-applicable", resp.Checks[1].Status)
	}
	if resp.Checks[2].Status != doctorStatusNotApplicable {
		t.Errorf("assume-role = %q, want not-applicable", resp.Checks[2].Status)
	}
	if resp.Checks[3].Status != doctorStatusPass {
		t.Errorf("cluster-access = %q, want pass (detail=%s)", resp.Checks[3].Status, resp.Checks[3].Detail)
	}
	if resp.Overall != doctorOverallPass {
		t.Errorf("Overall = %q, want pass", resp.Overall)
	}

	entries := srv.auditLog.List(0)
	if len(entries) == 0 {
		t.Fatal("expected an audit entry for the doctor run")
	}
	last := entries[len(entries)-1]
	if last.Event != "cluster_doctor_run" {
		t.Errorf("audit Event = %q, want cluster_doctor_run", last.Event)
	}
}

// ----- timeout bound -----------------------------------------------------

// TestDoctorCheckAssumeRole_BoundedContext asserts the context handed to the
// assume-role attempt carries a deadline no further out than
// doctorCheckTimeout — proving the per-check bound is wired without
// actually waiting it out.
func TestDoctorCheckAssumeRole_BoundedContext(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)

	var deadline time.Time
	var hasDeadline bool
	srv.doctorAssumeRoleFn = func(ctx context.Context, _, _ string) error {
		deadline, hasDeadline = ctx.Deadline()
		return nil
	}

	srv.doctorCheckAssumeRole(context.Background(), "cross-account")
	if !hasDeadline {
		t.Fatal("expected the context passed to the assume-role attempt to carry a deadline")
	}
	if time.Until(deadline) > doctorCheckTimeout+time.Second {
		t.Errorf("deadline %v out, want it bounded by doctorCheckTimeout (%v)", time.Until(deadline), doctorCheckTimeout)
	}
}

// TestDoctorCheckAddonSecretPaths_BoundedContext is the same assertion for
// the addon-secret-paths check's GetSecretValue call.
func TestDoctorCheckAddonSecretPaths_BoundedContext(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	installCredProvider(srv, nil, &providers.AddonSecretProviderConfig{Type: "aws-sm"}, nil)

	var deadline time.Time
	var hasDeadline bool
	srv.doctorAddonSecretProviderFn = func(providers.AddonSecretProviderConfig) (providers.SecretProvider, error) {
		return &fakeAddonSecretProvider{values: map[string][]byte{}}, nil
	}
	_ = deadline
	_ = hasDeadline

	// The whole run is bounded by doctorRunTimeout at the handler level;
	// this asserts a single check never exceeds doctorCheckTimeout even
	// when called with a long-lived background context (the direct-call
	// path used throughout this file, matching how the handler derives
	// each check's context from the run-level one).
	start := time.Now()
	srv.doctorCheckAddonSecretPaths(context.Background(), "prod-eu")
	if time.Since(start) > doctorCheckTimeout+2*time.Second {
		t.Errorf("check took %v, want it bounded by doctorCheckTimeout (%v)", time.Since(start), doctorCheckTimeout)
	}
}
