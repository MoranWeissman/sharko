package remediation

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// fakeArgo records calls to TerminateOperation and SyncApplication.
type fakeArgo struct {
	mu         sync.Mutex
	apps       []models.ArgocdApplication
	terminated []string
	synced     []string
	// errTerminate forces TerminateOperation to return an error.
	errTerminate error
	// errSync forces SyncApplication to return an error.
	errSync error
}

func (f *fakeArgo) ListApplications(_ context.Context) ([]models.ArgocdApplication, error) {
	return f.apps, nil
}

func (f *fakeArgo) TerminateOperation(_ context.Context, appName string) error {
	if f.errTerminate != nil {
		return f.errTerminate
	}
	f.mu.Lock()
	f.terminated = append(f.terminated, appName)
	f.mu.Unlock()
	return nil
}

func (f *fakeArgo) SyncApplication(_ context.Context, appName string) error {
	if f.errSync != nil {
		return f.errSync
	}
	f.mu.Lock()
	f.synced = append(f.synced, appName)
	f.mu.Unlock()
	return nil
}

// liveKedaApp is the fixture that mirrors the real keda incident shape.
// Operation Running + SyncFailed resource + "completed unsuccessfully".
func liveKedaApp(mergeTime time.Time) models.ArgocdApplication {
	// startedAt is 10 minutes before the merge — clearly stale.
	startedAt := mergeTime.Add(-10 * time.Minute)
	return models.ArgocdApplication{
		Name:             "keda-moran-test",
		SyncStatus:       "OutOfSync",
		HealthStatus:     "Healthy",
		OperationPhase:   "Running",
		OperationMessage: "one or more synchronization tasks completed unsuccessfully, reason: CustomResourceDefinition.apiextensions.k8s.io \"scaledjobs.keda.sh\" is invalid: metadata.annotations: Too long: must have at most 262144 bytes",
		HasSyncFailedResource: true,
		OperationStartedAt: startedAt.UTC().Format(time.RFC3339),
	}
}

type auditCapture struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *auditCapture) add(e audit.Entry) {
	a.mu.Lock()
	a.entries = append(a.entries, e)
	a.mu.Unlock()
}

func (a *auditCapture) all() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry{}, a.entries...)
}

func makeRemediator(fa *fakeArgo, nowFn func() time.Time) (*Remediator, *auditCapture) {
	ac := &auditCapture{}
	r := New(Deps{
		ArgoClient: fa,
		AuditFn:    ac.add,
		NowFn:      nowFn,
	})
	return r, ac
}

func makePR(addonName, clusterName string, prID int) prtracker.PRInfo {
	return prtracker.PRInfo{
		PRID:       prID,
		Addon:      addonName,
		Cluster:    clusterName,
		Operation:  prtracker.OpAddonConfigure,
		LastStatus: "merged",
		LastPolled: time.Now(),
	}
}

// TestRemediation_V2cleanup37 — table-driven tests covering all six spec cases.
func TestRemediation_V2cleanup37(t *testing.T) {
	mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name              string
		apps              []models.ArgocdApplication
		pr                prtracker.PRInfo
		envDisable        bool
		skipCooldown      bool // second call within cooldown window
		wantTerminated    int
		wantSynced        int
		wantAuditSuccess  bool
	}{
		{
			// (i) Live keda shape: op Running + SyncFailed task + startedAt before merge → terminate+sync once, audit recorded.
			name: "keda_live_shape_triggers_remediation",
			apps: []models.ArgocdApplication{
				liveKedaApp(mergeBase),
			},
			pr:             makePR("keda", "moran-test", 8),
			wantTerminated: 1,
			wantSynced:     1,
			wantAuditSuccess: true,
		},
		{
			// (ii) op started AFTER merge → no action.
			name: "op_started_after_merge_no_action",
			apps: []models.ArgocdApplication{
				{
					Name:               "keda-moran-test",
					OperationPhase:     "Running",
					OperationMessage:   "one or more synchronization tasks completed unsuccessfully",
					HasSyncFailedResource: true,
					OperationStartedAt: mergeBase.Add(5 * time.Minute).UTC().Format(time.RFC3339),
				},
			},
			pr:             makePR("keda", "moran-test", 9),
			wantTerminated: 0,
			wantSynced:     0,
		},
		{
			// (iii) op running but NOT failing → no action.
			name: "op_running_not_failing_no_action",
			apps: []models.ArgocdApplication{
				{
					Name:               "keda-moran-test",
					OperationPhase:     "Running",
					OperationMessage:   "sync in progress",
					HasSyncFailedResource: false,
					OperationStartedAt: mergeBase.Add(-10 * time.Minute).UTC().Format(time.RFC3339),
				},
			},
			pr:             makePR("keda", "moran-test", 10),
			wantTerminated: 0,
			wantSynced:     0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fa := &fakeArgo{apps: tc.apps}
			now := mergeBase
			rem, ac := makeRemediator(fa, func() time.Time { return now })

			tc.pr.LastPolled = mergeBase

			rem.OnMerge(tc.pr)

			if len(fa.terminated) != tc.wantTerminated {
				t.Errorf("TerminateOperation called %d times, want %d", len(fa.terminated), tc.wantTerminated)
			}
			if len(fa.synced) != tc.wantSynced {
				t.Errorf("SyncApplication called %d times, want %d", len(fa.synced), tc.wantSynced)
			}

			if tc.wantAuditSuccess {
				found := false
				for _, e := range ac.all() {
					if e.Event == "argocd_auto_remediated" && e.Result == "success" {
						found = true
					}
				}
				if !found {
					t.Errorf("expected audit entry argocd_auto_remediated/success, got %v", ac.all())
				}
			}
		})
	}
}

// TestRemediation_Cooldown — second event within 5 min is suppressed.
func TestRemediation_Cooldown(t *testing.T) {
	mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	fa := &fakeArgo{apps: []models.ArgocdApplication{liveKedaApp(mergeBase)}}
	now := mergeBase
	rem, _ := makeRemediator(fa, func() time.Time { return now })

	pr := makePR("keda", "moran-test", 8)
	pr.LastPolled = mergeBase

	// First call — should act.
	rem.OnMerge(pr)
	if len(fa.terminated) != 1 {
		t.Fatalf("first call: expected 1 terminate, got %d", len(fa.terminated))
	}

	// Advance time by 2 minutes — within the 5-min cooldown.
	now = mergeBase.Add(2 * time.Minute)
	// Second call with a different PR ID but same app.
	pr2 := makePR("keda", "moran-test", 99)
	pr2.LastPolled = now
	// Reset app startedAt so it still looks stale vs pr2's merge time.
	fa.apps = []models.ArgocdApplication{liveKedaApp(now)}
	rem.OnMerge(pr2)
	if len(fa.terminated) != 1 {
		t.Errorf("second call within cooldown: expected still 1 terminate, got %d", len(fa.terminated))
	}
}

// TestRemediation_OncePerMergeChange — same PR+app combination fires only once even if
// the merge callback fires again.
func TestRemediation_OncePerMergeChange(t *testing.T) {
	mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	fa := &fakeArgo{apps: []models.ArgocdApplication{liveKedaApp(mergeBase)}}
	now := mergeBase
	// Set NowFn so cooldown is at t+10min (already expired) but mergeKey deduplication fires.
	rem, _ := makeRemediator(fa, func() time.Time { return now })

	pr := makePR("keda", "moran-test", 8)
	pr.LastPolled = mergeBase

	rem.OnMerge(pr)
	if len(fa.terminated) != 1 {
		t.Fatalf("first call: expected 1 terminate, got %d", len(fa.terminated))
	}

	// Advance past cooldown.
	now = mergeBase.Add(10 * time.Minute)
	fa.apps = []models.ArgocdApplication{liveKedaApp(now)}

	// Same PR ID again — fired[mergeKey] should block it.
	pr.LastPolled = now
	rem.OnMerge(pr)
	if len(fa.terminated) != 1 {
		t.Errorf("second call with same PR ID: expected still 1 terminate (once-per-change), got %d", len(fa.terminated))
	}
}

// TestRemediation_KillSwitch — auto-remediation disabled env toggle.
// We test the isAutoRemediateEnabled helper directly.
func TestRemediation_KillSwitch(t *testing.T) {
	cases := []struct {
		env  string
		want bool
	}{
		{"", true},       // default: on
		{"true", true},
		{"1", true},
		{"false", false},
		{"0", false},
		{"FALSE", false},
	}
	for _, tc := range cases {
		got := isAutoRemediateEnabled(tc.env)
		if got != tc.want {
			t.Errorf("isAutoRemediateEnabled(%q) = %v, want %v", tc.env, got, tc.want)
		}
	}
}

// TestRemediation_NonSharkoApp — apps not matching Sharko naming are skipped.
func TestRemediation_NonSharkoApp(t *testing.T) {
	mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	startedAt := mergeBase.Add(-5 * time.Minute)
	nonSharkoApp := models.ArgocdApplication{
		// Infrastructure app — should be excluded.
		Name:               "argocd-notifications",
		OperationPhase:     "Failed",
		OperationStartedAt: startedAt.UTC().Format(time.RFC3339),
		OperationMessage:   "some failure",
	}
	fa := &fakeArgo{apps: []models.ArgocdApplication{nonSharkoApp}}
	rem, _ := makeRemediator(fa, func() time.Time { return mergeBase })
	pr := prtracker.PRInfo{PRID: 11, LastPolled: mergeBase, Operation: prtracker.OpAddonConfigure}
	rem.OnMerge(pr)
	if len(fa.terminated) != 0 {
		t.Errorf("non-Sharko app: expected 0 terminates, got %d", len(fa.terminated))
	}
}

// TestIsSharkogenerated tests the naming heuristic.
func TestIsSharkogenerated(t *testing.T) {
	rem := &Remediator{}
	cases := []struct {
		name string
		want bool
	}{
		{"keda-moran-test", true},
		{"cert-manager-prod", true},
		{"sharko-connectivity-check", false},
		{"argocd-notifications", false},
		{"kube-system-metrics-server", false},
		{"nohyphen", false},
		{"", false},
	}
	for _, tc := range cases {
		got := rem.isSharkogenerated(tc.name)
		if got != tc.want {
			t.Errorf("isSharkogenerated(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestIsFailingAndStale tests the stale detection logic.
func TestIsFailingAndStale(t *testing.T) {
	mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	before := mergeBase.Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	after := mergeBase.Add(5 * time.Minute).UTC().Format(time.RFC3339)
	rem := &Remediator{}

	cases := []struct {
		name      string
		app       models.ArgocdApplication
		want      bool
	}{
		{
			name: "running_syncfailed_before_merge",
			app: models.ArgocdApplication{
				OperationPhase:        "Running",
				OperationMessage:      "completed unsuccessfully",
				HasSyncFailedResource: true,
				OperationStartedAt:    before,
			},
			want: true,
		},
		{
			name: "running_syncfailed_after_merge",
			app: models.ArgocdApplication{
				OperationPhase:        "Running",
				OperationMessage:      "completed unsuccessfully",
				HasSyncFailedResource: true,
				OperationStartedAt:    after,
			},
			want: false,
		},
		{
			name: "phase_failed_before_merge",
			app: models.ArgocdApplication{
				OperationPhase:     "Failed",
				OperationMessage:   "sync failed",
				OperationStartedAt: before,
			},
			want: true,
		},
		{
			name: "running_not_failing",
			app: models.ArgocdApplication{
				OperationPhase:        "Running",
				OperationMessage:      "in progress",
				HasSyncFailedResource: false,
				OperationStartedAt:    before,
			},
			want: false,
		},
		{
			name: "no_operation",
			app:  models.ArgocdApplication{},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rem.isFailingAndStale(tc.app, mergeBase)
			if got != tc.want {
				t.Errorf("isFailingAndStale(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// Ensure the package compiles when SHARKO_AUTO_REMEDIATE is referenced.
func TestIsAutoRemediateEnabled_Compiled(_ *testing.T) {
	_ = isAutoRemediateEnabled(fmt.Sprintf("%v", true))
}
