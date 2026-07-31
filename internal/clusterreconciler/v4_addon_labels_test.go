package clusterreconciler

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// clusterAddonsYAML renders a clusters/<name>.yaml body with the given
// addon -> enabled map. Written as a literal (rather than through
// models.SaveClusterAddons) so the test pins the ON-DISK shape a person
// would actually author, not whatever the writer happens to emit.
func clusterAddonsYAML(cluster string, addons map[string]bool) []byte {
	body := "apiVersion: sharko.dev/v1\n" +
		"kind: ClusterAddons\n" +
		"cluster: " + cluster + "\n"
	if len(addons) == 0 {
		return []byte(body + "addons: {}\n")
	}
	body += "addons:\n"
	for addon, enabled := range addons {
		body += fmt.Sprintf("  %s:\n    enabled: %t\n", addon, enabled)
	}
	return []byte(body)
}

func v4RepoFiles(assignments map[string]map[string]bool, clusters ...string) map[string][]byte {
	files := map[string][]byte{
		V4ManagedClustersPath: envelopedManagedClusters(clusters...),
	}
	for cluster, addons := range assignments {
		files["clusters/"+cluster+".yaml"] = clusterAddonsYAML(cluster, addons)
	}
	return files
}

func v4TestVault(clusters ...string) *fakeVault {
	creds := map[string]*providers.Kubeconfig{}
	for _, c := range clusters {
		creds[c] = &providers.Kubeconfig{
			Server: "https://" + c + ".example.com",
			CAData: []byte("ca"),
			Token:  "tk",
		}
	}
	return &fakeVault{creds: creds}
}

// TestPollOnce_V4Repo_DerivesAddonLabelsPerCluster is the core FIX-2 proof:
// nothing in Sharko used to write the addons.sharko.dev/<addon> label the v4
// engine's ApplicationSet selects on, so enabling an addon on a v4 repo
// deployed nothing at all. The reconciler now derives that label from
// clusters/*.yaml.
func TestPollOnce_V4Repo_DerivesAddonLabelsPerCluster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gp := &fakeGit{files: v4RepoFiles(map[string]map[string]bool{
		"prod-eu": {"cert-manager": true, "metrics-server": true, "falco": false},
		"prod-us": {"cert-manager": true},
	}, "prod-eu", "prod-us")}

	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, gp, k8sClient, v4TestVault("prod-eu", "prod-us"), &auditCollector{}, nil)
	r.pollOnce(ctx)

	eu, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("prod-eu Secret: %v", err)
	}
	if got := eu.Labels[models.V4AddonLabelKey("cert-manager")]; got != models.LabelEnabled {
		t.Errorf("prod-eu %s = %q, want %q", models.V4AddonLabelKey("cert-manager"), got, models.LabelEnabled)
	}
	if got := eu.Labels[models.V4AddonLabelKey("metrics-server")]; got != models.LabelEnabled {
		t.Errorf("prod-eu %s = %q, want %q", models.V4AddonLabelKey("metrics-server"), got, models.LabelEnabled)
	}
	// enabled:false must produce NO label at all — the engine selector only
	// matches "enabled", and the absence is what lets a later disable
	// actually remove the key.
	if _, present := eu.Labels[models.V4AddonLabelKey("falco")]; present {
		t.Errorf("prod-eu carries a label for a disabled addon: %v", eu.Labels)
	}

	us, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-us", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("prod-us Secret: %v", err)
	}
	if got := us.Labels[models.V4AddonLabelKey("cert-manager")]; got != models.LabelEnabled {
		t.Errorf("prod-us %s = %q, want %q", models.V4AddonLabelKey("cert-manager"), got, models.LabelEnabled)
	}
	// Per-cluster, not fleet-wide: prod-us never asked for metrics-server.
	if _, present := us.Labels[models.V4AddonLabelKey("metrics-server")]; present {
		t.Errorf("prod-us picked up another cluster's addon label: %v", us.Labels)
	}
}

// TestPollOnce_V4Repo_DisableRemovesLabelOnNextReconcile walks the full
// lifecycle: enable, converge, then flip enabled to false in git and
// reconcile again. The stale label must be gone, otherwise the addon keeps
// deploying forever after somebody disabled it.
//
// Self-heal is ON here only because this test was written when that setting
// was the sole way to re-write labels on an ALREADY-EXISTING managed Secret.
// It no longer is: a v4 addon label converges on the normal tick with the
// setting OFF (see v4_enable_converges_test.go, which is the default-install
// version of this walk). Leaving it ON here still proves the lifecycle and
// proves the setting did not break it.
func TestPollOnce_V4Repo_DisableRemovesLabelOnNextReconcile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gp := &fakeGit{files: v4RepoFiles(map[string]map[string]bool{
		"prod-eu": {"cert-manager": true},
	}, "prod-eu")}

	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, gp, k8sClient, v4TestVault("prod-eu"), &auditCollector{}, nil)
	r.deps.SelfHealFn = func(context.Context) bool { return true }

	r.pollOnce(ctx)
	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("first reconcile did not create the Secret: %v", err)
	}
	if secret.Labels[models.V4AddonLabelKey("cert-manager")] != models.LabelEnabled {
		t.Fatalf("first reconcile did not stamp the addon label: %v", secret.Labels)
	}

	// Somebody disables the addon: the assignment file flips to false.
	gp.files["clusters/prod-eu.yaml"] = clusterAddonsYAML("prod-eu", map[string]bool{"cert-manager": false})
	r.pollOnce(ctx)

	secret, err = k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret disappeared: %v", err)
	}
	if _, present := secret.Labels[models.V4AddonLabelKey("cert-manager")]; present {
		t.Errorf("the addon label survived a disable — the addon would keep deploying: %v", secret.Labels)
	}
	// Ownership must survive the label convergence.
	if !IsManagedBySharko(secret) {
		t.Errorf("self-heal dropped the Sharko ownership label: %v", secret.Labels)
	}
}

// TestPollOnce_V3Repo_NoV4LabelsDerived is the "v3 path unchanged" guard:
// a v3 repo never lists clusters/, never derives an addons.sharko.dev/ key,
// and keeps writing exactly the bare labels its managed-clusters.yaml
// declares.
func TestPollOnce_V3Repo_NoV4LabelsDerived(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	v3Body := []byte("apiVersion: sharko.dev/v1\n" +
		"kind: ManagedClusters\n" +
		"metadata:\n  name: managed-clusters\n" +
		"spec:\n  clusters:\n" +
		"    - name: prod-eu\n" +
		"      labels:\n        cert-manager: enabled\n")

	gp := &fakeGit{files: map[string][]byte{
		DefaultManagedClustersPath: v3Body,
		// A stray v4 tree that must be completely ignored on a v3 repo.
		"clusters/prod-eu.yaml": clusterAddonsYAML("prod-eu", map[string]bool{"falco": true}),
	}}

	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, gp, k8sClient, v4TestVault("prod-eu"), &auditCollector{}, nil)
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("prod-eu Secret: %v", err)
	}
	if got := secret.Labels["cert-manager"]; got != models.LabelEnabled {
		t.Errorf("v3 bare addon label = %q, want %q", got, models.LabelEnabled)
	}
	for k := range secret.Labels {
		if models.IsV4AddonLabelKey(k) {
			t.Errorf("a v3 repo's Secret picked up a v4 addon label %q: %v", k, secret.Labels)
		}
	}
}

func TestV4LabelsFor_SkipsDisabledAndUnnamed(t *testing.T) {
	t.Parallel()
	spec := models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons: map[string]models.ClusterAddonsAddon{
			"cert-manager":   {Enabled: true},
			"falco":          {Enabled: false},
			"../../engine":   {Enabled: true}, // hand-authored garbage
			"metrics-server": {Enabled: true},
		},
	}
	got := v4LabelsFor(spec)
	want := map[string]string{
		models.V4AddonLabelKey("cert-manager"):   models.LabelEnabled,
		models.V4AddonLabelKey("metrics-server"): models.LabelEnabled,
	}
	if len(got) != len(want) {
		t.Fatalf("v4LabelsFor = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("v4LabelsFor[%s] = %q, want %q", k, got[k], v)
		}
	}
}
