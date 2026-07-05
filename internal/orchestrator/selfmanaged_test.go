package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// V2-cleanup-57.2 — self-managed connections (connection_managed_by: user).
//
// These tests pin the orchestrator half of the contract:
//
//  1. A self-managed registration NEVER writes the ArgoCD cluster Secret —
//     the direct-write branch (V2-cleanup-8.2) is skipped even for an
//     inline kubeconfig that carries a bearer token.
//  2. The managed-clusters.yaml record carries connectionManagedBy: user;
//     Sharko-managed registrations keep OMITTING the field (absent ==
//     sharko; pre-57.2 files stay byte-identical).
//  3. Credentials are OPTIONAL for a self-managed registration: no inline
//     kubeconfig and no secrets backend → registration still succeeds,
//     recording the skip; a supplied kubeconfig still parses (and a broken
//     one still errors — a pasted typo must not be swallowed).
//  4. Unknown connection_managed_by values are a typed caller error (400
//     at the API edge), never a silent fallback to Sharko-managed.
//  5. Adopt records connectionManagedBy: user — adopted clusters default
//     to self-managed.
//  6. RemoveCluster cleanup=all leaves a self-managed cluster's ArgoCD
//     Secret in place (never deletes the user's connection).

func TestRegisterCluster_SelfManaged_NeverWritesArgoSecret(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	mgr := newMockArgoSecretManager()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:                "byo-conn",
		Provider:            "kubeconfig",
		Kubeconfig:          v125TestBearerKubeconfig, // bearer token present — would trigger the direct write in sharko mode
		Addons:              map[string]bool{"monitoring": true},
		ConnectionManagedBy: models.ConnectionManagedByUser,
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success status, got %q (%s)", result.Status, result.Error)
	}
	if len(mgr.ensured) != 0 {
		t.Fatalf("self-managed registration must NEVER write the ArgoCD cluster Secret; Ensure was called with %+v", mgr.ensured)
	}
	// Git record carries the mode.
	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "connectionManagedBy: user") {
		t.Fatalf("managed-clusters.yaml must record connectionManagedBy: user, got:\n%s", mc)
	}
	// Direct-write step must not be in the completed steps.
	for _, s := range result.CompletedSteps {
		if s == "write_argocd_secret" {
			t.Fatalf("write_argocd_secret step must not run for self-managed: %v", result.CompletedSteps)
		}
	}
}

func TestRegisterCluster_SharkoManaged_StillWritesSecret_AndOmitsField(t *testing.T) {
	// The other side of the partition: default (sharko) mode is
	// byte-for-byte the pre-57.2 behavior — direct write happens for a
	// bearer-token kubeconfig, and the git record does NOT carry the field.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	mgr := newMockArgoSecretManager()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "sharko-owned",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"monitoring": true},
		// ConnectionManagedBy deliberately absent.
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(mgr.ensured) != 1 {
		t.Fatalf("sharko-managed kubeconfig registration must direct-write the Secret exactly once, got %d", len(mgr.ensured))
	}
	mc := string(git.files["configuration/managed-clusters.yaml"])
	if strings.Contains(mc, "connectionManagedBy") {
		t.Fatalf("sharko-managed entries must OMIT connectionManagedBy (absent == sharko), got:\n%s", mc)
	}
}

func TestRegisterCluster_SelfManaged_NoCredentialsAtAll_Succeeds(t *testing.T) {
	// No inline kubeconfig, no secrets backend (credProvider nil): a
	// self-managed registration proceeds straight to the Git record and
	// records the credentials skip. Stage-1 verification is skipped —
	// there is nothing to verify with.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:                "byo-nocreds",
		Provider:            "kubeconfig", // inline source, but nothing pasted
		Addons:              map[string]bool{"monitoring": true},
		ConnectionManagedBy: models.ConnectionManagedByUser,
	})
	if err != nil {
		t.Fatalf("expected success without credentials, got %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q (%s)", result.Status, result.Error)
	}
	if result.Verification != nil {
		t.Fatal("verification must be skipped when no credentials are available")
	}
	foundSkip := false
	for _, s := range result.CompletedSteps {
		if s == "skip_credentials_self_managed" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Fatalf("expected skip_credentials_self_managed step, got %v", result.CompletedSteps)
	}
	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "connectionManagedBy: user") {
		t.Fatalf("managed-clusters.yaml must record the mode, got:\n%s", mc)
	}
}

func TestRegisterCluster_SelfManaged_BackendSource_LookupFailure_Succeeds(t *testing.T) {
	// Backend source configured but the lookup fails (nothing stored for
	// this cluster — the normal self-managed case). Registration must
	// continue without verification instead of failing.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{} // empty: every lookup errors
	orch := New(nil, creds, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:                "byo-backend",
		Addons:              map[string]bool{"monitoring": true},
		ConnectionManagedBy: models.ConnectionManagedByUser,
	})
	if err != nil {
		t.Fatalf("expected success despite lookup failure, got %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q (%s)", result.Status, result.Error)
	}
}

func TestRegisterCluster_SelfManaged_BrokenInlineKubeconfig_StillErrors(t *testing.T) {
	// A pasted-but-broken kubeconfig is a caller error in BOTH modes — the
	// self-managed relaxation only covers the ABSENT case.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:                "byo-broken",
		Provider:            "kubeconfig",
		Kubeconfig:          "not: a: kubeconfig: at: all",
		ConnectionManagedBy: models.ConnectionManagedByUser,
	})
	if err == nil {
		t.Fatal("a broken pasted kubeconfig must still be rejected")
	}
}

func TestRegisterCluster_UnknownConnectionMode_TypedError(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:                "typo",
		Provider:            "kubeconfig",
		Kubeconfig:          v125TestBearerKubeconfig,
		ConnectionManagedBy: "owner", // not a mode
	})
	if err == nil {
		t.Fatal("unknown connection_managed_by must be rejected")
	}
	if !IsInvalidConnectionMode(err) {
		t.Fatalf("want InvalidConnectionModeError, got %T: %v", err, err)
	}
}

func TestAdoptClusters_RecordsSelfManagedConnection(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "pre-existing", Server: "https://pre-existing.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	res, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"pre-existing"},
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].Status != "success" {
		t.Fatalf("unexpected adopt result: %+v", res.Results)
	}
	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "connectionManagedBy: user") {
		t.Fatalf("adopted clusters must default to a self-managed connection, got:\n%s", mc)
	}
}

func TestRemoveCluster_SelfManaged_LeavesArgoSecretInPlace(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "byo-conn", Server: "https://byo.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: byo-conn\n    connectionManagedBy: user\n    labels:\n      monitoring: \"enabled\"\n")
	git.files["configuration/addons-clusters-values/byo-conn.yaml"] = []byte("clusterGlobalValues: {}\n")
	mgr := newMockArgoSecretManager()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "byo-conn",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(argocd.deletedServers) != 0 {
		t.Fatalf("cleanup=all on a self-managed cluster must NOT delete the ArgoCD cluster Secret; deleted %v", argocd.deletedServers)
	}
	foundSkip := false
	for _, s := range result.CompletedSteps {
		if s == "skip_argocd_secret_user_managed" {
			foundSkip = true
		}
		if s == "delete_argocd_cluster" {
			t.Fatalf("delete_argocd_cluster step must not run: %v", result.CompletedSteps)
		}
	}
	if !foundSkip {
		t.Fatalf("expected skip_argocd_secret_user_managed step, got %v", result.CompletedSteps)
	}
	if !strings.Contains(result.Message, "managed by you") {
		t.Fatalf("removal message must name the left-in-place Secret, got %q", result.Message)
	}
}

func TestRemoveCluster_SharkoManaged_StillDeletesArgoSecret(t *testing.T) {
	// Pin the other side: default-mode clusters keep the pre-57.2 cleanup.
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "sharko-owned", Server: "https://sharko-owned.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: sharko-owned\n    labels:\n      monitoring: \"enabled\"\n")
	git.files["configuration/addons-clusters-values/sharko-owned.yaml"] = []byte("clusterGlobalValues: {}\n")
	mgr := newMockArgoSecretManager()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "sharko-owned",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(argocd.deletedServers) != 1 {
		t.Fatalf("sharko-managed cleanup=all must still delete the ArgoCD cluster; deleted %v", argocd.deletedServers)
	}
}
