package api

// connection_reconciliation_test.go — Story 1 of the connection
// reconciliation view.
//
// Three layers:
//
//  1. THE INVARIANT (product correction 1): sync.state == "synced" with
//     verification_scope != "full" is a bug by definition. The builder is
//     driven across every mode × situation combination it can possibly be
//     handed, and any response with that pair fails the test.
//  2. THE MATRIX: every page-state-matrix-v3 row the API produces (1–16;
//     row 17 is a request-local UI state and deliberately not in the API)
//     is pinned as (sync_state, verification_scope, managed_scope,
//     approval_required, plan.action).
//  3. THE SENTENCES: the mode statements from product ruling 8 are pinned
//     by EXACT equality against literals written in this file — the project
//     lesson is that != "" tests let a wrong sentence survive four rounds.
//
// Plus the endpoint half: the handler joins provenance and health onto the
// same read-only core the comparison endpoint runs, leaks nothing, and the
// legacy-inline no-reconstruction rules hold end to end.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/settings"
)

// reconTestCommit is the fixture git head SHA (comparisonGP's headSHA).
const reconTestCommit = "1111111111111111111111111111111111111111"

// reconView builds a baseline comparison view — a clean, full-scope,
// backend-stored cluster — and lets each test mutate exactly the fields its
// situation needs.
func reconView(mut func(*connectionComparisonView)) connectionComparisonView {
	v := connectionComparisonView{
		Cluster:              comparisonCluster,
		Status:               string(connectioncompare.StatusSynced),
		Scope:                string(connectioncompare.ScopeFull),
		OwnershipMode:        string(connectioncompare.ModeBackendStoredCredentials),
		CheckedAt:            "2026-08-18T10:00:00Z",
		Branch:               "main",
		ComparedCommit:       reconTestCommit,
		ComparedPath:         clusterreconciler.DefaultManagedClustersPath,
		CredentialSourceType: models.CredsSourceSecretKubeconfig,
		Differences:          []connectionComparisonDifference{},
		NotChecked:           []connectionComparisonNotChecked{},
		RepairAvailable:      true,
		RepairScope:          string(connectioncompare.RepairScopeFullConnection),
		ValuesNeverReturned:  true,
	}
	if mut != nil {
		mut(&v)
	}
	return v
}

func reconSafeDiff(path string) connectionComparisonDifference {
	expected, live := "expected-value", "live-value"
	return connectionComparisonDifference{Path: path, Status: "different", Expected: &expected, Live: &live}
}

func reconSensitiveDiff() connectionComparisonDifference {
	return connectionComparisonDifference{Path: "data.config", Status: "different", Sensitive: true}
}

func buildRecon(v connectionComparisonView, selfHealOn bool) connectionReconciliationView {
	return buildConnectionReconciliationView(connectionReconciliationFacts{
		view:        v,
		healthState: healthStateConnected,
		selfHealOn:  selfHealOn,
	})
}

// --- 1. The synchronization invariant ---------------------------------------

// TestConnectionReconciliation_SyncedRequiresFullScope_Invariant drives the
// builder across the full cross product of every status, scope, ownership
// mode, drift shape, repair offer, commit knowledge, self-heal setting and
// repo layout the handler could ever hand it — including incoherent
// combinations the comparison itself never produces — and FAILS on any
// response where sync.state == "synced" && verification_scope != "full".
func TestConnectionReconciliation_SyncedRequiresFullScope_Invariant(t *testing.T) {
	statuses := []connectioncompare.Status{
		connectioncompare.StatusSynced, connectioncompare.StatusOutOfSync,
		connectioncompare.StatusMissing, connectioncompare.StatusCheckFailed,
		connectioncompare.StatusOwnershipConflict, connectioncompare.StatusLimited,
		connectioncompare.Status(""),
	}
	scopes := []connectioncompare.Scope{
		connectioncompare.ScopeFull, connectioncompare.ScopeLimited,
		connectioncompare.ScopeAddonLabelsOnly, connectioncompare.ScopeOwnershipConflict,
		connectioncompare.Scope(""),
	}
	modes := []connectioncompare.Mode{
		connectioncompare.ModeBackendStoredCredentials, connectioncompare.ModeEKSToken,
		connectioncompare.ModeInlineKubeconfig, connectioncompare.ModeSelfManaged,
		connectioncompare.ModeAdopted, connectioncompare.ModeForeignOwned,
		connectioncompare.ModeUnknownSource, connectioncompare.Mode(""),
	}
	sources := []string{
		models.CredsSourceSecretKubeconfig, models.CredsSourceEKSToken,
		models.CredsSourceInlineKubeconfig, "",
	}
	diffSets := [][]connectionComparisonDifference{
		nil,
		{reconSafeDiff("metadata.labels[datadog]")},
		{reconSafeDiff("data.server")},
		{reconSensitiveDiff()},
		{reconSafeDiff("metadata.labels[datadog]"), reconSensitiveDiff()},
	}
	notCheckedSets := [][]connectionComparisonNotChecked{
		nil,
		{{Path: "data.config", Reason: "not compared in this test"}},
	}
	repairs := []struct {
		available bool
		scope     connectioncompare.RepairScope
	}{
		{false, connectioncompare.RepairScopeNone},
		{true, connectioncompare.RepairScopeFullConnection},
		{true, connectioncompare.RepairScopeAddonLabelsOnly},
	}
	paths := []string{clusterreconciler.DefaultManagedClustersPath, clusterreconciler.V4ManagedClustersPath}
	commits := []string{"", reconTestCommit}
	limitReasons := []string{"", "a limit sentence"}

	type combo struct {
		status      connectioncompare.Status
		scope       connectioncompare.Scope
		mode        connectioncompare.Mode
		source      string
		diffs       []connectionComparisonDifference
		nc          []connectionComparisonNotChecked
		repairAvail bool
		repairScope connectioncompare.RepairScope
		path        string
		commit      string
		limitReason string
		selfHeal    bool
	}
	var combos []combo
	for _, status := range statuses {
		for _, scope := range scopes {
			for _, mode := range modes {
				for _, source := range sources {
					for _, diffs := range diffSets {
						for _, nc := range notCheckedSets {
							for _, rep := range repairs {
								for _, path := range paths {
									for _, commit := range commits {
										for _, limitReason := range limitReasons {
											for _, selfHeal := range []bool{false, true} {
												combos = append(combos, combo{status, scope, mode, source, diffs, nc, rep.available, rep.scope, path, commit, limitReason, selfHeal})
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	checked := 0
	for _, c := range combos {
		v := reconView(func(cv *connectionComparisonView) {
			cv.Status = string(c.status)
			cv.Scope = string(c.scope)
			cv.OwnershipMode = string(c.mode)
			cv.CredentialSourceType = c.source
			cv.Differences = c.diffs
			cv.NotChecked = c.nc
			cv.RepairAvailable = c.repairAvail
			cv.RepairScope = string(c.repairScope)
			cv.ComparedPath = c.path
			cv.ComparedCommit = c.commit
			cv.LimitReason = c.limitReason
			cv.FailureReason = "a failure sentence"
		})
		out := buildRecon(v, c.selfHeal)
		checked++

		if out.Sync.State == syncStateSynced && out.Sync.VerificationScope != verificationScopeFull {
			t.Fatalf("INVARIANT VIOLATED: sync.state=synced with verification_scope=%q for %+v",
				out.Sync.VerificationScope, c)
		}
		// A reported difference must never wear the synced word, whatever
		// the input status claimed.
		if out.Sync.State == syncStateSynced && len(v.Differences) > 0 {
			t.Fatalf("synced with %d reported differences (%+v)", len(v.Differences), c)
		}
		// Blocked is deterministic: foreign ownership only.
		if out.Sync.State == syncStateBlocked && c.status != connectioncompare.StatusOwnershipConflict {
			t.Fatalf("blocked without an ownership conflict (%+v)", c)
		}
		// legacy_inline never gets repair_connection; foreign never gets
		// anything but take_over (dispatch hard rules).
		if out.ManagementMode == managementModeLegacyInline && out.Plan.Action == planActionRepairConnection {
			t.Fatalf("legacy_inline offered repair_connection (%+v)", c)
		}
		if out.ManagementMode == managementModeForeignOwned && out.Plan.Action != planActionTakeOver {
			t.Fatalf("foreign_owned offered %q, only take_over is allowed (%+v)", out.Plan.Action, c)
		}
		// approval_required is true exactly when drift touches connection
		// configuration or credential material.
		wantApproval := len(out.Drift.ConnectionConfiguration) > 0 || len(out.Drift.CredentialMaterial) > 0
		if out.Sync.ApprovalRequired != wantApproval {
			t.Fatalf("approval_required=%v, want %v (config=%d cred=%d, %+v)",
				out.Sync.ApprovalRequired, wantApproval,
				len(out.Drift.ConnectionConfiguration), len(out.Drift.CredentialMaterial), c)
		}
		// The generic repair-door sentences ride ONLY with an
		// actually-offered repair. A withheld door must carry the
		// explanation of its absence instead of pointing the admin at an
		// action that is not there (review blocker, 2026-08-18).
		if out.Sync.ApprovalRequired && out.Plan.Action != planActionRepairConnection {
			if out.Sync.Reason == reasonOutOfSyncApprovalRequired {
				t.Fatalf("sync.reason points at the repair action while plan.action=%q (%+v)", out.Plan.Action, c)
			}
			if out.Plan.RequiresApproval == planRequiresApprovalSentence {
				t.Fatalf("plan.requires_approval points at the repair action while plan.action=%q (%+v)", out.Plan.Action, c)
			}
			for _, cond := range out.Conditions {
				if cond.ID == conditionApproval && cond.Detail == condApprovalRequired {
					t.Fatalf("the approval condition points at the repair action while plan.action=%q (%+v)", out.Plan.Action, c)
				}
			}
			if out.Sync.State == syncStateOutOfSync && out.Sync.Reason == "" {
				t.Fatalf("approval-gated out_of_sync with no reason at all (%+v)", c)
			}
		}
		// Ruling (d): the verification enum is CLOSED at three values. This
		// is what keeps the removed labels_only from creeping back — and
		// what would catch any other speculative value being published on
		// the wire, across every combination the builder can be handed.
		switch out.Sync.VerificationScope {
		case verificationScopeFull, verificationScopePartial, verificationScopeNone:
		default:
			t.Fatalf("verification_scope = %q, outside the closed enum full|partial|none (%+v)", out.Sync.VerificationScope, c)
		}
		// Scope honesty (F2): a "full" verification claim never rides over
		// deliberately-unchecked fields.
		if out.Sync.VerificationScope == verificationScopeFull && len(v.NotChecked) > 0 {
			t.Fatalf("verification_scope=full with %d unchecked field(s) (%+v)", len(v.NotChecked), c)
		}
		// An unknown produced by a comparison that RAN as limited never has
		// an empty reason (the never-run row 15 is the one legitimate
		// empty, and its status is empty too).
		if out.Sync.State == syncStateUnknown && c.status == connectioncompare.StatusLimited && out.Sync.Reason == "" {
			t.Fatalf("unknown from a limited comparison with an empty reason (%+v)", c)
		}
	}
	if checked < 100000 {
		t.Fatalf("the grid shrank to %d combinations — the invariant sweep lost coverage", checked)
	}
}

// --- 2. The matrix, row by row ----------------------------------------------

func TestConnectionReconciliation_MatrixV3Rows(t *testing.T) {
	type want struct {
		state        string
		verification string
		managedScope string
		approval     bool
		action       string
		automatic    string // "" means "must be absent"
		reason       string // "" means "not asserted"
	}
	cases := []struct {
		row      string
		view     connectionComparisonView
		selfHeal bool
		want     want
	}{
		{
			row:  "1: clean, secret-kubeconfig (sharko_managed)",
			view: reconView(nil),
			want: want{state: syncStateSynced, verification: verificationScopeFull, managedScope: managedScopeFullConnection, action: planActionNone},
		},
		{
			row: "2: clean, EKS (sharko_managed) — never rolled into synced",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.OwnershipMode = string(connectioncompare.ModeEKSToken)
				v.CredentialSourceType = models.CredsSourceEKSToken
				v.LimitReason = connectioncompare.LimitReasonEKSNoStoredCredential
				v.NotChecked = []connectionComparisonNotChecked{{Path: "data.config", Reason: "no stored credential"}}
			}),
			want: want{state: syncStateUnknown, verification: verificationScopePartial, managedScope: managedScopeFullConnection, action: planActionRepairConnection, reason: connectioncompare.LimitReasonEKSNoStoredCredential},
		},
		{
			row: "3: new git revision, details changed (sharko_managed)",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("data.server")}
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeFullConnection, approval: true, action: planActionRepairConnection, reason: reasonOutOfSyncApprovalRequired},
		},
		{
			row: "3b: details drift, commit unknown — repair withheld (R3-8)",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("data.server")}
				// The comparison already withdrew the repair and wrote the
				// R3-8 withdrawal sentence into the limit reason — exactly
				// what compareClusterConnection does when the git provider
				// cannot name a commit.
				v.ComparedCommit = ""
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
				v.LimitReason = "Sharko cannot tell which commit your git branch is on, so it will not offer to rewrite this connection. Sharko only makes this change when it can name the exact commit it is matching."
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeFullConnection, approval: true, action: planActionNone, reason: "Sharko cannot tell which commit your git branch is on, so it will not offer to rewrite this connection. Sharko only makes this change when it can name the exact commit it is matching."},
		},
		{
			row: "4: v4 addon-label drift (sharko_managed) — converges by itself",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.ComparedPath = clusterreconciler.V4ManagedClustersPath
				v.Differences = []connectionComparisonDifference{reconSafeDiff("metadata.labels[addons.sharko.dev/datadog]")}
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeFullConnection, action: planActionNone, automatic: planAutomaticLabelSync},
		},
		{
			row: "5: slash-free label drift, self-heal off — the label-sync door",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("metadata.labels[datadog]")}
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeFullConnection, action: planActionSyncAddonLabels},
		},
		{
			row: "5b: slash-free label drift, self-heal ON — converges by itself",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("metadata.labels[datadog]")}
			}),
			selfHeal: true,
			want:     want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeFullConnection, action: planActionNone, automatic: planAutomaticLabelSync},
		},
		{
			row: "6: credential rotation, no new commit (secret-kubeconfig)",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSensitiveDiff()}
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeFullConnection, approval: true, action: planActionRepairConnection, reason: reasonOutOfSyncApprovalRequired},
		},
		{
			row: "7: mixed label + credential drift",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("metadata.labels[datadog]"), reconSensitiveDiff()}
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeFullConnection, approval: true, action: planActionRepairConnection},
		},
		{
			row: "8: credentials provider unavailable",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusCheckFailed)
				v.FailureReason = failBackendRead
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
			}),
			want: want{state: syncStateUnknown, verification: verificationScopeNone, managedScope: managedScopeFullConnection, action: planActionNone, reason: failBackendRead},
		},
		{
			row: "9: foreign ownership",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOwnershipConflict)
				v.Scope = string(connectioncompare.ScopeOwnershipConflict)
				v.OwnershipMode = string(connectioncompare.ModeForeignOwned)
				v.LimitReason = modeStatementForeignOwned
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
			}),
			want: want{state: syncStateBlocked, verification: verificationScopeNone, managedScope: managedScopeNone, action: planActionTakeOver},
		},
		{
			row: "10: live Secret missing (durable source) — created next pass",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusMissing)
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeNone, managedScope: managedScopeFullConnection, action: planActionNone, automatic: planAutomaticSecretCreate, reason: reasonSecretMissingDurable},
		},
		{
			row: "11: live Secret missing (legacy_inline) — cannot be restored",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusMissing)
				v.OwnershipMode = string(connectioncompare.ModeInlineKubeconfig)
				v.CredentialSourceType = models.CredsSourceInlineKubeconfig
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeNone, managedScope: managedScopeAddonLabels, action: planActionMigrateCredential, reason: reasonSecretMissingLegacyInline},
		},
		{
			row: "12: legacy inline, present and clean — verification incomplete",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.OwnershipMode = string(connectioncompare.ModeInlineKubeconfig)
				v.CredentialSourceType = models.CredsSourceInlineKubeconfig
				v.LimitReason = "the inline limit sentence"
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
			}),
			want: want{state: syncStateUnknown, verification: verificationScopePartial, managedScope: managedScopeAddonLabels, action: planActionMigrateCredential},
		},
		{
			row: "13: self-managed, labels match — synced of the owned scope",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				v.OwnershipMode = string(connectioncompare.ModeSelfManaged)
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
			}),
			want: want{state: syncStateSynced, verification: verificationScopeFull, managedScope: managedScopeAddonLabels, action: planActionNone},
		},
		{
			// MATRIX v4, ruling (a): row 14 is AUTOMATIC. The matrix was
			// wrong and the running reconciler is authoritative —
			// syncSelfManaged re-applies the addon labels on every tick with
			// no setting gate, so Sharko must not offer a manual button for
			// work it does itself. action none, approval_required false.
			row: "14: self-managed, labels drifted — converges by itself (ruling a)",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				v.OwnershipMode = string(connectioncompare.ModeSelfManaged)
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("metadata.labels[datadog]")}
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeAddonLabels, action: planActionNone, automatic: planAutomaticLabelSync},
		},
		{
			row: "15: never checked",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = ""
				v.Scope = ""
				v.OwnershipMode = ""
				v.CheckedAt = ""
				v.ComparedCommit = ""
				v.RepairAvailable = false
				v.RepairScope = ""
			}),
			want: want{state: syncStateUnknown, verification: verificationScopeNone, managedScope: managedScopeFullConnection, action: planActionNone},
		},
		{
			row: "16: check failed",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusCheckFailed)
				v.FailureReason = failLiveRead
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
			}),
			want: want{state: syncStateUnknown, verification: verificationScopeNone, managedScope: managedScopeFullConnection, action: planActionNone, reason: failLiveRead},
		},
	}

	for _, tc := range cases {
		t.Run("row "+tc.row, func(t *testing.T) {
			out := buildRecon(tc.view, tc.selfHeal)
			if out.Sync.State != tc.want.state {
				t.Errorf("sync.state = %q, want %q", out.Sync.State, tc.want.state)
			}
			if out.Sync.VerificationScope != tc.want.verification {
				t.Errorf("verification_scope = %q, want %q", out.Sync.VerificationScope, tc.want.verification)
			}
			if out.ManagedScope != tc.want.managedScope {
				t.Errorf("managed_scope = %q, want %q", out.ManagedScope, tc.want.managedScope)
			}
			if out.Sync.ApprovalRequired != tc.want.approval {
				t.Errorf("approval_required = %v, want %v", out.Sync.ApprovalRequired, tc.want.approval)
			}
			if out.Plan.Action != tc.want.action {
				t.Errorf("plan.action = %q, want %q", out.Plan.Action, tc.want.action)
			}
			if out.Plan.Automatic != tc.want.automatic {
				t.Errorf("plan.automatic = %q, want %q", out.Plan.Automatic, tc.want.automatic)
			}
			if tc.want.reason != "" && out.Sync.Reason != tc.want.reason {
				t.Errorf("sync.reason = %q, want %q", out.Sync.Reason, tc.want.reason)
			}
			if out.Plan.Action == planActionRepairConnection {
				if out.Plan.ReviewedCommit != tc.view.ComparedCommit {
					t.Errorf("reviewed_commit = %q, want the compared commit %q", out.Plan.ReviewedCommit, tc.view.ComparedCommit)
				}
			} else if out.Plan.ReviewedCommit != "" {
				t.Errorf("reviewed_commit = %q on action %q, want empty", out.Plan.ReviewedCommit, out.Plan.Action)
			}
			if !out.ValuesNeverReturned {
				t.Error("values_never_returned must always be true")
			}
		})
	}
}

// Row 15's half of the W3-6 rule at the wire: an empty checked_at is ABSENT
// from the JSON, never Go's zero time and never an empty string.
func TestConnectionReconciliation_CheckedAtAbsentWhenNeverRun(t *testing.T) {
	out := buildRecon(reconView(func(v *connectionComparisonView) {
		v.Status = ""
		v.CheckedAt = ""
	}), false)
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "checked_at") {
		t.Fatalf("checked_at present in a never-run response: %s", body)
	}
	if strings.Contains(string(body), "0001-01-01") {
		t.Fatalf("Go zero time in response: %s", body)
	}
	if strings.Contains(string(body), "last_successful_application") {
		t.Fatalf("last_successful_application present with no known application: %s", body)
	}
}

// --- 3. The exact sentences (ruling 8) ---------------------------------------

// TestConnectionReconciliation_ModeStatementsExact pins the approved
// sentences character for character, as LITERALS IN THIS TEST — not
// references to the production constants — so a paraphrase in either place
// fails. (Project lesson: a wrong sentence survived four review rounds
// because its only test asserted != "".)
func TestConnectionReconciliation_ModeStatementsExact(t *testing.T) {
	cases := []struct {
		mode     connectioncompare.Mode
		wantMode string
		exact    string
	}{
		{connectioncompare.ModeBackendStoredCredentials, "sharko_managed",
			"Git defines the connection. Sharko resolves its credential references and maintains the resulting ArgoCD Secret."},
		{connectioncompare.ModeEKSToken, "sharko_managed",
			"Git defines the connection. Sharko resolves its credential references and maintains the resulting ArgoCD Secret."},
		{connectioncompare.ModeSelfManaged, "self_managed",
			"Connection data is managed outside Sharko. Sharko reconciles only its addon-label keys from Git."},
		{connectioncompare.ModeAdopted, "self_managed",
			"Connection data is managed outside Sharko. Sharko reconciles only its addon-label keys from Git."},
		{connectioncompare.ModeInlineKubeconfig, "legacy_inline",
			"This connection's credential exists only in the live Secret and cannot be restored from Git."},
		{connectioncompare.ModeForeignOwned, "foreign_owned",
			"Another tool owns this connection, so Sharko will not change it."},
	}
	for _, tc := range cases {
		out := buildRecon(reconView(func(v *connectionComparisonView) {
			v.OwnershipMode = string(tc.mode)
		}), false)
		if out.ManagementMode != tc.wantMode {
			t.Errorf("mode %q: management_mode = %q, want %q", tc.mode, out.ManagementMode, tc.wantMode)
		}
		if out.ModeStatement != tc.exact {
			t.Errorf("mode %q: mode_statement = %q, want exactly %q", tc.mode, out.ModeStatement, tc.exact)
		}
	}
}

// The three new missing-Secret reason sentences and the two plan sentences
// are pinned exactly too — they are new user-facing text this story
// introduces.
func TestConnectionReconciliation_NewSentencesExact(t *testing.T) {
	exact := map[string]string{
		reasonSecretMissingDurable:      "This cluster has no connection Secret right now. Sharko will create it from git and the configured credentials source on the reconciler's next pass.",
		reasonSecretMissingLegacyInline: "This cluster's connection Secret is gone, and its credential existed only in that Secret — Sharko cannot restore it from Git. Store a fresh credential in a supported credentials provider and move the cluster onto it.",
		reasonSecretMissingSelfManaged:  "You maintain this cluster's connection Secret yourself and it has not been created yet. Sharko does not create it.",
		planAutomaticSecretCreate:       "Sharko will create this connection Secret from git and the configured credentials source on the reconciler's next pass.",
		planAutomaticLabelSync:          "Sharko re-applies the addon labels git declares on the reconciler's next pass.",
		reasonOutOfSyncApprovalRequired: "The live connection no longer matches what git defines. Sharko will not change connection details or credential material by itself — an admin reviews and applies the change through the repair action.",
		reasonOutOfSyncLegacyInline:     "The live connection no longer matches what git defines. Its credential exists only in the live Secret, so Sharko cannot rebuild the connection from Git. Store a fresh credential in a supported credentials provider and move the cluster onto it.",
		reasonOutOfSyncRepairWithheld:   "The live connection no longer matches what git defines, and Sharko cannot offer a repair for this connection right now.",
		reasonVerificationIncomplete:    "Sharko could not compare part of what it owns on this connection, so it cannot call the connection synced.",
	}
	for got, want := range exact {
		if got != want {
			t.Errorf("sentence drifted:\n got %q\nwant %q", got, want)
		}
	}
	// The old missing-Secret promise must never be reused for a legacy
	// inline cluster: "will create it on its next pass" is a reconstruction
	// promise Sharko cannot keep there.
	if strings.Contains(reasonSecretMissingLegacyInline, "next pass") {
		t.Error("the legacy-inline missing sentence promises a next-pass fix — banned (ruling 1)")
	}
	// The legacy-inline withheld-door sentence must never point at the
	// repair action — the door does not exist for this mode.
	if strings.Contains(strings.ToLower(reasonOutOfSyncLegacyInline), "repair") {
		t.Error("the legacy-inline drift sentence mentions the repair action — banned (review blocker)")
	}
}

// TestConnectionReconciliation_RepairWithheldExplanations pins the review
// blocker end to end: whenever drift needs an admin but the repair door is
// NOT offered, the body explains WHY the door is absent — and the generic
// "…through the repair action" sentences appear nowhere in the response.
func TestConnectionReconciliation_RepairWithheldExplanations(t *testing.T) {
	// The R3-8 withdrawal sentence, verbatim — the exact literal
	// compareClusterConnection writes when the git provider cannot name a
	// commit.
	const r38Sentence = "Sharko cannot tell which commit your git branch is on, so it will not offer to rewrite this connection. Sharko only makes this change when it can name the exact commit it is matching."

	cases := []struct {
		name       string
		view       connectionComparisonView
		wantAction string
		wantReason string
	}{
		{
			name: "commit unknown — repair withdrawn (R3-8), sentence surfaces verbatim",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("data.server")}
				v.ComparedCommit = ""
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
				v.LimitReason = r38Sentence
			}),
			wantAction: planActionNone,
			wantReason: r38Sentence,
		},
		{
			name: "legacy inline with connection-configuration drift — migration, never the repair door",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.OwnershipMode = string(connectioncompare.ModeInlineKubeconfig)
				v.CredentialSourceType = models.CredsSourceInlineKubeconfig
				v.Differences = []connectionComparisonDifference{reconSafeDiff("type")}
				v.RepairAvailable = true
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
				v.LimitReason = "the inline limit sentence"
			}),
			wantAction: planActionMigrateCredential,
			wantReason: reasonOutOfSyncLegacyInline,
		},
		{
			name: "unknown source with connection-configuration drift — the not-recorded sentence explains",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.OwnershipMode = string(connectioncompare.ModeUnknownSource)
				v.CredentialSourceType = ""
				v.Differences = []connectionComparisonDifference{reconSafeDiff("type")}
				v.RepairAvailable = true
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
				v.LimitReason = connectioncompare.LimitReasonSourceNotRecorded
			}),
			wantAction: planActionNone,
			wantReason: connectioncompare.LimitReasonSourceNotRecorded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := buildRecon(tc.view, false)

			if !out.Sync.ApprovalRequired {
				t.Fatal("connection-configuration drift must set approval_required")
			}
			if out.Plan.Action != tc.wantAction {
				t.Fatalf("plan.action = %q, want %q", out.Plan.Action, tc.wantAction)
			}
			if out.Sync.Reason != tc.wantReason {
				t.Errorf("sync.reason = %q, want the withheld-door explanation %q", out.Sync.Reason, tc.wantReason)
			}
			if out.Plan.RequiresApproval != tc.wantReason {
				t.Errorf("plan.requires_approval = %q, want the withheld-door explanation %q", out.Plan.RequiresApproval, tc.wantReason)
			}
			sawApprovalCond := false
			for _, c := range out.Conditions {
				if c.ID == conditionApproval {
					sawApprovalCond = true
					if c.Detail != tc.wantReason {
						t.Errorf("approval condition detail = %q, want %q", c.Detail, tc.wantReason)
					}
				}
			}
			if !sawApprovalCond {
				t.Error("the approval condition is missing")
			}

			// The generic repair-door sentences must appear NOWHERE in the
			// marshalled response — not in the reason, the plan, or any
			// condition.
			body, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, banned := range []string{reasonOutOfSyncApprovalRequired, planRequiresApprovalSentence, condApprovalRequired} {
				if strings.Contains(string(body), banned) {
					t.Errorf("the response points the admin at a repair door that is not offered: %q", banned)
				}
			}
		})
	}
}

// --- The endpoint ------------------------------------------------------------

func reconciliationReq(cluster string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+cluster+"/connection-reconciliation", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	return req
}

func decodeRecon(t *testing.T, w *httptest.ResponseRecorder) connectionReconciliationView {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var out connectionReconciliationView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return out
}

// TestConnectionReconciliation_SyncedThroughTheEndpoint: the full join — the
// same core as the comparison, plus provenance annotations off the live
// Secret.
func TestConnectionReconciliation_SyncedThroughTheEndpoint(t *testing.T) {
	live := expectedLiveSecretForFixture(t)
	live.Annotations = map[string]string{
		clusterreconciler.AnnotationRevision:  reconTestCommit,
		clusterreconciler.AnnotationWrittenAt: "2026-08-18T09:00:00Z",
	}
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	out := decodeRecon(t, w)

	if out.ManagementMode != managementModeSharkoManaged {
		t.Errorf("management_mode = %q, want sharko_managed", out.ManagementMode)
	}
	if out.Sync.State != syncStateSynced || out.Sync.VerificationScope != verificationScopeFull {
		t.Fatalf("sync = %q/%q, want synced/full (drift: %+v)", out.Sync.State, out.Sync.VerificationScope, out.Drift)
	}
	if out.Definition.DesiredRevision != reconTestCommit {
		t.Errorf("desired_revision = %q, want %q", out.Definition.DesiredRevision, reconTestCommit)
	}
	if out.Definition.AppliedRevision != reconTestCommit {
		t.Errorf("applied_revision = %q, want the Secret's provenance annotation %q", out.Definition.AppliedRevision, reconTestCommit)
	}
	if out.Sync.LastSuccessfulApplication != "2026-08-18T09:00:00Z" {
		t.Errorf("last_successful_application = %q, want the written-at annotation", out.Sync.LastSuccessfulApplication)
	}
	if out.Sync.CheckedAt == "" {
		t.Error("checked_at must be present after a completed check")
	}
	if out.Plan.Action != planActionNone {
		t.Errorf("plan.action = %q, want none for a synced connection", out.Plan.Action)
	}
	if !out.ValuesNeverReturned {
		t.Error("values_never_returned must be true")
	}
	if strings.Contains(w.Body.String(), "0001-01-01") {
		t.Errorf("response carries Go's zero time: %s", w.Body.String())
	}
}

// TestConnectionReconciliation_NeverReturnsTheSecretValue: the sentinel
// really is the stored token and the live value differs, so the endpoint has
// something to leak — and no form of it may appear.
func TestConnectionReconciliation_NeverReturnsTheSecretValue(t *testing.T) {
	live := expectedLiveSecretForFixture(t)
	live.Data["config"] = []byte("a-rotated-live-config-that-differs")
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	out := decodeRecon(t, w)

	if out.Sync.State != syncStateOutOfSync {
		t.Fatalf("sync.state = %q, want out_of_sync", out.Sync.State)
	}
	if !out.Sync.ApprovalRequired {
		t.Error("credential drift must set approval_required")
	}
	if len(out.Drift.CredentialMaterial) != 1 || out.Drift.CredentialMaterial[0].Path != "data.config" || !out.Drift.CredentialMaterial[0].Sensitive {
		t.Fatalf("credential_material = %+v, want exactly one sensitive data.config entry", out.Drift.CredentialMaterial)
	}
	if out.Plan.Action != planActionRepairConnection {
		t.Errorf("plan.action = %q, want repair_connection", out.Plan.Action)
	}
	if out.Plan.ReviewedCommit != reconTestCommit {
		t.Errorf("reviewed_commit = %q, want %q", out.Plan.ReviewedCommit, reconTestCommit)
	}

	body := w.Body.String()
	for _, form := range sentinelForms() {
		if strings.Contains(body, form) {
			t.Errorf("response contains a form of the secret value: %q", form)
		}
	}
	for _, form := range sentinelLengthForms() {
		if strings.Contains(body, form) {
			t.Errorf("response contains the secret's length: %q", form)
		}
	}
	assertNoSentinelLengthInJSON(t, "reconciliation response", w.Body.Bytes())

	// The sensitive drift entries must carry no expected/live keys at all.
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding raw: %v", err)
	}
	drift := raw["drift"].(map[string]interface{})
	for _, entry := range drift["credential_material"].([]interface{}) {
		m := entry.(map[string]interface{})
		if _, ok := m["expected"]; ok {
			t.Error("a sensitive drift entry carries an expected value")
		}
		if _, ok := m["live"]; ok {
			t.Error("a sensitive drift entry carries a live value")
		}
	}
}

func TestConnectionReconciliation_403ForViewer(t *testing.T) {
	_, router, _ := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+comparisonCluster+"/connection-reconciliation", nil)
	req.Header.Set("X-Sharko-User", "ro")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a viewer, got %d", w.Code)
	}
}

func TestConnectionReconciliation_404WhenClusterNotManaged(t *testing.T) {
	_, router, _ := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq("never-heard-of-it"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestConnectionReconciliation_MissingSecretDurableSource: row 10 end to end
// — no Secret, a durable source, an honest next-pass creation promise.
func TestConnectionReconciliation_MissingSecretDurableSource(t *testing.T) {
	_, router, _ := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	out := decodeRecon(t, w)

	if out.Sync.State != syncStateOutOfSync || out.Sync.VerificationScope != verificationScopeNone {
		t.Fatalf("sync = %q/%q, want out_of_sync/none", out.Sync.State, out.Sync.VerificationScope)
	}
	if out.Sync.Reason != reasonSecretMissingDurable {
		t.Errorf("reason = %q, want the durable missing sentence", out.Sync.Reason)
	}
	if out.Plan.Automatic != planAutomaticSecretCreate {
		t.Errorf("plan.automatic = %q, want the next-pass creation sentence", out.Plan.Automatic)
	}
	if out.Definition.AppliedRevision != "" || out.Sync.LastSuccessfulApplication != "" {
		t.Error("a missing Secret has no applied revision and no last application")
	}
}

// --- Legacy inline through the endpoint (rulings 1 and 5) --------------------

const inlineManagedYAML = "clusters:\n- name: " + comparisonCluster + "\n  credsSource: inline-kubeconfig\n  labels:\n    datadog: enabled\n"

// TestConnectionReconciliation_InlineCluster_WorksWithPasteDisabled proves
// correction 5's "existing inline connections keep working": with
// allow_inline_credentials OFF, an existing inline cluster still gets its
// connection compared and reported — the kill switch governs NEW
// registrations only.
func TestConnectionReconciliation_InlineCluster_WorksWithPasteDisabled(t *testing.T) {
	srv, router, _ := comparisonFixture(t, inlineManagedYAML, expectedLiveSecretForFixture(t), comparisonFakeVault{})

	// The kill switch, explicitly off — the reconciliation read must not care.
	store := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	if err := store.SetAllowInlineCredentials(t.Context(), false); err != nil {
		t.Fatalf("turning the setting off: %v", err)
	}
	srv.SetSettingsStore(store)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	out := decodeRecon(t, w)

	if out.ManagementMode != managementModeLegacyInline {
		t.Fatalf("management_mode = %q, want legacy_inline", out.ManagementMode)
	}
	if out.ModeStatement != "This connection's credential exists only in the live Secret and cannot be restored from Git." {
		t.Errorf("mode_statement = %q, want the exact ruled sentence", out.ModeStatement)
	}
	if out.Sync.State == syncStateSynced {
		t.Error("an inline cluster must never report synced (verification cannot be full)")
	}
	if out.Sync.CheckedAt == "" {
		t.Error("the comparison must actually have run with the setting off")
	}
	if out.Plan.Action == planActionRepairConnection {
		t.Error("legacy_inline must never be offered repair_connection")
	}
	if out.ManagedScope != managedScopeAddonLabels {
		t.Errorf("managed_scope = %q, want addon_labels (the rebuildable scope)", out.ManagedScope)
	}
}

// TestConnectionReconciliation_InlineSecretDeleted_NoReconstructionPromise:
// row 11 end to end — ruling 1's no-reconstruction rules at the wire. (The
// reconciler-side proof that the Secret is really never re-created lives in
// internal/clusterreconciler's no-recreation test.)
func TestConnectionReconciliation_InlineSecretDeleted_NoReconstructionPromise(t *testing.T) {
	_, router, _ := comparisonFixture(t, inlineManagedYAML, nil, comparisonFakeVault{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	out := decodeRecon(t, w)

	if out.ManagementMode != managementModeLegacyInline {
		t.Fatalf("management_mode = %q, want legacy_inline", out.ManagementMode)
	}
	if out.Sync.State != syncStateOutOfSync || out.Sync.VerificationScope != verificationScopeNone {
		t.Fatalf("sync = %q/%q, want out_of_sync/none", out.Sync.State, out.Sync.VerificationScope)
	}
	if out.Sync.Reason != reasonSecretMissingLegacyInline {
		t.Errorf("reason = %q, want the cannot-restore sentence", out.Sync.Reason)
	}
	if out.Plan.Action != planActionMigrateCredential {
		t.Errorf("plan.action = %q, want migrate_credentials", out.Plan.Action)
	}
	if out.Plan.Automatic != "" {
		t.Errorf("plan.automatic = %q — any automatic sentence here is a reconstruction promise", out.Plan.Automatic)
	}
	if strings.Contains(strings.ToLower(out.Sync.Reason), "next pass") {
		t.Error("the reason promises a next-pass fix Sharko cannot deliver")
	}
}

// TestConnectionReconciliation_ComparisonEndpointUnchanged: the shared core
// gained unexported provenance joins — the OLD endpoint's wire shape must not
// have grown any new key.
func TestConnectionReconciliation_ComparisonEndpointUnchanged(t *testing.T) {
	live := expectedLiveSecretForFixture(t)
	live.Annotations = map[string]string{
		clusterreconciler.AnnotationRevision:  reconTestCommit,
		clusterreconciler.AnnotationWrittenAt: "2026-08-18T09:00:00Z",
	}
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	allowed := map[string]bool{
		"cluster": true, "status": true, "scope": true, "ownership_mode": true,
		"limit_reason": true, "failure_reason": true, "checked_at": true,
		"branch": true, "compared_commit": true, "compared_path": true,
		"credential_source_type": true, "differences": true, "not_checked": true,
		"checked_field_count": true, "repair_available": true, "repair_scope": true,
		"values_never_returned": true,
	}
	for key := range raw {
		if !allowed[key] {
			t.Errorf("the comparison endpoint grew a new key %q — it must stay byte-for-byte unchanged", key)
		}
	}
	if _, ok := raw["applied_revision"]; ok {
		t.Error("the provenance join leaked onto the comparison endpoint")
	}
}

// --- F5: the EKS proof for the NEW endpoint ----------------------------------

// TestConnectionReconciliation_EKSPath_ZeroMintAndNoLeak drives the real EKS
// metadata path through handleGetConnectionReconciliation, across its
// branches (clean, and with label drift): the mint counter stays at ZERO and
// no form of the stored sentinel appears in the response or the logs. Same
// fixtures as the comparison endpoint's own EKS proof — the two endpoints
// share the read core, and this pins that the join layer did not open a new
// route to the mint.
func TestConnectionReconciliation_EKSPath_ZeroMintAndNoLeak(t *testing.T) {
	liveClean := liveConnectionSecret(map[string]string{"datadog": "enabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		// A live config carrying a token minted at some earlier moment — the
		// sentinel rides inside it, so the endpoint really has something to
		// leak.
		"config": `{"bearerToken":"` + eksMetadataAPISentinel + `","tlsClientConfig":{}}`,
	}, nil)
	liveLabelDrift := liveConnectionSecret(map[string]string{"datadog": "disabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"bearerToken":"` + eksMetadataAPISentinel + `","tlsClientConfig":{}}`,
	}, nil)

	cases := []struct {
		name      string
		live      *corev1.Secret
		wantState string
	}{
		{"clean — row 2, unknown/partial, repair offered by policy", liveClean, syncStateUnknown},
		{"label drift — out_of_sync, labels group only", liveLabelDrift, syncStateOutOfSync},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mint := &eksAPIMintCounter{}
			backend := eksBackendForAPI(t, mint)

			var logs strings.Builder
			restore := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(restore) })

			_, router, _ := comparisonFixture(t, eksManagedYAML, tc.live, backend)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, reconciliationReq(comparisonCluster))
			out := decodeRecon(t, w)

			// 1. THE MINT. Zero, not low — same bar as the comparison's own
			// EKS proof, inherited because both run the same read core.
			if mint.calls != 0 {
				t.Fatalf("the reconciliation view minted %d EKS sign-in token(s); it must mint ZERO — the read path must never reach GetCredentials", mint.calls)
			}

			// 2. The honest answer.
			if out.ManagementMode != managementModeSharkoManaged {
				t.Errorf("management_mode = %q, want sharko_managed", out.ManagementMode)
			}
			if out.Sync.State != tc.wantState {
				t.Errorf("sync.state = %q, want %q", out.Sync.State, tc.wantState)
			}
			if out.Sync.State == syncStateSynced {
				t.Fatal("an EKS cluster was reported synced; its credential content was never compared")
			}
			if out.Sync.VerificationScope != verificationScopePartial {
				t.Errorf("verification_scope = %q, want partial for EKS", out.Sync.VerificationScope)
			}
			sawConfigUnchecked := false
			for _, n := range out.Drift.NotChecked {
				if n.Path == "data.config" {
					sawConfigUnchecked = true
				}
			}
			if !sawConfigUnchecked {
				t.Errorf("data.config must be in drift.not_checked for EKS, got %+v", out.Drift.NotChecked)
			}

			// 3. No form of the sentinel anywhere.
			assertNoEKSAPISentinel(t, "reconciliation response body", w.Body.String())
			assertNoEKSAPILengthInJSON(t, "reconciliation response body", w.Body.Bytes())
			assertNoEKSAPISentinel(t, "log output", logs.String())
		})
	}
}

// ============================================================================
// B5 — ONE derivation. The endpoint is a projection of the canonical core,
// and the display words come from the server.
// ============================================================================

// TestConnectionReconciliation_SyncBlockIsExactlyTheCanonicalCore proves the
// endpoint derives NOTHING of its own about sync state. Across the same wide
// grid the invariant sweep uses, every canonical field on the response is
// field-for-field what connectionCanonicalStateFor produced — which is also
// exactly what the fleet row reads. Two surfaces, one answer, structurally.
//
// It also re-proves, by construction, the two independence claims the
// canonical core's doc comment makes: the same canonical answer comes back
// with the self-heal setting on and off, and with every ArgoCD health word.
func TestConnectionReconciliation_SyncBlockIsExactlyTheCanonicalCore(t *testing.T) {
	statuses := []connectioncompare.Status{
		connectioncompare.StatusSynced, connectioncompare.StatusOutOfSync,
		connectioncompare.StatusMissing, connectioncompare.StatusCheckFailed,
		connectioncompare.StatusOwnershipConflict, connectioncompare.StatusLimited,
		connectioncompare.Status(""),
	}
	scopes := []connectioncompare.Scope{
		connectioncompare.ScopeFull, connectioncompare.ScopeLimited,
		connectioncompare.ScopeAddonLabelsOnly, connectioncompare.ScopeOwnershipConflict,
	}
	modes := []connectioncompare.Mode{
		connectioncompare.ModeBackendStoredCredentials, connectioncompare.ModeEKSToken,
		connectioncompare.ModeInlineKubeconfig, connectioncompare.ModeSelfManaged,
		connectioncompare.ModeForeignOwned, connectioncompare.ModeUnknownSource,
		connectioncompare.Mode(""),
	}
	diffSets := [][]connectionComparisonDifference{
		nil,
		{reconSafeDiff("metadata.labels[datadog]")},
		{reconSafeDiff("data.server")},
		{reconSensitiveDiff()},
	}
	healths := []string{healthStateConnected, healthStateUnavailable, healthStateNotChecked}

	checked := 0
	for _, status := range statuses {
		for _, scope := range scopes {
			for _, mode := range modes {
				for _, diffs := range diffSets {
					for _, notChecked := range [][]connectionComparisonNotChecked{nil, {{Path: "data.config", Reason: "r"}}} {
						v := reconView(func(cv *connectionComparisonView) {
							cv.Status = string(status)
							cv.Scope = string(scope)
							cv.OwnershipMode = string(mode)
							cv.Differences = diffs
							cv.NotChecked = notChecked
							cv.LimitReason = "a limit sentence"
							cv.FailureReason = "a failure sentence"
						})
						canon := connectionCanonicalStateFor(v)
						for _, selfHeal := range []bool{false, true} {
							for _, health := range healths {
								out := buildConnectionReconciliationView(connectionReconciliationFacts{
									view: v, healthState: health, selfHealOn: selfHeal,
								})
								checked++
								if out.ManagementMode != canon.ManagementMode ||
									out.ManagedScope != canon.ManagedScope ||
									out.Sync.State != canon.SyncState ||
									out.Sync.VerificationScope != canon.VerificationScope ||
									out.Sync.ApprovalRequired != canon.ApprovalRequired ||
									out.Sync.Reason != canon.Reason ||
									out.Sync.Headline != canon.Headline ||
									out.Sync.Qualifier != canon.Qualifier {
									t.Fatalf("the endpoint derived its own answer instead of the canonical one\n endpoint: mode=%q scope=%q state=%q verification=%q approval=%v reason=%q headline=%q qualifier=%q\ncanonical: mode=%q scope=%q state=%q verification=%q approval=%v reason=%q headline=%q qualifier=%q\n(status=%q compareScope=%q ownership=%q selfHeal=%v health=%q)",
										out.ManagementMode, out.ManagedScope, out.Sync.State, out.Sync.VerificationScope,
										out.Sync.ApprovalRequired, out.Sync.Reason, out.Sync.Headline, out.Sync.Qualifier,
										canon.ManagementMode, canon.ManagedScope, canon.SyncState, canon.VerificationScope,
										canon.ApprovalRequired, canon.Reason, canon.Headline, canon.Qualifier,
										status, scope, mode, selfHeal, health)
								}
							}
						}
					}
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("the grid shrank to %d combinations — this projection proof lost coverage", checked)
	}
}

// TestConnectionReconciliation_HeadlineWordsExact pins every display word by
// EXACT equality against a literal written here. The project lesson is that a
// wrong sentence survived four review rounds behind a != "" assertion, so
// these are character-for-character, and the words the product owner banned
// are proven unproducible.
func TestConnectionReconciliation_HeadlineWordsExact(t *testing.T) {
	cases := []struct {
		name         string
		state        connectionCanonicalState
		wantHeadline string
		wantQualif   string
	}{
		{
			name:         "sharko-managed clean",
			state:        connectionCanonicalState{ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection, SyncState: syncStateSynced, VerificationScope: verificationScopeFull, CheckedAt: "t"},
			wantHeadline: "Connection synced",
		},
		{
			name:         "self-managed clean — never bare Synced, never Connection synced",
			state:        connectionCanonicalState{ManagementMode: managementModeSelfManaged, ManagedScope: managedScopeAddonLabels, SyncState: syncStateSynced, VerificationScope: verificationScopeFull, CheckedAt: "t"},
			wantHeadline: "Addon labels synced",
			wantQualif:   "Connection data managed outside Sharko",
		},
		{
			name:         "legacy inline clean",
			state:        connectionCanonicalState{ManagementMode: managementModeLegacyInline, ManagedScope: managedScopeAddonLabels, SyncState: syncStateUnknown, VerificationScope: verificationScopePartial, CheckedAt: "t"},
			wantHeadline: "Verification incomplete",
			wantQualif:   qualifierLegacyInline,
		},
		{
			name:         "foreign owned",
			state:        connectionCanonicalState{ManagementMode: managementModeForeignOwned, ManagedScope: managedScopeNone, SyncState: syncStateBlocked, VerificationScope: verificationScopeNone, CheckedAt: "t"},
			wantHeadline: "Blocked",
		},
		{
			name:         "out of sync needing approval",
			state:        connectionCanonicalState{ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection, SyncState: syncStateOutOfSync, VerificationScope: verificationScopeFull, ApprovalRequired: true, CheckedAt: "t"},
			wantHeadline: "Out of sync — approval required",
		},
		{
			name:         "self-managed labels drifted",
			state:        connectionCanonicalState{ManagementMode: managementModeSelfManaged, ManagedScope: managedScopeAddonLabels, SyncState: syncStateOutOfSync, VerificationScope: verificationScopeFull, CheckedAt: "t"},
			wantHeadline: "Addon labels out of sync",
			wantQualif:   "Connection data managed outside Sharko",
		},
		{
			name:         "legacy inline, live Secret gone",
			state:        connectionCanonicalState{ManagementMode: managementModeLegacyInline, ManagedScope: managedScopeAddonLabels, SyncState: syncStateOutOfSync, VerificationScope: verificationScopeNone, CheckedAt: "t", LiveSecretMissing: true},
			wantHeadline: "Out of sync — cannot be restored from Git",
			wantQualif:   qualifierLegacyInline,
		},
		{
			name:         "EKS clean — partial, and the qualifier is not said twice",
			state:        connectionCanonicalState{ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection, SyncState: syncStateUnknown, VerificationScope: verificationScopePartial, CheckedAt: "t"},
			wantHeadline: "Configuration matches; credential content not compared",
		},
		{
			name:         "check failed",
			state:        connectionCanonicalState{ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection, SyncState: syncStateUnknown, VerificationScope: verificationScopeNone, CheckedAt: "t"},
			wantHeadline: "Unknown — check failed",
		},
		{
			name:         "never checked",
			state:        connectionCanonicalState{ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection, SyncState: syncStateUnknown, VerificationScope: verificationScopeNone},
			wantHeadline: "Not checked yet",
		},
		{
			name:         "sharko-managed out of sync, labels only",
			state:        connectionCanonicalState{ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection, SyncState: syncStateOutOfSync, VerificationScope: verificationScopeFull, CheckedAt: "t"},
			wantHeadline: "Out of sync",
		},
		{
			name:         "sharko-managed partial, out of sync — the credential qualifier rides",
			state:        connectionCanonicalState{ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection, SyncState: syncStateOutOfSync, VerificationScope: verificationScopePartial, ApprovalRequired: true, CheckedAt: "t"},
			wantHeadline: "Out of sync — approval required",
			wantQualif:   "Credential content not compared",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectionSyncHeadline(tc.state); got != tc.wantHeadline {
				t.Errorf("headline = %q, want %q", got, tc.wantHeadline)
			}
			if got := connectionSyncQualifier(tc.state); got != tc.wantQualif {
				t.Errorf("qualifier = %q, want %q", got, tc.wantQualif)
			}
		})
	}
}

// TestConnectionReconciliation_BareSyncedIsUnproducible drives the whole
// matrix grid and proves the banned words can never come out of the shared
// derivation: bare "Synced" for anything, and "Connection synced" for a
// connection whose data Sharko does not manage.
func TestConnectionReconciliation_BareSyncedIsUnproducible(t *testing.T) {
	modes := []connectioncompare.Mode{
		connectioncompare.ModeBackendStoredCredentials, connectioncompare.ModeEKSToken,
		connectioncompare.ModeInlineKubeconfig, connectioncompare.ModeSelfManaged,
		connectioncompare.ModeAdopted, connectioncompare.ModeForeignOwned,
		connectioncompare.ModeUnknownSource, connectioncompare.Mode(""),
	}
	statuses := []connectioncompare.Status{
		connectioncompare.StatusSynced, connectioncompare.StatusLimited,
		connectioncompare.StatusOutOfSync, connectioncompare.StatusMissing,
		connectioncompare.StatusCheckFailed, connectioncompare.StatusOwnershipConflict,
	}
	scopes := []connectioncompare.Scope{
		connectioncompare.ScopeFull, connectioncompare.ScopeLimited, connectioncompare.ScopeAddonLabelsOnly,
	}
	for _, mode := range modes {
		for _, status := range statuses {
			for _, scope := range scopes {
				canon := connectionCanonicalStateFor(reconView(func(v *connectionComparisonView) {
					v.OwnershipMode = string(mode)
					v.Status = string(status)
					v.Scope = string(scope)
					v.LimitReason = "a limit sentence"
					v.FailureReason = "a failure sentence"
				}))
				if canon.Headline == "Synced" {
					t.Fatalf("bare \"Synced\" rendered for mode=%q status=%q scope=%q", mode, status, scope)
				}
				if canon.ManagementMode != managementModeSharkoManaged && canon.Headline == headlineConnectionSynced {
					t.Fatalf("%q claimed \"Connection synced\" for mode=%q status=%q scope=%q", canon.ManagementMode, mode, status, scope)
				}
				if canon.SyncState == syncStateSynced && canon.VerificationScope != verificationScopeFull {
					t.Fatalf("INVARIANT VIOLATED in the canonical core: synced at %q (mode=%q status=%q scope=%q)", canon.VerificationScope, mode, status, scope)
				}
			}
		}
	}
}

// TestConnectionReconciliation_ResponseKeySet pins the endpoint's wire shape.
// Story 1's fields must all still be there, byte-for-byte the same names, and
// the corrective round adds exactly two: sync.headline and sync.qualifier.
func TestConnectionReconciliation_ResponseKeySet(t *testing.T) {
	live := expectedLiveSecretForFixture(t)
	live.Data["config"] = []byte("a-rotated-live-config-that-differs")
	live.Annotations = map[string]string{
		clusterreconciler.AnnotationRevision:  reconTestCommit,
		clusterreconciler.AnnotationWrittenAt: "2026-08-18T09:00:00Z",
	}
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	wantTop := []string{
		"cluster", "management_mode", "managed_scope", "mode_statement",
		"definition", "sync", "health", "conditions", "drift", "plan",
		"values_never_returned",
	}
	assertExactKeys(t, "response", raw, wantTop)

	var sync map[string]json.RawMessage
	if err := json.Unmarshal(raw["sync"], &sync); err != nil {
		t.Fatalf("decoding sync: %v", err)
	}
	// checked_at and last_successful_application are present here because
	// this fixture ran a real check against a stamped Secret; qualifier is
	// absent on this row (full scope, sharko-managed) and that is the
	// omitempty contract.
	assertExactKeys(t, "sync", sync, []string{
		"state", "verification_scope", "approval_required", "reason",
		"checked_at", "last_successful_application", "headline",
	})
}

func assertExactKeys(t *testing.T, what string, got map[string]json.RawMessage, want []string) {
	t.Helper()
	allowed := make(map[string]bool, len(want))
	for _, k := range want {
		allowed[k] = true
	}
	for k := range got {
		if !allowed[k] {
			t.Errorf("%s grew an unexpected key %q", what, k)
		}
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("%s lost the key %q", what, k)
		}
	}
}

// ============================================================================
// Ruling (a) — matrix row 14 was wrong, the running reconciler is right.
// ============================================================================

// TestConnectionReconciliation_SelfManagedLabelDrift_IsAutomaticNeverAButton
// pins ruling (a) at the builder: a self-managed connection whose addon
// labels drifted converges by itself, so the plan says so and Sharko offers
// NO manual door for work its own reconciler performs every tick.
func TestConnectionReconciliation_SelfManagedLabelDrift_IsAutomaticNeverAButton(t *testing.T) {
	for _, selfHeal := range []bool{false, true} {
		out := buildRecon(reconView(func(v *connectionComparisonView) {
			v.Status = string(connectioncompare.StatusOutOfSync)
			v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
			v.OwnershipMode = string(connectioncompare.ModeSelfManaged)
			v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
			v.Differences = []connectionComparisonDifference{reconSafeDiff("metadata.labels[datadog]")}
		}), selfHeal)

		if out.Plan.Action != planActionNone {
			t.Errorf("selfHeal=%v: plan.action = %q — ruling (a) forbids offering a manual action the reconciler performs itself", selfHeal, out.Plan.Action)
		}
		if out.Plan.Automatic != planAutomaticLabelSync {
			t.Errorf("selfHeal=%v: plan.automatic = %q, want the next-pass label sentence", selfHeal, out.Plan.Automatic)
		}
		if out.Sync.ApprovalRequired {
			t.Errorf("selfHeal=%v: approval_required must stay false — no connection detail or credential is involved", selfHeal)
		}
		if out.Sync.Headline != headlineAddonLabelsOutOfSync {
			t.Errorf("selfHeal=%v: headline = %q, want %q", selfHeal, out.Sync.Headline, headlineAddonLabelsOutOfSync)
		}
		if len(out.Plan.ActionScopes) != 0 {
			t.Errorf("selfHeal=%v: action_scopes = %v on an action-free plan", selfHeal, out.Plan.ActionScopes)
		}
	}
}

// TestConnectionReconciliation_SelfManagedLabelDrift_HealsOnTheNextPass is the
// transition ruling (a) asked for, end to end against a REAL reconciler: a
// self-managed connection with drifted addon labels reports "Addon labels out
// of sync" and promises nothing but the next pass; one reconciler pass later
// the same connection reports "Addon labels synced". The promise the plan
// makes is the behaviour the reconciler actually has.
func TestConnectionReconciliation_SelfManagedLabelDrift_HealsOnTheNextPass(t *testing.T) {
	selfYAML := "clusters:\n- name: " + comparisonCluster + "\n  connectionManagedBy: user\n  credsSource: secret-kubeconfig\n  labels:\n    datadog: enabled\n"
	// The user's own Secret: no managed-by label, and the addon label the
	// git record declares is wrong. Real drift, in the one scope Sharko owns
	// on a self-managed connection.
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      comparisonCluster,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				"datadog":                        "disabled",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(comparisonCluster),
			"server": []byte("https://their-own-address.invalid"),
			"config": []byte(`{"bearerToken":"theirs"}`),
		},
	}
	srv, router, argoClient := comparisonFixture(t, selfYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	before := decodeRecon(t, w)
	if before.ManagementMode != managementModeSelfManaged {
		t.Fatalf("management_mode = %q, want self_managed", before.ManagementMode)
	}
	if before.Sync.State != syncStateOutOfSync {
		t.Fatalf("sync.state = %q, want out_of_sync (drift: %+v)", before.Sync.State, before.Drift)
	}
	if before.Sync.Headline != headlineAddonLabelsOutOfSync {
		t.Errorf("headline = %q, want %q", before.Sync.Headline, headlineAddonLabelsOutOfSync)
	}
	if before.Plan.Action != planActionNone {
		t.Errorf("plan.action = %q — ruling (a): no manual door for work the reconciler does itself", before.Plan.Action)
	}
	if before.Plan.Automatic != planAutomaticLabelSync {
		t.Errorf("plan.automatic = %q, want the next-pass label sentence", before.Plan.Automatic)
	}

	// One real reconciler pass — the same syncSelfManaged the 30-second tick
	// runs, on the same fake ArgoCD the comparison reads.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.clusterRecon.Start(ctx)
	defer srv.clusterRecon.Stop()
	srv.clusterRecon.Trigger()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := argoClient.CoreV1().Secrets("argocd").Get(ctx, comparisonCluster, metav1.GetOptions{})
		if err == nil && s.Labels["datadog"] == "enabled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	after, err := argoClient.CoreV1().Secrets("argocd").Get(ctx, comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Secret back: %v", err)
	}
	if after.Labels["datadog"] != "enabled" {
		t.Fatalf("the reconciler did not re-apply the addon label by itself (datadog=%q) — ruling (a)'s premise does not hold and the plan's promise would be a lie", after.Labels["datadog"])
	}
	// The user's own connection details are still theirs, untouched.
	if got := string(after.Data["server"]); got != "https://their-own-address.invalid" {
		t.Errorf("the reconciler rewrote the user's own API address to %q", got)
	}
	if got := string(after.Data["config"]); got != `{"bearerToken":"theirs"}` {
		t.Error("the reconciler rewrote the user's own credential blob")
	}

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, reconciliationReq(comparisonCluster))
	healed := decodeRecon(t, w2)
	if healed.Sync.State != syncStateSynced {
		t.Fatalf("after the pass sync.state = %q, want synced (drift: %+v)", healed.Sync.State, healed.Drift)
	}
	if healed.Sync.VerificationScope != verificationScopeFull {
		t.Errorf("verification_scope = %q, want full of the owned addon-label scope", healed.Sync.VerificationScope)
	}
	if healed.Sync.Headline != headlineAddonLabelsSynced {
		t.Errorf("headline = %q, want %q — never bare Synced, never Connection synced", healed.Sync.Headline, headlineAddonLabelsSynced)
	}
	if healed.Plan.Action != planActionNone || healed.Plan.Automatic != "" {
		t.Errorf("a healed connection still promises something: action=%q automatic=%q", healed.Plan.Action, healed.Plan.Automatic)
	}
}
