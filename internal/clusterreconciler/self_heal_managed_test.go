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
				LabelManagedBy:                   LabelValueSharko,
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

// TestManagedClusterSelfHeal_ON_Converges verifies that when self-heal is ON,
// the reconciler FULLY converges the git-desired addon labels onto a drifted
// Sharko-MANAGED cluster Secret (V3 GF1 acceptance criteria #1, #3):
//
//   - git-declared addon label ADDED
//   - a removed-in-git Sharko addon label DELETED (full convergence — the
//     honest "drift corrected" the old SyncLabelsOnly-based code could not do)
//   - managed-by AND secret-type PRESERVED (the data-loss regression this
//     story fixes — the old handover branch deleted managed-by)
//   - foreign label + Data + annotations byte-for-byte UNTOUCHED
//
// The companion "next tick still managed" assertion lives in
// TestManagedClusterSelfHeal_SurvivesNextTick.
func TestManagedClusterSelfHeal_ON_Converges(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create a managed cluster Secret with drifted labels + a foreign label.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				"example.com/team":               "platform", // foreign — must be untouched
				"addon-old":                      "enabled",  // removed-in-git — must be DELETED
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

	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "test-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}

	// git-desired addon label ADDED
	if updated.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon-foo should be 'enabled' after self-heal, got %q", updated.Labels["addon-foo"])
	}
	// removed-in-git Sharko addon label DELETED (full convergence)
	if _, has := updated.Labels["addon-old"]; has {
		t.Errorf("addon-old should be DELETED by convergence, still present: %q", updated.Labels["addon-old"])
	}
	// managed-by PRESERVED — the regression this story fixes
	if updated.Labels[LabelManagedBy] != LabelValueSharko {
		t.Errorf("managed-by label MUST survive self-heal, got %q", updated.Labels[LabelManagedBy])
	}
	// secret-type PRESERVED
	if updated.Labels["argocd.argoproj.io/secret-type"] != "cluster" {
		t.Errorf("secret-type label MUST survive self-heal, got %q", updated.Labels["argocd.argoproj.io/secret-type"])
	}
	// foreign label UNTOUCHED
	if updated.Labels["example.com/team"] != "platform" {
		t.Errorf("foreign label example.com/team MUST be untouched, got %q", updated.Labels["example.com/team"])
	}
	// annotations UNTOUCHED
	if updated.Annotations["user-annotation"] != "keep-this" {
		t.Error("user annotations must be preserved")
	}
	// Data UNTOUCHED
	if string(updated.Data["server"]) != "https://test.example.com" {
		t.Error("Data must be preserved")
	}
	if string(updated.Data["config"]) != `{"execProviderConfig":{}}` {
		t.Error("Data['config'] must be preserved byte-for-byte")
	}

	// Reconcile record shows Succeeded (drift corrected) with drift cleared.
	rec, ok := r.LastReconcile("test-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record for test-cluster")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Errorf("expected OutcomeSucceeded (drift corrected), got %v (msg=%q)", rec.Outcome, rec.Message)
	}
	if rec.LabelDrift != nil {
		t.Errorf("expected LabelDrift to be nil after full convergence, got %+v", rec.LabelDrift)
	}
	if rec.Message != "drift corrected — git-desired addon labels converged" {
		t.Errorf("unexpected message: %q", rec.Message)
	}

	// Audit log contains a self-heal success event.
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

// TestManagedClusterSelfHeal_SurvivesNextTick verifies GF1 acceptance
// criterion #2: after a heal, the cluster is STILL managed on the next
// reconcile tick (survives listManagedSecrets) and reports Synced with no
// drift — proving the ownership label was preserved, not stripped.
func TestManagedClusterSelfHeal_SurvivesNextTick(t *testing.T) {
	client := fake.NewSimpleClientset()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				// addon-foo missing — drift
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
		"test-cluster": {Server: "https://test.example.com", Token: "fake-token", CAData: []byte("fake-ca")},
	}

	r := New(Deps{
		CMStore:     cmstore.NewStore(client, "sharko", "sharko-recon-state"),
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       &vault,
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
		SelfHealFn:  func(ctx context.Context) bool { return true },
	})

	// First tick: heals the drift.
	r.pollOnce(context.Background())

	// The cluster must still be visible to the ownership-filtered lister —
	// i.e. it did NOT get de-managed by an ownership-label strip.
	managed, err := r.listManagedSecrets(context.Background())
	if err != nil {
		t.Fatalf("listManagedSecrets: %v", err)
	}
	if _, ok := managed["test-cluster"]; !ok {
		t.Fatal("cluster dropped out of listManagedSecrets after heal — ownership label was stripped (the data-loss bug)")
	}

	// Second tick: now in sync, must report Succeeded with no drift.
	r.pollOnce(context.Background())
	rec, ok := r.LastReconcile("test-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record on the second tick")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Errorf("second tick: expected OutcomeSucceeded, got %v (msg=%q)", rec.Outcome, rec.Message)
	}
	if rec.LabelDrift != nil {
		t.Errorf("second tick: expected no drift, got %+v", rec.LabelDrift)
	}
}

// TestManagedClusterSelfHeal_Adopted verifies that self-heal on an ADOPTED
// cluster Secret (owned by another controller; Sharko is a guest) converges
// ONLY Sharko's own addon labels and never stamps ownership, while leaving the
// other owner's labels, Data, and annotations untouched (coordinator's
// adopted-vs-managed requirement).
func TestManagedClusterSelfHeal_Adopted(t *testing.T) {
	client := fake.NewSimpleClientset()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko, // present because adopted secrets carry it
				"argocd.argoproj.io/secret-type": "cluster",
				"app.kubernetes.io/instance":     "some-other-app", // the other owner's tracking label
				"addon-stale":                    "enabled",        // removed-in-git — must be DELETED
			},
			Annotations: map[string]string{
				"sharko.dev/adopted": "true", // adopted marker
				"keep-me":            "yes",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("adopted-cluster"),
			"server": []byte("https://adopted.example.com"),
			"config": []byte(`{"tlsClientConfig":{}}`),
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
    - name: adopted-cluster
      secretPath: adopted-cluster
      labels:
        addon-foo: enabled
`)

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": managedClustersBody,
	}
	var vault fakeVault
	vault.creds = map[string]*providers.Kubeconfig{
		"adopted-cluster": {Server: "https://adopted.example.com", Token: "fake-token", CAData: []byte("fake-ca")},
	}

	r := New(Deps{
		CMStore:     cmstore.NewStore(client, "sharko", "sharko-recon-state"),
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       &vault,
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
		SelfHealFn:  func(ctx context.Context) bool { return true },
	})

	r.pollOnce(context.Background())

	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "adopted-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	// Sharko's own addon labels converge.
	if updated.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon-foo should be added on adopted cluster, got %q", updated.Labels["addon-foo"])
	}
	if _, has := updated.Labels["addon-stale"]; has {
		t.Error("addon-stale should be DELETED by convergence on adopted cluster")
	}
	// The other owner's tracking label is untouched.
	if updated.Labels["app.kubernetes.io/instance"] != "some-other-app" {
		t.Errorf("the other owner's app-instance label MUST be untouched, got %q", updated.Labels["app.kubernetes.io/instance"])
	}
	// secret-type untouched.
	if updated.Labels["argocd.argoproj.io/secret-type"] != "cluster" {
		t.Errorf("secret-type MUST be untouched on adopted cluster, got %q", updated.Labels["argocd.argoproj.io/secret-type"])
	}
	// Annotations (including the adopted marker) untouched.
	if updated.Annotations["sharko.dev/adopted"] != "true" || updated.Annotations["keep-me"] != "yes" {
		t.Errorf("annotations MUST be untouched on adopted cluster, got %+v", updated.Annotations)
	}
	// Data untouched.
	if string(updated.Data["server"]) != "https://adopted.example.com" {
		t.Error("Data must be preserved on adopted cluster")
	}

	rec, ok := r.LastReconcile("adopted-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record for adopted-cluster")
	}
	if rec.Outcome != OutcomeSucceeded || rec.LabelDrift != nil {
		t.Errorf("expected Succeeded + nil drift on adopted heal, got %v drift=%+v", rec.Outcome, rec.LabelDrift)
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
				LabelManagedBy:                   LabelValueSharko,
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
