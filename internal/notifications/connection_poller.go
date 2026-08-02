package notifications

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/logging"
)

// Stable alert titles. These strings are part of the contract: they are used
// BOTH as the Store.Add dedup key AND as the Store.Resolve key, and the
// frontend (Story 2) keys its rendering off them. Do not change them without
// updating the frontend and the tests in lockstep.
const (
	// TitleGitConnectionBroken is raised when Sharko cannot reach its own Git
	// connection — the one it uses for every commit and pull request.
	TitleGitConnectionBroken = "Sharko can't reach your Git connection"
	// TitleArgoRepoBroken is raised when ArgoCD cannot sync the repo.
	TitleArgoRepoBroken = "ArgoCD can't sync the repo"
	// TitleArgoAuthFailed is raised when ArgoCD rejects the token Sharko
	// uses to check on the bootstrap app. This is a credential problem, not
	// a repo-sync problem — distinct from TitleArgoRepoBroken, which
	// implies ArgoCD looked at the repo and found it broken.
	TitleArgoAuthFailed = "ArgoCD rejected Sharko's token"
	// TitleArgoUnreachable is raised when Sharko could not get a usable
	// answer from ArgoCD at all (not a permission problem, not an invalid
	// token — e.g. a network blip). Distinct from TitleArgoRepoBroken:
	// Sharko never established that anything is actually broken, only that
	// it could not check.
	TitleArgoUnreachable = "Sharko can't reach ArgoCD"
)

// DefaultConnectionCheckInterval is how often the poller probes both
// connections. Connection health needs faster feedback than the 30-minute
// version/drift Checker, so it runs on its own short cadence.
const DefaultConnectionCheckInterval = 60 * time.Second

// HealthResult is the outcome of probing one connection.
//
// determined is false when there is nothing to probe yet (no active
// connection configured). In that case healthy/detail are ignored and the
// poller leaves the connection's last-known state untouched — it does not
// raise a false "broken" alert just because nothing is set up.
// title/lead default to empty, meaning "use the caller's title/lead passed
// to evaluate()". A health-check closure that can distinguish more than one
// kind of failure (e.g. argoHealthFn distinguishing "ArgoCD rejected our
// token" from "ArgoCD can't sync the repo") sets these via
// UnhealthyResultWithTitle so the bell alert names the right problem instead
// of a generic one.
type HealthResult struct {
	determined bool
	healthy    bool
	detail     string
	title      string
	lead       string
}

// HealthyResult builds a determined-healthy result.
func HealthyResult() HealthResult { return HealthResult{determined: true, healthy: true} }

// UnhealthyResult builds a determined-unhealthy result with a plain-English
// reason (e.g. the TestConnection error or the bootstrap-probe detail). Uses
// the default title/lead evaluate() was called with.
func UnhealthyResult(detail string) HealthResult {
	return HealthResult{determined: true, healthy: false, detail: detail}
}

// UnhealthyResultWithTitle builds a determined-unhealthy result that
// overrides the default alert title and lead sentence. Use this when a
// health-check closure can distinguish more than one kind of failure and the
// generic title would mislabel the problem (e.g. a credential rejection is
// not the same as "can't sync the repo").
func UnhealthyResultWithTitle(title, lead, detail string) HealthResult {
	return HealthResult{determined: true, healthy: false, detail: detail, title: title, lead: lead}
}

// UndeterminedResult is used when the connection can't be probed (not
// configured yet). It maps to a no-op for that tick.
func UndeterminedResult() HealthResult { return HealthResult{} }

// ConnectionPoller periodically checks two connections — Sharko→Git and
// ArgoCD→repo — and pushes a notification to the bell when either breaks,
// auto-clearing it when the connection recovers. It is transition-driven: it
// acts on the edge (healthy↔unhealthy) and does not re-nag every tick.
type ConnectionPoller struct {
	store    *Store
	interval time.Duration

	// gitHealthFn probes the Sharko→Git connection.
	gitHealthFn func(ctx context.Context) HealthResult
	// argoHealthFn probes the ArgoCD→repo connection.
	argoHealthFn func(ctx context.Context) HealthResult

	// Last-known health per connection. nil = no prior determination yet.
	gitHealthy  *bool
	argoHealthy *bool

	// Last title used to Add() an unhealthy alert for each connection, so a
	// later healthy transition resolves the SAME alert even when a health
	// closure raises different titles across checks (e.g. argoHealthFn
	// switching between "ArgoCD can't sync the repo" and "ArgoCD rejected
	// Sharko's token" without ever going healthy in between). Empty when no
	// alert is currently open for that connection.
	gitLastTitle  string
	argoLastTitle string

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewConnectionPoller builds a poller. The two health closures are injected so
// the package does not need to import internal/api (where ProbeBootstrapApp
// lives) or internal/service — serve.go, which imports both, builds them and
// hands them in. This keeps the dependency direction clean and the poller
// trivially testable with fakes.
func NewConnectionPoller(
	store *Store,
	interval time.Duration,
	gitHealthFn func(ctx context.Context) HealthResult,
	argoHealthFn func(ctx context.Context) HealthResult,
) *ConnectionPoller {
	if interval <= 0 {
		interval = DefaultConnectionCheckInterval
	}
	return &ConnectionPoller{
		store:        store,
		interval:     interval,
		gitHealthFn:  gitHealthFn,
		argoHealthFn: argoHealthFn,
		stopCh:       make(chan struct{}),
	}
}

// Start launches the background goroutine. It runs one check immediately so a
// problem present at startup surfaces without waiting a full interval, then
// repeats on the configured interval until Stop is called.
func (p *ConnectionPoller) Start() {
	go func() {
		p.check()

		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.check()
			case <-p.stopCh:
				return
			}
		}
	}()
}

// Stop signals the background goroutine to exit. Safe to call multiple times.
func (p *ConnectionPoller) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
}

func (p *ConnectionPoller) check() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = logging.WithRequestID(ctx, fmt.Sprintf("connpoll-%d", time.Now().Unix()))

	p.evaluate(
		ctx,
		p.gitHealthFn,
		&p.gitHealthy,
		&p.gitLastTitle,
		TitleGitConnectionBroken,
		"Sharko uses this Git connection for every commit and pull request, and right now it can't reach it.",
	)
	p.evaluate(
		ctx,
		p.argoHealthFn,
		&p.argoHealthy,
		&p.argoLastTitle,
		TitleArgoRepoBroken,
		"ArgoCD can't sync from the repo right now, so cluster changes won't roll out until this is fixed.",
	)
}

// evaluate probes one connection and reconciles the bell against the
// transition. It acts only on edges:
//   - healthy → unhealthy : Add the titled alert
//   - unhealthy → healthy : Resolve the titled alert
//   - no change           : do nothing (no re-nag, survives mark-read)
//   - can't determine      : no-op, last-known state untouched
//
// defaultTitle/defaultLead are used unless the probe's HealthResult supplies
// its own (via UnhealthyResultWithTitle) — a health closure that can
// distinguish more than one kind of failure (e.g. argoHealthFn separating
// "ArgoCD rejected our token" from "ArgoCD can't sync the repo") overrides
// per-result so the alert names the right problem. lastTitle records which
// title is currently open for this connection so a later healthy transition
// resolves that SAME alert, even if the unhealthy classification changed
// across checks without ever going healthy in between.
func (p *ConnectionPoller) evaluate(
	ctx context.Context,
	probe func(ctx context.Context) HealthResult,
	last **bool,
	lastTitle *string,
	defaultTitle string,
	defaultLead string,
) {
	res := probe(ctx)
	if !res.determined {
		// Nothing to probe (no active connection). Don't invent a "broken"
		// alert and don't disturb the last-known state.
		return
	}

	title := defaultTitle
	if res.title != "" {
		title = res.title
	}
	lead := defaultLead
	if res.lead != "" {
		lead = res.lead
	}

	prev := *last
	if prev != nil && *prev == res.healthy {
		// No transition — nothing to do. This is what prevents re-adding on
		// every tick and re-adding after the user marks the alert read.
		return
	}

	if res.healthy {
		if prev != nil && *lastTitle != "" { // only resolve if we'd previously flagged a break
			p.store.Resolve(*lastTitle)
		}
		*lastTitle = ""
	} else {
		desc := lead
		if res.detail != "" {
			desc = lead + " Reason: " + res.detail
		}
		p.store.Add(Notification{
			ID:          fmt.Sprintf("connection-%s-%d", title, time.Now().UnixNano()),
			Type:        TypeConnection,
			Title:       title,
			Description: desc,
			Timestamp:   time.Now(),
		})
		logging.LoggerFromContext(ctx).Warn("connection health degraded",
			"title", title, "detail", res.detail, "component", "notifications")
		*lastTitle = title
	}

	healthy := res.healthy
	*last = &healthy
}
