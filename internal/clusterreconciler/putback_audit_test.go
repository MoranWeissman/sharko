package clusterreconciler

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// TestPollOnce_PutsBackADeletedSecret_WritesOneHonestLine pins ep4 round R,
// R7. The Repair click's own activity line moved to the request (one click,
// one line), and the reconciler's repair function stopped writing lines. That
// must not have taken the reconciler's OWN real actions with it.
//
// Someone deletes a connection Secret Sharko owns. On the next pass the
// reconciler puts it back. That is a real write nobody clicked for, so it
// needs exactly one activity line, credited to the reconciler, that says what
// happened: the Secret was created. It must not say "repaired", because
// nobody asked for a repair.
func TestPollOnce_PutsBackADeletedSecret_WritesOneHonestLine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://prod-eu.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	k8sClient := fake.NewSimpleClientset()
	first := &auditCollector{}
	r := newReconcilerForTest(t, nil, k8sClient, vault, first, envelopedManagedClusters("prod-eu"))
	r.pollOnce(ctx)
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{}); err != nil {
		t.Fatalf("the first pass did not create the Secret, so this proves nothing: %v", err)
	}

	// Someone deletes it.
	if err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Delete(ctx, "prod-eu", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting the Secret: %v", err)
	}

	// Collect only what the put-back pass writes.
	second := &auditCollector{}
	r.deps.AuditFn = second.Add
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{}); err != nil {
		t.Fatalf("the reconciler did not put the deleted Secret back: %v", err)
	}

	var lines []audit.Entry
	for _, e := range second.Snapshot() {
		if e.Resource != "cluster:prod-eu" {
			continue
		}
		lines = append(lines, e)
		if strings.Contains(e.Event, "repair") {
			t.Errorf("putting back a deleted Secret wrote a %q line — nobody clicked Repair", e.Event)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("putting back one deleted Secret wrote %d activity lines for it, want exactly 1: %+v", len(lines), lines)
	}
	e := lines[0]
	if e.Event != string(EventClusterSecretCreate) {
		t.Errorf("event = %q, want %q (the Secret was created again)", e.Event, EventClusterSecretCreate)
	}
	if e.Source != "reconciler" {
		t.Errorf("source = %q, want reconciler — the background pass did this, not a person", e.Source)
	}
	if e.Result != "success" || e.Changes != audit.ChangesApplied {
		t.Errorf("result/changes = %q/%q, want success/applied", e.Result, e.Changes)
	}
	if strings.Contains(strings.ToLower(e.Detail), "repair") {
		t.Errorf("the put-back line's detail says %q — it must not say repaired", e.Detail)
	}
}
