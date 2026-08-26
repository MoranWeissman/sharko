package remediation

// permission_test.go — the background remediator needs ArgoCD's
// `applications, sync` permission just as the restart-sync button does, and
// an operator can perfectly reasonably be running without it: installing
// Sharko does not grant it, and Sharko never grants it to itself.
//
// The failure this pins is the ordering one. Remediation terminates the stale
// operation first and re-syncs second. Without the permission the re-sync was
// never going to happen, so terminating first cancels a running operation and
// puts nothing in its place — the application ends up worse off than if
// Sharko had done nothing. Asking ArgoCD before touching anything is the only
// way that cannot happen.

import (
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/models"
)

// TestRemediation_WhenArgoCDRefusesSync_NothingIsTerminated is the direction
// that catches remediation acting on a permission it does not have.
func TestRemediation_WhenArgoCDRefusesSync_NothingIsTerminated(t *testing.T) {
	mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	app := liveKedaApp(mergeBase)
	app.Project = "sharko-addons"

	fa := &fakeArgo{
		apps:    []models.ArgocdApplication{app},
		canSync: argocd.CapabilityDenied,
	}
	rem, ac := makeRemediator(fa, func() time.Time { return mergeBase })

	pr := makePR("keda", "moran-test", 11)
	pr.LastPolled = mergeBase
	rem.OnMerge(pr)

	if len(fa.terminated) != 0 {
		t.Errorf("the stale operation was terminated %d time(s) even though ArgoCD had already said the "+
			"re-sync would be refused. Terminating and then not re-syncing leaves the application with "+
			"nothing running, which is worse than leaving it alone.", len(fa.terminated))
	}
	if len(fa.synced) != 0 {
		t.Errorf("the sync was attempted %d time(s) after ArgoCD said no", len(fa.synced))
	}

	var found bool
	for _, e := range ac.all() {
		if e.Event != "argocd_auto_remediation_unavailable" {
			continue
		}
		found = true
		if e.Result != "skipped" {
			t.Errorf("the audit entry records result %q. Nothing was done, so recording anything that reads "+
				"as done or as a failure of Sharko's is wrong.", e.Result)
		}
		if !strings.Contains(e.Detail, "may not sync applications") {
			t.Errorf("the audit entry does not say what was missing: %q", e.Detail)
		}
		if !strings.Contains(e.Detail, "ArgoCD still applies what Git says") {
			t.Errorf("the audit entry does not say what still works without the permission, so somebody "+
				"reading the log cannot tell whether their fleet has stopped: %q", e.Detail)
		}
	}
	if !found {
		t.Errorf("nothing was recorded at all. A background action that quietly does nothing is the exact "+
			"silent no-op this check exists to prevent. entries: %v", ac.all())
	}
}

// TestRemediation_WhenTheCheckCannotAnswer_RemediationStillRuns is the other
// direction. An ArgoCD too old to answer the capability question must not have
// its silence read as a refusal — that would switch remediation off for
// installs that have the permission and always did.
func TestRemediation_WhenTheCheckCannotAnswer_RemediationStillRuns(t *testing.T) {
	mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	fa := &fakeArgo{
		apps:    []models.ArgocdApplication{liveKedaApp(mergeBase)},
		canSync: argocd.CapabilityUnknown,
	}
	rem, _ := makeRemediator(fa, func() time.Time { return mergeBase })

	pr := makePR("keda", "moran-test", 12)
	pr.LastPolled = mergeBase
	rem.OnMerge(pr)

	if len(fa.terminated) != 1 || len(fa.synced) != 1 {
		t.Errorf("ArgoCD could not answer whether the sync is allowed, so the calls themselves are the "+
			"authority and remediation must go ahead. terminated=%d synced=%d, want 1 and 1",
			len(fa.terminated), len(fa.synced))
	}
}
