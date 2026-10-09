package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// V2-cleanup-89.5 / V2-cleanup-90.1 — foreign ArgoCD ownership warning at
// adopt/register.
//
// These tests pin the orchestrator half of the contract: the optional
// foreignOwnerDetector capability is type-asserted off o.argoSecretManager
// (mirrors ownershipLabelStripper's pattern in remove.go), a detected
// foreign owner is attached to the result's Warnings without failing the
// operation, a mock that does NOT implement the capability (the existing
// shared mockArgoSecretManager) produces no warnings — proving every
// pre-89.5 test keeps passing unmodified — and (V2-cleanup-90.1) the
// warning text differs by confidence: hard names the app as the manager,
// soft says "may be" and mentions Helm.

// trackingOwnerEntry pairs an owning app name with the confidence
// mockArgoSecretManagerWithTracking.GetTrackingOwner should report for it.
type trackingOwnerEntry struct {
	appName    string
	confidence string // "hard" or "soft" — mirrors foreignOwnerConfidence's literal values
}

// mockArgoSecretManagerWithTracking extends the shared mockArgoSecretManager
// (adopt_test.go) with the OPTIONAL foreignOwnerDetector capability that
// AdoptClusters / RegisterCluster type-assert for the foreign-ownership
// warning. Kept as a separate wrapper so tests that must NOT see the
// capability keep using the plain mock.
type mockArgoSecretManagerWithTracking struct {
	*mockArgoSecretManager
	trackingOwner map[string]trackingOwnerEntry // cluster -> owning app name + confidence
	trackingErr   error
}

func newMockArgoSecretManagerWithTracking() *mockArgoSecretManagerWithTracking {
	return &mockArgoSecretManagerWithTracking{
		mockArgoSecretManager: newMockArgoSecretManager(),
		trackingOwner:         make(map[string]trackingOwnerEntry),
	}
}

// setHardOwner registers a hard-confidence tracking owner for cluster — a
// verified tracking-id match against that exact secret.
func (m *mockArgoSecretManagerWithTracking) setHardOwner(cluster, appName string) {
	m.trackingOwner[cluster] = trackingOwnerEntry{appName: appName, confidence: string(foreignOwnerConfidenceHard)}
}

// setSoftOwner registers a soft-confidence tracking owner for cluster (a
// mismatched tracking-id or a label-only match — could be plain Helm).
func (m *mockArgoSecretManagerWithTracking) setSoftOwner(cluster, appName string) {
	m.trackingOwner[cluster] = trackingOwnerEntry{appName: appName, confidence: string(foreignOwnerConfidenceSoft)}
}

func (m *mockArgoSecretManagerWithTracking) GetTrackingOwner(_ context.Context, name string) (string, string, bool, error) {
	if m.trackingErr != nil {
		return "", "", false, m.trackingErr
	}
	entry, found := m.trackingOwner[name]
	return entry.appName, entry.confidence, found, nil
}

// ---------- AdoptClusters ----------

func TestAdoptClusters_ForeignOwnerWarning_Hard(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	asm := newMockArgoSecretManagerWithTracking()
	asm.setHardOwner("cluster-a", "renderer-app")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}
	cr := result.Results[0]
	if cr.Status != "success" {
		t.Fatalf("foreign ownership must WARN, not fail the adoption; status=%q error=%q", cr.Status, cr.Error)
	}
	if len(cr.Warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(cr.Warnings), cr.Warnings)
	}
	if !strings.Contains(cr.Warnings[0], `"renderer-app"`) {
		t.Errorf("warning = %q, want it to name the owning app", cr.Warnings[0])
	}
	if !strings.Contains(cr.Warnings[0], "Replace") {
		t.Errorf("warning = %q, want it to mention the Replace sync-option risk", cr.Warnings[0])
	}
	if strings.Contains(cr.Warnings[0], "may be") {
		t.Errorf("warning = %q, a hard-confidence warning must assert ownership, not hedge with \"may be\"", cr.Warnings[0])
	}
}

// TestAdoptClusters_ForeignOwnerWarning_Soft pins the V2-cleanup-90.1 soft
// text: hedges with "may be" and names the Helm possibility, since a
// soft-confidence signal (mismatched tracking-id or label-only match) is
// also what a plain Helm release stamps.
func TestAdoptClusters_ForeignOwnerWarning_Soft(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	asm := newMockArgoSecretManagerWithTracking()
	asm.setSoftOwner("cluster-a", "helm-release")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cr := result.Results[0]
	if cr.Status != "success" {
		t.Fatalf("foreign ownership must WARN, not fail the adoption; status=%q error=%q", cr.Status, cr.Error)
	}
	if len(cr.Warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(cr.Warnings), cr.Warnings)
	}
	if !strings.Contains(cr.Warnings[0], `"helm-release"`) {
		t.Errorf("warning = %q, want it to name the owning app/release", cr.Warnings[0])
	}
	if !strings.Contains(cr.Warnings[0], "may be") {
		t.Errorf("warning = %q, want the soft-confidence hedge \"may be\"", cr.Warnings[0])
	}
	if !strings.Contains(cr.Warnings[0], "Helm") {
		t.Errorf("warning = %q, want it to mention the Helm possibility", cr.Warnings[0])
	}
	if !strings.Contains(cr.Warnings[0], "Replace") {
		t.Errorf("warning = %q, want it to mention the Replace sync-option risk", cr.Warnings[0])
	}
}

func TestAdoptClusters_NoForeignOwner_NoWarning(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	asm := newMockArgoSecretManagerWithTracking() // no tracking owner registered
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results[0].Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", result.Results[0].Warnings)
	}
}

// TestAdoptClusters_PlainMockManager_NoWarning proves the optional
// capability really is optional: the shared mockArgoSecretManager (used by
// every other adopt test) does NOT implement GetTrackingOwner, so the
// type-assert fails silently and no warning is ever attached — pre-89.5
// tests using the plain mock keep passing unmodified.
func TestAdoptClusters_PlainMockManager_NoWarning(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	asm := newMockArgoSecretManager() // plain mock — no GetTrackingOwner method
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results[0].Warnings) != 0 {
		t.Fatalf("expected no warnings from a manager without the optional capability, got %v", result.Results[0].Warnings)
	}
}

// TestAdoptClusters_ForeignOwnerCheckError_DoesNotFailAdoption — a
// detection-call error (e.g. a transient K8s API error) must never fail
// the adoption; it is advisory-only and simply produces no warning.
func TestAdoptClusters_ForeignOwnerCheckError_DoesNotFailAdoption(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	asm := newMockArgoSecretManagerWithTracking()
	asm.trackingErr = context.DeadlineExceeded

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Results[0].Status != "success" {
		t.Fatalf("a foreign-owner detection failure must not fail adoption; status=%q", result.Results[0].Status)
	}
	if len(result.Results[0].Warnings) != 0 {
		t.Fatalf("expected no warnings on a detection error, got %v", result.Results[0].Warnings)
	}
}

// ---------- RegisterCluster (self-managed) ----------

func TestRegisterCluster_SelfManaged_ForeignOwnerWarning(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	asm := newMockArgoSecretManagerWithTracking()
	asm.setHardOwner("byo-conn", "cluster-secrets-app")

	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:                "byo-conn",
		Provider:            "kubeconfig",
		Addons:              map[string]bool{"monitoring": true},
		ConnectionManagedBy: models.ConnectionManagedByUser,
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("foreign ownership must WARN, not fail registration; status=%q error=%q", result.Status, result.Error)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], `"cluster-secrets-app"`) {
		t.Errorf("warning = %q, want it to name the owning app", result.Warnings[0])
	}
}

// TestRegisterCluster_SelfManaged_ForeignOwnerWarning_Soft mirrors the
// hard-confidence test above, for the soft signal.
func TestRegisterCluster_SelfManaged_ForeignOwnerWarning_Soft(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	asm := newMockArgoSecretManagerWithTracking()
	asm.setSoftOwner("byo-conn", "cluster-secrets-app")

	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:                "byo-conn",
		Provider:            "kubeconfig",
		Addons:              map[string]bool{"monitoring": true},
		ConnectionManagedBy: models.ConnectionManagedByUser,
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "may be") || !strings.Contains(result.Warnings[0], "Helm") {
		t.Errorf("warning = %q, want the soft-confidence \"may be\" hedge and the Helm mention", result.Warnings[0])
	}
}

// TestRegisterCluster_SharkoManaged_NoForeignOwnerCheck — the foreign-owner
// check only runs for self-managed (connectionManagedBy: user)
// registrations; a Sharko-managed registration must never surface this
// warning even if the mock is primed with a tracking owner (a Sharko-owned
// Secret with a stray foreign marker is a different, out-of-scope problem).
func TestRegisterCluster_SharkoManaged_NoForeignOwnerCheck(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	asm := newMockArgoSecretManagerWithTracking()
	asm.setHardOwner("sharko-owned", "renderer-app")

	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:     "sharko-owned",
		Provider: "kubeconfig",
		Addons:   map[string]bool{"monitoring": true},
		// ConnectionManagedBy deliberately absent (sharko-managed).
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("sharko-managed registration must not run the foreign-owner check, got warnings: %v", result.Warnings)
	}
}
