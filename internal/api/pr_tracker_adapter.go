// Package api — orchestrator <-> prtracker bridge (V125-1-6).
//
// The orchestrator deliberately does NOT import internal/prtracker (that
// would close a small cycle through internal/audit), so it declares a
// minimal local interface (orchestrator.PRTracker) plus a local mirror
// of PRInfo (orchestrator.TrackedPR). This file lives in the api
// package because only this layer can import both safely.
//
// Story V125-1-6 / BUG-056: every PR-creating orchestrator path now
// funnels through commitChangesWithMeta, which calls TrackPR via this
// adapter. Previously only ad-hoc handler-level TrackPR calls fired,
// so register-cluster, adopt-cluster, init, and 5 orchestrator addon
// ops were silently missing from the dashboard PR panel.

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

// attachPRTracker wires the server's PR tracker onto an orchestrator
// instance so every commitChangesWithMeta call automatically tracks
// the resulting PR. Safe to call when no tracker is configured (no-op).
func (s *Server) attachPRTracker(orch *orchestrator.Orchestrator) *orchestrator.Orchestrator {
	if orch == nil || s.prTracker == nil {
		return orch
	}
	orch.SetPRTracker(&prTrackerAdapter{t: s.prTracker})
	return orch
}

// aiToolTrackerAdapter satisfies ai.ToolPRTracker so the AI assistant's
// write tools (enable_addon, disable_addon, update_addon_version) can
// surface their PRs on the dashboard PR panel under the "AI" filter
// chip. V125-1-6.
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
