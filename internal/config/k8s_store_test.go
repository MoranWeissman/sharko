package config

import (
	"testing"

	"github.com/moran/argocd-addons-platform/internal/models"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestK8sStore(t *testing.T) *K8sStore {
	t.Helper()
	clientset := fake.NewSimpleClientset()
	store, _ := newK8sStoreWithClient(clientset, "test-ns", "aap-connections", "test-encryption-key-32chars-ok")
	return store
}

func TestK8sStore_SaveAndList(t *testing.T) {
	s := newTestK8sStore(t)

	conn := models.Connection{
		Name: "my-conn",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "acme",
			Repo:     "infra",
			Token:    "ghp_secret",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: "https://argocd.example.com",
			Token:     "argocd-token",
			Namespace: "argocd",
		},
	}

	if err := s.SaveConnection(conn); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	list, err := s.ListConnections()
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(list))
	}
	if list[0].Name != "my-conn" {
		t.Errorf("expected name 'my-conn', got %q", list[0].Name)
	}
	if list[0].Git.Token != "ghp_secret" {
		t.Errorf("expected token preserved, got %q", list[0].Git.Token)
	}
}

func TestK8sStore_GetConnection(t *testing.T) {
	s := newTestK8sStore(t)

	conn := models.Connection{
		Name: "test-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
	}
	if err := s.SaveConnection(conn); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	got, err := s.GetConnection("test-conn")
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got == nil {
		t.Fatal("expected connection, got nil")
	}
	if got.Name != "test-conn" {
		t.Errorf("expected 'test-conn', got %q", got.Name)
	}

	notFound, err := s.GetConnection("does-not-exist")
	if err != nil {
		t.Fatalf("GetConnection non-existent: %v", err)
	}
	if notFound != nil {
		t.Errorf("expected nil for non-existent, got %+v", notFound)
	}
}

func TestK8sStore_DeleteConnection(t *testing.T) {
	s := newTestK8sStore(t)

	for _, name := range []string{"conn-a", "conn-b"} {
		if err := s.SaveConnection(models.Connection{
			Name: name,
			Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub},
		}); err != nil {
			t.Fatalf("SaveConnection %s: %v", name, err)
		}
	}

	if err := s.DeleteConnection("conn-a"); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}

	list, err := s.ListConnections()
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 connection after delete, got %d", len(list))
	}
	if list[0].Name != "conn-b" {
		t.Errorf("expected 'conn-b' to remain, got %q", list[0].Name)
	}
}

func TestK8sStore_DeleteNonExistent(t *testing.T) {
	s := newTestK8sStore(t)

	err := s.DeleteConnection("no-such-conn")
	if err == nil {
		t.Fatal("expected error deleting non-existent connection, got nil")
	}
}

func TestK8sStore_ActiveConnection(t *testing.T) {
	s := newTestK8sStore(t)

	first := models.Connection{
		Name: "first",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub},
	}
	second := models.Connection{
		Name: "second",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub},
	}

	if err := s.SaveConnection(first); err != nil {
		t.Fatalf("SaveConnection first: %v", err)
	}
	if err := s.SaveConnection(second); err != nil {
		t.Fatalf("SaveConnection second: %v", err)
	}

	// First connection should be auto-active
	active, err := s.GetActiveConnection()
	if err != nil {
		t.Fatalf("GetActiveConnection: %v", err)
	}
	if active != "first" {
		t.Errorf("expected 'first' to be active, got %q", active)
	}

	// Set second as active
	if err := s.SetActiveConnection("second"); err != nil {
		t.Fatalf("SetActiveConnection: %v", err)
	}

	active, err = s.GetActiveConnection()
	if err != nil {
		t.Fatalf("GetActiveConnection after set: %v", err)
	}
	if active != "second" {
		t.Errorf("expected 'second' to be active, got %q", active)
	}
}

func TestK8sStore_SetActiveNonExistent(t *testing.T) {
	s := newTestK8sStore(t)

	err := s.SetActiveConnection("ghost")
	if err == nil {
		t.Fatal("expected error setting active to non-existent connection, got nil")
	}
}

func TestK8sStore_FirstConnectionBecomesDefaultAndActive(t *testing.T) {
	s := newTestK8sStore(t)

	conn := models.Connection{
		Name: "only-one",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub},
	}
	if err := s.SaveConnection(conn); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	list, err := s.ListConnections()
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(list))
	}
	if !list[0].IsDefault {
		t.Error("expected first connection to be marked as default")
	}

	active, err := s.GetActiveConnection()
	if err != nil {
		t.Fatalf("GetActiveConnection: %v", err)
	}
	if active != "only-one" {
		t.Errorf("expected 'only-one' to be active, got %q", active)
	}
}

func TestK8sStore_UpdateExisting(t *testing.T) {
	s := newTestK8sStore(t)

	conn := models.Connection{
		Name:        "updatable",
		Description: "original",
		Git:         models.GitRepoConfig{Provider: models.GitProviderGitHub},
	}
	if err := s.SaveConnection(conn); err != nil {
		t.Fatalf("SaveConnection original: %v", err)
	}

	conn.Description = "updated"
	if err := s.SaveConnection(conn); err != nil {
		t.Fatalf("SaveConnection updated: %v", err)
	}

	list, err := s.ListConnections()
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 connection (no duplicates), got %d", len(list))
	}
	if list[0].Description != "updated" {
		t.Errorf("expected description 'updated', got %q", list[0].Description)
	}
}

func TestK8sStore_PersistsAcrossInstances(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	store1, err := newK8sStoreWithClient(clientset, "test-ns", "aap-connections", "test-encryption-key-32chars-ok")
	if err != nil {
		t.Fatalf("newK8sStoreWithClient store1: %v", err)
	}

	conn := models.Connection{
		Name: "persisted",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Token: "secret-token"},
	}
	if err := store1.SaveConnection(conn); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	store2, err := newK8sStoreWithClient(clientset, "test-ns", "aap-connections", "test-encryption-key-32chars-ok")
	if err != nil {
		t.Fatalf("newK8sStoreWithClient store2: %v", err)
	}

	list, err := store2.ListConnections()
	if err != nil {
		t.Fatalf("ListConnections store2: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 connection from store2, got %d", len(list))
	}
	if list[0].Git.Token != "secret-token" {
		t.Errorf("expected token 'secret-token', got %q", list[0].Git.Token)
	}
}

func TestK8sStore_WrongKeyReturnsError(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	store1, err := newK8sStoreWithClient(clientset, "test-ns", "aap-connections", "correct-key-for-encryption-ok!!")
	if err != nil {
		t.Fatalf("newK8sStoreWithClient store1: %v", err)
	}

	conn := models.Connection{
		Name: "secret-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Token: "very-secret"},
	}
	if err := store1.SaveConnection(conn); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	store2, err := newK8sStoreWithClient(clientset, "test-ns", "aap-connections", "wrong-key-for-decryption-bad!!")
	if err != nil {
		t.Fatalf("newK8sStoreWithClient store2: %v", err)
	}

	_, err = store2.ListConnections()
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key, got nil")
	}
}

func TestK8sStore_DeleteActiveReassigns(t *testing.T) {
	s := newTestK8sStore(t)

	_ = s.SaveConnection(models.Connection{Name: "conn-a"})
	_ = s.SaveConnection(models.Connection{Name: "conn-b"})
	_ = s.SetActiveConnection("conn-a")

	if err := s.DeleteConnection("conn-a"); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}

	active, err := s.GetActiveConnection()
	if err != nil {
		t.Fatalf("GetActiveConnection: %v", err)
	}
	if active != "conn-b" {
		t.Errorf("expected active to shift to 'conn-b', got %q", active)
	}
}

func TestK8sStore_IsDefaultMutualExclusion(t *testing.T) {
	s := newTestK8sStore(t)

	_ = s.SaveConnection(models.Connection{Name: "a", IsDefault: true})
	_ = s.SaveConnection(models.Connection{Name: "b", IsDefault: true})

	list, err := s.ListConnections()
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}

	defaultCount := 0
	for _, c := range list {
		if c.IsDefault {
			defaultCount++
			if c.Name != "b" {
				t.Errorf("expected 'b' to be default, got %q", c.Name)
			}
		}
	}
	if defaultCount != 1 {
		t.Errorf("expected exactly 1 default, got %d", defaultCount)
	}
}

func TestK8sStore_EmptySecretReturnsEmpty(t *testing.T) {
	s := newTestK8sStore(t)

	list, err := s.ListConnections()
	if err != nil {
		t.Fatalf("ListConnections on empty store: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d connections", len(list))
	}

	active, err := s.GetActiveConnection()
	if err != nil {
		t.Fatalf("GetActiveConnection on empty store: %v", err)
	}
	if active != "" {
		t.Errorf("expected empty active connection, got %q", active)
	}
}
