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
	"github.com/MoranWeissman/sharko/internal/models"
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
	finishFn     FinishFn             // the step that can fail and is tried again; see SetFinishFn
	// maxFinishAttempts caps the tries of finishFn; 0 means DefaultMaxFinishAttempts.
	maxFinishAttempts int
	finishMu          sync.Mutex
	finishing         map[int]bool
	interval          time.Duration
	triggerCh         chan struct{}
	stopCh            chan struct{}
	stopOnce          sync.Once
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

// FinishFn is the work Sharko still has to do on a cluster's ArgoCD
// connection once a pull request reaches "merged" or "closed": take the
// ownership label off a kept connection, swap ownership after an adopt or
// a take over, or take a waiting mark off. Unlike the merge and complete
// hooks it can fail, and a failure keeps the pull request tracked so the
// next poll tries again.
type FinishFn func(ctx context.Context, pr PRInfo, status string) error

// SetFinishFn registers the finish step. It runs after the merge and
// complete hooks. When it returns an error the pull request stays tracked
// with its terminal status, and every later poll runs only this step
// again (the merge and complete hooks never run twice), up to
// maxFinishAttempts times.
func (t *Tracker) SetFinishFn(fn FinishFn) {
	t.finishFn = fn
}

// SetMaxFinishAttempts changes how many failed finish runs the tracker
// allows before it gives up. n <= 0 puts back DefaultMaxFinishAttempts.
func (t *Tracker) SetMaxFinishAttempts(n int) {
	t.maxFinishAttempts = n
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
		// A pull request already merged or closed that is only waiting
		// for its finish step is not "pending" any more. It is listed
		// only when that exact status is asked for.
		if isTerminal(pr.LastStatus) && status != pr.LastStatus {
			continue
		}
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

// DefaultMaxFinishAttempts is how many times the tracker runs a pull
// request's finish step (see SetFinishFn) before it gives up on it. With
// the default 30 s poll that is about ten minutes of tries. When it gives
// up it logs an error and writes an audit entry, and it drops the pull
// request, but the finish step never ran to the end, so whatever mark was
// protecting the cluster's connection stays on it. Nothing gets deleted
// because a finish failed.
const DefaultMaxFinishAttempts = 20

// isTerminal says whether a status is one a pull request never leaves.
func isTerminal(status string) bool {
	return status == "merged" || status == "closed"
}

// PollOnce performs a single poll pass: for each tracked PR, queries
// the Git provider for status, updates ConfigMap on change, and emits
// audit events on merge or close.
//
// It never writes back the copy it read at the start. Every change goes
// into the state as it is at write time, inside ReadModifyWrite, and only
// for the pull requests this pass handled. So a pull request tracked
// while the pass runs is never lost (EP3 dry run round R, R2). The hooks
// run outside the store's lock.
//
// A pull request that reached merged or closed but whose finish step
// failed is kept with that status. Later passes do not ask Git about it
// again; they only run the finish step again (R1, R4).
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

	polledAt := make(map[string]time.Time)
	newStatus := make(map[string]string)
	var gone []string

	for id, pr := range prs {
		if isTerminal(pr.LastStatus) {
			// Git already said merged or closed and the hooks already
			// ran. Only the finish step is still owed.
			t.runFinish(ctx, pr, pr.LastStatus)
			continue
		}
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
				gone = append(gone, id)
				continue
			}
			log.Warn("[prtracker] failed to poll PR", "pr_id", pr.PRID, "error", err)
			continue
		}

		if !isTerminal(status) {
			polledAt[id] = time.Now()
			if status != pr.LastStatus {
				newStatus[id] = status
			}
			continue
		}
		t.handleTerminal(ctx, gp, pr.PRID, status)
	}

	if len(newStatus) == 0 && len(gone) == 0 && len(polledAt) == 0 {
		return
	}
	if err := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		cur := t.extractPRs(data)
		for _, id := range gone {
			if p, ok := cur[id]; ok && !isTerminal(p.LastStatus) {
				delete(cur, id)
			}
		}
		for id, at := range polledAt {
			p, ok := cur[id]
			if !ok || isTerminal(p.LastStatus) {
				continue
			}
			p.LastPolled = at
			if st, changed := newStatus[id]; changed {
				p.LastStatus = st
			}
			cur[id] = p
		}
		return t.encodePRs(data, cur)
	}); err != nil {
		log.Error("[prtracker] failed to write state", "error", err)
	}
}

// claimTerminal records, in the stored state, that a tracked pull request
// reached a terminal status. Only the first caller to do this claims it,
// so a pull request seen merged by the poll loop and by a refresh at the
// same moment runs its hooks once. found is false when the pull request is
// not tracked at all.
func (t *Tracker) claimTerminal(ctx context.Context, prID int, status string) (pr PRInfo, found, claimed bool, err error) {
	err = t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		prs := t.extractPRs(data)
		key := strconv.Itoa(prID)
		cur, ok := prs[key]
		found, claimed = ok, false
		if !ok {
			return nil
		}
		pr = cur
		if isTerminal(cur.LastStatus) {
			return nil
		}
		cur.LastStatus = status
		cur.LastPolled = time.Now()
		cur.FinishAttempts = 0
		prs[key] = cur
		pr = cur
		claimed = true
		return t.encodePRs(data, prs)
	})
	return pr, found, claimed, err
}

// handleTerminal claims a pull request that just reached merged or closed,
// then (outside the store's lock) writes its audit entry, deletes its
// branch on merge, runs the merge and complete hooks once, and runs the
// finish step. It returns the claimed entry, or nil when someone else
// claimed it first or it is not tracked.
func (t *Tracker) handleTerminal(ctx context.Context, gp GitProvider, prID int, status string) (*PRInfo, bool, error) {
	log := logging.LoggerFromContext(ctx)
	pr, found, claimed, err := t.claimTerminal(ctx, prID, status)
	if err != nil {
		log.Error("[prtracker] failed to write state", "pr_id", prID, "error", err)
		return nil, found, err
	}
	if !claimed {
		return nil, found, nil
	}

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
	} else {
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
	}
	if t.onCompleteFn != nil {
		t.onCompleteFn(pr, status)
	}
	t.runFinish(ctx, pr, status)
	return &pr, true, nil
}

// runFinish runs the finish step for a pull request that reached a
// terminal status, then settles the stored entry: dropped when the step
// worked (or there is no step), kept with one more attempt counted when it
// failed, and dropped with a loud error once it has failed
// maxFinishAttempts times. Two finish runs for the same pull request never
// overlap.
func (t *Tracker) runFinish(ctx context.Context, pr PRInfo, status string) {
	log := logging.LoggerFromContext(ctx)

	t.finishMu.Lock()
	if t.finishing == nil {
		t.finishing = make(map[int]bool)
	}
	if t.finishing[pr.PRID] {
		t.finishMu.Unlock()
		return
	}
	t.finishing[pr.PRID] = true
	t.finishMu.Unlock()
	defer func() {
		t.finishMu.Lock()
		delete(t.finishing, pr.PRID)
		t.finishMu.Unlock()
	}()

	var finishErr error
	if t.finishFn != nil {
		finishErr = t.finishFn(ctx, pr, status)
	}

	maxTries := t.maxFinishAttempts
	if maxTries <= 0 {
		maxTries = DefaultMaxFinishAttempts
	}
	var attempts int
	var gaveUp, kept bool
	err := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		prs := t.extractPRs(data)
		key := strconv.Itoa(pr.PRID)
		cur, ok := prs[key]
		attempts, gaveUp, kept = 0, false, false
		if !ok {
			return nil
		}
		if finishErr == nil {
			delete(prs, key)
			return t.encodePRs(data, prs)
		}
		cur.FinishAttempts++
		attempts = cur.FinishAttempts
		if attempts >= maxTries {
			gaveUp = true
			delete(prs, key)
		} else {
			kept = true
			prs[key] = cur
		}
		return t.encodePRs(data, prs)
	})
	if err != nil {
		log.Error("[prtracker] failed to write state", "pr_id", pr.PRID, "error", err)
		return
	}
	switch {
	case gaveUp:
		log.Error("[prtracker] GAVE UP finishing a pull request after every try failed. The mark that protects the cluster's connection stays on it, so nothing gets deleted. Look at the connection and finish it by hand.",
			"pr_id", pr.PRID, "cluster", pr.Cluster, "operation", pr.Operation,
			"status", status, "attempts", attempts, "error", finishErr)
		t.auditFn(audit.Entry{
			Level:     "error",
			Event:     "pr_finish_gave_up",
			User:      pr.User,
			Action:    pr.Operation,
			Resource:  fmt.Sprintf("pr:%d cluster:%s", pr.PRID, pr.Cluster),
			Source:    "prtracker",
			Result:    "failure",
			RequestID: logging.RequestID(ctx),
		})
	case kept:
		log.Warn("[prtracker] could not finish a pull request yet; keeping it tracked to try again",
			"pr_id", pr.PRID, "cluster", pr.Cluster, "operation", pr.Operation,
			"status", status, "attempt", attempts, "max_attempts", maxTries, "error", finishErr)
	}
}

// The sentinels below let callers (internal/api/prs.go's handleRefreshPR)
// classify a PollSinglePR failure into an honest HTTP status instead of a
// blanket 500 — the UI polls this endpoint every 7s after a submit, so one
// transient Gitea/GitHub hiccup used to paint the browser console red on
// every poll (v4-walkfix W1 item 3). errors.Is is used rather than string
// matching so the classification survives message wording changes.
var (
	// ErrPRNotTracked means the Git provider answered fine, but this PR ID
	// is not (or no longer) in the tracker's own state — a 404, not a
	// server fault.
	ErrPRNotTracked = errors.New("pr not tracked")
	// ErrGitProviderUnavailable means Sharko itself has no Git provider
	// configured right now — a genuine internal/config fault, not a
	// transient upstream blip.
	ErrGitProviderUnavailable = errors.New("no git provider available")
	// ErrPRPollUpstream wraps any error the Git provider itself returned
	// from the actual status call (network blip, rate limit, auth hiccup,
	// a Gitea 405 mid-merge race, ...) — this is the transient case that
	// should map to 502, not 500.
	ErrPRPollUpstream = errors.New("pr poll upstream error")
)

// PollSinglePR polls a single PR by ID and updates its status.
func (t *Tracker) PollSinglePR(ctx context.Context, prID int) (*PRInfo, error) {
	log := logging.LoggerFromContext(ctx)
	gp := t.gitProvider()
	if gp == nil {
		return nil, fmt.Errorf("%w", ErrGitProviderUnavailable)
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
				if !ok || isTerminal(pr.LastStatus) {
					// Already merged or closed and waiting for its
					// finish step: leave it for the poll loop.
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
		return nil, fmt.Errorf("poll PR #%d: %w: %w", prID, ErrPRPollUpstream, err)
	}

	if isTerminal(status) {
		claimedPR, found, err := t.handleTerminal(ctx, gp, prID, status)
		if err != nil {
			return nil, err
		}
		if claimedPR != nil {
			return claimedPR, nil
		}
		if !found {
			return nil, fmt.Errorf("PR #%d not tracked: %w", prID, ErrPRNotTracked)
		}
		// Someone else already saw it finish; it is waiting for its
		// finish step (or about to be dropped). Report what is stored.
		cur, err := t.GetPR(ctx, prID)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			p := PRInfo{PRID: prID, LastStatus: status}
			return &p, nil
		}
		return cur, nil
	}

	var result *PRInfo
	updateErr := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		prs := t.extractPRs(data)
		key := strconv.Itoa(prID)
		pr, ok := prs[key]
		if !ok {
			return fmt.Errorf("PR #%d not tracked: %w", prID, ErrPRNotTracked)
		}
		if !isTerminal(pr.LastStatus) {
			pr.LastStatus = status
		}
		pr.LastPolled = time.Now()
		prs[key] = pr
		result = &pr
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

// KeptConnections tells the cluster reconciler's orphan sweep, per
// cluster, what its tracked remove and un-adopt pull requests still have
// to do with the cluster's ArgoCD connection:
//
//   - a merged pull request that keeps the connection and has not finished
//     yet gives ReleasePR: the sweep hands the connection back for that
//     pull request instead of deleting it. An OPEN one that keeps the
//     connection only holds it: it may still be closed, and then the
//     connection stays Sharko's (EP3 dry run round S, S3).
//   - any remove or un-adopt that is still open, or merged and not
//     finished, gives Hold: the sweep leaves the Secret alone, so a
//     cleanup=all finish can still read the cluster's credentials and
//     remove the addon secrets before the connection goes (S1).
//
// Closed pull requests hold nothing. It only reads the tracker's
// ConfigMap, so a Tracker built with a nil Git provider (read-only view)
// can answer it.
func (t *Tracker) KeptConnections(ctx context.Context) (map[string]models.ConnectionHold, error) {
	data, err := t.cmStore.Read(ctx)
	if err != nil {
		return nil, err
	}
	kept := make(map[string]models.ConnectionHold)
	for _, pr := range t.extractPRs(data) {
		if pr.Cluster == "" || (pr.Operation != OpRemoveCluster && pr.Operation != OpUnadoptCluster) {
			continue
		}
		if pr.LastStatus != "open" && pr.LastStatus != "merged" {
			continue
		}
		h := kept[pr.Cluster]
		h.Hold = true
		if pr.LastStatus == "merged" && pr.FollowUp != nil && pr.FollowUp.KeepConnection {
			h.ReleasePR = pr.PRID
		}
		kept[pr.Cluster] = h
	}
	return kept, nil
}
