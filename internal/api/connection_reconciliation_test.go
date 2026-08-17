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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/settings"
	"k8s.io/client-go/kubernetes/fake"
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

	checked := 0
	for _, status := range statuses {
		for _, scope := range scopes {
			for _, mode := range modes {
				for _, source := range sources {
					for _, diffs := range diffSets {
						for _, nc := range notCheckedSets {
							for _, rep := range repairs {
								for _, path := range paths {
									for _, commit := range commits {
										for _, selfHeal := range []bool{false, true} {
											v := reconView(func(cv *connectionComparisonView) {
												cv.Status = string(status)
												cv.Scope = string(scope)
												cv.OwnershipMode = string(mode)
												cv.CredentialSourceType = source
												cv.Differences = diffs
												cv.NotChecked = nc
												cv.RepairAvailable = rep.available
												cv.RepairScope = string(rep.scope)
												cv.ComparedPath = path
												cv.ComparedCommit = commit
												cv.LimitReason = "a limit sentence"
												cv.FailureReason = "a failure sentence"
											})
											out := buildRecon(v, selfHeal)
											checked++
											if out.Sync.State == syncStateSynced && out.Sync.VerificationScope != verificationScopeFull {
												t.Fatalf("INVARIANT VIOLATED: sync.state=synced with verification_scope=%q for status=%q scope=%q mode=%q source=%q diffs=%d",
													out.Sync.VerificationScope, status, scope, mode, source, len(diffs))
											}
											// A reported difference must never wear the synced word,
											// whatever the input status claimed.
											if out.Sync.State == syncStateSynced && len(v.Differences) > 0 {
												t.Fatalf("synced with %d reported differences (status=%q scope=%q mode=%q)",
													len(v.Differences), status, scope, mode)
											}
											// Blocked is deterministic: foreign ownership only.
											if out.Sync.State == syncStateBlocked && connectioncompare.Status(v.Status) != connectioncompare.StatusOwnershipConflict {
												t.Fatalf("blocked without an ownership conflict (status=%q)", v.Status)
											}
											// legacy_inline never gets repair_connection; foreign never
											// gets anything but take_over (dispatch hard rules).
											if out.ManagementMode == managementModeLegacyInline && out.Plan.Action == planActionRepairConnection {
												t.Fatalf("legacy_inline offered repair_connection (status=%q scope=%q)", v.Status, v.Scope)
											}
											if out.ManagementMode == managementModeForeignOwned && out.Plan.Action != planActionTakeOver {
												t.Fatalf("foreign_owned offered %q, only take_over is allowed", out.Plan.Action)
											}
											// approval_required is true exactly when drift touches
											// connection configuration or credential material.
											wantApproval := len(out.Drift.ConnectionConfiguration) > 0 || len(out.Drift.CredentialMaterial) > 0
											if out.Sync.ApprovalRequired != wantApproval {
												t.Fatalf("approval_required=%v, want %v (config=%d cred=%d)",
													out.Sync.ApprovalRequired, wantApproval,
													len(out.Drift.ConnectionConfiguration), len(out.Drift.CredentialMaterial))
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
			row: "14: self-managed, labels drifted",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				v.OwnershipMode = string(connectioncompare.ModeSelfManaged)
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("metadata.labels[datadog]")}
			}),
			want: want{state: syncStateOutOfSync, verification: verificationScopeFull, managedScope: managedScopeAddonLabels, action: planActionSyncAddonLabels},
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
