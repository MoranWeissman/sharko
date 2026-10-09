package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// orphans_test.go — leftover-secrets S1. Fixtures mostly reuse
// reconciler_test.go's catalog/cluster YAML (datadog addon pushing
// "datadog-secret" in namespace "monitoring" to "prod-cluster").

const managedByLabelKey = "app.kubernetes.io/managed-by"
const managedByLabelValue = "sharko"

// labeledSecret builds a Sharko-managed Secret — carries the managed-by
// label, and the addon provenance annotation only when addon != "".
func labeledSecret(namespace, name, addon string) *corev1.Secret {
	meta := metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    map[string]string{managedByLabelKey: managedByLabelValue},
	}
	if addon != "" {
		meta.Annotations = map[string]string{remoteclient.AnnotationAddon: addon}
	}
	return &corev1.Secret{ObjectMeta: meta, Type: corev1.SecretTypeOpaque}
}

// scanReconciler builds a Reconciler over the given catalog/cluster YAML,
// wired to a single fake clientset for every cluster credLookup asks for —
// enough for every single-cluster orphan test below.
func scanReconciler(gitReader GitReader, client kubernetes.Interface) *Reconciler {
	return newReconciler(
		gitReader,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)
}

// planAndScan runs planPushes then scanOrphans directly — the same two
// calls reconcile()/CheckAll() make, without needing a full reconcile pass
// (and its secretProvider/push-write side effects) for tests that only care
// about the scan itself.
func planAndScan(t *testing.T, r *Reconciler) {
	t.Helper()
	ctx := context.Background()
	gr := r.gitReader()
	if gr == nil {
		t.Fatal("gitReader() returned nil")
	}
	plan, err := r.planPushes(ctx, gr)
	if err != nil {
		t.Fatalf("planPushes: %v", err)
	}
	r.scanOrphans(ctx, plan)
}

// TestScanOrphans_ReportsUnclaimedLabeledAndAnnotatedSecret is locked
// decision test #1: a live secret carrying BOTH the managed-by label AND
// the addon provenance annotation, that nothing in the plan claims, is
// reported — with the addon name read straight off the annotation.
func TestScanOrphans_ReportsUnclaimedLabeledAndAnnotatedSecret(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)

	planAndScan(t, r)

	got := r.OrphanedSecrets()
	if len(got) != 1 {
		t.Fatalf("OrphanedSecrets() = %+v, want exactly 1 orphan", got)
	}
	o := got[0]
	if o.Cluster != "prod-cluster" || o.Namespace != "data" || o.Name != "old-redis-secret" || o.Addon != "redis" {
		t.Errorf("orphan = %+v, want cluster=prod-cluster namespace=data name=old-redis-secret addon=redis", o)
	}
	if o.LastChecked.IsZero() {
		t.Error("LastChecked was never stamped")
	}
}

// TestScanOrphans_LabelWithoutAddonAnnotationNeverReported is locked
// decision test #2 — the kubeconfig-secret / ArgoCD-connection-secret
// safety pin. A Secret with the managed-by label but NO addon annotation
// (exactly what a cluster-registration kubeconfig secret or an ArgoCD
// connection secret looks like) must never be offered for deletion.
func TestScanOrphans_LabelWithoutAddonAnnotationNeverReported(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("argocd", "cluster-kubeconfig", ""))
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)

	planAndScan(t, r)

	if got := r.OrphanedSecrets(); len(got) != 0 {
		t.Fatalf("OrphanedSecrets() = %+v, want none — a managed-by label alone must never be treated as an orphan candidate", got)
	}
}

// TestScanOrphans_ClaimedSecretNeverReported is locked decision test #3: a
// labeled+annotated secret that IS still claimed by the plan (the addon's
// push definition still asks for exactly this namespace/name on this
// cluster) is not an orphan.
func TestScanOrphans_ClaimedSecretNeverReported(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("monitoring", "datadog-secret", "datadog"))
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)

	planAndScan(t, r)

	if got := r.OrphanedSecrets(); len(got) != 0 {
		t.Fatalf("OrphanedSecrets() = %+v, want none — this secret is still claimed by the plan", got)
	}
}

// TestScanOrphans_ZeroPushDefinitions_StillScansAndFindsOrphans is locked
// decision test #4 — the hand-deleted-catalog-entry case. A catalog with NO
// push definitions at all must still read the cluster list and scan every
// cluster for leftovers; this is precisely the scenario orphan detection
// exists for.
func TestScanOrphans_ZeroPushDefinitions_StillScansAndFindsOrphans(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	r := scanReconciler(standardGitReader(catalogWithoutSecrets), client)

	ctx := context.Background()
	plan, err := r.planPushes(ctx, r.gitReader())
	if err != nil {
		t.Fatalf("planPushes: %v", err)
	}
	if len(plan.work) != 0 {
		t.Fatalf("plan.work = %+v, want empty (catalogWithoutSecrets defines no push)", plan.work)
	}
	if len(plan.clusters) == 0 {
		t.Fatal("plan.clusters is empty — the cluster list must be read even with zero push definitions")
	}

	r.scanOrphans(ctx, plan)

	got := r.OrphanedSecrets()
	if len(got) != 1 || got[0].Name != "old-redis-secret" {
		t.Fatalf("OrphanedSecrets() = %+v, want the seeded orphan found even with zero push definitions", got)
	}
}

// perClusterCredProvider hands back a distinct kubeconfig per credLookup key
// (so perClusterClientFn below can route to a distinct fake clientset per
// cluster), and can be told to fail for a specific key — simulating a
// cluster whose credentials cannot be fetched at all.
type perClusterCredProvider struct {
	failFor string
}

func (p *perClusterCredProvider) GetCredentials(lookup string) (*providers.Kubeconfig, error) {
	if lookup == p.failFor {
		return nil, errors.New("credentials not found")
	}
	return &providers.Kubeconfig{Raw: []byte(lookup)}, nil
}
func (p *perClusterCredProvider) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (p *perClusterCredProvider) SearchSecrets(string) ([]string, error)        { return nil, nil }
func (p *perClusterCredProvider) HealthCheck(context.Context) error             { return nil }

// perClusterClientFn routes each kubeconfig (built as []byte(credLookup) by
// perClusterCredProvider above) to its own fake clientset, keyed by cluster
// name.
func perClusterClientFn(clients map[string]kubernetes.Interface) RemoteClientFactory {
	return func(kubeconfig []byte) (kubernetes.Interface, error) {
		c, ok := clients[string(kubeconfig)]
		if !ok {
			return nil, errors.New("no fake client configured for this cluster")
		}
		return c, nil
	}
}

// injectListFailure makes every LIST of secrets against this fake clientset
// fail, so a test can simulate "the scan reached this cluster but the LIST
// call itself failed" precisely, distinct from a credentials/connect
// failure.
func injectListFailure(client *fake.Clientset) {
	client.PrependReactor("list", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("list secrets: connection refused")
	})
}

const twoClusterYAML = `
clusters:
  - name: prod-cluster
    labels:
      nginx: enabled
  - name: staging-cluster
    labels:
      nginx: enabled
`

// findOrphan is a small assertion helper: find one orphan record by cluster
// + name in a snapshot slice.
func findOrphan(list []models.OrphanedSecret, cluster, name string) (models.OrphanedSecret, bool) {
	for _, o := range list {
		if o.Cluster == cluster && o.Name == name {
			return o, true
		}
	}
	return models.OrphanedSecret{}, false
}

func twoClusterGitReader() *mockGitReader {
	return &mockGitReader{
		files: map[string][]byte{
			"configuration/addons-catalog.yaml":   []byte(catalogWithoutSecrets),
			"configuration/managed-clusters.yaml": []byte(twoClusterYAML),
		},
	}
}

// TestScanOrphans_OneClusterFails_OthersStillScanned_PreviousRecordsKept is
// locked decision test #5 / #10 — per-cluster isolation. A cluster whose
// LIST call fails this pass must carry its PREVIOUS orphan records forward
// unchanged (not wiped, not fabricated), and must not stop any other
// cluster's scan.
func TestScanOrphans_OneClusterFails_OthersStillScanned_PreviousRecordsKept(t *testing.T) {
	prodClient := fake.NewSimpleClientset(labeledSecret("data", "prod-orphan", "redis"))
	stagingClient := fake.NewSimpleClientset(labeledSecret("data", "staging-orphan", "redis"))

	credProvider := &perClusterCredProvider{}
	clientFn := perClusterClientFn(map[string]kubernetes.Interface{
		"prod-cluster":    prodClient,
		"staging-cluster": stagingClient,
	})

	r := newReconciler(twoClusterGitReader(), credProvider, &mockSecretProvider{}, clientFn)

	// Pass 1: both clusters healthy — both get real records.
	planAndScan(t, r)
	first := r.OrphanedSecrets()
	if len(first) != 2 {
		t.Fatalf("pass 1: OrphanedSecrets() = %+v, want 2 (one per cluster)", first)
	}
	staleStagingRecord, ok := findOrphan(first, "staging-cluster", "staging-orphan")
	if !ok {
		t.Fatalf("pass 1: staging-cluster's orphan is missing: %+v", first)
	}

	// Pass 2: staging-cluster's LIST call now fails.
	injectListFailure(stagingClient)
	planAndScan(t, r)

	second := r.OrphanedSecrets()
	prodAfter, ok := findOrphan(second, "prod-cluster", "prod-orphan")
	if !ok {
		t.Fatalf("pass 2: prod-cluster's orphan disappeared even though only staging-cluster's LIST failed: %+v", second)
	}
	_ = prodAfter

	stagingAfter, ok := findOrphan(second, "staging-cluster", "staging-orphan")
	if !ok {
		t.Fatalf("pass 2: staging-cluster's PREVIOUS record was dropped instead of carried forward: %+v", second)
	}
	if !stagingAfter.LastChecked.Equal(staleStagingRecord.LastChecked) {
		t.Errorf("pass 2: staging-cluster's LastChecked moved (%v -> %v) even though this pass could not actually check it",
			staleStagingRecord.LastChecked, stagingAfter.LastChecked)
	}
}

// ---------------------------------------------------------------------------
// DeleteOrphanedSecret
// ---------------------------------------------------------------------------

const catalogWithRedisReclaim = `
applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: "3.50.0"
    namespace: monitoring
    secrets:
      - secretName: datadog-secret
        namespace: monitoring
        keys:
          api-key: "secrets/datadog/api-key"
          app-key: "secrets/datadog/app-key"
  - name: redis
    repoURL: https://charts.example.com
    chart: redis
    version: "1.0.0"
    namespace: data
    secrets:
      - secretName: old-redis-secret
        namespace: data
        keys:
          password: "secrets/redis/password"
`

const clusterAddonsReclaimYAML = `
clusters:
  - name: prod-cluster
    labels:
      datadog: enabled
      redis: enabled
`

// TestDeleteOrphanedSecret_HappyPath deletes a genuine, still-on-record
// orphan and removes it from the in-memory map immediately (locked
// decision #8) so the row disappears on the next list read.
func TestDeleteOrphanedSecret_HappyPath(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)
	planAndScan(t, r)

	if err := r.DeleteOrphanedSecret(context.Background(), "prod-cluster", "data", "old-redis-secret"); err != nil {
		t.Fatalf("DeleteOrphanedSecret: %v", err)
	}

	if _, getErr := client.CoreV1().Secrets("data").Get(context.Background(), "old-redis-secret", metav1.GetOptions{}); !apierrors.IsNotFound(getErr) {
		t.Fatalf("expected the secret to be deleted, get err = %v", getErr)
	}
	if got := r.OrphanedSecrets(); len(got) != 0 {
		t.Fatalf("OrphanedSecrets() = %+v, want the record removed after a successful delete", got)
	}
}

// TestDeleteOrphanedSecret_UnknownRecordRefuses is locked decision test #6a
// — a (cluster, namespace, name) that was never scanned as an orphan is
// refused outright, before anything reaches Kubernetes.
func TestDeleteOrphanedSecret_UnknownRecordRefuses(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)

	err := r.DeleteOrphanedSecret(context.Background(), "prod-cluster", "data", "old-redis-secret")
	if !errors.Is(err, ErrOrphanUnknown) {
		t.Fatalf("err = %v, want ErrOrphanUnknown", err)
	}
}

// TestDeleteOrphanedSecret_ReclaimedByFreshPlanRefuses is locked decision
// test #6b — between the scan that found this orphan and the delete click,
// the catalog changed to claim it again (the addon was re-added). The FRESH
// re-read at delete time must catch that and refuse, never the stale scan
// record.
func TestDeleteOrphanedSecret_ReclaimedByFreshPlanRefuses(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	gr := standardGitReader(catalogWithSecrets)
	r := scanReconciler(gr, client)
	planAndScan(t, r)

	if _, ok := findOrphan(r.OrphanedSecrets(), "prod-cluster", "old-redis-secret"); !ok {
		t.Fatal("setup: expected the orphan to be on record before reclaiming it")
	}

	// The catalog changes underneath — redis is now a real push definition
	// claiming exactly this secret.
	gr.files["configuration/addons-catalog.yaml"] = []byte(catalogWithRedisReclaim)
	gr.files["configuration/managed-clusters.yaml"] = []byte(clusterAddonsReclaimYAML)

	err := r.DeleteOrphanedSecret(context.Background(), "prod-cluster", "data", "old-redis-secret")
	if !errors.Is(err, ErrOrphanReclaimed) {
		t.Fatalf("err = %v, want ErrOrphanReclaimed", err)
	}
	if _, getErr := client.CoreV1().Secrets("data").Get(context.Background(), "old-redis-secret", metav1.GetOptions{}); getErr != nil {
		t.Fatalf("the secret was deleted despite being reclaimed: %v", getErr)
	}
}

// TestDeleteOrphanedSecret_AnnotationStrippedRefuses is locked decision
// test #6c — the live secret's provenance annotation is gone by the time
// the delete reaches Kubernetes (checked via a fresh Get, before the
// delete call itself).
func TestDeleteOrphanedSecret_AnnotationStrippedRefuses(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)
	planAndScan(t, r)

	live, err := client.CoreV1().Secrets("data").Get(context.Background(), "old-redis-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	live.Annotations = nil
	if _, err := client.CoreV1().Secrets("data").Update(context.Background(), live, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	err = r.DeleteOrphanedSecret(context.Background(), "prod-cluster", "data", "old-redis-secret")
	if !errors.Is(err, ErrOrphanRefused) {
		t.Fatalf("err = %v, want ErrOrphanRefused", err)
	}
	if _, getErr := client.CoreV1().Secrets("data").Get(context.Background(), "old-redis-secret", metav1.GetOptions{}); getErr != nil {
		t.Fatalf("the secret was deleted despite the missing annotation: %v", getErr)
	}
}

// TestDeleteOrphanedSecret_LabelStrippedRefuses is locked decision test
// #6d — the managed-by label is gone, so remoteclient.DeleteSecretIfManaged
// itself refuses (ErrForeignSecret), mapped to ErrOrphanRefused.
func TestDeleteOrphanedSecret_LabelStrippedRefuses(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)
	planAndScan(t, r)

	live, err := client.CoreV1().Secrets("data").Get(context.Background(), "old-redis-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(live.Labels, managedByLabelKey)
	if _, err := client.CoreV1().Secrets("data").Update(context.Background(), live, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	err = r.DeleteOrphanedSecret(context.Background(), "prod-cluster", "data", "old-redis-secret")
	if !errors.Is(err, ErrOrphanRefused) {
		t.Fatalf("err = %v, want ErrOrphanRefused", err)
	}
	if _, getErr := client.CoreV1().Secrets("data").Get(context.Background(), "old-redis-secret", metav1.GetOptions{}); getErr != nil {
		t.Fatalf("the secret was deleted despite the stripped label: %v", getErr)
	}
}
