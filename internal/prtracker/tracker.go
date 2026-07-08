// Package prtracker tracks pull requests created by Sharko operations.
// It polls the Git provider for status changes and emits audit events
// when PRs are merged or closed. State is persisted in a Kubernetes
// ConfigMap via cmstore, so pending PRs survive pod restarts.
package prtracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
)

// syntheticTickID returns the canonical "prtrack-<unix_ts>" correlation ID
// for a single PR-tracker poll tick. The unix timestamp is captured at the
// tick boundary so every slog line emitted during the tick shares the same
// ID — searchable and clearly non-request-driven. V2-2.2.
func syntheticTickID() string {
	return fmt.Sprintf("prtrack-%d", time.Now().Unix())
}

// GitProvider is the subset of gitprovider.GitProvider needed by the tracker.
//
// DeleteBranch is included so the tracker can clean up the source
// branch when it observes a PR's status flipping to "merged" (e.g. an
// external user merged a Sharko-opened PR via the GitHub UI).
// Best-effort: failures here are logged but never block the loop.
type GitProvider interface {
	GetPullRequestStatus(ctx context.Context, prNumber int) (string, error)
	DeleteBranch(ctx context.Context, branchName string) error
}

// Tracker polls the Git provider for PR status changes and persists
// tracking state in a ConfigMap.
type Tracker struct {
	cmStore      *cmstore.Store
	gitProvider  func() GitProvider // lazy — returns nil when no connection is active
	auditFn      func(audit.Entry)
	onMergeFn    func(PRInfo)         // callback when a PR is merged (e.g. trigger reconciler)
	onCompleteFn func(PRInfo, string) // callback when a PR reaches ANY terminal state (merged OR closed); string is "merged" | "closed"
	interval     time.Duration
	triggerCh    chan struct{}
	stopCh       chan struct{}
	stopOnce     sync.Once
}

// NewTracker creates a PR tracker.
// gitProviderFn is a lazy accessor that returns the currently-active GitProvider,
// or nil when no connection is live.
// auditFn is called to emit audit events. It must be non-nil.
func NewTracker(
	cmStore *cmstore.Store,
	gitProviderFn func() GitProvider,
	auditFn func(audit.Entry),
) *Tracker {
	interval := 30 * time.Second
	if v := os.Getenv("SHARKO_PR_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}

	return &Tracker{
		cmStore:     cmStore,
		gitProvider: gitProviderFn,
		auditFn:     auditFn,
		interval:    interval,
		triggerCh:   make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
	}
}

// SetOnMergeFn registers a callback invoked when a tracked PR is merged.
// Typically used to trigger the argosecrets reconciler.
func (t *Tracker) SetOnMergeFn(fn func(PRInfo)) {
	t.onMergeFn = fn
}

// SetOnCompleteFn registers a callback invoked when a tracked PR reaches
// ANY terminal state — merged OR closed-without-merge — right before it is
// dropped from tracking. The second argument is the terminal status
// ("merged" or "closed"). Used by the durable change-log store
// (internal/changelog) to record a completed change before prtracker
// forgets the PR entirely (V2-cleanup-84.1). Unlike SetOnMergeFn, this
// fires on both outcomes so the change log captures rejected/abandoned
// changes too, not just successful ones.
func (t *Tracker) SetOnCompleteFn(fn func(PRInfo, string)) {
	t.onCompleteFn = fn
}

// Start launches the background poll loop. Runs one reconcile immediately,
// then repeats on every tick or Trigger() call.
//
// Every tick gets a fresh synthetic correlation ID (`prtrack-<unix_ts>`)
// attached to the per-tick context so every slog line emitted by PollOnce
// in that pass carries the same request_id. V2-2.2.
func (t *Tracker) Start(ctx context.Context) {
	go func() {
		t.PollOnce(logging.WithRequestID(ctx, syntheticTickID()))
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.PollOnce(logging.WithRequestID(ctx, syntheticTickID()))
			case <-t.triggerCh:
				t.PollOnce(logging.WithRequestID(ctx, syntheticTickID()))
			case <-ctx.Done():
				return
			case <-t.stopCh:
				return
			}
		}
	}()
}

// Stop shuts down the poll loop. Safe to call multiple times.
func (t *Tracker) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopCh)
	})
}

// Trigger requests an immediate poll. Non-blocking.
func (t *Tracker) Trigger() {
	select {
	case t.triggerCh <- struct{}{}:
	default:
	}
}

// TrackPR adds a PR to tracking.
func (t *Tracker) TrackPR(ctx context.Context, pr PRInfo) error {
	return t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		prs := t.extractPRs(data)
		key := strconv.Itoa(pr.PRID)
		prs[key] = pr
		return t.encodePRs(data, prs)
	})
}

// StopTracking removes a PR from tracking.
func (t *Tracker) StopTracking(ctx context.Context, prID int) error {
	return t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		prs := t.extractPRs(data)
		delete(prs, strconv.Itoa(prID))
		return t.encodePRs(data, prs)
	})
}

// GetPR returns a single tracked PR by ID.
func (t *Tracker) GetPR(ctx context.Context, prID int) (*PRInfo, error) {
	data, err := t.cmStore.Read(ctx)
	if err != nil {
		return nil, err
	}
	prs := t.extractPRs(data)
	pr, ok := prs[strconv.Itoa(prID)]
	if !ok {
		return nil, nil
	}
	return &pr, nil
}

// ListPRs returns all tracked PRs, optionally filtered.
//
// Callers that need to filter by Operation should use
// ListPRsFiltered, which takes an additional operations slice. ListPRs
// is preserved for backward compatibility (no Operation filter applied).
func (t *Tracker) ListPRs(ctx context.Context, status, cluster, addon, user string) ([]PRInfo, error) {
	return t.ListPRsFiltered(ctx, status, cluster, addon, user, nil)
}

// ListPRsFiltered returns all tracked PRs filtered by status, cluster,
// addon, user and (optional) operations. An empty/nil operations slice
// means "no operation filter" (return PRs of any operation). When set,
// only PRs whose Operation matches one of the supplied codes are
// returned.
func (t *Tracker) ListPRsFiltered(ctx context.Context, status, cluster, addon, user string, operations []string) ([]PRInfo, error) {
	data, err := t.cmStore.Read(ctx)
	if err != nil {
		return nil, err
	}
	prs := t.extractPRs(data)

	var opSet map[string]struct{}
	if len(operations) > 0 {
		built := make(map[string]struct{}, len(operations))
		for _, op := range operations {
			op = strings.TrimSpace(op)
			if op == "" {
				continue
			}
			built[op] = struct{}{}
		}
		// Only enable the filter if at least one non-empty operation
		// survived trimming. An all-blank slice is treated as "no
		// filter" — matches the CSV-handler intent where the user
		// passed `?operation=` or `?operation=  ,  `.
		if len(built) > 0 {
			opSet = built
		}
	}

	var result []PRInfo
	for _, pr := range prs {
		if status != "" && pr.LastStatus != status {
			continue
		}
		if cluster != "" && pr.Cluster != cluster {
			continue
		}
		if addon != "" && pr.Addon != addon {
			continue
		}
		if user != "" && pr.User != user {
			continue
		}
		if opSet != nil {
			if _, ok := opSet[pr.Operation]; !ok {
				continue
			}
		}
		result = append(result, pr)
	}
	return result, nil
}

// PollOnce performs a single poll pass: for each tracked PR, queries
// the Git provider for status, updates ConfigMap on change, and emits
// audit events on merge or close.
//
// All slog lines emitted within a single poll pass carry the request_id
// attached to ctx (synthetic `prtrack-<unix_ts>` when called from the tick
// loop, inbound request_id when called from a handler).
func (t *Tracker) PollOnce(ctx context.Context) {
	log := logging.LoggerFromContext(ctx)
	gp := t.gitProvider()
	if gp == nil {
		log.Debug("[prtracker] no Git provider — skipping poll")
		return
	}

	data, err := t.cmStore.Read(ctx)
	if err != nil {
		log.Error("[prtracker] failed to read state", "error", err)
		return
	}

	prs := t.extractPRs(data)
	if len(prs) == 0 {
		return
	}

	var changed bool
	var toRemove []string

	for id, pr := range prs {
		status, err := gp.GetPullRequestStatus(ctx, pr.PRID)
		if err != nil {
			// A definitive not-found (HTTP 404) means the PR no longer
			// exists on the provider — it was deleted, or the repo was
			// recreated. Drop the stale entry so it self-heals instead of
			// lingering as a phantom "open" PR forever. Every other error
			// (auth, rate-limit, 5xx, network) is treated as transient: keep
			// the entry and retry on the next poll.
			if errors.Is(err, gitprovider.ErrPullRequestNotFound) {
				log.Info("[prtracker] PR no longer exists on provider — dropping stale entry",
					"pr_id", pr.PRID, "cluster", pr.Cluster, "operation", pr.Operation)
				// Result is "failure", not "warn" — "warn" is a log level, not
				// one of the four valid audit result words
				// (success/partial/rejected/failure), and had no UI badge.
				// The PR vanishing out from under the tracker (deleted, or the
				// repo was recreated) means the tracked operation never
				// reached its intended end state — that's a failure outcome,
				// even though we log it at "warn" level since the tracker
				// self-heals by dropping the stale entry (V2-cleanup-85.2).
				t.auditFn(audit.Entry{
					Level:     "warn",
					Event:     "pr_gone",
					User:      pr.User,
					Action:    pr.Operation,
					Resource:  fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
					Source:    "prtracker",
					Result:    "failure",
					RequestID: logging.RequestID(ctx),
				})
				toRemove = append(toRemove, id)
				continue
			}
			log.Warn("[prtracker] failed to poll PR", "pr_id", pr.PRID, "error", err)
			continue
		}

		if status == pr.LastStatus {
			pr.LastPolled = time.Now()
			prs[id] = pr
			continue
		}

		pr.LastStatus = status
		pr.LastPolled = time.Now()
		prs[id] = pr
		changed = true

		if status == "merged" {
			log.Info("[prtracker] PR merged", "pr_id", pr.PRID, "cluster", pr.Cluster, "operation", pr.Operation)
			t.auditFn(audit.Entry{
				Level:     "info",
				Event:     "pr_merged",
				User:      pr.User,
				Action:    pr.Operation,
				Resource:  fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:    "prtracker",
				Result:    "success",
				RequestID: logging.RequestID(ctx),
			})
			// Delete the source branch on observed-merge. Skip silently
			// when the tracker has no branch on file (legacy state-store
			// entries may lack PRBranch). Best-effort: a DeleteBranch
			// error is logged but never blocks the tracker loop.
			if pr.PRBranch != "" {
				if delErr := gp.DeleteBranch(ctx, pr.PRBranch); delErr != nil {
					log.Warn("[prtracker] failed to delete branch after observed merge",
						"branch", pr.PRBranch, "pr_id", pr.PRID, "error", delErr)
				}
			}
			if t.onMergeFn != nil {
				t.onMergeFn(pr)
			}
			if t.onCompleteFn != nil {
				t.onCompleteFn(pr, "merged")
			}
			toRemove = append(toRemove, id)
		} else if status == "closed" {
			log.Info("[prtracker] PR closed without merge", "pr_id", pr.PRID, "cluster", pr.Cluster)
			t.auditFn(audit.Entry{
				Level:     "warn",
				Event:     "pr_closed_without_merge",
				User:      pr.User,
				Action:    pr.Operation,
				Resource:  fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:    "prtracker",
				Result:    "failure",
				RequestID: logging.RequestID(ctx),
			})
			if t.onCompleteFn != nil {
				t.onCompleteFn(pr, "closed")
			}
			toRemove = append(toRemove, id)
		}
	}

	// Remove merged/closed PRs from tracking.
	for _, id := range toRemove {
		delete(prs, id)
	}

	if changed || len(toRemove) > 0 {
		if err := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
			return t.encodePRs(data, prs)
		}); err != nil {
			log.Error("[prtracker] failed to write state", "error", err)
		}
	}
}

// PollSinglePR polls a single PR by ID and updates its status.
func (t *Tracker) PollSinglePR(ctx context.Context, prID int) (*PRInfo, error) {
	log := logging.LoggerFromContext(ctx)
	gp := t.gitProvider()
	if gp == nil {
		return nil, fmt.Errorf("no Git provider available")
	}

	status, err := gp.GetPullRequestStatus(ctx, prID)
	if err != nil {
		// Definitive not-found (HTTP 404): the PR is gone from the provider
		// (deleted, or repo recreated). Drop the stale entry and audit it,
		// mirroring how merged/closed entries are removed below. Every other
		// error stays transient — surface it so the caller retries later.
		if errors.Is(err, gitprovider.ErrPullRequestNotFound) {
			var gonePR *PRInfo
			rmErr := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
				prs := t.extractPRs(data)
				key := strconv.Itoa(prID)
				pr, ok := prs[key]
				if !ok {
					return nil
				}
				gonePR = &pr
				delete(prs, key)
				return t.encodePRs(data, prs)
			})
			if rmErr != nil {
				return nil, rmErr
			}
			if gonePR != nil {
				log.Info("[prtracker] PR no longer exists on provider — dropping stale entry",
					"pr_id", prID, "cluster", gonePR.Cluster, "operation", gonePR.Operation)
				t.auditFn(audit.Entry{
					Level:     "warn",
					Event:     "pr_gone",
					User:      gonePR.User,
					Action:    gonePR.Operation,
					Resource:  fmt.Sprintf("pr:%d cluster:%s", prID, gonePR.Cluster),
					Source:    "prtracker",
					Result:    "warn",
					RequestID: logging.RequestID(ctx),
				})
			}
			// The PR is gone; it is no longer tracked. Return no PR and no
			// error — this is a clean, expected terminal state, not a failure.
			return nil, nil
		}
		return nil, fmt.Errorf("poll PR #%d: %w", prID, err)
	}

	var result *PRInfo
	updateErr := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		prs := t.extractPRs(data)
		key := strconv.Itoa(prID)
		pr, ok := prs[key]
		if !ok {
			return fmt.Errorf("PR #%d not tracked", prID)
		}

		pr.LastStatus = status
		pr.LastPolled = time.Now()
		prs[key] = pr
		result = &pr

		if status == "merged" {
			t.auditFn(audit.Entry{
				Level:     "info",
				Event:     "pr_merged",
				User:      pr.User,
				Action:    pr.Operation,
				Resource:  fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:    "prtracker",
				Result:    "success",
				RequestID: logging.RequestID(ctx),
			})
			// Delete the source branch on observed-merge.
			// Best-effort: log on failure, never block the poll.
			if pr.PRBranch != "" {
				if delErr := gp.DeleteBranch(ctx, pr.PRBranch); delErr != nil {
					log.Warn("[prtracker] failed to delete branch after observed merge",
						"branch", pr.PRBranch, "pr_id", pr.PRID, "error", delErr)
				}
			}
			if t.onMergeFn != nil {
				t.onMergeFn(pr)
			}
			if t.onCompleteFn != nil {
				t.onCompleteFn(pr, "merged")
			}
			delete(prs, key)
		} else if status == "closed" {
			t.auditFn(audit.Entry{
				Level:     "warn",
				Event:     "pr_closed_without_merge",
				User:      pr.User,
				Action:    pr.Operation,
				Resource:  fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:    "prtracker",
				Result:    "failure",
				RequestID: logging.RequestID(ctx),
			})
			if t.onCompleteFn != nil {
				t.onCompleteFn(pr, "closed")
			}
			delete(prs, key)
		}

		return t.encodePRs(data, prs)
	})

	if updateErr != nil {
		return nil, updateErr
	}
	return result, nil
}

// ReconcileOnStartup reads persisted state and reconciles any PRs that
// changed while the server was down.
func (t *Tracker) ReconcileOnStartup(ctx context.Context) {
	ctx = logging.WithRequestID(ctx, "prtrack-startup-"+strconv.FormatInt(time.Now().Unix(), 10))
	logging.LoggerFromContext(ctx).Info("[prtracker] reconciling on startup")
	t.PollOnce(ctx)
}

// extractPRs reads the "prs" map from the ConfigMap state.
func (t *Tracker) extractPRs(data map[string]interface{}) map[string]PRInfo {
	result := make(map[string]PRInfo)

	prsRaw, ok := data["prs"]
	if !ok {
		return result
	}

	// The data comes from JSON unmarshal into map[string]interface{},
	// so we need to re-marshal and unmarshal into our typed map.
	b, err := json.Marshal(prsRaw)
	if err != nil {
		return result
	}

	if err := json.Unmarshal(b, &result); err != nil {
		slog.Warn("[prtracker] failed to unmarshal PRs from state", "error", err)
		return make(map[string]PRInfo)
	}
	return result
}

// encodePRs writes the PRs map back into the ConfigMap state.
func (t *Tracker) encodePRs(data map[string]interface{}, prs map[string]PRInfo) error {
	// Convert to map[string]interface{} for cmstore compatibility.
	b, err := json.Marshal(prs)
	if err != nil {
		return fmt.Errorf("marshal PRs: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("unmarshal PRs to interface: %w", err)
	}
	data["prs"] = raw
	return nil
}
