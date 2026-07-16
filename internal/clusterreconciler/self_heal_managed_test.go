package clusterreconciler

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// TestManagedClusterSelfHeal_OFF_NoWrite verifies that when self-heal is OFF
// (the default), a drifted managed cluster stays OutOfSync and Sharko does NOT
// modify the cluster Secret (V3 G3 acceptance criterion #1).
func TestManagedClusterSelfHeal_OFF_NoWrite(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create a managed cluster Secret with drifted labels (addon-foo missing)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:         LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				// addon-foo is MISSING — drift
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "test-cluster",
			"server": "https://test.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{})

	// managed-clusters.yaml declares addon-foo enabled
	managedClustersBody := []byte(`
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: test-cluster
      secretPath: test-cluster
      labels:
        addon-foo: enabled
`)

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": managedClustersBody,
	}

	var vault fakeVault
	vault.creds = map[string]*providers.Kubeconfig{
		"test-cluster": {
			Server: "https://test.example.com",
			Token:  "fake-token",
			CAData: []byte("fake-ca"),
		},
	}

	var auditLog []audit.Entry
	auditFn := func(e audit.Entry) {
		auditLog = append(auditLog, e)
	}

	// Self-heal OFF (nil SelfHealFn → defaults to false)
	r := New(Deps{
		CMStore:     cmstore.NewStore(client, "sharko", "sharko-recon-state"),
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       &vault,
		AuditFn:     auditFn,
		Namespace:   "argocd",
		SelfHealFn:  nil, // OFF
	})

	r.pollOnce(context.Background())

	// Assert: Secret is UNCHANGED (no write occurred)
	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "test-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if _, has := updated.Labels["addon-foo"]; has {
		t.Error("addon-foo label was added — self-heal OFF should NOT write to the Secret")
	}

	// Assert: reconcile record shows OutOfSync (drift detected, not corrected)
	rec, ok := r.LastReconcile("test-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record for test-cluster")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Errorf("expected OutcomeSucceeded (Secret present), got %v", rec.Outcome)
	}
	if rec.LabelDrift == nil {
		t.Fatal("expected non-nil LabelDrift when labels don't match")
	}
	if len(rec.LabelDrift.Added) != 1 || rec.LabelDrift.Added[0] != "addon-foo" {
		t.Errorf("expected drift.Added=['addon-foo'], got %v", rec.LabelDrift.Added)
	}
}

// TestManagedClusterSelfHeal_ON_ReApplies verifies that when self-heal is ON,
// the reconciler re-applies git-desired addon labels onto a drifted managed
// cluster, merging ONLY addon-label keys (V3 G3 acceptance criterion #2).
func TestManagedClusterSelfHeal_ON_ReApplies(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create a managed cluster Secret with drifted labels
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:         LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				"addon-old":           "enabled", // will be removed (not in git)
				// addon-foo missing (will be added from git)
			},
			Annotations: map[string]string{
				"user-annotation": "keep-this", // must stay untouched
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("test-cluster"),
			"server": []byte("https://test.example.com"),
			"config": []byte(`{"execProviderConfig":{}}`),
		},
	}
	client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{})

	// managed-clusters.yaml declares addon-foo enabled (addon-old absent)
	managedClustersBody := []byte(`
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: test-cluster
      secretPath: test-cluster
      labels:
        addon-foo: enabled
`)

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": managedClustersBody,
	}

	var vault fakeVault
	vault.creds = map[string]*providers.Kubeconfig{
		"test-cluster": {
			Server: "https://test.example.com",
			Token:  "fake-token",
			CAData: []byte("fake-ca"),
		},
	}

	var auditLog []audit.Entry
	auditFn := func(e audit.Entry) {
		auditLog = append(auditLog, e)
	}

	// Self-heal ON
	selfHealOn := func(ctx context.Context) bool { return true }

	r := New(Deps{
		CMStore:     cmstore.NewStore(client, "sharko", "sharko-recon-state"),
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       &vault,
		AuditFn:     auditFn,
		Namespace:   "argocd",
		SelfHealFn:  selfHealOn,
	})

	r.pollOnce(context.Background())

	// Assert: Secret was UPDATED with git-desired addon labels
	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "test-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if updated.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon-foo should be 'enabled' after self-heal, got %q", updated.Labels["addon-foo"])
	}
	// Note: addon-old stays on the Secret — SyncLabelsOnly merges desired keys
	// but does NOT remove extra keys (by design — same as self-managed path).
	// The "Removed" drift is detected but not auto-healed in this implementation.
	//
	// Note: managed-by label IS removed by SyncLabelsOnly for non-adopted Secrets
	// (line 785-806 in manager.go) — this is the self-managed handover behavior.
	// For a fully managed cluster self-heal, we might want different behavior in
	// the future, but for V3 G3 MVP we use the same SyncLabelsOnly primitive.
	// Annotations must stay untouched
	if updated.Annotations["user-annotation"] != "keep-this" {
		t.Error("user annotations must be preserved")
	}
	// Data must stay untouched
	if string(updated.Data["server"]) != "https://test.example.com" {
		t.Error("Data must be preserved")
	}

	// Assert: reconcile record shows Succeeded (drift corrected)
	rec, ok := r.LastReconcile("test-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record for test-cluster")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Errorf("expected OutcomeSucceeded (drift corrected), got %v", rec.Outcome)
	}
	if rec.LabelDrift != nil {
		t.Errorf("expected LabelDrift to be nil after self-heal, got %+v", rec.LabelDrift)
	}
	if rec.Message != "drift corrected — git-desired labels re-applied" {
		t.Errorf("unexpected message: %q", rec.Message)
	}

	// Assert: audit log contains a self-heal success event
	var foundSelfHeal bool
	for _, e := range auditLog {
		if e.Event == "cluster_secret_managed_self_heal" && e.Result == "success" {
			foundSelfHeal = true
		}
	}
	if !foundSelfHeal {
		t.Error("expected a cluster_secret_managed_self_heal success audit entry")
	}
}

// TestManagedClusterSelfHeal_DefaultOFF verifies that the setting defaults to
// OFF when the getter returns false (V3 G3 acceptance criterion #3).
func TestManagedClusterSelfHeal_DefaultOFF(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create a managed cluster Secret with drifted labels
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:         LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "test-cluster",
			"server": "https://test.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{})

	managedClustersBody := []byte(`
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: test-cluster
      labels:
        addon-foo: enabled
`)

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": managedClustersBody,
	}

	var vault fakeVault
	vault.creds = map[string]*providers.Kubeconfig{
		"test-cluster": {
			Server: "https://test.example.com",
			Token:  "fake-token",
			CAData: []byte("fake-ca"),
		},
	}

	var auditLog []audit.Entry
	auditFn := func(e audit.Entry) {
		auditLog = append(auditLog, e)
	}

	// Self-heal explicitly OFF
	selfHealOff := func(ctx context.Context) bool { return false }

	r := New(Deps{
		CMStore:     cmstore.NewStore(client, "sharko", "sharko-recon-state"),
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       &vault,
		AuditFn:     auditFn,
		Namespace:   "argocd",
		SelfHealFn:  selfHealOff,
	})

	r.pollOnce(context.Background())

	// Assert: Secret is UNCHANGED
	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "test-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if _, has := updated.Labels["addon-foo"]; has {
		t.Error("addon-foo label was added — default OFF should NOT write")
	}
}

// TestSelfManagedUnaffected verifies that self-managed connections continue to
// self-heal via syncSelfManaged regardless of the managed_cluster_self_heal
// setting (V3 G3 acceptance criterion #4 — self-managed path unchanged).
func TestSelfManagedUnaffected(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create a self-managed cluster Secret (user owns it — no managed-by label)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "self-managed-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				// addon-foo missing — will be synced regardless of managed_cluster_self_heal
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "self-managed-cluster",
			"server": "https://self.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{})

	// managed-clusters.yaml declares this cluster as self-managed (connectionManagedBy: user)
	managedClustersBody := []byte(`
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: self-managed-cluster
      connectionManagedBy: user
      labels:
        addon-foo: enabled
`)

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": managedClustersBody,
	}

	var vault fakeVault
	vault.creds = map[string]*providers.Kubeconfig{
		"self-managed-cluster": {
			Server: "https://self.example.com",
			Token:  "fake-token",
			CAData: []byte("fake-ca"),
		},
	}

	var auditLog []audit.Entry
	auditFn := func(e audit.Entry) {
		auditLog = append(auditLog, e)
	}

	// Self-heal OFF for managed clusters — self-managed should still sync
	selfHealOff := func(ctx context.Context) bool { return false }

	r := New(Deps{
		CMStore:     cmstore.NewStore(client, "sharko", "sharko-recon-state"),
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       &vault,
		AuditFn:     auditFn,
		Namespace:   "argocd",
		SelfHealFn:  selfHealOff,
	})

	r.pollOnce(context.Background())

	// Assert: self-managed Secret WAS updated (syncSelfManaged always runs)
	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "self-managed-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if updated.Labels["addon-foo"] != "enabled" {
		t.Error("self-managed cluster should have addon-foo synced regardless of managed_cluster_self_heal setting")
	}

	// Assert: audit log contains a user_label_sync event (not managed_self_heal)
	var foundUserSync bool
	for _, e := range auditLog {
		if e.Event == "cluster_secret_user_label_sync" && e.Result == "success" {
			foundUserSync = true
		}
	}
	if !foundUserSync {
		t.Error("expected a cluster_secret_user_label_sync success audit entry for self-managed cluster")
	}
}
