// Package api — orchestrator <-> prtracker bridge.
//
// The orchestrator deliberately does NOT import internal/prtracker (that
// would close a small cycle through internal/audit), so it declares a
// minimal local interface (orchestrator.PRTracker) plus a local mirror
// of PRInfo (orchestrator.TrackedPR). This file lives in the api
// package because only this layer can import both safely.
//
// Every PR-creating orchestrator path funnels through
// commitChangesWithMeta, which calls TrackPR via this adapter, so every
// Sharko-created PR surfaces on the dashboard PR panel.

package api

import (
	"context"
	"time"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// prTrackerAdapter satisfies orchestrator.PRTracker by translating the
// orchestrator-local TrackedPR shape into the prtracker.PRInfo type the
// real Tracker expects.
type prTrackerAdapter struct {
	t *prtracker.Tracker
}

// TrackPR delegates to the underlying prtracker.Tracker. CreatedAt
// defaults to time.Now() when the orchestrator did not set it
// (defensive — every commitChangesWithMeta call site sets it).
func (a *prTrackerAdapter) TrackPR(ctx context.Context, p orchestrator.TrackedPR) error {
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	lastStatus := p.LastStatus
	if lastStatus == "" {
		lastStatus = "open"
	}
	return a.t.TrackPR(ctx, prtracker.PRInfo{
		PRID:       p.PRID,
		PRUrl:      p.PRUrl,
		PRBranch:   p.PRBranch,
		PRTitle:    p.PRTitle,
		PRBase:     p.PRBase,
		Cluster:    p.Cluster,
		Addon:      p.Addon,
		Operation:  p.Operation,
		User:       p.User,
		Source:     p.Source,
		CreatedAt:  createdAt,
		LastStatus: lastStatus,
	})
}

// attachPRTracker wires server-lifetime background hooks onto a
// per-request orchestrator instance. Despite the name (kept stable to
// avoid touching every call site), this helper now attaches TWO hooks:
//
//  1. PR tracker — every commitChangesWithMeta call automatically
//     tracks the resulting PR for dashboard surfacing.
//  2. Reconciler trigger — every managed-clusters.yaml commit nudges
//     the cluster Secret reconciler for sub-5s post-PR convergence;
//     absent the nudge the reconciler still converges on its 30s
//     safety-net tick.
//
// Both hooks are independently optional: a nil tracker or trigger is a
// silent no-op. Callers MUST NOT assume either is wired — production
// (cmd/sharko/serve.go) wires both only when the relevant Server-level
// setter was called at boot, and tests deliberately leave both unwired.
func (s *Server) attachPRTracker(orch *orchestrator.Orchestrator) *orchestrator.Orchestrator {
	if orch == nil {
		return orch
	}
	if s.prTracker != nil {
		orch.SetPRTracker(&prTrackerAdapter{t: s.prTracker})
	}
	if s.reconcilerTrigger != nil {
		orch.SetReconcilerTrigger(s.reconcilerTrigger)
	}
	// Per-cluster credential-fetch routing (V2-cleanup-60.4): share the
	// server-lifetime router so orchestrator-side fetches (addon ops,
	// adopt/unadopt, cluster updates) route inline-registered clusters via
	// the ArgoCD reader exactly like the api-side fetch sites — one cached
	// ArgoCD reader, one test seam. nil (no provider published) is a no-op:
	// the orchestrator keeps its New() default.
	orch.SetCredsRouter(s.credsRouter())
	return orch
}

// aiToolTrackerAdapter satisfies ai.ToolPRTracker so the AI assistant's
// write tools (enable_addon, disable_addon, update_addon_version) can
// surface their PRs on the dashboard PR panel under the "AI" filter chip.
type aiToolTrackerAdapter struct {
	t *prtracker.Tracker
}

// TrackPR converts the ai.ToolTrackedPR into a prtracker.PRInfo and
// delegates. CreatedAt is set to time.Now() because the AI tools call
// this at PR-creation time.
func (a *aiToolTrackerAdapter) TrackPR(ctx context.Context, p ai.ToolTrackedPR) error {
	return a.t.TrackPR(ctx, prtracker.PRInfo{
		PRID:       p.PRID,
		PRUrl:      p.PRUrl,
		PRBranch:   p.PRBranch,
		PRTitle:    p.PRTitle,
		PRBase:     p.PRBase,
		Cluster:    p.Cluster,
		Addon:      p.Addon,
		Operation:  p.Operation,
		User:       p.User,
		Source:     p.Source,
		CreatedAt:  time.Now(),
		LastStatus: "open",
	})
}
