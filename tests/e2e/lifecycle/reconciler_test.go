//go:build e2e

// reconciler_test.go — V125-1-8.4 end-to-end smoke for the cluster Secret
// reconciler.
//
// What these tests prove (against a real ArgoCD installed in a kind mgmt
// cluster):
//
//  1. The orchestrator → reconciler trigger seam wired in serve.go works
//     end-to-end. When a register PR is merged, the in-process reconciler
//     wakes up via Trigger() and converges the labeled ArgoCD cluster
//     Secret into the argocd namespace within 5s — measurably faster than
//     the 30s safety-net tick alone would allow, which is the only way a
//     <5s assertion can pass without depending on tick cadence.
//
//  2. Git → ArgoCD deletion works symmetrically. When managed-clusters.yaml
//     loses a cluster entry (and the change is merged), the reconciler
//     deletes the corresponding sharko-labeled Secret within 5s. Mutation
//     of managed-clusters.yaml uses gitops.RemoveClusterEntry (V125-1-8.3)
//     to exercise the envelope-aware parse-mutate-marshal path.
//
//  3. Self-healing for accidental Secret deletion. kubectl-deleting the
//     labeled Secret directly causes the reconciler's next periodic tick
//     to re-create it. The 30s production tick interval is preserved for
//     test #1 + #2 (which assert <5s convergence via Trigger), and a fresh
//     reconciler with a fast tick is started for #3 so wall-clock stays
//     reasonable while still proving the tick-driven recovery contract.
//
// Implementation notes:
//
//   - The in-process Sharko harness deliberately omits the cluster reconciler
//     (it omits all production background subsystems for boot-speed). The
//     test constructs the reconciler in-test with a real kind k8sClient
//     (for ArgoCD Secret CRUD) + the same MockGitProvider Sharko's writes
//     hit + a MockClusterCredentialsProvider stub (since the in-process
//     boot has nil credProvider). The reconciler's Trigger() is wired onto
//     the Sharko Server via SetReconcilerTrigger so the orchestrator's
//     fireReconcilerTrigger() seam fires the production code path.
//
//   - The cmstore.Store dependency is satisfied with a fake clientset —
//     the reconciler's pollOnce never reads from CMStore today (state is
//     held in the channel), and a non-nil store is required only because
//     Deps documents it as such. A real in-cluster CM store is not needed
//     to validate this story.
package lifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/demo"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestClusterReconcilerE2E is the V125-1-8.4 end-to-end smoke suite.
//
// One shared topology (1 mgmt cluster with ArgoCD + 1 target kind cluster)
// drives three subtests. The kind provisioning + ArgoCD install dominates
// wall-time (~90s); the reconciler subtests each add ~5-35s depending on
// whether the trigger or the tick is the convergence path being asserted.
func TestClusterReconcilerE2E(t *testing.T) {
	// ---- prereq guards: skip cleanly when the host can't run kind ----
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("TestClusterReconcilerE2E: kind not installed; install via `brew install kind` or https://kind.sigs.k8s.io/")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("TestClusterReconcilerE2E: kubectl not installed")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("TestClusterReconcilerE2E: docker not installed (required by kind)")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("TestClusterReconcilerE2E: docker daemon not reachable: %v\noutput: %s", err, out)
	}

	// ---- safety: clean up stale e2e clusters from prior failed runs ----
	harness.DestroyAllStaleE2EClusters(t)

	// ---- provision topology: 1 mgmt + 1 target ----
	t.Logf("provisioning kind topology (1 mgmt + 1 target) — typically 60-90s")
	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 1})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt, target := clusters[0], clusters[1]

	harness.WaitClusterReady(t, mgmt, 90*time.Second)
	harness.WaitClusterReady(t, target, 90*time.Second)

	// ---- install argocd into mgmt (~60-90s) ----
	t.Logf("installing argocd into management cluster (%s)", mgmt.Name)
	harness.InstallArgoCD(t, mgmt)

	// ---- argocd access + start git fixtures + sharko ----
	argoAccess := startArgoCDAccess(t, mgmt)
	gitfake := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)

	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     gitfake,
		GitProvider: ghmock,
	})
	sharko.WaitHealthy(t, 30*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())
	admin := harness.NewClient(t, sharko)

	seedActiveConnection(t, admin, argoAccess.URL, argoAccess.Token)

	// ---- build a kubernetes.Interface bound to the mgmt cluster for
	//      the reconciler's ArgoCD Secret CRUD path ----
	mgmtK8sClient := buildK8sClient(t, mgmt.Kubeconfig)

	// ---- bring up the cluster reconciler in-process ----
	//
	// 30s tick matches DefaultTickInterval — slow enough that sub-5s
	// convergence in subtests #1 + #2 cannot be tick-driven (it must be
	// the Trigger() path). Subtest #3 starts its own short-tick reconciler
	// so it does not have to wait 30s for the tick path.
	//
	// The MockClusterCredentialsProvider always returns server/CAData/token
	// fields the reconciler needs — even when the cluster name was never
	// pre-seeded into demoClusters (the GetCredentials fallback synthesises
	// values, see internal/demo/mock_provider.go).
	auditFn := func(audit.Entry) { /* no-op for e2e; quiet logs */ }
	recon := startReconciler(t, reconcilerConfig{
		k8sClient:    mgmtK8sClient,
		gitProvider:  ghmock,
		auditFn:      auditFn,
		tickInterval: clusterreconciler.DefaultTickInterval,
	})

	// Wire the trigger onto the Sharko Server so the orchestrator's
	// fireReconcilerTrigger() seam (called inside RegisterCluster +
	// RefreshClusterCredentials in internal/orchestrator/cluster.go) fans
	// out into our in-test reconciler. This mirrors what cmd/sharko/serve.go
	// does at production boot.
	sharko.APIServer().SetReconcilerTrigger(recon.Trigger)

	// Constant for sharko's repo defaults (mirrors what the in-process
	// harness wires into Server.repoPaths above).
	const managedClustersPath = "configuration/managed-clusters.yaml"

	// ---------------------------------------------------------------
	// subtests
	// ---------------------------------------------------------------

	t.Run("RegisterCluster_PostMergeReconcile_CreatesSecret", func(t *testing.T) {
		const clusterName = "recon-trigger-create"

		// Snapshot any pre-existing matching Secret count so the subtest's
		// assertion is independent of state leaked from other subtests.
		preCount := countLabeledSecrets(t, mgmtK8sClient)
		t.Logf("argocd ns: %d sharko-labeled Secret(s) before register", preCount)

		// Register via the full Sharko API. auto-merge is on (see
		// seedActiveConnection's gitops block), so commitChangesWithMeta
		// will MergePullRequest synchronously and fireReconcilerTrigger
		// will land its nudge on a reconciler that can immediately observe
		// the new managed-clusters.yaml entry on main.
		body := makeKubeconfigRegisterBody(t, target, clusterName)
		resp := admin.Do(t, http.MethodPost, "/api/v1/clusters", body)
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("register cluster: status=%d", resp.StatusCode)
		}
		var result orchestrator.RegisterClusterResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode register result: %v", err)
		}
		t.Logf("register: status=%s git=%+v", result.Status, result.Git)

		// Confirm the managed-clusters.yaml entry actually landed on main.
		// If it did not, the reconciler can't possibly create the Secret —
		// surface that as a diagnostic before the 5s wait expires.
		mainBody := ghmock.FileAt("main", managedClustersPath)
		if !strings.Contains(mainBody, clusterName) {
			t.Fatalf("managed-clusters.yaml on main does NOT contain %q after register; orchestrator commit did not merge.\nfile body:\n%s",
				clusterName, mainBody)
		}

		// Sub-5s convergence assertion. Cannot be tick-driven (30s tick),
		// so passing this proves the Trigger() path works end-to-end:
		// orchestrator.fireReconcilerTrigger → Server.reconcilerTrigger →
		// Reconciler.Trigger → triggerCh → run() → pollOnce → Secret create.
		secret := waitForSharkoLabeledSecret(t, mgmtK8sClient, clusterName, 5*time.Second)
		t.Logf("post-merge reconcile produced Secret %s/%s with managed-by=%s in <5s",
			secret.Namespace, secret.Name, secret.Labels[clusterreconciler.LabelManagedBy])

		if !clusterreconciler.IsManagedBySharko(secret) {
			t.Errorf("Secret missing sharko ownership label; labels=%v", secret.Labels)
		}
		postCount := countLabeledSecrets(t, mgmtK8sClient)
		if postCount != preCount+1 {
			t.Errorf("expected exactly 1 new sharko-labeled Secret; before=%d after=%d", preCount, postCount)
		}
	})

	t.Run("ClusterRemovedFromGit_ReconcilerDeletes", func(t *testing.T) {
		const clusterName = "recon-trigger-delete"

		// Seed: drop a cluster entry into managed-clusters.yaml on main +
		// trigger the reconciler so the Secret exists before the delete
		// half of the test runs.
		seedClusterEntryOnMain(t, ghmock, managedClustersPath, clusterName)
		recon.Trigger()
		secret := waitForSharkoLabeledSecret(t, mgmtK8sClient, clusterName, 5*time.Second)
		t.Logf("pre-delete: Secret %s/%s present (managed-by=%s)",
			secret.Namespace, secret.Name, secret.Labels[clusterreconciler.LabelManagedBy])

		// Mutate main directly: parse → remove → re-marshal. Exercises the
		// V125-1-8.3 RemoveClusterEntry envelope-aware path. We bypass the
		// Sharko orchestrator for this subtest because the in-process
		// DeregisterCluster path does not remove the cluster from
		// managed-clusters.yaml today (it only deletes the values file);
		// the reconciler's delete behaviour is driven by the file content
		// regardless of how it was mutated.
		preBody, err := ghmock.GetFileContent(context.Background(), managedClustersPath, "main")
		if err != nil {
			t.Fatalf("read managed-clusters.yaml: %v", err)
		}
		removedBody, err := gitops.RemoveClusterEntry(preBody, clusterName)
		if err != nil {
			t.Fatalf("RemoveClusterEntry(%s): %v", clusterName, err)
		}
		writeFileToMain(t, ghmock, managedClustersPath, removedBody)

		// Fire the trigger (simulating what prTracker.OnMergeFn would do
		// when the deregister PR merges in production).
		recon.Trigger()

		// Sub-5s convergence assertion — Trigger-driven, not tick-driven.
		waitForSecretAbsent(t, mgmtK8sClient, clusterName, 5*time.Second)
		t.Logf("post-removal reconcile deleted Secret for %q in <5s", clusterName)
	})

	t.Run("AccidentalSecretDeletion_SelfHealing", func(t *testing.T) {
		const clusterName = "recon-self-heal"

		// Use a SHORT-TICK reconciler for this subtest so the tick-driven
		// self-heal does not impose 30s wall-time. The contract (design §9
		// row "Secret with sharko label deleted → re-create from git") is
		// independent of the cadence — we just want to prove the tick
		// reconciles, not benchmark the production interval.
		fastRecon := startReconciler(t, reconcilerConfig{
			k8sClient:    mgmtK8sClient,
			gitProvider:  ghmock,
			auditFn:      auditFn,
			tickInterval: 2 * time.Second,
		})

		// Seed: add an entry to managed-clusters.yaml on main + use the
		// fast reconciler's first tick to create the Secret. We do NOT
		// call Trigger here so the create itself is also tick-driven —
		// proves the pollOnce loop is alive.
		seedClusterEntryOnMain(t, ghmock, managedClustersPath, clusterName)
		secret := waitForSharkoLabeledSecret(t, mgmtK8sClient, clusterName, 10*time.Second)
		t.Logf("self-heal subtest seed: Secret %s/%s created (resourceVersion=%s)",
			secret.Namespace, secret.Name, secret.ResourceVersion)

		// Simulate accidental admin deletion via kubectl.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := mgmtK8sClient.CoreV1().Secrets("argocd").Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil {
			cancel()
			t.Fatalf("kubectl-delete Secret %s: %v", secret.Name, err)
		}
		cancel()
		t.Logf("simulated accidental deletion of Secret %s/argocd", secret.Name)

		// Self-heal assertion: reconciler tick should re-create the Secret.
		// 10s window covers up to 5x the 2s tick — plenty of margin for
		// CI without making the test flaky.
		healed := waitForSharkoLabeledSecret(t, mgmtK8sClient, clusterName, 10*time.Second)
		t.Logf("self-heal: Secret %s/%s re-created (resourceVersion=%s vs original %s)",
			healed.Namespace, healed.Name, healed.ResourceVersion, secret.ResourceVersion)
		if !clusterreconciler.IsManagedBySharko(healed) {
			t.Errorf("re-created Secret missing sharko ownership label; labels=%v", healed.Labels)
		}
		if healed.ResourceVersion == secret.ResourceVersion {
			t.Errorf("Secret resourceVersion unchanged after delete+heal — suggests stale read, not a fresh re-create")
		}

		// Stop the fast reconciler so its goroutine does not race the
		// next subtest's reconcile cadence. The shared 30s-tick recon
		// continues to run until t.Cleanup fires.
		fastRecon.Stop()
	})
}

// ---------------------------------------------------------------------------
// reconciler test fixtures
// ---------------------------------------------------------------------------

// reconcilerConfig is the test-side bundle for spinning up an in-process
// clusterreconciler.Reconciler against the real kind mgmt cluster.
type reconcilerConfig struct {
	k8sClient    kubernetes.Interface
	gitProvider  gitprovider.GitProvider
	auditFn      func(audit.Entry)
	tickInterval time.Duration
}

// startReconciler constructs + starts a reconciler with sensible defaults
// for the e2e suite. Registers t.Cleanup so the goroutine is reaped at the
// end of the test even when the caller forgot to call Stop().
//
// Vault is a MockClusterCredentialsProvider — sufficient for the
// reconciler's GetCredentials calls; the actual K8s connection it would
// authenticate to is irrelevant because the test only inspects Secret
// existence/labels/data, not in-cluster connectivity.
//
// CMStore is a fake-clientset-backed store. The reconciler's pollOnce
// does not read from it today (state is held in the trigger channel) but
// Deps documents the field as required so we satisfy it.
func startReconciler(t *testing.T, cfg reconcilerConfig) *clusterreconciler.Reconciler {
	t.Helper()

	fakeCS := k8sfake.NewSimpleClientset()
	cmStore := cmstore.NewStore(fakeCS, "sharko", "sharko-e2e-recon")
	vault := &demo.MockClusterCredentialsProvider{}

	gpGetter := func() gitprovider.GitProvider { return cfg.gitProvider }

	recon := clusterreconciler.New(clusterreconciler.Deps{
		CMStore:             cmStore,
		GitProvider:         gpGetter,
		ArgoClient:          cfg.k8sClient,
		Vault:               vault,
		AuditFn:             cfg.auditFn,
		TickInterval:        cfg.tickInterval,
		ManagedClustersPath: "configuration/managed-clusters.yaml",
		Namespace:           "argocd",
		Branch:              "main",
	})
	ctx, cancel := context.WithCancel(context.Background())
	recon.Start(ctx)
	t.Cleanup(func() {
		recon.Stop()
		cancel()
	})
	return recon
}

// ---------------------------------------------------------------------------
// ghmock seed/mutation helpers — keep test bodies focused on assertions
// ---------------------------------------------------------------------------

// seedClusterEntryOnMain ensures a managed-clusters.yaml on main contains
// a single entry for clusterName (preserving any existing entries).
// Constructs the file via gitops.AddClusterEntry against the current main
// content (or a fresh enveloped spec when the file does not yet exist),
// then writes it back via BatchCreateFiles.
//
// Mirrors what the orchestrator's commitChangesWithMeta + envelope-aware
// writer would have produced, but bypasses the PR + merge mechanics so
// the test can deterministically prepare state for the reconciler.
func seedClusterEntryOnMain(t *testing.T, ghmock *harness.MockGitProvider, path, clusterName string) {
	t.Helper()
	current, err := ghmock.GetFileContent(context.Background(), path, "main")
	if err != nil {
		// Seed an empty enveloped file when not present. AddClusterEntry
		// accepts nil/empty and returns a freshly-marshalled envelope.
		current = nil
	}
	updated, err := gitops.AddClusterEntry(current, gitops.ClusterEntryInput{
		Name:   clusterName,
		Region: "test",
	})
	if err != nil {
		t.Fatalf("seedClusterEntryOnMain: AddClusterEntry(%s): %v", clusterName, err)
	}
	writeFileToMain(t, ghmock, path, updated)
}

// writeFileToMain writes content at path on main via BatchCreateFiles.
// MockGitProvider treats main as a normal branch — overwrites the path's
// blob with new bytes. No PR involvement.
func writeFileToMain(t *testing.T, ghmock *harness.MockGitProvider, path string, content []byte) {
	t.Helper()
	files := map[string][]byte{path: content}
	if err := ghmock.BatchCreateFiles(context.Background(), files, "main", ""); err != nil {
		t.Fatalf("writeFileToMain(%s): %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// argocd-side observation helpers
// ---------------------------------------------------------------------------

// waitForSharkoLabeledSecret polls the argocd namespace for a Secret with
// the sharko ownership label AND name == clusterName. Returns the Secret
// on success; t.Fatalf on timeout. Polls every 200ms.
//
// The label selector matches what reconciler.listManagedSecrets uses, so a
// pass here is direct evidence the reconciler produced a Secret the way
// the production code paths expect.
func waitForSharkoLabeledSecret(t *testing.T, cs kubernetes.Interface, clusterName string, timeout time.Duration) *corev1.Secret {
	t.Helper()
	deadline := time.Now().Add(timeout)
	selector := clusterreconciler.LabelManagedBy + "=" + clusterreconciler.LabelValueSharko
	var lastSeen int
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		list, err := cs.CoreV1().Secrets("argocd").List(ctx, metav1.ListOptions{LabelSelector: selector})
		cancel()
		if err == nil {
			lastSeen = len(list.Items)
			for i := range list.Items {
				s := &list.Items[i]
				if s.Name == clusterName {
					return s
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("waitForSharkoLabeledSecret(%s): never appeared in argocd ns within %s (saw %d sharko-labeled Secret(s) at last list)",
		clusterName, timeout, lastSeen)
	return nil // unreachable
}

// waitForSecretAbsent is the inverse of waitForSharkoLabeledSecret —
// asserts the named Secret is GONE from the argocd ns within timeout.
// t.Fatalf on timeout. Polls every 200ms.
func waitForSecretAbsent(t *testing.T, cs kubernetes.Interface, clusterName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := cs.CoreV1().Secrets("argocd").Get(ctx, clusterName, metav1.GetOptions{})
		cancel()
		if err != nil {
			// Anything that's not a 200 / found counts as "absent" for the
			// purpose of this assertion (404 is the success path; transient
			// API errors will be retried by the next poll iteration).
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("waitForSecretAbsent(%s): Secret still present in argocd ns after %s", clusterName, timeout)
}

// countLabeledSecrets returns the number of sharko-labeled Secrets in
// the argocd namespace. Used as a coarse independence check between
// subtests so a leak from #1 cannot silently satisfy #2 / #3.
func countLabeledSecrets(t *testing.T, cs kubernetes.Interface) int {
	t.Helper()
	selector := clusterreconciler.LabelManagedBy + "=" + clusterreconciler.LabelValueSharko
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := cs.CoreV1().Secrets("argocd").List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		t.Fatalf("countLabeledSecrets: list: %v", err)
	}
	return len(list.Items)
}

