package clusterreconciler

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// resync_test.go — one-time re-sync from the drift view (v4-8-5).
//
// ResyncClusterLabels must:
//  1. Correct a drifted managed cluster's addon labels to match git, in one
//     call, regardless of the managed_cluster_self_heal setting.
//  2. NEVER consult (or claim to change) that setting — proven by wiring a
//     SelfHealFn that panics if called.
//  3. Never touch foreign labels, Data, or annotations — same guarantee
//     selfHealManagedCluster already has, exercised here through the public
//     entry point.
//  4. Return ErrClusterNotManaged for a cluster absent from the git-managed
//     list.
//  5. Report an honest added/removed/changed/unchanged diff.

// TestResyncClusterLabels_DriftedManaged_CorrectsAndReportsDiff pins
// acceptance criteria #1, #3, #5, and the self-heal-OFF requirement (#6):
// the resync corrects drift, reports what it did, and never needed the
// self-heal setting to do it.
func TestResyncClusterLabels_DriftedManaged_CorrectsAndReportsDiff(t *testing.T) {
	client := fake.NewSimpleClientset()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				"example.com/team":               "platform", // foreign — must survive
				"addon-old":                      "enabled",  // removed-in-git — must be deleted
				// addon-foo missing — will be added
			},
			Annotations: map[string]string{
				"user-annotation": "keep-this",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("test-cluster"),
			"server": []byte("https://test.example.com"),
			"config": []byte(`{"execProviderConfig":{}}`),
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

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

	selfHealCalls := 0
	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       staticVault(&fakeVault{}),
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
		// A resync must never consult the self-heal setting — panic if it
		// does, proving acceptance criterion #2 without relying on a
		// separate settings.Store round trip.
		SelfHealFn: func(ctx context.Context) bool {
			selfHealCalls++
			panic("ResyncClusterLabels must never consult SelfHealFn")
		},
	})

	result, err := r.ResyncClusterLabels(context.Background(), "test-cluster")
	if err != nil {
		t.Fatalf("ResyncClusterLabels: %v", err)
	}
	if selfHealCalls != 0 {
		t.Fatalf("SelfHealFn was consulted %d times — resync must ignore the self-heal setting entirely", selfHealCalls)
	}

	if result.Outcome != OutcomeSucceeded {
		t.Errorf("expected OutcomeSucceeded, got %v (msg=%q)", result.Outcome, result.Message)
	}

	// Reported diff: addon-foo added, addon-old removed, nothing changed.
	if len(result.Added) != 1 || result.Added[0] != "addon-foo" {
		t.Errorf("expected Added=['addon-foo'], got %v", result.Added)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "addon-old" {
		t.Errorf("expected Removed=['addon-old'], got %v", result.Removed)
	}
	if len(result.Changed) != 0 {
		t.Errorf("expected no Changed keys, got %v", result.Changed)
	}

	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "test-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated secret: %v", err)
	}

	// Labels now match git.
	if updated.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon-foo should be 'enabled' after resync, got %q", updated.Labels["addon-foo"])
	}
	if _, has := updated.Labels["addon-old"]; has {
		t.Error("addon-old should have been removed by resync (full convergence)")
	}

	// Ownership + foreign labels + Data + annotations untouched.
	if updated.Labels[LabelManagedBy] != LabelValueSharko {
		t.Errorf("managed-by label must survive, got %q", updated.Labels[LabelManagedBy])
	}
	if updated.Labels["example.com/team"] != "platform" {
		t.Errorf("foreign label must be untouched, got %q", updated.Labels["example.com/team"])
	}
	if updated.Annotations["user-annotation"] != "keep-this" {
		t.Error("annotations must be untouched")
	}
	if string(updated.Data["server"]) != "https://test.example.com" {
		t.Error("Data must be preserved")
	}

	// Drift is now gone.
	rec, ok := r.LastReconcile("test-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record after resync")
	}
	if rec.LabelDrift != nil {
		t.Errorf("expected no residual drift after resync, got %+v", rec.LabelDrift)
	}

	// Resyncing again reports "already matched" — Added/Removed/Changed
	// empty, and everything desired shows up as Unchanged.
	result2, err := r.ResyncClusterLabels(context.Background(), "test-cluster")
	if err != nil {
		t.Fatalf("second ResyncClusterLabels: %v", err)
	}
	if len(result2.Added) != 0 || len(result2.Removed) != 0 || len(result2.Changed) != 0 {
		t.Errorf("second resync should report no diff, got added=%v removed=%v changed=%v", result2.Added, result2.Removed, result2.Changed)
	}
	if len(result2.Unchanged) != 1 || result2.Unchanged[0] != "addon-foo" {
		t.Errorf("expected Unchanged=['addon-foo'], got %v", result2.Unchanged)
	}
}

// TestResyncClusterLabels_UnknownCluster_ReturnsErrClusterNotManaged pins
// acceptance criterion #4: a cluster absent from the git-managed list is
// reported distinctly, not silently treated as "nothing to do".
func TestResyncClusterLabels_UnknownCluster_ReturnsErrClusterNotManaged(t *testing.T) {
	client := fake.NewSimpleClientset()

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(`
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: other-cluster
      labels: {}
`),
	}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       staticVault(&fakeVault{}),
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
	})

	_, err := r.ResyncClusterLabels(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrClusterNotManaged) {
		t.Fatalf("expected ErrClusterNotManaged, got %v", err)
	}
}

// TestResyncClusterLabels_SelfManaged_SyncsWithoutTouchingConnectionData
// covers the self-managed (user-owned) connection branch: resync reuses
// syncSelfManaged, so connection Data/annotations stay untouched and only
// addon labels move.
func TestResyncClusterLabels_SelfManaged_SyncsWithoutTouchingConnectionData(t *testing.T) {
	client := fake.NewSimpleClientset()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "self-managed-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "self-managed-cluster",
			"server": "https://self.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(`
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
`),
	}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       staticVault(&fakeVault{}),
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
	})

	result, err := r.ResyncClusterLabels(context.Background(), "self-managed-cluster")
	if err != nil {
		t.Fatalf("ResyncClusterLabels: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Errorf("expected OutcomeSucceeded, got %v", result.Outcome)
	}

	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "self-managed-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated secret: %v", err)
	}
	if updated.Labels["addon-foo"] != "enabled" {
		t.Error("self-managed connection should have addon-foo synced")
	}
	// No managed-by ownership label ever stamped on a self-managed Secret.
	if _, has := updated.Labels[LabelManagedBy]; has {
		t.Error("resync must never stamp the managed-by label on a self-managed connection")
	}
}

// TestResyncClusterLabels_SelfManagedSecretNotYetCreated_SkipsCleanly
// verifies the "user hasn't created the Secret yet" branch is a clean skip,
// not an error.
func TestResyncClusterLabels_SelfManagedSecretNotYetCreated_SkipsCleanly(t *testing.T) {
	client := fake.NewSimpleClientset()

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(`
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
`),
	}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       staticVault(&fakeVault{}),
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
	})

	result, err := r.ResyncClusterLabels(context.Background(), "self-managed-cluster")
	if err != nil {
		t.Fatalf("ResyncClusterLabels: %v", err)
	}
	if result.Outcome != OutcomeSkipped {
		t.Errorf("expected OutcomeSkipped when the user hasn't created the Secret yet, got %v", result.Outcome)
	}
}

// TestResyncClusterLabels_ManagedSecretNeverExisted_SaysSoPlainly is task
// #117. A Sharko-managed cluster whose secret has never been created used
// to be told the secret "disappeared between drift detection and self-heal
// attempt" — a race that never happened, describing a moment that never
// was. Nothing disappeared; there is simply nothing there yet.
func TestResyncClusterLabels_ManagedSecretNeverExisted_SaysSoPlainly(t *testing.T) {
	client := fake.NewSimpleClientset()

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(`
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: never-created
      labels:
        addon-foo: enabled
`),
	}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       staticVault(&fakeVault{}),
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
	})

	result, err := r.ResyncClusterLabels(context.Background(), "never-created")
	if err != nil {
		t.Fatalf("ResyncClusterLabels: %v", err)
	}
	if result.Outcome != OutcomeSkipped {
		t.Errorf("outcome = %v, want skipped", result.Outcome)
	}
	if result.Message != ManagedSecretNotCreatedMessage {
		t.Errorf("message = %q, want %q", result.Message, ManagedSecretNotCreatedMessage)
	}
	if strings.Contains(result.Message, "disappeared") {
		t.Error("the message still claims the secret disappeared when it never existed")
	}
	rec, ok := r.LastReconcile("never-created")
	if !ok || rec.Message != ManagedSecretNotCreatedMessage {
		t.Errorf("record = %+v, ok=%v — want the same plain message on the row", rec, ok)
	}
}

// TestResyncClusterLabels_Adopted_ConvergesOnlySharkoKeys mirrors the
// self-heal adopted-cluster guarantee through the public entry point:
// resync on an adopted Secret converges only Sharko's addon keys, leaving
// the other owner's labels/Data/annotations untouched.
func TestResyncClusterLabels_Adopted_ConvergesOnlySharkoKeys(t *testing.T) {
	client := fake.NewSimpleClientset()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				"app.kubernetes.io/instance":     "some-other-app",
			},
			Annotations: map[string]string{
				"sharko.dev/adopted": "true",
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
	if _, err := client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(`
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
`),
	}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       staticVault(&fakeVault{}),
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
	})

	result, err := r.ResyncClusterLabels(context.Background(), "adopted-cluster")
	if err != nil {
		t.Fatalf("ResyncClusterLabels: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Errorf("expected OutcomeSucceeded, got %v (msg=%q)", result.Outcome, result.Message)
	}

	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "adopted-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated secret: %v", err)
	}
	if updated.Labels["addon-foo"] != "enabled" {
		t.Error("addon-foo should have been added on the adopted cluster")
	}
	if updated.Labels["app.kubernetes.io/instance"] != "some-other-app" {
		t.Error("the other owner's tracking label must be untouched")
	}
	if updated.Annotations["sharko.dev/adopted"] != "true" || updated.Annotations["keep-me"] != "yes" {
		t.Error("annotations must be untouched on an adopted cluster")
	}
}

// A taken-over cluster with no addons turned on is the ordinary state right
// after a takeover: the addon file is empty on purpose, and the connection
// still carries the previous owner's labels, recorded by key.
//
// Before this fix, "Re-sync now" on such a cluster reported Failed forever
// and listed the previous owner's labels as Removed — labels nothing had
// touched. This pins the honest answer: success, and no diff rows.
func TestResyncClusterLabels_TakenOverCluster_NoAddonsIsNotDrift(t *testing.T) {
	client := fake.NewSimpleClientset()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "brownfield-eu",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				"env":                            "prod",     // carried over
				"team":                           "platform", // carried over
			},
			Annotations: map[string]string{
				"sharko.dev/adopted":                   "true",
				"sharko.dev/takeover-preserved-labels": "env,team",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("brownfield-eu"),
			"server": []byte("https://brownfield-eu.example.com"),
			"config": []byte(`{"execProviderConfig":{}}`),
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	// In the fleet record with no addon labels at all — exactly what a
	// takeover writes.
	var gp fakeGit
	gp.files = map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(`
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: brownfield-eu
      secretPath: brownfield-eu
`),
	}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return &gp },
		ArgoClient:  client,
		Vault:       staticVault(&fakeVault{}),
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
	})

	result, err := r.ResyncClusterLabels(context.Background(), "brownfield-eu")
	if err != nil {
		t.Fatalf("ResyncClusterLabels: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Errorf("outcome = %v, want %v (msg=%q)", result.Outcome, OutcomeSucceeded, result.Message)
	}
	if len(result.Added) != 0 || len(result.Removed) != 0 || len(result.Changed) != 0 {
		t.Errorf("a taken-over cluster with no addons reported a diff: added=%v removed=%v changed=%v",
			result.Added, result.Removed, result.Changed)
	}

	rec, ok := r.LastReconcile("brownfield-eu")
	if !ok {
		t.Fatal("expected a LastReconcile record after resync")
	}
	if rec.LabelDrift != nil {
		t.Errorf("residual drift reported on a taken-over cluster: %+v", rec.LabelDrift)
	}

	updated, err := client.CoreV1().Secrets("argocd").Get(context.Background(), "brownfield-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated secret: %v", err)
	}
	if updated.Labels["env"] != "prod" || updated.Labels["team"] != "platform" {
		t.Errorf("the previous owner's labels were removed by the resync: %v", updated.Labels)
	}
}

// TestResyncClusterLabels_NoGitProvider_ReturnsError guards the
// precondition checks — no fake providers.ClusterCredentialsProvider
// dependency is needed since resync never fetches credentials, only labels.
func TestResyncClusterLabels_NoGitProvider_ReturnsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := New(Deps{
		ArgoClient: client,
		AuditFn:    func(audit.Entry) {},
		Namespace:  "argocd",
	})
	if _, err := r.ResyncClusterLabels(context.Background(), "any-cluster"); err == nil {
		t.Fatal("expected an error when no GitProvider is configured")
	}
}

var _ providers.ClusterCredentialsProvider = (*fakeVault)(nil)
