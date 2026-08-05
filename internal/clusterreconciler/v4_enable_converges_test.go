package clusterreconciler

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// The live-smoke bug this file guards, in one sentence: on a v4 repo the
// cluster Secret already exists (registration writes it, with no addon
// labels by design), enabling an addon only writes cluster-addons/<name>.yaml, and
// the reconciler used to apply the derived addons.sharko.dev/ label ONLY
// when the opt-in managed-cluster self-heal setting was ON — which it is not
// on a default install. So enable merged a PR and deployed nothing, forever,
// silently. Applying those labels is now ordinary convergence: it happens on
// the normal tick with no setting consulted.
//
// Every test here runs with self-heal OFF (SelfHealFn either nil or a
// counter that returns false), because "with the setting off" is the whole
// point.

// countingSelfHeal returns a SelfHealFn that always reports OFF and counts
// how many times the reconciler asked. The v4 addon-label path must never
// ask.
func countingSelfHeal(calls *int) func(context.Context) bool {
	return func(context.Context) bool {
		*calls++
		return false
	}
}

// seedClusterSecret puts a Sharko-owned ArgoCD cluster Secret into the fake
// clientset — the state a v4 repo is in right after registration and before
// anybody enables anything.
func seedClusterSecret(t *testing.T, client *fake.Clientset, name string, labels, annotations map[string]string) {
	t.Helper()
	all := map[string]string{
		LabelManagedBy:                   LabelValueSharko,
		"argocd.argoproj.io/secret-type": "cluster",
	}
	for k, v := range labels {
		all[k] = v
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   DefaultArgoCDNamespace,
			Labels:      all,
			Annotations: annotations,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   name,
			"server": "https://" + name + ".example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seeding the %s cluster Secret: %v", name, err)
	}
}

func getSecret(t *testing.T, client *fake.Clientset, name string) *corev1.Secret {
	t.Helper()
	s, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the %s cluster Secret: %v", name, err)
	}
	return s
}

// unreadableFileGit is a fakeGit whose cluster-addons/ listing still names the
// file but whose read of that one path fails — a git hiccup, or a file
// somebody broke. Everything else behaves normally.
type unreadableFileGit struct {
	*fakeGit
	failPath string
}

func (g *unreadableFileGit) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	if path == g.failPath {
		return nil, errors.New("boom: transient git failure")
	}
	return g.fakeGit.GetFileContent(ctx, path, ref)
}

// TestV4Enable_StampsAddonLabelWithSelfHealOff is the exact live repro:
// cluster registered (Secret exists, no addon labels), addon enabled (the
// assignment file gains the addon), self-heal OFF. The label the engine's
// ApplicationSet selects on must appear on the very next tick.
func TestV4Enable_StampsAddonLabelWithSelfHealOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// A v4 repo with the cluster registered and NO assignment file yet —
	// exactly what registration leaves behind.
	gp := &fakeGit{files: map[string][]byte{
		V4ManagedClustersPath: envelopedManagedClusters("spoke-eu"),
	}}
	client := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, gp, client, v4TestVault("spoke-eu"), &auditCollector{}, nil)
	selfHealAsked := 0
	r.deps.SelfHealFn = countingSelfHeal(&selfHealAsked)

	r.pollOnce(ctx)
	secret := getSecret(t, client, "spoke-eu")
	for k := range secret.Labels {
		if models.IsV4AddonLabelKey(k) {
			t.Fatalf("registration should leave no addon labels behind, found %q: %v", k, secret.Labels)
		}
	}

	// Somebody enables cert-manager: the PR merges, the assignment file
	// gains the addon. Nothing else changes.
	gp.files["cluster-addons/spoke-eu.yaml"] = clusterAddonsYAML("spoke-eu", map[string]bool{"cert-manager": true})
	r.pollOnce(ctx)

	secret = getSecret(t, client, "spoke-eu")
	key := models.V4AddonLabelKey("cert-manager")
	if got := secret.Labels[key]; got != models.LabelEnabled {
		t.Errorf("enable deployed nothing — %s = %q, want %q (labels: %v)", key, got, models.LabelEnabled, secret.Labels)
	}
	if !IsManagedBySharko(secret) {
		t.Errorf("the convergence dropped Sharko's ownership label: %v", secret.Labels)
	}
	if selfHealAsked != 0 {
		t.Errorf("the reconciler consulted the opt-in self-heal setting %d times for a v4 addon label — that gate is exactly the bug", selfHealAsked)
	}
}

// TestV4Disable_RemovesAddonLabelWithSelfHealOff is the same story in
// reverse: turning an addon off has to actually stop it, with no setting on.
func TestV4Disable_RemovesAddonLabelWithSelfHealOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	key := models.V4AddonLabelKey("cert-manager")
	gp := &fakeGit{files: v4RepoFiles(map[string]map[string]bool{
		"spoke-eu": {"cert-manager": true},
	}, "spoke-eu")}
	client := fake.NewSimpleClientset()
	// The Secret already carries the label — the cluster is running the addon.
	seedClusterSecret(t, client, "spoke-eu", map[string]string{key: models.LabelEnabled}, nil)

	r := newReconcilerForTest(t, gp, client, v4TestVault("spoke-eu"), &auditCollector{}, nil)
	selfHealAsked := 0
	r.deps.SelfHealFn = countingSelfHeal(&selfHealAsked)

	// Somebody disables it: the entry flips to enabled:false.
	gp.files["cluster-addons/spoke-eu.yaml"] = clusterAddonsYAML("spoke-eu", map[string]bool{"cert-manager": false})
	r.pollOnce(ctx)

	secret := getSecret(t, client, "spoke-eu")
	if _, present := secret.Labels[key]; present {
		t.Errorf("disable left the label in place — the addon keeps deploying: %v", secret.Labels)
	}
	if !IsManagedBySharko(secret) {
		t.Errorf("the convergence dropped Sharko's ownership label: %v", secret.Labels)
	}
	if selfHealAsked != 0 {
		t.Errorf("the reconciler consulted the opt-in self-heal setting %d times for a v4 addon label", selfHealAsked)
	}
}

// TestV4Converge_LeavesTakeoverPreservedLabelAlone guards the takeover
// promise. A label carried over from the cluster's previous owner can look
// exactly like a v3 addon key ("cert-manager"), and the takeover wrote down
// its exact name so nothing has to guess. The unconditional v4 path must
// stamp its own key and leave that one untouched.
func TestV4Converge_LeavesTakeoverPreservedLabelAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gp := &fakeGit{files: v4RepoFiles(map[string]map[string]bool{
		"spoke-eu": {"metrics-server": true},
	}, "spoke-eu")}
	client := fake.NewSimpleClientset()
	seedClusterSecret(t, client,
		"spoke-eu",
		map[string]string{"cert-manager": models.LabelEnabled}, // previous owner's, addon-shaped
		map[string]string{argosecrets.AnnotationTakeoverPreservedLabels: "cert-manager"},
	)

	r := newReconcilerForTest(t, gp, client, v4TestVault("spoke-eu"), &auditCollector{}, nil)
	r.pollOnce(ctx)

	secret := getSecret(t, client, "spoke-eu")
	if got := secret.Labels[models.V4AddonLabelKey("metrics-server")]; got != models.LabelEnabled {
		t.Errorf("the enabled addon's label was not applied: %v", secret.Labels)
	}
	if got := secret.Labels["cert-manager"]; got != models.LabelEnabled {
		t.Errorf("a takeover-preserved label was removed — the previous owner's ApplicationSet loses this cluster: %v", secret.Labels)
	}
}

// TestV4Converge_LeavesNonAddonLabelsAlone proves the unconditional path
// only ever touches Sharko's own addon keys: foreign qualified labels and
// the Secret's connection data come through untouched.
func TestV4Converge_LeavesNonAddonLabelsAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gp := &fakeGit{files: v4RepoFiles(map[string]map[string]bool{
		"spoke-eu": {"cert-manager": true},
	}, "spoke-eu")}
	client := fake.NewSimpleClientset()
	seedClusterSecret(t, client, "spoke-eu", map[string]string{
		"example.com/team":           "platform",
		"app.kubernetes.io/instance": "someone-elses-app",
	}, nil)

	r := newReconcilerForTest(t, gp, client, v4TestVault("spoke-eu"), &auditCollector{}, nil)
	before := getSecret(t, client, "spoke-eu")
	r.pollOnce(ctx)

	secret := getSecret(t, client, "spoke-eu")
	if got := secret.Labels[models.V4AddonLabelKey("cert-manager")]; got != models.LabelEnabled {
		t.Fatalf("the enabled addon's label was not applied: %v", secret.Labels)
	}
	if got := secret.Labels["example.com/team"]; got != "platform" {
		t.Errorf("a foreign label was changed: %v", secret.Labels)
	}
	if got := secret.Labels["app.kubernetes.io/instance"]; got != "someone-elses-app" {
		t.Errorf("ArgoCD's own tracking label was changed: %v", secret.Labels)
	}
	if secret.Data == nil && before.Data != nil || len(secret.Data) != len(before.Data) {
		t.Errorf("the connection data changed: before %v, after %v", before.Data, secret.Data)
	}
	// P2-C5: a real write now stamps provenance annotations
	// (sharko.dev/source-file, sharko.dev/written-at — sharko.dev/revision
	// is absent here because the fake git provider doesn't implement
	// gitprovider.BranchRevisioner, so the compared revision is honestly
	// unknown). This test's job is still "nothing UNRELATED was touched" —
	// every pre-existing annotation key (none, in this fixture) must
	// survive, and no other key should appear.
	for k, v := range before.Annotations {
		if got := secret.Annotations[k]; got != v {
			t.Errorf("pre-existing annotation %q changed: before %q, after %q", k, v, got)
		}
	}
	wantKeys := map[string]bool{AnnotationSourceFile: true, AnnotationWrittenAt: true}
	for k := range secret.Annotations {
		if !wantKeys[k] && before.Annotations[k] == "" {
			t.Errorf("unexpected new annotation %q = %q", k, secret.Annotations[k])
		}
	}
	if secret.Annotations[AnnotationSourceFile] == "" {
		t.Error("expected sharko.dev/source-file to be stamped on a real write")
	}
}

// TestV3Drift_StaysDriftOnlyWithSelfHealOff is the "nothing about v3 moved"
// guard. A v3 repo has no cluster-addons/ assignments at all, so the unconditional
// path can never fire there: drift is reported, nothing is written, exactly
// as before this fix.
func TestV3Drift_StaysDriftOnlyWithSelfHealOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	v3Body := []byte("apiVersion: sharko.dev/v1\n" +
		"kind: ManagedClusters\n" +
		"metadata:\n  name: managed-clusters\n" +
		"spec:\n  clusters:\n" +
		"    - name: spoke-eu\n" +
		"      labels:\n        cert-manager: enabled\n")

	gp := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: v3Body}}
	client := fake.NewSimpleClientset()
	// The live Secret is missing the addon label git asks for — drift.
	seedClusterSecret(t, client, "spoke-eu", nil, nil)

	r := newReconcilerForTest(t, gp, client, v4TestVault("spoke-eu"), &auditCollector{}, nil)
	selfHealAsked := 0
	r.deps.SelfHealFn = countingSelfHeal(&selfHealAsked)
	r.pollOnce(ctx)

	secret := getSecret(t, client, "spoke-eu")
	if _, present := secret.Labels["cert-manager"]; present {
		t.Errorf("a v3 repo wrote the drifted label with self-heal OFF — v3 behaviour changed: %v", secret.Labels)
	}
	if selfHealAsked == 0 {
		t.Error("a v3 drift no longer consults the opt-in self-heal setting — that setting is what governs v3")
	}
	rec, ok := r.LastReconcile("spoke-eu")
	if !ok {
		t.Fatal("no reconcile record for spoke-eu")
	}
	if rec.LabelDrift == nil {
		t.Error("v3 drift was not reported")
	}
}

// TestV4Converge_HeldWhenAssignmentFileUnreadable is the safety valve that
// comes with writing on every tick. If Sharko cannot read
// cluster-addons/<name>.yaml, it does not know which addons should be on —
// converging to "none" would undeploy the cluster's whole stack over a git
// hiccup or a YAML typo. Report the drift, write nothing.
func TestV4Converge_HeldWhenAssignmentFileUnreadable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	key := models.V4AddonLabelKey("cert-manager")
	base := &fakeGit{files: v4RepoFiles(map[string]map[string]bool{
		"spoke-eu": {"cert-manager": true},
	}, "spoke-eu")}
	gp := &unreadableFileGit{fakeGit: base, failPath: "cluster-addons/spoke-eu.yaml"}

	client := fake.NewSimpleClientset()
	seedClusterSecret(t, client, "spoke-eu", map[string]string{key: models.LabelEnabled}, nil)

	r := newReconcilerForTest(t, gp, client, v4TestVault("spoke-eu"), &auditCollector{}, nil)
	r.pollOnce(ctx)

	secret := getSecret(t, client, "spoke-eu")
	if got := secret.Labels[key]; got != models.LabelEnabled {
		t.Errorf("an unreadable assignment file wiped the cluster's addon labels: %v", secret.Labels)
	}
}

// TestV4Converge_HeldWhenClustersFolderUnreadable is the fleet-wide version
// of the same valve: the cluster-addons/ listing itself failed, so no cluster's
// desired set is known and none of their labels may be touched.
func TestV4Converge_HeldWhenClustersFolderUnreadable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	key := models.V4AddonLabelKey("cert-manager")
	gp := &listFailsGit{fakeGit: &fakeGit{files: map[string][]byte{
		V4ManagedClustersPath: envelopedManagedClusters("spoke-eu"),
	}}}

	client := fake.NewSimpleClientset()
	seedClusterSecret(t, client, "spoke-eu", map[string]string{key: models.LabelEnabled}, nil)

	r := newReconcilerForTest(t, gp, client, v4TestVault("spoke-eu"), &auditCollector{}, nil)
	r.pollOnce(ctx)

	secret := getSecret(t, client, "spoke-eu")
	if got := secret.Labels[key]; got != models.LabelEnabled {
		t.Errorf("an unreadable cluster-addons/ folder wiped the fleet's addon labels: %v", secret.Labels)
	}
}

// listFailsGit is a fakeGit whose cluster-addons/ listing errors out for a real
// reason (not "the folder does not exist", which is a normal fresh repo).
type listFailsGit struct {
	*fakeGit
}

func (g *listFailsGit) ListDirectory(ctx context.Context, dir, ref string) ([]string, error) {
	if dir == v4ClustersDir {
		return nil, errors.New("boom: transient git failure")
	}
	return g.fakeGit.ListDirectory(ctx, dir, ref)
}

// compile-time proof both wrappers still satisfy the interface the
// reconciler asks for.
var (
	_ gitprovider.GitProvider = (*unreadableFileGit)(nil)
	_ gitprovider.GitProvider = (*listFailsGit)(nil)
)
