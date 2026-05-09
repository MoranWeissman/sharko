// init_test.go — V124-15 / BUG-034 regression coverage for runInitOperation's
// already-initialized branch.
//
// Background: prior to V124-15, runInitOperation's "is repo already
// initialized?" check Failed the operation unconditionally when
// bootstrap/root-app.yaml existed on the base branch. That broke the
// idempotent-retry case — a user who genuinely succeeded on a previous run
// and clicks Initialize again would see the wizard render red ("repo already
// initialized: bootstrap/root-app.yaml exists") even though their cluster
// was perfectly healthy.
//
// V124-15 disambiguates by probing ArgoCD: when the bootstrap root app is
// Synced + Healthy, the operation Completes (idempotent success); otherwise
// it Fails with a descriptive error so the user can act.
//
// These tests exercise that branch only — full first-run init with all six
// steps is covered by integration tests elsewhere.

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// ---------------------------------------------------------------------------
// Mocks tailored for runInitOperation early-exit tests.
// ---------------------------------------------------------------------------

// initFakeGit is a minimal gitprovider.GitProvider that returns the configured
// payload for "bootstrap/root-app.yaml" on the base branch and an error for
// every other path (mirrors the real provider's "not found" behavior).
type initFakeGit struct {
	rootAppExists bool
}

func (f *initFakeGit) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if path == "bootstrap/root-app.yaml" && f.rootAppExists {
		return []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"), nil
	}
	return nil, errors.New("not found: " + path)
}

func (f *initFakeGit) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (f *initFakeGit) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}

func (f *initFakeGit) TestConnection(_ context.Context) error                          { return nil }
func (f *initFakeGit) CreateBranch(_ context.Context, _, _ string) error               { return nil }
func (f *initFakeGit) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}
func (f *initFakeGit) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}
func (f *initFakeGit) DeleteFile(_ context.Context, _, _, _ string) error { return nil }
func (f *initFakeGit) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *initFakeGit) MergePullRequest(_ context.Context, _ int) error          { return nil }
func (f *initFakeGit) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "open", nil
}
func (f *initFakeGit) DeleteBranch(_ context.Context, _ string) error { return nil }

// initFakeArgocd is a minimal orchestrator.ArgocdClient. Every method except
// GetApplication is a no-op — the BUG-034 already-initialized branch only
// touches GetApplication.
type initFakeArgocd struct {
	app    *models.ArgocdApplication // returned from GetApplication when getErr is nil
	getErr error
}

func (a *initFakeArgocd) ListClusters(_ context.Context) ([]models.ArgocdCluster, error) {
	return nil, nil
}
func (a *initFakeArgocd) RegisterCluster(_ context.Context, _, _ string, _ []byte, _ string, _ map[string]string) error {
	return nil
}
func (a *initFakeArgocd) DeleteCluster(_ context.Context, _ string) error { return nil }
func (a *initFakeArgocd) UpdateClusterLabels(_ context.Context, _ string, _ map[string]string) error {
	return nil
}
func (a *initFakeArgocd) SyncApplication(_ context.Context, _ string) error { return nil }
func (a *initFakeArgocd) CreateProject(_ context.Context, _ []byte) error   { return nil }
func (a *initFakeArgocd) CreateApplication(_ context.Context, _ []byte) error {
	return nil
}
func (a *initFakeArgocd) AddRepository(_ context.Context, _, _, _ string) error { return nil }
func (a *initFakeArgocd) GetApplication(_ context.Context, _ string) (*models.ArgocdApplication, error) {
	return a.app, a.getErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newInitTestServer builds a Server with just the fields runInitOperation
// touches when it returns early on the already-initialized branch:
//   - opsStore   (Create/Start/UpdateStep/Complete/Fail)
//   - gitMu      (mutex passed to orchestrator.New)
//   - auditLog   (always written from runInitOperation tail; we don't reach it
//                 on the early-return path but it must be non-nil to avoid a
//                 nil-deref if the test is later extended)
func newInitTestServer() *Server {
	return &Server{
		opsStore: operations.NewStore(),
		gitMu:    sync.Mutex{},
		auditLog: audit.NewLog(100),
	}
}

// runInit is a thin wrapper that mirrors how handleInit kicks off the
// goroutine. We run it inline (not in a goroutine) for deterministic test
// assertions — runInitOperation has no external waits on the early-return
// branch, so synchronous execution is correct here.
func runInit(s *Server, gp gitprovider.GitProvider, ac orchestrator.ArgocdClient) string {
	steps := []string{
		"Creating bootstrap files",
		"Pushing to branch",
		"Creating pull request",
		"Waiting for PR merge",
		"Bootstrapping ArgoCD",
		"Waiting for sync",
	}
	session := s.opsStore.Create("init", steps)
	gitops := orchestrator.GitOpsConfig{BaseBranch: "main", RepoURL: "https://github.com/test/repo"}
	s.runInitOperation(context.Background(), session.ID, orchestrator.InitRepoRequest{}, gitops, gp, ac, nil)
	return session.ID
}

// ---------------------------------------------------------------------------
// V124-15 / BUG-034 — already-initialized branch
// ---------------------------------------------------------------------------

// When the repo is already initialized AND ArgoCD's bootstrap root app is
// Synced + Healthy, the operation must Complete (idempotent retry — the
// previous init genuinely succeeded). The wizard renders the
// "Repository initialized successfully" state.
func TestRunInitOperation_AlreadyInitialized_HealthyArgoCD_Completes(t *testing.T) {
	s := newInitTestServer()
	gp := &initFakeGit{rootAppExists: true}
	ac := &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
		},
	}

	sessID := runInit(s, gp, ac)

	sess, ok := s.opsStore.Get(sessID)
	if !ok {
		t.Fatalf("session %q not found in store", sessID)
	}
	if sess.Status != operations.StatusCompleted {
		t.Errorf("expected status=%s, got %s (error=%q)",
			operations.StatusCompleted, sess.Status, sess.Error)
	}
	if !strings.Contains(sess.Result, "already initialized") {
		t.Errorf("expected result to contain %q, got %q",
			"already initialized", sess.Result)
	}
	// All steps should be marked completed with the "already initialized"
	// detail so the wizard's step list renders cleanly.
	for i, step := range sess.Steps {
		if step.Status != operations.StatusCompleted {
			t.Errorf("step %d (%q): expected status=completed, got %s",
				i, step.Name, step.Status)
		}
		if step.Detail != "already initialized" {
			t.Errorf("step %d (%q): expected detail=%q, got %q",
				i, step.Name, "already initialized", step.Detail)
		}
	}
}

// When the repo is already initialized but the ArgoCD app is missing
// (GetApplication returns an error), the operation must Fail with a
// descriptive error so the user knows the cluster reality is broken even
// though the Git side looks done.
func TestRunInitOperation_AlreadyInitialized_MissingArgoCDApp_Fails(t *testing.T) {
	s := newInitTestServer()
	gp := &initFakeGit{rootAppExists: true}
	ac := &initFakeArgocd{
		getErr: errors.New("application not found: cluster-addons-bootstrap"),
	}

	sessID := runInit(s, gp, ac)

	sess, ok := s.opsStore.Get(sessID)
	if !ok {
		t.Fatalf("session %q not found in store", sessID)
	}
	if sess.Status != operations.StatusFailed {
		t.Errorf("expected status=%s, got %s (result=%q)",
			operations.StatusFailed, sess.Status, sess.Result)
	}
	wantSubstr := "repo initialized but ArgoCD bootstrap is missing or unhealthy"
	if !strings.Contains(sess.Error, wantSubstr) {
		t.Errorf("expected error to contain %q, got %q", wantSubstr, sess.Error)
	}
	// The detail from GetApplication must be threaded through so the user
	// sees the actual reason — not a generic message.
	if !strings.Contains(sess.Error, "not found") {
		t.Errorf("expected error to surface the GetApplication error, got %q", sess.Error)
	}
}

// When the repo is already initialized AND the ArgoCD app exists but is
// OutOfSync / Degraded, the operation must Fail with a descriptive error
// that includes the unhealthy status. This protects against the
// "manually deleted the deployment" partial-state case.
func TestRunInitOperation_AlreadyInitialized_UnhealthyArgoCDApp_Fails(t *testing.T) {
	s := newInitTestServer()
	gp := &initFakeGit{rootAppExists: true}
	ac := &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "OutOfSync",
			HealthStatus: "Degraded",
		},
	}

	sessID := runInit(s, gp, ac)

	sess, ok := s.opsStore.Get(sessID)
	if !ok {
		t.Fatalf("session %q not found in store", sessID)
	}
	if sess.Status != operations.StatusFailed {
		t.Errorf("expected status=%s, got %s (result=%q)",
			operations.StatusFailed, sess.Status, sess.Result)
	}
	for _, want := range []string{"sync=OutOfSync", "health=Degraded"} {
		if !strings.Contains(sess.Error, want) {
			t.Errorf("expected error to contain %q, got %q", want, sess.Error)
		}
	}
}

// ---------------------------------------------------------------------------
// V124-17 / BUG-041 — pollPRMerge tightened (immediate first probe + 5s ticker)
// ---------------------------------------------------------------------------

// pollFakeGit is a controllable gitprovider used by the BUG-041 tests. It
// counts GetFileContent calls (atomically — pollPRMerge runs in the same
// goroutine as the test in our calls below, but reads to the counter from
// the assertion are race-friendly anyway) and returns success/error
// according to the configured policy.
type pollFakeGit struct {
	initFakeGit
	calls atomic.Int32
	// returnSuccess: when set, every GetFileContent for bootstrap/root-app.yaml
	// returns a non-nil byte slice with nil error. When false (default), the
	// embedded initFakeGit.GetFileContent returns "not found".
	returnSuccess atomic.Bool
}

func (p *pollFakeGit) GetFileContent(ctx context.Context, path, branch string) ([]byte, error) {
	p.calls.Add(1)
	if p.returnSuccess.Load() {
		return []byte("apiVersion: argoproj.io/v1alpha1\n"), nil
	}
	return p.initFakeGit.GetFileContent(ctx, path, branch)
}

// TestPollPRMerge_ImmediateCheck_ReturnsTrueWithoutTickerWait confirms the
// V124-17 / BUG-041 fix: when the bootstrap file is already on the base
// branch, pollPRMerge must return true immediately without waiting for the
// ticker to fire. Pre-V124-17, the first GetFileContent probe happened
// 10s after entry; this test uses the production interval and a tight
// 200ms timeout to prove the immediate path.
func TestPollPRMerge_ImmediateCheck_ReturnsTrueWithoutTickerWait(t *testing.T) {
	s := newInitTestServer()
	steps := []string{"step-1"}
	session := s.opsStore.Create("init", steps)
	s.opsStore.Start(session.ID)

	gp := &pollFakeGit{}
	gp.returnSuccess.Store(true) // file already on base branch

	// Use a short context deadline as a safety net — if the immediate-probe
	// path were broken, the ticker (5s in production) would never fire
	// within this window and pollPRMerge would return false on ctx.Done.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	merged := s.pollPRMerge(ctx, session.ID, gp, "main")
	elapsed := time.Since(start)

	if !merged {
		t.Fatalf("expected pollPRMerge to return true (file present); got false")
	}
	// Immediate-check must complete well under 1s. 100ms is generous given
	// the only work is one mocked file-read.
	if elapsed > 100*time.Millisecond {
		t.Errorf("expected pollPRMerge to return within 100ms (immediate check); took %s", elapsed)
	}
	// Exactly one GetFileContent call — the immediate-probe path; the
	// ticker loop must not have run.
	if got := gp.calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 GetFileContent call (immediate probe); got %d", got)
	}
}

// TestPollPRMerge_TickerInterval_Is5Seconds confirms the V124-17 / BUG-041
// ticker tightening. We override the package-var to a tiny interval (so
// the test runs in milliseconds rather than seconds) and assert the
// production-default value is 5s.
//
// Why two assertions: the interval value is the contract (5s), but the
// observable behaviour we care about is "ticker actually fires at the
// configured cadence" — both protect against a regression that flips the
// value back to 10s but leaves the ticker plumbing intact.
func TestPollPRMerge_TickerInterval_Is5Seconds(t *testing.T) {
	if got := pollPRMergeInterval; got != 5*time.Second {
		t.Errorf("pollPRMergeInterval: expected 5s (V124-17 / BUG-041); got %s", got)
	}

	// Behaviour assertion: with the file NOT present, force the immediate
	// check to fail, then count tick-driven probes within a known window.
	// Override the interval to 10ms so we get ~10 ticks in 100ms.
	old := pollPRMergeInterval
	pollPRMergeInterval = 10 * time.Millisecond
	defer func() { pollPRMergeInterval = old }()

	s := newInitTestServer()
	steps := []string{"step-1"}
	session := s.opsStore.Create("init", steps)
	s.opsStore.Start(session.ID)
	// Mark the session as alive so the heartbeat guard inside pollPRMerge
	// doesn't bail out before the deadline.
	s.opsStore.Heartbeat(session.ID)

	gp := &pollFakeGit{} // returnSuccess=false ⇒ never reports merged

	// The session-alive guard inside pollPRMerge looks at the most recent
	// heartbeat. We hold a goroutine that re-heartbeats every 20ms so the
	// 2-minute IsAlive check never trips during the test window.
	hbCtx, hbCancel := context.WithCancel(context.Background())
	defer hbCancel()
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				s.opsStore.Heartbeat(session.ID)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	merged := s.pollPRMerge(ctx, session.ID, gp, "main")
	if merged {
		t.Fatalf("expected pollPRMerge to return false (file never present); got true")
	}

	// 1 (immediate) + ~9 ticks at 10ms intervals over 100ms. Allow a
	// generous range (≥3) to keep the test tolerant of CI scheduler
	// jitter while still proving the ticker fires more than once.
	calls := gp.calls.Load()
	if calls < 3 {
		t.Errorf("expected ≥3 GetFileContent calls (1 immediate + ticker firings within 100ms at 10ms cadence); got %d", calls)
	}
}

// Belt-and-suspenders: ensure the legacy 401 / "Session expired" reload path
// in the rest of api.ts is unaffected by the V124-15 OperationApiError change.
// This test is here (rather than in api.ts test) because it exercises the
// HTTP boundary, not the wizard. We just hit /api/v1/health unauthenticated.
// (Health is open in tests; this is mostly a sanity check that NewRouter
// still works after the test additions in this package.)
func TestNewRouter_StillBuildsAfterV12415(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 from /api/v1/health, got %d", w.Code)
	}
}
