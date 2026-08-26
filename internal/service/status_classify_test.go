package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/models"
)

// TestDeployingNotCountedAsIssue_V2cleanup45 verifies the fix for the With-Issues
// count / With-Issues filter mismatch: a "deploying" addon must NOT increment
// TotalWithIssues (the frontend filter excludes "deploying" from the issue set).
//
// The test drives GetClusterComparison with a stub ArgoCD server that returns
// three apps for the test cluster:
//
//   - keda (Running phase, no failure) → classifyAddonApp → "deploying"
//   - velero (Synced + Healthy)        → classifyAddonApp → "healthy"
//   - cert-manager (Failed phase)      → classifyAddonApp → "sync_failing"
//
// Expected: TotalHealthy == 1, TotalWithIssues == 1, and keda appears in
// AddonComparisons with Status=="deploying" but is NOT counted in either bucket.
func TestDeployingNotCountedAsIssue_V2cleanup45(t *testing.T) {
	const clusterName = "test-cluster"

	// managedClustersYAML has a single cluster whose labels enable the three
	// addons we're testing. The enveloped format is what the real service writes.
	managedClustersYAML := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: test-cluster
      region: us-east-1
      labels:
        keda: enabled
        velero: enabled
        cert-manager: enabled
`

	// addonsCatalogYAML declares the three addons.
	addonsCatalogYAML := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: keda
      repoURL: https://kedacore.github.io/charts
      chart: keda
      version: "2.13.0"
      namespace: keda
    - name: velero
      repoURL: https://vmware-tanzu.github.io/helm-charts
      chart: velero
      version: "7.2.1"
      namespace: velero
    - name: cert-manager
      repoURL: https://charts.jetstack.io
      chart: cert-manager
      version: "1.14.4"
      namespace: cert-manager
`

	// Stub ArgoCD server: returns a matching cluster + the three apps.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/clusters":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					{
						"name":   clusterName,
						"server": "https://test-cluster.example.com",
					},
				},
			})

		case r.URL.Path == "/api/v1/applications":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					// keda: Running phase, no failure → "deploying"
					{
						"metadata": map[string]interface{}{
							"name": "keda-" + clusterName,
						},
						"spec": map[string]interface{}{
							"destination": map[string]interface{}{
								"server": "https://test-cluster.example.com",
							},
						},
						"status": map[string]interface{}{
							"sync":   map[string]interface{}{"status": "OutOfSync"},
							"health": map[string]interface{}{"status": "Healthy"},
							"operationState": map[string]interface{}{
								"phase":   "Running",
								"message": "Syncing resources",
							},
						},
					},
					// velero: Synced + Healthy → "healthy"
					{
						"metadata": map[string]interface{}{
							"name": "velero-" + clusterName,
						},
						"spec": map[string]interface{}{
							"destination": map[string]interface{}{
								"server": "https://test-cluster.example.com",
							},
						},
						"status": map[string]interface{}{
							"sync":   map[string]interface{}{"status": "Synced"},
							"health": map[string]interface{}{"status": "Healthy"},
						},
					},
					// cert-manager: Failed phase → "sync_failing"
					{
						"metadata": map[string]interface{}{
							"name": "cert-manager-" + clusterName,
						},
						"spec": map[string]interface{}{
							"destination": map[string]interface{}{
								"server": "https://test-cluster.example.com",
							},
						},
						"status": map[string]interface{}{
							"sync":   map[string]interface{}{"status": "OutOfSync"},
							"health": map[string]interface{}{"status": "Degraded"},
							"operationState": map[string]interface{}{
								"phase":   "Failed",
								"message": "rpc error: sync failed",
							},
						},
					},
				},
			})

		default:
			// Return empty for anything else (connection info, etc.)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	svc := NewClusterService("")

	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte(managedClustersYAML),
			"configuration/addons-catalog.yaml":   []byte(addonsCatalogYAML),
		},
	}

	resp, err := svc.GetClusterComparison(context.Background(), clusterName, gp, ac)
	if err != nil {
		t.Fatalf("GetClusterComparison returned unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("GetClusterComparison returned nil response")
	}

	// Core assertion: deploying must NOT count as an issue.
	if resp.TotalWithIssues != 1 {
		t.Errorf("TotalWithIssues = %d, want 1 (only cert-manager is an issue; keda is deploying)", resp.TotalWithIssues)
	}
	if resp.TotalHealthy != 1 {
		t.Errorf("TotalHealthy = %d, want 1 (only velero is healthy)", resp.TotalHealthy)
	}

	// Verify keda itself surfaces with the "deploying" status in comparisons.
	var kedaStatus string
	for _, comp := range resp.AddonComparisons {
		if comp.AddonName == "keda-"+clusterName || comp.AddonName == "keda" {
			kedaStatus = comp.Status
		}
	}
	if kedaStatus != "deploying" {
		t.Errorf("keda status = %q, want %q", kedaStatus, "deploying")
	}
}

// TestClassifyAddonApp_V2cleanup36 tests the V2-cleanup-36 status classification
// logic against live-captured ArgoCD payload shapes. Every case must match the
// exact fixture observed in the keda rollout incident.
//
// Priority: sync_failing > deploying > existing health mapping.
//
// Test structure mirrors review requirement: "table-driven; fixtures mirror
// REAL data shapes verbatim; every fix ships with the test that fails on old
// logic and passes now."
func TestClassifyAddonApp_V2cleanup36(t *testing.T) {
	cases := []struct {
		name       string
		app        models.ArgocdApplication
		wantStatus string
		// wantSyncFailing replaces the old wantIssueMsg field (B8).
		//
		// The old field asserted that ArgoCD's own operationState.message came
		// BACK OUT of classifyAddonApp, and the call site put that straight
		// into issues[] on a 200 response. That assertion was the leak's
		// keeper: any fix had to break it. It is inverted, not deleted — the
		// classification still has to fire on exactly the same inputs, and the
		// message must no longer come out. The app fixture above still carries
		// the real ArgoCD text, so the absence check below has something real
		// to be absent.
		wantSyncFailing bool
		oldLogicWould   string // what classifyHealth(health, sync) would have returned — proves failure on old logic
	}{
		{
			// (i) LIVE keda incident: Running + SyncFailed task + "completed unsuccessfully" message.
			// Old classifyHealth(Healthy, OutOfSync) → "healthy" (optimistic lie).
			name: "keda_crd_too_long",
			app: models.ArgocdApplication{
				SyncStatus:     "OutOfSync",
				HealthStatus:   "Healthy",
				OperationPhase: "Running",
				OperationMessage: "one or more synchronization tasks completed unsuccessfully, " +
					"reason: CustomResourceDefinition.apiextensions.k8s.io \"scaledjobs.keda.sh\" " +
					"is invalid: metadata.annotations: Too long: must have at most 262144 bytes",
				HasSyncFailedResource: true,
			},
			wantStatus:      "sync_failing",
			wantSyncFailing: true,
			oldLogicWould:   "healthy",
		},
		{
			// (ii) Mid-rollout: Running phase, no failures yet.
			// Old classifyHealth(Healthy, OutOfSync) → "healthy" (premature optimism; see DeploymentBadge
			// "Not deployed yet" pessimism at the tile level — the two lies cancel but neither is honest).
			// New: deploying.
			name: "active_rollout_no_failures",
			app: models.ArgocdApplication{
				SyncStatus:            "OutOfSync",
				HealthStatus:          "Healthy",
				OperationPhase:        "Running",
				OperationMessage:      "",
				HasSyncFailedResource: false,
			},
			wantStatus:      "deploying",
			wantSyncFailing: false,
			oldLogicWould:   "healthy",
		},
		{
			// (ii-b) Health=Progressing, no op.
			name: "progressing_no_op",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Progressing",
			},
			wantStatus:      "deploying",
			wantSyncFailing: false,
			oldLogicWould:   "progressing",
		},
		{
			// (iii) PIN: Synced + Healthy → healthy (must not change).
			name: "synced_healthy_pin",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Healthy",
			},
			wantStatus:      "healthy",
			wantSyncFailing: false,
			oldLogicWould:   "healthy",
		},
		{
			// (iv) PIN: Degraded → unhealthy.
			name: "degraded_pin",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Degraded",
			},
			wantStatus:      "unhealthy",
			wantSyncFailing: false,
			oldLogicWould:   "unhealthy",
		},
		{
			// (iv-b) PIN: Unknown health → unknown_health.
			name: "unknown_health_pin",
			app: models.ArgocdApplication{
				SyncStatus:   "Synced",
				HealthStatus: "Unknown",
			},
			wantStatus:      "unknown_health",
			wantSyncFailing: false,
			oldLogicWould:   "unknown_health",
		},
		{
			// Phase=Failed (no running confusion).
			name: "phase_failed",
			app: models.ArgocdApplication{
				SyncStatus:       "OutOfSync",
				HealthStatus:     "Degraded",
				OperationPhase:   "Failed",
				OperationMessage: "rpc error: code = Unknown desc = sync operation failed",
			},
			wantStatus:      "sync_failing",
			wantSyncFailing: true,
			oldLogicWould:   "unhealthy",
		},
		{
			// Phase=Error.
			name: "phase_error",
			app: models.ArgocdApplication{
				SyncStatus:       "OutOfSync",
				HealthStatus:     "Unknown",
				OperationPhase:   "Error",
				OperationMessage: "context deadline exceeded",
			},
			wantStatus:      "sync_failing",
			wantSyncFailing: true,
			oldLogicWould:   "unknown_health",
		},
		{
			// Running + "completed unsuccessfully" in message but no SyncFailed resource.
			// Message check alone must fire.
			name: "running_message_only",
			app: models.ArgocdApplication{
				SyncStatus:            "OutOfSync",
				HealthStatus:          "Healthy",
				OperationPhase:        "Running",
				OperationMessage:      "one or more synchronization tasks completed unsuccessfully",
				HasSyncFailedResource: false,
			},
			wantStatus:      "sync_failing",
			wantSyncFailing: true,
			oldLogicWould:   "healthy",
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
			wantStatus:      "sync_failing",
			wantSyncFailing: true,
			oldLogicWould:   "healthy",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotSyncFailing := classifyAddonApp(tc.app)

			if gotStatus != tc.wantStatus {
				t.Errorf("classifyAddonApp status = %q, want %q (old logic would give %q)",
					gotStatus, tc.wantStatus, tc.oldLogicWould)
			}

			if gotSyncFailing != tc.wantSyncFailing {
				t.Errorf("classifyAddonApp syncFailing = %v, want %v", gotSyncFailing, tc.wantSyncFailing)
			}

			// The inversion (B8): whatever comes back out of the whole path,
			// none of ArgoCD's own message may be in it. safeAddonFailure is
			// what the call site now puts on the response, so that is what is
			// swept — not the bool, which cannot carry text by construction.
			if tc.app.OperationMessage != "" {
				detail := safeAddonFailure(tc.app)
				for _, chunk := range messageChunks(tc.app.OperationMessage) {
					if strings.Contains(detail, chunk) {
						t.Errorf("argocd_operation_message would carry ArgoCD's own words (%q).\n\nthe detail was:\n%s", chunk, detail)
					}
				}
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

// min is a helper for keeping the assertions below self-contained.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// messageChunks cuts an ArgoCD operation message into overlapping pieces long
// enough that finding one in an output is proof the message was copied, and
// short enough that a truncating or first-line-only "fix" cannot hide behind
// them.
//
// It is the sweep the three retired tests are replaced by. Those tests —
// TestTrimOperationMessage_V2cleanup36, TestFullOperationMessage_V2cleanup38
// and TestClassifyAddonApp_V2cleanup38_FullVsShort — each asserted that
// ArgoCD's own text SURVIVED onto the response: the first pinned the 300-char
// first-line form, the second required the tail "field not declared in schema"
// to still be present after a 4000-char cap, and the third forbade the short
// and full forms from being equal. All three were the leak's keepers. They are
// inverted here rather than deleted, so the same inputs are still exercised and
// the same outputs are still checked — for absence instead of presence.
func messageChunks(msg string) []string {
	var out []string
	const width = 24
	for i := 0; i+width <= len(msg); i += width / 2 {
		out = append(out, msg[i:i+width])
	}
	if len(out) == 0 && msg != "" {
		out = append(out, msg)
	}
	return out
}

// TestSafeAddonFailure_KeepsNoneOfArgocdsOwnWords is the direct replacement for
// the three retired tests. It uses the same live keda payload they used.
func TestSafeAddonFailure_KeepsNoneOfArgocdsOwnWords(t *testing.T) {
	// The live keda error, verbatim from the incident those tests captured —
	// multi-line, past 300 characters, and with a repository URL embedded in
	// it the way a Git-transport failure surfacing through ArgoCD writes one.
	const token = "K3pQ-status-classify-token-sentinel-7h2v-never-leaves-the-server"
	liveKedaErr := "one or more synchronization tasks completed unsuccessfully, reason: " +
		"failed to create typed patch object (keda/keda-admission-webhooks; apps/v1, Kind=Deployment): " +
		".spec.template.spec.containers[name=\"keda-admission-webhooks\"].resources.metricServer: " +
		"field not declared in schema\nwhile fetching https://x-access-token:" + token +
		"@github.example/sharko-org/addons.git"

	app := models.ArgocdApplication{
		SyncStatus:       "OutOfSync",
		HealthStatus:     "Degraded",
		OperationPhase:   "Failed",
		OperationMessage: liveKedaErr,
		SourceRepoURL:    "https://x-access-token:" + token + "@github.example/sharko-org/addons.git",
	}

	// The fixture must genuinely carry the token, or the absence below is an
	// absence of nothing.
	if !strings.Contains(app.OperationMessage, token) {
		t.Fatal("the fixture does not carry the token — this test proves nothing")
	}

	detail := safeAddonFailure(app)

	// The safe repository address is deliberately still in the detail, and the
	// message quotes that same repository, so chunks that fall entirely inside
	// it are not evidence of the message being copied. Everything else is.
	const safeRepo = "https://github.example/sharko-org/addons.git"

	if strings.Contains(detail, token) {
		t.Errorf("argocd_operation_message carries the repository token:\n%s", detail)
	}
	for _, chunk := range messageChunks(liveKedaErr) {
		if strings.Contains(safeRepo, chunk) {
			continue
		}
		if strings.Contains(detail, chunk) {
			t.Errorf("argocd_operation_message carries ArgoCD's own words (%q):\n%s", chunk, detail)
		}
	}
	// The retired test's own marker, named so a reviewer can see which
	// assertion was flipped.
	if strings.Contains(detail, "field not declared in schema") {
		t.Error("the tail TestFullOperationMessage_V2cleanup38 demanded be preserved is still being passed through")
	}

	// And it still tells an operator something true. A fix that returned an
	// empty string would pass every check above and help nobody.
	const wantSentence = "ArgoCD could not finish syncing this addon. Sharko does not repeat ArgoCD's own message here: that message quotes whatever ArgoCD was working on, which includes the repository address with its access token inside it. Open this application in ArgoCD to read the full error."
	if !strings.HasPrefix(detail, wantSentence) {
		t.Errorf("the detail is\n  %q\nwant it to start with exactly\n  %q", detail, wantSentence)
	}
	// The repository is still named, with the credential gone — the same trade
	// the init-status probe made.
	if !strings.Contains(detail, "repo=https://github.example/sharko-org/addons.git") {
		t.Errorf("the detail no longer names the repository, so an operator cannot tell which repo is failing:\n%s", detail)
	}
	for _, want := range []string{"phase=Failed", "sync=OutOfSync", "health=Degraded"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail dropped the safe fact %q:\n%s", want, detail)
		}
	}
	_ = min // kept for the assertions above and any future truncation check
}

// TestSafeAddonFailure_UnreadableRepoURLIsNotNamedAtAll: when SafeRepoURL
// cannot take a URL apart it says nothing, and so must this. Falling back to
// the original string would be the whole leak again.
func TestSafeAddonFailure_UnreadableRepoURLIsNotNamedAtAll(t *testing.T) {
	const token = "K3pQ-unreadable-url-sentinel-8j4c-never-leaves-the-server"
	app := models.ArgocdApplication{
		SyncStatus:     "OutOfSync",
		HealthStatus:   "Degraded",
		OperationPhase: "Failed",
		SourceRepoURL:  "https://x-access-token:" + token + "@github.example/org/repo.git\x7f",
	}
	detail := safeAddonFailure(app)
	if strings.Contains(detail, token) {
		t.Errorf("an unreadable repo URL put the token into the detail:\n%s", detail)
	}
	if strings.Contains(detail, "repo=") {
		t.Errorf("an unreadable repo URL must leave no repo= artifact at all:\n%s", detail)
	}
}

// TestSafeAddonFailure_UnknownArgocdWordsAreNotEchoed: the phase, sync and
// health values are ArgoCD's words arriving over the wire. Only the ones Sharko
// knows come through; anything else is named as unrecognised rather than
// repeated.
func TestSafeAddonFailure_UnknownArgocdWordsAreNotEchoed(t *testing.T) {
	const token = "K3pQ-unknown-enum-sentinel-2w9k-never-leaves-the-server"
	app := models.ArgocdApplication{
		SyncStatus:     "OutOfSync " + token,
		HealthStatus:   "Degraded " + token,
		OperationPhase: "Failed " + token,
	}
	detail := safeAddonFailure(app)
	if strings.Contains(detail, token) {
		t.Errorf("an unrecognised ArgoCD status word was echoed with the sentinel in it:\n%s", detail)
	}
	for _, want := range []string{"phase=unrecognised", "sync=unrecognised", "health=unrecognised"} {
		if !strings.Contains(detail, want) {
			t.Errorf("expected %q in the detail:\n%s", want, detail)
		}
	}
}
