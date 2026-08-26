package api

// connection_credential_check.go — W3-3: the fleet page notices credential
// drift by itself; repair stays a human's click.
//
// # What this is
//
// A slow background loop that runs the SAME read-only comparison the
// Check-again button drives (compareClusterConnection in
// connection_comparison.go) over every managed cluster, and a per-cluster
// in-memory store both that loop and the manual endpoint write into. The
// fleet rows (connectionSecretRow in system_managed_secrets.go) read the
// store, so a person learns about credential drift from the fleet page
// without opening each connection.
//
// # Why it is NOT in internal/clusterreconciler
//
// The reconciler's own design note
// (internal/clusterreconciler/connection_drift_notice.go) explains why its
// 30-second tick must never read the credentials backend: a stored-facts
// read per cluster per tick is hundreds of thousands of backend reads a day
// for a question a person asks a few times a week. This loop runs on its own
// slow interval instead (SHARKO_CONNECTION_CREDENTIAL_CHECK_INTERVAL,
// default 15 minutes), in this package, where the comparison assembly, the
// fleet rows and the admin gate already live.
//
// # Read-only, inherited — and never the write path's builder
//
// Detection never writes or repairs credentials, never enables self-heal,
// and never writes git: the loop calls compareClusterConnection and nothing
// else, and that core is read-only by construction. It NEVER calls
// clusterreconciler.ConnectionCredentialSpecForWrite — that is the write
// path's builder and can mint an EKS sign-in token. A background pass over
// an EKS cluster mints ZERO tokens (pinned by
// TestConnectionCredentialCheckLoop_EKS_NotComparedAndZeroMint).
//
// # Nothing derived from a value, ever
//
// The store holds a status word, a timestamp, the fixed sentences the API
// already ships, and — since B5 — the canonical reconciliation answer for
// this connection (mode, owned scope, sync state, verification scope,
// approval requirement, headline, qualifier). All of it is enum words, one
// bool and fixed literals. No value, no length, no hash, no fragment, no
// field path — the field-level detail stays on the connection page, behind
// the same read gate as today.
//
// # One derivation, two surfaces (B5)
//
// record() is already handed the entire finished comparison for every
// managed cluster: on every loop pass, and immediately whenever anyone opens
// a connection page or clicks Check. So the canonical answer is computed
// HERE, once, by the same pure function the connection page's response is
// built from — and the fleet row reads the result instead of deriving a
// second, addon-label-only verdict of its own. That costs no extra I/O: the
// mapping is pure, and this file's loop already ran the only reads involved.
//
// # Transition-only reporting
//
// Four transitions, each written once per episode: drift appears, drift
// clears, the check starts failing, the check completes again. Never
// repeated while unchanged, and the loop never writes the per-check audit
// entry the human endpoint writes — Recent activity must not flood every
// interval. No Kubernetes events (the reconciler's shape notice owns that
// channel), no email, no webhook.
//
// # Every entry here says what it means (ruling f, 2026-08-19)
//
// Finding drift is a check that SUCCEEDED. It used to be recorded as
// Result: "failure" on an entry whose own detail said "Nothing was changed",
// while a check that genuinely did not finish wrote no entry at all — so the
// only outcome word this surface produced was a "failure" that had not
// happened, and the one real failure was invisible. Now: detection is
// success with a warn level (the drift state supplies the warning), a real
// failure has a failure-shaped title AND a failure outcome, and every entry
// carries Changes: not_applicable, because a read-only check neither changed
// anything nor failed to.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/lifecycleevents"
)

// DefaultConnectionCredentialCheckInterval is how often the background loop
// re-checks every managed cluster's connection against its configured
// credentials source when SHARKO_CONNECTION_CREDENTIAL_CHECK_INTERVAL is not
// set. Deliberately slow: each pass is a git read, a Secret read and at most
// one secrets-backend read per cluster.
const DefaultConnectionCredentialCheckInterval = 15 * time.Minute

// The boot-window retry (B13 item 5).
//
// WHY IT EXISTS. run() has always fired one pass immediately, before
// creating the ticker. But that pass returns early and silently when the
// cluster reconciler, the hub client, the git provider or the ArgoCD client
// is not up yet — which at boot is normal, because those are wired in the
// same startup sequence. There was no retry, so the next attempt was a FULL
// interval away: fifteen minutes of every fleet row reading "Not checked
// yet" because Sharko asked once, a second too early, and then waited.
//
// The retry covers the boot window and nothing else. Only a not-ready
// answer is retried — a check that ran and failed is a real failure and
// waits for the next tick like any other, because retrying a broken backend
// every five seconds is how a slow loop becomes a fast one. The steady-state
// interval is untouched: these delays only run before the ticker starts.
const (
	// checkLoopFirstRetryDelay is the first backoff step; each retry doubles
	// it up to checkLoopMaxRetryDelay, and the delay is additionally capped
	// at the loop's own interval so a short interval never waits longer than
	// a normal tick.
	checkLoopFirstRetryDelay = 5 * time.Second
	checkLoopMaxRetryDelay   = 2 * time.Minute
	// checkLoopMaxBootRetries bounds the whole thing: 5s+10s+20s+40s+80s+120s
	// is a little under five minutes, comfortably inside one default
	// interval, after which the ticker is the only schedule again.
	checkLoopMaxBootRetries = 6
)

// The plain sentences for "background checks are not running, and here is
// why". Fixed literals, like every other sentence this feature ships — never
// a Go error, never provider text. Two of them are the comparison's own
// existing refusal sentences (connection_comparison.go), reused verbatim so
// the fleet page and a connection page give one answer to the same question.
const (
	// checkLoopNotScheduled is the out-of-cluster case, and it is the one
	// this item is really about: cmd/sharko/serve.go starts the loop only
	// inside its in-cluster branch, so a server running outside a cluster
	// schedules no check at all and every row reads "Not checked yet"
	// forever.
	checkLoopNotScheduled = "This server is not running the background connection check, so a connection is only checked when someone opens its page or asks for a check."
	// checkLoopNotRunYet covers the moment between the loop starting and its
	// first pass finishing.
	checkLoopNotRunYet = "The background connection check has not finished a pass yet on this server."
	// checkLoopNoReconciler — the cluster reconciler is not wired on this
	// server, so there is nothing to read the git-declared connections from.
	//
	// The SENTENCE names no machinery (product ruling, 2026-08-19:
	// information text speaks in the user's terms). It says what this server
	// does not do and what that costs the reader, not which component is
	// missing. The comment is free to name the reconciler; the sentence a
	// person reads is not.
	checkLoopNoReconciler = "This server is not set up to keep cluster connections in step with Git, so the background connection check has nothing to compare against."
	// checkLoopNoArgoCD — the cluster list needs ArgoCD.
	checkLoopNoArgoCD = "Sharko is not connected to ArgoCD right now, so the background connection check cannot list the clusters to check."
	// checkLoopClusterListFailed is a real failure of a pass that DID start,
	// not a missing dependency — so it is reported and deliberately not
	// retried early.
	checkLoopClusterListFailed = "Sharko could not read the cluster list on the last background pass, so no connection was checked."
)

// checkLoopPass is what one attempted pass reports back.
//
// Ready separates "a dependency was not up" from "the pass ran". Only the
// first is retried, and only the first is a boot-window problem. Reason is
// the fixed sentence for a pass that checked nothing, whichever kind it
// was — empty exactly when the pass really walked the fleet.
type checkLoopPass struct {
	Ready  bool
	Reason string
}

// connectionCheckStatus is the server-wide answer to "are background
// connection checks running, and if not, why". Guarded by a mutex; one
// writer (the loop) and one reader (the managed-secrets handler).
type connectionCheckStatus struct {
	mu sync.Mutex
	// scheduled is true once a loop has been started on this server at all.
	// False is the out-of-cluster answer.
	scheduled bool
	interval  time.Duration
	// attempted is true once any pass has been attempted.
	attempted   bool
	lastAttempt time.Time
	ready       bool
	reason      string
}

func newConnectionCheckStatus() *connectionCheckStatus {
	return &connectionCheckStatus{}
}

// markScheduled records that a loop exists on this server and how often it
// runs.
func (s *connectionCheckStatus) markScheduled(interval time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduled = true
	s.interval = interval
}

// recordPass stores the outcome of one attempted pass.
func (s *connectionCheckStatus) recordPass(at time.Time, pass checkLoopPass) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempted = true
	s.lastAttempt = at
	s.ready = pass.Ready
	s.reason = pass.Reason
}

// connectionCheckStatusView is the wire shape. It is on the managed-secrets
// response because that is the page where "Not checked yet" appears on every
// row.
type connectionCheckStatusView struct {
	// Running is true only when the last attempted pass really checked the
	// fleet. It is deliberately not "a loop object exists": a loop that skips
	// every pass because git is down is not running checks, whatever its
	// goroutine is doing.
	Running bool `json:"running"`
	// Reason is the plain sentence for why checks are not running. Empty
	// exactly when Running is true.
	Reason string `json:"reason,omitempty"`
	// IntervalSeconds is the loop's cadence, 0 when no loop is scheduled at
	// all on this server.
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	// LastAttempt is RFC3339 of the last pass the loop tried, absent when it
	// has never tried one. ABSENT, never a zero time — the W3-6 rule.
	LastAttempt string `json:"last_attempt,omitempty"`
}

// snapshot renders the current answer. Nil-safe: a Server whose status was
// never wired reports the same thing as a server with no loop, which is the
// truth in both cases.
func (s *connectionCheckStatus) snapshot() connectionCheckStatusView {
	if s == nil {
		return connectionCheckStatusView{Reason: checkLoopNotScheduled}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := connectionCheckStatusView{IntervalSeconds: int(s.interval / time.Second)}
	if !s.scheduled {
		out.Reason = checkLoopNotScheduled
		return out
	}
	if s.attempted {
		out.LastAttempt = s.lastAttempt.UTC().Format(time.RFC3339)
	}
	switch {
	case !s.attempted:
		out.Reason = checkLoopNotRunYet
	case s.ready && s.reason == "":
		out.Running = true
	default:
		out.Reason = s.reason
		if out.Reason == "" {
			out.Reason = checkLoopNotRunYet
		}
	}
	return out
}

// The four credential-check outcomes a fleet row can carry.
const (
	// credentialCheckDrifted — the comparison looked and found a real
	// difference (or the connection is missing, or another tool owns it).
	// Manual repair is required; nothing was changed.
	credentialCheckDrifted = "drifted"
	// credentialCheckClear — every field inside a FULL scope was checked
	// and matched. Only a full-scope comparison can earn this word.
	credentialCheckClear = "clear"
	// credentialCheckNotCompared — the comparison honestly could not
	// compare the credential half (EKS, inline kubeconfig, an unrecorded
	// source) and found nothing wrong in the part it could check. The
	// detail carries the comparison's own established limit sentence.
	credentialCheckNotCompared = "not_compared"
	// credentialCheckFailed — the check itself did not finish. Never
	// softened into drift and never into health.
	credentialCheckFailed = "check_failed"
)

// credentialDriftNotice is the fixed drifted sentence, exact wording from
// the product owner's ruling. It identifies no field, no value, no length,
// no hash — it names the situation and the human next step, nothing else.
const credentialDriftNotice = "This connection's stored details no longer match its configured credentials source. Nothing was changed. An admin can review and repair it from the connection page."

// credentialDriftClearedNotice is the transition sentence for the audit
// entry written when a drift episode ends.
const credentialDriftClearedNotice = "This connection's check no longer reports credential drift."

// credentialCheckRecoveredNotice closes an open check-FAILURE episode: the
// check is finishing again, whatever it now finds.
const credentialCheckRecoveredNotice = "This connection's check is completing again."

// credentialCheckFromView maps one finished comparison onto the store's
// status word and fixed sentence.
//
// THE RULE, WRITTEN DOWN: "drifted" requires something the comparison
// actually REPORTED — a checked-field difference (out_of_sync), a connection
// that is not there at all (missing), or a connection another tool owns
// (ownership_conflict). A limited-scope comparison (EKS, inline kubeconfig,
// unrecorded source) whose only gaps are NotChecked territory has no
// credential difference to claim, so a clean limited pass stores
// not_compared with the comparison's own established limit sentence — never
// drifted, never clear ("I found no problem in the part I looked at" is not
// "this is right"). But a limited comparison that DID find a real difference
// in a checked, non-sensitive field is honestly drifted: the comparison
// reported it, at any scope. check_failed stays check_failed, with the
// comparison's safe classified sentence — a backend hiccup never reads as
// drift or as health. Pinned by TestCredentialCheckFromView_MappingRule.
func credentialCheckFromView(view connectionComparisonView) (status, detail string) {
	switch connectioncompare.Status(view.Status) {
	case connectioncompare.StatusSynced:
		return credentialCheckClear, ""
	case connectioncompare.StatusLimited:
		return credentialCheckNotCompared, view.LimitReason
	case connectioncompare.StatusOutOfSync, connectioncompare.StatusMissing, connectioncompare.StatusOwnershipConflict:
		return credentialCheckDrifted, credentialDriftNotice
	default:
		// StatusCheckFailed, and — fail closed — any status this mapping
		// does not recognise: a check Sharko cannot classify is a check
		// that did not finish, never drift and never health.
		return credentialCheckFailed, view.FailureReason
	}
}

// connectionCredentialCheckRecord is one cluster's last check outcome. None
// of it is ever derived from a credential value.
type connectionCredentialCheckRecord struct {
	// Status is one of the four credentialCheck* words above.
	Status string
	// Detail is the fixed sentence for that status ("" for clear).
	Detail string
	// CheckedAt is when the check ran, RFC3339 — copied from the view that
	// produced it, so the store and the connection page can never disagree
	// about when Sharko looked.
	CheckedAt string
	// Canonical (B5) is the FULL canonical reconciliation answer for this
	// connection — the same struct the connection page's own response is a
	// projection of, computed by the same pure function from the same
	// finished comparison. The fleet row reads it verbatim.
	//
	// This is what makes "one canonical derivation, shared by both
	// surfaces" literally true. record() already holds the whole finished
	// comparison view and used to throw all but three fields of it away;
	// keeping the mapped answer costs one pure function call and no I/O at
	// all — no Kubernetes read, no git read, no secrets-backend read, no
	// ArgoCD call. It is safe to compute here precisely because
	// connectionCanonicalStateFor needs neither an ArgoCD client nor the
	// settings store (see connection_canonical.go).
	//
	// It carries no value, no length, no hash, no fragment and no field
	// path: it is status words, enum words, a bool, and the fixed sentences
	// the API already ships.
	Canonical connectionCanonicalState
}

// connectionCredentialCheckStore holds the per-cluster outcomes, guarded by
// a mutex — the same in-memory stance the reconciler takes for its own
// records. Both writers (the background loop and the manual endpoint) go
// through record(), which is also where the transition-only audit entry is
// decided, so "one entry per drift episode" holds no matter who noticed.
type connectionCredentialCheckStore struct {
	mu      sync.Mutex
	records map[string]connectionCredentialCheckRecord
	// checkFailing tracks the open CHECK-FAILURE episode per cluster, the
	// same transition-only shape driftAnnounced uses below and for the same
	// reason: a backend that is down must not write an entry every pass.
	// It is separate from driftAnnounced because the two say different
	// things — "Sharko looked and found a difference" versus "Sharko could
	// not look" — and a check that could not look must not disturb an open
	// drift episode in either direction.
	checkFailing map[string]bool
	// driftAnnounced tracks the open drift EPISODE per cluster, separately
	// from the last check's status. It opens when drift is first seen
	// (one "detected" entry), closes when a later check answers clear or
	// not_compared (one "cleared" entry) — and a check_failed in between
	// changes NOTHING here: a check that could not look can neither clear
	// a drift nor re-announce it, and without this a flapping backend
	// would re-detect the same drift on every recovery.
	driftAnnounced map[string]bool
	// auditFn is audit.Log.Add. The entries written here carry only the
	// fixed sentences above — no raw error ever reaches this boundary,
	// because the comparison already classified every failure into a safe
	// literal. There is nothing to sanitise on the way past either way:
	// an entry's Error is a SafeText that only the audit sink can build,
	// and the sink rebuilds it from the entry's Reason every time.
	auditFn func(audit.Entry)
}

func newConnectionCredentialCheckStore(auditFn func(audit.Entry)) *connectionCredentialCheckStore {
	return &connectionCredentialCheckStore{
		records:        make(map[string]connectionCredentialCheckRecord),
		driftAnnounced: make(map[string]bool),
		checkFailing:   make(map[string]bool),
		auditFn:        auditFn,
	}
}

// record stores one finished comparison's outcome for a cluster and writes
// the transition-only audit entry when — and only when — this outcome opens
// or closes a drift episode.
func (st *connectionCredentialCheckStore) record(cluster string, view connectionComparisonView) {
	if st == nil {
		return
	}
	status, detail := credentialCheckFromView(view)

	// The canonical answer, from the same pure function the connection page
	// uses, over the same finished comparison. Pure — no I/O of any kind.
	canonical := connectionCanonicalStateFor(view)

	st.mu.Lock()
	st.records[cluster] = connectionCredentialCheckRecord{
		Status:    status,
		Detail:    detail,
		CheckedAt: view.CheckedAt,
		Canonical: canonical,
	}
	announced := st.driftAnnounced[cluster]
	failed := st.checkFailing[cluster]
	var announce, clear, failing, recovered bool
	switch status {
	case credentialCheckDrifted:
		if !announced {
			st.driftAnnounced[cluster] = true
			announce = true
		}
	case credentialCheckClear, credentialCheckNotCompared:
		if announced {
			delete(st.driftAnnounced, cluster)
			clear = true
		}
	case credentialCheckFailed:
		// Ruling (f): a check that genuinely did not finish must be VISIBLE.
		// It used to write nothing at all, so the only outcome word this
		// surface ever produced was the "failure" on a check that had in
		// fact succeeded — the one real execution failure was silent.
		//
		// Transition-only, exactly like the drift episode above and for the
		// same reason: a backend that is down for an hour must not write
		// four entries a minute. The drift episode is deliberately left
		// alone here — a check that could not look can neither clear a
		// drift nor re-announce it.
		if !failed {
			st.checkFailing[cluster] = true
			failing = true
		}
	}
	if status != credentialCheckFailed && failed {
		delete(st.checkFailing, cluster)
		recovered = true
	}
	st.mu.Unlock()

	if st.auditFn == nil {
		return
	}
	if recovered {
		st.auditFn(audit.Entry{
			Level:    "info",
			Event:    lifecycleevents.ConnectionCredentialCheckRecovered,
			User:     "sharko",
			Action:   "credential_check",
			Resource: "cluster:" + cluster,
			Source:   "server",
			Result:   "success",
			Changes:  audit.ChangesNotApplicable,
			Detail:   credentialCheckRecoveredNotice,
		})
	}
	switch {
	case announce:
		// RULING (f), the primary target. This check SUCCEEDED — it ran end
		// to end and correctly found a difference. It used to record
		// Result: "failure" on an entry whose own detail said "Nothing was
		// changed", so the title, the outcome and the change line all
		// disagreed. Successfully detecting drift is a successful check
		// with an attention result; the drift state itself supplies the
		// warning, which is what the warn level is for. Nothing was written
		// and nothing was going to be — the check is read-only.
		st.auditFn(audit.Entry{
			Level:    "warn",
			Event:    lifecycleevents.ConnectionCredentialDriftDetected,
			User:     "sharko",
			Action:   "credential_check",
			Resource: "cluster:" + cluster,
			Source:   "server",
			Result:   "success",
			Changes:  audit.ChangesNotApplicable,
			Detail:   credentialDriftNotice,
		})
	case clear:
		st.auditFn(audit.Entry{
			Level:    "info",
			Event:    lifecycleevents.ConnectionCredentialDriftCleared,
			User:     "sharko",
			Action:   "credential_check",
			Resource: "cluster:" + cluster,
			Source:   "server",
			Result:   "success",
			Changes:  audit.ChangesNotApplicable,
			Detail:   credentialDriftClearedNotice,
		})
	case failing:
		// A failure-shaped title for a genuinely failed check. The detail is
		// the comparison's own safe classified sentence — never a live
		// backend error.
		st.auditFn(audit.Entry{
			Level:    "error",
			Event:    lifecycleevents.ConnectionCredentialCheckFailed,
			User:     "sharko",
			Action:   "credential_check",
			Resource: "cluster:" + cluster,
			Source:   "server",
			Result:   "failure",
			Changes:  audit.ChangesNotApplicable,
			Detail:   detail,
		})
	}
}

// get returns one cluster's last recorded outcome. Nil-safe: a server whose
// store was never wired simply has no records.
func (st *connectionCredentialCheckStore) get(cluster string) (connectionCredentialCheckRecord, bool) {
	if st == nil {
		return connectionCredentialCheckRecord{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.records[cluster]
	return rec, ok
}

// ConnectionCredentialCheckLoop is the background half: one goroutine, one
// slow ticker, calling the exact per-cluster core the manual endpoint calls.
// Mirrors the project's reconciler lifecycle shape — sync.Once Start,
// idempotent Stop, per-instance interval (no package-level state).
type ConnectionCredentialCheckLoop struct {
	srv       *Server
	interval  time.Duration
	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}

	// The boot-window retry knobs and the one test seam, PER INSTANCE — the
	// project's rule, so nothing here races when tests run in parallel.
	firstRetryDelay time.Duration
	maxRetryDelay   time.Duration
	maxBootRetries  int
	// runOnceFn is what run() calls for a pass. It defaults to l.runOnce.
	//
	// IT EXISTS SO THE IMMEDIATE FIRST PASS IS TESTABLE. Every test drove
	// runOnce directly, so deleting the immediate call in run() broke
	// nothing at all — the one line that decides whether a fresh server
	// says anything for its first fifteen minutes had no test on it.
	runOnceFn func(context.Context) checkLoopPass
}

// NewConnectionCredentialCheckLoop builds the loop. A non-positive interval
// falls back to the default — the caller (cmd/sharko/serve.go) parses the
// env var and logs its own warning on a bad value.
func NewConnectionCredentialCheckLoop(srv *Server, interval time.Duration) *ConnectionCredentialCheckLoop {
	if interval <= 0 {
		interval = DefaultConnectionCredentialCheckInterval
	}
	l := &ConnectionCredentialCheckLoop{
		srv:             srv,
		interval:        interval,
		stop:            make(chan struct{}),
		firstRetryDelay: checkLoopFirstRetryDelay,
		maxRetryDelay:   checkLoopMaxRetryDelay,
		maxBootRetries:  checkLoopMaxBootRetries,
	}
	l.runOnceFn = l.runOnce
	return l
}

// Start launches the single goroutine. Safe to call more than once; only the
// first call does anything.
func (l *ConnectionCredentialCheckLoop) Start(ctx context.Context) {
	l.startOnce.Do(func() {
		// Recorded BEFORE the goroutine starts, so a request that arrives in
		// the same millisecond reads "scheduled, no pass finished yet"
		// rather than "this server runs no background check" — which would
		// be a different and wrong statement.
		if l.srv != nil {
			l.srv.connCheckStatus.markScheduled(l.interval)
		}
		go l.run(ctx)
	})
}

// Stop ends the loop. Idempotent.
func (l *ConnectionCredentialCheckLoop) Stop() {
	l.stopOnce.Do(func() { close(l.stop) })
}

func (l *ConnectionCredentialCheckLoop) run(ctx context.Context) {
	// One pass right away: a fleet page that says nothing for a whole
	// interval after boot helps nobody.
	//
	// A pass whose dependencies are not up yet used to "skip harmlessly",
	// which was the wrong word for it — the next attempt was a full interval
	// later, so asking one second too early during startup cost fifteen
	// minutes of silence. Now a not-ready answer is retried on a short
	// backoff until the dependencies arrive or the boot window is spent.
	if pass := l.pass(ctx); !pass.Ready {
		l.retryWhileNotReady(ctx)
	}

	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.stop:
			return
		case <-ticker.C:
			l.pass(ctx)
		}
	}
}

// pass runs one attempt and publishes what it found, so the fleet page can
// say whether checks are running and why not.
func (l *ConnectionCredentialCheckLoop) pass(ctx context.Context) checkLoopPass {
	out := l.runOnceFn(ctx)
	if l.srv != nil {
		l.srv.connCheckStatus.recordPass(time.Now(), out)
	}
	return out
}

// retryWhileNotReady covers the boot window, and only the boot window.
//
// It retries ONLY a not-ready answer — a dependency that has not come up.
// A pass that ran and failed (the cluster list read, say) is a real failure
// and waits for the next tick like any other; retrying a broken backend
// every few seconds is how a deliberately slow loop turns into a fast one.
// The delay doubles from firstRetryDelay up to maxRetryDelay and is capped
// at the loop's own interval, so a short interval never waits longer than a
// normal tick would.
func (l *ConnectionCredentialCheckLoop) retryWhileNotReady(ctx context.Context) {
	delay := l.firstRetryDelay
	for attempt := 0; attempt < l.maxBootRetries; attempt++ {
		wait := delay
		if wait > l.interval {
			wait = l.interval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-l.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		if l.pass(ctx).Ready {
			return
		}
		delay *= 2
		if delay > l.maxRetryDelay {
			delay = l.maxRetryDelay
		}
	}
}

// runOnce checks every managed cluster once. It enumerates clusters through
// the same ListClusters call the fleet page itself uses, so the loop covers
// exactly the rows a person sees. The manual check still works (and still
// updates the same store) whenever a person asks, whatever this returns.
//
// IT NOW SAYS WHY IT DID NOTHING. Every skip used to be a slog.Debug and a
// bare return, so a server where checks could not run looked exactly like
// one where they ran and found nothing: every fleet row read "Not checked
// yet", the synced count was zero, and no sentence anywhere said why. The
// returned checkLoopPass carries a fixed plain sentence to
// connectionCheckStatus, which the managed-secrets response publishes.
//
// Ready is false ONLY for a dependency that is not up. The cluster-list
// failure below is a pass that started and failed — reported, but not
// retried early.
func (l *ConnectionCredentialCheckLoop) runOnce(ctx context.Context) checkLoopPass {
	s := l.srv
	if s == nil {
		return checkLoopPass{Reason: checkLoopNotScheduled}
	}
	if s.clusterRecon == nil {
		slog.Debug("[connection-credential-check] skipped: cluster reconciler not available")
		return checkLoopPass{Reason: checkLoopNoReconciler}
	}
	if s.clusterRecon.GitProviderForRead() == nil {
		slog.Debug("[connection-credential-check] skipped: no git connection for reads")
		return checkLoopPass{Reason: failNoGitConnection}
	}
	if _, _, ok := s.k8sClientAndNamespace(); !ok {
		slog.Debug("[connection-credential-check] skipped: no hub cluster client")
		return checkLoopPass{Reason: failNoHubClient}
	}
	gp, gpErr := s.connSvc.GetActiveGitProvider()
	if gpErr != nil {
		slog.Debug("[connection-credential-check] skipped: no active git provider")
		return checkLoopPass{Reason: failNoGitConnection}
	}
	ac, acErr := s.connSvc.GetActiveArgocdClient()
	if acErr != nil {
		slog.Debug("[connection-credential-check] skipped: no active ArgoCD client")
		return checkLoopPass{Reason: checkLoopNoArgoCD}
	}
	listResp, listErr := s.clusterSvc.ListClusters(ctx, gp, ac)
	if listErr != nil {
		// The list error's own text is not logged — same caution as the
		// comparison's git-read branch. Ready: the dependencies were all
		// there and the pass really ran, so this waits for the next tick
		// rather than hammering a failing read.
		slog.Warn("[connection-credential-check] could not list clusters")
		return checkLoopPass{Ready: true, Reason: checkLoopClusterListFailed}
	}

	for _, c := range listResp.Clusters {
		if !c.Managed {
			continue
		}
		view, refusal := s.compareClusterConnection(ctx, c.Name)
		if refusal != nil {
			// A dependency vanished mid-pass, or this cluster left the
			// git list between the enumeration above and this check.
			// Either way there is nothing honest to record for the row.
			continue
		}
		s.connCredChecks.record(c.Name, view)
	}
	return checkLoopPass{Ready: true}
}
