package remediation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// legacyOnlyArgo models a cluster bootstrapped before the v4 rename: the only
// bootstrap Application it has is "cluster-addons-bootstrap", so refreshing
// "sharko-engine" fails the way a real ArgoCD would (app not found).
type legacyOnlyArgo struct {
	refreshed []string
	attempted []string
}

func (f *legacyOnlyArgo) ListApplications(context.Context) ([]models.ArgocdApplication, error) {
	return nil, nil
}
func (f *legacyOnlyArgo) TerminateOperation(context.Context, string) error { return nil }
func (f *legacyOnlyArgo) SyncApplication(context.Context, string) error    { return nil }
func (f *legacyOnlyArgo) CanSyncApplication(context.Context, string, string) argocd.Capability {
	return argocd.CapabilityAllowed
}
func (f *legacyOnlyArgo) RefreshApplication(_ context.Context, appName string, _ bool) (*models.ArgocdApplication, error) {
	f.attempted = append(f.attempted, appName)
	if appName == orchestrator.BootstrapRootAppName {
		return nil, fmt.Errorf("application %q not found", appName)
	}
	f.refreshed = append(f.refreshed, appName)
	return nil, nil
}

// TestOnMergeRefresh_FallsBackToLegacyBootstrapName — refreshing only the
// current name left every pre-v4 cluster waiting out ArgoCD's ~3-minute git
// poll after each merge, which is the exact delay OnMergeRefresh exists to
// remove.
func TestOnMergeRefresh_FallsBackToLegacyBootstrapName(t *testing.T) {
	fa := &legacyOnlyArgo{}
	var entries []audit.Entry
	rem := New(Deps{
		ArgoClient: fa,
		AuditFn:    func(e audit.Entry) { entries = append(entries, e) },
		NowFn:      func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) },
	})

	pr := makePR("keda", "prod-eu", 91)
	rem.OnMergeRefresh(context.Background(), pr)

	if len(fa.attempted) < 2 || fa.attempted[0] != orchestrator.BootstrapRootAppName {
		t.Fatalf("attempted = %v, want the current name tried first", fa.attempted)
	}
	found := false
	for _, name := range fa.refreshed {
		if name == orchestrator.BootstrapRootAppNameLegacy {
			found = true
		}
	}
	if !found {
		t.Errorf("the v3-named bootstrap app was never refreshed; refreshed=%v", fa.refreshed)
	}

	// The audit entry must name the app that actually refreshed, not the
	// one that failed.
	for _, e := range entries {
		if e.Event == "argocd_refreshed_after_merge" && e.Resource == "app:"+orchestrator.BootstrapRootAppName {
			t.Errorf("audit claims %q refreshed, but that call failed", orchestrator.BootstrapRootAppName)
		}
	}
}
