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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
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
// payload for orchestrator.BootstrapRootAppPath on the base branch and an
// error for every other path (mirrors the real provider's "not found"
// behavior).
//
// V124-20 / BUG-045: the probe path is now sourced from the constant rather
// than a literal "bootstrap/root-app.yaml". Pre-V124-20 these tests passed
// because the literal matched the (then-buggy) API check; both layers
// have been corrected to point at the actual committed path
// ("root-app.yaml" at repo root, no bootstrap/ prefix).
type initFakeGit struct {
	rootAppExists bool
	// writeCalls counts every call to a mutating GitProvider method
	// (CreateBranch, CreateOrUpdateFile, BatchCreateFiles, DeleteFile,
	// CreatePullRequest, MergePullRequest, DeleteBranch). w2-q2's repair path
	// (RepoStatePartial + bootstrapAbsent) must perform NO git writes — this
	// counter is how TestRunInitOperation_AbsentBootstrapApp_Repairs proves
	// that.
	writeCalls atomic.Int32
}

func (f *initFakeGit) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if path == orchestrator.BootstrapRootAppPath && f.rootAppExists {
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

func (f *initFakeGit) TestConnection(_ context.Context) error { return nil }
func (f *initFakeGit) CreateBranch(_ context.Context, _, _ string) error {
	f.writeCalls.Add(1)
	return nil
}
func (f *initFakeGit) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	f.writeCalls.Add(1)
	return nil
}
func (f *initFakeGit) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	f.writeCalls.Add(1)
	return nil
}
func (f *initFakeGit) DeleteFile(_ context.Context, _, _, _ string) error {
	f.writeCalls.Add(1)
	return nil
}
func (f *initFakeGit) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	f.writeCalls.Add(1)
	return nil, nil
}
func (f *initFakeGit) MergePullRequest(_ context.Context, _ int) error {
	f.writeCalls.Add(1)
	return nil
}
func (f *initFakeGit) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "open", nil
}
func (f *initFakeGit) DeleteBranch(_ context.Context, _ string) error {
	f.writeCalls.Add(1)
	return nil
}

// initFakeArgocd is a minimal orchestrator.ArgocdClient. Every method except
// GetApplication is a no-op — the BUG-034 already-initialized branch only
// touches GetApplication.
type initFakeArgocd struct {
	app    *models.ArgocdApplication // returned from GetApplication when getErr is nil
	getErr error
	// listApps / listErr drive ListApplications, which ProbeBootstrapApp now
	// uses (V2-cleanup-11.2). When listApps is nil and listErr is nil, the
	// fixture synthesizes the list from app (so existing healthy/forbidden
	// fixtures keep working): a non-nil app → one-element list; a nil app with
	// a permission-denied getErr → that same error on LIST; a nil app with no
	// getErr → empty list (the "absent" case).
	listApps []models.ArgocdApplication
	listErr  error
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

// ListApplications backs the V2-cleanup-11.2 LIST-and-filter probe. Explicit
// listApps/listErr win; otherwise the result is synthesized from the existing
// app/getErr fields so pre-11.2 fixtures (healthy / forbidden / no-app) keep
// classifying the same way under the new probe.
func (a *initFakeArgocd) ListApplications(_ context.Context) ([]models.ArgocdApplication, error) {
	if a.listErr != nil {
		return nil, a.listErr
	}
	if a.listApps != nil {
		return a.listApps, nil
	}
	if a.getErr != nil {
		// A forbidden/permission fixture (getErr wraps ErrPermissionDenied)
		// should also make the LIST fail with that error.
		return nil, a.getErr
	}
	if a.app != nil {
		return []models.ArgocdApplication{*a.app}, nil
	}
	// Nil app, no error → the app simply does not exist yet (absent case).
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newInitTestServer builds a Server with just the fields runInitOperation
// touches when it returns early on the already-initialized branch:
//   - opsStore   (Create/Start/UpdateStep/Complete/Fail)
//   - gitMu      (mutex passed to orchestrator.New)
//   - auditLog   (always written from runInitOperation tail; we don't reach it
//     on the early-return path but it must be non-nil to avoid a
//     nil-deref if the test is later extended)
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

// TestRunInitOperation_ListFailsGeneric_Fails covers a LIST call that fails
// for an uncategorized reason (neither a 403 permission problem nor a 401
// invalid token) when the repo is already initialized. Sharko never got app
// data back, so the operation must Fail honestly with "couldn't check" —
// NOT the old "missing or unhealthy" wording, which asserted a fact Sharko
// never established (error review package 1; this fixture predates
// ErrTokenInvalid and used to stand in for what a real 401 looked like
// before that sentinel existed).
func TestRunInitOperation_ListFailsGeneric_Fails(t *testing.T) {
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
	wantSubstr := "Sharko could not check whether the ArgoCD bootstrap application is healthy"
	if !strings.Contains(sess.Error, wantSubstr) {
		t.Errorf("expected error to contain %q, got %q", wantSubstr, sess.Error)
	}
	// The underlying LIST error must still be threaded through so the user
	// sees the actual reason — not a generic message.
	if !strings.Contains(sess.Error, "not found") {
		t.Errorf("expected error to surface the underlying LIST error, got %q", sess.Error)
	}
}

// TestRunInitOperation_AuthFailed_Fails — a 401 from ArgoCD's LIST call
// (invalid/expired token) must Fail with the actionable token message, not
// be mislabeled as a broken bootstrap app. This is the exact root-cause
// scenario error review package 1 exists to fix: before ErrTokenInvalid and
// bootstrapAuthFailed existed, this fell through to the same generic bucket
// as TestRunInitOperation_ListFailsGeneric_Fails and produced the false
// "already exists but is not healthy" claim.
func TestRunInitOperation_AuthFailed_Fails(t *testing.T) {
	s := newInitTestServer()
	gp := &initFakeGit{rootAppExists: true}
	ac := &initFakeArgocd{
		listErr: fmt.Errorf("listing applications: %w", argocd.ErrTokenInvalid),
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
	if !strings.Contains(sess.Error, "invalid ArgoCD token") {
		t.Errorf("expected error to contain the actionable token message, got %q", sess.Error)
	}
	if strings.Contains(sess.Error, "already exists") {
		t.Errorf("auth-failed must NOT claim anything about the app already existing, got %q", sess.Error)
	}
	if gp.writeCalls.Load() != 0 {
		t.Errorf("expected zero git writes when refusing on an auth failure, got %d", gp.writeCalls.Load())
	}
}

// When the repo is already initialized AND the ArgoCD app exists but is
// OutOfSync / Degraded, the operation must Fail with a descriptive error
// that includes the unhealthy status. This protects against the
// "manually deleted the deployment" partial-state case.
//
// w2-q2: the message is also improved to plainly say the app already
// exists — so re-running Initialize (repair) will not fix it — and to name
// the app and point at ArgoCD/diagnostics, instead of the old generic
// "missing or unhealthy" wording. This is the LOCKED "refuse-on-unhealthy"
// decision: files-present + app-exists-but-degraded is NOT the same as
// files-present + app-absent (which now repairs — see
// TestRunInitOperation_AbsentBootstrapApp_Repairs).
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
	for _, want := range []string{
		"sync=OutOfSync", "health=Degraded",
		orchestrator.BootstrapRootAppName,
		"already exists", "will not fix it",
	} {
		if !strings.Contains(sess.Error, want) {
			t.Errorf("expected error to contain %q, got %q", want, sess.Error)
		}
	}
	if gp.writeCalls.Load() != 0 {
		t.Errorf("expected zero git writes when refusing a degraded app, got %d", gp.writeCalls.Load())
	}
}

// ---------------------------------------------------------------------------
// w2-q2 — real repair for files-present + bootstrap-app-absent
// ---------------------------------------------------------------------------
//
// Background: the wizard's yellow banner told the user "Re-run initialize to
// repair it", but runInitOperation's RepoStatePartial branch Failed
// unconditionally — a dead end. These tests cover the fix: when the repo
// files are already there and the ONLY thing missing is the ArgoCD
// bootstrap Application (bootstrapAbsent), POST /init now repairs it —
// creates the Application and waits for sync — with zero git writes, no PR,
// no re-seeding. Genuinely degraded/unreachable apps still refuse (covered
// above and below).

// TestRunInitOperation_AbsentBootstrapApp_Repairs is the repair happy path:
// files exist, the bootstrap Application was never created (LIST succeeds,
// empty — the "absent" classification), so runInitOperation must create it
// and wait for sync, WITHOUT touching git at all. gp.writeCalls proves no
// git write method was ever called — the assertion the story called out
// explicitly.
func TestRunInitOperation_AbsentBootstrapApp_Repairs(t *testing.T) {
	s := newInitTestServer()
	gp := &initFakeGit{rootAppExists: true}
	ac := &initFakeArgocd{
		// Empty (non-nil) listApps drives ProbeBootstrapApp to "absent" —
		// LIST succeeded, the bootstrap app just isn't in the results yet.
		listApps: []models.ArgocdApplication{},
		// GetApplication (used by WaitForSync, called AFTER BootstrapArgoCD
		// "creates" the app) is pre-seeded as Synced+Healthy — the fake
		// doesn't simulate CreateApplication actually mutating state, so we
		// seed the post-creation view directly.
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
		},
	}

	sessID := runInit(s, gp, ac)

	if gp.writeCalls.Load() != 0 {
		t.Fatalf("repair must perform zero git writes, got %d", gp.writeCalls.Load())
	}

	sess, ok := s.opsStore.Get(sessID)
	if !ok {
		t.Fatalf("session %q not found in store", sessID)
	}
	if sess.Status != operations.StatusCompleted {
		t.Fatalf("expected status=%s, got %s (error=%q)",
			operations.StatusCompleted, sess.Status, sess.Error)
	}
	if !strings.Contains(sess.Result, "repaired") {
		t.Errorf("expected result to mention the repair, got %q", sess.Result)
	}

	// Steps 1-4 (the git side) were skipped-as-already-done with the
	// repair-specific detail; steps 5-6 (ArgoCD side) actually ran.
	if len(sess.Steps) != 6 {
		t.Fatalf("expected 6 steps, got %d", len(sess.Steps))
	}
	for i := 0; i < 4; i++ {
		step := sess.Steps[i]
		if step.Status != operations.StatusCompleted {
			t.Errorf("step %d (%q): expected status=completed, got %s", i, step.Name, step.Status)
		}
		if !strings.Contains(step.Detail, "repairing ArgoCD bootstrap only") {
			t.Errorf("step %d (%q): expected repair detail, got %q", i, step.Name, step.Detail)
		}
	}
	if sess.Steps[4].Status != operations.StatusCompleted || sess.Steps[4].Detail != "ArgoCD bootstrapped" {
		t.Errorf("step 4 (bootstrap): expected completed/%q, got %s/%q",
			"ArgoCD bootstrapped", sess.Steps[4].Status, sess.Steps[4].Detail)
	}
	if sess.Steps[5].Status != operations.StatusCompleted || sess.Steps[5].Detail != "synced" {
		t.Errorf("step 5 (sync): expected completed/%q, got %s/%q",
			"synced", sess.Steps[5].Status, sess.Steps[5].Detail)
	}
}

// TestRunInitOperation_UnreachableBootstrapApp_Fails covers the third split
// of the old blanket RepoStatePartial Fail: Sync=Unknown means ArgoCD can't
// reach/evaluate the repo at all — a connection problem, not a missing app.
// Re-running Initialize cannot fix it, so this must stay a Fail (with a
// message describing a connection problem, not "missing or unhealthy"), and
// must perform zero git writes.
func TestRunInitOperation_UnreachableBootstrapApp_Fails(t *testing.T) {
	s := newInitTestServer()
	gp := &initFakeGit{rootAppExists: true}
	ac := &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "Unknown",
			HealthStatus: "Error",
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
	for _, want := range []string{"cannot reach or evaluate the repository", "sync=Unknown"} {
		if !strings.Contains(sess.Error, want) {
			t.Errorf("expected error to contain %q, got %q", want, sess.Error)
		}
	}
	if gp.writeCalls.Load() != 0 {
		t.Errorf("expected zero git writes when refusing an unreachable repo, got %d", gp.writeCalls.Load())
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

// ---------------------------------------------------------------------------
// V2-cleanup-52 — ProbeBootstrapApp detail includes repo URL when available
// ---------------------------------------------------------------------------

// TestProbeBootstrapApp_UnreachableWithRepoURL verifies that when the bootstrap
// app is present but unreachable (Sync=Unknown) AND SourceRepoURL is set, the
// returned detail contains the repo URL. The poller composes the bell
// Description as "lead + ' Reason: ' + detail", so this makes the bell alert
// name the failing repo (V2-cleanup-52).
func TestProbeBootstrapApp_UnreachableWithRepoURL(t *testing.T) {
	const repoURL = "https://github.com/example/gitops"
	ac := &initFakeArgocd{
		// listApps is set directly so ListApplications returns the app with
		// SourceRepoURL populated — ProbeBootstrapApp uses LIST-and-filter.
		listApps: []models.ArgocdApplication{
			{
				Name:          orchestrator.BootstrapRootAppName,
				SyncStatus:    "Unknown",
				HealthStatus:  "Missing",
				SourceRepoURL: repoURL,
			},
		},
	}

	status, detail := ProbeBootstrapApp(context.Background(), ac)

	if status != bootstrapUnreachable {
		t.Errorf("expected status=%q, got %q", bootstrapUnreachable, status)
	}
	if !strings.Contains(detail, repoURL) {
		t.Errorf("expected detail to contain repo URL %q, got %q", repoURL, detail)
	}
	// V2-cleanup-51 contract: sync= and health= must still be present.
	if !strings.Contains(detail, "sync=Unknown") {
		t.Errorf("expected detail to contain sync=Unknown, got %q", detail)
	}
	if !strings.Contains(detail, "health=Missing") {
		t.Errorf("expected detail to contain health=Missing, got %q", detail)
	}
}

// TestProbeBootstrapApp_UnhealthyEmptyRepoURL verifies that when SourceRepoURL
// is empty the detail does NOT contain a trailing "repo=" artifact — the string
// must read cleanly without an empty URL field (V2-cleanup-52).
func TestProbeBootstrapApp_UnhealthyEmptyRepoURL(t *testing.T) {
	ac := &initFakeArgocd{
		listApps: []models.ArgocdApplication{
			{
				Name:          orchestrator.BootstrapRootAppName,
				SyncStatus:    "OutOfSync",
				HealthStatus:  "Degraded",
				SourceRepoURL: "", // empty — must produce no repo= artifact
			},
		},
	}

	_, detail := ProbeBootstrapApp(context.Background(), ac)

	if strings.Contains(detail, "repo=") {
		t.Errorf("expected no repo= artifact in detail when SourceRepoURL is empty, got %q", detail)
	}
}
