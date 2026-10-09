package remoteclient

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// secrets_race_test.go — the check-then-create race (task #152 story E,
// case 2). EnsureSecret's Get says the Secret is not there yet, but by the
// time it calls Create somebody else has already created one with that
// exact name. This does not fabricate an interleaving by reasoning about
// EnsureSecret's code path — it drives the fake clientset's own
// ObjectTracker so the Create call really does hit a real AlreadyExists
// conflict, the same thing a real API server returns for this race.
//
// The bug this test caught: EnsureSecret used to just wrap whatever error
// Create returned and hand it back — which meant a caller checking
// errors.Is(err, remoteclient.ErrForeignSecret) (internal/secrets/reconciler.go
// does exactly this, and says so in a comment) never saw ErrForeignSecret
// for this exact race, only a generic wrapped Kubernetes conflict error.
// No secret was ever overwritten either way — Create failing means nothing
// was written — but the race was silently misclassified as a plain failure
// instead of the ownership refusal it actually is. Fixed by re-checking
// ownership on an AlreadyExists conflict, the same question the update path
// already asks.

// racedForeignSecret is the secret that "appears" between EnsureSecret's
// Get and its Create — created by somebody else, not Sharko.
func racedForeignSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "addon-creds",
			Namespace: "monitoring",
			Labels:    map[string]string{managedByLabel: "external-secrets"},
		},
		Data: map[string][]byte{"api-key": []byte("theirs")},
	}
}

// secretsGVR is the GroupVersionResource ObjectTracker.Create needs to seed
// an object directly, bypassing the normal reactor chain.
var secretsGVR = corev1.SchemeGroupVersion.WithResource("secrets")

// injectRaceOnFirstGet wires a fake clientset so that the FIRST "get secrets"
// call — EnsureSecret's existence check — behaves exactly as if nothing were
// there yet (a real NotFound), but as a side effect plants racer directly in
// the tracker first. Because the object is now in the tracker, the fake
// clientset's own default Create reactor (unmodified — this only touches
// "get") will reject EnsureSecret's subsequent Create with a real
// AlreadyExists error, precisely mirroring what a real API server does when
// two callers race to create the same name.
func injectRaceOnFirstGet(t *testing.T, client *fake.Clientset, racer *corev1.Secret) {
	t.Helper()
	fired := false
	client.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fired {
			return false, nil, nil // let every later Get answer normally from the tracker
		}
		fired = true
		if err := client.Tracker().Create(secretsGVR, racer, racer.Namespace); err != nil {
			t.Fatalf("seeding the racing secret into the tracker: %v", err)
		}
		return true, nil, apierrors.NewNotFound(corev1.Resource("secrets"), racer.Name)
	})
}

// TestEnsureSecret_CheckThenCreateRace_ForeignWinnerIsRefused is the
// headline: the Secret that wins the race belongs to somebody else, so
// EnsureSecret must refuse with ErrForeignSecret — not overwrite it, and not
// misreport it as a bare API failure.
func TestEnsureSecret_CheckThenCreateRace_ForeignWinnerIsRefused(t *testing.T) {
	client := fake.NewSimpleClientset()
	racer := racedForeignSecret()
	injectRaceOnFirstGet(t, client, racer)

	err := EnsureSecret(context.Background(), client, "monitoring", "addon-creds",
		map[string][]byte{"api-key": []byte("sharkos-new-value")}, nil)

	if err == nil {
		t.Fatal("EnsureSecret succeeded despite losing the create race to somebody else's secret")
	}
	if !errors.Is(err, ErrForeignSecret) {
		t.Errorf("error = %v, want ErrForeignSecret — the race must be classified as an ownership refusal, not a generic failure", err)
	}

	live, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "addon-creds", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("reading back the secret: %v", getErr)
	}
	if string(live.Data["api-key"]) != "theirs" {
		t.Errorf("value = %q, want the racer's untouched value — Sharko overwrote a secret it lost the race for", live.Data["api-key"])
	}
	if live.Labels[managedByLabel] == managedByValue {
		t.Error("the racer's secret was relabeled as Sharko's own")
	}

	// The rejected Create itself shows up as an attempted "create" action
	// (that's the mechanism of the race, and it's expected) — what must
	// never appear is an "update" or a second "create", which would mean
	// EnsureSecret fell back to writing over the winner after losing.
	for _, verb := range mutations(client) {
		if verb == "update" || verb == "delete" {
			t.Errorf("the race produced a %s after losing the create — that must never happen", verb)
		}
	}
}

// TestEnsureSecret_CheckThenCreateRace_SharkosOwnWinnerIsLeftAlone is the
// rarer half of the same race: the Secret that wins is ALSO Sharko's own
// (e.g. two concurrent reconcile passes both trying to create it). Nothing
// dangerous happens either way — Create failing means no write occurred —
// so EnsureSecret must not report ErrForeignSecret for a Secret Sharko
// really does own, and it must not silently claim success for a write that
// never landed.
func TestEnsureSecret_CheckThenCreateRace_SharkosOwnWinnerIsLeftAlone(t *testing.T) {
	client := fake.NewSimpleClientset()
	racer := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "addon-creds",
			Namespace: "monitoring",
			Labels:    map[string]string{managedByLabel: managedByValue},
		},
		Data: map[string][]byte{"api-key": []byte("written-by-the-other-racer")},
	}
	injectRaceOnFirstGet(t, client, racer)

	err := EnsureSecret(context.Background(), client, "monitoring", "addon-creds",
		map[string][]byte{"api-key": []byte("sharkos-new-value")}, nil)

	if err == nil {
		t.Fatal("EnsureSecret reported success for a Create that the API server actually rejected")
	}
	if errors.Is(err, ErrForeignSecret) {
		t.Error("a Secret Sharko itself owns was classified as foreign")
	}

	live, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "addon-creds", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("reading back the secret: %v", getErr)
	}
	if string(live.Data["api-key"]) != "written-by-the-other-racer" {
		t.Errorf("value = %q, want the other racer's value untouched — this call's Create never landed", live.Data["api-key"])
	}
}
