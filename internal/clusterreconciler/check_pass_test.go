package clusterreconciler

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// check_pass_test.go — P1-A A2. The connection engine's Refresh finally
// only looks.
//
// The rule these tests hold in place: a check performs Gets and Lists and
// nothing else. If any of them ever sees a create, update, patch or delete
// come out of checkOnce, the page's "Refresh" button has started writing
// again and the whole lane is undone.

// checkWriteActions returns every mutating action a fake clientset saw.
// Get and List are reads and are deliberately not counted.
func checkWriteActions(client *fake.Clientset) []string {
	var out []string
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "delete-collection":
			out = append(out, a.GetVerb()+" "+a.GetResource().Resource)
		}
	}
	return out
}

func checkVault(names ...string) *fakeVault {
	creds := make(map[string]*providers.Kubeconfig, len(names))
	for _, n := range names {
		creds[n] = &providers.Kubeconfig{
			Server: "https://" + n + ".example.com",
			CAData: []byte("fake-ca-bytes"),
			Token:  "fake-token",
		}
	}
	return &fakeVault{creds: creds}
}

// sharkoOwnedSecret builds a live ArgoCD cluster Secret Sharko owns, with
// the given addon labels on top of the ownership label.
func sharkoOwnedSecret(name string, addonLabels map[string]string) *corev1.Secret {
	labels := map[string]string{LabelManagedBy: LabelValueSharko}
	for k, v := range addonLabels {
		labels[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: DefaultArgoCDNamespace, Labels: labels},
		Data:       map[string][]byte{"server": []byte("https://" + name + ".example.com")},
	}
}

// TestCheckOnce_WritesNothing is acceptance criterion 2: a check-only pass
// performs zero writes against Kubernetes while still updating the records
// the page reads.
func TestCheckOnce_WritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// A deliberately busy estate: one cluster in sync, one drifted, one in
	// git with no secret at all, and one live secret git has never heard of
	// (the write pass would delete that one).
	body := []byte("apiVersion: sharko.dev/v1\n" +
		"kind: ManagedClusters\n" +
		"metadata:\n  name: managed-clusters\n" +
		"spec:\n  clusters:\n" +
		"    - name: in-sync\n      labels:\n        datadog: enabled\n" +
		"    - name: drifted\n      labels:\n        datadog: enabled\n" +
		"    - name: no-secret-yet\n      labels: {}\n")

	client := fake.NewSimpleClientset(
		sharkoOwnedSecret("in-sync", map[string]string{"datadog": "enabled"}),
		sharkoOwnedSecret("drifted", nil), // git wants datadog: enabled, live has nothing
		sharkoOwnedSecret("orphan", nil),  // live, but absent from git
	)
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, client, checkVault("in-sync", "drifted", "no-secret-yet"), audits, body)

	r.checkOnce(ctx)

	if got := checkWriteActions(client); len(got) != 0 {
		t.Fatalf("the check pass wrote to Kubernetes: %v", got)
	}
	// Every secret it started with is still there — including the orphan
	// the write pass would have swept.
	for _, name := range []string{"in-sync", "drifted", "orphan"} {
		if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
			t.Errorf("secret %q is gone after a check: %v", name, err)
		}
	}

	// And the records the page reads were genuinely updated.
	if rec, ok := r.LastReconcile("in-sync"); !ok || rec.Outcome != OutcomeSucceeded || rec.LabelDrift != nil {
		t.Errorf("in-sync record = %+v, ok=%v — want succeeded with no drift", rec, ok)
	}
	rec, ok := r.LastReconcile("drifted")
	if !ok || rec.Outcome != OutcomeSucceeded || rec.LabelDrift == nil {
		t.Fatalf("drifted record = %+v, ok=%v — want succeeded WITH drift", rec, ok)
	}
	if len(rec.LabelDrift.Added) != 1 || rec.LabelDrift.Added[0] != "datadog" {
		t.Errorf("drift = %+v, want the missing datadog label named", rec.LabelDrift)
	}
	if rec, ok := r.LastReconcile("no-secret-yet"); !ok || rec.Message != ManagedSecretNotCreatedMessage {
		t.Errorf("no-secret-yet record = %+v, ok=%v — want the plain 'not created yet' message", rec, ok)
	}
}

// TestCheckOnce_LeavesAnUnlabeledSecretAlone — a same-name secret somebody
// else created is reported, never adopted and never overwritten.
func TestCheckOnce_LeavesAnUnlabeledSecretAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-eu", Namespace: DefaultArgoCDNamespace},
		Data:       map[string][]byte{"server": []byte("https://prod-eu.example.com")},
	})
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, client, checkVault("prod-eu"), audits, envelopedManagedClusters("prod-eu"))

	r.checkOnce(ctx)

	if got := checkWriteActions(client); len(got) != 0 {
		t.Fatalf("the check pass wrote to somebody else's secret: %v", got)
	}
	rec, ok := r.LastReconcile("prod-eu")
	if !ok || rec.Outcome != OutcomeSkipped || rec.Message != UnlabeledSecretExistsMessage {
		t.Errorf("record = %+v, ok=%v — want a skip naming the unlabeled secret", rec, ok)
	}
}

// TestCheckOnce_SelfManagedSecretNotCreatedYet — the user owns that secret,
// so its absence gets the user's own wording, not Sharko's.
func TestCheckOnce_SelfManagedSecretNotCreatedYet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte("apiVersion: sharko.dev/v1\n" +
		"kind: ManagedClusters\n" +
		"metadata:\n  name: managed-clusters\n" +
		"spec:\n  clusters:\n    - name: byo\n      connectionManagedBy: user\n      labels: {}\n")

	client := fake.NewSimpleClientset()
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, client, checkVault("byo"), audits, body)

	r.checkOnce(ctx)

	if got := checkWriteActions(client); len(got) != 0 {
		t.Fatalf("the check pass wrote something: %v", got)
	}
	rec, ok := r.LastReconcile("byo")
	if !ok || rec.Message != SelfManagedSecretNotCreatedMessage {
		t.Errorf("record = %+v, ok=%v — want the self-managed waiting message", rec, ok)
	}
}

// TestTriggerCheck_RunsTheCheckAndNotTheWritePass — acceptance criterion 3,
// engine side: the check channel drives checkFn, and the ticker/Trigger
// path still drives the write pass. Both seams are swapped so a swap of the
// two in run() fails loudly.
func TestTriggerCheck_RunsTheCheckAndNotTheWritePass(t *testing.T) {
	t.Parallel()

	polled := make(chan struct{}, 4)
	checked := make(chan struct{}, 4)

	r := New(Deps{
		GitProvider:  func() gitprovider.GitProvider { return &fakeGit{files: map[string][]byte{}} },
		ArgoClient:   fake.NewSimpleClientset(),
		Vault:        checkVault(),
		AuditFn:      func(audit.Entry) {},
		TickInterval: time.Hour, // the ticker must not fire during this test
	})
	r.pollFn = func(context.Context) { polled <- struct{}{} }
	r.checkFn = func(context.Context) { checked <- struct{}{} }
	r.Start(context.Background())
	defer r.Stop()

	// P2-D D5: Start() now runs one immediate write pass before entering
	// the select loop, so the FIRST value on polled is that startup pass,
	// not a signal about TriggerCheck. Drain it before asserting anything
	// about what TriggerCheck itself did or didn't fire.
	select {
	case <-polled:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not run its immediate write pass")
	}

	r.TriggerCheck()
	select {
	case <-checked:
	case <-time.After(2 * time.Second):
		t.Fatal("TriggerCheck did not run the check pass")
	}
	select {
	case <-polled:
		t.Fatal("TriggerCheck ran the WRITE pass — Refresh must never write")
	case <-time.After(150 * time.Millisecond):
	}

	// The write nudge still writes: the scheduled loop's behaviour is
	// deliberately unchanged by this lane.
	r.Trigger()
	select {
	case <-polled:
	case <-time.After(2 * time.Second):
		t.Fatal("Trigger no longer runs the write pass — the GitOps loop must keep enforcing")
	}
}

// TestScheduledTick_StillRunsTheWritePass — acceptance criterion 3, the
// other half: the periodic tick is still the write pass. This is the scope
// line of the whole lane written as a test.
func TestScheduledTick_StillRunsTheWritePass(t *testing.T) {
	t.Parallel()

	polled := make(chan struct{}, 4)
	r := New(Deps{
		GitProvider:  func() gitprovider.GitProvider { return &fakeGit{files: map[string][]byte{}} },
		ArgoClient:   fake.NewSimpleClientset(),
		Vault:        checkVault(),
		AuditFn:      func(audit.Entry) {},
		TickInterval: 20 * time.Millisecond,
	})
	r.pollFn = func(context.Context) { polled <- struct{}{} }
	r.checkFn = func(context.Context) { t.Error("the scheduled tick ran the check pass instead of the write pass") }
	r.Start(context.Background())
	defer r.Stop()

	select {
	case <-polled:
	case <-time.After(2 * time.Second):
		t.Fatal("the scheduled tick never ran the write pass")
	}
}

// TestCheckOnce_RealWritePassStillCreatesAndDeletes — the control. Given
// exactly the estate TestCheckOnce_WritesNothing checks, the WRITE pass
// does create and delete. Without this, "the check wrote nothing" could be
// passing because the fixture had nothing to do.
func TestCheckOnce_RealWritePassStillCreatesAndDeletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset(sharkoOwnedSecret("orphan", nil))
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, client, checkVault("prod-eu"), audits, envelopedManagedClusters("prod-eu"))

	r.pollOnce(ctx)

	if got := checkWriteActions(client); len(got) == 0 {
		t.Fatal("the write pass wrote nothing — the fixture proves nothing about the check pass")
	}
	if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{}); err != nil {
		t.Errorf("the write pass did not create the missing secret: %v", err)
	}
	if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "orphan", metav1.GetOptions{}); err == nil {
		t.Error("the write pass did not sweep the orphan")
	}
}
