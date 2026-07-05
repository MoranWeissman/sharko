package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// mockArgoSecretManagerWithStrip extends the shared mockArgoSecretManager
// (adopt_test.go) with the OPTIONAL ownershipLabelStripper capability that
// RemoveCluster type-asserts for the handover-at-removal-time strip
// (V2-cleanup-60.1). Kept as a separate wrapper so tests that must NOT see
// the capability keep using the plain mock.
type mockArgoSecretManagerWithStrip struct {
	*mockArgoSecretManager
	stripped []string
	stripErr error
}

func newMockArgoSecretManagerWithStrip() *mockArgoSecretManagerWithStrip {
	return &mockArgoSecretManagerWithStrip{mockArgoSecretManager: newMockArgoSecretManager()}
}

func (m *mockArgoSecretManagerWithStrip) StripOwnershipLabel(_ context.Context, name string) (bool, error) {
	if m.stripErr != nil {
		return false, m.stripErr
	}
	if m.managedByLabel[name] != "sharko" {
		return false, nil
	}
	delete(m.managedByLabel, name)
	m.stripped = append(m.stripped, name)
	return true, nil
}

func TestRemoveCluster_AllCleanup_Success(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels:\n      cert-manager: true\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# values")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Git == nil {
		t.Error("expected git result")
	}
	// Should have created a PR.
	if len(git.prs) == 0 {
		t.Error("expected a PR to be created")
	}
	// Values file should be deleted.
	found := false
	for _, f := range git.deletedFiles {
		if strings.Contains(f, "prod-eu.yaml") {
			found = true
		}
	}
	if !found {
		t.Error("expected values file to be deleted")
	}
}

func TestRemoveCluster_GitCleanup_SkipsSecrets(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "git",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	// Should NOT have attempted ArgoCD deletion.
	if len(argocd.deletedServers) > 0 {
		t.Error("expected no ArgoCD cluster deletion with cleanup=git")
	}
}

func TestRemoveCluster_NoneCleanup_OnlyRemovesEntry(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "none",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	// Values file should NOT be deleted with cleanup=none.
	if len(git.deletedFiles) > 0 {
		t.Error("expected no file deletions with cleanup=none")
	}
}

func TestRemoveCluster_RequiresConfirmation(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     false,
	})
	if err == nil {
		t.Fatal("expected error for missing confirmation")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemoveCluster_DryRun(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected dry_run result")
	}
	if result.DryRun.PRTitle == "" {
		t.Error("expected PR title in dry run")
	}
	// No PRs should have been created.
	if len(git.prs) > 0 {
		t.Error("expected no PRs in dry-run mode")
	}
}

func TestRemoveCluster_EmptyName(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Yes: true})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

// ---------- ownership safety on removal (V2-cleanup-60.1, H1) ----------

// Retry of a removal whose PR already merged: the managed-clusters.yaml
// entry is gone, so selfManagedConnection silently defaults to false. The
// ownership gate must still refuse to delete a Secret that does not carry
// Sharko's managed-by label — that Secret is the user's.
func TestRemoveCluster_RetryWithNoEntry_RefusesDeleteOfUnlabeledSecret(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "byo-conn", Server: "https://byo.example.com"},
	}
	git := newMockGitProvider()
	// The entry was already removed by the first attempt's merged PR.
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: some-other-cluster\n    labels: {}\n")
	mgr := newMockArgoSecretManager() // managedByLabel["byo-conn"] is "" — no sharko label
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
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
		t.Fatalf("must never delete an ArgoCD cluster Secret without Sharko's ownership label; deleted %v", argocd.deletedServers)
	}
	foundSkip := false
	for _, s := range result.CompletedSteps {
		if s == "skip_argocd_secret_not_sharko_labeled" {
			foundSkip = true
		}
		if s == "delete_argocd_cluster" {
			t.Fatalf("delete_argocd_cluster step must not run: %v", result.CompletedSteps)
		}
	}
	if !foundSkip {
		t.Fatalf("expected skip_argocd_secret_not_sharko_labeled step, got %v", result.CompletedSteps)
	}
	if !strings.Contains(result.Message, "ownership label") {
		t.Fatalf("message must explain the refused delete in plain English, got %q", result.Message)
	}
}

// Same retry shape, but the Secret DOES carry Sharko's label — the retry
// must still complete the cleanup and delete the ArgoCD cluster Secret.
func TestRemoveCluster_RetryWithNoEntry_LabeledSecretStillDeleted(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters: []\n")
	mgr := newMockArgoSecretManager()
	mgr.managedByLabel["prod-eu"] = "sharko"
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(argocd.deletedServers) != 1 {
		t.Fatalf("sharko-labeled Secret must still be cleaned up on retry; deleted %v", argocd.deletedServers)
	}
	foundDelete := false
	for _, s := range result.CompletedSteps {
		if s == "delete_argocd_cluster" {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Fatalf("expected delete_argocd_cluster step, got %v", result.CompletedSteps)
	}
}

// Any doubt means refuse: a label read error must block the delete too.
func TestRemoveCluster_OwnershipLabelReadError_RefusesDelete(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	mgr := newMockArgoSecretManager()
	mgr.labelErr = fmt.Errorf("secrets \"prod-eu\" is forbidden")
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(argocd.deletedServers) != 0 {
		t.Fatalf("a label read error must refuse the delete (fail-safe); deleted %v", argocd.deletedServers)
	}
	foundSkip := false
	for _, s := range result.CompletedSteps {
		if s == "skip_argocd_secret_not_sharko_labeled" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Fatalf("expected skip_argocd_secret_not_sharko_labeled step, got %v", result.CompletedSteps)
	}
}

// Self-managed removal must hand over eagerly: strip Sharko's leftover
// ownership label from the user's Secret at removal time, so the orphan
// sweep can never delete it once the git entry is gone.
func TestRemoveCluster_SelfManaged_StripsOwnershipLabelAtRemoval(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "byo-conn", Server: "https://byo.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: byo-conn\n    connectionManagedBy: user\n    labels:\n      monitoring: \"enabled\"\n")
	git.files["configuration/addons-clusters-values/byo-conn.yaml"] = []byte("clusterGlobalValues: {}\n")
	mgr := newMockArgoSecretManagerWithStrip()
	// Leftover from the cluster's earlier Sharko-managed life.
	mgr.managedByLabel["byo-conn"] = "sharko"
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "byo-conn",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(mgr.stripped) != 1 || mgr.stripped[0] != "byo-conn" {
		t.Fatalf("expected ownership label stripped from byo-conn, got %v", mgr.stripped)
	}
	if len(argocd.deletedServers) != 0 {
		t.Fatalf("self-managed Secret must never be deleted; deleted %v", argocd.deletedServers)
	}
	foundStrip, foundSkip := false, false
	for _, s := range result.CompletedSteps {
		if s == "strip_sharko_ownership_label" {
			foundStrip = true
		}
		if s == "skip_argocd_secret_user_managed" {
			foundSkip = true
		}
	}
	if !foundStrip {
		t.Fatalf("expected strip_sharko_ownership_label step, got %v", result.CompletedSteps)
	}
	if !foundSkip {
		t.Fatalf("expected skip_argocd_secret_user_managed step to remain, got %v", result.CompletedSteps)
	}
}

// The strip is best-effort: a strip failure must be loud in logs but never
// block the removal itself.
func TestRemoveCluster_SelfManaged_StripFailureDoesNotBlockRemoval(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "byo-conn", Server: "https://byo.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: byo-conn\n    connectionManagedBy: user\n    labels: {}\n")
	mgr := newMockArgoSecretManagerWithStrip()
	mgr.stripErr = fmt.Errorf("update forbidden")
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "byo-conn",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("strip failure must not block removal, got status %s (error: %s)", result.Status, result.Error)
	}
	if len(argocd.deletedServers) != 0 {
		t.Fatalf("self-managed Secret must never be deleted; deleted %v", argocd.deletedServers)
	}
}

func TestRemoveCluster_InvalidCleanup(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "invalid",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected error for invalid cleanup scope")
	}
	if !strings.Contains(err.Error(), "invalid cleanup scope") {
		t.Errorf("unexpected error: %v", err)
	}
}
