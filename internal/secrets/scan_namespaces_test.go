package secrets

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/providers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// scan_namespaces_test.go — the leftover-secret scan lists Secrets only in
// the namespaces Sharko could have written to, never across all namespaces,
// and still finds the secret of an addon that was removed from the catalog.

// listRecorder notes the namespace of every LIST of secrets a fake
// clientset receives. An empty string means "all namespaces".
type listRecorder struct {
	mu         sync.Mutex
	namespaces []string
}

func (l *listRecorder) seen() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.namespaces...)
}

// recordLists hooks a recorder onto the clientset. The reactor only notes
// the call and lets the fake answer it as usual.
func recordLists(c *fake.Clientset) *listRecorder {
	rec := &listRecorder{}
	c.PrependReactor("list", "secrets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		rec.mu.Lock()
		rec.namespaces = append(rec.namespaces, a.GetNamespace())
		rec.mu.Unlock()
		return false, nil, nil
	})
	return rec
}

// assertNoAllNamespaceList fails if any LIST of secrets went across all
// namespaces.
func assertNoAllNamespaceList(t *testing.T, where string, rec *listRecorder) {
	t.Helper()
	for _, ns := range rec.seen() {
		if ns == "" {
			t.Fatalf("%s: secrets were listed across ALL namespaces (namespaces seen: %q) — the scan must only list in named namespaces", where, rec.seen())
		}
	}
}

func sortedUnique(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// memNamespaceStore is an in-memory NamespaceRecordStore that can be told
// to fail, and counts loads and saves.
type memNamespaceStore struct {
	mu      sync.Mutex
	record  map[string][]string
	loadErr error
	saveErr error
	loads   int
	saves   int
}

func (s *memNamespaceStore) Load(context.Context) (map[string][]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	out := map[string][]string{}
	for k, v := range s.record {
		out[k] = append([]string(nil), v...)
	}
	return out, nil
}

func (s *memNamespaceStore) Save(_ context.Context, record map[string][]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.record = map[string][]string{}
	for k, v := range record {
		s.record[k] = append([]string(nil), v...)
	}
	return nil
}

// countingCredProvider counts GetCredentials calls.
type countingCredProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *countingCredProvider) GetCredentials(string) (*providers.Kubeconfig, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.Kubeconfig{Raw: []byte("fake-kubeconfig")}, nil
}
func (p *countingCredProvider) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (p *countingCredProvider) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (p *countingCredProvider) HealthCheck(context.Context) error              { return nil }

// redisInCatalogRepo is a v3 repo whose catalog still has redis, pushing
// old-redis-secret into "data", enabled on prod-cluster.
func redisInCatalogRepo() *mockGitReader {
	return &mockGitReader{files: map[string][]byte{
		"configuration/addons-catalog.yaml":   []byte(catalogWithRedisReclaim),
		"configuration/managed-clusters.yaml": []byte(clusterAddonsReclaimYAML),
	}}
}

// removeRedisFromCatalog changes the repo so redis is gone from the catalog
// entirely — nothing in the catalog names "data" any more.
func removeRedisFromCatalog(gr *mockGitReader) {
	gr.files["configuration/addons-catalog.yaml"] = []byte(catalogWithSecrets)
	gr.files["configuration/managed-clusters.yaml"] = []byte(clusterAddonsYAML)
}

// (a) A full reconcile pass and a CheckAll never list across all
// namespaces; they list in exactly the catalog's push namespaces plus the
// cluster's record.
func TestScanOrphans_NeverListsAcrossAllNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset(
		labeledSecret("monitoring", "datadog-secret", "datadog"),
		labeledSecret("data", "old-redis-secret", "redis"),
		labeledSecret("kube-system", "somebody-elses", "redis"),
	)
	rec := recordLists(client)
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)
	rememberNamespace(r, "prod-cluster", "data")

	r.reconcile()
	assertNoAllNamespaceList(t, "reconcile", rec)
	if got, want := sortedUnique(rec.seen()), []string{"data", "monitoring"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reconcile listed secrets in %q, want exactly %q", got, want)
	}

	if err := r.CheckAll(context.Background()); err != nil {
		t.Fatalf("CheckAll: %v", err)
	}
	assertNoAllNamespaceList(t, "CheckAll", rec)
	if got, want := sortedUnique(rec.seen()), []string{"data", "monitoring"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckAll listed secrets in %q, want exactly %q", got, want)
	}

	// The secret in a namespace no catalog ever named is not seen at all.
	if _, ok := findOrphan(r.OrphanedSecrets(), "prod-cluster", "somebody-elses"); ok {
		t.Fatal("a secret in a namespace Sharko never wrote to was reported — the scan looked outside its namespaces")
	}
	if _, ok := findOrphan(r.OrphanedSecrets(), "prod-cluster", "old-redis-secret"); !ok {
		t.Fatalf("the leftover in the remembered namespace was not found: %+v", r.OrphanedSecrets())
	}
}

// (b) The real flow: redis is in the catalog and its secret is on the
// cluster (claimed, not a leftover). Then redis is removed from the catalog.
// The next pass must still look in "data" and report the secret.
func TestScanOrphans_AddonRemovedFromCatalog_LeftoverStillFound(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	rec := recordLists(client)
	gr := redisInCatalogRepo()
	r := scanReconciler(gr, client)

	planAndScan(t, r)
	if got := r.OrphanedSecrets(); len(got) != 0 {
		t.Fatalf("pass 1: OrphanedSecrets() = %+v, want none — redis is still in the catalog and claims this secret", got)
	}

	removeRedisFromCatalog(gr)
	before := len(rec.seen())
	planAndScan(t, r)

	pass2 := sortedUnique(rec.seen()[before:])
	if want := []string{"data", "monitoring"}; !reflect.DeepEqual(pass2, want) {
		t.Fatalf("pass 2 listed secrets in %q, want %q — \"data\" must still be scanned after redis left the catalog", pass2, want)
	}
	assertNoAllNamespaceList(t, "pass 2", rec)

	got := r.OrphanedSecrets()
	if len(got) != 1 {
		t.Fatalf("pass 2: OrphanedSecrets() = %+v, want exactly the removed addon's secret", got)
	}
	o := got[0]
	if o.Cluster != "prod-cluster" || o.Namespace != "data" || o.Name != "old-redis-secret" || o.Addon != "redis" {
		t.Errorf("orphan = %+v, want cluster=prod-cluster namespace=data name=old-redis-secret addon=redis", o)
	}

	// And it keeps being found on later passes, as long as it is there.
	planAndScan(t, r)
	if _, ok := findOrphan(r.OrphanedSecrets(), "prod-cluster", "old-redis-secret"); !ok {
		t.Fatalf("pass 3: the leftover disappeared from the report while still on the cluster: %+v", r.OrphanedSecrets())
	}
}

// The record survives a restart through the store: a NEW reconciler sharing
// the same store still looks in "data" after redis left the catalog.
func TestScanOrphans_AddonRemovedFromCatalog_FoundAfterRestart(t *testing.T) {
	store := &memNamespaceStore{}
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	gr := redisInCatalogRepo()

	first := scanReconciler(gr, client)
	first.SetNamespaceRecordStore(store)
	planAndScan(t, first)
	if got := store.record["prod-cluster"]; !reflect.DeepEqual(got, []string{"data", "monitoring"}) {
		t.Fatalf("saved record for prod-cluster = %q, want [data monitoring]", got)
	}

	removeRedisFromCatalog(gr)

	restarted := scanReconciler(gr, client)
	restarted.SetNamespaceRecordStore(store)
	rec := recordLists(client)
	planAndScan(t, restarted)

	assertNoAllNamespaceList(t, "after restart", rec)
	if _, ok := findOrphan(restarted.OrphanedSecrets(), "prod-cluster", "old-redis-secret"); !ok {
		t.Fatalf("after a restart the removed addon's leftover was not found: %+v (lists: %q)", restarted.OrphanedSecrets(), rec.seen())
	}
}

// Without a store, a restart forgets the record. This pins the limit the
// docs describe, so nobody claims more than the code does.
func TestScanOrphans_NoStore_RestartForgetsRemovedAddonNamespace(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	gr := redisInCatalogRepo()
	planAndScan(t, scanReconciler(gr, client))

	removeRedisFromCatalog(gr)
	restarted := scanReconciler(gr, client)
	planAndScan(t, restarted)
	if got := restarted.OrphanedSecrets(); len(got) != 0 {
		t.Fatalf("OrphanedSecrets() = %+v, want none — with no store the record is in memory only", got)
	}
}

// A namespace leaves the record only when the catalog no longer names it
// AND a list there worked AND found no Sharko addon secret.
func TestScanOrphans_RecordDropsANamespaceOnlyWhenItIsEmptyAndOutOfTheCatalog(t *testing.T) {
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	gr := redisInCatalogRepo()
	r := scanReconciler(gr, client)
	planAndScan(t, r)
	removeRedisFromCatalog(gr)

	// Still holds a secret: stays.
	planAndScan(t, r)
	if got := r.KnownSecretNamespaces("prod-cluster"); !reflect.DeepEqual(got, []string{"data", "monitoring"}) {
		t.Fatalf("KnownSecretNamespaces = %q, want [data monitoring] while the leftover is still there", got)
	}

	// The list fails: stays.
	if err := client.Tracker().Delete(secretsGVR, "data", "old-redis-secret"); err != nil {
		t.Fatal(err)
	}
	failing := fake.NewSimpleClientset()
	injectListFailure(failing)
	r.remoteClientFn = fakeRemoteClientFn(failing)
	planAndScan(t, r)
	if got := r.KnownSecretNamespaces("prod-cluster"); !reflect.DeepEqual(got, []string{"data", "monitoring"}) {
		t.Fatalf("KnownSecretNamespaces = %q, want [data monitoring] — a failed list proves nothing", got)
	}

	// The list works and finds nothing: "data" goes. "monitoring" is still
	// in the catalog, so it stays even though it is empty too.
	r.remoteClientFn = fakeRemoteClientFn(client)
	planAndScan(t, r)
	if got := r.KnownSecretNamespaces("prod-cluster"); !reflect.DeepEqual(got, []string{"monitoring"}) {
		t.Fatalf("KnownSecretNamespaces = %q, want [monitoring]", got)
	}
}

// A cluster with no namespace to look in is not contacted at all.
func TestScanOrphans_NoNamespaces_ClusterNotContacted(t *testing.T) {
	creds := &countingCredProvider{}
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	r := newReconciler(standardGitReader(catalogWithoutSecrets), creds, &mockSecretProvider{}, fakeRemoteClientFn(client))

	planAndScan(t, r)

	if creds.calls != 0 {
		t.Fatalf("credentials were fetched %d times for a cluster with no namespace to scan", creds.calls)
	}
	if n := len(client.Actions()); n != 0 {
		t.Fatalf("the cluster got %d API calls, want none: %+v", n, client.Actions())
	}
}

// One namespace failing keeps only that namespace's previous records; the
// others get fresh results.
func TestScanOrphans_OneNamespaceFails_OnlyItKeepsPreviousRecords(t *testing.T) {
	client := fake.NewSimpleClientset(
		labeledSecret("data", "old-redis-secret", "redis"),
		labeledSecret("monitoring", "datadog-secret", "datadog"),
	)
	gr := &mockGitReader{files: map[string][]byte{
		"configuration/addons-catalog.yaml":   []byte(catalogWithSecrets),
		"configuration/managed-clusters.yaml": []byte(clusterAddonsNoMatch), // datadog off: its secret is a leftover
	}}
	r := scanReconciler(gr, client)
	rememberNamespace(r, "prod-cluster", "data")
	planAndScan(t, r)
	if n := len(r.OrphanedSecrets()); n != 2 {
		t.Fatalf("pass 1: %d leftovers, want 2: %+v", n, r.OrphanedSecrets())
	}
	oldData, _ := findOrphan(r.OrphanedSecrets(), "prod-cluster", "old-redis-secret")

	// "data" now fails to list; datadog's leftover is removed by hand.
	client.PrependReactor("list", "secrets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "data" {
			return true, nil, errors.New("forbidden")
		}
		return false, nil, nil
	})
	if err := client.Tracker().Delete(secretsGVR, "monitoring", "datadog-secret"); err != nil {
		t.Fatal(err)
	}
	planAndScan(t, r)

	got := r.OrphanedSecrets()
	kept, ok := findOrphan(got, "prod-cluster", "old-redis-secret")
	if !ok || !kept.LastChecked.Equal(oldData.LastChecked) {
		t.Fatalf("the namespace that failed did not keep its previous record unchanged: %+v", got)
	}
	if _, ok := findOrphan(got, "prod-cluster", "datadog-secret"); ok {
		t.Fatalf("the namespace that worked kept a stale record: %+v", got)
	}
}

// (c) On a v4 repo the scan uses the clusters in the root
// managed-clusters.yaml — and only those.
func TestScanOrphans_V4Repo_ScansTheClustersInManagedClusters(t *testing.T) {
	prodAssign, err := config.V4ClusterAddonsPath("prod-cluster")
	if err != nil {
		t.Fatal(err)
	}
	stagingAssign, err := config.V4ClusterAddonsPath("staging-cluster")
	if err != nil {
		t.Fatal(err)
	}
	ghostAssign, err := config.V4ClusterAddonsPath("ghost-cluster")
	if err != nil {
		t.Fatal(err)
	}
	repo := &v4GitReader{files: map[string][]byte{
		config.AddonCatalogPath: v4Catalog(t, []config.AddonSecretRequirement{v4PushRequirement()}),
		config.V4ManagedClustersPath: []byte(`apiVersion: sharko.io/v1
kind: ManagedClusters
clusters:
  - name: prod-cluster
  - name: staging-cluster
`),
		prodAssign:    v4Assignment(t, "prod-cluster", map[string]bool{"datadog": true}),
		stagingAssign: v4Assignment(t, "staging-cluster", map[string]bool{"datadog": false}),
		// An assignment file for a cluster that is NOT in managed-clusters.yaml.
		ghostAssign: v4Assignment(t, "ghost-cluster", map[string]bool{"datadog": false}),
	}}

	prod := fake.NewSimpleClientset(labeledSecret("monitoring", "datadog-secret", "datadog"))
	staging := fake.NewSimpleClientset(labeledSecret("monitoring", "datadog-secret", "datadog"))
	ghost := fake.NewSimpleClientset(labeledSecret("monitoring", "datadog-secret", "datadog"))
	prodRec, stagingRec := recordLists(prod), recordLists(staging)

	r := newReconciler(repo, &perClusterCredProvider{}, &mockSecretProvider{}, perClusterClientFn(map[string]kubernetes.Interface{
		"prod-cluster":    prod,
		"staging-cluster": staging,
		"ghost-cluster":   ghost,
	}))
	planAndScan(t, r)

	if got := prodRec.seen(); !reflect.DeepEqual(got, []string{"monitoring"}) {
		t.Errorf("prod-cluster lists = %q, want [monitoring]", got)
	}
	if got := stagingRec.seen(); !reflect.DeepEqual(got, []string{"monitoring"}) {
		t.Errorf("staging-cluster lists = %q, want [monitoring]", got)
	}
	if n := len(ghost.Actions()); n != 0 {
		t.Errorf("ghost-cluster (not in managed-clusters.yaml) got %d API calls, want none", n)
	}

	got := r.OrphanedSecrets()
	if len(got) != 1 {
		t.Fatalf("OrphanedSecrets() = %+v, want exactly staging-cluster's leftover", got)
	}
	if o := got[0]; o.Cluster != "staging-cluster" || o.Namespace != "monitoring" || o.Name != "datadog-secret" || o.Addon != "datadog" {
		t.Errorf("orphan = %+v, want staging-cluster/monitoring/datadog-secret (datadog is off there)", o)
	}
}

// (d) A v3 repo still wins over v4 files and keeps its own cluster list:
// clusters come from configuration/managed-clusters.yaml, not the root one.
func TestScanOrphans_V3Repo_Unchanged(t *testing.T) {
	prod := fake.NewSimpleClientset(labeledSecret("monitoring", "datadog-secret", "datadog"))
	v4Only := fake.NewSimpleClientset(labeledSecret("monitoring", "datadog-secret", "datadog"))
	prodRec := recordLists(prod)
	gr := &mockGitReader{files: map[string][]byte{
		"configuration/addons-catalog.yaml":   []byte(catalogWithSecrets),
		"configuration/managed-clusters.yaml": []byte(clusterAddonsNoMatch),
		config.V4ManagedClustersPath: []byte(`apiVersion: sharko.io/v1
kind: ManagedClusters
clusters:
  - name: v4-only-cluster
`),
	}}
	r := newReconciler(gr, &perClusterCredProvider{}, &mockSecretProvider{}, perClusterClientFn(map[string]kubernetes.Interface{
		"prod-cluster":    prod,
		"v4-only-cluster": v4Only,
	}))
	planAndScan(t, r)

	if got := prodRec.seen(); !reflect.DeepEqual(got, []string{"monitoring"}) {
		t.Errorf("prod-cluster lists = %q, want [monitoring]", got)
	}
	if n := len(v4Only.Actions()); n != 0 {
		t.Errorf("a cluster only in the v4 file was contacted on a v3 repo (%d calls)", n)
	}
	got := r.OrphanedSecrets()
	if len(got) != 1 || got[0].Cluster != "prod-cluster" || got[0].Name != "datadog-secret" {
		t.Fatalf("OrphanedSecrets() = %+v, want prod-cluster's disabled datadog secret, as before", got)
	}
}

// Nothing is saved until the stored record could be read, so a partial
// in-memory record never overwrites a fuller stored one.
func TestNamespaceRecord_NoSaveBeforeALoadWorks(t *testing.T) {
	store := &memNamespaceStore{
		record:  map[string][]string{"prod-cluster": {"data"}},
		loadErr: errors.New("configmap read failed"),
	}
	client := fake.NewSimpleClientset(labeledSecret("data", "old-redis-secret", "redis"))
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)
	r.SetNamespaceRecordStore(store)

	planAndScan(t, r)
	if store.saves != 0 {
		t.Fatalf("saved %d times while the stored record could not be read", store.saves)
	}

	store.loadErr = nil
	planAndScan(t, r)
	if store.saves != 1 {
		t.Fatalf("saves = %d after the load worked, want 1", store.saves)
	}
	if got := store.record["prod-cluster"]; !reflect.DeepEqual(got, []string{"data", "monitoring"}) {
		t.Fatalf("saved record = %q, want the stored \"data\" kept and \"monitoring\" added", got)
	}
	if _, ok := findOrphan(r.OrphanedSecrets(), "prod-cluster", "old-redis-secret"); !ok {
		t.Fatalf("the stored namespace was not scanned once the load worked: %+v", r.OrphanedSecrets())
	}
}

// A failed save is tried again on a later pass; an unchanged record is not
// saved again.
func TestNamespaceRecord_FailedSaveIsRetried_UnchangedIsNotRewritten(t *testing.T) {
	store := &memNamespaceStore{saveErr: errors.New("configmap write failed")}
	client := fake.NewSimpleClientset()
	r := scanReconciler(standardGitReader(catalogWithSecrets), client)
	r.SetNamespaceRecordStore(store)

	planAndScan(t, r)
	if store.saves != 1 || store.record != nil {
		t.Fatalf("pass 1: saves=%d record=%v, want one failed attempt", store.saves, store.record)
	}

	store.saveErr = nil
	planAndScan(t, r)
	if store.saves != 2 || !reflect.DeepEqual(store.record["prod-cluster"], []string{"monitoring"}) {
		t.Fatalf("pass 2: saves=%d record=%v, want the retry to save [monitoring]", store.saves, store.record)
	}

	planAndScan(t, r)
	if store.saves != 2 {
		t.Fatalf("pass 3: saves=%d, want no new save when nothing changed", store.saves)
	}
}

// The production store keeps the record in a hub ConfigMap and reads back
// exactly what it wrote; a missing ConfigMap reads as an empty record.
func TestConfigMapNamespaceStore_RoundTrip(t *testing.T) {
	ctx := context.Background()
	hub := fake.NewSimpleClientset()
	store := NewConfigMapNamespaceStore(cmstore.NewStore(hub, "sharko", NamespaceRecordConfigMapName))

	got, err := store.Load(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("Load on a missing ConfigMap = %v, %v; want an empty record and no error", got, err)
	}

	want := map[string][]string{"prod-cluster": {"data", "monitoring"}, "staging-cluster": {"monitoring"}}
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := hub.CoreV1().ConfigMaps("sharko").Get(ctx, NamespaceRecordConfigMapName, metav1.GetOptions{}); err != nil {
		t.Fatalf("the ConfigMap %q was not created in the release namespace: %v", NamespaceRecordConfigMapName, err)
	}
	got, err = store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %v, want %v", got, want)
	}
}
