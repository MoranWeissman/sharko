package orchestrator

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// secrets_ownership_test.go — P1-A A1, the delete half.
//
// Disabling an addon and deregistering a cluster both used to Delete the
// addon secret by name with no ownership question asked. That is the same
// hole as the overwrite bug, pointing the more dangerous way: an overwrite
// is corrected by the next pass, a delete of somebody else's secret is not.

func ownershipDefs() map[string]AddonSecretDefinition {
	return map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
}

func secretOwnedBy(owner string) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "datadog-secret", Namespace: "monitoring"},
		Data:       map[string][]byte{"api-key": []byte("someone-elses-value")},
	}
	if owner != "" {
		s.Labels = map[string]string{"app.kubernetes.io/managed-by": owner}
	}
	return s
}

func ownershipOrch(client *fake.Clientset) *Orchestrator {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	fetcher := &mockSecretFetcher{secrets: map[string][]byte{"secrets/datadog/api-key": []byte("key")}}
	orch.SetSecretManagement(ownershipDefs(), fetcher, fakeClientFactoryFor(client))
	return orch
}

func assertSecretStillThere(t *testing.T, client *fake.Clientset) {
	t.Helper()
	live, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("a secret Sharko did not create was deleted: %v", err)
	}
	if string(live.Data["api-key"]) != "someone-elses-value" {
		t.Errorf("the secret's content changed: %q", live.Data["api-key"])
	}
}

// TestDisableAddon_LeavesAForeignSecretAlone — the disable-addon cleanup
// path.
func TestDisableAddon_LeavesAForeignSecretAlone(t *testing.T) {
	for _, owner := range []string{"external-secrets", ""} {
		client := fake.NewSimpleClientset(secretOwnedBy(owner))
		orch := ownershipOrch(client)

		deleted, err := orch.deleteAddonSecrets(context.Background(), "prod-eu", nil, map[string]bool{"datadog": false})
		if err != nil {
			t.Fatalf("owner=%q: unexpected error: %v", owner, err)
		}
		if len(deleted) != 0 {
			t.Errorf("owner=%q: reported %v as deleted", owner, deleted)
		}
		assertSecretStillThere(t, client)
	}
}

// TestDeregister_LeavesAForeignSecretAlone — the deregister cleanup path.
func TestDeregister_LeavesAForeignSecretAlone(t *testing.T) {
	for _, owner := range []string{"external-secrets", ""} {
		client := fake.NewSimpleClientset(secretOwnedBy(owner))
		orch := ownershipOrch(client)

		deleted, err := orch.deleteAllAddonSecrets(context.Background(), "prod-eu", nil)
		if err != nil {
			t.Fatalf("owner=%q: unexpected error: %v", owner, err)
		}
		if len(deleted) != 0 {
			t.Errorf("owner=%q: reported %v as deleted", owner, deleted)
		}
		assertSecretStillThere(t, client)
	}
}

// TestCreateAddonSecrets_RefusesToOverwriteAForeignSecret — the register /
// enable-addon write path uses the same choke point, so it is gated too.
func TestCreateAddonSecrets_RefusesToOverwriteAForeignSecret(t *testing.T) {
	client := fake.NewSimpleClientset(secretOwnedBy("external-secrets"))
	orch := ownershipOrch(client)

	result, err := orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(result.Created) != 0 {
		t.Errorf("claimed to have created %v", result.Created)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("expected the refusal to be reported once, got %d", len(result.Failed))
	}
	assertSecretStillThere(t, client)
}
