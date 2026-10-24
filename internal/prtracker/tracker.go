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
	// now is the clock; tests move it to step through the retry gaps.
	now       func() time.Time
	finishMu  sync.Mutex
	finishing map[int]bool
	interval  time.Duration
	triggerCh chan struct{}
	stopCh    chan struct{}
	stopOnce  sync.Once
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

// clock returns the current time from the tracker's clock.
func (t *Tracker) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// SetClock replaces the clock the retry gaps are measured with. Tests
// only; nil puts back time.Now.
func (t *Tracker) SetClock(now func() time.Time) {
	t.now = now
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
		// The default list (no status) shows what is still going on:
		// open pull requests, and merged or closed ones whose finish step
		// has not run yet ("finishing", EP3 dry run round S, S21). One
		// whose finish was given up is listed only when its exact status
		// (merged or closed) is asked for.
		switch status {
		case "":
			if pr.GaveUp {
				continue
			}
		case StatusFinishing:
			if !IsFinishing(pr) {
				continue
			}
		default:
			if pr.LastStatus != status {
				continue
			}
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
// FinishRetryGap between tries that is about ten minutes in all, however
// often something asks for a poll (EP3 dry run round S, S10). When it
// gives up it logs an error and writes one audit entry, and it keeps the
// entry marked as given up (see GaveUpKeepFor), so nothing starts the
// same tries again.
const DefaultMaxFinishAttempts = 20

// FinishRetryGap is the least time between two tries of a failed finish
// step. A poll that comes sooner (a Trigger, a refresh) skips it.
const FinishRetryGap = 30 * time.Second

// GaveUpKeepFor is how long an entry whose finish was given up stays in
// the tracker's state before it is dropped.
const GaveUpKeepFor = 30 * 24 * time.Hour

// GaveUpLogMessage is the error line written once when the tracker gives
// up on a finish step. It is true for every kind of pull request: it does
// not say a mark protects the connection, because a removal with
// cleanup=all has none.
const GaveUpLogMessage = "[prtracker] Sharko gave up finishing a pull request: every try failed. The work after the merge or close is not done. Look at the cluster and its ArgoCD connection, and finish it by hand."

// StatusFinishing is the list status of a pull request that merged or
// closed and whose finish step has not run to the end yet.
const StatusFinishing = "finishing"

// IsFinishing says whether a tracked pull request merged or closed and
// its finish step is still owed (not given up).
func IsFinishing(pr PRInfo) bool {
	return isTerminal(pr.LastStatus) && !pr.GaveUp
}

// ListStatus is the status the PR list shows: "finishing" while the
// finish step is still owed, otherwise the status Git gave.
func ListStatus(pr PRInfo) string {
	if IsFinishing(pr) {
		return StatusFinishing
	}
	return pr.LastStatus
}

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
	var expired []string

	for id, pr := range prs {
		if isTerminal(pr.LastStatus) {
			if pr.GaveUp {
				if t.clock().Sub(pr.GaveUpAt) > GaveUpKeepFor {
					expired = append(expired, id)
				}
				continue
			}
			// Git already said merged or closed. The finish step (and
			// the hooks, if a restart came before they ran) is still
			// owed. runFinish waits out the gap after a failed try.
			t.runFinish(ctx, pr.PRID, pr.LastStatus, false)
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
			// It is handled as closed, so its finish step still runs: a
			// waiting mark comes off and nothing is left waiting forever
			// (EP3 dry run round S, S10).
			if errors.Is(err, gitprovider.ErrPullRequestNotFound) {
				t.handleTerminal(ctx, pr.PRID, "closed", true)
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
		t.handleTerminal(ctx, pr.PRID, status, false)
	}

	if len(newStatus) == 0 && len(expired) == 0 && len(polledAt) == 0 {
		return
	}
	if err := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		cur := t.extractPRs(data)
		for _, id := range expired {
			if p, ok := cur[id]; ok && p.GaveUp {
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
func (t *Tracker) claimTerminal(ctx context.Context, prID int, status string, gone bool) (pr PRInfo, found, claimed bool, err error) {
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
		cur.NextFinishAt = time.Time{}
		cur.HooksPending = true
		cur.Gone = gone
		prs[key] = cur
		pr = cur
		claimed = true
		return t.encodePRs(data, prs)
	})
	return pr, found, claimed, err
}

// handleTerminal claims a pull request that just reached merged or closed
// (gone: Git said it no longer exists, handled as closed), then runs its
// hooks and its finish step through runFinish. It returns the claimed
// entry, or nil when someone else claimed it first or it is not tracked.
func (t *Tracker) handleTerminal(ctx context.Context, prID int, status string, gone bool) (*PRInfo, bool, error) {
	log := logging.LoggerFromContext(ctx)
	pr, found, claimed, err := t.claimTerminal(ctx, prID, status, gone)
	if err != nil {
		log.Error("[prtracker] failed to write state", "pr_id", prID, "error", err)
		return nil, found, err
	}
	if !claimed {
		return nil, found, nil
	}
	if done := t.runFinish(ctx, prID, status, true); done != nil {
		pr = *done
	}
	return &pr, true, nil
}

// runHooks writes the merged, closed or gone audit entry, deletes the
// branch on merge, and runs the merge and complete hooks.
func (t *Tracker) runHooks(ctx context.Context, pr PRInfo, status string) {
	log := logging.LoggerFromContext(ctx)
	switch {
	case status == "merged":
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
		if gp := t.gitProvider(); pr.PRBranch != "" && gp != nil {
			if delErr := gp.DeleteBranch(ctx, pr.PRBranch); delErr != nil {
				log.Warn("[prtracker] failed to delete branch after observed merge",
					"branch", pr.PRBranch, "pr_id", pr.PRID, "error", delErr)
			}
		}
		if t.onMergeFn != nil {
			t.onMergeFn(pr)
		}
	case pr.Gone:
		// The PR vanished from the provider (deleted, or the repo was
		// recreated). It is handled as closed. Result is "failure": the
		// tracked operation never reached its end state (V2-cleanup-85.2).
		log.Info("[prtracker] PR no longer exists on provider — handling it as closed",
			"pr_id", pr.PRID, "cluster", pr.Cluster, "operation", pr.Operation)
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
	default:
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
}

// runFinish does what is still owed for a pull request that reached a
// terminal status. It reads the entry as stored now. If its hooks may not
// have run (HooksPending: just claimed, or a restart came in between), it
// runs them and clears the flag. Then, unless a failed try was too recent
// (NextFinishAt), it runs the finish step and settles the entry: dropped
// when the step worked (or there is no step), kept with one more attempt
// and the next try time when it failed, and kept as given up, with one
// error line and one audit entry, once it has failed maxFinishAttempts
// times. Two runs for the same pull request never overlap. It returns the
// entry as it was before the finish step ran, or nil when nothing ran.
func (t *Tracker) runFinish(ctx context.Context, prID int, status string, fresh bool) *PRInfo {
	log := logging.LoggerFromContext(ctx)

	t.finishMu.Lock()
	if t.finishing == nil {
		t.finishing = make(map[int]bool)
	}
	if t.finishing[prID] {
		t.finishMu.Unlock()
		return nil
	}
	t.finishing[prID] = true
	t.finishMu.Unlock()
	defer func() {
		t.finishMu.Lock()
		delete(t.finishing, prID)
		t.finishMu.Unlock()
	}()

	cur, err := t.GetPR(ctx, prID)
	if err != nil {
		log.Error("[prtracker] failed to read state", "pr_id", prID, "error", err)
		return nil
	}
	if cur == nil || cur.LastStatus != status || cur.GaveUp {
		return nil
	}
	pr := *cur
	if pr.HooksPending {
		if !fresh {
			log.Info("[prtracker] running the merge or close steps again: Sharko stopped before they were done",
				"pr_id", pr.PRID, "cluster", pr.Cluster, "operation", pr.Operation)
		}
		t.runHooks(ctx, pr, status)
		pr.HooksPending = false
		if err := t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
			prs := t.extractPRs(data)
			key := strconv.Itoa(prID)
			p, ok := prs[key]
			if !ok {
				return nil
			}
			p.HooksPending = false
			prs[key] = p
			return t.encodePRs(data, prs)
		}); err != nil {
			log.Error("[prtracker] failed to write state", "pr_id", prID, "error", err)
		}
	}
	now := t.clock()
	if now.Before(pr.NextFinishAt) {
		return &pr
	}

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
	err = t.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		prs := t.extractPRs(data)
		key := strconv.Itoa(prID)
		p, ok := prs[key]
		attempts, gaveUp, kept = 0, false, false
		if !ok {
			return nil
		}
		if finishErr == nil {
			delete(prs, key)
			return t.encodePRs(data, prs)
		}
		p.FinishAttempts++
		attempts = p.FinishAttempts
		if attempts >= maxTries {
			gaveUp = true
			p.GaveUp = true
			p.GaveUpAt = now
			p.NextFinishAt = time.Time{}
		} else {
			kept = true
			p.NextFinishAt = now.Add(FinishRetryGap)
		}
		prs[key] = p
		return t.encodePRs(data, prs)
	})
	if err != nil {
		log.Error("[prtracker] failed to write state", "pr_id", prID, "error", err)
		return &pr
	}
	switch {
	case gaveUp:
		log.Error(GaveUpLogMessage,
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
	return &pr
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
	gp := t.gitProvider()
	if gp == nil {
		return nil, fmt.Errorf("%w", ErrGitProviderUnavailable)
	}

	gone := false
	status, err := gp.GetPullRequestStatus(ctx, prID)
	if err != nil {
		// Definitive not-found (HTTP 404): the PR is gone from the provider
		// (deleted, or repo recreated). It is handled as closed, so its
		// finish step still runs (EP3 dry run round S, S10). Every other
		// error stays transient — surface it so the caller retries later.
		if !errors.Is(err, gitprovider.ErrPullRequestNotFound) {
			return nil, fmt.Errorf("poll PR #%d: %w: %w", prID, ErrPRPollUpstream, err)
		}
		status, gone = "closed", true
	}

	if isTerminal(status) {
		claimedPR, found, err := t.handleTerminal(ctx, prID, status, gone)
		if err != nil {
			return nil, err
		}
		if claimedPR != nil {
			return claimedPR, nil
		}
		if !found {
			if gone {
				// Not tracked and gone: nothing to do and nothing to report.
				return nil, nil
			}
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
