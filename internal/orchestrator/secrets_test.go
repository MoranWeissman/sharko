package orchestrator

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeClientFactoryFor returns a RemoteClientFactory that always returns the given client.
// Unlike fakeClientFactory (in orchestrator_test.go), this accepts an existing client so
// tests can inspect state after operations.
func fakeClientFactoryFor(client kubernetes.Interface) RemoteClientFactory {
	return func(_ []byte) (kubernetes.Interface, error) {
		return client, nil
	}
}

func TestCreateAddonSecrets_NoSecretManagement(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	// No SetSecretManagement call — should return nil, nil.
	created, err := orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no secrets created, got %v", created)
	}
}

func TestCreateAddonSecrets_CreatesSecretForEnabledAddon(t *testing.T) {
	client := fake.NewSimpleClientset()
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{
		secrets: map[string][]byte{
			"secrets/datadog/api-key": []byte("fake-api-key"),
		},
	}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	created, err := orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 1 || created[0] != "datadog-secret" {
		t.Errorf("expected [datadog-secret], got %v", created)
	}

	// Verify secret exists in the fake cluster.
	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if string(secret.Data["api-key"]) != "fake-api-key" {
		t.Errorf("unexpected api-key value: %s", secret.Data["api-key"])
	}
}

func TestCreateAddonSecrets_SkipsDisabledAddons(t *testing.T) {
	client := fake.NewSimpleClientset()
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{secrets: map[string][]byte{"secrets/datadog/api-key": []byte("key")}}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	// datadog is disabled
	created, err := orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no secrets for disabled addon, got %v", created)
	}
}

func TestCreateAddonSecrets_FetcherError(t *testing.T) {
	client := fake.NewSimpleClientset()
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{err: errors.New("vault unavailable")}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	_, err := orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	if err == nil {
		t.Fatal("expected error from secret fetcher")
	}
}

func TestDeleteAddonSecrets_DeletesDisabledAddonSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{secrets: map[string][]byte{"secrets/datadog/api-key": []byte("key")}}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	// First create the secret.
	_, _ = orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})

	// Now disable it — should delete.
	deleted, err := orch.deleteAddonSecrets(context.Background(), nil, map[string]bool{"datadog": false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "datadog-secret" {
		t.Errorf("expected [datadog-secret] deleted, got %v", deleted)
	}
}

func TestDeleteAllAddonSecrets_DeletesAll(t *testing.T) {
	client := fake.NewSimpleClientset()
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{secrets: map[string][]byte{"secrets/datadog/api-key": []byte("key")}}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	// Create first.
	_, _ = orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})

	// Delete all.
	deleted, err := orch.deleteAllAddonSecrets(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 1 {
		t.Errorf("expected 1 deleted secret, got %v", deleted)
	}
}

func TestDeleteAllAddonSecrets_NoSecretManagement(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	deleted, err := orch.deleteAllAddonSecrets(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("expected no deleted secrets, got %v", deleted)
	}
}
