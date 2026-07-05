package clusterreconciler

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-57.2 — self-managed connections (connectionManagedBy: user).
//
// The user creates and maintains the ArgoCD cluster Secret; Sharko is a
// guest that syncs addon labels onto it and NOTHING else. These tests pin
// the canonical reconciler's contract:
//
//  1. No Secret creation for self-managed entries — even when the user's
//     Secret does not exist yet (visible pending state, no error loop).
//  2. Label-only merge onto an existing user-created Secret: addon labels
//     converge; Data/StringData stay byte-identical; no managed-by, no
//     secret-type, no connectivity-check label is ever stamped.
//  3. Mode-switch handover (sharko → user): a leftover managed-by=sharko
//     label is stripped (non-adopted Secrets) so the orphan sweep can never
//     reclaim the user's connection later.
//  4. Adopted Secrets keep their managed-by label + adopted annotation.
//  5. Sharko-managed entries in the same spec still get their Secret
//     created exactly as before (mode partition does not leak).

// envelopedManagedClustersWithModes renders an envelope where each entry
// may carry connectionManagedBy + addon labels.
type testClusterEntry struct {
	Name    string
	Mode    string // "" = omit (sharko default)
	Labels  map[string]string
}

func envelopedWithModes(entries ...testClusterEntry) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: sharko.io/v1\n")
	b.WriteString("kind: ManagedClusters\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: managed-clusters\n")
	b.WriteString("spec:\n")
	if len(entries) == 0 {
		b.WriteString("  clusters: []\n")
		return []byte(b.String())
	}
	b.WriteString("  clusters:\n")
	for _, e := range entries {
		b.WriteString("    - name: " + e.Name + "\n")
		if e.Mode != "" {
			b.WriteString("      connectionManagedBy: " + e.Mode + "\n")
		}
		if len(e.Labels) > 0 {
			b.WriteString("      labels:\n")
			for k, v := range e.Labels {
				b.WriteString("        " + k + ": " + v + "\n")
			}
		}
	}
	return []byte(b.String())
}

// userSecret builds a user-created ArgoCD cluster Secret the way the
// operator guide instructs: secret-type label set by the user, NO
// sharko managed-by label, connection config in Data.
func userSecret(name string, extraLabels map[string]string) *corev1.Secret {
	labels := map[string]string{
		"argocd.argoproj.io/secret-type": "cluster",
	}
	for k, v := range extraLabels {
		labels[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: DefaultArgoCDNamespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(name),
			"server": []byte("https://user-cluster.example.com:6443"),
			"config": []byte(`{"bearerToken":"user-owned-token","tlsClientConfig":{"insecure":false,"caData":"dXNlci1jYQ=="}}`),
		},
	}
}

func TestSelfManaged_MissingUserSecret_PendingNotError(t *testing.T) {
	client := fake.NewSimpleClientset() // no Secret exists yet
	audits := &auditCollector{}
	body := envelopedWithModes(testClusterEntry{
		Name: "byo-conn", Mode: "user", Labels: map[string]string{"monitoring": "enabled"},
	})
	// Vault must never be consulted for a self-managed cluster's Secret
	// creation — give it nothing so an accidental create would fail loudly.
	r := newReconcilerForTest(t, nil, client, &fakeVault{}, audits, body)

	r.pollOnce(context.Background())

	// No Secret was created.
	if all := secretsListUnfiltered(t, client, DefaultArgoCDNamespace); len(all) != 0 {
		t.Fatalf("expected NO secret writes for self-managed cluster without a user Secret, found %d", len(all))
	}
	// Visible pending state: audit event fired, and it is NOT an error.
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_user_pending", "byo-conn") {
		t.Fatalf("expected cluster_secret_user_pending audit event, got %+v", entries)
	}
	for _, e := range entries {
		if e.Result == "failure" {
			t.Fatalf("pending user Secret must not audit as failure: %+v", e)
		}
	}
	// Summary must report user_pending, not errors.
	found := false
	for _, e := range entries {
		if e.Event == "cluster_secret_reconcile_tick" {
			found = true
			if !strings.Contains(e.Resource, "user_pending:1") || !strings.Contains(e.Resource, "errors:0") {
				t.Fatalf("summary should count user_pending:1 errors:0, got %q", e.Resource)
			}
		}
	}
	if !found {
		t.Fatal("expected summary audit entry")
	}
}

func TestSelfManaged_LabelOnlyMerge_DataUntouched(t *testing.T) {
	secret := userSecret("byo-conn", map[string]string{"team": "payments"})
	client := fake.NewSimpleClientset(secret)
	audits := &auditCollector{}
	body := envelopedWithModes(testClusterEntry{
		Name: "byo-conn", Mode: "user",
		Labels: map[string]string{"monitoring": "enabled", "logging": "disabled"},
	})
	r := newReconcilerForTest(t, nil, client, &fakeVault{}, audits, body)

	r.pollOnce(context.Background())

	got, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "byo-conn", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("user secret vanished: %v", err)
	}
	// Addon labels merged.
	if got.Labels["monitoring"] != "enabled" || got.Labels["logging"] != "disabled" {
		t.Fatalf("addon labels not synced: %v", got.Labels)
	}
	// Foreign labels kept.
	if got.Labels["team"] != "payments" {
		t.Fatalf("foreign label dropped: %v", got.Labels)
	}
	if got.Labels["argocd.argoproj.io/secret-type"] != "cluster" {
		t.Fatalf("user's secret-type label dropped: %v", got.Labels)
	}
	// Sharko must NOT claim ownership or stamp guest-forbidden labels.
	if _, has := got.Labels[LabelManagedBy]; has {
		t.Fatalf("managed-by label must NOT be stamped on a user-owned Secret: %v", got.Labels)
	}
	if _, has := got.Labels[models.LabelConnectivityCheck]; has {
		t.Fatalf("connectivity-check label must NOT be stamped on a user-owned Secret: %v", got.Labels)
	}
	// Connection data byte-identical.
	for k, want := range secret.Data {
		if string(got.Data[k]) != string(want) {
			t.Fatalf("Data[%q] mutated: got %q want %q", k, got.Data[k], want)
		}
	}
	if len(got.Data) != len(secret.Data) {
		t.Fatalf("Data key set changed: got %d keys want %d", len(got.Data), len(secret.Data))
	}
	if len(got.StringData) != 0 {
		t.Fatalf("StringData must stay empty on a label-only sync: %v", got.StringData)
	}
	if !hasEventForResource(audits.Snapshot(), "cluster_secret_user_label_sync", "byo-conn") {
		t.Fatal("expected cluster_secret_user_label_sync audit event")
	}
}

func TestSelfManaged_LegacyBoolLabels_NormalizedOnSync(t *testing.T) {
	// Pre-V2-cleanup-20 entries carry "true"/"false" — the label-only sync
	// must upgrade them exactly like the create path does.
	client := fake.NewSimpleClientset(userSecret("byo-conn", nil))
	audits := &auditCollector{}
	body := envelopedWithModes(testClusterEntry{
		Name: "byo-conn", Mode: "user",
		Labels: map[string]string{"monitoring": "\"true\""},
	})
	r := newReconcilerForTest(t, nil, client, &fakeVault{}, audits, body)

	r.pollOnce(context.Background())

	got, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "byo-conn", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Labels["monitoring"] != models.LabelEnabled {
		t.Fatalf("legacy true not normalized to enabled: %v", got.Labels)
	}
}

func TestSelfManaged_ModeSwitch_StripsSharkoOwnershipLabel(t *testing.T) {
	// A cluster that used to be Sharko-managed: its Secret carries the
	// managed-by=sharko label. The entry flips to connectionManagedBy: user
	// → the next tick must strip the ownership label (so the orphan sweep
	// can never delete the user's connection later) and leave Data alone.
	secret := userSecret("switched", nil)
	secret.Labels[LabelManagedBy] = LabelValueSharko
	client := fake.NewSimpleClientset(secret)
	audits := &auditCollector{}
	body := envelopedWithModes(testClusterEntry{
		Name: "switched", Mode: "user", Labels: map[string]string{"monitoring": "enabled"},
	})
	r := newReconcilerForTest(t, nil, client, &fakeVault{}, audits, body)

	r.pollOnce(context.Background())

	got, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "switched", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, has := got.Labels[LabelManagedBy]; has {
		t.Fatalf("mode switch must strip the managed-by ownership label: %v", got.Labels)
	}
	if got.Labels["monitoring"] != "enabled" {
		t.Fatalf("addon labels not synced during handover: %v", got.Labels)
	}
	for k, want := range secret.Data {
		if string(got.Data[k]) != string(want) {
			t.Fatalf("Data[%q] mutated during handover: got %q want %q", k, got.Data[k], want)
		}
	}

	// After the strip, a later tick with the cluster REMOVED from git must
	// not delete the Secret — it is invisible to the sharko-labeled sweep.
	r2 := newReconcilerForTest(t, nil, client, &fakeVault{}, &auditCollector{}, envelopedWithModes())
	r2.pollOnce(context.Background())
	if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "switched", metav1.GetOptions{}); err != nil {
		t.Fatalf("user-owned Secret must survive removal from git: %v", err)
	}
}

func TestSelfManaged_AdoptedSecret_KeepsManagedByLabel(t *testing.T) {
	// Adopted Secrets carry managed-by=sharko + the adopted annotation.
	// The adopt rail (Unadopt, orphan-sweep immunity) depends on both —
	// the self-managed label sync must NOT strip them.
	secret := userSecret("adopted-one", nil)
	secret.Labels[LabelManagedBy] = LabelValueSharko
	secret.Annotations = map[string]string{annotationAdopted: "true"}
	client := fake.NewSimpleClientset(secret)
	audits := &auditCollector{}
	body := envelopedWithModes(testClusterEntry{
		Name: "adopted-one", Mode: "user", Labels: map[string]string{"monitoring": "enabled"},
	})
	r := newReconcilerForTest(t, nil, client, &fakeVault{}, audits, body)

	r.pollOnce(context.Background())

	got, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "adopted-one", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Labels[LabelManagedBy] != LabelValueSharko {
		t.Fatalf("adopted Secret must keep the managed-by label: %v", got.Labels)
	}
	if got.Annotations[annotationAdopted] != "true" {
		t.Fatalf("adopted annotation must survive the label sync: %v", got.Annotations)
	}
	if got.Labels["monitoring"] != "enabled" {
		t.Fatalf("addon labels must still sync on an adopted Secret: %v", got.Labels)
	}
}

func TestSelfManaged_PartitionDoesNotLeak_SharkoEntriesStillCreated(t *testing.T) {
	// Mixed spec: one Sharko-managed entry (Secret must be created from
	// vault creds, exactly as before 57.2) and one self-managed entry
	// (no Secret creation, pending state).
	client := fake.NewSimpleClientset()
	audits := &auditCollector{}
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"sharko-owned": {Server: "https://sharko-owned.example.com:6443", Token: "tok", CAData: []byte("ca")},
	}}
	body := envelopedWithModes(
		testClusterEntry{Name: "sharko-owned", Labels: map[string]string{"monitoring": "enabled"}},
		testClusterEntry{Name: "byo-conn", Mode: "user", Labels: map[string]string{"monitoring": "enabled"}},
	)
	r := newReconcilerForTest(t, nil, client, vault, audits, body)

	r.pollOnce(context.Background())

	// Sharko-managed Secret created, with ownership label + secret-type.
	created, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "sharko-owned", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("sharko-managed Secret must still be created: %v", err)
	}
	if !IsManagedBySharko(created) {
		t.Fatalf("sharko-managed Secret must carry the ownership label: %v", created.Labels)
	}
	// Self-managed Secret NOT created.
	if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), "byo-conn", metav1.GetOptions{}); err == nil {
		t.Fatal("self-managed cluster must NOT get a Sharko-created Secret")
	}
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_create", "sharko-owned") {
		t.Fatal("expected create audit for the sharko-managed cluster")
	}
	if !hasEventForResource(entries, "cluster_secret_user_pending", "byo-conn") {
		t.Fatal("expected pending audit for the self-managed cluster")
	}
}

func TestSelfManaged_IdempotentSecondTick_NoMutations(t *testing.T) {
	// Once labels are converged, subsequent ticks must not write anything.
	client := fake.NewSimpleClientset(userSecret("byo-conn", nil))
	audits := &auditCollector{}
	body := envelopedWithModes(testClusterEntry{
		Name: "byo-conn", Mode: "user", Labels: map[string]string{"monitoring": "enabled"},
	})
	r := newReconcilerForTest(t, nil, client, &fakeVault{}, audits, body)

	r.pollOnce(context.Background())
	afterFirst := countMutations(client)
	if afterFirst == 0 {
		t.Fatal("first tick should have synced labels (one update expected)")
	}
	r.pollOnce(context.Background())
	if got := countMutations(client); got != afterFirst {
		t.Fatalf("second tick must be a no-op: mutations went %d → %d", afterFirst, got)
	}
}
