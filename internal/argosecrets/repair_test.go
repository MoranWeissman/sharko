package argosecrets

// repair_test.go — the write primitive's own rules.
//
// Each test here pins one of R3-1's fifteen numbered rules at the level where
// the rule actually lives: in the writer. The endpoint's tests
// (internal/api/connection_repair_test.go) pin the rules that live in the
// handler.

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// repairSentinel is a made-up credential value used only in this file. It is
// deliberately not shaped like a real token from any provider.
const repairSentinel = "r7t3w9qxrepair-primitive-sentinel-do-not-copy"

const repairCluster = "prod-eu"

// ownedLiveSecret builds a live connection Secret Sharko owns, carrying a
// foreign label and a foreign data key that every repair must leave alone.
func ownedLiveSecret(labels map[string]string, data map[string]string, annotations map[string]string) *corev1.Secret {
	l := map[string]string{
		LabelManagedBy:  ManagedByValue,
		LabelSecretType: "cluster",
	}
	for k, v := range labels {
		l[k] = v
	}
	d := map[string][]byte{}
	for k, v := range data {
		d[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        repairCluster,
			Namespace:   "argocd",
			Labels:      l,
			Annotations: annotations,
		},
		Type: corev1.SecretTypeOpaque,
		Data: d,
	}
}

// desiredFor builds the connection Secret the canonical builder produces, which
// is what a caller must hand the repair.
func desiredFor(t *testing.T, labels map[string]string) *corev1.Secret {
	t.Helper()
	built, err := BuildClusterSecret(ClusterSecretSpec{
		Name:   repairCluster,
		Server: "https://" + repairCluster + ".invalid",
		Token:  repairSentinel,
		CAData: "dGVzdC1jYS1ieXRlcw==",
		Labels: labels,
	}, "argocd")
	if err != nil {
		t.Fatalf("building the desired secret: %v", err)
	}
	return built
}

// --- rule 6: never delete and recreate -------------------------------------

// TestRepair_NeverDeletesAndNeverCreates is rule 6. A delete-and-recreate would
// lose every field Sharko does not model and would briefly leave ArgoCD with no
// connection at all, so the only verb a repair may use on the object is update.
func TestRepair_NeverDeletesAndNeverCreates(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairCluster, "server": "https://stale.invalid", "config": `{"bearerToken":"old"}`},
		nil)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	out, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !out.Changed {
		t.Fatal("the repair reported no change on a genuinely drifted connection")
	}

	sawUpdate := false
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "delete", "deletecollection":
			t.Errorf(`the repair issued a %q.

Rule 6: never delete and recreate. A delete loses every field Sharko does not model, breaks anything watching the object, and leaves ArgoCD with no connection for the gap.`, a.GetVerb())
		case "create":
			t.Error("the repair issued a create; a repair is an in-place update of something that already exists")
		case "update":
			sawUpdate = true
		}
	}
	if !sawUpdate {
		t.Error("the repair issued no update at all")
	}
}

// --- rule 5: only owned fields change --------------------------------------

// TestRepair_LeavesForeignLabelsAnnotationsAndDataKeysAlone is rule 5. Every
// piece of the object that is somebody else's survives a full rewrite of the
// parts that are Sharko's.
func TestRepair_LeavesForeignLabelsAnnotationsAndDataKeysAlone(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{
			"datadog":                    "disabled",
			"some-other-tool.io/owner":   "them",
			"app.kubernetes.io/instance": "argocd-tracking",
		},
		map[string]string{
			"name":             repairCluster,
			"server":           "https://stale.invalid",
			"config":           `{"bearerToken":"old"}`,
			"shard":            "3",
			"namespaces":       "team-a,team-b",
			"clusterResources": "true",
		},
		map[string]string{
			"their-tool.io/note":  "please keep me",
			"sharko.dev/whatever": "also keep me",
		})
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	if _, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil); err != nil {
		t.Fatalf("repair: %v", err)
	}

	after, err := client.CoreV1().Secrets("argocd").Get(context.Background(), repairCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	// Foreign labels survive.
	for key, want := range map[string]string{
		"some-other-tool.io/owner":   "them",
		"app.kubernetes.io/instance": "argocd-tracking",
	} {
		if got := after.Labels[key]; got != want {
			t.Errorf("foreign label %q = %q, want %q — a repair must not touch a label that is not Sharko's", key, got, want)
		}
	}

	// Foreign data keys survive, byte for byte.
	for key, want := range map[string]string{
		"shard":            "3",
		"namespaces":       "team-a,team-b",
		"clusterResources": "true",
	} {
		if got := string(after.Data[key]); got != want {
			t.Errorf("foreign data key %q = %q, want %q — this is connection config Sharko does not model and must preserve", key, got, want)
		}
	}

	// Foreign annotations survive.
	for key, want := range map[string]string{
		"their-tool.io/note":  "please keep me",
		"sharko.dev/whatever": "also keep me",
	} {
		if got := after.Annotations[key]; got != want {
			t.Errorf("annotation %q = %q, want %q", key, got, want)
		}
	}

	// And the owned fields really did change, or the test above proves nothing.
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("the owned addon label was not repaired: datadog = %q, want enabled", after.Labels["datadog"])
	}
	if string(after.Data["server"]) != "https://"+repairCluster+".invalid" {
		t.Errorf("the owned server field was not repaired: %q", string(after.Data["server"]))
	}
}

// TestRepair_RemovesSharkoAddonLabelsGitNoLongerDeclares: full convergence over
// Sharko's own addon keys. A leftover enabled label keeps an addon deployed
// after it was turned off in git, so "repaired" has to mean the key is gone.
func TestRepair_RemovesSharkoAddonLabelsGitNoLongerDeclares(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{"datadog": "enabled", "logging": "enabled"},
		map[string]string{"name": repairCluster, "server": "https://" + repairCluster + ".invalid"},
		nil)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	// Git now declares only datadog.
	if _, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil); err != nil {
		t.Fatalf("repair: %v", err)
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), repairCluster, metav1.GetOptions{})
	if _, still := after.Labels["logging"]; still {
		t.Error("an addon label git no longer declares survived the repair — the addon would keep deploying")
	}
	if after.Labels["datadog"] != "enabled" {
		t.Error("the addon label git still declares was lost")
	}
}

// TestRepair_NeverTouchesTakeoverPreservedLabels: a takeover recorded the
// previous owner's label keys precisely so later actions have a list instead of
// a guess. Those keys are outside Sharko's scope, and an unqualified one is
// exactly what IsAddonLabelKey would otherwise call Sharko's own.
func TestRepair_NeverTouchesTakeoverPreservedLabels(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{"env": "prod", "team": "platform", "datadog": "disabled"},
		map[string]string{"name": repairCluster, "server": "https://" + repairCluster + ".invalid"},
		map[string]string{AnnotationTakeoverPreservedLabels: "env,team"})
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	if _, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil); err != nil {
		t.Fatalf("repair: %v", err)
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), repairCluster, metav1.GetOptions{})
	if after.Labels["env"] != "prod" {
		t.Errorf(`a takeover-preserved label was removed by the repair (env = %q).

These keys are unqualified, so IsAddonLabelKey calls them Sharko's — the preserved-label record is the only thing that says otherwise, and the takeover promised the user they would be kept.`, after.Labels["env"])
	}
	if after.Labels["team"] != "platform" {
		t.Errorf("takeover-preserved label team = %q, want platform", after.Labels["team"])
	}
}

// --- rules 3 and 4: the ownership recheck immediately before the write ------

// TestRepair_RefusesWhenAnotherOwnerAppeared is rule 4 in its simple form: the
// live Secret already names another tool when the repair reads it.
func TestRepair_RefusesWhenAnotherOwnerAppeared(t *testing.T) {
	live := ownedLiveSecret(nil,
		map[string]string{"name": repairCluster, "server": "https://" + repairCluster + ".invalid", "config": `{"bearerToken":"theirs"}`},
		nil)
	live.Labels[LabelManagedBy] = "another-tool"
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	_, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if !errors.Is(err, ErrRepairOwnershipChanged) {
		t.Fatalf("err = %v, want ErrRepairOwnershipChanged", err)
	}
	assertNoWriteReached(t, client)
	assertSecretUnchanged(t, client, live)
}

// TestRepair_RefusesAnUnlabeledSecret: no ownership marker at all is Adopt
// territory, never something to overwrite.
func TestRepair_RefusesAnUnlabeledSecret(t *testing.T) {
	live := ownedLiveSecret(nil,
		map[string]string{"name": repairCluster, "server": "https://" + repairCluster + ".invalid"}, nil)
	delete(live.Labels, LabelManagedBy)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	_, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if !errors.Is(err, ErrRepairOwnershipChanged) {
		t.Fatalf("err = %v, want ErrRepairOwnershipChanged — an unlabeled Secret is Adopt territory", err)
	}
	assertNoWriteReached(t, client)
}

// TestRepair_RefusesAnAdoptedSecret: an adopted connection is a guest
// arrangement — the credential material is the user's, and only the addon labels
// are Sharko's. A full repair must never reach one.
func TestRepair_RefusesAnAdoptedSecret(t *testing.T) {
	live := ownedLiveSecret(nil,
		map[string]string{"name": repairCluster, "server": "https://" + repairCluster + ".invalid"},
		map[string]string{AnnotationAdopted: "true"})
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	_, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if !errors.Is(err, ErrRepairOwnershipChanged) {
		t.Fatalf("err = %v, want ErrRepairOwnershipChanged for an adopted connection", err)
	}
	assertNoWriteReached(t, client)
}

// TestRepair_SendsTheResourceVersionItRead is the other half of rule 3, and it
// is the half that covers the case the recheck cannot see.
//
// The recheck reads the ownership label off the object the Get returned. If
// somebody takes the connection over AFTER that Get, no amount of re-checking in
// Sharko can notice — the only thing that can is Kubernetes itself. So the
// Update must carry the resourceVersion from the Get, which makes the API server
// perform a compare-and-swap and reject a write built on a stale read.
//
// This asserts the mechanism rather than the outcome, on purpose. The fake
// clientset does NOT implement optimistic concurrency — it happily accepts an
// Update with any resourceVersion — so a test that mutated the object mid-flight
// and expected a conflict would pass or fail based on the fake's behaviour, not
// on Sharko's. What Sharko controls is whether it sends the version it read, and
// that is exactly what is checked here. The handling of the conflict the real API
// server would then return is pinned separately by
// TestRepair_ConflictIsItsOwnAnswer.
func TestRepair_SendsTheResourceVersionItRead(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairCluster, "server": "https://stale.invalid", "config": `{"bearerToken":"old"}`},
		nil)
	live.ResourceVersion = "424242"
	client := fake.NewSimpleClientset(live)

	var sentResourceVersion string
	var sawUpdate bool
	client.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		update, ok := action.(k8stesting.UpdateAction)
		if !ok {
			return false, nil, nil
		}
		secret, ok := update.GetObject().(*corev1.Secret)
		if !ok {
			return false, nil, nil
		}
		sawUpdate = true
		sentResourceVersion = secret.ResourceVersion
		return false, nil, nil
	})

	mgr := NewManager(client, "argocd")
	if _, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil); err != nil {
		t.Fatalf("repair: %v", err)
	}

	if !sawUpdate {
		t.Fatal("no update was issued, so nothing was proven")
	}
	if sentResourceVersion != "424242" {
		t.Errorf(`the repair sent resourceVersion %q, want "424242" (the version it read).

Without the version it read, the API server accepts the write blind and a connection somebody took over between Sharko's read and Sharko's write gets silently overwritten. This is the last line behind the ownership recheck.`, sentResourceVersion)
	}
}

// TestRepair_RefusesAnOwnerChangeItCanSee is rule 4 through the recheck itself:
// the takeover has already landed by the time the repair does its live read, so
// the recheck sees it and refuses without writing.
func TestRepair_RefusesAnOwnerChangeItCanSee(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairCluster, "server": "https://stale.invalid", "config": `{"bearerToken":"old"}`},
		nil)
	client := fake.NewSimpleClientset(live)

	// The steal happens before the repair's own Get, which is what makes it
	// visible to the recheck.
	stolen := live.DeepCopy()
	stolen.Labels[LabelManagedBy] = "another-tool"
	if _, err := client.CoreV1().Secrets("argocd").Update(context.Background(), stolen, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("seeding the takeover: %v", err)
	}

	mgr := NewManager(client, "argocd")
	_, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if !errors.Is(err, ErrRepairOwnershipChanged) {
		t.Fatalf("err = %v, want ErrRepairOwnershipChanged", err)
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), repairCluster, metav1.GetOptions{})
	if after.Labels[LabelManagedBy] != "another-tool" {
		t.Errorf("ownership label = %q, want another-tool — Sharko overwrote a connection it does not own", after.Labels[LabelManagedBy])
	}
	if string(after.Data["config"]) != `{"bearerToken":"old"}` {
		t.Error("the credential blob was rewritten on a connection Sharko does not own")
	}
	if after.Labels["datadog"] != "disabled" {
		t.Error("an addon label was rewritten on a connection Sharko does not own")
	}
}

// TestRepair_ConflictIsItsOwnAnswer: a plain resourceVersion conflict (somebody
// wrote the Secret for any reason during the repair) is reported as its own
// refusal rather than as a generic failure, so the caller can say what happened.
func TestRepair_ConflictIsItsOwnAnswer(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairCluster, "server": "https://stale.invalid"}, nil)
	client := fake.NewSimpleClientset(live)
	client.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Resource: "secrets"}, repairCluster, errors.New("resourceVersion moved"))
	})

	mgr := NewManager(client, "argocd")
	_, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if !errors.Is(err, ErrRepairSecretChangedUnderneath) {
		t.Fatalf("err = %v, want ErrRepairSecretChangedUnderneath", err)
	}
}

// TestRepair_MissingSecretIsRefusedNotCreated: repair corrects something that
// exists. Creating a missing connection is the reconciler's job.
func TestRepair_MissingSecretIsRefusedNotCreated(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, "argocd")

	_, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if !errors.Is(err, ErrRepairSecretMissing) {
		t.Fatalf("err = %v, want ErrRepairSecretMissing", err)
	}
	for _, a := range client.Actions() {
		if a.GetVerb() == "create" {
			t.Error("the repair created a connection Secret that did not exist; that is the reconciler's job, not repair's")
		}
	}
}

// --- no churn ---------------------------------------------------------------

// TestRepair_AlreadyCorrectWritesNothing: a connection that already matches what
// the Git-defined connection gets no write at all, and therefore no provenance
// stamp — an
// untouched Secret has nothing to be provenance for.
func TestRepair_AlreadyCorrectWritesNothing(t *testing.T) {
	desired := desiredFor(t, map[string]string{"datadog": "enabled"})
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repairCluster,
			Namespace: "argocd",
			Labels:    map[string]string{},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{},
	}
	for k, v := range desired.Labels {
		live.Labels[k] = v
	}
	for k, v := range desired.StringData {
		live.Data[k] = []byte(v)
	}

	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	out, err := mgr.RepairOwnedConnection(context.Background(), desired,
		map[string]string{"sharko.dev/written-at": "2026-08-12T00:00:00Z"})
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if out.Changed {
		t.Errorf("the repair reported a change on an already-correct connection (fields: %v)", out.FieldsWritten)
	}
	assertNoWriteReached(t, client)

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), repairCluster, metav1.GetOptions{})
	if _, stamped := after.Annotations["sharko.dev/written-at"]; stamped {
		t.Error("a no-op repair stamped a provenance annotation; nothing was written, so there is nothing to be provenance for")
	}
}

// --- no credential value in anything the primitive reports ------------------

// TestRepair_OutcomeNamesFieldsNeverValues: the outcome reports which fields it
// wrote. data.config appears by NAME when the credential blob changed, and its
// contents appear nowhere.
func TestRepair_OutcomeNamesFieldsNeverValues(t *testing.T) {
	live := ownedLiveSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairCluster, "server": "https://stale.invalid", "config": `{"bearerToken":"old"}`},
		nil)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	out, err := mgr.RepairOwnedConnection(context.Background(),
		desiredFor(t, map[string]string{"datadog": "enabled"}), nil)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}

	sawConfig := false
	for _, f := range out.FieldsWritten {
		if f == "data.config" {
			sawConfig = true
		}
		if strings.Contains(f, repairSentinel) {
			t.Errorf("a reported field path carries the credential value: %q", f)
		}
	}
	if !sawConfig {
		t.Errorf("the credential blob was rewritten but data.config is not in the reported fields: %v", out.FieldsWritten)
	}
}

// --- helpers ---------------------------------------------------------------

func assertNoWriteReached(t *testing.T, client *fake.Clientset) {
	t.Helper()
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Errorf("a refused repair issued a %q on %q — a refusal must write nothing at all",
				a.GetVerb(), a.GetResource().Resource)
		}
	}
}

func assertSecretUnchanged(t *testing.T, client *fake.Clientset, want *corev1.Secret) {
	t.Helper()
	after, err := client.CoreV1().Secrets("argocd").Get(context.Background(), want.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	for k, v := range want.Labels {
		if after.Labels[k] != v {
			t.Errorf("label %q = %q, want %q — the object was supposed to be untouched", k, after.Labels[k], v)
		}
	}
	for k, v := range want.Data {
		if string(after.Data[k]) != string(v) {
			t.Errorf("data key %q changed — the object was supposed to be untouched", k)
		}
	}
}
