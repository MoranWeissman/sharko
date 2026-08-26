package api

// connection_ownership_marker_test.go — R2-1. The proof that a difference in
// one of the THREE SYSTEM LABELS is not an addon-label problem, and that a
// connection Secret which does not carry Sharko's ownership marker is not
// reported as one Sharko owns.
//
// # What went wrong
//
// The reconciliation response sorted every difference whose path started with
// "metadata.labels[" into the addon_labels group. The comparison also compares
// three labels that are NOT addon labels and produce that identical path
// shape: app.kubernetes.io/managed-by, argocd.argoproj.io/secret-type and
// sharko.dev/connectivity-check. So a difference in any of them landed in the
// bucket that carries no approval requirement, and:
//
//   - approval_required went false — a change to the OWNERSHIP MARKER ITSELF
//     needed no admin approval;
//   - the full-connection repair, which is the one thing that puts those
//     labels back, was withheld;
//   - the page explained it as "Some addon labels on this connection do not
//     match what Git declares." when no addon label differed at all;
//   - plan.automatic promised "Sharko automatically reapplies the addon labels
//     defined in Git." — which nothing performs for these keys.
//
// Separately, the ownership condition row was written from the management
// MODE. Classify's foreign-owner rule needs a marker that is non-empty AND not
// Sharko's, so a Secret carrying NO managed-by label falls through to the
// ordinary Sharko-managed path — and the page rendered "Sharko owns this
// connection Secret." as a PASSED CHECK about a Secret that says no such
// thing, while the reconciler skips it as Adopt territory forever and both
// repair paths refuse it.
//
// # How these tests are built
//
// Every tuple below goes through the REAL connectioncompare.Classify, the REAL
// connectioncompare.Compare, the REAL finishView and the REAL response
// builder. Nothing here hand-builds a response struct, so no test in this file
// can pass on a shape the server cannot actually send.
//
// The live Secret is built by argosecrets.BuildClusterSecret from the SAME
// spec the comparison rebuilds its expected side from, and then exactly ONE
// label is taken away or changed. That is what makes each case's difference
// list a single named difference rather than a pile of incidental ones — and
// it means the fixture cannot quietly agree with the assertion, because the
// fixture is produced by the production builder and the assertion names a
// field path.

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
)

// ownershipNamespace is the namespace the reconciler writes connection
// Secrets into, matching the rest of this package's fixtures.
const ownershipNamespace = "argocd"

// ownershipConnectionSpec is the cluster's connection as Sharko would write
// it. ONE spec: the live Secret and the comparison's expected side are both
// built from it, so everything except the label a case removes matches by
// construction.
func ownershipConnectionSpec() argosecrets.ClusterSecretSpec {
	return argosecrets.ClusterSecretSpec{
		Name:   comparisonCluster,
		Server: "https://" + comparisonCluster + ".invalid",
		Token:  "a-stored-sign-in-token",
	}
}

// ownershipLiveSecret builds the live connection Secret through the real
// builder and then applies the case's one mutation.
//
// The label set is assembled exactly the way connectioncompare's expectedSides
// assembles it — the git-desired addon labels with the connectivity-check
// label derived under the same setting — so a matching case really does match.
func ownershipLiveSecret(t *testing.T, addonLabels map[string]string, checkOn bool, mutate func(*corev1.Secret)) *corev1.Secret {
	t.Helper()

	labels := make(map[string]string, len(addonLabels)+1)
	for k, v := range addonLabels {
		labels[k] = v
	}
	models.ApplyConnectivityCheckLabel(labels, checkOn)

	spec := ownershipConnectionSpec()
	spec.Labels = labels
	built, err := argosecrets.BuildClusterSecret(spec, ownershipNamespace)
	if err != nil {
		t.Fatalf("building the live connection Secret: %v", err)
	}

	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      built.Name,
			Namespace: built.Namespace,
			Labels:    map[string]string{},
		},
		Type: built.Type,
		Data: map[string][]byte{},
	}
	for k, v := range built.Labels {
		live.Labels[k] = v
	}
	// Kubernetes returns values in Data even for keys written through
	// StringData, so a live Secret read back carries Data — mirror that.
	for k, v := range built.StringData {
		live.Data[k] = []byte(v)
	}
	if mutate != nil {
		mutate(live)
	}
	return live
}

// ownershipTuple runs the whole real pipeline for one situation, with the
// connectivity-check feature off.
func ownershipTuple(t *testing.T, addonLabels map[string]string, mutate func(*corev1.Secret), selfHealOn bool) connectionReconciliationView {
	t.Helper()
	return ownershipTupleWithCheck(t, addonLabels, false, mutate, selfHealOn)
}

// ownershipTupleWithCheck is the same pipeline with the connectivity-check
// setting as an input, because the third system label only exists when that
// feature is on and the cluster has no addons enabled.
func ownershipTupleWithCheck(t *testing.T, addonLabels map[string]string, checkOn bool, mutate func(*corev1.Secret), selfHealOn bool) connectionReconciliationView {
	t.Helper()

	live := ownershipLiveSecret(t, addonLabels, checkOn, mutate)

	// liveManagedBy is the handler's own reader — the test does not re-type
	// the label key, so it cannot read a different marker than production.
	marker := liveManagedBy(live)

	policy := connectioncompare.Classify(connectioncompare.ClassifyInput{
		CredsSource:                  models.CredsSourceSecretKubeconfig,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                marker,
		LiveAdopted:                  argosecrets.IsAdopted(live.Annotations),
	})

	expectedSpec := ownershipConnectionSpec()
	res := connectioncompare.Compare(connectioncompare.Request{
		ClusterName:         comparisonCluster,
		Namespace:           ownershipNamespace,
		Policy:              policy,
		Live:                live,
		LiveFound:           true,
		DesiredAddonLabels:  addonLabels,
		AddonLabelsKnown:    true,
		ConnectivityCheckOn: checkOn,
		ExpectedSpec:        &expectedSpec,
	})

	v := reconView(nil)
	v.policy = policy
	v.liveSecretFound = true
	v.liveOwnershipMarker = marker
	v = finishView(v, res)
	return buildRecon(v, selfHealOn)
}

// driftPaths lists the field paths in one drift group.
func driftPaths(entries []connectionReconciliationDriftEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func conditionByID(out connectionReconciliationView, id string) (connectionReconciliationCondition, bool) {
	for _, c := range out.Conditions {
		if c.ID == id {
			return c, true
		}
	}
	return connectionReconciliationCondition{}, false
}

// --- 1. The three system labels are not addon labels -------------------------

// TestConnectionReconciliation_SystemLabelDriftIsNotAddonLabelDrift is the
// headline proof. For each of the three system labels, one label is removed
// from an otherwise perfectly matching connection and the response is read off
// the real builders.
//
// It failed on every one of the three before the grouping fix: the difference
// landed in addon_labels, approval_required was false, the reason was the
// addon-label sentence and the plan promised an automatic re-apply.
func TestConnectionReconciliation_SystemLabelDriftIsNotAddonLabelDrift(t *testing.T) {
	addonLabels := map[string]string{"datadog": "enabled"}

	cases := []struct {
		name string
		key  string
	}{
		{"the ownership marker", argosecrets.LabelManagedBy},
		{"the ArgoCD secret-type label", argosecrets.LabelSecretType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ownershipTuple(t, addonLabels, func(s *corev1.Secret) {
				delete(s.Labels, tc.key)
			}, false)

			wantPath := "metadata.labels[" + tc.key + "]"

			// It is a connection-configuration difference, never an
			// addon-label one.
			if got := driftPaths(out.Drift.AddonLabels); len(got) != 0 {
				t.Errorf("addon_labels should be empty, got %v", got)
			}
			if got := driftPaths(out.Drift.ConnectionConfiguration); !containsPath(got, wantPath) {
				t.Fatalf("connection_configuration should carry %q, got %v", wantPath, got)
			}

			// An admin has to approve it.
			if !out.Sync.ApprovalRequired {
				t.Error("approval_required is false for a change to a system label")
			}

			// And the page must not explain it as addon labels.
			if out.Sync.Reason == reasonOutOfSyncLabelsOnly {
				t.Error("sync.reason is the addon-label sentence, but no addon label differs")
			}

			// Nothing promises an automatic re-apply of labels nothing
			// re-applies.
			if out.Plan.Automatic == planAutomaticLabelSync {
				t.Error("plan.automatic promises an automatic addon-label re-apply for a system label")
			}
		})
	}
}

// TestConnectionReconciliation_ConnectivityCheckLabelIsNotAnAddonLabel is the
// third system label, which only exists when the connectivity-check feature is
// on and the cluster has no addons enabled — so it needs its own tuple rather
// than a row in the table above.
//
// Both spellings are stamped together (the canonical sharko.dev key and the
// transitional sharko.io one) and both are removed here, so the case is a
// connection with the check label gone entirely.
func TestConnectionReconciliation_ConnectivityCheckLabelIsNotAnAddonLabel(t *testing.T) {
	out := ownershipTupleWithCheck(t, map[string]string{}, true, func(s *corev1.Secret) {
		models.RemoveConnectivityCheckLabels(s.Labels)
	}, false)

	wantPath := "metadata.labels[" + models.LabelConnectivityCheck + "]"
	if got := driftPaths(out.Drift.ConnectionConfiguration); !containsPath(got, wantPath) {
		t.Fatalf("connection_configuration should carry %q, got %v", wantPath, got)
	}
	if got := driftPaths(out.Drift.AddonLabels); len(got) != 0 {
		t.Errorf("addon_labels should be empty, got %v", got)
	}
	if !out.Sync.ApprovalRequired {
		t.Error("approval_required is false for a missing connectivity-check label")
	}
	if out.Sync.Reason == reasonOutOfSyncLabelsOnly {
		t.Error("sync.reason is the addon-label sentence, but no addon label differs")
	}
	if out.Plan.Automatic == planAutomaticLabelSync {
		t.Error("plan.automatic promises an addon-label re-apply for the connectivity-check label")
	}
}

// TestConnectionReconciliation_SecretTypeLossOffersTheRepair is the Blind
// Hunter's trigger, end to end: a Sharko-OWNED connection that has lost
// argocd.argoproj.io/secret-type is a Secret ArgoCD no longer recognises as a
// cluster. The ownership marker is still Sharko's, so the full-connection
// repair — the one path that puts the label back — must be offered, and the
// page must point at it.
func TestConnectionReconciliation_SecretTypeLossOffersTheRepair(t *testing.T) {
	out := ownershipTuple(t, map[string]string{"datadog": "enabled"}, func(s *corev1.Secret) {
		delete(s.Labels, argosecrets.LabelSecretType)
	}, false)

	if out.Plan.Action != planActionRepairConnection {
		t.Fatalf("plan.action = %q, want %q", out.Plan.Action, planActionRepairConnection)
	}
	if out.Sync.Reason != reasonOutOfSyncApprovalRequired {
		t.Errorf("sync.reason = %q, want the repair-door sentence", out.Sync.Reason)
	}
	if out.Sync.Headline != headlineOutOfSyncApproval {
		t.Errorf("headline = %q, want %q", out.Sync.Headline, headlineOutOfSyncApproval)
	}
	// The ownership marker is intact, so the ownership row still passes.
	cond, ok := conditionByID(out, conditionOwnership)
	if !ok {
		t.Fatal("no ownership condition")
	}
	if cond.Status != conditionStatusOK || cond.Detail != condOwnershipOK {
		t.Errorf("ownership condition = %s/%q, want ok + the owned sentence", cond.Status, cond.Detail)
	}
}

// --- 2. A Secret with no ownership marker ------------------------------------

// TestConnectionReconciliation_UnmarkedSecretIsNotReportedAsOwned is the Edge
// Case Hunter's trigger, end to end: a git-listed cluster whose live Secret
// carries NO app.kubernetes.io/managed-by label at all — stripped, hand-made,
// added by `argocd cluster add`, or a half-finished adopt.
//
// Every assertion below was false before this story.
func TestConnectionReconciliation_UnmarkedSecretIsNotReportedAsOwned(t *testing.T) {
	out := ownershipTuple(t, map[string]string{"datadog": "enabled"}, func(s *corev1.Secret) {
		delete(s.Labels, argosecrets.LabelManagedBy)
	}, false)

	// The ownership row states the live fact, and it is not a passed check.
	cond, ok := conditionByID(out, conditionOwnership)
	if !ok {
		t.Fatal("no ownership condition")
	}
	if cond.Status == conditionStatusOK {
		t.Error("the ownership check passes for a Secret that carries no ownership marker")
	}
	if cond.Detail != condOwnershipNotMarked {
		t.Errorf("ownership detail = %q, want the unmarked sentence", cond.Detail)
	}
	if cond.Detail == condOwnershipOK {
		t.Error("the page says Sharko owns a Secret that does not say so")
	}

	// An admin has to approve a change to the ownership marker.
	if !out.Sync.ApprovalRequired {
		t.Error("approval_required is false for a missing ownership marker")
	}

	// The repair door is NOT offered: RepairOwnedConnection refuses on this
	// exact marker, so offering it would be a button that always comes back
	// refused. The withheld door explains its absence instead.
	if out.Plan.Action == planActionRepairConnection {
		t.Error("the repair is offered on a connection the write path refuses")
	}
	if out.Sync.Reason == reasonOutOfSyncApprovalRequired {
		t.Error("sync.reason points at the repair door while the door is withheld")
	}

	// And nothing is promised to happen by itself.
	if out.Plan.Automatic != "" {
		t.Errorf("plan.automatic = %q on a Secret nothing writes to", out.Plan.Automatic)
	}
}

// TestConnectionReconciliation_UnmarkedSecretNeverPromisesSelfHeal is
// requirement 3 in the case where a real addon label ALSO differs — the case
// where the promise looks most defensible and is still false. The periodic
// pass reaches a cluster's Secret through listManagedSecrets, which selects on
// app.kubernetes.io/managed-by=sharko, so an unmarked Secret is invisible to
// it and the create path then leaves it alone as Adopt territory, every tick.
//
// Driven with the self-heal setting ON and again on a v4 repo, which are the
// two ways labelsSelfHeal becomes true.
func TestConnectionReconciliation_UnmarkedSecretNeverPromisesSelfHeal(t *testing.T) {
	for _, selfHealOn := range []bool{false, true} {
		out := ownershipTuple(t, map[string]string{"datadog": "enabled"}, func(s *corev1.Secret) {
			delete(s.Labels, argosecrets.LabelManagedBy)
			s.Labels["datadog"] = "disabled" // a genuine addon-label difference
		}, selfHealOn)

		if !containsPath(driftPaths(out.Drift.AddonLabels), "metadata.labels[datadog]") {
			t.Fatalf("self_heal=%v: the addon-label difference is missing from addon_labels: %v",
				selfHealOn, driftPaths(out.Drift.AddonLabels))
		}
		if out.Plan.Automatic != "" {
			t.Errorf("self_heal=%v: plan.automatic = %q — nothing re-applies labels on an unmarked Secret",
				selfHealOn, out.Plan.Automatic)
		}
	}
}

// TestConnectionReconciliation_MarkedSecretStillSelfHeals is the other
// direction of the same gate, so the fix cannot pass by simply never promising
// anything. A connection that DOES carry Sharko's marker, with the self-heal
// setting on and nothing but addon-label drift, still gets the promise.
func TestConnectionReconciliation_MarkedSecretStillSelfHeals(t *testing.T) {
	out := ownershipTuple(t, map[string]string{"datadog": "enabled"}, func(s *corev1.Secret) {
		s.Labels["datadog"] = "disabled"
	}, true)

	if out.Plan.Automatic != planAutomaticLabelSync {
		t.Errorf("plan.automatic = %q, want the label-sync promise on a marked Secret", out.Plan.Automatic)
	}
	if out.Sync.ApprovalRequired {
		t.Error("approval_required is true for pure addon-label drift")
	}
	if out.Sync.Reason != reasonOutOfSyncLabelsOnly {
		t.Errorf("sync.reason = %q, want the addon-label sentence", out.Sync.Reason)
	}
}

// --- 3. A real addon label is still an addon label ---------------------------

// TestConnectionReconciliation_AddonLabelDriftStillGroupsAsAddonLabels guards
// the over-correction. Both addon-label vocabularies — the bare v3 key and the
// qualified v4 addons.sharko.dev/ key, which contains a "/" and would be
// misread by any "no slash" shortcut — must still land in addon_labels, with
// no approval required.
func TestConnectionReconciliation_AddonLabelDriftStillGroupsAsAddonLabels(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"a v3 addon key", "datadog"},
		{"a v4 addon key", models.V4AddonLabelPrefix + "datadog"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addonLabels := map[string]string{tc.key: "enabled"}
			out := ownershipTuple(t, addonLabels, func(s *corev1.Secret) {
				delete(s.Labels, tc.key)
			}, false)

			wantPath := "metadata.labels[" + tc.key + "]"
			if got := driftPaths(out.Drift.AddonLabels); !containsPath(got, wantPath) {
				t.Fatalf("addon_labels should carry %q, got %v", wantPath, got)
			}
			if got := driftPaths(out.Drift.ConnectionConfiguration); len(got) != 0 {
				t.Errorf("connection_configuration should be empty, got %v", got)
			}
			if out.Sync.ApprovalRequired {
				t.Error("approval_required is true for pure addon-label drift")
			}
			if out.Sync.Reason != reasonOutOfSyncLabelsOnly {
				t.Errorf("sync.reason = %q, want the addon-label sentence", out.Sync.Reason)
			}
		})
	}
}

// --- 4. The path parser ------------------------------------------------------

// TestLabelKeyFromFieldPath_RoundTripsAndRefusesEverythingElse pins the
// inverse of labelFieldPath. The round-trip half uses the real comparison's
// own path for a real key rather than a re-typed string, so the two halves of
// the pair cannot drift.
func TestLabelKeyFromFieldPath_RoundTripsAndRefusesEverythingElse(t *testing.T) {
	keys := []string{
		"datadog",
		argosecrets.LabelManagedBy,
		argosecrets.LabelSecretType,
		models.V4AddonLabelPrefix + "datadog",
	}
	for _, key := range keys {
		path := "metadata.labels[" + key + "]"
		got, ok := connectioncompare.LabelKeyFromFieldPath(path)
		if !ok || got != key {
			t.Errorf("LabelKeyFromFieldPath(%q) = %q,%v; want %q,true", path, got, ok, key)
		}
	}

	notLabels := []string{
		"", "data.server", "data.config", "type", "metadata.name",
		"metadata.labels", "metadata.labels[", "metadata.labels[]",
		"metadata.labels[datadog", "xmetadata.labels[datadog]",
	}
	for _, path := range notLabels {
		if got, ok := connectioncompare.LabelKeyFromFieldPath(path); ok {
			t.Errorf("LabelKeyFromFieldPath(%q) = %q,true; want false", path, got)
		}
	}
}

// TestIsAddonLabelDrift_UsesTheOwnershipPredicate states the grouping rule
// directly, over every path shape the comparison can produce.
//
// It deliberately does NOT compare isAddonLabelDrift against a second copy of
// the rule — a test whose expectation is the implementation restated proves
// nothing. Each case names its answer.
func TestIsAddonLabelDrift_UsesTheOwnershipPredicate(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"metadata.labels[datadog]", true},
		{"metadata.labels[" + models.V4AddonLabelPrefix + "datadog]", true},
		{"metadata.labels[" + argosecrets.LabelManagedBy + "]", false},
		{"metadata.labels[" + argosecrets.LabelSecretType + "]", false},
		{"metadata.labels[sharko.dev/connectivity-check]", false},
		{"metadata.labels[app.kubernetes.io/instance]", false},
		{"data.server", false},
		{"data.config", false},
		{"type", false},
		{"metadata.name", false},
	}
	for _, tc := range cases {
		if got := isAddonLabelDrift(tc.path); got != tc.want {
			t.Errorf("isAddonLabelDrift(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
