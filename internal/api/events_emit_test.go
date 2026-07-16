package api

// events_emit_test.go — V3 E1 proof that each wired failure surface emits the
// expected k8s Warning event with a stable Reason and no secret material.
// Uses record.FakeRecorder (buffered channel of "<type> <reason> <message>")
// injected via events.NewRecorderForTest so assertions are synchronous.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"k8s.io/client-go/tools/record"
)

// attachFakeRecorder wires a FakeRecorder-backed EventRecorder onto srv and
// returns the underlying fake so tests can drain its Events channel.
func attachFakeRecorder(srv *Server) *record.FakeRecorder {
	fake := record.NewFakeRecorder(20)
	srv.SetEventRecorder(events.NewRecorderForTest(fake, "sharko"))
	return fake
}

// drainOne returns the next event string, or "" if none is buffered.
func drainOne(fake *record.FakeRecorder) string {
	select {
	case e := <-fake.Events:
		return e
	default:
		return ""
	}
}

// assertWarning asserts exactly one buffered event, of type Warning, carrying
// the wanted reason, and carrying no secret-shaped material.
func assertWarning(t *testing.T, fake *record.FakeRecorder, wantReason string) {
	t.Helper()
	got := drainOne(fake)
	if got == "" {
		t.Fatalf("expected a Warning event with reason %q, got none", wantReason)
	}
	if !strings.HasPrefix(got, "Warning ") {
		t.Errorf("expected Warning event, got %q", got)
	}
	if !strings.Contains(got, wantReason) {
		t.Errorf("expected reason %q, got %q", wantReason, got)
	}
	assertNoSecretMaterialAPI(t, got)
	// No second event should be buffered for these single-failure paths.
	if extra := drainOne(fake); extra != "" {
		t.Errorf("expected exactly one event, got a second: %q", extra)
	}
}

func assertNoSecretMaterialAPI(t *testing.T, message string) {
	t.Helper()
	banned := []string{"sharko_", "BEGIN ", "eyJ", "AKIA", "704909879244"}
	for _, b := range banned {
		if strings.Contains(message, b) {
			t.Errorf("event message contains banned secret-shaped token %q: %q", b, message)
		}
	}
}

// ---- surface 1: AWS assume-role failure (doctor check 3) --------------------

func TestEvent_AWSAssumeRoleFailed(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	fake := attachFakeRecorder(srv)
	srv.doctorAssumeRoleFn = func(context.Context, string, string) error {
		return errors.New("AccessDenied: not authorized to perform sts:AssumeRole")
	}

	check := srv.doctorCheckAssumeRole(context.Background(), "cross-account")
	if check.Status != doctorStatusFail {
		t.Fatalf("expected fail, got %q", check.Status)
	}
	assertWarning(t, fake, events.ReasonAWSAssumeRoleFailed)
}

// ---- surface 1b: AWS Secrets Manager read failure (doctor check 2) ----------

func TestEvent_AWSSecretsGetFailed(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	fake := attachFakeRecorder(srv)
	installCredProvider(srv, nil, &providers.AddonSecretProviderConfig{Type: "aws-sm"}, nil)
	srv.doctorAddonSecretProviderFn = func(providers.AddonSecretProviderConfig) (providers.SecretProvider, error) {
		return &fakeAddonSecretProvider{err: errors.New("access denied")}, nil
	}

	check := srv.doctorCheckAddonSecretPaths(context.Background(), "prod-eu")
	if check.Status != doctorStatusFail {
		t.Fatalf("expected fail, got %q (detail=%s)", check.Status, check.Detail)
	}
	assertWarning(t, fake, events.ReasonAWSSecretsGetFailed)
}

// ---- surface 2: host ArgoCD API failure (discover) --------------------------

// discoverFakeArgocd fails ListClusters with a configurable error to drive the
// ArgoCD-unreachable vs auth-failed branches.
type discoverFakeArgocd struct {
	listClustersErr error
}

func (a *discoverFakeArgocd) ListClusters(context.Context) ([]models.ArgocdCluster, error) {
	return nil, a.listClustersErr
}
func (a *discoverFakeArgocd) ListApplications(context.Context) ([]models.ArgocdApplication, error) {
	return nil, nil
}
func (a *discoverFakeArgocd) RegisterCluster(context.Context, string, string, []byte, string, map[string]string) error {
	return nil
}
func (a *discoverFakeArgocd) DeleteCluster(context.Context, string) error { return nil }
func (a *discoverFakeArgocd) UpdateClusterLabels(context.Context, string, map[string]string) error {
	return nil
}
func (a *discoverFakeArgocd) SyncApplication(context.Context, string) error   { return nil }
func (a *discoverFakeArgocd) CreateProject(context.Context, []byte) error     { return nil }
func (a *discoverFakeArgocd) CreateApplication(context.Context, []byte) error { return nil }
func (a *discoverFakeArgocd) AddRepository(context.Context, string, string, string) error {
	return nil
}
func (a *discoverFakeArgocd) GetApplication(context.Context, string) (*models.ArgocdApplication, error) {
	return nil, nil
}

// stubClusterCredProvider is a minimal ClusterCredentialsProvider so the
// discover handler's s.credProvider()==nil early-return does not fire.
type stubClusterCredProvider struct{}

func (stubClusterCredProvider) GetCredentials(string) (*providers.Kubeconfig, error) {
	return &providers.Kubeconfig{}, nil
}
func (stubClusterCredProvider) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (stubClusterCredProvider) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (stubClusterCredProvider) HealthCheck(context.Context) error              { return nil }

func TestEvent_ArgoCDUnreachable(t *testing.T) {
	srv := newDoctorTestServer(t)
	fake := attachFakeRecorder(srv)
	installCredProvider(srv, stubClusterCredProvider{}, nil, nil)
	srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{
		listClustersErr: errors.New("dial tcp: connection refused"),
	})

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator")
	w := httptest.NewRecorder()
	srv.handleDiscoverClusters(w, req)

	assertWarning(t, fake, events.ReasonArgoCDUnreachable)
}

func TestEvent_ArgoCDAuthFailed(t *testing.T) {
	srv := newDoctorTestServer(t)
	fake := attachFakeRecorder(srv)
	installCredProvider(srv, stubClusterCredProvider{}, nil, nil)
	srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{
		listClustersErr: argocd.ErrPermissionDenied,
	})

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator")
	w := httptest.NewRecorder()
	srv.handleDiscoverClusters(w, req)

	assertWarning(t, fake, events.ReasonArgoCDAuthFailed)
}

// ---- nil-recorder safety (out-of-cluster / dev mode) ------------------------

func TestEvent_NilRecorder_NoPanic(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	// No attachFakeRecorder — s.eventRecorder is nil.
	srv.doctorAssumeRoleFn = func(context.Context, string, string) error {
		return errors.New("AccessDenied")
	}
	// Must not panic even though the failure path calls s.emitWarning.
	check := srv.doctorCheckAssumeRole(context.Background(), "cross-account")
	if check.Status != doctorStatusFail {
		t.Fatalf("expected fail, got %q", check.Status)
	}
}
