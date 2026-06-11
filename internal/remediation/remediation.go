// Package remediation handles automatic and manual recovery of ArgoCD
// applications that are stuck in a failing sync operation after a
// Sharko-authored configuration change merges.
//
// Auto-remediation fires from the prtracker merge callback. For each merged
// PR that carries an addon name, the remediator finds the matching ArgoCD
// application (addon-cluster naming convention) and, when it is failing with
// an operation that started BEFORE the merge, terminates the stale operation
// and re-syncs.
//
// Guard-rails (all must hold before acting):
//   - operation in flight AND failing (phase Failed|Error, or Running with a
//     SyncFailed resource) — uses the same classifyFailing logic as #418
//   - operationState.startedAt is before the PR merge time
//   - per-app cooldown of 5 minutes has elapsed
//   - only one auto-attempt per merged change (tracked by mergeKey = PRID+appName)
//   - kill-switch: env SHARKO_AUTO_REMEDIATE (absent/"true" = on, "false" = off)
package remediation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/service"
)

// isBenignTerminateError returns true when a TerminateOperation error
// indicates no operation was in progress (benign race window).
func isBenignTerminateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no operation is in progress") ||
		(strings.Contains(msg, "unexpected status 400") && strings.Contains(msg, "no operation"))
}

// ArgoClient is the subset of the ArgoCD client the remediator needs.
type ArgoClient interface {
	ListApplications(ctx context.Context) ([]models.ArgocdApplication, error)
	TerminateOperation(ctx context.Context, appName string) error
	SyncApplication(ctx context.Context, appName string) error
	// RefreshApplication triggers ArgoCD to re-fetch the application state from
	// Git. Called after a Sharko PR merges so ArgoCD picks up the new config
	// without waiting for its next poll cycle (~3 min in most environments).
	RefreshApplication(ctx context.Context, appName string, hard bool) (*models.ArgocdApplication, error)
}

// Deps holds the external dependencies for the Remediator.
// All fields are mandatory unless noted.
type Deps struct {
	ArgoClient ArgoClient
	AuditFn    func(audit.Entry) // called for every auto-remediation event
	NowFn      func() time.Time  // injectable for tests; defaults to time.Now
}

// Remediator watches merge events from prtracker and auto-terminates stale
// failing sync operations that Sharko caused.
type Remediator struct {
	deps Deps

	mu       sync.Mutex
	cooldown map[string]time.Time // appName → last-remediation time
	fired    map[string]bool      // mergeKey (prID+appName) → already acted
}

// New creates a Remediator. Call OnMerge from the prtracker merge callback.
func New(deps Deps) *Remediator {
	if deps.NowFn == nil {
		deps.NowFn = time.Now
	}
	return &Remediator{
		deps:     deps,
		cooldown: make(map[string]time.Time),
		fired:    make(map[string]bool),
	}
}

const (
	cooldownDuration = 5 * time.Minute
)

// OnMerge is called by the prtracker merge callback when a Sharko-authored PR
// merges. It resolves the affected ArgoCD application(s) and, when the
// failing-and-stale condition holds, terminates and re-syncs them.
func (r *Remediator) OnMerge(pr prtracker.PRInfo) {
	ctx := context.Background()

	// Resolve the addon name from PRInfo. Configure/addon operations carry it.
	addonName := pr.Addon
	clusterName := pr.Cluster

	// Merge time: use pr.LastPolled (set at the moment the merge is detected);
	// use now if it's zero.
	mergeTime := pr.LastPolled
	if mergeTime.IsZero() {
		mergeTime = r.deps.NowFn()
	}

	apps, err := r.deps.ArgoClient.ListApplications(ctx)
	if err != nil {
		slog.Error("remediation: failed to list ArgoCD applications", "error", err)
		return
	}

	for _, app := range apps {
		if !r.isSharkogenerated(app.Name) {
			continue
		}
		// When we know the addon+cluster, restrict to that app only.
		if addonName != "" && clusterName != "" {
			expected := addonName + "-" + clusterName
			if app.Name != expected {
				continue
			}
		} else if addonName != "" {
			// Only addon known: match any app whose name starts with addon-.
			if !strings.HasPrefix(app.Name, addonName+"-") {
				continue
			}
		}
		// Unknown addon: sweep all Sharko-generated apps (fallback path).

		if !r.isFailingAndStale(app, mergeTime) {
			continue
		}

		r.mu.Lock()
		mergeKey := fmt.Sprintf("%d/%s", pr.PRID, app.Name)
		if r.fired[mergeKey] {
			r.mu.Unlock()
			slog.Debug("remediation: already acted for this merge+app", "mergeKey", mergeKey)
			continue
		}
		if last, ok := r.cooldown[app.Name]; ok {
			if r.deps.NowFn().Sub(last) < cooldownDuration {
				r.mu.Unlock()
				slog.Debug("remediation: cooldown active", "app", app.Name,
					"remaining", cooldownDuration-r.deps.NowFn().Sub(last))
				continue
			}
		}
		r.fired[mergeKey] = true
		r.cooldown[app.Name] = r.deps.NowFn()
		r.mu.Unlock()

		r.act(ctx, app, pr)
	}
}

// act terminates the stale operation and re-syncs the application.
func (r *Remediator) act(ctx context.Context, app models.ArgocdApplication, pr prtracker.PRInfo) {
	slog.Info("remediation: terminating stale sync", "app", app.Name, "pr_id", pr.PRID,
		"operation_phase", app.OperationPhase, "started_at", app.OperationStartedAt)

	if err := r.deps.ArgoClient.TerminateOperation(ctx, app.Name); err != nil {
		if isBenignTerminateError(err) {
			// Race window: the operation finished between isFailingAndStale and now.
			// Log a warning and proceed to re-sync — the op is already done.
			slog.Warn("remediation: terminate returned benign 'no operation' error; proceeding to sync",
				"app", app.Name, "error", err)
		} else {
			slog.Error("remediation: terminate operation failed", "app", app.Name, "error", err)
			r.deps.AuditFn(audit.Entry{
				Level:    "error",
				Event:    "argocd_auto_remediation_failed",
				Action:   "terminate_operation",
				Resource: "app:" + app.Name,
				Source:   "remediation",
				Result:   "failure",
				Error:    err.Error(),
				Detail:   fmt.Sprintf("failed to terminate stale sync for %s after PR #%d merged", app.Name, pr.PRID),
			})
			return
		}
	}

	if err := r.deps.ArgoClient.SyncApplication(ctx, app.Name); err != nil {
		slog.Error("remediation: re-sync failed", "app", app.Name, "error", err)
		r.deps.AuditFn(audit.Entry{
			Level:    "error",
			Event:    "argocd_auto_remediation_failed",
			Action:   "sync_application",
			Resource: "app:" + app.Name,
			Source:   "remediation",
			Result:   "failure",
			Error:    err.Error(),
			Detail:   fmt.Sprintf("terminated stale sync for %s but re-sync failed after PR #%d merged", app.Name, pr.PRID),
		})
		return
	}

	msg := fmt.Sprintf("terminated stale sync for %s: operation predated the configuration change merged in PR #%d and was failing",
		app.Name, pr.PRID)
	slog.Info("remediation: auto-remediation completed", "app", app.Name, "pr_id", pr.PRID)
	r.deps.AuditFn(audit.Entry{
		Level:    "info",
		Event:    "argocd_auto_remediated",
		Action:   "terminate_and_sync",
		Resource: "app:" + app.Name,
		Source:   "remediation",
		Result:   "success",
		Detail:   msg,
	})
}

// LazyArgoClient wraps a ConnectionService and lazily resolves the active
// ArgoCD client at call time. This allows the remediator to be wired at
// startup even when no ArgoCD connection is configured yet.
type LazyArgoClient struct {
	ConnSvc *service.ConnectionService
}

func (l *LazyArgoClient) ListApplications(ctx context.Context) ([]models.ArgocdApplication, error) {
	c, err := l.ConnSvc.GetActiveArgocdClient()
	if err != nil {
		return nil, fmt.Errorf("no active ArgoCD connection: %w", err)
	}
	return c.ListApplications(ctx)
}

func (l *LazyArgoClient) TerminateOperation(ctx context.Context, appName string) error {
	c, err := l.ConnSvc.GetActiveArgocdClient()
	if err != nil {
		return fmt.Errorf("no active ArgoCD connection: %w", err)
	}
	return c.TerminateOperation(ctx, appName)
}

func (l *LazyArgoClient) SyncApplication(ctx context.Context, appName string) error {
	c, err := l.ConnSvc.GetActiveArgocdClient()
	if err != nil {
		return fmt.Errorf("no active ArgoCD connection: %w", err)
	}
	return c.SyncApplication(ctx, appName)
}

func (l *LazyArgoClient) RefreshApplication(ctx context.Context, appName string, hard bool) (*models.ArgocdApplication, error) {
	c, err := l.ConnSvc.GetActiveArgocdClient()
	if err != nil {
		return nil, fmt.Errorf("no active ArgoCD connection: %w", err)
	}
	return c.RefreshApplication(ctx, appName, hard)
}

// OnMergeRefresh triggers an ArgoCD refresh for the bootstrap application and,
// when PRInfo carries an addon+cluster, for the affected addon application too.
// This eliminates the ~3-minute git-poll delay after a Sharko PR merges.
//
// Refresh failures are non-fatal (slog.Warn only) — they are a best-effort
// optimisation, not a correctness requirement. No kill-switch; refresh is
// cheap and merge-driven.
func (r *Remediator) OnMergeRefresh(ctx context.Context, pr prtracker.PRInfo) {
	// Always refresh the bootstrap application.
	bootstrapAppName := orchestrator.BootstrapRootAppName
	if _, err := r.deps.ArgoClient.RefreshApplication(ctx, bootstrapAppName, false); err != nil {
		slog.Warn("remediation: failed to refresh bootstrap app after PR merge",
			"app", bootstrapAppName, "pr_id", pr.PRID, "error", err)
	} else {
		slog.Info("remediation: refreshed ArgoCD after PR merge",
			"app", bootstrapAppName, "pr_id", pr.PRID)
		r.deps.AuditFn(audit.Entry{
			Level:    "info",
			Event:    "argocd_refreshed_after_merge",
			Action:   "refresh_application",
			Resource: "app:" + bootstrapAppName,
			Source:   "remediation",
			Result:   "success",
			Detail:   fmt.Sprintf("refreshed ArgoCD after PR #%d merged", pr.PRID),
		})
	}

	// When the PR targets a specific addon+cluster, also refresh that app.
	if pr.Addon != "" && pr.Cluster != "" {
		addonAppName := pr.Addon + "-" + pr.Cluster
		if _, err := r.deps.ArgoClient.RefreshApplication(ctx, addonAppName, false); err != nil {
			slog.Warn("remediation: failed to refresh addon app after PR merge",
				"app", addonAppName, "pr_id", pr.PRID, "error", err)
		} else {
			slog.Info("remediation: refreshed ArgoCD after PR merge",
				"app", addonAppName, "pr_id", pr.PRID)
			r.deps.AuditFn(audit.Entry{
				Level:    "info",
				Event:    "argocd_refreshed_after_merge",
				Action:   "refresh_application",
				Resource: "app:" + addonAppName,
				Source:   "remediation",
				Result:   "success",
				Detail:   fmt.Sprintf("refreshed ArgoCD after PR #%d merged", pr.PRID),
			})
		}
	}
}

// IsAutoRemediateEnabled reports whether auto-remediation is on.
// Exported so serve.go can call it with the env value.
// "false" / "0" (case-insensitive) turns it off; anything else (including
// absent/empty) keeps it on — matching the connectivityCheck toggle idiom.
func IsAutoRemediateEnabled(val string) bool {
	return isAutoRemediateEnabled(val)
}

// isAutoRemediateEnabled is the unexported form used by tests.
func isAutoRemediateEnabled(val string) bool {
	return !strings.EqualFold(val, "false") && val != "0"
}

// isSharkogenerated returns true when the application name follows Sharko's
// generated naming convention: <addon>-<cluster>. Infrastructure apps and
// the connectivity-check app are excluded by the callers (they don't carry a
// Sharko PR so their mergeTime guard eliminates them anyway), but an extra
// heuristic here is: the name must contain a hyphen and must not be in the
// known infrastructure prefix list.
func (r *Remediator) isSharkogenerated(name string) bool {
	if name == "" || name == "sharko-connectivity-check" {
		return false
	}
	// Infrastructure app prefixes (mirrors isInfrastructureApp in service/cluster.go).
	infraPrefixes := []string{
		"argocd-", "cert-manager-system-", "kube-system-",
		"monitoring-", "ingress-nginx-", "external-secrets-system-",
	}
	lower := strings.ToLower(name)
	for _, p := range infraPrefixes {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	// Must contain a hyphen (addon-cluster pattern requires it).
	return strings.Contains(name, "-")
}

// isFailingAndStale returns true when the application has a failing operation
// that started before mergeTime AND is still in flight (not yet finished).
// It reuses the same detection logic as the #418 classifyAddonApp in
// internal/service (phase Failed|Error, or Running with HasSyncFailedResource
// or "completed unsuccessfully" in the message).
//
// A finished operation (OperationFinishedAt set) is NEVER terminated even if
// its terminal phase is Failed/Error — it is already done and terminating it
// would return a benign 400 from ArgoCD. The correct action for a finished
// failing op is to re-sync directly, which the caller handles when this
// function returns false.
func (r *Remediator) isFailingAndStale(app models.ArgocdApplication, mergeTime time.Time) bool {
	phase := app.OperationPhase
	if phase == "" {
		return false // no operation in flight
	}

	// A finished operation must not be terminated.
	if app.OperationFinishedAt != "" {
		return false
	}

	opMsg := app.OperationMessage
	opFailed := phase == "Failed" || phase == "Error"
	opRunningWithFailure := phase == "Running" &&
		(app.HasSyncFailedResource || strings.Contains(opMsg, "completed unsuccessfully"))

	if !opFailed && !opRunningWithFailure {
		return false // not failing
	}

	// Check that the operation started before the merge.
	if app.OperationStartedAt == "" {
		// No timestamp: treat conservatively as stale if merge time is in the past.
		return !mergeTime.IsZero()
	}
	startedAt, err := time.Parse(time.RFC3339, app.OperationStartedAt)
	if err != nil {
		// Unparseable timestamp: same conservative treatment.
		return true
	}
	return startedAt.Before(mergeTime)
}
