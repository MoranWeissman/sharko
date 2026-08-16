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
// The store holds a status word, a fixed sentence and a timestamp. No value,
// no length, no hash, no fragment, no field path — the field-level detail
// stays on the connection page, behind the same read gate as today.
//
// # Transition-only reporting
//
// One audit entry when drift appears, one when it clears. Never repeated
// while unchanged, and the loop never writes the per-check audit entry the
// human endpoint writes — Recent activity must not flood every interval. No
// Kubernetes events (the reconciler's shape notice owns that channel), no
// email, no webhook.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
)

// DefaultConnectionCredentialCheckInterval is how often the background loop
// re-checks every managed cluster's connection against its configured
// credentials source when SHARKO_CONNECTION_CREDENTIAL_CHECK_INTERVAL is not
// set. Deliberately slow: each pass is a git read, a Secret read and at most
// one secrets-backend read per cluster.
const DefaultConnectionCredentialCheckInterval = 15 * time.Minute

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

// connectionCredentialCheckRecord is one cluster's last credential-check
// outcome. Three fields, none of them ever derived from a credential value.
type connectionCredentialCheckRecord struct {
	// Status is one of the four credentialCheck* words above.
	Status string
	// Detail is the fixed sentence for that status ("" for clear).
	Detail string
	// CheckedAt is when the check ran, RFC3339 — copied from the view that
	// produced it, so the store and the connection page can never disagree
	// about when Sharko looked.
	CheckedAt string
}

// connectionCredentialCheckStore holds the per-cluster outcomes, guarded by
// a mutex — the same in-memory stance the reconciler takes for its own
// records. Both writers (the background loop and the manual endpoint) go
// through record(), which is also where the transition-only audit entry is
// decided, so "one entry per drift episode" holds no matter who noticed.
type connectionCredentialCheckStore struct {
	mu      sync.Mutex
	records map[string]connectionCredentialCheckRecord
	// driftAnnounced tracks the open drift EPISODE per cluster, separately
	// from the last check's status. It opens when drift is first seen
	// (one "detected" entry), closes when a later check answers clear or
	// not_compared (one "cleared" entry) — and a check_failed in between
	// changes NOTHING here: a check that could not look can neither clear
	// a drift nor re-announce it, and without this a flapping backend
	// would re-detect the same drift on every recovery.
	driftAnnounced map[string]bool
	// auditFn is audit.Log.Add. The entries written here carry only the
	// fixed sentences above — no raw error ever reaches this boundary
	// (the comparison already classified every failure into a safe
	// literal), so there is no live error to run credsafe.Is on and
	// Entry.CredentialFailure stays false.
	auditFn func(audit.Entry)
}

func newConnectionCredentialCheckStore(auditFn func(audit.Entry)) *connectionCredentialCheckStore {
	return &connectionCredentialCheckStore{
		records:        make(map[string]connectionCredentialCheckRecord),
		driftAnnounced: make(map[string]bool),
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

	st.mu.Lock()
	st.records[cluster] = connectionCredentialCheckRecord{
		Status:    status,
		Detail:    detail,
		CheckedAt: view.CheckedAt,
	}
	announced := st.driftAnnounced[cluster]
	var announce, clear bool
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
		// credentialCheckFailed: deliberately no arm — see driftAnnounced.
	}
	st.mu.Unlock()

	if st.auditFn == nil {
		return
	}
	switch {
	case announce:
		st.auditFn(audit.Entry{
			Level:    "warn",
			Event:    "connection_credential_drift_detected",
			User:     "sharko",
			Action:   "credential_check",
			Resource: "cluster:" + cluster,
			Source:   "server",
			Result:   "failure",
			Detail:   credentialDriftNotice,
		})
	case clear:
		st.auditFn(audit.Entry{
			Level:    "info",
			Event:    "connection_credential_drift_cleared",
			User:     "sharko",
			Action:   "credential_check",
			Resource: "cluster:" + cluster,
			Source:   "server",
			Result:   "success",
			Detail:   credentialDriftClearedNotice,
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
}

// NewConnectionCredentialCheckLoop builds the loop. A non-positive interval
// falls back to the default — the caller (cmd/sharko/serve.go) parses the
// env var and logs its own warning on a bad value.
func NewConnectionCredentialCheckLoop(srv *Server, interval time.Duration) *ConnectionCredentialCheckLoop {
	if interval <= 0 {
		interval = DefaultConnectionCredentialCheckInterval
	}
	return &ConnectionCredentialCheckLoop{srv: srv, interval: interval, stop: make(chan struct{})}
}

// Start launches the single goroutine. Safe to call more than once; only the
// first call does anything.
func (l *ConnectionCredentialCheckLoop) Start(ctx context.Context) {
	l.startOnce.Do(func() {
		go l.run(ctx)
	})
}

// Stop ends the loop. Idempotent.
func (l *ConnectionCredentialCheckLoop) Stop() {
	l.stopOnce.Do(func() { close(l.stop) })
}

func (l *ConnectionCredentialCheckLoop) run(ctx context.Context) {
	// One pass right away: a fleet page that says nothing for a whole
	// interval after boot helps nobody, and a pass whose dependencies are
	// not up yet skips harmlessly.
	l.runOnce(ctx)
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.stop:
			return
		case <-ticker.C:
			l.runOnce(ctx)
		}
	}
}

// runOnce checks every managed cluster once. It enumerates clusters through
// the same ListClusters call the fleet page itself uses, so the loop covers
// exactly the rows a person sees. Any missing dependency skips the pass
// quietly — the loop is a safety net, and the manual check still works (and
// still updates the same store) whenever a person asks.
func (l *ConnectionCredentialCheckLoop) runOnce(ctx context.Context) {
	s := l.srv
	if s == nil {
		return
	}
	if s.clusterRecon == nil || s.clusterRecon.GitProviderForRead() == nil {
		slog.Debug("[connection-credential-check] skipped: cluster reconciler or git connection not available")
		return
	}
	if _, _, ok := s.k8sClientAndNamespace(); !ok {
		slog.Debug("[connection-credential-check] skipped: no hub cluster client")
		return
	}
	gp, gpErr := s.connSvc.GetActiveGitProvider()
	if gpErr != nil {
		slog.Debug("[connection-credential-check] skipped: no active git provider")
		return
	}
	ac, acErr := s.connSvc.GetActiveArgocdClient()
	if acErr != nil {
		slog.Debug("[connection-credential-check] skipped: no active ArgoCD client")
		return
	}
	listResp, listErr := s.clusterSvc.ListClusters(ctx, gp, ac)
	if listErr != nil {
		// The list error's own text is not logged — same caution as the
		// comparison's git-read branch.
		slog.Warn("[connection-credential-check] skipped: could not list clusters")
		return
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
}
