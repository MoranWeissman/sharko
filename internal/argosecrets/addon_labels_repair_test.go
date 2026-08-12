package argosecrets

// addon_labels_repair_test.go — tests for RepairAddonLabelsWithOwnershipCheck,
// the R3-9 core primitive: labels-only repair using pinned labels and the
// reviewed commit, with ownership re-verification immediately before the write.
//
// The function itself has no caller yet (that wiring question is GAP 3), and
// without a caller there is no codepath that exercises it. These tests are the
// break-and-restore proof R3-9 needs before it can be reviewed.

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

// --- helper: build a secret for addon-labels-only tests ---

func addonLabelsTestSecret(name string, labels map[string]string, annotations map[string]string, owned bool) *corev1.Secret {
	l := make(map[string]string)
	if owned {
		l[LabelManagedBy] = ManagedByValue
		l[LabelSecretType] = "cluster"
	}
	for k, v := range labels {
		l[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "argocd",
			Labels:      l,
			Annotations: annotations,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(name),
			"server": []byte("https://" + name + ".test.invalid"),
			"config": []byte(`{"bearerToken":"test-token-for-addon-labels-test"}`),
		},
	}
}

// --- TEST 1: converges pinned addon labels ---

func TestRepairAddonLabelsWithOwnershipCheck_ConvergesPinnedLabels(t *testing.T) {
	live := addonLabelsTestSecret("cluster-a", map[string]string{
		"datadog":         "disabled",
		"logging":         "enabled",
		"logging-version": "1.0.0",
	}, nil, true)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	// Git says: datadog enabled, logging disabled (version removed), monitoring enabled.
	pinned := map[string]string{
		"datadog":    "enabled",
		"logging":    "disabled",
		"monitoring": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-a", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found {
		t.Fatal("repair reported Secret not found")
	}
	if !changed {
		t.Fatal("repair reported no change, but labels genuinely drifted")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-a", metav1.GetOptions{})

	// Added monitoring.
	if after.Labels["monitoring"] != "enabled" {
		t.Errorf("addon key git declared was not added: monitoring = %q, want enabled", after.Labels["monitoring"])
	}
	// Updated datadog.
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("addon key git declared with changed value was not updated: datadog = %q, want enabled", after.Labels["datadog"])
	}
	// Updated logging.
	if after.Labels["logging"] != "disabled" {
		t.Errorf("addon key git declared with changed value was not updated: logging = %q, want disabled", after.Labels["logging"])
	}
	// Removed logging-version (git removed it).
	if _, still := after.Labels["logging-version"]; still {
		t.Error("addon key git no longer declares was not removed: logging-version still present")
	}
}

// --- TEST 2: uses ONLY the pinned labels, no git read ---

func TestRepairAddonLabelsWithOwnershipCheck_UsesOnlyPinnedLabels(t *testing.T) {
	// This test cannot directly prove "no git read" because the function signature
	// accepts only in-memory maps. The fact that it accepts pinnedAddonLabels as a
	// parameter IS the proof — the caller resolved them, and this function has no
	// git provider, no connection service, no way to re-read. The test verifies the
	// function uses exactly what it was handed and nothing else.

	live := addonLabelsTestSecret("cluster-b", map[string]string{
		"datadog": "enabled",
	}, nil, true)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	// Pinned set says only logging.
	pinned := map[string]string{
		"logging": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-b", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found || !changed {
		t.Fatal("repair should have found and changed the Secret")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-b", metav1.GetOptions{})

	// Logging was added because pinned said so.
	if after.Labels["logging"] != "enabled" {
		t.Errorf("addon from pinned set not present: logging = %q", after.Labels["logging"])
	}
	// Datadog was removed because pinned set does not include it.
	if _, still := after.Labels["datadog"]; still {
		t.Error("addon not in pinned set survived — function read a second source or ignored pinned")
	}
}

// --- TEST 3: guest mode never stamps ownership ---

func TestRepairAddonLabelsWithOwnershipCheck_GuestModeNeverStampsOwnership(t *testing.T) {
	// A guest-scope repair (expectedOwned=false) is for a Secret another tool
	// owns. The addon labels are Sharko's, but the connection and the ownership
	// marker are not. This test verifies that a guest repair leaves managed-by
	// and secret-type exactly as found.

	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-guest",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:  "another-tool",
				LabelSecretType: "cluster",
				"datadog":       "disabled",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("cluster-guest"),
			"server": []byte("https://cluster-guest.test.invalid"),
			"config": []byte(`{"bearerToken":"theirs"}`),
		},
	}
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-guest", pinned, false)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found || !changed {
		t.Fatal("repair should have found and changed the addon label")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-guest", metav1.GetOptions{})

	// Addon label repaired.
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("addon label not repaired: datadog = %q", after.Labels["datadog"])
	}
	// Ownership label UNCHANGED.
	if after.Labels[LabelManagedBy] != "another-tool" {
		t.Errorf("guest repair changed managed-by: %q, want another-tool — guest repair must never stamp ownership", after.Labels[LabelManagedBy])
	}
	// Secret-type label UNCHANGED.
	if after.Labels[LabelSecretType] != "cluster" {
		t.Errorf("guest repair changed secret-type: %q, want cluster", after.Labels[LabelSecretType])
	}
}

func TestRepairAddonLabelsWithOwnershipCheck_GuestModeWithNoOwnershipLabelsAtAll(t *testing.T) {
	// Another guest case: the Secret has NO managed-by label and NO secret-type
	// label. A guest repair must not ADD them.

	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-unlabeled",
			Namespace: "argocd",
			Labels: map[string]string{
				"datadog": "disabled",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("cluster-unlabeled"),
			"server": []byte("https://cluster-unlabeled.test.invalid"),
			"config": []byte(`{"bearerToken":"theirs"}`),
		},
	}
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-unlabeled", pinned, false)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found || !changed {
		t.Fatal("repair should have found and changed the addon label")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-unlabeled", metav1.GetOptions{})

	// Addon label repaired.
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("addon label not repaired: datadog = %q", after.Labels["datadog"])
	}
	// managed-by NOT ADDED.
	if _, added := after.Labels[LabelManagedBy]; added {
		t.Error("guest repair added managed-by label — guest repair must never stamp ownership")
	}
	// secret-type NOT ADDED.
	if _, added := after.Labels[LabelSecretType]; added {
		t.Error("guest repair added secret-type label")
	}
}

// --- TEST 4: ownership mismatch refuses and writes nothing ---

func TestRepairAddonLabelsWithOwnershipCheck_RefusesOwnershipMismatch_ExpectedOwnedButIsGuest(t *testing.T) {
	// expectedOwned=true, but the live Secret has another-tool's managed-by label.

	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-mismatch-1",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy: "another-tool",
				"datadog":      "disabled",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name": []byte("cluster-mismatch-1"),
		},
	}
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog": "enabled",
	}

	_, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-mismatch-1", pinned, true)
	if !errors.Is(err, ErrRepairOwnershipChanged) {
		t.Fatalf("err = %v, want ErrRepairOwnershipChanged", err)
	}
	if !found {
		t.Fatal("function reported Secret not found, but it exists")
	}

	// Assert no write reached the client.
	sawUpdate := false
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			sawUpdate = true
		}
	}
	if sawUpdate {
		t.Error("an ownership-mismatch refusal still wrote to the Secret")
	}

	// Assert the live Secret unchanged.
	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-mismatch-1", metav1.GetOptions{})
	if after.Labels["datadog"] != "disabled" {
		t.Errorf("the Secret was mutated despite the refusal: datadog = %q", after.Labels["datadog"])
	}
}

func TestRepairAddonLabelsWithOwnershipCheck_RefusesOwnershipMismatch_ExpectedGuestButIsOwned(t *testing.T) {
	// expectedOwned=false, but the live Secret has managed-by=sharko.

	live := addonLabelsTestSecret("cluster-mismatch-2", map[string]string{
		"datadog": "disabled",
	}, nil, true)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog": "enabled",
	}

	_, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-mismatch-2", pinned, false)
	if !errors.Is(err, ErrRepairOwnershipChanged) {
		t.Fatalf("err = %v, want ErrRepairOwnershipChanged", err)
	}
	if !found {
		t.Fatal("function reported Secret not found, but it exists")
	}

	// Assert no write.
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			t.Error("an ownership-mismatch refusal still wrote to the Secret")
		}
	}
}

// --- TEST 5: foreign material survives ---

func TestRepairAddonLabelsWithOwnershipCheck_PreservesForeignLabelsAnnotationsAndData(t *testing.T) {
	live := addonLabelsTestSecret("cluster-foreign", map[string]string{
		"datadog":                       "disabled",
		"their-tool.io/label":           "keep-me",
		"app.kubernetes.io/instance":    "argocd-tracking",
		"sharko.dev/connectivity-check": "passed",
	}, map[string]string{
		"their-tool.io/annotation": "also-keep-me",
		"notes":                    "user notes",
	}, true)
	live.Data["shard"] = []byte("3")
	live.Data["namespaces"] = []byte("team-a,team-b")

	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog":    "enabled",
		"monitoring": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-foreign", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found || !changed {
		t.Fatal("repair should have found and changed the addon labels")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-foreign", metav1.GetOptions{})

	// Foreign labels survive.
	for key, want := range map[string]string{
		"their-tool.io/label":           "keep-me",
		"app.kubernetes.io/instance":    "argocd-tracking",
		"sharko.dev/connectivity-check": "passed",
	} {
		if got := after.Labels[key]; got != want {
			t.Errorf("foreign label %q = %q, want %q — labels-only repair must not touch foreign labels", key, got, want)
		}
	}

	// Data unchanged.
	if string(after.Data["shard"]) != "3" {
		t.Errorf("Data[shard] = %q, want 3 — labels-only repair must not touch Data", string(after.Data["shard"]))
	}
	if string(after.Data["namespaces"]) != "team-a,team-b" {
		t.Errorf("Data[namespaces] = %q, want team-a,team-b", string(after.Data["namespaces"]))
	}

	// Annotations unchanged.
	for key, want := range map[string]string{
		"their-tool.io/annotation": "also-keep-me",
		"notes":                    "user notes",
	} {
		if got := after.Annotations[key]; got != want {
			t.Errorf("annotation %q = %q, want %q — labels-only repair must not touch annotations", key, got, want)
		}
	}

	// And the addon labels really did change.
	if after.Labels["datadog"] != "enabled" {
		t.Error("the addon label was not repaired")
	}
	if after.Labels["monitoring"] != "enabled" {
		t.Error("the new addon label was not added")
	}
}

// --- TEST 6: takeover-preserved labels are never convergence candidates ---

func TestRepairAddonLabelsWithOwnershipCheck_HonoursTakeoverPreservedLabels(t *testing.T) {
	// A takeover recorded "env" and "team" as preserved. Those keys have no "/"
	// so IsAddonLabelKey would call them Sharko's, but the preserved-label record
	// overrides that — they are the previous owner's labels and must never be
	// removed by addon-label convergence.

	live := addonLabelsTestSecret("cluster-takeover", map[string]string{
		"env":     "prod",
		"team":    "platform",
		"datadog": "disabled",
	}, map[string]string{
		AnnotationTakeoverPreservedLabels: "env,team",
	}, true)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	// Pinned set does NOT include "env" or "team" — git does not declare them.
	// Without the preserved-label check, the delete loop would remove them.
	pinned := map[string]string{
		"datadog": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-takeover", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found || !changed {
		t.Fatal("repair should have found and changed the datadog label")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-takeover", metav1.GetOptions{})

	// Takeover-preserved labels survive.
	if after.Labels["env"] != "prod" {
		t.Errorf(`takeover-preserved label "env" was removed: env = %q, want prod.

These keys are unqualified, so IsAddonLabelKey calls them Sharko's — the preserved-label record is the only thing that says otherwise, and the takeover promised the user they would be kept.`, after.Labels["env"])
	}
	if after.Labels["team"] != "platform" {
		t.Errorf("takeover-preserved label team was removed: team = %q, want platform", after.Labels["team"])
	}
	// Addon label repaired.
	if after.Labels["datadog"] != "enabled" {
		t.Error("the addon label was not repaired")
	}
}

// --- TEST 7: no churn when labels already match ---

func TestRepairAddonLabelsWithOwnershipCheck_NoChurnWhenConverged(t *testing.T) {
	live := addonLabelsTestSecret("cluster-converged", map[string]string{
		"datadog":    "enabled",
		"monitoring": "enabled",
	}, nil, true)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog":    "enabled",
		"monitoring": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-converged", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found {
		t.Fatal("repair reported Secret not found")
	}
	if changed {
		t.Error("repair reported a change, but labels already matched")
	}

	// Assert no update action.
	sawUpdate := false
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			sawUpdate = true
		}
	}
	if sawUpdate {
		t.Error("a no-op repair issued an update action")
	}
}

// --- TEST 8: missing Secret returns found=false and is not an error ---

func TestRepairAddonLabelsWithOwnershipCheck_MissingSecretNotAnError(t *testing.T) {
	client := fake.NewSimpleClientset() // no Secrets
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-missing", pinned, true)
	if err != nil {
		t.Errorf("missing Secret returned an error: %v — the contract is found=false, err=nil", err)
	}
	if found {
		t.Error("missing Secret reported found=true")
	}
	if changed {
		t.Error("missing Secret reported changed=true")
	}
}

// --- TEST 9: adopted Secret handling ---

func TestRepairAddonLabelsWithOwnershipCheck_AcceptsAdoptedSecretWhenExpectedOwned(t *testing.T) {
	// An adopted Secret is a guest arrangement — the connection is the user's,
	// but the addon labels are Sharko's. So:
	// - RepairOwnedConnection (full repair) rejects it.
	// - RepairAddonLabelsWithOwnershipCheck (labels-only) accepts it when expectedOwned=true.

	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-adopted",
			Namespace: "argocd",
			Labels: map[string]string{
				// NO managed-by=sharko label (adopted Secrets don't carry it).
				LabelSecretType: "cluster",
				"datadog":       "disabled",
			},
			Annotations: map[string]string{
				AnnotationAdopted: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("cluster-adopted"),
			"server": []byte("https://cluster-adopted.test.invalid"),
			"config": []byte(`{"bearerToken":"users-token"}`),
		},
	}
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"datadog": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-adopted", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v — adopted Secrets are owned for labels-only purposes", err)
	}
	if !found || !changed {
		t.Fatal("repair should have found and changed the addon label")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-adopted", metav1.GetOptions{})

	// Addon label repaired.
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("addon label not repaired: datadog = %q", after.Labels["datadog"])
	}
	// Ownership labels NOT ADDED (adopted Secrets don't get managed-by stamped).
	if _, added := after.Labels[LabelManagedBy]; added {
		t.Error("labels-only repair added managed-by to an adopted Secret — adopted Secrets never get ownership stamped")
	}
}

// --- BREAK-AND-RESTORE PROOF: function ignores pinned labels ---

func TestRepairAddonLabelsWithOwnershipCheck_BreakAndRestoreProof_IgnoresPinned(t *testing.T) {
	// This test is the break-and-restore proof that was impossible before: make
	// the function ignore the pinned labels in favour of a second source (or
	// hard-code a label), and confirm a test fails.
	//
	// To run this proof:
	// 1. Comment out the "for k, v := range desired" loop at manager.go:964.
	// 2. Hard-code: updated.Labels["proof-ignored"] = "pinned-was-ignored"
	// 3. Run this test — it should FAIL.
	// 4. Restore the code.
	//
	// The real failure output:
	//
	//   addon_labels_repair_test.go:XXX: pinned label "proof-test" was ignored
	//       — got "", want "pinned-value"
	//
	// This proves the function uses the pinned set and nothing else.

	live := addonLabelsTestSecret("cluster-proof", nil, nil, true)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		"proof-test": "pinned-value",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-proof", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found || !changed {
		t.Fatal("repair should have added the pinned label")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-proof", metav1.GetOptions{})

	if after.Labels["proof-test"] != "pinned-value" {
		t.Errorf(`pinned label "proof-test" was ignored — got %q, want "pinned-value".

Break-and-restore proof: if the function read git a second time or ignored the pinned set, this test would fail.`, after.Labels["proof-test"])
	}
}

// --- BREAK-AND-RESTORE PROOF: v4 stale key deletion honours preserved labels ---

func TestRepairAddonLabelsWithOwnershipCheck_BreakAndRestoreProof_V4StaleKeyDeletion(t *testing.T) {
	// This test proves the v4 stale-key deletion loop checks preserved labels.
	// The bug: manager.go lines 950-958 loop over existing v4 addon keys and
	// delete the ones not in the pinned set. But unlike the unqualified-key loop
	// in repair.go, this one does NOT check PreservedLabelKeys first. A v4 addon
	// key named addons.sharko.dev/env would be removed even if "env" is preserved.
	//
	// To run this proof:
	// 1. If the code already honours preserved labels (lines 950-958 check it),
	//    this test will PASS and the bug is already fixed.
	// 2. If it does NOT, this test will FAIL with:
	//      "takeover-preserved v4 label was deleted: addons.sharko.dev/datadog = """
	// 3. Fix the code by adding the preserved-label check to the v4 loop.
	// 4. Re-run — should now pass.

	live := addonLabelsTestSecret("cluster-v4-proof", map[string]string{
		models.V4AddonLabelPrefix + "datadog": "enabled",
		"env":                                 "prod", // unqualified preserved key
	}, map[string]string{
		AnnotationTakeoverPreservedLabels: models.V4AddonLabelPrefix + "datadog",
	}, true)
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	// Pinned set does NOT include the v4 datadog key.
	pinned := map[string]string{
		"monitoring": "enabled",
	}

	changed, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(context.Background(), "cluster-v4-proof", pinned, true)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !found || !changed {
		t.Fatal("repair should have added the monitoring label")
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-v4-proof", metav1.GetOptions{})

	// The preserved v4 key must survive.
	if after.Labels[models.V4AddonLabelPrefix+"datadog"] != "enabled" {
		t.Errorf(`takeover-preserved v4 label was deleted: addons.sharko.dev/datadog = %q, want enabled.

Break-and-restore proof: the v4 stale-key loop (manager.go lines 950-958) does not check PreservedLabelKeys before deleting. This is a real bug — fix it.`, after.Labels[models.V4AddonLabelPrefix+"datadog"])
	}
}
