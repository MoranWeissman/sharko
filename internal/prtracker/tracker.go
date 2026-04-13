// Package prtracker tracks pull requests created by Sharko operations.
// It polls the Git provider for status changes and emits audit events
// when PRs are merged or closed. State is persisted in a Kubernetes
// ConfigMap via cmstore, so pending PRs survive pod restarts.
package prtracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
)

// GitProvider is the subset of gitprovider.GitProvider needed by the tracker.
type GitProvider interface {
	GetPullRequestStatus(ctx context.Context, prNumber int) (string, error)
}

// Tracker polls the Git provider for PR status changes and persists
// tracking state in a ConfigMap.
type Tracker struct {
	cmStore     *cmstore.Store
	gitProvider func() GitProvider // lazy — returns nil when no connection is active
	auditFn     func(audit.Entry)
	onMergeFn   func(PRInfo) // callback when a PR is merged (e.g. trigger reconciler)
	interval    time.Duration
	triggerCh   chan struct{}
	stopCh      chan struct{}
	stopOnce    sync.Once
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

// Start launches the background poll loop. Runs one reconcile immediately,
// then repeats on every tick or Trigger() call.
func (t *Tracker) Start(ctx context.Context) {
	go func() {
		t.PollOnce(ctx)
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.PollOnce(ctx)
			case <-t.triggerCh:
				t.PollOnce(ctx)
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
func (t *Tracker) ListPRs(ctx context.Context, status, cluster, user string) ([]PRInfo, error) {
	data, err := t.cmStore.Read(ctx)
	if err != nil {
		return nil, err
	}
	prs := t.extractPRs(data)
	var result []PRInfo
	for _, pr := range prs {
		if status != "" && pr.LastStatus != status {
			continue
		}
		if cluster != "" && pr.Cluster != cluster {
			continue
		}
		if user != "" && pr.User != user {
			continue
		}
		result = append(result, pr)
	}
	return result, nil
}

// PollOnce performs a single poll pass: for each tracked PR, queries
// the Git provider for status, updates ConfigMap on change, and emits
// audit events on merge or close.
func (t *Tracker) PollOnce(ctx context.Context) {
	gp := t.gitProvider()
	if gp == nil {
		slog.Debug("[prtracker] no Git provider — skipping poll")
		return
	}

	data, err := t.cmStore.Read(ctx)
	if err != nil {
		slog.Error("[prtracker] failed to read state", "error", err)
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
			slog.Warn("[prtracker] failed to poll PR", "pr_id", pr.PRID, "error", err)
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
			slog.Info("[prtracker] PR merged", "pr_id", pr.PRID, "cluster", pr.Cluster, "operation", pr.Operation)
			t.auditFn(audit.Entry{
				Level:    "info",
				Event:    "pr_merged",
				User:     pr.User,
				Action:   pr.Operation,
				Resource: fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:   "prtracker",
				Result:   "success",
			})
			if t.onMergeFn != nil {
				t.onMergeFn(pr)
			}
			toRemove = append(toRemove, id)
		} else if status == "closed" {
			slog.Info("[prtracker] PR closed without merge", "pr_id", pr.PRID, "cluster", pr.Cluster)
			t.auditFn(audit.Entry{
				Level:    "warn",
				Event:    "pr_closed_without_merge",
				User:     pr.User,
				Action:   pr.Operation,
				Resource: fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:   "prtracker",
				Result:   "failure",
			})
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
			slog.Error("[prtracker] failed to write state", "error", err)
		}
	}
}

// PollSinglePR polls a single PR by ID and updates its status.
func (t *Tracker) PollSinglePR(ctx context.Context, prID int) (*PRInfo, error) {
	gp := t.gitProvider()
	if gp == nil {
		return nil, fmt.Errorf("no Git provider available")
	}

	status, err := gp.GetPullRequestStatus(ctx, prID)
	if err != nil {
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
				Level:    "info",
				Event:    "pr_merged",
				User:     pr.User,
				Action:   pr.Operation,
				Resource: fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:   "prtracker",
				Result:   "success",
			})
			if t.onMergeFn != nil {
				t.onMergeFn(pr)
			}
			delete(prs, key)
		} else if status == "closed" {
			t.auditFn(audit.Entry{
				Level:    "warn",
				Event:    "pr_closed_without_merge",
				User:     pr.User,
				Action:   pr.Operation,
				Resource: fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
				Source:   "prtracker",
				Result:   "failure",
			})
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
	slog.Info("[prtracker] reconciling on startup")
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
