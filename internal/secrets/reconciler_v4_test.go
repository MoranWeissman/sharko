package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// These tests are the internal/secrets half of the v4 wave 2.5 review's
// finding F1: on a repo that migrated to the v4 layout, the reconciler read
// only the v3 file paths — which migration deletes — so it warn-logged,
// returned before writing any status, and pushed nothing. Workloads kept
// the credentials they already had until the first rotation and then went
// stale, with nothing in Sharko pointing at why.
//
// What they pin: a v4 repo pushes, skips and rotates exactly as the v3
// tests prove for v3, and anything that stops the reconciler reading the
// repo shows up in the status.

// ---- v4 fixtures ----

// v4PushRequirement is the datadog secret used across these tests: the same
// Secret, namespace and provider paths the v3 fixture (catalogWithSecrets)
// uses, so "v4 behaves like v3" is a real comparison and not two different
// scenarios that happen to both pass.
func v4PushRequirement() config.AddonSecretRequirement {
	return config.AddonSecretRequirement{
		Name:        "datadog-secret",
		Description: "Sharko creates this Secret on every cluster running datadog.",
		Push: &models.AddonSecretRef{
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys: map[string]string{
				"api-key": "secrets/datadog/api-key",
				"app-key": "secrets/datadog/app-key",
			},
		},
	}
}

// v4Catalog renders catalog.yaml through the SAME writer the migration and
// the add-to-catalog operation use, so this fixture cannot drift away from
// what Sharko actually commits.
func v4Catalog(t *testing.T, secrets []config.AddonSecretRequirement) []byte {
	t.Helper()
	body, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"datadog": {
				RepoURL:   "https://helm.datadoghq.com",
				Chart:     "datadog",
				Version:   "3.50.0",
				Namespace: "monitoring",
				Secrets:   secrets,
			},
		},
	})
	if err != nil {
		t.Fatalf("writing the v4 catalog fixture: %v", err)
	}
	return body
}

// v4Assignment renders one cluster-addons/<name>.yaml — which addons that cluster
// runs — through the real writer.
func v4Assignment(t *testing.T, cluster string, addons map[string]bool) []byte {
	t.Helper()
	spec := models.ClusterAddonsSpec{Cluster: cluster, Addons: map[string]models.ClusterAddonsAddon{}}
	for name, enabled := range addons {
		spec.Addons[name] = models.ClusterAddonsAddon{Enabled: enabled}
	}
	body, err := models.SaveClusterAddons(spec)
	if err != nil {
		t.Fatalf("writing the cluster assignment fixture for %s: %v", cluster, err)
	}
	return body
}

const v4ManagedClustersYAML = `apiVersion: sharko.io/v1
kind: ManagedClusters
clusters:
  - name: prod-cluster
`

// v4GitReader is a whole v4 repo: catalog.yaml, the root
// managed-clusters.yaml, and one assignment file per cluster. A path that
// is not in the map comes back as gitprovider.ErrFileNotFound, which is
// what the real providers return and what "this cluster has no addons yet"
// looks like.
type v4GitReader struct {
	files map[string][]byte
	// failPaths returns a non-not-found error for a path — a token
	// problem, a rate limit, a network blip.
	failPaths map[string]error
}

func (m *v4GitReader) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if err, ok := m.failPaths[path]; ok {
		return nil, err
	}
	data, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("get file content: path %q: %w", path, gitprovider.ErrFileNotFound)
	}
	return data, nil
}

func v4Repo(t *testing.T, secrets []config.AddonSecretRequirement, enabled bool) *v4GitReader {
	t.Helper()
	assignPath, err := config.V4ClusterAddonsPath("prod-cluster")
	if err != nil {
		t.Fatalf("building the assignment path: %v", err)
	}
	return &v4GitReader{files: map[string][]byte{
		config.AddonCatalogPath:      v4Catalog(t, secrets),
		config.V4ManagedClustersPath: []byte(v4ManagedClustersYAML),
		assignPath:                   v4Assignment(t, "prod-cluster", map[string]bool{"datadog": enabled}),
	}}
}

// ---- tests ----

// TestReconcileV4_CreatesSkipsAndRotates walks a migrated repo through the
// full life of a credential: first push, no-op when nothing changed, and a
// real update when the value behind the provider path is rotated. That last
// one is the moment F1 was about — before this fix, it never happened on a
// v4 repo.
func TestReconcileV4_CreatesSkipsAndRotates(t *testing.T) {
	client := fake.NewSimpleClientset()
	provider := &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("the-api-key"),
		"secrets/datadog/app-key": []byte("the-app-key"),
	}}

	r := newReconciler(
		v4Repo(t, []config.AddonSecretRequirement{v4PushRequirement()}, true),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		provider,
		fakeRemoteClientFn(client),
	)

	r.reconcile()
	stats := r.GetStats().(ReconcileStats)
	if stats.Created != 1 || stats.Errors != 0 {
		t.Fatalf("first pass: stats = %+v, want Created=1 Errors=0 (errors: %v)", stats, r.GetErrors())
	}
	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the Secret was never created on the cluster: %v", err)
	}
	if string(secret.Data["api-key"]) != "the-api-key" {
		t.Errorf("api-key = %q, want %q", secret.Data["api-key"], "the-api-key")
	}

	r.reconcile()
	if stats = r.GetStats().(ReconcileStats); stats.Skipped != 1 || stats.Updated != 0 {
		t.Errorf("second pass: stats = %+v, want Skipped=1 Updated=0", stats)
	}

	// Rotation: the value behind the same provider path changes.
	provider.values["secrets/datadog/api-key"] = []byte("rotated-key")
	r.reconcile()
	if stats = r.GetStats().(ReconcileStats); stats.Updated != 1 {
		t.Errorf("after rotation: stats = %+v, want Updated=1", stats)
	}
	secret, err = client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the rotated Secret: %v", err)
	}
	if string(secret.Data["api-key"]) != "rotated-key" {
		t.Errorf("api-key = %q, want the rotated value — this is the failure that kills workloads weeks after a migration", secret.Data["api-key"])
	}
}

// TestReconcileV4_AddonSwitchedOffIsLeftAlone: on v4, which addons a cluster
// runs comes from cluster-addons/<name>.yaml, not from labels. An addon with
// enabled:false must not get its Secret.
func TestReconcileV4_AddonSwitchedOffIsLeftAlone(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		v4Repo(t, []config.AddonSecretRequirement{v4PushRequirement()}, false),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("k"),
			"secrets/datadog/app-key": []byte("k"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	if stats := r.GetStats().(ReconcileStats); stats.Checked != 0 || stats.Created != 0 {
		t.Errorf("stats = %+v, want nothing checked or created", stats)
	}
}

// TestReconcileV4_ProseOnlyRequirementPushesNothing: a requirement with no
// push block is a note for a person. Sharko has no provider path for it and
// must not invent one.
func TestReconcileV4_ProseOnlyRequirementPushesNothing(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		v4Repo(t, []config.AddonSecretRequirement{{
			Name:        "datadog-api-key",
			Description: "get an API key from your Datadog account and put it on the cluster",
		}}, true),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Checked != 0 || stats.Errors != 0 {
		t.Errorf("stats = %+v, want nothing to do and no errors (%v)", stats, r.GetErrors())
	}
}

// TestReconcileV4_ClusterWithNoAssignmentFileIsNotAnError: a registered
// cluster nobody has given an addon to yet has no cluster-addons/<name>.yaml.
// That is an ordinary state, not a failure — it must not fill the status
// with errors every five minutes.
func TestReconcileV4_ClusterWithNoAssignmentFileIsNotAnError(t *testing.T) {
	repo := v4Repo(t, []config.AddonSecretRequirement{v4PushRequirement()}, true)
	assignPath, _ := config.V4ClusterAddonsPath("prod-cluster")
	delete(repo.files, assignPath)

	r := newReconciler(
		repo,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	r.reconcile()

	if errs := r.GetErrors(); len(errs) != 0 {
		t.Errorf("a cluster with no addons yet must not produce errors: %v", errs)
	}
}

// TestReconcileV4_UnreadableAssignmentFileLandsInTheStatus: when one
// cluster's file cannot be read for a REAL reason, that cluster is skipped
// and the reason is recorded. Silently skipping it is how the whole F1
// class of bug happens.
func TestReconcileV4_UnreadableAssignmentFileLandsInTheStatus(t *testing.T) {
	repo := v4Repo(t, []config.AddonSecretRequirement{v4PushRequirement()}, true)
	assignPath, _ := config.V4ClusterAddonsPath("prod-cluster")
	repo.failPaths = map[string]error{assignPath: errors.New("403 from the git host")}

	r := newReconciler(
		repo,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Errors != 1 {
		t.Fatalf("stats = %+v, want Errors=1", stats)
	}
	errs := r.GetErrors()
	if len(errs) != 1 || !strings.Contains(errs[0], "prod-cluster") || !strings.Contains(errs[0], "403") {
		t.Errorf("errors = %v, want one naming the cluster and the reason", errs)
	}
	if stats.LastRun.IsZero() {
		t.Error("the run must be stamped even when it went wrong — a status that says 'never ran' hides the problem")
	}
}

// TestReconcile_NoCatalogAnywhereLandsInTheStatus is the F1 fix stated
// plainly: a repo where neither catalog file can be read used to warn-log
// and return BEFORE the status was written, so the API kept serving the
// last good run and nothing anywhere said secrets had stopped being
// pushed.
func TestReconcile_NoCatalogAnywhereLandsInTheStatus(t *testing.T) {
	r := newReconciler(
		&v4GitReader{files: map[string][]byte{}},
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Errors != 1 {
		t.Fatalf("stats = %+v, want Errors=1", stats)
	}
	if stats.LastRun.IsZero() {
		t.Error("expected the failed run to be stamped")
	}
	errs := r.GetErrors()
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one", errs)
	}
	for _, want := range []string{config.AddonCatalogPath, v3CatalogPath} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("the error should name %q so the reader knows where Sharko looked: %q", want, errs[0])
		}
	}
}

// TestReconcileV4_UnreadableManagedClustersLandsInTheStatus: the same
// stance one level up — if the cluster list itself cannot be read, nothing
// is pushed and the status says so.
func TestReconcileV4_UnreadableManagedClustersLandsInTheStatus(t *testing.T) {
	repo := v4Repo(t, []config.AddonSecretRequirement{v4PushRequirement()}, true)
	repo.failPaths = map[string]error{config.V4ManagedClustersPath: errors.New("connection reset")}

	r := newReconciler(
		repo,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Errors != 1 || stats.LastRun.IsZero() {
		t.Fatalf("stats = %+v, want one error and a stamped run", stats)
	}
	if errs := r.GetErrors(); len(errs) != 1 || !strings.Contains(errs[0], config.V4ManagedClustersPath) {
		t.Errorf("errors = %v, want one naming %s", errs, config.V4ManagedClustersPath)
	}
}

// TestReconcileV4_HalfWrittenPushSaysWhatIsMissing: a push block somebody
// hand-edited and left incomplete gets a sentence naming the missing key,
// not a confusing Kubernetes API error.
func TestReconcileV4_HalfWrittenPushSaysWhatIsMissing(t *testing.T) {
	// Built in memory rather than through SaveAddonCatalog: the JSON
	// Schema refuses to WRITE a half-written push block, so this is the
	// state a hand-edit (or a read with no compiled validator) can reach.
	work := secretWork{
		clusterName: "prod-cluster",
		credLookup:  "prod-cluster",
		addon:       "datadog",
		push:        models.AddonSecretRef{SecretName: "datadog-secret"},
	}
	r := newReconciler(
		v4Repo(t, []config.AddonSecretRequirement{v4PushRequirement()}, true),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)

	stats := ReconcileStats{}
	err := r.reconcileSecret(context.Background(), &stats, work.credLookup, work.addon, work.push)
	if err == nil {
		t.Fatal("expected the incomplete definition to be refused")
	}
	for _, want := range []string{"namespace", "keys"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name the missing %q", err, want)
		}
	}
}

// TestReconcile_V3RepoStillWins: a repo carrying both layouts keeps its v3
// answer, the same preference internal/config's credential resolver and the
// cluster reconciler make. Nothing about this fix may quietly re-point a
// live v3 repo at a different file.
func TestReconcile_V3RepoStillWins(t *testing.T) {
	client := fake.NewSimpleClientset()
	assignPath, _ := config.V4ClusterAddonsPath("prod-cluster")
	repo := &v4GitReader{files: map[string][]byte{
		v3CatalogPath:                         []byte(catalogWithSecrets),
		"configuration/managed-clusters.yaml": []byte(clusterAddonsYAML),
		// A v4 catalog is present too, approving nothing.
		config.AddonCatalogPath:      v4Catalog(t, nil),
		config.V4ManagedClustersPath: []byte(v4ManagedClustersYAML),
		assignPath:                   v4Assignment(t, "prod-cluster", map[string]bool{}),
	}}

	r := newReconciler(
		repo,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("k1"),
			"secrets/datadog/app-key": []byte("k2"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	if stats := r.GetStats().(ReconcileStats); stats.Created != 1 {
		t.Errorf("stats = %+v, want the v3 catalog's secret created (errors: %v)", stats, r.GetErrors())
	}
}
