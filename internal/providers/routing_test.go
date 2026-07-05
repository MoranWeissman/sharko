package providers

import (
	"context"
	"errors"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

// routingFakeProvider is a minimal recording ClusterCredentialsProvider.
type routingFakeProvider struct {
	calls []string
	kc    *Kubeconfig
	err   error
}

func (p *routingFakeProvider) GetCredentials(name string) (*Kubeconfig, error) {
	p.calls = append(p.calls, name)
	if p.err != nil {
		return nil, p.err
	}
	return p.kc, nil
}
func (p *routingFakeProvider) ListClusters() ([]ClusterInfo, error)   { return nil, nil }
func (p *routingFakeProvider) SearchSecrets(string) ([]string, error) { return nil, nil }
func (p *routingFakeProvider) HealthCheck(context.Context) error      { return nil }

var _ ClusterCredentialsProvider = (*routingFakeProvider)(nil)

func readerFn(p ClusterCredentialsProvider, err error) func() (ClusterCredentialsProvider, error) {
	return func() (ClusterCredentialsProvider, error) { return p, err }
}

// Inline-registered cluster: the ArgoCD reader is consulted with the CLUSTER
// NAME and the backend is NEVER touched — regardless of what the backend is.
func TestRouter_InlineSource_RoutesToArgoCDReader_BackendUntouched(t *testing.T) {
	backend := &routingFakeProvider{err: errors.New("backend must not be consulted")}
	reader := &routingFakeProvider{kc: &Kubeconfig{Server: "https://from-argocd"}}
	r := &ClusterCredsRouter{Backend: backend, ArgoCDReaderFn: readerFn(reader, nil)}

	kc, err := r.Fetch("kind-local", "some-secret-path-override", models.CredsSourceInlineKubeconfig)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if kc.Server != "https://from-argocd" {
		t.Errorf("Server = %q, want the ArgoCD-read credentials", kc.Server)
	}
	if len(backend.calls) != 0 {
		t.Errorf("backend consulted for an inline cluster: %v", backend.calls)
	}
	// The ArgoCD Secret is keyed by cluster NAME, never by a secretPath
	// override (a backend concept).
	if len(reader.calls) != 1 || reader.calls[0] != "kind-local" {
		t.Errorf("reader calls = %v, want [kind-local]", reader.calls)
	}
}

// Inline route with an unavailable reader surfaces an actionable error.
func TestRouter_InlineSource_ReaderUnavailable_ActionableError(t *testing.T) {
	backend := &routingFakeProvider{kc: &Kubeconfig{}}
	r := &ClusterCredsRouter{Backend: backend, ArgoCDReaderFn: readerFn(nil, errors.New("no in-cluster config"))}

	_, err := r.Fetch("kind-local", "kind-local", models.CredsSourceInlineKubeconfig)
	if err == nil {
		t.Fatal("want error when the ArgoCD reader is unavailable for an inline cluster")
	}
	if len(backend.calls) != 0 {
		t.Errorf("backend consulted for an inline cluster: %v", backend.calls)
	}
}

// Backend sources keep the exact pre-existing route: backend +lookup key,
// reader untouched — even when the backend errors (no masking fallback).
func TestRouter_BackendSources_RouteToBackend_NoFallback(t *testing.T) {
	for _, src := range []string{models.CredsSourceSecretKubeconfig, models.CredsSourceEKSToken} {
		backendErr := errors.New("secret not found")
		backend := &routingFakeProvider{err: backendErr}
		reader := &routingFakeProvider{kc: &Kubeconfig{Server: "https://from-argocd"}}
		r := &ClusterCredsRouter{Backend: backend, ArgoCDReaderFn: readerFn(reader, nil)}

		_, err := r.Fetch("prod-eu", "clusters/prod-eu", src)
		if !errors.Is(err, backendErr) {
			t.Errorf("src %s: err = %v, want the backend error verbatim", src, err)
		}
		if len(backend.calls) != 1 || backend.calls[0] != "clusters/prod-eu" {
			t.Errorf("src %s: backend calls = %v, want [clusters/prod-eu]", src, backend.calls)
		}
		if len(reader.calls) != 0 {
			t.Errorf("src %s: ArgoCD reader consulted for a backend cluster: %v", src, reader.calls)
		}
	}
}

// Unknown/legacy record: backend first; backend success never touches the reader.
func TestRouter_UnknownSource_BackendSuccess_NoReaderCall(t *testing.T) {
	backend := &routingFakeProvider{kc: &Kubeconfig{Server: "https://from-backend"}}
	reader := &routingFakeProvider{kc: &Kubeconfig{Server: "https://from-argocd"}}
	r := &ClusterCredsRouter{Backend: backend, ArgoCDReaderFn: readerFn(reader, nil)}

	kc, err := r.Fetch("prod-eu", "prod-eu", "")
	if err != nil || kc.Server != "https://from-backend" {
		t.Fatalf("kc=%+v err=%v, want backend credentials", kc, err)
	}
	if len(reader.calls) != 0 {
		t.Errorf("reader consulted despite backend success: %v", reader.calls)
	}
}

// Unknown/legacy record + backend miss: the ArgoCD reader heals the fetch
// (a pre-60.4 inline-registered cluster has nothing in the backend).
func TestRouter_UnknownSource_BackendMiss_HealsViaArgoCDReader(t *testing.T) {
	backend := &routingFakeProvider{err: errors.New("secret not found")}
	reader := &routingFakeProvider{kc: &Kubeconfig{Server: "https://from-argocd"}}
	r := &ClusterCredsRouter{Backend: backend, ArgoCDReaderFn: readerFn(reader, nil)}

	kc, err := r.Fetch("kind-local", "kind-local", "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if kc.Server != "https://from-argocd" {
		t.Errorf("Server = %q, want the ArgoCD-read credentials", kc.Server)
	}
}

// Unknown/legacy record + BOTH routes failing: the ORIGINAL backend error is
// returned (backend-registered clusters keep their failure surface — typed
// errors + the "not found" substring the suggestion search keys on).
func TestRouter_UnknownSource_BothFail_OriginalBackendError(t *testing.T) {
	backendErr := errors.New("secret not found in AWS SM")
	backend := &routingFakeProvider{err: backendErr}
	reader := &routingFakeProvider{err: errors.New("argocd secret missing too")}
	r := &ClusterCredsRouter{Backend: backend, ArgoCDReaderFn: readerFn(reader, nil)}

	_, err := r.Fetch("prod-eu", "prod-eu", "")
	if !errors.Is(err, backendErr) {
		t.Errorf("err = %v, want the original backend error", err)
	}
}

// When the backend IS the ArgoCD provider (type argocd / in-cluster
// auto-default) the router short-circuits to a single backend call for
// every source — routing cannot change anything.
func TestRouter_ArgoCDBackend_SingledPath(t *testing.T) {
	backend := newArgoCDProviderWithClient(fake.NewSimpleClientset(), "argocd")
	reader := &routingFakeProvider{kc: &Kubeconfig{Server: "https://from-reader"}}
	r := &ClusterCredsRouter{Backend: backend, ArgoCDReaderFn: readerFn(reader, nil)}

	// The empty fake clientset makes the ArgoCD backend fail with its own
	// "not found" shape — the point is the ROUTE: an inline source must go
	// straight to Backend (single path), never through the separate reader.
	_, err := r.Fetch("kind-local", "kind-local", models.CredsSourceInlineKubeconfig)
	if err == nil {
		t.Fatal("empty ArgoCD backend should error — routing must have gone to the backend")
	}
	if len(reader.calls) != 0 {
		t.Errorf("separate reader consulted despite the backend already being the ArgoCD provider: %v", reader.calls)
	}
}

// Nil-router / nil-backend defensive shapes.
func TestRouter_NilShapes(t *testing.T) {
	var nilRouter *ClusterCredsRouter
	if _, err := nilRouter.Fetch("a", "a", ""); err == nil {
		t.Error("nil router must error, not panic")
	}

	r := &ClusterCredsRouter{Backend: nil, ArgoCDReaderFn: nil}
	if _, err := r.Fetch("a", "a", ""); err == nil {
		t.Error("nil backend + nil reader must error, not panic")
	}

	// Nil backend + working reader: inline still routes.
	reader := &routingFakeProvider{kc: &Kubeconfig{Server: "https://from-argocd"}}
	r2 := &ClusterCredsRouter{Backend: nil, ArgoCDReaderFn: readerFn(reader, nil)}
	kc, err := r2.Fetch("kind-local", "kind-local", models.CredsSourceInlineKubeconfig)
	if err != nil || kc.Server != "https://from-argocd" {
		t.Errorf("inline with nil backend: kc=%+v err=%v, want ArgoCD-read credentials", kc, err)
	}
}
