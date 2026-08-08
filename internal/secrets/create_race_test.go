package secrets

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// create_race_test.go — task #152 story E, case 2, exercised through the
// FULL production path (planPushes -> reconcileSecret -> EnsureSecret), not
// just the remoteclient package in isolation. reconciler.go's own comment at
// the create call site says the choke point turns this race into
// ItemOutcomeForeign; this test proves the row the UI reads actually says
// that, using a real fake-clientset AlreadyExists conflict rather than
// reasoning about the code.

var secretsGVR = corev1.SchemeGroupVersion.WithResource("secrets")

// TestReconcile_CheckThenCreateRace_RecordsForeignNotError starts with an
// empty cluster (the secret genuinely doesn't exist when the pass begins),
// but seeds a foreign secret into the fake clientset's tracker in the gap
// between the reconciler's own existence check and EnsureSecret's own
// check-before-create — the second "get secrets" call the whole pass makes
// — so the eventual Create hits a real conflict.
func TestReconcile_CheckThenCreateRace_RecordsForeignNotError(t *testing.T) {
	client := fake.NewSimpleClientset()
	racer := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			Labels:    map[string]string{managedByLabelKey: "external-secrets"},
		},
		Data: map[string][]byte{"api-key": []byte("theirs")},
	}

	gets := 0
	client.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets == 2 {
			if err := client.Tracker().Create(secretsGVR, racer, racer.Namespace); err != nil {
				t.Fatalf("seeding the racing secret: %v", err)
			}
			return true, nil, apierrors.NewNotFound(corev1.Resource("secrets"), racer.Name)
		}
		return false, nil, nil
	})

	r := foreignGateReconciler(client)
	r.reconcile()

	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeForeign) {
		t.Fatalf("LastItemOutcome = (%q, %v), want (%q, true) — a check-then-create race must record the same boundary a same-pass foreign secret does",
			outcome, ok, ItemOutcomeForeign)
	}
	// Foreign is a boundary, not a failure — the row must carry no error
	// text and the stats' error counter must stay at zero, same contract
	// TestPeriodicPass_NeverWritesAForeignSecret pins for the ordinary case.
	if msg, hasErr := r.LastItemError("prod-cluster", "datadog"); hasErr {
		t.Errorf("a raced-foreign secret recorded an error: %q", msg)
	}
	if stats := r.GetStats().(ReconcileStats); stats.Errors != 0 || stats.Created != 0 || stats.Updated != 0 {
		t.Errorf("stats = %+v, want no writes and no errors for a raced-foreign secret", stats)
	}

	live, err := client.CoreV1().Secrets("monitoring").Get(t.Context(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the live secret back: %v", err)
	}
	if string(live.Data["api-key"]) != "theirs" {
		t.Errorf("the racer's content changed to %q", live.Data["api-key"])
	}
}
