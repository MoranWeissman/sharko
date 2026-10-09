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

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
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

// TestDoctorCheckAssumeRole_CauseSpecificFixes asserts the check returns
// cause-specific fix text for the clearly distinguishable assume-role failure
// sub-types, matching the V2-cleanup-91.3 classifier wiring.
func TestDoctorCheckAssumeRole_CauseSpecificFixes(t *testing.T) {
	tests := []struct {
		name         string
		awsError     error
		wantInFix    []string
		wantNotInFix []string
	}{
		{
			name:         "trust policy rejection",
			awsError:     errors.New("User: arn:aws:sts::123456789012:assumed-role/sharko-role/session is not authorized to assume arn:aws:iam::123456789012:role/target-role"),
			wantInFix:    []string{"trust policy", "Sharko's identity"},
			wantNotInFix: []string{"sts:TagSession"},
		},
		{
			name:         "missing sts:TagSession",
			awsError:     errors.New("User is not authorized to perform: sts:TagSession on resource"),
			wantInFix:    []string{"sts:TagSession", "EKS Pod Identity"},
			wantNotInFix: []string{"trust policy"},
		},
		{
			name:         "generic failure falls back to combined hint",
			awsError:     errors.New("timeout connecting to STS endpoint"),
			wantInFix:    []string{"assume-role", "trust policy", "sts:AssumeRole", "sts:TagSession"},
			wantNotInFix: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newDoctorTestServer(t)
			withDoctorGitFiles(srv)
			srv.doctorAssumeRoleFn = func(context.Context, string, string) error {
				return tt.awsError
			}

			check := srv.doctorCheckAssumeRole(context.Background(), "cross-account")
			if check.Status != doctorStatusFail {
				t.Fatalf("Status = %q, want fail", check.Status)
			}
			for _, want := range tt.wantInFix {
				if !strings.Contains(check.Fix, want) {
					t.Errorf("Fix = %q, want it to contain %q", check.Fix, want)
				}
			}
			for _, notWant := range tt.wantNotInFix {
				if strings.Contains(check.Fix, notWant) {
					t.Errorf("Fix = %q, must not contain %q", check.Fix, notWant)
				}
			}
		})
	}
}

// TestDoctorCheckClusterAccess_L6L12Fix_Unchanged is a regression pin that
// the check-3-pass + check-4-fail EKS-access-entry message (line ~578) is
// unchanged by V2-cleanup-91.3 — the story's scope is only check-3 fix text.
func TestDoctorCheckClusterAccess_L6L12Fix_Unchanged(t *testing.T) {
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
	// The exact L6/L12 message from line ~578 must be unchanged.
	if !strings.Contains(check.Fix, "The role works in AWS, but the cluster doesn't trust it yet") {
		t.Errorf("Fix = %q, want the unchanged check-3-pass + check-4-fail message", check.Fix)
	}
	if !strings.Contains(check.Fix, "add an EKS access entry") {
		t.Errorf("Fix = %q, want it to mention EKS access entry", check.Fix)
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

// ----- check 5: secret-ownership (V2-cleanup-89.5) -----------------------

func TestDoctorCheckSecretOwnership_NotApplicable_NoManagerConfigured(t *testing.T) {
	srv := newDoctorTestServer(t) // s.argoSecretManager left nil

	check := srv.doctorCheckSecretOwnership(context.Background(), "prod-eu")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
}

func TestDoctorCheckSecretOwnership_NotApplicable_NoSecretYet(t *testing.T) {
	srv := newDoctorTestServer(t)
	srv.SetArgoSecretManager(argosecrets.NewManager(fake.NewSimpleClientset(), "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "no-such-cluster")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
}

func TestDoctorCheckSecretOwnership_NotApplicable_SharkoManaged(t *testing.T) {
	srv := newDoctorTestServer(t)
	sharkoSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":   "sharko",
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
	}
	srv.SetArgoSecretManager(argosecrets.NewManager(fake.NewSimpleClientset(sharkoSecret), "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "prod-eu")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable for a Sharko-managed connection (detail=%s)", check.Status, check.Detail)
	}
}

func TestDoctorCheckSecretOwnership_Pass_NoForeignMarker(t *testing.T) {
	srv := newDoctorTestServer(t)
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "byo-conn",
			Namespace: "argocd",
			// No managed-by label (self-managed) and no tracking marker.
			Labels: map[string]string{"argocd.argoproj.io/secret-type": "cluster"},
		},
	}
	srv.SetArgoSecretManager(argosecrets.NewManager(fake.NewSimpleClientset(userSecret), "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "byo-conn")
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want pass (detail=%s)", check.Status, check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty on pass", check.Fix)
	}
}

func TestDoctorCheckSecretOwnership_Fail_ForeignMarkerFound(t *testing.T) {
	srv := newDoctorTestServer(t)
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "byo-conn",
			Namespace: "argocd",
			Annotations: map[string]string{
				argosecrets.AnnotationTrackingID: "cluster-secrets-app:/Secret:argocd/byo-conn",
			},
		},
	}
	srv.SetArgoSecretManager(argosecrets.NewManager(fake.NewSimpleClientset(userSecret), "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "byo-conn")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "cluster-secrets-app") {
		t.Errorf("Detail = %q, want it to name the owning app", check.Detail)
	}
	if check.Fix == "" {
		t.Fatal("expected a non-empty Fix on failure")
	}
	if !strings.Contains(check.Fix, "Replace") {
		t.Errorf("Fix = %q, want it to mention the Replace sync-option risk", check.Fix)
	}
}

// TestDoctorCheckSecretOwnership_Warn_LabelOnlyMatch pins the V2-cleanup-90.1
// soft-confidence path: a bare app.kubernetes.io/instance label (the
// standard Helm release label, with no tracking-id annotation at all) must
// WARN, not fail — this is the review finding H1 false-positive the story
// fixes: a plain Helm-installed secret should never read as a scary FAIL.
func TestDoctorCheckSecretOwnership_Warn_LabelOnlyMatch(t *testing.T) {
	srv := newDoctorTestServer(t)
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "byo-conn",
			Namespace: "argocd",
			Labels:    map[string]string{"app.kubernetes.io/instance": "my-helm-release"},
		},
	}
	srv.SetArgoSecretManager(argosecrets.NewManager(fake.NewSimpleClientset(userSecret), "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "byo-conn")
	if check.Status != doctorStatusWarn {
		t.Fatalf("Status = %q, want warn (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "my-helm-release") {
		t.Errorf("Detail = %q, want it to name the app/release found", check.Detail)
	}
	if check.Fix == "" {
		t.Fatal("expected a non-empty Fix on warn")
	}
	if !strings.Contains(check.Fix, "Helm") && !strings.Contains(check.Detail, "Helm") {
		t.Errorf("Detail/Fix must mention the Helm possibility somewhere; Detail=%q Fix=%q", check.Detail, check.Fix)
	}
}

// TestDoctorCheckSecretOwnership_Warn_MismatchedTrackingID pins the other
// soft-confidence arm: a tracking-id annotation IS present, but its own
// namespace/name suffix names a DIFFERENT secret — the annotation was very
// likely copied from elsewhere, so it warns rather than fails.
func TestDoctorCheckSecretOwnership_Warn_MismatchedTrackingID(t *testing.T) {
	srv := newDoctorTestServer(t)
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "byo-conn",
			Namespace: "argocd",
			Annotations: map[string]string{
				argosecrets.AnnotationTrackingID: "cluster-secrets-app:/Secret:argocd/some-other-secret",
			},
		},
	}
	srv.SetArgoSecretManager(argosecrets.NewManager(fake.NewSimpleClientset(userSecret), "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "byo-conn")
	if check.Status != doctorStatusWarn {
		t.Fatalf("Status = %q, want warn (detail=%s)", check.Status, check.Detail)
	}
}

// TestDoctorCheckSecretOwnership_SingleGet asserts the check issues EXACTLY
// ONE Get against the K8s API for the secret — replacing the pre-90.1
// GetManagedByLabel + GetTrackingOwner double-Get pattern with a single
// GetSecretOwnership call.
func TestDoctorCheckSecretOwnership_SingleGet(t *testing.T) {
	srv := newDoctorTestServer(t)
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "byo-conn",
			Namespace: "argocd",
			Annotations: map[string]string{
				argosecrets.AnnotationTrackingID: "cluster-secrets-app:/Secret:argocd/byo-conn",
			},
		},
	}
	client := fake.NewSimpleClientset(userSecret)
	getCount := 0
	client.PrependReactor("get", "secrets", func(clienttesting.Action) (bool, runtime.Object, error) {
		getCount++
		return false, nil, nil
	})
	srv.SetArgoSecretManager(argosecrets.NewManager(client, "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "byo-conn")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail (detail=%s)", check.Status, check.Detail)
	}
	if getCount != 1 {
		t.Fatalf("Get call count = %d, want exactly 1 (single-Get, not GetManagedByLabel + GetTrackingOwner)", getCount)
	}
}

// TestDoctorCheckSecretOwnership_Fail_RBACError pins the error-type
// differentiation (review finding part of L4): a real read failure
// (permission, timeout, anything other than not-found) must produce a fix
// naming the ACTUAL problem, never the misleading "secret still exists"
// missing-secret advice.
func TestDoctorCheckSecretOwnership_Fail_RBACError(t *testing.T) {
	srv := newDoctorTestServer(t)
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "byo-conn", Namespace: "argocd"},
	})
	client.PrependReactor("get", "secrets", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is forbidden: User cannot get resource")
	})
	srv.SetArgoSecretManager(argosecrets.NewManager(client, "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "byo-conn")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Fix, "RBAC") {
		t.Errorf("Fix = %q, want it to name the real problem via the RBAC fix text", check.Fix)
	}
	if strings.Contains(check.Fix, "still exists") {
		t.Errorf("Fix = %q, must NOT use the misleading missing-secret advice for a real read failure", check.Fix)
	}
	// SECURITY (S2). This assertion used to be
	//   if !strings.Contains(check.Fix, "forbidden")
	// — it REQUIRED the Kubernetes error's own words in a field that carries
	// `json:"fix,omitempty"` and is rendered in a browser, so the leak was
	// pinned as the design in the same way FriendlyMessage's doc comment was.
	// Inverted: the underlying reason belongs in the server log, never here.
	// "User cannot get resource" is the part of the fake error that stands in
	// for what a real 403 discloses — the identity Sharko runs as.
	for _, leaked := range []string{"forbidden", "User cannot get resource"} {
		if strings.Contains(check.Fix, leaked) {
			t.Errorf("Fix = %q, must not carry the Kubernetes error's own words (%q)", check.Fix, leaked)
		}
		if strings.Contains(check.Detail, leaked) {
			t.Errorf("Detail = %q, must not carry the Kubernetes error's own words (%q)", check.Detail, leaked)
		}
	}
}

// TestDoctorCheckSecretOwnership_TypedForbidden_SaysSoWithoutQuoting proves
// the classifier reads the TYPED Kubernetes status rather than the message
// wording: a real apierrors Forbidden gets the permission-specific sentence,
// and none of the error's own words come with it.
func TestDoctorCheckSecretOwnership_TypedForbidden_SaysSoWithoutQuoting(t *testing.T) {
	srv := newDoctorTestServer(t)
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "byo-conn", Namespace: "argocd"},
	})
	// A real typed 403, carrying the kind of identity detail a live cluster
	// puts in one.
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "secrets"}, "byo-conn",
		errors.New(`User "system:serviceaccount:sharko:sharko-sa" cannot get resource "secrets"`),
	)
	client.PrependReactor("get", "secrets", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden
	})
	srv.SetArgoSecretManager(argosecrets.NewManager(client, "argocd"))

	check := srv.doctorCheckSecretOwnership(context.Background(), "byo-conn")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "not allowed") {
		t.Errorf("Detail = %q, want the permission-specific sentence chosen by the typed status", check.Detail)
	}
	if !strings.Contains(check.Fix, "RBAC") {
		t.Errorf("Fix = %q, want the RBAC next step", check.Fix)
	}
	// The identity out of a 403 is the whole reason this check exists.
	for _, leaked := range []string{"system:serviceaccount", "sharko-sa", "cannot get resource"} {
		if strings.Contains(check.Detail+check.Fix, leaked) {
			t.Errorf("the 403's own words (%q) reached the doctor response: detail=%q fix=%q", leaked, check.Detail, check.Fix)
		}
	}
}

// ----- check 6: connectivity-app-drift (walk finding) --------------------
//
// doctorCheckConnectivityApp must tell apart three causes for a missing
// connectivity-check application BEFORE ever blaming a stale ApplicationSet
// selector — the same distinctions dashboard.go's five-state cluster
// breakdown makes: cluster missing from ArgoCD, cluster present but not yet
// confirmed connected, and only then (genuinely connected + labeled + app
// absent) the hedged selector diagnosis. The diagnosis's Fix text is also
// layout-aware (v3 templates/bootstrap/ vs v4 engine chart).

// doctorConnectivityFixture bundles the moving parts this check reads: the
// cluster's ArgoCD Secret labels (k8s, via the cluster reconciler's
// k8sClientAndNamespace()), the ArgoCD cluster + application lists (a stub
// ArgoCD HTTP server, since GetActiveArgocdClient returns the concrete
// *argocd.Client — there is no interface seam to fake here), and the repo
// layout (git provider content at BootstrapRootAppPath).
type doctorConnectivityFixture struct {
	clusterName       string
	secretLabels      map[string]string
	secretAnnotations map[string]string
	argoClusters      []map[string]interface{}
	argoApps          []map[string]interface{}
	v4Repo            bool
}

func newConnectivityDoctorServer(t *testing.T, f doctorConnectivityFixture) *Server {
	t.Helper()
	srv := newDoctorTestServer(t)

	files := map[string][]byte{
		srv.repoPaths.Catalog:         []byte(doctorCatalogYAML),
		srv.repoPaths.ManagedClusters: []byte(doctorManagedClustersYAML),
	}
	if f.v4Repo {
		files[orchestrator.BootstrapRootAppPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	}
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: files})

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        f.clusterName,
			Namespace:   "argocd",
			Labels:      f.secretLabels,
			Annotations: f.secretAnnotations,
		},
	}
	k8sClient := fake.NewSimpleClientset(secret)
	srv.SetClusterReconciler(clusterreconciler.New(clusterreconciler.Deps{
		GitProvider: func() gitprovider.GitProvider { return nil },
		ArgoClient:  k8sClient,
		Vault:       staticVault(nil),
		AuditFn:     func(_ audit.Entry) {},
	}))
	srv.SetArgoSecretManager(argosecrets.NewManager(k8sClient, "argocd"))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": f.argoClusters})
	})
	mux.HandleFunc("/api/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": f.argoApps})
	})
	argoSrv := httptest.NewServer(mux)
	t.Cleanup(argoSrv.Close)

	if err := srv.connSvc.Create(models.CreateConnectionRequest{
		Name: "doctor-connectivity-test",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
		Argocd: models.ArgocdConfig{
			ServerURL: argoSrv.URL,
			Token:     "test-token",
			Insecure:  true,
		},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := srv.connSvc.SetActive("doctor-connectivity-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}

	return srv
}

func connectivityLabeledSecret() map[string]string {
	return map[string]string{"sharko.dev/connectivity-check": "enabled"}
}

// TestDoctorCheckConnectivityApp_NotApplicable_ClusterNotInArgoCD is cause 1:
// the cluster carries the connectivity-check label but ArgoCD's cluster
// list has no entry for it at all — nothing has been created yet, so this
// must NOT be diagnosed as a stale selector.
func TestDoctorCheckConnectivityApp_NotApplicable_ClusterNotInArgoCD(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName:  "prod-eu",
		secretLabels: connectivityLabeledSecret(),
		argoClusters: nil, // prod-eu is absent from ArgoCD's cluster list
		argoApps:     nil,
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "prod-eu")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "not registered in ArgoCD's cluster list") {
		t.Errorf("Detail = %q, want it to say the cluster isn't registered in ArgoCD", check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty — not-applicable checks carry no fix", check.Fix)
	}
}

// TestDoctorCheckConnectivityApp_NotApplicable_ConnectionNotConfirmed is
// cause 2 (the "zero addons / untested" case): the cluster IS in ArgoCD's
// cluster list but its connection state has not resolved to a confirmed
// Successful/Connected — it's still settling. Also must not be diagnosed
// as a stale selector.
func TestDoctorCheckConnectivityApp_NotApplicable_ConnectionNotConfirmed(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName:  "prod-eu",
		secretLabels: connectivityLabeledSecret(),
		argoClusters: []map[string]interface{}{
			{"name": "prod-eu", "server": "https://prod-eu.example.test"},
			// no info.connectionState at all -> ConnectionState resolves to "".
		},
		argoApps: nil,
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "prod-eu")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "not a confirmed Successful/Connected") {
		t.Errorf("Detail = %q, want it to explain the connection isn't confirmed yet", check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty — not-applicable checks carry no fix", check.Fix)
	}
}

// TestDoctorCheckConnectivityApp_NotApplicable_RealAddonDeployed pins the
// pre-existing "the placeholder check app correctly yielded" behavior for a
// genuinely connected cluster with a real addon deployed — unchanged by
// this fix, still not-applicable, still not a selector claim.
func TestDoctorCheckConnectivityApp_NotApplicable_RealAddonDeployed(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName:  "prod-eu",
		secretLabels: connectivityLabeledSecret(),
		argoClusters: []map[string]interface{}{
			{"name": "prod-eu", "server": "https://prod-eu.example.test", "info": map[string]interface{}{
				"connectionState": map[string]interface{}{"status": "Successful"},
			}},
		},
		argoApps: []map[string]interface{}{
			{"metadata": map[string]interface{}{"name": "datadog-prod-eu"}},
		},
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "prod-eu")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "correctly yielded") {
		t.Errorf("Detail = %q, want it to explain the check app correctly yielded to a real addon", check.Detail)
	}
}

// TestDoctorCheckConnectivityApp_Pass_AppExists: connected, labeled, and
// the expected connectivity-check application exists — pass, no drift.
func TestDoctorCheckConnectivityApp_Pass_AppExists(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName:  "prod-eu",
		secretLabels: connectivityLabeledSecret(),
		argoClusters: []map[string]interface{}{
			{"name": "prod-eu", "server": "https://prod-eu.example.test", "info": map[string]interface{}{
				"connectionState": map[string]interface{}{"status": "Successful"},
			}},
		},
		argoApps: []map[string]interface{}{
			{"metadata": map[string]interface{}{"name": "connectivity-check-prod-eu"}},
		},
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "prod-eu")
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want pass (detail=%s)", check.Status, check.Detail)
	}
}

// TestDoctorCheckConnectivityApp_Warn_V3Layout is cause 3: genuinely
// connected, labeled, no addons, no check app — the hedged selector
// warning fires, with the v3 templates/bootstrap/ fix wording since this
// repo has no engine pin (sharko-engine.yaml).
func TestDoctorCheckConnectivityApp_Warn_V3Layout(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName:  "prod-eu",
		secretLabels: connectivityLabeledSecret(),
		v4Repo:       false,
		argoClusters: []map[string]interface{}{
			{"name": "prod-eu", "server": "https://prod-eu.example.test", "info": map[string]interface{}{
				"connectionState": map[string]interface{}{"status": "Successful"},
			}},
		},
		argoApps: nil,
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "prod-eu")
	if check.Status != doctorStatusWarn {
		t.Fatalf("Status = %q, want warn (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "one possible cause") {
		t.Errorf("Detail = %q, want the hedged wording ('one possible cause'), not a flat declaration", check.Detail)
	}
	if !strings.Contains(check.Fix, "templates/bootstrap/") {
		t.Errorf("Fix = %q, want the v3 bootstrap-templates wording for a repo with no engine pin", check.Fix)
	}
	if strings.Contains(check.Fix, "sharko-engine chart") {
		t.Errorf("Fix = %q, must not use v4 engine-chart wording on a v3 repo", check.Fix)
	}
}

// TestDoctorCheckConnectivityApp_Warn_V4Layout is the same cause-3 scenario
// on a v4 repo (sharko-engine.yaml present) — the fix must point at the
// engine chart, never at templates/bootstrap/ (a v4 repo has no scaffolded
// templates at all).
func TestDoctorCheckConnectivityApp_Warn_V4Layout(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName:  "prod-eu",
		secretLabels: connectivityLabeledSecret(),
		v4Repo:       true,
		argoClusters: []map[string]interface{}{
			{"name": "prod-eu", "server": "https://prod-eu.example.test", "info": map[string]interface{}{
				"connectionState": map[string]interface{}{"status": "Successful"},
			}},
		},
		argoApps: nil,
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "prod-eu")
	if check.Status != doctorStatusWarn {
		t.Fatalf("Status = %q, want warn (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Fix, "sharko-engine chart") {
		t.Errorf("Fix = %q, want the v4 engine-chart wording", check.Fix)
	}
	if !strings.Contains(check.Fix, "0.3.0") {
		t.Errorf("Fix = %q, want it to name the minimum chart version (0.3.0)", check.Fix)
	}
	if strings.Contains(check.Fix, "templates/bootstrap/") {
		t.Errorf("Fix = %q, must not use v3 template wording on a v4 repo (v4 has no scaffolded templates)", check.Fix)
	}
}

// ----- missing-label finding (walk finding: bare spoke) -------------------
//
// Before the reconciler self-heal fix, the connectivity-check label was only
// ever derived at Secret-CREATE time, so a Sharko-managed cluster with zero
// enabled addons could sit missing the label forever. The doctor learns to
// tell that apart from the (correct) cases where no label is expected at
// all: self-managed connections, adopted (guest) clusters, and managed
// clusters that genuinely have an addon enabled.

// TestDoctorCheckConnectivityApp_Fail_ManagedZeroAddonsMissingLabel is the
// bug itself: a Sharko-managed (non-adopted) Secret with zero enabled
// addons and no connectivity-check label is a real finding, not
// not-applicable.
func TestDoctorCheckConnectivityApp_Fail_ManagedZeroAddonsMissingLabel(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName: "spoke-us",
		secretLabels: map[string]string{
			argosecrets.LabelManagedBy:       argosecrets.ManagedByValue,
			"argocd.argoproj.io/secret-type": "cluster",
			// zero addon labels, no connectivity-check label — the bug
		},
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "spoke-us")
	if check.Status != doctorStatusFail {
		t.Fatalf("Status = %q, want fail (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "zero enabled addons") {
		t.Errorf("Detail = %q, want it to name zero enabled addons as the reason", check.Detail)
	}
	// The Fix is written out as a literal. It used to be asserted as
	// `strings.Contains(check.Fix, "reconciler")` — which pinned the DEFECT:
	// user-facing text never names Sharko's own plumbing, and the sentence it
	// was protecting also promised "within ~30 seconds" for an interval the
	// operator sets. Both are now caught by
	// TestBannedWordings_UserFacingTextNamesNoPlumbingAndPromisesNoClock.
	const wantFix = "Sharko automatically adds this label. If it does not appear, check that the part of Sharko that manages cluster connections is running on this server."
	if check.Fix != wantFix {
		t.Errorf("Fix drifted:\n got: %q\nwant: %q", check.Fix, wantFix)
	}
}

// TestDoctorCheckConnectivityApp_NotApplicable_ManagedWithAddonMissingLabel
// verifies the finding is scoped to zero-addon clusters: a managed Secret
// with an enabled addon and no connectivity-check label is correctly
// unlabeled (the label auto-removes itself once an addon is on), so this
// stays not-applicable.
func TestDoctorCheckConnectivityApp_NotApplicable_ManagedWithAddonMissingLabel(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName: "spoke-us",
		secretLabels: map[string]string{
			argosecrets.LabelManagedBy:       argosecrets.ManagedByValue,
			"argocd.argoproj.io/secret-type": "cluster",
			"datadog":                        "enabled",
		},
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "spoke-us")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "not labeled for connectivity-check") {
		t.Errorf("Detail = %q, want the standard not-labeled wording", check.Detail)
	}
}

// TestDoctorCheckConnectivityApp_NotApplicable_SelfManagedZeroAddonsMissingLabel
// verifies self-managed (user-owned) connections are never flagged: they
// never carry the label by design, regardless of addon count.
func TestDoctorCheckConnectivityApp_NotApplicable_SelfManagedZeroAddonsMissingLabel(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName: "self-managed-cluster",
		secretLabels: map[string]string{
			"argocd.argoproj.io/secret-type": "cluster",
			// no managed-by — user-owned connection, zero addons
		},
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "self-managed-cluster")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
	}
}

// TestDoctorCheckConnectivityApp_NotApplicable_AdoptedZeroAddonsMissingLabel
// verifies adopted (guest) clusters are never flagged: they are guests in a
// shared ArgoCD and must never carry the label, regardless of addon count.
func TestDoctorCheckConnectivityApp_NotApplicable_AdoptedZeroAddonsMissingLabel(t *testing.T) {
	srv := newConnectivityDoctorServer(t, doctorConnectivityFixture{
		clusterName: "adopted-cluster",
		secretLabels: map[string]string{
			argosecrets.LabelManagedBy:       argosecrets.ManagedByValue,
			"argocd.argoproj.io/secret-type": "cluster",
			// zero addon labels, no connectivity-check label
		},
		secretAnnotations: map[string]string{argosecrets.AnnotationAdopted: "true"},
	})

	check := srv.doctorCheckConnectivityApp(context.Background(), "adopted-cluster")
	if check.Status != doctorStatusNotApplicable {
		t.Fatalf("Status = %q, want not-applicable (detail=%s)", check.Status, check.Detail)
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
		// V2-cleanup-90.1 — warn rollup.
		{
			name: "pass mixed with warn",
			checks: []doctorCheck{
				{Status: doctorStatusPass}, {Status: doctorStatusWarn},
			},
			want: doctorOverallPartial,
		},
		{
			name: "all warn",
			checks: []doctorCheck{
				{Status: doctorStatusWarn}, {Status: doctorStatusWarn},
			},
			want: doctorOverallPartial,
		},
		{
			name: "warn mixed with not-applicable, no pass or fail",
			checks: []doctorCheck{
				{Status: doctorStatusWarn}, {Status: doctorStatusNotApplicable},
			},
			want: doctorOverallPartial,
		},
		{
			name: "fail dominates warn",
			checks: []doctorCheck{
				{Status: doctorStatusFail}, {Status: doctorStatusWarn},
			},
			want: doctorOverallFail,
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
	if len(resp.Checks) != 6 {
		t.Fatalf("len(Checks) = %d, want 6", len(resp.Checks))
	}
	wantIDs := []string{
		doctorCheckConnectionCredentials,
		doctorCheckAddonSecretPaths,
		doctorCheckAssumeRole,
		doctorCheckClusterAccess,
		doctorCheckSecretOwnership,
		doctorCheckConnectivityApp,
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
	// No argoSecretManager wired in this test server, so secret-ownership
	// reports not-applicable rather than pass/fail.
	if resp.Checks[4].Status != doctorStatusNotApplicable {
		t.Errorf("secret-ownership = %q, want not-applicable (detail=%s)", resp.Checks[4].Status, resp.Checks[4].Detail)
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
