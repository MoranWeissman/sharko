package clusterreconciler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// These tests run the real Gitea provider (against a small fake Gitea
// server) into the real reconciler, to prove one thing: when Sharko cannot
// read managed-clusters.yaml from Gitea, no connection Secret is deleted.
// Cases: the file is not there (404), the repo has no commits (200 with
// `[]`), a request that fails (500), a request that fails to connect, and
// the extra "is this a real file" check failing.

type giteaFake struct {
	rawStatus  int    // status for GET .../raw/<path>
	rawBody    string // body for the raw answer
	repoStatus int    // status for GET /repos/o/r
	repoBody   string
}

func (g giteaFake) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/api/v1/version":
			_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
		case p == "/api/v1/repos/o/r":
			w.WriteHeader(g.repoStatus)
			_, _ = w.Write([]byte(g.repoBody))
		case strings.HasPrefix(p, "/api/v1/repos/o/r/raw/"):
			w.WriteHeader(g.rawStatus)
			_, _ = w.Write([]byte(g.rawBody))
		default:
			// ListDirectory, contents, branches: not found.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	})
}

func runGiteaTick(t *testing.T, url string) (*fake.Clientset, *auditCollector, *Reconciler) {
	t.Helper()
	gp, err := gitprovider.NewGiteaProvider(url, "o", "r", "tok")
	if err != nil {
		t.Fatalf("NewGiteaProvider: %v", err)
	}
	k8sClient := fake.NewSimpleClientset(keeperSecret())
	audits := &auditCollector{}
	r := newReconcilerForTest(t, gp, k8sClient, &fakeVault{}, audits, nil)
	r.pollOnce(context.Background())
	return k8sClient, audits, r
}

func assertNoSecretDeleted(t *testing.T, k8sClient *fake.Clientset) {
	t.Helper()
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "keeper", metav1.GetOptions{}); err != nil {
		t.Fatalf("the connection Secret was deleted: %v", err)
	}
	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatalf("unexpected delete action: %s", actionTarget(a))
		}
	}
}

// The file is not in the repo (Gitea answers 404 for both paths): the sweep
// is held and says so.
func TestGiteaFileMissing_SweepHeld(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(giteaFake{rawStatus: 404, rawBody: `{"message":"Not Found"}`, repoStatus: 200, repoBody: `{"name":"r"}`}.handler())
	defer srv.Close()
	k8s, audits, _ := runGiteaTick(t, srv.URL)
	assertNoSecretDeleted(t, k8s)
	if !hasEvent(audits.Snapshot(), "orphan_sweep_held", "partial") {
		t.Fatalf("expected an orphan_sweep_held audit entry; got %v", audits.Snapshot())
	}
}

// An empty repo: Gitea answers 200 with `[]` and the repo says it is empty.
func TestGiteaEmptyRepo_SweepHeld(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(giteaFake{rawStatus: 200, rawBody: `[]`, repoStatus: 200, repoBody: `{"name":"r","empty":true}`}.handler())
	defer srv.Close()
	k8s, audits, _ := runGiteaTick(t, srv.URL)
	assertNoSecretDeleted(t, k8s)
	if !hasEvent(audits.Snapshot(), "orphan_sweep_held", "partial") {
		t.Fatalf("expected an orphan_sweep_held audit entry; got %v", audits.Snapshot())
	}
}

// A request that fails (500): the tick stops before it touches anything.
func TestGiteaRequestFails_NothingDeleted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(giteaFake{rawStatus: 500, rawBody: `{"message":"boom"}`, repoStatus: 200, repoBody: `{"name":"r"}`}.handler())
	defer srv.Close()
	k8s, audits, _ := runGiteaTick(t, srv.URL)
	assertNoSecretDeleted(t, k8s)
	if !hasEvent(audits.Snapshot(), "cluster_secret_reconcile", "failure") {
		t.Fatalf("expected a failure audit entry for the failed read; got %v", audits.Snapshot())
	}
}

// The `[]` answer needs a second look at the repo, and that look fails: the
// read is an error, never "file not there", and nothing is deleted.
func TestGiteaEmptyListCheckFails_NothingDeleted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(giteaFake{rawStatus: 200, rawBody: `[]`, repoStatus: 500, repoBody: `{"message":"boom"}`}.handler())
	defer srv.Close()
	k8s, audits, _ := runGiteaTick(t, srv.URL)
	assertNoSecretDeleted(t, k8s)
	if !hasEvent(audits.Snapshot(), "cluster_secret_reconcile", "failure") {
		t.Fatalf("expected a failure audit entry; got %v", audits.Snapshot())
	}
}

// The server cannot be reached at all.
func TestGiteaUnreachable_NothingDeleted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(giteaFake{rawStatus: 200, rawBody: `x`, repoStatus: 200, repoBody: `{}`}.handler())
	gp, err := gitprovider.NewGiteaProvider(srv.URL, "o", "r", "tok")
	if err != nil {
		t.Fatal(err)
	}
	srv.Close() // the server is gone after the client is built
	k8sClient := fake.NewSimpleClientset(keeperSecret())
	audits := &auditCollector{}
	r := newReconcilerForTest(t, gp, k8sClient, &fakeVault{}, audits, nil)
	r.pollOnce(context.Background())
	assertNoSecretDeleted(t, k8sClient)
	if !hasEvent(audits.Snapshot(), "cluster_secret_reconcile", "failure") {
		t.Fatalf("expected a failure audit entry; got %v", audits.Snapshot())
	}
}
