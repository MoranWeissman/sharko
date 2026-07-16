package config

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestMergeConnectionFromEnv_NonSecretFieldWins verifies a git-declared
// non-secret field overwrites the runtime value (git wins).
func TestMergeConnectionFromEnv_NonSecretFieldWins(t *testing.T) {
	t.Setenv(envConnGitOwner, "git-owner")
	t.Setenv(envConnArgocdServerURL, "https://argo.example.com")
	t.Setenv(envConnProviderRegion, "eu-west-1")

	conn := &models.Connection{
		Name: "active",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "runtime-owner", Token: "ghp_secret"},
		Argocd: models.ArgocdConfig{ServerURL: "https://old.example.com", Token: "argo_secret"},
		Provider: &models.ProviderConfig{Type: "aws-sm", Region: "us-east-1"},
	}

	changed := MergeConnectionFromEnv(conn)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if conn.Git.Owner != "git-owner" {
		t.Errorf("owner: git should win, got %q", conn.Git.Owner)
	}
	if conn.Argocd.ServerURL != "https://argo.example.com" {
		t.Errorf("argocd server_url: git should win, got %q", conn.Argocd.ServerURL)
	}
	if conn.Provider.Region != "eu-west-1" {
		t.Errorf("provider region: git should win, got %q", conn.Provider.Region)
	}
}

// TestMergeConnectionFromEnv_PreservesSecrets is the security gate: the
// field-level merge MUST preserve the encrypted secret material (git token/PAT,
// ArgoCD token) while overwriting non-secret fields.
func TestMergeConnectionFromEnv_PreservesSecrets(t *testing.T) {
	t.Setenv(envConnGitOwner, "new-owner")
	t.Setenv(envConnArgocdServerURL, "https://new-argo.example.com")

	conn := &models.Connection{
		Name: "active",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "old-owner",
			Token:    "ghp_supersecret_token",
			PAT:      "azdo_supersecret_pat",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: "https://old-argo.example.com",
			Token:     "argocd_supersecret_token",
		},
	}

	MergeConnectionFromEnv(conn)

	if conn.Git.Token != "ghp_supersecret_token" {
		t.Errorf("git token must be preserved, got %q", conn.Git.Token)
	}
	if conn.Git.PAT != "azdo_supersecret_pat" {
		t.Errorf("git PAT must be preserved, got %q", conn.Git.PAT)
	}
	if conn.Argocd.Token != "argocd_supersecret_token" {
		t.Errorf("argocd token must be preserved, got %q", conn.Argocd.Token)
	}
	// And non-secret fields were still merged.
	if conn.Git.Owner != "new-owner" || conn.Argocd.ServerURL != "https://new-argo.example.com" {
		t.Error("non-secret fields should have merged from env")
	}
}

// TestMergeConnectionFromEnv_UndeclaredFieldsUnchanged verifies back-compat:
// fields NOT declared in env keep their runtime value.
func TestMergeConnectionFromEnv_UndeclaredFieldsUnchanged(t *testing.T) {
	// Only owner is declared; everything else must persist.
	t.Setenv(envConnGitOwner, "declared-owner")

	conn := &models.Connection{
		Name:   "active",
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "old", Repo: "runtime-repo"},
		Argocd: models.ArgocdConfig{ServerURL: "https://keepme.example.com", Namespace: "argocd"},
		GitOps: &models.GitOpsSettings{BaseBranch: "develop"},
	}

	MergeConnectionFromEnv(conn)

	if conn.Git.Repo != "runtime-repo" {
		t.Errorf("undeclared repo must persist, got %q", conn.Git.Repo)
	}
	if conn.Argocd.ServerURL != "https://keepme.example.com" {
		t.Errorf("undeclared argocd server must persist, got %q", conn.Argocd.ServerURL)
	}
	if conn.Argocd.Namespace != "argocd" {
		t.Errorf("undeclared argocd namespace must persist, got %q", conn.Argocd.Namespace)
	}
	if conn.GitOps == nil || conn.GitOps.BaseBranch != "develop" {
		t.Error("undeclared gitops base branch must persist")
	}
}

// TestMergeConnectionFromEnv_NoDeclaredFields verifies a pristine env leaves
// the connection untouched and reports no change (idempotency / no churn).
func TestMergeConnectionFromEnv_NoDeclaredFields(t *testing.T) {
	conn := &models.Connection{
		Name:   "active",
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Argocd: models.ArgocdConfig{ServerURL: "https://argo"},
	}
	if MergeConnectionFromEnv(conn) {
		t.Error("expected changed=false when nothing is declared")
	}
}

// TestMergeConnectionFromEnv_Idempotent verifies re-applying an already-merged
// value reports no change (so the periodic reclaim does not churn the Secret).
func TestMergeConnectionFromEnv_Idempotent(t *testing.T) {
	t.Setenv(envConnGitOwner, "steady-owner")
	conn := &models.Connection{Git: models.GitRepoConfig{Owner: "steady-owner"}}
	if MergeConnectionFromEnv(conn) {
		t.Error("value already matches env — expected changed=false")
	}
}

// TestMergeConnectionFromEnv_LenientBool verifies a malformed bool is treated
// as undeclared (warn + keep runtime value), never crashing.
func TestMergeConnectionFromEnv_LenientBool(t *testing.T) {
	t.Setenv(envConnArgocdInsecure, "maybe")
	conn := &models.Connection{Argocd: models.ArgocdConfig{Insecure: false}}
	if MergeConnectionFromEnv(conn) {
		t.Error("malformed bool should be treated as undeclared (no change)")
	}
	if conn.Argocd.Insecure {
		t.Error("insecure should keep its runtime value on malformed env")
	}
}

// TestMergeConnectionFromEnv_AllocatesNilProvider verifies a declared provider
// field allocates the pointer when the connection had none.
func TestMergeConnectionFromEnv_AllocatesNilProvider(t *testing.T) {
	t.Setenv(envConnProviderType, "aws-sm")
	t.Setenv(envConnGitOpsPRAutoMerge, "true")
	conn := &models.Connection{} // nil Provider, nil GitOps
	if !MergeConnectionFromEnv(conn) {
		t.Fatal("expected changed=true")
	}
	if conn.Provider == nil || conn.Provider.Type != "aws-sm" {
		t.Errorf("provider should be allocated with type aws-sm, got %+v", conn.Provider)
	}
	if conn.GitOps == nil || conn.GitOps.PRAutoMerge == nil || !*conn.GitOps.PRAutoMerge {
		t.Error("gitops PRAutoMerge should be allocated and true")
	}
}

// --- ReconcileConnectionFromEnv (Store-level) ---

// fakeStore is a minimal in-memory Store for reconcile tests.
type fakeStore struct {
	conns  map[string]models.Connection
	active string
	saved  []models.Connection
}

func newFakeStore() *fakeStore { return &fakeStore{conns: map[string]models.Connection{}} }

func (f *fakeStore) ListConnections() ([]models.Connection, error) { return nil, nil }
func (f *fakeStore) GetConnection(name string) (*models.Connection, error) {
	c, ok := f.conns[name]
	if !ok {
		return nil, nil
	}
	return &c, nil
}
func (f *fakeStore) SaveConnection(conn models.Connection) error {
	f.conns[conn.Name] = conn
	f.saved = append(f.saved, conn)
	return nil
}
func (f *fakeStore) DeleteConnection(name string) error   { delete(f.conns, name); return nil }
func (f *fakeStore) GetActiveConnection() (string, error) { return f.active, nil }
func (f *fakeStore) SetActiveConnection(name string) error {
	f.active = name
	return nil
}

// TestReconcileConnectionFromEnv_MergesAndSaves verifies the reconcile fetches
// the active connection, merges env, and persists (git wins) while preserving
// the secret token round-tripped through the store.
func TestReconcileConnectionFromEnv_MergesAndSaves(t *testing.T) {
	t.Setenv(envConnGitOwner, "reconciled-owner")

	store := newFakeStore()
	store.conns["active"] = models.Connection{
		Name: "active",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "old", Token: "ghp_keep"},
	}
	store.active = "active"

	changed, err := ReconcileConnectionFromEnv(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected one save, got %d", len(store.saved))
	}
	got := store.saved[0]
	if got.Git.Owner != "reconciled-owner" {
		t.Errorf("owner not merged, got %q", got.Git.Owner)
	}
	if got.Git.Token != "ghp_keep" {
		t.Errorf("secret token must survive the save round-trip, got %q", got.Git.Token)
	}
}

// TestReconcileConnectionFromEnv_NoActiveConnection verifies a no-op when there
// is no active connection (never fabricate a credential-less connection).
func TestReconcileConnectionFromEnv_NoActiveConnection(t *testing.T) {
	t.Setenv(envConnGitOwner, "irrelevant")
	store := newFakeStore() // no connections, empty active
	changed, err := ReconcileConnectionFromEnv(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no-op when no active connection")
	}
	if len(store.saved) != 0 {
		t.Error("must not save anything when there is no active connection")
	}
}

// TestReconcileConnectionFromEnv_IdempotentNoSave verifies an already-converged
// connection is not re-saved (no Secret write churn under the periodic reclaim).
func TestReconcileConnectionFromEnv_IdempotentNoSave(t *testing.T) {
	t.Setenv(envConnGitOwner, "converged")
	store := newFakeStore()
	store.conns["active"] = models.Connection{Name: "active", Git: models.GitRepoConfig{Owner: "converged"}}
	store.active = "active"

	changed, err := ReconcileConnectionFromEnv(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change when already converged")
	}
	if len(store.saved) != 0 {
		t.Error("must not save when nothing changed")
	}
}

var _ Store = (*fakeStore)(nil)
