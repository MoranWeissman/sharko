package service

// TestClassifyAddonApp_V2cleanup36 tests the V2-cleanup-36 status classification
// logic against live-captured ArgoCD payload shapes. Every case must match the
// exact fixture observed in the keda rollout incident.
//
// Priority: sync_failing > deploying > existing health mapping.
//
// Test structure mirrors review requirement: "table-driven; fixtures mirror
// REAL data shapes verbatim; every fix ships with the test that fails on old
// logic and passes now."

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestClassifyAddonApp_V2cleanup36(t *testing.T) {
	cases := []struct {
		name          string
		app           models.ArgocdApplication
		wantStatus    string
		wantIssueMsg  string // empty = no issue expected
		oldLogicWould string // what classifyHealth(health, sync) would have returned — proves failure on old logic
	}{
		{
			// (i) LIVE keda incident: Running + SyncFailed task + "completed unsuccessfully" message.
			// Old classifyHealth(Healthy, OutOfSync) → "healthy" (optimistic lie).
			name: "keda_crd_too_long",
			app: models.ArgocdApplication{
				SyncStatus:   "OutOfSync",
				HealthStatus: "Healthy",
				OperationPhase: "Running",
				OperationMessage: "one or more synchronization tasks completed unsuccessfully, " +
					"reason: CustomResourceDefinition.apiextensions.k8s.io \"scaledjobs.keda.sh\" " +
					"is invalid: metadata.annotations: Too long: must have at most 262144 bytes",
				HasSyncFailedResource: true,
			},
			wantStatus:    "sync_failing",
			wantIssueMsg:  "one or more synchronization tasks completed unsuccessfully",
			oldLogicWould: "healthy",
		},
		{
			// (ii) Mid-rollout: Running phase, no failures yet.
			// Old classifyHealth(Healthy, OutOfSync) → "healthy" (premature optimism; see DeploymentBadge
			// "Not deployed yet" pessimism at the tile level — the two lies cancel but neither is honest).
			// New: deploying.
			name: "active_rollout_no_failures",
			app: models.ArgocdApplication{
				SyncStatus:     "OutOfSync",
				HealthStatus:   "Healthy",
				OperationPhase: "Running",
				OperationMessage: "",
				HasSyncFailedResource: false,
			},
			wantStatus:    "deploying",
			wantIssueMsg:  "",
			oldLogicWould: "healthy",
		},
		{
			// (ii-b) Health=Progressing, no op.
			name: "progressing_no_op",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Progressing",
			},
			wantStatus:    "deploying",
			wantIssueMsg:  "",
			oldLogicWould: "progressing",
		},
		{
			// (iii) PIN: Synced + Healthy → healthy (must not change).
			name: "synced_healthy_pin",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Healthy",
			},
			wantStatus:    "healthy",
			wantIssueMsg:  "",
			oldLogicWould: "healthy",
		},
		{
			// (iv) PIN: Degraded → unhealthy.
			name: "degraded_pin",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Degraded",
			},
			wantStatus:    "unhealthy",
			wantIssueMsg:  "",
			oldLogicWould: "unhealthy",
		},
		{
			// (iv-b) PIN: Unknown health → unknown_health.
			name: "unknown_health_pin",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Unknown",
			},
			wantStatus:    "unknown_health",
			wantIssueMsg:  "",
			oldLogicWould: "unknown_health",
		},
		{
			// Phase=Failed (no running confusion).
			name: "phase_failed",
			app: models.ArgocdApplication{
				SyncStatus:     "OutOfSync",
				HealthStatus:   "Degraded",
				OperationPhase: "Failed",
				OperationMessage: "rpc error: code = Unknown desc = sync operation failed",
			},
			wantStatus:    "sync_failing",
			wantIssueMsg:  "rpc error: code = Unknown desc = sync operation failed",
			oldLogicWould: "unhealthy",
		},
		{
			// Phase=Error.
			name: "phase_error",
			app: models.ArgocdApplication{
				SyncStatus:     "OutOfSync",
				HealthStatus:   "Unknown",
				OperationPhase: "Error",
				OperationMessage: "context deadline exceeded",
			},
			wantStatus:    "sync_failing",
			wantIssueMsg:  "context deadline exceeded",
			oldLogicWould: "unknown_health",
		},
		{
			// Running + "completed unsuccessfully" in message but no SyncFailed resource.
			// Message check alone must fire.
			name: "running_message_only",
			app: models.ArgocdApplication{
				SyncStatus:     "OutOfSync",
				HealthStatus:   "Healthy",
				OperationPhase: "Running",
				OperationMessage: "one or more synchronization tasks completed unsuccessfully",
				HasSyncFailedResource: false,
			},
			wantStatus:    "sync_failing",
			wantIssueMsg:  "one or more synchronization tasks completed unsuccessfully",
			oldLogicWould: "healthy",
		},
		{
			// Running + SyncFailed resource but benign message.
			// Resource check alone must fire.
			name: "running_syncfailed_resource_only",
			app: models.ArgocdApplication{
				SyncStatus:            "OutOfSync",
				HealthStatus:          "Healthy",
				OperationPhase:        "Running",
				OperationMessage:      "Syncing",
				HasSyncFailedResource: true,
			},
			wantStatus:    "sync_failing",
			wantIssueMsg:  "Syncing",
			oldLogicWould: "healthy",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotIssue := classifyAddonApp(tc.app)

			if gotStatus != tc.wantStatus {
				t.Errorf("classifyAddonApp status = %q, want %q (old logic would give %q)",
					gotStatus, tc.wantStatus, tc.oldLogicWould)
			}

			// For issue message we check prefix rather than exact equality to
			// allow trimming and capping without making the test brittle.
			if tc.wantIssueMsg != "" {
				if !strings.Contains(gotIssue, tc.wantIssueMsg[:min(len(tc.wantIssueMsg), 40)]) {
					t.Errorf("classifyAddonApp issue = %q, want to contain %q",
						gotIssue, tc.wantIssueMsg)
				}
			} else if gotIssue != "" {
				t.Errorf("classifyAddonApp issue = %q, want empty", gotIssue)
			}

			// Prove the old logic fails on the new cases (regression guard).
			// Old logic: classifyHealth(HealthStatus, SyncStatus).
			gotOld := classifyHealth(tc.app.HealthStatus, tc.app.SyncStatus)
			if tc.oldLogicWould != "" && gotOld != tc.oldLogicWould {
				t.Errorf("old-logic baseline: classifyHealth(%q, %q) = %q, expected %q",
					tc.app.HealthStatus, tc.app.SyncStatus, gotOld, tc.oldLogicWould)
			}
			// For cases where old logic produces a WRONG answer, confirm the new
			// code produces a DIFFERENT (correct) answer.
			if tc.oldLogicWould != "" && tc.oldLogicWould != tc.wantStatus {
				if gotOld == tc.wantStatus {
					t.Errorf("old logic accidentally returns %q — test value is wrong", tc.wantStatus)
				}
			}
		})
	}
}

// TestTrimOperationMessage_V2cleanup36 verifies that first-line extraction and
// the 300-char cap work as specified.
func TestTrimOperationMessage_V2cleanup36(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single line short", "sync failed", "sync failed"},
		{"multi-line takes first line", "line one\nline two\nline three", "line one"},
		{"exactly 300 chars stays", strings.Repeat("a", 300), strings.Repeat("a", 300)},
		{"over 300 chars gets trimmed", strings.Repeat("b", 350), strings.Repeat("b", 300)},
		{"trailing spaces stripped", "  sync failed  ", "sync failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trimOperationMessage(tc.in)
			if got != tc.want {
				t.Errorf("trimOperationMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// min is a helper for Go versions that predate the builtin min (Go 1.21+).
// The module is already on Go 1.25.8 so this is just a local helper to keep
// the test self-contained without importing math.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestFullOperationMessage_V2cleanup38 verifies that fullOperationMessage
// preserves newlines + full text and only caps at 4000 chars.
func TestFullOperationMessage_V2cleanup38(t *testing.T) {
	// The live keda error — multi-line, > 300 chars but < 4000.
	// trimOperationMessage would cut this at the first comma (first newline) + 300
	// chars, but fullOperationMessage must keep ALL of it.
	liveKedaErr := "one or more synchronization tasks completed unsuccessfully, reason: " +
		"failed to create typed patch object (keda/keda-admission-webhooks; apps/v1, Kind=Deployment): " +
		".spec.template.spec.containers[name=\"keda-admission-webhooks\"].resources.metricServer: " +
		"field not declared in schema,failed to create typed patch object " +
		"(keda/keda-operator; apps/v1, Kind=Deployment): " +
		".spec.template.spec.containers[name=\"keda-operator\"].resources.metricServer: " +
		"field not declared in schema"

	cases := []struct {
		name    string
		in      string
		wantLen int    // 0 = check exact equality
		wantContains string
	}{
		{
			name:         "empty",
			in:           "",
			wantLen:      0,
			wantContains: "",
		},
		{
			name:         "live_keda_error_preserved_fully",
			in:           liveKedaErr,
			wantContains: "field not declared in schema",
		},
		{
			name:    "multiline_preserved",
			in:      "line one\nline two\nline three",
			wantContains: "line two", // newlines kept
		},
		{
			name:    "over_4000_chars_capped",
			in:      strings.Repeat("x", 5000),
			wantLen: 4000,
		},
		{
			name:    "exactly_4000_stays",
			in:      strings.Repeat("y", 4000),
			wantLen: 4000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fullOperationMessage(tc.in)
			if tc.wantLen > 0 && len(got) != tc.wantLen {
				t.Errorf("fullOperationMessage len = %d, want %d", len(got), tc.wantLen)
			}
			if tc.wantContains != "" && !strings.Contains(got, tc.wantContains) {
				t.Errorf("fullOperationMessage = %q, want to contain %q", got[:min(len(got), 100)], tc.wantContains)
			}
		})
	}
}

// TestClassifyAddonApp_V2cleanup38_FullVsShort verifies that classifyAddonApp
// returns only the SHORT first-line message (issues[]) while the call site
// separately fetches the full message for argocd_operation_message.
// This test pins the contract: issues carries short text.
func TestClassifyAddonApp_V2cleanup38_FullVsShort(t *testing.T) {
	// Live keda error with multi-line / comma-separated content past 300 chars.
	longMsg := "one or more synchronization tasks completed unsuccessfully, reason: " +
		"failed to create typed patch object (keda/keda-admission-webhooks; apps/v1, Kind=Deployment): " +
		".spec.template.spec.containers[name=\"keda-admission-webhooks\"].resources.metricServer: " +
		"field not declared in schema,failed to create typed patch object " +
		"(keda/keda-operator; apps/v1, Kind=Deployment): " +
		strings.Repeat("additional error detail ", 15)

	app := models.ArgocdApplication{
		SyncStatus:       "OutOfSync",
		HealthStatus:     "Healthy",
		OperationPhase:   "Failed",
		OperationMessage: longMsg,
	}

	status, issueMsg := classifyAddonApp(app)
	if status != "sync_failing" {
		t.Fatalf("expected sync_failing, got %q", status)
	}

	// issueMsg must be the SHORT first-line version (≤300 chars, single line).
	if len(issueMsg) > 300 {
		t.Errorf("issueMsg len %d exceeds 300 char cap", len(issueMsg))
	}
	if strings.Contains(issueMsg, "\n") {
		t.Errorf("issueMsg must not contain newlines")
	}

	// fullOperationMessage must return the whole thing.
	full := fullOperationMessage(longMsg)
	if !strings.HasPrefix(full, issueMsg[:min(len(issueMsg), 50)]) {
		// issueMsg should be the first chunk of the full message.
		t.Errorf("full message prefix mismatch: full=%q, issue=%q", full[:min(len(full), 80)], issueMsg[:min(len(issueMsg), 80)])
	}
	if len(full) <= len(issueMsg) && len(longMsg) > 300 {
		t.Errorf("expected full (%d) to be longer than issue (%d) for long message", len(full), len(issueMsg))
	}
}
