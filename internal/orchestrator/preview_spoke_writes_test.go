package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// A preview (dry run) of register or adopt must make NO write on the user's
// cluster. Before this, both ran the full connection test (create the
// sharko-test namespace, write a test Secret) before they reached their
// dry-run exit, so pressing "Preview" changed the cluster. These tests read
// the fake clientset's action log and count the writes.

// spokeWrites is every write the fake cluster saw, as "<verb> <resource>".
func spokeWrites(client *fake.Clientset) []string {
	var out []string
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "get", "list", "watch":
			continue
		}
		out = append(out, fmt.Sprintf("%s %s", a.GetVerb(), a.GetResource().Resource))
	}
	return out
}

func assertSpokeWrites(t *testing.T, client *fake.Clientset, want []string) {
	t.Helper()
	got := spokeWrites(client)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("writes on the spoke cluster:\n  got  %q\n  want %q", got, want)
	}
}

func assertPreviewNote(t *testing.T, v *verify.Result) {
	t.Helper()
	if v == nil {
		t.Fatal("the preview carries no connection check result")
	}
	if len(v.Notes) != 1 || v.Notes[0] != "Write access was not checked because this is a preview." {
		t.Fatalf("preview Notes = %q, want the plain 'not checked' sentence", v.Notes)
	}
}

// The full check with cleanup, when the test namespace is not there yet.
var fullCheckWritesNewNamespace = []string{
	"create namespaces",
	"create secrets",
	"delete secrets",
	"delete namespaces",
}

func TestRegisterCluster_DryRun_MakesNoSpokeWrites(t *testing.T) {
	spoke := fake.NewSimpleClientset()
	orch := New(nil, defaultCredsWithRaw(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(nil, nil, fakeClientFactoryFor(spoke))

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected a preview")
	}
	assertSpokeWrites(t, spoke, nil)
	if result.Verification == nil || !result.Verification.Success {
		t.Fatalf("the preview should still say the cluster answered, got %+v", result.Verification)
	}
	assertPreviewNote(t, result.DryRun.Verification)
}

func TestRegisterCluster_Real_RunsFullCheckAndCleansUp(t *testing.T) {
	spoke := fake.NewSimpleClientset()
	orch := New(nil, defaultCredsWithRaw(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(nil, nil, fakeClientFactoryFor(spoke))

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if result.Verification == nil || !result.Verification.Success {
		t.Fatalf("expected a passing full check, got %+v", result.Verification)
	}
	if len(result.Verification.Notes) != 0 {
		t.Errorf("a real register DID check write access; Notes = %q", result.Verification.Notes)
	}
	assertSpokeWrites(t, spoke, fullCheckWritesNewNamespace)
}

func TestRegisterCluster_Real_KeepsANamespaceThatWasAlreadyThere(t *testing.T) {
	spoke := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: verify.TestNamespace()}})
	orch := New(nil, defaultCredsWithRaw(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(nil, nil, fakeClientFactoryFor(spoke))

	if _, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{Name: "prod-eu", Region: "eu-west-1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	assertSpokeWrites(t, spoke, []string{"create secrets", "delete secrets"})
}

func adoptTestOrch(spoke *fake.Clientset) *Orchestrator {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{{Name: "cluster-a", Server: "https://a.example.com"}}
	creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"cluster-a": {Server: "https://a.example.com", CAData: []byte("fake-ca"), Token: "fake-token", Raw: []byte("fake-kubeconfig")},
	}}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")
	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(nil, nil, fakeClientFactoryFor(spoke))
	return orch
}

func TestAdoptClusters_DryRun_MakesNoSpokeWrites(t *testing.T) {
	spoke := fake.NewSimpleClientset()
	orch := adoptTestOrch(spoke)

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("adopt dry run: %v", err)
	}
	cr := result.Results[0]
	if cr.DryRun == nil {
		t.Fatalf("expected a preview, got status %q error %q", cr.Status, cr.Error)
	}
	assertSpokeWrites(t, spoke, nil)
	assertPreviewNote(t, cr.DryRun.Verification)
}

func TestAdoptClusters_Real_RunsFullCheckAndCleansUp(t *testing.T) {
	spoke := fake.NewSimpleClientset()
	orch := adoptTestOrch(spoke)

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{Clusters: []string{"cluster-a"}})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	cr := result.Results[0]
	if cr.Verification == nil || !cr.Verification.Success {
		t.Fatalf("expected a passing full check, got %+v", cr.Verification)
	}
	assertSpokeWrites(t, spoke, fullCheckWritesNewNamespace)
}
