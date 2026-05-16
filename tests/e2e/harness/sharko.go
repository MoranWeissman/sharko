//go:build e2e

package harness

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/api"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/service"
)

// SharkoMode selects the boot strategy. The default (zero value) is the fast
// in-process path; helm mode is opt-in via E2E_SHARKO_MODE=helm.
type SharkoMode int

const (
	// SharkoModeInProcess instantiates sharko's HTTP router in the test
	// process via httptest.NewServer. Fast (~50ms boot) but does not
	// exercise containerised paths (Helm chart, image, K8s wiring).
	SharkoModeInProcess SharkoMode = iota
	// SharkoModeHelm helm-installs sharko into a kind mgmt cluster via
	// installSharkoHelm + bootstrapHelmSharkoAuth (V125-1-13.1/13.2/13.3).
	// Slow (~30-90s boot depending on image build cache) but full-stack
	// fidelity. Requires SharkoConfig.MgmtCluster to point at a provisioned
	// kind cluster.
	SharkoModeHelm
)

// SharkoConfig declares boot parameters for a single Sharko instance.
//
// Zero-value defaults are sane for the in-process hello-world path:
//   - Mode resolves from E2E_SHARKO_MODE (else InProcess)
//   - AdminUser defaults to "admin"
//   - AdminPass defaults to a freshly generated 32-hex-char value
//   - AIDisabled defaults true (in-process tests almost never want AI)
type SharkoConfig struct {
	// Mode picks the boot path. Leave zero to honour E2E_SHARKO_MODE.
	Mode SharkoMode
	// MgmtCluster is the kind cluster to helm-install into. Required
	// for SharkoModeHelm; ignored for SharkoModeInProcess.
	MgmtCluster *KindCluster
	// GitFake is the fake git backend for sharko's GitOps flow.
	// Required — the in-process boot path stores the fake's RepoURL on
	// the active connection so any future git operation is reproducible.
	GitFake *GitFake
	// GitProvider, when non-nil, is installed as the active
	// gitprovider.GitProvider via *Server.SetDemoGitProvider, bypassing
	// the real GitHub API. Story 7-1.3 wires this up: pass a
	// *MockGitProvider (from ghmock.go) and every sharko write hits the
	// in-memory fake. Optional — when nil, sharko falls back to whatever
	// the active connection resolves to (which is no provider at all in
	// the in-process boot path; reads will fail until a connection is
	// configured via the API).
	GitProvider gitprovider.GitProvider
	// AdminUser is the bootstrap admin account name. Defaults to "admin".
	AdminUser string
	// AdminPass is the bootstrap admin password. Defaults to a
	// cryptographically-random 32-hex-char string when empty.
	AdminPass string
	// AIDisabled, when true, skips AI client initialisation. Defaults
	// to true in StartSharko regardless of zero-value semantics — there
	// is no scenario in 7-1.2 that needs AI on.
	AIDisabled bool

	// HelmOverrides are extra `--set key=value` overrides forwarded to
	// installSharkoHelm (V125-1-13.1's HelmInstallConfig.SetValues).
	// Optional; ignored for SharkoModeInProcess.
	HelmOverrides map[string]string

	// HelmTimeout caps the rollout wait for the Helm install (V125-1-13.1).
	// Defaults to 180s when zero. Ignored for SharkoModeInProcess.
	HelmTimeout time.Duration
}

// Sharko is a running sharko instance reachable over HTTP at URL.
//
// All teardown is wired via t.Cleanup at construction; callers do not need
// to call Stop() explicitly.
type Sharko struct {
	// URL is the base URL of the sharko HTTP server (e.g.
	// "http://127.0.0.1:42345"). Append "/api/v1/..." for endpoints.
	URL string
	// AdminUser is the bootstrap admin account name (defaults to "admin").
	AdminUser string
	// AdminPass is the bootstrap admin password — useful when a test
	// needs to authenticate via HTTP Basic / login flow.
	AdminPass string
	// Mode is the boot mode actually selected (after env-var resolution).
	Mode SharkoMode
	// Token is a pre-minted JWT for SharkoModeHelm callers who need to
	// skip the login flow (apiclient.Client.SetToken accepts it directly).
	// Empty for SharkoModeInProcess — that path uses HTTP Basic via
	// AdminUser/AdminPass.
	Token string

	server *httptest.Server // populated for in-process mode
	apiSrv *api.Server      // populated for in-process mode; used by SeedUsers to bypass the login limiter
	closed bool
}

// APIServer returns the underlying *api.Server for in-process boots. nil
// for helm mode. Used by SeedUsers (7-1.3) to call AddDemoUser directly
// without exhausting the login rate limiter (5/minute/IP) — a real test
// suite easily seeds more users than that.
//
// External callers should prefer the typed API client + SeedUsers
// helpers; this accessor is reserved for harness-internal use.
func (s *Sharko) APIServer() *api.Server { return s.apiSrv }

// StartSharko boots sharko per cfg and registers t.Cleanup teardown.
//
// Mode resolution order:
//  1. cfg.Mode if non-zero (i.e. caller explicitly chose Helm)
//  2. E2E_SHARKO_MODE=helm  → SharkoModeHelm
//  3. otherwise              → SharkoModeInProcess
//
// In-process mode constructs the api.Server + api.NewRouter pair the same
// way cmd/sharko/serve.go does in --demo mode (no provider, no reconcilers,
// no K8s client) and wraps the resulting http.Handler in httptest.NewServer.
//
// Helm mode installs Sharko via Helm into cfg.MgmtCluster (V125-1-13.3) and
// returns a *Sharko whose URL points at the port-forwarded Helm-installed
// instance. cfg.MgmtCluster is required for SharkoModeHelm.
//
// Calls t.Fatalf on setup failure — does not return on error.
func StartSharko(t *testing.T, cfg SharkoConfig) *Sharko {
	t.Helper()
	if cfg.GitFake == nil {
		t.Fatalf("StartSharko: cfg.GitFake is required (call StartGitFake first)")
	}

	mode := cfg.Mode
	if mode == SharkoModeInProcess {
		// Honour env override only when caller did not explicitly pick.
		if v := os.Getenv("E2E_SHARKO_MODE"); strings.EqualFold(v, "helm") {
			mode = SharkoModeHelm
		}
	}

	user := cfg.AdminUser
	if user == "" {
		user = "admin"
	}
	pass := cfg.AdminPass
	if pass == "" {
		pass = randHex(t, 16) // 32 hex chars
	}

	switch mode {
	case SharkoModeInProcess:
		return startSharkoInProcess(t, cfg, user, pass)
	case SharkoModeHelm:
		return startSharkoHelm(t, cfg)
	default:
		t.Fatalf("StartSharko: unknown mode %d", mode)
		return nil
	}
}

// startSharkoHelm bridges StartSharko's mode selector to V125-1-13.1's
// installSharkoHelm + V125-1-13.2's bootstrapHelmSharkoAuth, returning a
// *Sharko whose URL is host-reachable via a port-forward and whose
// AdminUser/AdminPass/Token come from the in-cluster bootstrap admin secret.
//
// cfg.MgmtCluster must be non-nil — every Helm-mode test owns at least one
// kind cluster (the management cluster), provisioned via ProvisionTopology
// before StartSharko is called.
//
// The returned *Sharko's APIServer() returns nil for Helm mode (the in-
// process api.Server is not constructed); callers that need to seed users
// or bypass rate limiters must drive Sharko via the typed API client.
func startSharkoHelm(t *testing.T, cfg SharkoConfig) *Sharko {
	t.Helper()
	if cfg.MgmtCluster == nil {
		t.Fatalf("StartSharko[helm]: cfg.MgmtCluster is required for SharkoModeHelm")
	}

	helmCfg := HelmInstallConfig{
		KindClusterName: cfg.MgmtCluster.Name,
		Namespace:       defaultHelmNamespace,
		Timeout:         cfg.HelmTimeout,
		SetValues:       cfg.HelmOverrides,
	}

	helmHandle, err := installSharkoHelm(t, cfg.MgmtCluster, helmCfg)
	if err != nil {
		t.Fatalf("StartSharko[helm]: installSharkoHelm: %v", err)
	}

	authBundle, err := bootstrapHelmSharkoAuth(t, helmHandle)
	if err != nil {
		t.Fatalf("StartSharko[helm]: bootstrapHelmSharkoAuth: %v", err)
	}

	s := &Sharko{
		URL:       authBundle.BaseURL,
		AdminUser: authBundle.AdminUser,
		AdminPass: authBundle.AdminPass,
		Mode:      SharkoModeHelm,
		Token:     authBundle.Token,
		// server, apiSrv left nil — APIServer() returns nil for helm
		// mode per the doc comment on (*Sharko).APIServer.
	}
	t.Cleanup(func() { s.Stop() })
	t.Logf("harness: sharko (helm) ready at %s [user=%s, ns=%s, cluster=%s, token-len=%d]",
		s.URL, s.AdminUser, helmHandle.Namespace, helmHandle.KindClusterName, len(s.Token))
	return s
}

// startSharkoInProcess wires up sharko the same way cmd/sharko/serve.go does
// in --demo mode and returns a running httptest.Server. The wiring deliberately
// avoids every optional subsystem (catalog signing, secrets reconciler, AI,
// argo reconciler, PR tracker) so the boot is deterministic and fast.
func startSharkoInProcess(t *testing.T, cfg SharkoConfig, user, pass string) *Sharko {
	t.Helper()

	// Per-test on-disk file store under t.TempDir so tests are isolated.
	connStorePath := tempFile(t, "sharko-conn-*.yaml")
	store := config.NewFileStore(connStorePath)

	// Construct the same service deps that handlers_test.go does.
	connSvc := service.NewConnectionService(store)
	clusterSvc := service.NewClusterService("")
	addonSvc := service.NewAddonService("")
	dashboardSvc := service.NewDashboardService(connSvc, "")
	observabilitySvc := service.NewObservabilityService()

	// AI off — pass an empty Config; ai.Client returns IsEnabled()=false.
	aiClient := ai.NewClient(ai.Config{})
	upgradeSvc := service.NewUpgradeService(aiClient, nil, "")

	srv := api.NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient)
	srv.SetVersion("e2e")

	// Optional GitProvider injection (V2 Epic 7-1.3). When the caller
	// supplies a fake (typically *MockGitProvider from ghmock.go), wire
	// it onto the connection service so every sharko handler that calls
	// connSvc.GetActiveGitProvider() receives the fake. Without this
	// reads against the real GitHub API would fail with "no active
	// connection" — the in-process boot path doesn't seed a connection.
	if cfg.GitProvider != nil {
		srv.SetDemoGitProvider(cfg.GitProvider)
	}

	// Seed a bootstrap admin so login flows in 7-1.3 have a known credential.
	// In local mode auth.Store accepts plaintext passwords, so this is enough
	// to satisfy ValidateCredentials with no extra hashing dance.
	if err := srv.AddDemoUser(user, pass, "admin"); err != nil {
		t.Fatalf("StartSharko: seed admin user: %v", err)
	}

	router := api.NewRouter(srv, nil /* no static UI */)
	httpSrv := httptest.NewServer(router)

	s := &Sharko{
		URL:       httpSrv.URL,
		AdminUser: user,
		AdminPass: pass,
		Mode:      SharkoModeInProcess,
		server:    httpSrv,
		apiSrv:    srv,
	}
	t.Cleanup(func() { s.Stop() })
	t.Logf("harness: sharko (in-process) ready at %s [user=%s, version=e2e]", s.URL, user)
	return s
}

// Stop tears down the sharko server. Idempotent. t.Cleanup invokes this
// automatically.
func (s *Sharko) Stop() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.server != nil {
		s.server.Close()
	}
}

// WaitHealthy polls GET /api/v1/health until it returns 200 OK or the
// timeout elapses. Calls t.Fatalf on timeout. Default poll interval 100ms.
//
// In-process mode is typically ready in <50ms so a 5–10s timeout is more
// than enough; helm mode (when implemented) will need 60+ seconds.
func (s *Sharko) WaitHealthy(t *testing.T, timeout time.Duration) {
	t.Helper()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	pollInterval := 100 * time.Millisecond
	url := s.URL + "/api/v1/health"

	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			lastStatus = resp.StatusCode
			// Drain to allow connection reuse and free server goroutines.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("Sharko.WaitHealthy: %s did not return 200 within %s (last status=%d, last err=%v)",
		url, timeout, lastStatus, lastErr)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// tempFile returns the absolute path of a freshly-created empty file under
// t.TempDir(). The pattern follows os.CreateTemp semantics ("foo-*.bar").
func tempFile(t *testing.T, pattern string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), pattern)
	if err != nil {
		t.Fatalf("tempFile(%q): %v", pattern, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		t.Fatalf("tempFile close %q: %v", name, err)
	}
	return name
}

// randHex returns 2*n hex characters of crypto-random data.
func randHex(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("randHex: %v", err)
	}
	return hex.EncodeToString(buf)
}

