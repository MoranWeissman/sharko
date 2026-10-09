package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// creds-reframe-1 — explicit creds_source on RegisterCluster.
//
// These tests pin the keystone contract added in creds-reframe-1:
//
//   - creds_source is an optional, additive request field with three legal
//     values: inline-kubeconfig, secret-kubeconfig, eks-token.
//   - When ABSENT it is derived from Provider so every request that works
//     today keeps working byte-for-byte (kubeconfig→inline, else→backend).
//   - When SET it is authoritative and wins over Provider.
//   - Per-source validation returns a 400-mappable *InvalidCredsSourceError.
//
// The inline path is observable via the "parse_kubeconfig" completed step;
// the backend path is observable via the "fetch_credentials" step. We assert
// on those step markers to prove which route a request took.

func hasStep(steps []string, want string) bool {
	for _, s := range steps {
		if s == want {
			return true
		}
	}
	return false
}

// ---------- pure resolver / derivation table ----------

func TestResolveCredsSource_Derivation(t *testing.T) {
	cases := []struct {
		name     string
		req      RegisterClusterRequest
		want     CredsSource
		wantErr  bool
		errMatch string
	}{
		{
			name: "empty creds_source + kubeconfig provider derives inline",
			req:  RegisterClusterRequest{Name: "c", Provider: "kubeconfig"},
			want: CredsSourceInlineKubeconfig,
		},
		{
			name: "empty creds_source + eks provider derives backend",
			req:  RegisterClusterRequest{Name: "c", Provider: "eks"},
			want: CredsSourceSecretKubeconfig,
		},
		{
			name: "empty creds_source + empty provider derives backend",
			req:  RegisterClusterRequest{Name: "c"},
			want: CredsSourceSecretKubeconfig,
		},
		{
			// V2-cleanup-60.4 un-trap: a pasted kubeconfig with nothing else
			// said is authoritative — never silently ignored in favour of a
			// backend lookup.
			name: "empty creds_source + empty provider + pasted kubeconfig derives inline",
			req:  RegisterClusterRequest{Name: "c", Kubeconfig: "apiVersion: v1\nkind: Config\n"},
			want: CredsSourceInlineKubeconfig,
		},
		{
			// Whitespace-only paste is NOT a paste.
			name: "empty creds_source + empty provider + whitespace kubeconfig derives backend",
			req:  RegisterClusterRequest{Name: "c", Kubeconfig: "  \n\t"},
			want: CredsSourceSecretKubeconfig,
		},
		{
			// An explicit non-kubeconfig provider still wins over the paste
			// (the caller said "backend"; the handler edge-rejects the bleed).
			name: "empty creds_source + eks provider + pasted kubeconfig derives backend",
			req:  RegisterClusterRequest{Name: "c", Provider: "eks", Kubeconfig: "apiVersion: v1\nkind: Config\n"},
			want: CredsSourceSecretKubeconfig,
		},
		{
			name: "explicit inline-kubeconfig is honored",
			req:  RegisterClusterRequest{Name: "c", CredsSource: CredsSourceInlineKubeconfig},
			want: CredsSourceInlineKubeconfig,
		},
		{
			name: "explicit secret-kubeconfig is honored",
			req:  RegisterClusterRequest{Name: "c", CredsSource: CredsSourceSecretKubeconfig},
			want: CredsSourceSecretKubeconfig,
		},
		{
			name: "explicit eks-token is honored",
			req:  RegisterClusterRequest{Name: "c", CredsSource: CredsSourceEKSToken},
			want: CredsSourceEKSToken,
		},
		{
			name: "creds_source wins over a disagreeing provider",
			req:  RegisterClusterRequest{Name: "c", Provider: "eks", CredsSource: CredsSourceInlineKubeconfig},
			want: CredsSourceInlineKubeconfig,
		},
		{
			name:     "unknown creds_source is rejected",
			req:      RegisterClusterRequest{Name: "c", CredsSource: "vault"},
			wantErr:  true,
			errMatch: "unknown creds_source",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveCredsSource(tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (source=%q)", got)
				}
				if !IsInvalidCredsSource(err) {
					t.Errorf("expected *InvalidCredsSourceError, got %T: %v", err, err)
				}
				if tc.errMatch != "" && !strings.Contains(err.Error(), tc.errMatch) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errMatch)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveCredsSource = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------- explicit routing through RegisterCluster ----------

// Explicit inline-kubeconfig routes to the inline (parse) path and does NOT
// require a credProvider backend.
func TestRegisterCluster_ExplicitInlineKubeconfig_RoutesInline(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "kind-sharko",
		CredsSource: CredsSourceInlineKubeconfig, // no Provider set on purpose
		Kubeconfig:  v125TestBearerKubeconfig,
		Addons:      map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	if !hasStep(result.CompletedSteps, "parse_kubeconfig") {
		t.Errorf("expected parse_kubeconfig step (inline path); got %v", result.CompletedSteps)
	}
	if hasStep(result.CompletedSteps, "fetch_credentials") {
		t.Errorf("inline path must NOT call credProvider; got %v", result.CompletedSteps)
	}
	if result.Cluster.Server != "https://127.0.0.1:60123" {
		t.Errorf("expected server from inline kubeconfig, got %q", result.Cluster.Server)
	}
}

// Explicit secret-kubeconfig routes to the backend (credProvider) path.
func TestRegisterCluster_ExplicitSecretKubeconfig_RoutesBackend(t *testing.T) {
	creds := defaultCreds() // seeded with "prod-eu"
	orch := New(nil, creds, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "prod-eu",
		CredsSource: CredsSourceSecretKubeconfig, // no Provider set on purpose
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	if !hasStep(result.CompletedSteps, "fetch_credentials") {
		t.Errorf("expected fetch_credentials step (backend path); got %v", result.CompletedSteps)
	}
	if hasStep(result.CompletedSteps, "parse_kubeconfig") {
		t.Errorf("backend path must NOT parse an inline kubeconfig; got %v", result.CompletedSteps)
	}
}

// Explicit eks-token routes to the SAME backend (credProvider) path — v1 does
// not split the backend sniff, so eks-token and secret-kubeconfig share it.
func TestRegisterCluster_ExplicitEKSToken_RoutesBackend(t *testing.T) {
	creds := defaultCreds() // seeded with "prod-eu"
	orch := New(nil, creds, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "prod-eu",
		CredsSource: CredsSourceEKSToken,
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !hasStep(result.CompletedSteps, "fetch_credentials") {
		t.Errorf("expected fetch_credentials step (backend path); got %v", result.CompletedSteps)
	}
	if hasStep(result.CompletedSteps, "parse_kubeconfig") {
		t.Errorf("eks-token path must NOT parse an inline kubeconfig; got %v", result.CompletedSteps)
	}
}

// ---------- backward-compat: empty creds_source behaves exactly as today ----------

// Empty creds_source + Provider:"kubeconfig" must behave identically to the
// pre-creds-reframe inline path (the same contract the existing
// TestRegisterCluster_Kubeconfig_HappyPath_NoCredProvider pins).
func TestRegisterCluster_BackwardCompat_EmptySource_KubeconfigProvider(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig", // creds_source intentionally empty
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	if !hasStep(result.CompletedSteps, "parse_kubeconfig") {
		t.Errorf("empty creds_source + kubeconfig provider must still take the inline path; got %v", result.CompletedSteps)
	}
}

// Empty creds_source + non-kubeconfig provider + SecretPath must behave
// identically to today: the backend lookup keys off SecretPath.
func TestRegisterCluster_BackwardCompat_EmptySource_BackendWithSecretPath(t *testing.T) {
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"clusters/prod/my-cluster": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
			},
		},
	}
	orch := New(nil, creds, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "my-cluster",
		Provider:   "eks", // creds_source intentionally empty
		SecretPath: "clusters/prod/my-cluster",
	})
	if err != nil {
		t.Fatalf("expected success via secret_path lookup, got error: %v", err)
	}
	if !hasStep(result.CompletedSteps, "fetch_credentials") {
		t.Errorf("empty creds_source + eks provider must still take the backend path; got %v", result.CompletedSteps)
	}
	if result.Cluster.Server != "https://k8s.example.com:6443" {
		t.Errorf("backend lookup must resolve via secret_path; got server %q", result.Cluster.Server)
	}
}

// ---------- validation 400s ----------

// inline-kubeconfig with no kubeconfig is OPTIONAL credentials, not a
// rejection (V2-cleanup-88.3 — lazy credentials generalized the
// self-managed-only relaxation this test used to pin). The registration
// succeeds as a connection-only registration and records the skip.
func TestRegisterCluster_InlineKubeconfig_NoKubeconfig_Succeeds(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "kind-sharko",
		CredsSource: CredsSourceInlineKubeconfig,
		Addons:      map[string]bool{"monitoring": true},
		// Kubeconfig omitted on purpose.
	})
	if err != nil {
		t.Fatalf("expected success without a kubeconfig, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q (%s)", result.Status, result.Error)
	}
	if !hasStep(result.CompletedSteps, "skip_credentials") {
		t.Errorf("expected skip_credentials step, got %v", result.CompletedSteps)
	}
	if hasStep(result.CompletedSteps, "parse_kubeconfig") {
		t.Errorf("must not attempt to parse an absent kubeconfig, got %v", result.CompletedSteps)
	}
}

// An unknown creds_source value is a caller error.
func TestRegisterCluster_UnknownCredsSource_Rejected(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "prod-eu",
		CredsSource: "vault",
	})
	if err == nil {
		t.Fatal("expected unknown creds_source to be rejected")
	}
	if !IsInvalidCredsSource(err) {
		t.Errorf("expected *InvalidCredsSourceError (→ 400), got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "unknown creds_source") {
		t.Errorf("error should name the unknown value, got: %v", err)
	}
}
