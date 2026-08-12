package argosecrets

// addon_labels_repair_scope_test.go — R4-1. FR35 promises that a labels-only
// repair writes Sharko's own addon labels and NOTHING ELSE. These tests hold
// that promise at the writer.
//
// Three system labels are the thing at stake, and all three are DNS-qualified,
// which is exactly why IsAddonLabelKey excludes them:
//
//   - app.kubernetes.io/managed-by      (ownership)
//   - argocd.argoproj.io/secret-type    (ArgoCD's cluster-Secret type)
//   - sharko.dev/connectivity-check     (derived, plus its legacy sharko.io/
//     spelling)
//
// "Unchanged" here means byte-for-byte, VALUE INCLUDED. A test that only looked
// at whether a key was still present would pass while the writer quietly reset
// its value, and that is one of the two things this story fixes.

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

// systemLabelKeys are the keys a labels-only repair may never add, change or
// remove. Listed once so a test cannot check two of the three and look green.
var systemLabelKeys = []string{
	LabelManagedBy,
	LabelSecretType,
	models.LabelConnectivityCheck,
	models.LabelConnectivityCheckLegacy,
}

// snapshotSystemLabels records the exact state of every system label — present
// with which value, or absent. Both halves matter: a test that only compared
// present values would miss a key being ADDED.
func snapshotSystemLabels(labels map[string]string) map[string]*string {
	snap := make(map[string]*string, len(systemLabelKeys))
	for _, k := range systemLabelKeys {
		if v, ok := labels[k]; ok {
			val := v
			snap[k] = &val
			continue
		}
		snap[k] = nil
	}
	return snap
}

func describeLabelState(v *string) string {
	if v == nil {
		return "absent"
	}
	return "present with value " + *v
}

// TestRepairAddonLabelsWithOwnershipCheck_EverySystemLabelByteForByteUnchanged
// is the R4-1 criterion-5 test.
//
// It walks the system labels across the combinations production can actually
// produce: each one present and absent, on a Sharko-owned connection
// (expectedOwned=true) and on a guest one (expectedOwned=false), and present
// with a value that is NOT the one Sharko would stamp so a silent reset shows
// up.
//
// The ownership dimension is not free to vary: expectedOwned=true needs the
// Secret to be owned (managed-by=sharko, or the adopted annotation), and
// expectedOwned=false needs it to be neither, or the ownership recheck refuses
// before any of this is reached. So "managed-by absent, owned" is expressed the
// only way production expresses it — through the adopted annotation.
//
// Every case drifts one real addon label, so a write genuinely happens. Without
// that, a writer that touched nothing at all would pass every assertion here
// and prove nothing.
func TestRepairAddonLabelsWithOwnershipCheck_EverySystemLabelByteForByteUnchanged(t *testing.T) {
	cases := []struct {
		name          string
		labels        map[string]string
		annotations   map[string]string
		expectedOwned bool
	}{
		{
			name: "owned, all three system labels present with the values Sharko stamps",
			labels: map[string]string{
				LabelManagedBy:                ManagedByValue,
				LabelSecretType:               "cluster",
				models.LabelConnectivityCheck: "true",
				"datadog":                     "disabled",
			},
			expectedOwned: true,
		},
		{
			name: "owned, secret-type and the check label both absent",
			labels: map[string]string{
				LabelManagedBy: ManagedByValue,
				"datadog":      "disabled",
			},
			expectedOwned: true,
		},
		{
			name: "owned, secret-type and the check label present with values Sharko would not stamp",
			labels: map[string]string{
				LabelManagedBy:                ManagedByValue,
				LabelSecretType:               "repository",
				models.LabelConnectivityCheck: "somebody-elses-value",
				"datadog":                     "disabled",
			},
			expectedOwned: true,
		},
		{
			name: "owned through the adopted annotation, managed-by absent",
			labels: map[string]string{
				LabelSecretType:               "cluster",
				models.LabelConnectivityCheck: "true",
				"datadog":                     "disabled",
			},
			annotations:   map[string]string{AnnotationAdopted: "true"},
			expectedOwned: true,
		},
		{
			name: "owned through the adopted annotation, managed-by present, check label absent",
			labels: map[string]string{
				LabelManagedBy:  ManagedByValue,
				LabelSecretType: "cluster",
				"datadog":       "disabled",
			},
			annotations:   map[string]string{AnnotationAdopted: "true"},
			expectedOwned: true,
		},
		{
			name: "guest, secret-type and the check label both present",
			labels: map[string]string{
				LabelSecretType:               "cluster",
				models.LabelConnectivityCheck: "true",
				"datadog":                     "disabled",
			},
			expectedOwned: false,
		},
		{
			name: "guest, every system label absent",
			labels: map[string]string{
				"datadog": "disabled",
			},
			expectedOwned: false,
		},
		{
			name: "guest, secret-type and the check label present with values Sharko would not stamp",
			labels: map[string]string{
				LabelSecretType:               "repository",
				models.LabelConnectivityCheck: "somebody-elses-value",
				"datadog":                     "disabled",
			},
			expectedOwned: false,
		},
		{
			name: "guest, the legacy check-label spelling present",
			labels: map[string]string{
				models.LabelConnectivityCheckLegacy: "true",
				"datadog":                           "disabled",
			},
			expectedOwned: false,
		},
		{
			name: "owned, the legacy check-label spelling present",
			labels: map[string]string{
				LabelManagedBy:                      ManagedByValue,
				LabelSecretType:                     "cluster",
				models.LabelConnectivityCheckLegacy: "true",
				"datadog":                           "disabled",
			},
			expectedOwned: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "cluster-system-labels",
					Namespace:   "argocd",
					Labels:      tc.labels,
					Annotations: tc.annotations,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"name":   []byte("cluster-system-labels"),
					"server": []byte("https://cluster-system-labels.test.invalid"),
					"config": []byte(`{"bearerToken":"a-made-up-token-for-this-test-only"}`),
				},
			}
			before := snapshotSystemLabels(tc.labels)

			client := fake.NewSimpleClientset(live)
			mgr := NewManager(client, "argocd")

			// One real addon change, so a write actually happens and the
			// assertions below are about a live write rather than a no-op.
			pinned := map[string]string{"datadog": "enabled"}

			outcome, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(
				context.Background(), "cluster-system-labels", pinned, tc.expectedOwned)
			if err != nil {
				t.Fatalf("repair failed: %v", err)
			}
			if !found {
				t.Fatal("repair reported the Secret not found, but it exists")
			}
			if !outcome.Changed {
				t.Fatal("repair reported no change, but the datadog label genuinely drifted — the rest of this test would prove nothing")
			}

			after, getErr := client.CoreV1().Secrets("argocd").Get(
				context.Background(), "cluster-system-labels", metav1.GetOptions{})
			if getErr != nil {
				t.Fatalf("reading the Secret back: %v", getErr)
			}

			// The addon label really was repaired.
			if after.Labels["datadog"] != "enabled" {
				t.Errorf("the addon label was not repaired: datadog = %q, want enabled", after.Labels["datadog"])
			}

			// Every system label byte-for-byte as it was — value included.
			got := snapshotSystemLabels(after.Labels)
			for _, k := range systemLabelKeys {
				want, have := before[k], got[k]
				switch {
				case want == nil && have != nil:
					t.Errorf(`a labels-only repair ADDED the system label %q (%s).

FR35 promises Sharko's own addon labels and nothing else. Every system label is DNS-qualified, so IsAddonLabelKey excludes it and no path through this writer may write one.`, k, describeLabelState(have))
				case want != nil && have == nil:
					t.Errorf(`a labels-only repair REMOVED the system label %q, which was %s.

Removing a system label that was already on the Secret is not something FR35 ever promised.`, k, describeLabelState(want))
				case want != nil && have != nil && *want != *have:
					t.Errorf(`a labels-only repair CHANGED the value of the system label %q: %q -> %q.

Byte-for-byte means the value too. Resetting a system label to the value Sharko would stamp is still a write Sharko did not promise.`, k, *want, *have)
				}
			}

			// And the repair did not REPORT touching one either. The diff of
			// what was written against what was read is what the caller sees,
			// so a system label appearing in it would be the writer telling the
			// user it changed something it must not have.
			for _, path := range outcome.FieldsWritten {
				for _, k := range systemLabelKeys {
					if strings.Contains(path, k) {
						t.Errorf("FieldsWritten names the system label %q: %q — a labels-only repair neither writes nor reports one", k, path)
					}
				}
			}
		})
	}
}

// TestRepairAddonLabelsWithOwnershipCheck_NonAddonKeyInPinnedSetIsNotWritten is
// the R4-1 criterion-6 test.
//
// The pinned set is built by the caller. Today's caller passes addon keys only,
// so this is a hole rather than a live incident — but the writer is the thing
// that makes the FR35 promise, and a promise enforced only by its callers is
// not enforced. So the writer filters, and a key outside its promise is skipped
// QUIETLY: no error, because the rest of the repair is fine and refusing it
// would turn a caller's mistake into a failed repair.
func TestRepairAddonLabelsWithOwnershipCheck_NonAddonKeyInPinnedSetIsNotWritten(t *testing.T) {
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-non-addon-keys",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                ManagedByValue,
				LabelSecretType:               "cluster",
				models.LabelConnectivityCheck: "true",
				"datadog":                     "disabled",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("cluster-non-addon-keys"),
			"server": []byte("https://cluster-non-addon-keys.test.invalid"),
			"config": []byte(`{"bearerToken":"a-made-up-token-for-this-test-only"}`),
		},
	}
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	// One legitimate addon key, so a write happens, plus four keys this writer
	// does not promise. Two of them try to move ownership, which is the case
	// that matters most.
	pinned := map[string]string{
		"datadog":                     "enabled",
		LabelManagedBy:                "another-tool",
		LabelSecretType:               "repository",
		models.LabelConnectivityCheck: "a-forged-value",
		"unrelated.example.io/thing":  "injected",
	}

	outcome, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(
		context.Background(), "cluster-non-addon-keys", pinned, true)
	if err != nil {
		t.Fatalf(`repair returned an error: %v.

A non-addon key in the pinned set is IGNORED, not an error — the caller asked for something outside this writer's promise, and declining quietly beats failing a repair that is otherwise correct.`, err)
	}
	if !found {
		t.Fatal("repair reported the Secret not found, but it exists")
	}
	if !outcome.Changed {
		t.Fatal("repair reported no change, but the datadog label genuinely drifted")
	}

	after, getErr := client.CoreV1().Secrets("argocd").Get(
		context.Background(), "cluster-non-addon-keys", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("reading the Secret back: %v", getErr)
	}

	// The one key that was this writer's to write.
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("the addon label was not repaired: datadog = %q, want enabled", after.Labels["datadog"])
	}

	// The ownership label is the serious one: writing what the caller asked
	// would have handed this connection to another tool.
	if got := after.Labels[LabelManagedBy]; got != ManagedByValue {
		t.Errorf(`the pinned set moved the ownership label: %q = %q, want %q.

The pinned set is caller input. A writer that stamps whatever ownership label it is handed can be asked to give a connection away.`, LabelManagedBy, got, ManagedByValue)
	}
	if got := after.Labels[LabelSecretType]; got != "cluster" {
		t.Errorf("the pinned set changed %q: %q, want cluster", LabelSecretType, got)
	}
	if got := after.Labels[models.LabelConnectivityCheck]; got != "true" {
		t.Errorf("the pinned set changed %q: %q, want true", models.LabelConnectivityCheck, got)
	}
	if got, present := after.Labels["unrelated.example.io/thing"]; present {
		t.Errorf(`the pinned set added the foreign label "unrelated.example.io/thing" = %q — this writer converges addon labels only`, got)
	}

	// And it reported only the one field it really wrote.
	want := []string{"metadata.labels[datadog]"}
	if len(outcome.FieldsWritten) != 1 || outcome.FieldsWritten[0] != want[0] {
		t.Errorf("FieldsWritten = %v, want %v — only the addon label was written, so only it may be reported", outcome.FieldsWritten, want)
	}
}

// TestRepairAddonLabelsWithOwnershipCheck_NonAddonKeyAloneIsANoOp is the other
// half of "ignored, not an error": when the pinned set contains nothing this
// writer may write, there is nothing to converge, so nothing is written and
// nothing is reported. Not a failure, and not a phantom success either.
func TestRepairAddonLabelsWithOwnershipCheck_NonAddonKeyAloneIsANoOp(t *testing.T) {
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-only-foreign-pins",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:  ManagedByValue,
				LabelSecretType: "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("cluster-only-foreign-pins"),
			"server": []byte("https://cluster-only-foreign-pins.test.invalid"),
			"config": []byte(`{"bearerToken":"a-made-up-token-for-this-test-only"}`),
		},
	}
	client := fake.NewSimpleClientset(live)
	mgr := NewManager(client, "argocd")

	pinned := map[string]string{
		LabelManagedBy:               "another-tool",
		"unrelated.example.io/thing": "injected",
	}

	outcome, found, err := mgr.RepairAddonLabelsWithOwnershipCheck(
		context.Background(), "cluster-only-foreign-pins", pinned, true)
	if err != nil {
		t.Fatalf("repair returned an error for a pinned set of keys it simply does not write: %v", err)
	}
	if !found {
		t.Fatal("repair reported the Secret not found, but it exists")
	}
	if outcome.Changed {
		t.Errorf("repair reported a change, but every key it was handed is outside what it writes: FieldsWritten = %v", outcome.FieldsWritten)
	}
	if n := countActions(client, "update"); n != 0 {
		t.Errorf("repair wrote to the Secret %d time(s) with nothing to converge", n)
	}
}
