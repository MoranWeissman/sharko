package notifications

import (
	"context"
	"strings"
	"testing"
)

// fakeProbe is a controllable health probe. Set the field, call check(),
// inspect the store.
type fakeProbe struct {
	result HealthResult
}

func (f *fakeProbe) fn(_ context.Context) HealthResult { return f.result }

// newTestPoller builds a poller wired to two fake probes. It does NOT start the
// background goroutine — tests drive ticks deterministically via p.check().
func newTestPoller() (*ConnectionPoller, *fakeProbe, *fakeProbe, *Store) {
	store := NewStore(50, nil)
	git := &fakeProbe{result: HealthyResult()}
	argo := &fakeProbe{result: HealthyResult()}
	p := NewConnectionPoller(store, DefaultConnectionCheckInterval, git.fn, argo.fn)
	return p, git, argo, store
}

func titlesInStore(s *Store) map[string]int {
	counts := map[string]int{}
	for _, n := range s.List() {
		counts[n.Title]++
	}
	return counts
}

func TestPoller_BothHealthy_NoNotifications(t *testing.T) {
	p, _, _, store := newTestPoller()
	p.check()
	if got := len(store.List()); got != 0 {
		t.Fatalf("expected no notifications when both healthy, got %d", got)
	}
}

func TestPoller_GitUnhealthy_RaisesGitAlert(t *testing.T) {
	p, git, _, store := newTestPoller()
	git.result = UnhealthyResult("dial tcp: timeout")
	p.check()

	counts := titlesInStore(store)
	if counts[TitleGitConnectionBroken] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", TitleGitConnectionBroken, counts[TitleGitConnectionBroken])
	}
	if counts[TitleArgoRepoBroken] != 0 {
		t.Fatalf("git break must not raise the ArgoCD alert")
	}
	// Type + reason carried through.
	n := store.List()[0]
	if n.Type != TypeConnection {
		t.Errorf("expected type %q, got %q", TypeConnection, n.Type)
	}
	if n.Description == "" || !strings.Contains(n.Description, "dial tcp: timeout") {
		t.Errorf("expected the underlying reason in the description, got %q", n.Description)
	}
}

func TestPoller_ArgoUnhealthy_RaisesArgoAlert(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResult("argocd app sync=OutOfSync health=Degraded")
	p.check()

	counts := titlesInStore(store)
	if counts[TitleArgoRepoBroken] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", TitleArgoRepoBroken, counts[TitleArgoRepoBroken])
	}
	if counts[TitleGitConnectionBroken] != 0 {
		t.Fatalf("argo break must not raise the Git alert")
	}
}

func TestPoller_BothUnhealthy_TwoDistinctAlerts(t *testing.T) {
	p, git, argo, store := newTestPoller()
	git.result = UnhealthyResult("git down")
	argo.result = UnhealthyResult("argo down")
	p.check()

	counts := titlesInStore(store)
	if counts[TitleGitConnectionBroken] != 1 || counts[TitleArgoRepoBroken] != 1 {
		t.Fatalf("expected two distinct alerts, got %+v", counts)
	}
}

func TestPoller_StaysUnhealthy_NotReAdded(t *testing.T) {
	p, git, _, store := newTestPoller()
	git.result = UnhealthyResult("git down")
	p.check()
	p.check()
	p.check()

	if got := titlesInStore(store)[TitleGitConnectionBroken]; got != 1 {
		t.Fatalf("expected the alert to be raised once across multiple ticks, got %d", got)
	}
}

func TestPoller_NotReAddedAfterMarkRead(t *testing.T) {
	p, git, _, store := newTestPoller()
	git.result = UnhealthyResult("git down")
	p.check()
	// User reads the bell.
	store.MarkAllRead()
	// Still unhealthy on the next tick — edge tracking must NOT re-add.
	p.check()

	if got := titlesInStore(store)[TitleGitConnectionBroken]; got != 1 {
		t.Fatalf("expected no re-add after mark-read while still unhealthy, got %d", got)
	}
}

func TestPoller_UnhealthyThenHealthy_Resolves(t *testing.T) {
	p, git, _, store := newTestPoller()
	git.result = UnhealthyResult("git down")
	p.check()
	if got := titlesInStore(store)[TitleGitConnectionBroken]; got != 1 {
		t.Fatalf("setup: expected the alert raised, got %d", got)
	}

	// Connection recovers.
	git.result = HealthyResult()
	p.check()

	if got := titlesInStore(store)[TitleGitConnectionBroken]; got != 0 {
		t.Fatalf("expected the alert to be resolved (gone) after recovery, got %d", got)
	}
}

func TestPoller_NoActiveConnection_NoOp(t *testing.T) {
	p, git, argo, store := newTestPoller()
	git.result = UndeterminedResult()
	argo.result = UndeterminedResult()
	p.check()
	p.check()

	if got := len(store.List()); got != 0 {
		t.Fatalf("expected no notifications when nothing is configured, got %d", got)
	}
}

func TestPoller_UndeterminedDoesNotResolveExistingBreak(t *testing.T) {
	p, git, _, store := newTestPoller()
	// First a real break.
	git.result = UnhealthyResult("git down")
	p.check()
	// Then the connection is removed / can't be probed — must NOT clear the
	// existing alert (we can't determine recovery).
	git.result = UndeterminedResult()
	p.check()

	if got := titlesInStore(store)[TitleGitConnectionBroken]; got != 1 {
		t.Fatalf("undetermined tick must not resolve a standing break, got %d", got)
	}
}

// --- Error review package 1 — per-result title override -------------------
//
// argoHealthFn can now distinguish "ArgoCD rejected our token" and "Sharko
// can't reach ArgoCD" from the generic "ArgoCD can't sync the repo" via
// UnhealthyResultWithTitle. These tests pin that override end to end.

func TestPoller_ArgoAuthFailed_RaisesDistinctTitle(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResultWithTitle(
		TitleArgoAuthFailed,
		"ArgoCD rejected the token Sharko uses to check on the bootstrap app.",
		"invalid ArgoCD token — check that the token is correct and not expired",
	)
	p.check()

	counts := titlesInStore(store)
	if counts[TitleArgoAuthFailed] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", TitleArgoAuthFailed, counts[TitleArgoAuthFailed])
	}
	if counts[TitleArgoRepoBroken] != 0 {
		t.Fatalf("an auth failure must NOT also raise the generic repo-broken title")
	}
	n := store.List()[0]
	if !strings.Contains(n.Description, "invalid ArgoCD token") {
		t.Errorf("expected the token detail in the description, got %q", n.Description)
	}
}

func TestPoller_ArgoUnreachable_RaisesDistinctTitle(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResultWithTitle(
		TitleArgoUnreachable,
		"Sharko couldn't get an answer from ArgoCD.",
		"dial tcp: i/o timeout",
	)
	p.check()

	counts := titlesInStore(store)
	if counts[TitleArgoUnreachable] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", TitleArgoUnreachable, counts[TitleArgoUnreachable])
	}
	if counts[TitleArgoRepoBroken] != 0 {
		t.Fatalf("an unreachable ArgoCD must NOT also raise the generic repo-broken title")
	}
}

// A recovered override-titled alert must resolve the SAME title it was
// raised under, not the caller's default title.
func TestPoller_ArgoAuthFailedThenHealthy_ResolvesTheOverrideTitle(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResultWithTitle(TitleArgoAuthFailed, "token rejected", "401")
	p.check()
	if got := titlesInStore(store)[TitleArgoAuthFailed]; got != 1 {
		t.Fatalf("setup: expected the auth-failed alert raised, got %d", got)
	}

	argo.result = HealthyResult()
	p.check()

	if got := titlesInStore(store)[TitleArgoAuthFailed]; got != 0 {
		t.Fatalf("expected the auth-failed alert resolved after recovery, got %d", got)
	}
}

// TestPoller_UnhealthyReclassifiedWithoutRecovering_UpdatesTitle — review
// findings r1, L12. Before this fix, an unhealthy→unhealthy reclassification
// (the connection never recovers in between, just the reason changes) fell
// through evaluate()'s "no transition" no-op, so the bell kept showing the
// FIRST title forever — actively wrong once the underlying reason changed.
func TestPoller_UnhealthyReclassifiedWithoutRecovering_UpdatesTitle(t *testing.T) {
	p, _, argo, store := newTestPoller()

	// First tick: ArgoCD can't sync the repo (generic degraded-app title).
	argo.result = UnhealthyResult("argocd app sync=OutOfSync health=Degraded")
	p.check()
	if got := titlesInStore(store)[TitleArgoRepoBroken]; got != 1 {
		t.Fatalf("setup: expected the repo-broken alert raised, got %d", got)
	}

	// Second tick: still unhealthy, but now ArgoCD is rejecting the token —
	// a different problem, never having gone healthy in between.
	argo.result = UnhealthyResultWithTitle(
		TitleArgoAuthFailed,
		"ArgoCD rejected the token Sharko uses to check on the bootstrap app.",
		"invalid ArgoCD token",
	)
	p.check()

	counts := titlesInStore(store)
	if counts[TitleArgoAuthFailed] != 1 {
		t.Fatalf("expected the reclassified alert %q to be raised, got %d", TitleArgoAuthFailed, counts[TitleArgoAuthFailed])
	}
	if counts[TitleArgoRepoBroken] != 0 {
		t.Fatalf("expected the stale %q alert to be resolved once the title changed, got %d still open", TitleArgoRepoBroken, counts[TitleArgoRepoBroken])
	}

	// Third tick: same reclassified reason again — must NOT re-add or
	// re-resolve (no title change this time).
	p.check()
	if got := titlesInStore(store)[TitleArgoAuthFailed]; got != 1 {
		t.Fatalf("expected no duplicate add on a repeated identical reclassification, got %d", got)
	}

	// Recovery still resolves the CURRENT (reclassified) title.
	argo.result = HealthyResult()
	p.check()
	if got := len(store.List()); got != 0 {
		t.Fatalf("expected all alerts resolved after recovery, got %d remaining: %+v", got, store.List())
	}
}
