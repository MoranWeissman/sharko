package notifications

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/logging"
)

// The alert titles. These are DISPLAY CONTENT and nothing else — reword any
// of them freely. Nothing keys on them: the Store deduplicates by ID,
// resolves by Code, the poller tracks its open alert by Code, and the browser
// routes by Code. Each title's identifier is the Code declared next to it in
// codes.go.
//
// They used to be the contract — the store's dedup key, the store's resolve
// key, and the string the browser compared to decide which connection panel
// got the detail. Capitalising one letter here would have blanked a panel
// with nothing failing anywhere. That is what codes.go exists to end.
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
	// TitleArgoForbidden is raised when ArgoCD rejects the LIST with a 403:
	// the token is valid but lacks permission to read applications. This is
	// a permission problem, not a repo-sync problem — distinct from
	// TitleArgoRepoBroken, which implies ArgoCD looked at the repo and found
	// it broken (review findings r1, H1).
	TitleArgoForbidden = "ArgoCD refused Sharko's token permission"
)

// The lead sentence of each alert's description — the half that says WHICH
// thing is broken and why it matters. The other half says what KIND of failure
// it was and comes from reasonSentences, chosen by the probe's Reason.
//
// These used to be typed out at the call site in cmd/sharko/serve.go and passed
// in as a string, alongside the raw `detail` string this story removed. They
// live here now for the same reason the titles do: a description that is
// persisted into a ConfigMap has to be owned by a catalog, not assembled by
// whoever happened to be holding an error. There is no lead parameter any more.
const (
	// LeadGitConnectionBroken — Sharko's own Git connection, the one every
	// commit and pull request goes through.
	LeadGitConnectionBroken = "Sharko uses this Git connection for every commit and pull request, and right now it can't reach it."
	// LeadArgoRepoBroken — ArgoCD looked at the repo and cannot sync it.
	LeadArgoRepoBroken = "ArgoCD can't sync from the repo right now, so cluster changes won't roll out until this is fixed."
	// LeadArgoAuthFailed — ArgoCD rejected the token Sharko authenticates with.
	LeadArgoAuthFailed = "ArgoCD rejected the token Sharko uses to check on the bootstrap app, so Sharko can't confirm the cluster is in sync."
	// LeadArgoUnreachable — no usable answer from ArgoCD at all.
	LeadArgoUnreachable = "Sharko couldn't get an answer from ArgoCD, so it can't confirm whether the cluster is in sync."
	// LeadArgoForbidden — the token is valid but lacks permission.
	LeadArgoForbidden = "ArgoCD refused Sharko's token permission to read applications, so Sharko can't confirm the cluster is in sync."
)

// connectionLeads maps each connection code to its lead sentence. It is read by
// descriptionFor, which render.go calls for every connection alert — so these
// are the only words that can appear in one.
//
// A connection code missing from this map is caught BY NAME by
// TestReason_EveryConnectionCodeHasALead.
var connectionLeads = map[Code]string{
	CodeGitConnectionBroken: LeadGitConnectionBroken,
	CodeArgoRepoBroken:      LeadArgoRepoBroken,
	CodeArgoAuthFailed:      LeadArgoAuthFailed,
	CodeArgoUnreachable:     LeadArgoUnreachable,
	CodeArgoForbidden:       LeadArgoForbidden,
}

// descriptionFor builds the description of a connection alert from two enums
// and nothing else.
//
// It is a pure function: same code and reason in, same sentences out, no error
// consulted and no state read. That is what makes Store.Add's re-derivation
// idempotent without anybody comparing message text — running it twice runs the
// same two map lookups twice.
//
// A code with no lead (not a connection alert) yields the reason sentence on
// its own rather than an empty description.
func descriptionFor(code Code, reason Reason) string {
	sentence := reason.sentence()
	lead, ok := connectionLeads[code]
	if !ok {
		return sentence
	}
	return lead + " " + sentence
}

// DefaultConnectionCheckInterval is how often the poller probes both
// connections. Connection health needs faster feedback than the 30-minute
// version/drift Checker, so it runs on its own short cadence.
const DefaultConnectionCheckInterval = 60 * time.Second

// HealthResult is the outcome of probing one connection.
//
// determined is false when there is nothing to probe yet (no active
// connection configured). In that case healthy/reason are ignored and the
// poller leaves the connection's last-known state untouched — it does not
// raise a false "broken" alert just because nothing is set up.
// code defaults to empty, meaning "use the default passed to evaluate()". A
// health-check closure that can distinguish more than one kind of failure
// (e.g. argoHealthFn distinguishing "ArgoCD rejected our token" from "ArgoCD
// can't sync the repo") sets it via UnhealthyResultWithCode, and the title
// follows from the code — the probe does not get to choose the words.
//
// THERE IS NO FREE-TEXT FIELD HERE AT ALL, AND THERE MUST NEVER BE ONE.
// This struct used to carry `detail string`, and both of Sharko's health
// probes filled it with a backend's own error text — which the store then
// wrote into the sharko-notifications ConfigMap, served on every
// GET /notifications, and restored on the next restart. It then carried
// `title string` for longer: caller-composed prose, persisted the same way and
// shown on the bell, with nothing checking it. Both are gone. The failure is
// described by `reason`, an enum; which alert it is by `code`, an enum; and
// every word a person reads is written by the server from those two (render.go).
// TestHealthResult_HasNoTextChannel fails on any string-typed field added back.
type HealthResult struct {
	determined bool
	healthy    bool
	reason     Reason
	code       Code
}

// HealthyResult builds a determined-healthy result.
func HealthyResult() HealthResult { return HealthResult{determined: true, healthy: true} }

// UnhealthyResult builds a determined-unhealthy result whose failure is
// described by a Reason. Uses the default code evaluate() was called with, and
// the title and lead sentence that belong to that code.
//
// It used to take the failure as a STRING, and every caller filled it with an
// error's own words. There is no parameter for raw text to arrive through any
// more: reach for ClassifyReason(err) at the point where the error is still
// alive and pass the answer.
func UnhealthyResult(reason Reason) HealthResult {
	return HealthResult{determined: true, healthy: false, reason: reason}
}

// UnhealthyResultWithCode builds a determined-unhealthy result that overrides
// the default alert code. Use this when a health-check closure can distinguish
// more than one kind of failure and the generic one would mislabel the problem
// (e.g. a credential rejection is not the same as "can't sync the repo").
//
// The code is the only thing a probe chooses: it is what the store, the poller
// and the browser act on, and it is what picks BOTH the title and the lead
// sentence out of the server's own table. The reason picks the sentence that
// says what kind of failure this was.
//
// It used to take a title as well, and every caller passed a string. That
// string went straight onto a record that gets persisted into the
// sharko-notifications ConfigMap and shown on the bell, with nothing checking
// it — the same shape as the `detail` leak, one field over. There is no
// parameter for words any more.
func UnhealthyResultWithCode(code Code, reason Reason) HealthResult {
	return HealthResult{determined: true, healthy: false, reason: reason, code: code}
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

	// Code of the alert currently open for each connection, so a later
	// healthy transition resolves the SAME alert even when a health closure
	// raises a different kind of failure across checks (e.g. argoHealthFn
	// switching between "can't sync the repo" and "rejected Sharko's token"
	// without ever going healthy in between). Empty when no alert is
	// currently open for that connection.
	//
	// This used to remember the TITLE. Recovery then depended on the exact
	// sentence matching what was raised, so rewording it would have left the
	// alert stuck on the bell with the connection healthy.
	gitLastCode  Code
	argoLastCode Code

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
		&p.gitLastCode,
		CodeGitConnectionBroken,
	)
	p.evaluate(
		ctx,
		p.argoHealthFn,
		&p.argoHealthy,
		&p.argoLastCode,
		CodeArgoRepoBroken,
	)
}

// evaluate probes one connection and reconciles the bell against the
// transition. It acts on edges, PLUS one more case (review findings r1,
// L12):
//   - healthy → unhealthy         : Add the alert
//   - unhealthy → healthy         : Resolve the alert
//   - unhealthy → unhealthy,
//     code changed                : Resolve the stale alert, Add the new one
//   - no change (same health AND
//     same code)                  : do nothing (no re-nag, survives mark-read)
//   - can't determine             : no-op, last-known state untouched
//
// The middle case used to fall through the "no change" no-op: a health
// closure that reclassifies WHY a connection is unhealthy without it ever
// recovering in between (e.g. argoHealthFn moving from "ArgoCD can't sync
// the repo" to "ArgoCD rejected Sharko's token") never updated the bell —
// the first classification sat there, actively wrong, until the connection
// either recovered or the process restarted.
//
// That reclassification check now compares CODES, not titles. A different
// problem has a different code, which is what "different problem" means here;
// rewording a sentence is not a different problem and no longer looks like
// one.
//
// defaultCode is used unless the probe's HealthResult supplies its own (via
// UnhealthyResultWithCode) — a health closure that can distinguish more than
// one kind of failure (e.g. argoHealthFn separating "ArgoCD rejected our token"
// from "ArgoCD can't sync the repo") overrides per-result so the alert
// identifies the right problem. lastCode records which alert is currently open
// for this connection so a later healthy transition (or a reclassification)
// resolves that SAME alert.
//
// There is no lead parameter, no detail parameter and no title parameter. The
// title, the description and the ID are all written by the server from the code
// (render.go), so a probe cannot put a backend's own words — or any words —
// into a record that gets persisted.
func (p *ConnectionPoller) evaluate(
	ctx context.Context,
	probe func(ctx context.Context) HealthResult,
	last **bool,
	lastCode *Code,
	defaultCode Code,
) {
	res := probe(ctx)
	if !res.determined {
		// Nothing to probe (no active connection). Don't invent a "broken"
		// alert and don't disturb the last-known state.
		return
	}

	code := defaultCode
	if res.code != "" {
		code = res.code
	}

	prev := *last
	sameHealthState := prev != nil && *prev == res.healthy
	// Healthy has no alert to compare — only an unhealthy→unhealthy
	// reclassification needs the code check to detect a real change.
	codeUnchanged := res.healthy || code == *lastCode
	if sameHealthState && codeUnchanged {
		// No transition, and (if unhealthy) no reclassification either —
		// nothing to do. This is what prevents re-adding on every tick and
		// re-adding after the user marks the alert read.
		return
	}

	if res.healthy {
		if prev != nil && *lastCode != "" { // only resolve if we'd previously flagged a break
			p.store.Resolve(*lastCode)
		}
		*lastCode = ""
	} else {
		// Unhealthy while already unhealthy, but a different problem —
		// resolve the stale alert first so the bell never shows two open
		// alerts for the same connection.
		if sameHealthState && *lastCode != "" && *lastCode != code {
			p.store.Resolve(*lastCode)
		}
		// The poller supplies two enums and a clock, and nothing else. The
		// title, the description and the ID are all written by the server from
		// the code (render.go): the code picks the title and the lead sentence,
		// the reason picks the sentence that says what kind of failure it was.
		//
		// The description used to be built here as
		//
		//	lead + " Reason: " + res.detail
		//
		// with res.detail holding a backend's own error text, and the title was
		// still passed in from the probe long after that was fixed. Both are
		// gone. The ID stays "connection-<code>": stable rather than
		// per-occurrence, because there is at most one open alert per code, and
		// a stable ID is what lets Store.Add deduplicate the re-raise on every
		// tick.
		p.store.Add(New(code, res.reason, Params{}, time.Now()))
		// The log line carries the two enums, not a failure message. The real
		// cause is logged where the error is still alive — in the health-probe
		// closures in cmd/sharko/serve.go — and stops there.
		logging.LoggerFromContext(ctx).Warn("connection health degraded",
			"code", code.String(), "reason", res.reason.String(), "component", "notifications")
		*lastCode = code
	}

	healthy := res.healthy
	*last = &healthy
}
