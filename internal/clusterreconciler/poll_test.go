package clusterreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// -------- test fixtures --------

// fakeGit is a minimal in-memory gitprovider.GitProvider — only the read
// methods pollOnce exercises are implemented; the write methods panic to
// flag accidental usage. We avoid pulling internal/demo's MockGitProvider
// to keep this package's test graph small and the failure modes easy to
// reason about (the demo seed bytes change over time as the rest of
// Sharko evolves).
type fakeGit struct {
	files map[string][]byte
	err   error // returned from GetFileContent on every path
}

func (f *fakeGit) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if body, ok := f.files[path]; ok {
		return body, nil
	}
	return nil, fmt.Errorf("fakeGit: %s: %w", path, gitprovider.ErrFileNotFound)
}
func (f *fakeGit) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	panic("fakeGit: ListDirectory not expected in clusterreconciler tests")
}
func (f *fakeGit) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	panic("fakeGit: ListPullRequests not expected in clusterreconciler tests")
}
func (f *fakeGit) TestConnection(_ context.Context) error { return nil }
func (f *fakeGit) CreateBranch(_ context.Context, _, _ string) error {
	panic("fakeGit: CreateBranch not expected — reconciler is read-only against git")
}
func (f *fakeGit) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	panic("fakeGit: CreateOrUpdateFile not expected — reconciler is read-only against git")
}
func (f *fakeGit) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	panic("fakeGit: BatchCreateFiles not expected — reconciler is read-only against git")
}
func (f *fakeGit) DeleteFile(_ context.Context, _, _, _ string) error {
	panic("fakeGit: DeleteFile not expected — reconciler is read-only against git")
}
func (f *fakeGit) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	panic("fakeGit: CreatePullRequest not expected — reconciler is read-only against git")
}
func (f *fakeGit) MergePullRequest(_ context.Context, _ int) error {
	panic("fakeGit: MergePullRequest not expected — reconciler is read-only against git")
}
func (f *fakeGit) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	panic("fakeGit: GetPullRequestStatus not expected — reconciler is read-only against git")
}
func (f *fakeGit) DeleteBranch(_ context.Context, _ string) error {
	panic("fakeGit: DeleteBranch not expected — reconciler is read-only against git")
}

// fakeVault implements providers.ClusterCredentialsProvider with per-name
// canned responses (success or error). Lets each test express "cluster A
// resolves; cluster B errors" explicitly.
type fakeVault struct {
	creds map[string]*providers.Kubeconfig
	errs  map[string]error // by cluster name; non-nil error overrides creds
}

func (v *fakeVault) GetCredentials(name string) (*providers.Kubeconfig, error) {
	if err, ok := v.errs[name]; ok {
		return nil, err
	}
	if c, ok := v.creds[name]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("fakeVault: no credentials for %q", name)
}
func (v *fakeVault) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (v *fakeVault) SearchSecrets(_ string) ([]string, error)       { return nil, nil }
func (v *fakeVault) HealthCheck(_ context.Context) error            { return nil }

// auditCollector accumulates audit.Entry emissions for assertion.
// Safe for concurrent use: pollOnce is synchronous in tests but the
// AuditFn contract is goroutine-safe and a mutex costs nothing here.
type auditCollector struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *auditCollector) Add(e audit.Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
}

func (a *auditCollector) Snapshot() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]audit.Entry, len(a.entries))
	copy(out, a.entries)
	return out
}

// hasEvent reports whether any entry has the given event AND result.
// Used to assert that a specific audit signal fired without coupling to
// the exact ordering or count of entries (some flows emit per-action +
// summary, and the summary count varies).
func hasEvent(entries []audit.Entry, event, result string) bool {
	for _, e := range entries {
		if e.Event == event && e.Result == result {
			return true
		}
	}
	return false
}

// hasEventForResource reports whether any entry has the given event and a
// Resource field that contains substr. The reconciler embeds the cluster
// name in Resource ("cluster:foo"), so substring is the natural assertion.
func hasEventForResource(entries []audit.Entry, event, substr string) bool {
	for _, e := range entries {
		if e.Event == event && strings.Contains(e.Resource, substr) {
			return true
		}
	}
	return false
}

// envelopedManagedClusters returns the canonical V125-1-9 envelope shape
// for the given cluster names. Used by tests that need a valid YAML body
// the schema validator accepts. The vault SecretPath defaults to "" so
// the reconciler falls back to the cluster Name for the credential
// lookup (matches argosecrets.Reconciler's idiom).
//
// IMPORTANT: when called with zero arguments we emit `clusters: []`
// (an explicit empty sequence) rather than `clusters:` (which yaml.v3
// renders as `null` and the V125-1-9 JSON Schema rejects with
// "expected array, but got null"). The test for "delete orphan when
// git is empty" depends on this rendering — without it the YAML
// fails schema validation BEFORE the diff runs.
func envelopedManagedClusters(clusters ...string) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: sharko.io/v1\n")
	b.WriteString("kind: ManagedClusters\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: managed-clusters\n")
	b.WriteString("spec:\n")
	if len(clusters) == 0 {
		b.WriteString("  clusters: []\n")
		return []byte(b.String())
	}
	b.WriteString("  clusters:\n")
	for _, name := range clusters {
		b.WriteString("    - name: ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// newReconcilerForTest builds a Reconciler wired against the supplied
// fakes. TickInterval is set high (1h) so the goroutine never auto-fires
// during a synchronous pollOnce assertion — tests drive pollOnce
// directly via r.pollOnce(ctx) without calling Start.
func newReconcilerForTest(t *testing.T, gp gitprovider.GitProvider, k8sClient *fake.Clientset, vault providers.ClusterCredentialsProvider, audits *auditCollector, body []byte) *Reconciler {
	t.Helper()

	var gitFn func() gitprovider.GitProvider
	if gp != nil {
		gitFn = func() gitprovider.GitProvider { return gp }
	} else {
		// Default helper: returns a fakeGit with the supplied body at the
		// default path. Lets tests pass `body=nil` to mean "no file".
		fg := &fakeGit{files: map[string][]byte{}}
		if body != nil {
			fg.files[DefaultManagedClustersPath] = body
		}
		gitFn = func() gitprovider.GitProvider { return fg }
	}

	return New(Deps{
		GitProvider:  gitFn,
		ArgoClient:   k8sClient,
		Vault:        vault,
		AuditFn:      audits.Add,
		TickInterval: 0, // default; we never Start the loop in these tests
	})
}

// secretsListUnfiltered returns every Secret in the argocd namespace
// regardless of label — used to assert the reconciler did NOT touch
// foreign Secrets (test #3).
func secretsListUnfiltered(t *testing.T, client *fake.Clientset, ns string) []corev1.Secret {
	t.Helper()
	list, err := client.CoreV1().Secrets(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unfiltered list failed: %v", err)
	}
	return list.Items
}

// countMutations sums the create + delete + update actions on the fake
// clientset. List + Get are read-only and excluded. Used by the
// idempotency test to assert "tick 2 mutated nothing new".
func countMutations(client *fake.Clientset) int {
	var n int
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create", "delete", "update":
			n++
		}
	}
	return n
}

// resetClientActions clears the recorded action log on the fake clientset
// between reconcile ticks so idempotency assertions can compare
// "before vs after tick N+1" without subtracting baseline action counts.
func resetClientActions(client *fake.Clientset) {
	client.Fake.ClearActions()
}

// -------- the 7 spec tests --------

// Test 1 — happy path: a cluster declared in git, nothing in argocd; the
// reconciler creates a single Secret with the sharko ownership label, the
// argocd cluster-type label, and the credentials supplied by the vault.
func TestPollOnce_NewClusterInGit_CreatesLabeledSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("prod-eu")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://prod-eu.example.com",
				CAData: []byte("fake-ca-bytes"),
				Token:  "fake-token",
			},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	// Secret was created.
	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'prod-eu' to exist after reconcile: %v", err)
	}
	if !IsManagedBySharko(secret) {
		t.Fatalf("created secret is missing the sharko ownership label: labels=%v", secret.Labels)
	}
	// argocd's secret-type label must be present so ArgoCD's cluster
	// generator picks it up — buildClusterSecret applies it via
	// argosecrets.BuildClusterSecretLabels.
	if secret.Labels["argocd.argoproj.io/secret-type"] != "cluster" {
		t.Fatalf("expected argocd.argoproj.io/secret-type=cluster, got labels=%v", secret.Labels)
	}
	// Server URL came from the vault.
	if got := secret.StringData["server"]; got != "https://prod-eu.example.com" {
		t.Fatalf("server=%q, want %q", got, "https://prod-eu.example.com")
	}

	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_create", "cluster:prod-eu") {
		t.Fatalf("expected cluster_secret_create audit entry for prod-eu; got %d entries", len(entries))
	}
	if !hasEvent(entries, "cluster_secret_reconcile_tick", "success") {
		t.Fatalf("expected success summary audit; got %d entries", len(entries))
	}
}

// Test 2 — orphan delete: an in-argocd, sharko-labeled Secret with no
// matching entry in git is deleted on the next tick. The reconciler does
// NOT defer to a second cycle (unlike the legacy argosecrets.Reconciler);
// the design doc §9 self-heal semantic is "git is the source of truth,
// delete immediately."
func TestPollOnce_ClusterRemovedFromGit_DeletesLabeledSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters() // zero clusters
	preexisting := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stale-cluster",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	k8sClient := fake.NewSimpleClientset(preexisting)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	_, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "stale-cluster", metav1.GetOptions{})
	if err == nil {
		t.Fatalf("expected 'stale-cluster' secret to be deleted, but it still exists")
	}

	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_delete", "cluster:stale-cluster") {
		t.Fatalf("expected cluster_secret_delete audit entry for stale-cluster; got %v", entries)
	}
}

// Test 3 — V125-2 Adopt safety: a same-name Secret exists in argocd WITHOUT
// the sharko label. The reconciler must NOT touch it (no overwrite, no
// adopt) AND must NOT create a colliding Secret (K8s would reject a same-
// name create anyway). Per design doc §9 this is V125-2 Adopt territory.
//
// Side effect we DO require: a cluster_secret_skip_unlabeled audit entry
// so the operator has a signal that the cluster is half-managed and an
// Adopt PR is needed.
func TestPollOnce_UnlabeledSecret_LeftAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("foreign-cluster")

	// Pre-existing secret with the same name but NO sharko label —
	// simulating an operator-created or externally-managed cluster.
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foreign-cluster",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				"created-by": "human-operator",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"server": []byte("https://foreign.example.com"),
		},
	}
	k8sClient := fake.NewSimpleClientset(foreign)
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"foreign-cluster": {Server: "https://sharko-view.example.com", CAData: []byte("x"), Token: "tk"},
		},
	}
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	// The foreign Secret is unchanged.
	got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "foreign-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("foreign Secret must still exist: %v", err)
	}
	if IsManagedBySharko(got) {
		t.Fatalf("foreign Secret was adopted (sharko label written) — reconciler must not touch unlabeled Secrets; labels=%v", got.Labels)
	}
	if string(got.Data["server"]) != "https://foreign.example.com" {
		t.Fatalf("foreign Secret data was overwritten: got server=%q, want %q",
			string(got.Data["server"]), "https://foreign.example.com")
	}

	// Exactly one Secret in the namespace — no shadow create attempt.
	items := secretsListUnfiltered(t, k8sClient, DefaultArgoCDNamespace)
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 Secret in %s ns (the foreign one), got %d", DefaultArgoCDNamespace, len(items))
	}

	// Audit must surface the skip so the operator sees a signal.
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_skip_unlabeled", "cluster:foreign-cluster") {
		t.Fatalf("expected cluster_secret_skip_unlabeled audit for foreign-cluster; got %v", entries)
	}
}

// Test 4 — idempotency: two consecutive ticks against an in-sync state
// must produce zero K8s mutations on tick #2. Asserted via the fake
// clientset's action recorder.
func TestPollOnce_NoChanges_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)

	// Tick #1: bootstrap from empty.
	r.pollOnce(ctx)
	tick1Mutations := countMutations(k8sClient)
	if tick1Mutations == 0 {
		t.Fatalf("tick 1 expected to create the cluster Secret but recorded 0 mutations")
	}

	// Tick #2: state matches desired; expect no further mutations.
	resetClientActions(k8sClient)
	r.pollOnce(ctx)
	tick2Mutations := countMutations(k8sClient)
	if tick2Mutations != 0 {
		t.Fatalf("tick 2 must be a no-op (state in sync), but recorded %d mutations", tick2Mutations)
	}
}

// Test 5 — git fetch fails: the reconciler MUST NOT touch any K8s state
// when it cannot read the desired state. State preservation + audit
// signal are the contract; the next tick retries.
func TestPollOnce_GitFetchFails_PreservesState_LogsAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Seed argocd with one sharko-labeled Secret. If the reconciler did
	// erroneously run the diff against an empty desired set on a git
	// error, this Secret would be deleted — which the test then catches.
	preexisting := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "must-not-be-touched",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	k8sClient := fake.NewSimpleClientset(preexisting)
	audits := &auditCollector{}

	gitErr := errors.New("simulated git API 503")
	gp := &fakeGit{err: gitErr}

	r := newReconcilerForTest(t, gp, k8sClient, &fakeVault{}, audits, nil)
	r.pollOnce(ctx)

	// State preserved.
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "must-not-be-touched", metav1.GetOptions{}); err != nil {
		t.Fatalf("must-not-be-touched Secret was lost on a git read failure: %v", err)
	}
	if got := countMutations(k8sClient); got != 0 {
		t.Fatalf("expected 0 K8s mutations on git failure, got %d", got)
	}

	// Audit signal fired with the failure shape.
	entries := audits.Snapshot()
	if !hasEvent(entries, "cluster_secret_reconcile", "failure") {
		t.Fatalf("expected cluster_secret_reconcile failure audit on git error; got %v", entries)
	}
}

// Test 6 — per-cluster error isolation: vault errors on cluster #2 do
// NOT block the reconciler from creating Secrets for clusters #1 and #3
// in the same tick. Design doc §10 "failure modes": one bad cluster
// must not poison the entire reconciliation.
func TestPollOnce_VaultFailsForOneCluster_OthersStillReconcile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1", "c2", "c3")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
			"c3": {Server: "https://c3.example.com", CAData: []byte("ca"), Token: "tk"},
		},
		errs: map[string]error{
			"c2": errors.New("simulated vault outage for c2"),
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	for _, name := range []string{"c1", "c3"} {
		if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
			t.Fatalf("cluster %q should have been created despite c2's vault error: %v", name, err)
		}
	}
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "c2", metav1.GetOptions{}); err == nil {
		t.Fatalf("cluster c2's Secret should NOT exist (vault failed); but Get succeeded")
	}

	// Audit shape: a failure event for c2; success events for c1 and c3;
	// the tick summary fired with result=partial.
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_create", "cluster:c2") {
		t.Fatalf("expected an audit entry referencing cluster c2 (the failure); got %v", entries)
	}
	if !hasEventForResource(entries, "cluster_secret_create", "cluster:c1") {
		t.Fatalf("expected an audit entry referencing cluster c1 (success); got %v", entries)
	}
	if !hasEvent(entries, "cluster_secret_reconcile_tick", "partial") {
		t.Fatalf("expected tick summary with result=partial when 1/3 clusters errored; got %v", entries)
	}
}

// Test 7 — invalid YAML rejection: a body that the V125-1-9 schema
// validator rejects (wrong kind in this case) must NOT cause any K8s
// mutation. The reconciler audits the failure and exits the tick.
func TestPollOnce_InvalidYAML_RejectedNotApplied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Enveloped body with the wrong kind — LoadManagedClusters rejects
	// it with an explicit wrong-kind error. This exercises the V125-1-9
	// schema-aware reader path the spec calls for.
	invalid := []byte(`apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: ghost-cluster
`)

	// Seed argocd with a sharko-labeled Secret that would be "orphaned"
	// (and thus deleted) if the reconciler proceeded with the empty
	// fallback. Catches any code path that swallows the validation
	// error and continues with a partial state.
	guardSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preexisting",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	k8sClient := fake.NewSimpleClientset(guardSecret)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, invalid)
	r.pollOnce(ctx)

	// guardSecret untouched + ghost-cluster never created.
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "preexisting", metav1.GetOptions{}); err != nil {
		t.Fatalf("preexisting Secret was lost despite YAML validation failure: %v", err)
	}
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "ghost-cluster", metav1.GetOptions{}); err == nil {
		t.Fatalf("ghost-cluster Secret was created despite YAML validation failure")
	}

	// Zero mutations recorded (only the list/get reads happened).
	for _, a := range k8sClient.Actions() {
		switch a.GetVerb() {
		case "create", "delete", "update":
			t.Fatalf("unexpected mutation %s on a YAML validation failure: %s/%s",
				a.GetVerb(), a.GetResource().Resource, actionTarget(a))
		}
	}

	// Audit signal: a schema-validation failure was recorded.
	entries := audits.Snapshot()
	if !hasEvent(entries, "cluster_secret_reconcile", "failure") {
		t.Fatalf("expected cluster_secret_reconcile failure audit on invalid YAML; got %v", entries)
	}
}

// clusterSecretShape unmarshals a created cluster Secret's config JSON enough
// to distinguish the bearerToken shape from the execProviderConfig (EKS) shape.
type clusterSecretShape struct {
	BearerToken        string           `json:"bearerToken"`
	ExecProviderConfig *json.RawMessage `json:"execProviderConfig"`
}

// readClusterSecretConfig fetches the named cluster Secret from the argocd
// namespace and parses its config JSON. buildClusterSecret writes config via
// StringData (the fake clientset keeps it there on Create), so read StringData
// first and fall back to Data.
func readClusterSecretConfig(t *testing.T, client *fake.Clientset, name string) clusterSecretShape {
	t.Helper()
	secret, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting secret %q: %v", name, err)
	}
	raw := secret.StringData["config"]
	if raw == "" {
		raw = string(secret.Data["config"])
	}
	if raw == "" {
		t.Fatalf("secret %q has empty config", name)
	}
	var cfg clusterSecretShape
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parsing config for secret %q: %v\nconfig=%s", name, err, raw)
	}
	return cfg
}

// Test V2-cleanup-12 #1 — a kubeconfig cluster whose vault credentials carry a
// bearer token must produce a Secret in the bearerToken shape, NOT the
// execProviderConfig (argocd-k8s-auth) shape ArgoCD v1.x rejects. This is the
// core regression guard: before the fix the reconciler dropped creds.Token, so
// buildSecretConfig fell into the AWS exec branch and clobbered the good secret.
func TestPollOnce_KubeconfigClusterKeepsBearerShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("kube-cluster")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"kube-cluster": {
				Server: "https://kube-cluster.example.com",
				CAData: []byte("fake-ca-bytes"),
				Token:  "static-bearer-token-xyz",
			},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	cfg := readClusterSecretConfig(t, k8sClient, "kube-cluster")
	if cfg.BearerToken != "static-bearer-token-xyz" {
		t.Fatalf("config.bearerToken = %q, want the vault token (the V2-cleanup-12 fix)", cfg.BearerToken)
	}
	if cfg.ExecProviderConfig != nil {
		t.Fatalf("kubeconfig cluster must NOT get execProviderConfig; got %s", string(*cfg.ExecProviderConfig))
	}
}

// Test V2-cleanup-12 #2 — a genuine EKS/IAM cluster whose credentials have NO
// token must still produce the execProviderConfig shape (AWS path unchanged).
// RoleARN flows through DefaultRoleARN and Region from the cluster entry.
func TestPollOnce_EKSClusterKeepsExecShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("eks-cluster")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"eks-cluster": {
				Server: "https://eks-cluster.example.com",
				CAData: []byte("fake-ca-bytes"),
				// No Token — pure IAM cluster.
			},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider {
			return &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: body}}
		},
		ArgoClient:     k8sClient,
		Vault:          vault,
		AuditFn:        audits.Add,
		DefaultRoleARN: "arn:aws:iam::123456789012:role/EKSReadRole",
	})
	r.pollOnce(ctx)

	cfg := readClusterSecretConfig(t, k8sClient, "eks-cluster")
	if cfg.BearerToken != "" {
		t.Fatalf("EKS cluster must NOT get a bearerToken; got %q", cfg.BearerToken)
	}
	if cfg.ExecProviderConfig == nil {
		t.Fatal("EKS cluster (empty Token) must keep the execProviderConfig shape")
	}
}

// Test V2-cleanup-12 #3 — round-trip idempotency: a kubeconfig cluster written
// as bearerToken, reconciled again (vault still returns the token), must stay
// bearerToken across ticks. Guards against a writer fight that flips the shape.
func TestPollOnce_BearerRoundTripDoesNotFlip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("kube-cluster")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"kube-cluster": {
				Server: "https://kube-cluster.example.com",
				CAData: []byte("fake-ca-bytes"),
				Token:  "static-bearer-token-xyz",
			},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)

	r.pollOnce(ctx)
	first := readClusterSecretConfig(t, k8sClient, "kube-cluster")
	if first.BearerToken == "" || first.ExecProviderConfig != nil {
		t.Fatalf("first tick should be bearerToken shape; bearer=%q exec=%v", first.BearerToken, first.ExecProviderConfig)
	}

	// Second tick — identical desired state; the secret must remain bearer.
	r.pollOnce(ctx)
	second := readClusterSecretConfig(t, k8sClient, "kube-cluster")
	if second.BearerToken != "static-bearer-token-xyz" {
		t.Fatalf("second tick bearerToken = %q, want unchanged token", second.BearerToken)
	}
	if second.ExecProviderConfig != nil {
		t.Fatalf("second tick must NOT flip to execProviderConfig; got %s", string(*second.ExecProviderConfig))
	}
}

// --- Story 28.1: clusterreconciler orphan sweep — adopted secrets are delete-proof ---

// TestPollOnce_AdoptedOrphan_NeverDeleted verifies that a sharko-labeled Secret
// with the adopted annotation is NOT deleted by the orphan sweep even when it is
// absent from managed-clusters.yaml (Story 28.1 — clusterreconciler sweep).
func TestPollOnce_AdoptedOrphan_NeverDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Secret is sharko-labeled (so listManagedSecrets returns it) AND adopted.
	adoptedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted-cluster",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
			Annotations: map[string]string{
				annotationAdopted: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"server": []byte("https://adopted-cluster.example.com"),
			"config": []byte(`{"bearerToken":"foreign-tok"}`),
		},
	}

	// Git says zero clusters — adopted-cluster is an orphan candidate.
	body := envelopedManagedClusters()
	k8sClient := fake.NewSimpleClientset(adoptedSecret)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	// Adopted secret must survive the orphan sweep.
	got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "adopted-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("adopted-cluster must still exist after orphan sweep: %v", err)
	}
	// Data must be untouched.
	if string(got.Data["config"]) != `{"bearerToken":"foreign-tok"}` {
		t.Errorf("adopted-cluster Data was mutated: config=%q", string(got.Data["config"]))
	}
	// Adopted annotation must still be present.
	if got.Annotations[annotationAdopted] != "true" {
		t.Errorf("adopted annotation missing after sweep; annotations=%v", got.Annotations)
	}

	// Audit must surface the skip.
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_skip_adopted", "cluster:adopted-cluster") {
		t.Fatalf("expected cluster_secret_skip_adopted audit for adopted-cluster; got %v", entries)
	}
	// No delete action should have been issued.
	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "delete" {
			t.Errorf("unexpected delete action on adopted secret: %s", actionTarget(a))
		}
	}
}

// TestPollOnce_NonAdoptedOrphan_Deleted verifies that a non-adopted orphan
// IS deleted (regression: registration-pending and adopted checks must not
// block regular orphan cleanup — Story 28.1).
func TestPollOnce_NonAdoptedOrphan_Deleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// A sharko-labeled secret that is not in git and has no adopted annotation.
	plainOrphan := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plain-orphan",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
			// No annotations.
		},
		Type: corev1.SecretTypeOpaque,
	}

	body := envelopedManagedClusters() // zero clusters
	k8sClient := fake.NewSimpleClientset(plainOrphan)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	_, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "plain-orphan", metav1.GetOptions{})
	if err == nil {
		t.Fatal("plain-orphan should have been deleted, but still exists")
	}
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_delete", "cluster:plain-orphan") {
		t.Fatalf("expected cluster_secret_delete audit for plain-orphan; got %v", entries)
	}
}

// actionTarget extracts a human-readable target name from a fake-client
// Action for test error messages. Different verbs expose the target
// differently in k8stesting's API; the safe fallback is the verb's GVR
// string form.
func actionTarget(a k8stesting.Action) string {
	switch v := a.(type) {
	case k8stesting.CreateActionImpl:
		if s, ok := v.GetObject().(*corev1.Secret); ok && s != nil {
			return s.Name
		}
	case k8stesting.DeleteActionImpl:
		return v.GetName()
	case k8stesting.UpdateActionImpl:
		if s, ok := v.GetObject().(*corev1.Secret); ok && s != nil {
			return s.Name
		}
	}
	return "<unknown>"
}
