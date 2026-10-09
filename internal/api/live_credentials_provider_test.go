package api

// live_credentials_provider_test.go — R2-1 criterion 1: swap the
// cluster-credentials backend while the server is running, and the CHECK, the
// REPAIR-credential-spec route, and the BACKGROUND WRITE all hit the new
// backend — no restart, no reconstruction of anything.
//
// The bug this pins against coming back: serve.go handed the reconciler the
// provider VALUE it had at boot, so the check (which reads the api.Server's
// live snapshot) and both writers (which read the boot value through
// ConnectionCredentialSpecForWrite) could be on two different provider
// generations at once — the repair handler literally compared with one
// generation and wrote with another in the same request.
//
// The swap here goes through installCredProvider, which stores a new
// providerSet through Server.providerState — the SAME atomic publication
// point ReinitializeFromConnection drives (via publishProviders) when the
// connections API changes the backend. The HTTP door → publish link is the
// connections handlers' own tested behavior; what this test adds is the part
// that was broken: every read downstream of the publish resolves at USE time.
//
// The backends are counting fakes with distinctive server addresses, so the
// test watches which backend each path ACTUALLY hits — both by counter and
// by the material that ends up in the written Secret — not wiring shapes.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// countingBackend is a cluster-credentials backend that counts every read and
// answers with its own distinctive server address, so a test can tell WHICH
// backend a code path hit — by counter and by the material it handed out.
type countingBackend struct {
	mu     sync.Mutex
	server string
	token  string
	gets   int // GetCredentials — the write path's read
	stored int // StoredConnectionFacts — the check path's read
}

func (b *countingBackend) GetCredentials(_ string) (*providers.Kubeconfig, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gets++
	return &providers.Kubeconfig{Server: b.server, CAData: []byte("not-a-real-ca"), Token: b.token}, nil
}

func (b *countingBackend) StoredConnectionFacts(_ string) (*providers.StoredConnectionFacts, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stored++
	return &providers.StoredConnectionFacts{Server: b.server, CAData: []byte("not-a-real-ca"), Token: b.token}, nil
}

func (b *countingBackend) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (b *countingBackend) SearchSecrets(_ string) ([]string, error)       { return nil, nil }
func (b *countingBackend) HealthCheck(_ context.Context) error            { return nil }

func (b *countingBackend) counts() (gets, stored int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gets, b.stored
}

// waitForSecretServer waits for the cluster Secret to exist with the given
// server address — the background write is asynchronous behind Trigger().
func waitForSecretServer(t *testing.T, client *fake.Clientset, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		secret, err := client.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
		if err == nil {
			last = secret.StringData["server"]
			if last == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background write never produced a Secret with server %q (last seen: %q)", want, last)
}

func TestLiveProvider_CheckRepairAndBackgroundWriteAllFollowTheSwap(t *testing.T) {
	backendA := &countingBackend{server: "https://backend-a.invalid", token: "made-up-token-a"}
	backendB := &countingBackend{server: "https://backend-b.invalid", token: "made-up-token-b"}

	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{
		managedYAML: []byte(backendManagedYAML),
		headSHA:     "1111111111111111111111111111111111111111",
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	// A live connection Secret exists at the start so the check has a real
	// live side to compare.
	argoClient := fake.NewSimpleClientset(liveConnectionSecret(
		map[string]string{"datadog": "enabled"},
		map[string]string{"server": "https://backend-a.invalid"},
		nil,
	))

	recon := clusterreconciler.New(clusterreconciler.Deps{
		CMStore:     cmstore.NewStore(fake.NewSimpleClientset(), "sharko", "live-provider-test"),
		GitProvider: func() gitprovider.GitProvider { return gp },
		ArgoClient:  argoClient,
		// THE PRODUCTION WIRING SHAPE (cmd/sharko/serve.go): the write path
		// resolves the Server's currently-published provider on every use —
		// the same snapshot generation the check path reads. Handing a
		// provider VALUE here instead is the R2-1 bug.
		Vault:        srv.ClusterCredentialsProvider,
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour, // only Trigger() drives passes in this test
	})
	srv.SetClusterReconciler(recon)

	// Boot state: backend A is the published provider.
	installCredProvider(srv, backendA, nil, nil)

	runCheck := func() {
		t.Helper()
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, comparisonReq(comparisonCluster))
		if rr.Code != http.StatusOK {
			t.Fatalf("connection comparison returned %d: %s", rr.Code, rr.Body.String())
		}
	}

	// --- Backend A is configured: every path hits A -------------------------

	runCheck()
	if _, stored := backendA.counts(); stored == 0 {
		t.Fatal("the check never read backend A although A is the published provider")
	}

	specA, err := recon.ConnectionCredentialSpecForWrite(models.ManagedClusterEntry{Name: comparisonCluster})
	if err != nil {
		t.Fatalf("repair-spec route failed on backend A: %v", err)
	}
	if specA.Server != "https://backend-a.invalid" {
		t.Fatalf("repair-spec server = %q, want backend A's", specA.Server)
	}

	// The background write: the Secret goes missing, the reconciler restores
	// it — from A's material.
	if err := argoClient.CoreV1().Secrets("argocd").Delete(context.Background(), comparisonCluster, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete live secret: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	recon.Start(ctx) // first pass runs immediately
	t.Cleanup(recon.Stop)
	waitForSecretServer(t, argoClient, "https://backend-a.invalid")

	// --- The operator swaps to backend B through the publish mechanism ------

	installCredProvider(srv, backendB, nil, nil)
	aGetsAtSwap, aStoredAtSwap := backendA.counts()

	// The check hits B now — and A not once more.
	runCheck()
	if _, stored := backendB.counts(); stored == 0 {
		t.Fatal("after the swap the check never read backend B — it is still on an old provider generation")
	}

	// The repair-spec route hits B now.
	specB, err := recon.ConnectionCredentialSpecForWrite(models.ManagedClusterEntry{Name: comparisonCluster})
	if err != nil {
		t.Fatalf("repair-spec route failed on backend B: %v", err)
	}
	if specB.Server != "https://backend-b.invalid" {
		t.Fatalf("after the swap the repair-spec server = %q, want backend B's — the write half is still on the boot provider", specB.Server)
	}

	// The background write hits B now: the Secret goes missing again, and the
	// restored Secret must be built from B's material.
	if err := argoClient.CoreV1().Secrets("argocd").Delete(context.Background(), comparisonCluster, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete live secret: %v", err)
	}
	recon.Trigger()
	waitForSecretServer(t, argoClient, "https://backend-b.invalid")

	// And backend A was never read again after the swap — by ANY path.
	aGets, aStored := backendA.counts()
	if aGets != aGetsAtSwap || aStored != aStoredAtSwap {
		t.Fatalf("backend A was still read after the swap (gets %d→%d, stored %d→%d) — some path holds a stale provider",
			aGetsAtSwap, aGets, aStoredAtSwap, aStored)
	}
}
