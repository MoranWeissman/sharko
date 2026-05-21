// Package clusterreconciler reconciles ArgoCD cluster Secret state from
// configuration/managed-clusters.yaml in git. It mirrors the
// internal/prtracker pattern: a single goroutine drives reconciliation, a
// 30s safety-net tick catches drift, and a non-blocking Trigger() channel
// provides low-latency post-merge convergence when prTracker observes a
// Sharko-opened PR being merged.
//
// V125-1-8.0 (this file): scaffold only. pollOnce is a stub; Story 8.1
// lands the real git→ArgoCD diff + act logic. The public lifecycle API
// (New / Start / Stop / Trigger) is the stable contract that downstream
// stories build against.
//
// See:
//   - docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md
//     §7 (Option E), §8 (pattern), §12 (V125-1-8 deltas).
//   - internal/prtracker/tracker.go (pattern this package mirrors).
package clusterreconciler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

const (
	// DefaultTickInterval is the reconciler's safety-net poll cadence.
	//
	// Low-latency post-merge convergence is driven by prTracker.SetOnMergeFn
	// calling Reconciler.Trigger() — see Story 8.4 wiring. The periodic tick
	// catches drift that did NOT originate from a Sharko-opened PR (e.g. a
	// human editing managed-clusters.yaml directly in the repo UI, or an
	// ArgoCD cluster Secret being mutated out-of-band).
	DefaultTickInterval = 30 * time.Second

	// DefaultManagedClustersPath is the in-repo location of the source-of-truth
	// managed clusters file. Overridable via Deps.ManagedClustersPath.
	DefaultManagedClustersPath = "configuration/managed-clusters.yaml"
)

// ArgoClient is the subset of the ArgoCD cluster-Secret API the reconciler
// needs. Defined locally (and intentionally minimal for the scaffold) so the
// concrete client lives outside this package and Story 8.1 can extend the
// method set without churning every dispatch site.
//
// Story 8.1 will widen this interface to cover Upsert/Delete/List of cluster
// Secrets. Until then it is empty: the contract is "some object the
// reconciler can call into," and the build cost is zero.
type ArgoClient interface{}

// Vault is the subset of the credential-provider API the reconciler needs
// (per-cluster kubeconfig / bearer-token retrieval at reconcile time).
// Mirrors the prtracker pattern of declaring a local interface for the
// dependency rather than importing the concrete type — keeps the package
// dependency graph flat and the test seams small.
//
// Story 8.1 will widen this interface to specify the exact methods needed.
type Vault interface{}

// Deps holds the reconciler's external dependencies. Constructor-injected so
// tests can substitute fakes (k8s.io/client-go/kubernetes/fake, gitprovider
// mocks, audit no-ops). Using a struct (rather than positional args) means
// Story 8.1's added fields are non-breaking for the caller in serve.go.
type Deps struct {
	// CMStore persists reconciler state across restarts. Required.
	CMStore *cmstore.Store

	// GitProvider is a lazy accessor returning the currently-active provider
	// (or nil when no connection is configured). Matches the prtracker idiom
	// — reconciler must tolerate "no provider yet" without panicking.
	GitProvider func() gitprovider.GitProvider

	// ArgoClient is the ArgoCD cluster-Secret API. Required at Start time;
	// nil-checked by pollOnce in Story 8.1. See ArgoClient interface above.
	ArgoClient ArgoClient

	// Vault provides per-cluster credentials at reconcile time. Required at
	// Start time; nil-checked by pollOnce in Story 8.1. See Vault interface.
	Vault Vault

	// AuditFn is called to emit audit events for each reconcile action
	// (create / update / delete of an ArgoCD cluster Secret). Must be
	// non-nil; the constructor does not enforce this — callers are expected
	// to wire audit.Log.Add or equivalent.
	AuditFn func(audit.Entry)

	// TickInterval overrides the periodic poll cadence. Zero (or negative)
	// means DefaultTickInterval (30s).
	TickInterval time.Duration

	// ManagedClustersPath overrides the in-repo source-of-truth file.
	// Empty string means DefaultManagedClustersPath.
	ManagedClustersPath string
}

// Reconciler is a background reconciler that converges ArgoCD cluster Secret
// state with managed-clusters.yaml. See package doc for behaviour.
type Reconciler struct {
	deps                Deps
	tickInterval        time.Duration
	managedClustersPath string

	triggerCh chan struct{} // buffered(1) — Trigger() never blocks
	stopCh    chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once

	// pollFn is the per-instance test seam invoked by run() on every tick
	// or trigger. Production initializes it to (*Reconciler).pollOnce in
	// New(); tests may swap it before calling Start to observe ticks /
	// triggers without depending on the stub pollOnce's slog output.
	//
	// Per-instance (rather than a package-level var) so parallel tests do
	// not race on a shared seam.
	pollFn func(context.Context)
}

// New constructs a Reconciler with the given dependencies. It does NOT start
// the background goroutine — call Start(ctx) for that. The two-step
// New/Start split mirrors prtracker.NewTracker / (*Tracker).Start so wiring
// in cmd/sharko/serve.go is symmetric.
//
// Defaults applied:
//   - deps.TickInterval <= 0 → DefaultTickInterval (30s)
//   - deps.ManagedClustersPath == "" → DefaultManagedClustersPath
func New(deps Deps) *Reconciler {
	tick := deps.TickInterval
	if tick <= 0 {
		tick = DefaultTickInterval
	}
	path := deps.ManagedClustersPath
	if path == "" {
		path = DefaultManagedClustersPath
	}
	r := &Reconciler{
		deps:                deps,
		tickInterval:        tick,
		managedClustersPath: path,
		triggerCh:           make(chan struct{}, 1),
		stopCh:              make(chan struct{}),
	}
	r.pollFn = r.pollOnce
	return r
}

// Start launches the reconcile goroutine. Calling Start more than once is a
// no-op (sync.Once). Cancel via Stop() or by cancelling ctx.
//
// Unlike prtracker.Start, this scaffold does NOT run an immediate
// reconcile before entering the loop — Story 8.1 will revisit that
// decision when the real pollOnce lands (it may want a startup reconcile
// to recover from drift accumulated while the pod was down).
func (r *Reconciler) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		go r.run(ctx)
	})
}

// Stop signals the reconcile goroutine to exit. Idempotent — calling Stop
// multiple times is safe and never causes a close-of-closed-channel panic.
func (r *Reconciler) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
}

// Trigger requests an immediate reconcile on top of the periodic tick.
// Used by prTracker.SetOnMergeFn for low-latency post-merge convergence
// (Story 8.4 wiring). Never blocks: if a trigger is already queued, the
// call is a no-op (the pending tick will cover it).
//
// Safe to call before Start. The buffered channel will hold the request
// until the goroutine starts and drains it.
func (r *Reconciler) Trigger() {
	select {
	case r.triggerCh <- struct{}{}:
	default:
	}
}

// run is the reconcile loop. Internal — the only entry point is Start.
func (r *Reconciler) run(ctx context.Context) {
	ticker := time.NewTicker(r.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.pollFn(ctx)
		case <-r.triggerCh:
			r.pollFn(ctx)
		}
	}
}

// pollOnce is the reconcile body. STUB for V125-1-8.0 — Story 8.1
// implements the real git→ArgoCD diff + act logic. For the scaffold it
// only logs once and returns.
//
// Invoked via r.pollFn (per-instance test seam set up in New) rather than
// called directly so tests can observe tick / trigger events without
// depending on slog output.
func (r *Reconciler) pollOnce(ctx context.Context) {
	// ctx is accepted for parity with Story 8.1's real implementation.
	_ = ctx
	slog.Info("[clusterreconciler] tick (V125-1-8.0 scaffold — pollOnce not yet implemented; Story 8.1 lands the real logic)",
		"managed_clusters_path", r.managedClustersPath,
		"tick_interval", r.tickInterval,
	)
}
