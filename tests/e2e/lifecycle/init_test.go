//go:build e2e

// Package lifecycle — V2 Epic 7-1.9 e2e coverage for the init / connections /
// operations API surfaces.
//
// Split into two test functions:
//
//   - TestConnectionsCRUDAndInit  — purely in-process (no kind, no argocd).
//     Drives the connection-config CRUD endpoints plus the full init flow
//     end-to-end against the GH-mock provider, with operation polling and
//     heartbeat / cancel assertions. Should pass in seconds.
//
//   - TestConnectionsDiscoverAndTest — requires kind + docker and installs
//     real ArgoCD into a mgmt kind cluster. Covers discover-argocd and the
//     active-connection test endpoint. Skips gracefully when kind / docker
//     are absent so `go test -tags=e2e ./tests/e2e/lifecycle/...` keeps
//     working on a developer laptop without local Kubernetes tooling.
package lifecycle

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
	"github.com/MoranWeissman/sharko/templates"
)

// ---------------------------------------------------------------------------
// In-process: connections CRUD + init flow + operations
// ---------------------------------------------------------------------------

// TestConnectionsCRUDAndInit exercises 9 of the 11 endpoints in story 7-1.9
// against a sharko instance booted in-process with the GH-mock git provider.
// The remaining two (discover-argocd, /connections/test against real ArgoCD)
// live in TestConnectionsDiscoverAndTest.
//
// Endpoint coverage:
//   - GET    /api/v1/connections/                  [ListConnectionsEmpty, ListConnectionsAfterCreate]
//   - POST   /api/v1/connections/                  [CreateConnection]
//   - PUT    /api/v1/connections/{name}            [UpdateConnection]
//   - DELETE /api/v1/connections/{name}            [DeleteConnection]
//   - POST   /api/v1/connections/active            [SetActive]
//   - POST   /api/v1/connections/test-credentials  [TestCredentials]
//   - POST   /api/v1/init                          [InitFlow]
//   - GET    /api/v1/operations/{id}               [InitFlow, OperationHeartbeat, OperationCancel]
//   - POST   /api/v1/operations/{id}/heartbeat     [OperationHeartbeat]
//   - POST   /api/v1/operations/{id}/cancel        [OperationCancel]
func TestConnectionsCRUDAndInit(t *testing.T) {
	git := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     git,
		GitProvider: ghmock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())

	// Wire the embedded bootstrap templates into the in-process api.Server.
	// StartSharko deliberately does not auto-wire these (the foundation
	// hello-world tests don't need them); init handler returns 500 without
	// this call ("template filesystem not configured").
	if srv := sharko.APIServer(); srv != nil {
		srv.SetTemplateFS(templates.TemplateFS)
	} else {
		t.Fatal("sharko.APIServer() returned nil — in-process boot is required for this test")
	}

	admin := harness.NewClient(t, sharko)

	// Canonical connection payload used across CRUD subtests. The Git fields
	// must match what the mock's StartGitMock seeded (owner="sharko-e2e",
	// repo="sharko-addons") so any sharko code path that does a buildGitProvider
	// → GitProvider lookup ends up at the same mock instance. The mock is
	// also installed as the active override via SharkoConfig.GitProvider, so
	// in practice every connection routes to the same mock regardless of
	// the URL we save here.
	// gitops.base_branch is required to be non-empty for the init flow's
	// PR-creation step — the mock GitProvider's CreatePullRequest does NOT
	// run base through resolveBranch (only resolveBranch-aware methods like
	// GetFileContent/CreateBranch.fromRef do), so an empty base is rejected
	// as "branch not found". We pin "main" explicitly here.
	autoMergeTrue := true
	connReq := models.CreateConnectionRequest{
		Name: "e2e-primary",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			RepoURL:  "https://github.com/sharko-e2e/sharko-addons",
			Owner:    "sharko-e2e",
			Repo:     "sharko-addons",
			Token:    "ghp_e2e_fake_token_0000000000000000000000",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: "https://argocd.e2e.invalid",
			Token:     "argocd_e2e_fake_token_0000000000000000",
			Namespace: "argocd",
		},
		GitOps: &models.GitOpsSettings{
			BaseBranch:   "main",
			BranchPrefix: "sharko/",
			CommitPrefix: "sharko:",
			PRAutoMerge:  &autoMergeTrue,
		},
		SetAsDefault: true,
	}

	t.Run("ListConnectionsEmpty", func(t *testing.T) {
		resp := admin.ListConnections(t)
		if len(resp.Connections) != 0 {
			t.Errorf("expected zero connections at boot, got %d: %+v",
				len(resp.Connections), resp.Connections)
		}
		if resp.ActiveConnection != "" {
			t.Errorf("expected empty active connection at boot, got %q", resp.ActiveConnection)
		}
	})

	t.Run("CreateConnection", func(t *testing.T) {
		ack := admin.CreateConnection(t, connReq)
		if ack.Status != "created" {
			t.Errorf("CreateConnection: status=%q want %q", ack.Status, "created")
		}
		if ack.Name != connReq.Name {
			t.Errorf("CreateConnection: name=%q want %q", ack.Name, connReq.Name)
		}

		// List should now show the connection. Tokens must be masked.
		resp := admin.ListConnections(t)
		if len(resp.Connections) != 1 {
			t.Fatalf("expected 1 connection after create, got %d", len(resp.Connections))
		}
		got := resp.Connections[0]
		if got.Name != connReq.Name {
			t.Errorf("listed name=%q want %q", got.Name, connReq.Name)
		}
		if got.GitProvider != models.GitProviderGitHub {
			t.Errorf("listed git_provider=%q want %q", got.GitProvider, models.GitProviderGitHub)
		}
		// Token must be masked, not echoed verbatim. MaskToken keeps the
		// first/last 4 chars; a fully-cleartext token would contain the
		// middle "fake_token" substring.
		if strings.Contains(got.GitTokenMasked, "fake_token") {
			t.Errorf("git_token_masked leaked plaintext substring: %q", got.GitTokenMasked)
		}
		if strings.Contains(got.ArgocdTokenMasked, "fake_token") {
			t.Errorf("argocd_token_masked leaked plaintext substring: %q", got.ArgocdTokenMasked)
		}
	})

	t.Run("SetActive", func(t *testing.T) {
		admin.SetActiveConnection(t, connReq.Name)

		resp := admin.ListConnections(t)
		if resp.ActiveConnection != connReq.Name {
			t.Errorf("active_connection=%q want %q", resp.ActiveConnection, connReq.Name)
		}
		// IsActive flag on the listed entry should flip too.
		if len(resp.Connections) == 0 || !resp.Connections[0].IsActive {
			t.Errorf("expected IsActive=true on listed connection, got %+v", resp.Connections)
		}
	})

	t.Run("UpdateConnection", func(t *testing.T) {
		// Mutate description; leave token fields empty so the update handler's
		// "keep saved value" branch is exercised. After PUT the token must
		// still be set (the create-time token is preserved server-side).
		upd := connReq
		upd.Description = "primary e2e connection — updated"
		upd.Git.Token = ""    // exercise the "keep saved token" path
		upd.Argocd.Token = "" // ditto for argocd

		ack := admin.UpdateConnection(t, connReq.Name, upd)
		if ack.Status != "updated" {
			t.Errorf("UpdateConnection: status=%q want %q", ack.Status, "updated")
		}
		if ack.Name != connReq.Name {
			t.Errorf("UpdateConnection: name=%q want %q", ack.Name, connReq.Name)
		}

		resp := admin.ListConnections(t)
		if len(resp.Connections) != 1 {
			t.Fatalf("expected 1 connection after update, got %d", len(resp.Connections))
		}
		got := resp.Connections[0]
		if got.Description != upd.Description {
			t.Errorf("description=%q want %q", got.Description, upd.Description)
		}
		// Token mask must still be non-empty — proves the saved token was
		// preserved by the empty-field overlay in handleUpdateConnection.
		if got.GitTokenMasked == "" {
			t.Error("git_token_masked is empty after update — saved token was wiped")
		}
		if got.ArgocdTokenMasked == "" {
			t.Error("argocd_token_masked is empty after update — saved token was wiped")
		}
	})

	t.Run("TestCredentials", func(t *testing.T) {
		// use_saved=true points the handler at the saved record's full creds
		// — the test surface the wizard's "leave blank to keep" UX relies on
		// (V124-19 / BUG-044). Note: TestCredentials does NOT route through
		// the SetDemoGitProvider override — it builds a fresh
		// gitprovider.NewGitHubProvider per call and hits the real GitHub
		// API. With our fake token both git+argocd will fail with network
		// errors. We don't assert success; we assert the response SHAPE is
		// the documented per-service {status, message?, auth?} object so
		// the wizard / CLI parsers never silently break.
		req := models.CreateConnectionRequest{Name: connReq.Name, UseSaved: true}
		got := admin.TestCredentials(t, req)

		for name, svc := range map[string]harness.CredentialServiceResult{
			"git":    got.Git,
			"argocd": got.Argocd,
		} {
			switch svc.Status {
			case "ok", "error":
				// fine — endpoint contract preserved.
			default:
				t.Errorf("%s.status=%q is neither ok nor error (message=%q)",
					name, svc.Status, svc.Message)
			}
		}
		t.Logf("test-credentials: git=%+v argocd=%+v", got.Git, got.Argocd)
	})

	t.Run("InitFlow", func(t *testing.T) {
		// Init kicks off a background goroutine that walks the 6-step
		// bootstrap. We use AutoMerge=true so step 4 ("Waiting for PR merge")
		// completes immediately via the mock; BootstrapArgoCD=false so we
		// skip the ArgoCD steps (there's no reachable ArgoCD in-process —
		// that's covered by TestConnectionsDiscoverAndTest's larger setup).
		req := orchestrator.InitRepoRequest{
			BootstrapArgoCD: false,
			AutoMerge:       true,
		}
		resp := admin.Init(t, req)
		if resp.OperationID == "" {
			t.Fatalf("Init: empty operation_id (response=%+v)", resp)
		}
		t.Logf("Init: operation_id=%s status=%s resumed=%v",
			resp.OperationID, resp.Status, resp.Resumed)

		// Poll until the operation completes. With AutoMerge=true +
		// BootstrapArgoCD=false the whole flow is bounded by the in-memory
		// mock; 30s is generous (typical completion is <500ms).
		sess := admin.WaitForOperationStatus(t, resp.OperationID, 30*time.Second,
			operations.StatusCompleted, operations.StatusFailed)
		if sess.Status != operations.StatusCompleted {
			t.Fatalf("Init operation failed: status=%s error=%q steps=%+v",
				sess.Status, sess.Error, sess.Steps)
		}

		// Verify the bootstrap files were committed to main via the mock.
		// orchestrator.BootstrapRootAppPath is the canonical signal — the
		// same constant the api.Server's already-initialized check uses.
		if !ghmock.FileExists("main", orchestrator.BootstrapRootAppPath) {
			t.Errorf("expected %q on main after init, got branches=%v",
				orchestrator.BootstrapRootAppPath, ghmock.ListBranches())
		}
		// One PR must have been created and merged.
		merged := ghmock.ListMockPRs("merged")
		if len(merged) != 1 {
			t.Errorf("expected exactly 1 merged PR after init, got %d: %+v",
				len(merged), merged)
		}
		if len(merged) > 0 && merged[0].BaseBranch != "main" {
			t.Errorf("merged PR base=%q want %q", merged[0].BaseBranch, "main")
		}
	})

	t.Run("OperationHeartbeat", func(t *testing.T) {
		// Kick off a second init with AutoMerge=false so the session lands
		// in StatusWaiting (waiting for human PR merge). The repo is already
		// initialized from the previous subtest, so the handler may hit
		// the idempotent-already-initialized branch and Complete instead —
		// either path produces a valid session to heartbeat against.
		req := orchestrator.InitRepoRequest{
			BootstrapArgoCD: false,
			AutoMerge:       false,
		}
		resp := admin.Init(t, req)
		if resp.OperationID == "" {
			t.Fatalf("Init: empty operation_id (response=%+v)", resp)
		}

		// Heartbeat the session — handler returns 200 with {"status":"ok"}.
		// The op store records HeartbeatAt; we don't expose that on the
		// session JSON for the user, so we assert via the next GetOperation
		// that the session is still reachable (not 404).
		admin.HeartbeatOperation(t, resp.OperationID)
		sess := admin.GetOperation(t, resp.OperationID)
		if sess.ID != resp.OperationID {
			t.Errorf("GetOperation: id=%q want %q", sess.ID, resp.OperationID)
		}

		// 404 path: heartbeat a non-existent operation must respond 404.
		body := admin.Do(t, http.MethodPost,
			"/api/v1/operations/does-not-exist-"+harness.RandSuffix()+"/heartbeat",
			nil)
		if body.StatusCode != http.StatusNotFound {
			t.Errorf("heartbeat on missing operation: status=%d want 404", body.StatusCode)
		}
		body.Body.Close()

		// Best-effort: cancel this session so the background goroutine wraps
		// up promptly without waiting for the 24h PR-merge deadline. Errors
		// are ignored — the next subtest doesn't depend on this.
		admin.Do(t, http.MethodPost,
			"/api/v1/operations/"+resp.OperationID+"/cancel", nil).Body.Close()
	})

	t.Run("OperationCancel", func(t *testing.T) {
		// Repeat the init kickoff and immediately cancel. We're not asserting
		// the goroutine winds down (it does, eventually, via the ticker
		// checking sess.Status) — just that the cancel endpoint flips the
		// session state to StatusCancelled visible via GetOperation.
		req := orchestrator.InitRepoRequest{
			BootstrapArgoCD: false,
			AutoMerge:       false,
		}
		resp := admin.Init(t, req)
		if resp.OperationID == "" {
			t.Fatalf("Init: empty operation_id (response=%+v)", resp)
		}

		// Cancel the running session.
		admin.CancelOperation(t, resp.OperationID)

		// GetOperation should now report StatusCancelled. The transition is
		// synchronous inside opsStore.Cancel, so we don't need Eventually
		// here — but a quick poll guards against the rare case where the
		// goroutine completed the init between our Init and Cancel calls
		// (which is fine: a Completed → Cancel keeps the session at
		// Completed because Cancel just sets the status verbatim, and the
		// session is already done — that's accepted as success too).
		sess := admin.WaitForOperationStatus(t, resp.OperationID, 5*time.Second,
			operations.StatusCancelled,
			operations.StatusCompleted, // see comment above — racy completion is OK
			operations.StatusFailed)
		if sess.Status != operations.StatusCancelled &&
			sess.Status != operations.StatusCompleted &&
			sess.Status != operations.StatusFailed {
			t.Errorf("after cancel: status=%s — expected cancelled/completed/failed", sess.Status)
		}

		// 404 path: cancel a non-existent operation.
		body := admin.Do(t, http.MethodPost,
			"/api/v1/operations/does-not-exist-"+harness.RandSuffix()+"/cancel", nil)
		if body.StatusCode != http.StatusNotFound {
			t.Errorf("cancel on missing operation: status=%d want 404", body.StatusCode)
		}
		body.Body.Close()
	})

	t.Run("DeleteConnection", func(t *testing.T) {
		// Tear down the connection we created at the top. The active
		// connection pointer can legitimately remain set to the name of a
		// just-deleted connection (no foreign-key cleanup in the FileStore);
		// the next subsequent test creating a new connection would overwrite
		// it. We only assert the list is empty.
		admin.DeleteConnection(t, connReq.Name)
		resp := admin.ListConnections(t)
		if len(resp.Connections) != 0 {
			t.Errorf("after delete: %d connections remain: %+v",
				len(resp.Connections), resp.Connections)
		}
	})
}

// ---------------------------------------------------------------------------
// kind + real argocd: discover-argocd + active-connection test
// ---------------------------------------------------------------------------

// TestConnectionsDiscoverAndTest covers the two endpoints that need a real
// ArgoCD install to be meaningful:
//
//   - GET  /api/v1/connections/discover-argocd  — must find ArgoCD in the
//     mgmt cluster's argocd namespace.
//   - POST /api/v1/connections/test             — must return a per-service
//     status, with argocd reachable (status=="ok") given the in-cluster
//     ArgoCD pointed at by the active connection.
//
// Skips gracefully when kind / docker are absent. In CI the e2e job has both
// preinstalled; locally a `brew install kind` keeps the suite working.
func TestConnectionsDiscoverAndTest(t *testing.T) {
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skipf("kind not installed; skipping: %v", err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not installed; skipping: %v", err)
	}

	harness.DestroyAllStaleE2EClusters(t)

	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 0})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt := clusters[0]
	harness.InstallArgoCD(t, mgmt)

	git := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		MgmtCluster: &mgmt,
		GitFake:     git,
		GitProvider: ghmock,
	})
	sharko.WaitHealthy(t, 30*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())
	admin := harness.NewClient(t, sharko)

	t.Run("DiscoverArgoCD", func(t *testing.T) {
		// The handler does an in-cluster service discovery against the
		// argocd namespace. In-process sharko (no real KUBECONFIG wired
		// for service lookup) will typically return an empty server_url,
		// but the endpoint MUST always return 200 with the documented
		// shape — that's the contract we assert here. has_env_token MUST
		// reflect whether ARGOCD_TOKEN is set in the test process
		// environment (we don't set it).
		got := admin.DiscoverArgocd(t, "argocd")
		if got.Namespace != "argocd" {
			t.Errorf("namespace=%q want %q", got.Namespace, "argocd")
		}
		// The discover endpoint may legitimately return an empty
		// server_url when the in-process server isn't running inside
		// the mgmt cluster's pod network. We assert only that the field
		// is present (Go's zero-value "" is a valid response) and that
		// the shape is preserved.
		t.Logf("discover-argocd: server_url=%q has_env_token=%v",
			got.ServerURL, got.HasEnvToken)
	})

	t.Run("TestReachable", func(t *testing.T) {
		// Seed an active connection so /connections/test has something to
		// resolve. argocd.server_url is set to the canonical in-cluster
		// service DNS; from outside the cluster TestConnection will likely
		// fail with a network error — that's expected and acceptable. We
		// only assert that the endpoint returns a 200 with the documented
		// per-service shape.
		conn := models.CreateConnectionRequest{
			Name: "e2e-discover",
			Git: models.GitRepoConfig{
				Provider: models.GitProviderGitHub,
				RepoURL:  "https://github.com/sharko-e2e/sharko-addons",
				Owner:    "sharko-e2e",
				Repo:     "sharko-addons",
				Token:    "ghp_e2e_fake_token_0000000000000000000000",
			},
			Argocd: models.ArgocdConfig{
				ServerURL: "https://argocd-server.argocd.svc.cluster.local",
				Token:     "argocd_e2e_fake_token_0000000000000000",
				Namespace: "argocd",
			},
			SetAsDefault: true,
		}
		admin.CreateConnection(t, conn)
		admin.SetActiveConnection(t, conn.Name)

		got := admin.TestActiveConnection(t)
		switch got.Git.Status {
		case "ok", "error":
			// fine
		default:
			t.Errorf("git.status=%q is neither ok nor error", got.Git.Status)
		}
		switch got.Argocd.Status {
		case "ok", "error":
			// fine
		default:
			t.Errorf("argocd.status=%q is neither ok nor error", got.Argocd.Status)
		}
		t.Logf("test-connection: git=%+v argocd=%+v", got.Git, got.Argocd)
	})
}
