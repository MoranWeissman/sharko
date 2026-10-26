package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ep4: `sharko add-clusters` sends POST /clusters/batch with dry_run on every
// entry to check each cluster before it asks. That check must not make an
// EKS sign-in token and must not write a "cluster registered" line.

// batchEKSBackend holds EKS details (no login). Each GetCredentials is a mint.
type batchEKSBackend struct {
	mu    sync.Mutex
	mints int
}

func (b *batchEKSBackend) GetCredentials(string) (*providers.Kubeconfig, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mints++
	return &providers.Kubeconfig{Server: "https://eks.example.invalid", Token: "k8s-aws-v1.minted"}, nil
}
func (b *batchEKSBackend) StoredConnectionFacts(string) (*providers.StoredConnectionFacts, error) {
	return &providers.StoredConnectionFacts{Server: "https://eks.example.invalid", CredentialMintedPerFetch: true}, nil
}
func (b *batchEKSBackend) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (b *batchEKSBackend) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (b *batchEKSBackend) HealthCheck(context.Context) error              { return nil }
func (b *batchEKSBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mints
}

func postBatch(t *testing.T, srv *Server, clusters []map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"clusters": clusters})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	w := httptest.NewRecorder()
	NewRouter(srv, nil).ServeHTTP(w, req)
	return w
}

func TestBatchDryRun_MakesNoTokenAndLogsNoRegistration(t *testing.T) {
	srv := newKillSwitchTestServer(t)
	backend := &batchEKSBackend{}
	srv.publishProviders(backend, nil, nil)

	w := postBatch(t, srv, []map[string]interface{}{
		{"name": "eks-one", "creds_source": "eks-token", "region": "us-east-1", "dry_run": true},
		{"name": "eks-two", "creds_source": "eks-token", "region": "us-east-1", "dry_run": true},
	})
	if w.Code >= 400 {
		t.Fatalf("batch dry run answered %d: %s", w.Code, w.Body.String())
	}
	if got := backend.count(); got != 0 {
		t.Fatalf("a batch dry run made %d EKS sign-in tokens, want 0", got)
	}
	if e := findEntry(srv, "cluster_registered"); e != nil {
		t.Fatalf("a batch dry run wrote a cluster_registered line: %+v", *e)
	}
	n := 0
	for _, e := range srv.AuditLog().List(0) {
		if e.Event == batchDryRunAuditEvent {
			n++
			if e.Changes != audit.ChangesNone {
				t.Errorf("the dry-run line says changes %q, want none", e.Changes)
			}
		}
	}
	if n != 1 {
		t.Fatalf("got %d %s lines, want 1", n, batchDryRunAuditEvent)
	}
}

func TestBatchDryRun_RealBatchStillLogsRegistration(t *testing.T) {
	srv := newKillSwitchTestServer(t)
	srv.publishProviders(&batchEKSBackend{}, nil, nil)

	postBatch(t, srv, []map[string]interface{}{
		{"name": "eks-one", "creds_source": "eks-token", "region": "us-east-1", "dry_run": true},
		// A real registration, refused by the inline-credentials gate before
		// any Git work, so the test needs no Git server.
		{"name": "pasted-one", "creds_source": "inline-kubeconfig", "kubeconfig": killSwitchInlineKubeconfig},
	})
	if findEntry(srv, "cluster_registered") == nil {
		t.Fatal("a batch with a real registration in it wrote no cluster_registered line")
	}
	if findEntry(srv, batchDryRunAuditEvent) != nil {
		t.Fatal("a batch with a real registration in it was logged as a dry run")
	}
}
