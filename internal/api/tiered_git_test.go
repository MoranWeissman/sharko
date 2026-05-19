package api

// tiered_git_test.go — covers the per-request provider resolver in
// providerFromConnectionWithToken (V125-1-13.y.2 / BUG-189-real).
//
// Two flavors of test:
//
//   1. Override NOT installed (production-shape path) — confirms the
//      existing happy and fallback branches build the right concrete
//      provider type. These cases must keep working untouched.
//
//   2. Override installed (test/demo path) — confirms the override-first
//      short-circuit returns the injected provider regardless of token /
//      provider configuration. This is the bug-fix gate.
//
// The override is detected by identity (==) against a tiny stub
// gitprovider.GitProvider. We never call into the stub's methods — only
// the returned value is inspected.

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// stubGitProvider implements gitprovider.GitProvider with no-op methods.
// Only its identity matters in these tests — no behavior is exercised. We
// keep it package-local rather than reusing demo.MockGitProvider to avoid
// dragging the entire demo package into the api test compile set.
type stubGitProvider struct{}

func (s *stubGitProvider) GetFileContent(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (s *stubGitProvider) ListDirectory(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (s *stubGitProvider) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (s *stubGitProvider) TestConnection(context.Context) error                       { return nil }
func (s *stubGitProvider) CreateBranch(context.Context, string, string) error         { return nil }
func (s *stubGitProvider) CreateOrUpdateFile(context.Context, string, []byte, string, string) error {
	return nil
}
func (s *stubGitProvider) BatchCreateFiles(context.Context, map[string][]byte, string, string) error {
	return nil
}
func (s *stubGitProvider) DeleteFile(context.Context, string, string, string) error { return nil }
func (s *stubGitProvider) CreatePullRequest(context.Context, string, string, string, string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (s *stubGitProvider) MergePullRequest(context.Context, int) error          { return nil }
func (s *stubGitProvider) GetPullRequestStatus(context.Context, int) (string, error) {
	return "", nil
}
func (s *stubGitProvider) DeleteBranch(context.Context, string) error { return nil }

// newConnectionServiceForTest returns a *service.ConnectionService backed by
// a fresh in-memory FileStore. We only use it as a wiring target for
// SetGitProviderOverride; no connection is persisted via this service
// (providerFromConnectionWithToken takes the connection as a parameter).
func newConnectionServiceForTest(t *testing.T) *service.ConnectionService {
	t.Helper()
	// FileStore on a non-existent path is fine — the tests never call any
	// store-touching method. providerFromConnectionWithToken only calls
	// s.connSvc.GitProviderOverride() and s.connSvc.GetActiveGitProvider()
	// (the latter not on override-installed paths).
	store := config.NewFileStore("")
	return service.NewConnectionService(store)
}

// newServerWithConnSvc builds a minimal *Server whose connSvc is the supplied
// ConnectionService. Other fields stay zero — providerFromConnectionWithToken
// touches only connSvc.
func newServerWithConnSvc(connSvc *service.ConnectionService) *Server {
	return &Server{connSvc: connSvc}
}

func TestProviderFromConnectionWithToken(t *testing.T) {
	type wantKind int
	const (
		wantGitHub wantKind = iota
		wantAzureDevOps
		wantOverride
		wantError // path delegated to GetActiveGitProvider with no override, no active conn — expected to error
	)

	overrideStub := &stubGitProvider{}

	cases := []struct {
		name           string
		installOverride bool
		conn           *models.Connection
		token          string
		want           wantKind
	}{
		// ----- Override NOT installed (production-shape) ------------------

		{
			name: "no override, github + token -> NewGitHubProvider",
			conn: &models.Connection{
				Name: "test-gh",
				Git: models.GitRepoConfig{
					Provider: models.GitProviderGitHub,
					Owner:    "acme",
					Repo:     "infra",
				},
			},
			token: "ghp_real_token",
			want:  wantGitHub,
		},
		{
			name: "no override, azuredevops + token -> NewAzureDevOpsProvider",
			conn: &models.Connection{
				Name: "test-ado",
				Git: models.GitRepoConfig{
					Provider:     models.GitProviderAzureDevOps,
					Organization: "acme",
					Project:      "platform",
					Repository:   "infra",
				},
			},
			token: "ado_real_pat",
			want:  wantAzureDevOps,
		},
		{
			name: "no override, github + empty token -> GetActiveGitProvider (errors, no active conn)",
			conn: &models.Connection{
				Name: "test-gh-fallback",
				Git: models.GitRepoConfig{
					Provider: models.GitProviderGitHub,
					Owner:    "acme",
					Repo:     "infra",
				},
			},
			token: "",
			want:  wantError,
		},
		{
			name: "no override, azuredevops + empty token -> GetActiveGitProvider (errors, no active conn)",
			conn: &models.Connection{
				Name: "test-ado-fallback",
				Git: models.GitRepoConfig{
					Provider:     models.GitProviderAzureDevOps,
					Organization: "acme",
					Project:      "platform",
					Repository:   "infra",
				},
			},
			token: "",
			want:  wantError,
		},
		{
			name: "no override, unknown provider -> GetActiveGitProvider (errors, no active conn)",
			conn: &models.Connection{
				Name: "test-unknown",
				Git: models.GitRepoConfig{
					Provider: "gitlab", // unsupported
				},
			},
			token: "any",
			want:  wantError,
		},

		// ----- Override INSTALLED (test/demo path — BUG-189-real fix) -----

		{
			name:            "override installed, github + token -> override (bug fix)",
			installOverride: true,
			conn: &models.Connection{
				Name: "test-gh-with-override",
				Git: models.GitRepoConfig{
					Provider: models.GitProviderGitHub,
					Owner:    "acme",
					Repo:     "infra",
				},
			},
			token: "ghmock-test-token", // non-empty mimics seedActiveConnection
			want:  wantOverride,
		},
		{
			name:            "override installed, azuredevops + token -> override (bug fix)",
			installOverride: true,
			conn: &models.Connection{
				Name: "test-ado-with-override",
				Git: models.GitRepoConfig{
					Provider:     models.GitProviderAzureDevOps,
					Organization: "acme",
					Project:      "platform",
					Repository:   "infra",
				},
			},
			token: "ado-mock-token",
			want:  wantOverride,
		},
		{
			name:            "override installed, unknown provider + token -> override",
			installOverride: true,
			conn: &models.Connection{
				Name: "test-unknown-with-override",
				Git: models.GitRepoConfig{
					Provider: "gitlab",
				},
			},
			token: "irrelevant",
			want:  wantOverride,
		},
		{
			name:            "override installed, github + empty token -> override",
			installOverride: true,
			conn: &models.Connection{
				Name: "test-gh-empty-token-with-override",
				Git: models.GitRepoConfig{
					Provider: models.GitProviderGitHub,
					Owner:    "acme",
					Repo:     "infra",
				},
			},
			token: "",
			want:  wantOverride,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connSvc := newConnectionServiceForTest(t)
			if tc.installOverride {
				connSvc.SetGitProviderOverride(overrideStub)
			}
			srv := newServerWithConnSvc(connSvc)

			got, err := srv.providerFromConnectionWithToken(tc.conn, tc.token)

			switch tc.want {
			case wantOverride:
				if err != nil {
					t.Fatalf("want override returned, got error: %v", err)
				}
				if got != overrideStub {
					t.Fatalf("want override stub (%p), got %T (%p)", overrideStub, got, got)
				}

			case wantGitHub:
				if err != nil {
					t.Fatalf("want *GitHubProvider, got error: %v", err)
				}
				if got == overrideStub {
					t.Fatalf("returned override stub on a no-override case — short-circuit fired incorrectly")
				}
				if _, ok := got.(*gitprovider.GitHubProvider); !ok {
					t.Fatalf("want *GitHubProvider, got %T", got)
				}

			case wantAzureDevOps:
				if err != nil {
					t.Fatalf("want *AzureDevOpsProvider, got error: %v", err)
				}
				if got == overrideStub {
					t.Fatalf("returned override stub on a no-override case — short-circuit fired incorrectly")
				}
				if _, ok := got.(*gitprovider.AzureDevOpsProvider); !ok {
					t.Fatalf("want *AzureDevOpsProvider, got %T", got)
				}

			case wantError:
				// no-override + token-empty (or unknown provider) delegates to
				// GetActiveGitProvider, which errors because the test store
				// has no active connection. The exact message comes from
				// service.ConnectionService.getActiveConn and is therefore
				// stable enough to assert on existence rather than text.
				if err == nil {
					t.Fatalf("want error from GetActiveGitProvider delegation, got provider %T", got)
				}
				if got == overrideStub {
					t.Fatalf("returned override stub on a no-override case — short-circuit fired incorrectly")
				}
			}
		})
	}
}

// TestProviderFromConnectionWithToken_ProductionSafety pins the no-override
// behavior to today's contract: with SetGitProviderOverride never called, the
// resolver MUST construct a fresh provider directly — proving the new
// short-circuit is fully gated by GitProviderOverride() == nil.
//
// Without this test, a future refactor of GitProviderOverride() that returned
// a non-nil sentinel could silently break every production deployment.
func TestProviderFromConnectionWithToken_ProductionSafety(t *testing.T) {
	connSvc := newConnectionServiceForTest(t)
	// Critically: do NOT call SetGitProviderOverride.
	if got := connSvc.GitProviderOverride(); got != nil {
		t.Fatalf("production-shape ConnectionService must return nil from GitProviderOverride(), got %T", got)
	}
	srv := newServerWithConnSvc(connSvc)

	got, err := srv.providerFromConnectionWithToken(
		&models.Connection{
			Name: "prod-shape",
			Git: models.GitRepoConfig{
				Provider: models.GitProviderGitHub,
				Owner:    "acme",
				Repo:     "infra",
			},
		},
		"ghp_real_production_token",
	)
	if err != nil {
		t.Fatalf("production-shape resolve: unexpected error: %v", err)
	}
	if _, ok := got.(*gitprovider.GitHubProvider); !ok {
		t.Fatalf("production-shape resolve: want *GitHubProvider, got %T", got)
	}
}
