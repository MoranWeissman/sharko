package notifications

// connection_poller_test.go — what the poller does on each transition.
//
// These used to count alerts BY TITLE. They now count by Code, because the
// title is display content and counting by it is the very thing the product
// owner ruled out: a test that finds an alert by its sentence goes green or
// red depending on the wording, which is not what any of these tests are
// about. The wording itself is pinned separately, and the identifier's
// independence from it is proved in code_independence_test.go.

import (
	"context"
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

// codesInStore counts open alerts by identifier. It replaces a helper that
// counted by title — see the file comment.
func codesInStore(s *Store) map[Code]int {
	counts := map[Code]int{}
	for _, n := range s.List() {
		counts[n.Code]++
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
	git.result = UnhealthyResult(ReasonTimeout)
	p.check()

	counts := codesInStore(store)
	if counts[CodeGitConnectionBroken] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", CodeGitConnectionBroken, counts[CodeGitConnectionBroken])
	}
	if counts[CodeArgoRepoBroken] != 0 {
		t.Fatalf("a Git break must not raise the ArgoCD alert")
	}
	// Type + reason carried through, and the default title was used.
	n := store.List()[0]
	if n.Type != TypeConnection {
		t.Errorf("expected type %q, got %q", TypeConnection, n.Type)
	}
	if n.Title != TitleGitConnectionBroken {
		t.Errorf("expected the default Git title, got %q", n.Title)
	}
	// The description is the catalog lead for the code plus the catalog
	// sentence for the reason. This assertion used to require the probe's raw
	// text ("dial tcp: timeout") to appear here — the leak security story S4
	// removed, pinned by a passing test as the expected behaviour.
	if n.Description != LeadGitConnectionBroken+" "+reasonSentences[ReasonTimeout] {
		t.Errorf("the description is not the catalog lead plus the catalog reason sentence, got %q", n.Description)
	}
	if n.Reason != ReasonTimeout {
		t.Errorf("expected the structured reason to be stored, got %q", n.Reason)
	}
	if n.Schema != CurrentSchema {
		t.Errorf("a freshly-raised notification must be stamped with the current schema, got %d", n.Schema)
	}
}

func TestPoller_ArgoUnhealthy_RaisesArgoAlert(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResult(ReasonNotSynced)
	p.check()

	counts := codesInStore(store)
	if counts[CodeArgoRepoBroken] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", CodeArgoRepoBroken, counts[CodeArgoRepoBroken])
	}
	if counts[CodeGitConnectionBroken] != 0 {
		t.Fatalf("an ArgoCD break must not raise the Git alert")
	}
}

func TestPoller_BothUnhealthy_TwoDistinctAlerts(t *testing.T) {
	p, git, argo, store := newTestPoller()
	git.result = UnhealthyResult(ReasonUnreachable)
	argo.result = UnhealthyResult(ReasonUnreachable)
	p.check()

	counts := codesInStore(store)
	if counts[CodeGitConnectionBroken] != 1 || counts[CodeArgoRepoBroken] != 1 {
		t.Fatalf("expected two distinct alerts, got %+v", counts)
	}
}

func TestPoller_StaysUnhealthy_NotReAdded(t *testing.T) {
	p, git, _, store := newTestPoller()
	git.result = UnhealthyResult(ReasonUnreachable)
	p.check()
	p.check()
	p.check()

	if got := codesInStore(store)[CodeGitConnectionBroken]; got != 1 {
		t.Fatalf("expected the alert to be raised once across multiple ticks, got %d", got)
	}
}

func TestPoller_NotReAddedAfterMarkRead(t *testing.T) {
	p, git, _, store := newTestPoller()
	git.result = UnhealthyResult(ReasonUnreachable)
	p.check()
	// User reads the bell.
	store.MarkAllRead()
	// Still unhealthy on the next tick — edge tracking must NOT re-add.
	p.check()

	if got := codesInStore(store)[CodeGitConnectionBroken]; got != 1 {
		t.Fatalf("expected no re-add after mark-read while still unhealthy, got %d", got)
	}
}

func TestPoller_UnhealthyThenHealthy_Resolves(t *testing.T) {
	p, git, _, store := newTestPoller()
	git.result = UnhealthyResult(ReasonUnreachable)
	p.check()
	if got := codesInStore(store)[CodeGitConnectionBroken]; got != 1 {
		t.Fatalf("setup: expected the alert raised, got %d", got)
	}

	// Connection recovers.
	git.result = HealthyResult()
	p.check()

	if got := codesInStore(store)[CodeGitConnectionBroken]; got != 0 {
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
	git.result = UnhealthyResult(ReasonUnreachable)
	p.check()
	// Then the connection is removed / can't be probed — must NOT clear the
	// existing alert (we can't determine recovery).
	git.result = UndeterminedResult()
	p.check()

	if got := codesInStore(store)[CodeGitConnectionBroken]; got != 1 {
		t.Fatalf("undetermined tick must not resolve a standing break, got %d", got)
	}
}

// --- Error review package 1 — per-result override -------------------------
//
// argoHealthFn can distinguish "ArgoCD rejected our token" and "Sharko can't
// reach ArgoCD" from the generic "ArgoCD can't sync the repo" via
// UnhealthyResultWithCode. These pin that override end to end — on the
// identifier, which is what tells the three apart, with the title carried
// alongside as the words to show.

func TestPoller_ArgoAuthFailed_RaisesDistinctCode(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResultWithCode(
		CodeArgoAuthFailed,
		ReasonCredentials,
	)
	p.check()

	counts := codesInStore(store)
	if counts[CodeArgoAuthFailed] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", CodeArgoAuthFailed, counts[CodeArgoAuthFailed])
	}
	if counts[CodeArgoRepoBroken] != 0 {
		t.Fatalf("an auth failure must NOT also raise the generic repo-broken alert")
	}
	n := store.List()[0]
	// The description is the two catalog sentences the code and the reason
	// select, and nothing else. This assertion used to look for a fragment of
	// the raw detail string the probe passed in — which is precisely the leak
	// security story S4 removed, written down as the expected behaviour.
	if n.Description != LeadArgoAuthFailed+" "+reasonSentences[ReasonCredentials] {
		t.Errorf("the description is not the catalog lead plus the catalog reason sentence, got %q", n.Description)
	}
	if n.Reason != ReasonCredentials {
		t.Errorf("expected the structured reason to be stored, got %q", n.Reason)
	}
	// The title is chosen by the CODE, in render.go. The probe passes no
	// words at all any more — UnhealthyResultWithCode has no title parameter.
	if n.Title != TitleArgoAuthFailed {
		t.Errorf("expected the code to select the auth-failed title, got %q", n.Title)
	}
}

func TestPoller_ArgoUnreachable_RaisesDistinctCode(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResultWithCode(
		CodeArgoUnreachable,
		ReasonUnreachable,
	)
	p.check()

	counts := codesInStore(store)
	if counts[CodeArgoUnreachable] != 1 {
		t.Fatalf("expected exactly one %q alert, got %d", CodeArgoUnreachable, counts[CodeArgoUnreachable])
	}
	if counts[CodeArgoRepoBroken] != 0 {
		t.Fatalf("an unreachable ArgoCD must NOT also raise the generic repo-broken alert")
	}
}

// A recovered override alert must resolve the SAME alert it was raised as,
// not the caller's default one.
func TestPoller_ArgoAuthFailedThenHealthy_ResolvesTheOverrideCode(t *testing.T) {
	p, _, argo, store := newTestPoller()
	argo.result = UnhealthyResultWithCode(CodeArgoAuthFailed, ReasonCredentials)
	p.check()
	if got := codesInStore(store)[CodeArgoAuthFailed]; got != 1 {
		t.Fatalf("setup: expected the auth-failed alert raised, got %d", got)
	}

	argo.result = HealthyResult()
	p.check()

	if got := codesInStore(store)[CodeArgoAuthFailed]; got != 0 {
		t.Fatalf("expected the auth-failed alert resolved after recovery, got %d", got)
	}
}

// TestPoller_UnhealthyReclassifiedWithoutRecovering_UpdatesAlert — review
// findings r1, L12. Before that fix, an unhealthy→unhealthy reclassification
// (the connection never recovers in between, just the reason changes) fell
// through evaluate()'s "no transition" no-op, so the bell kept showing the
// FIRST problem forever — actively wrong once the underlying reason changed.
//
// The reclassification check now compares identifiers rather than titles: a
// different problem has a different identifier, and a reworded sentence is not
// a different problem.
func TestPoller_UnhealthyReclassifiedWithoutRecovering_UpdatesAlert(t *testing.T) {
	p, _, argo, store := newTestPoller()

	// First tick: ArgoCD can't sync the repo (the generic degraded-app case).
	argo.result = UnhealthyResult(ReasonNotSynced)
	p.check()
	if got := codesInStore(store)[CodeArgoRepoBroken]; got != 1 {
		t.Fatalf("setup: expected the repo-broken alert raised, got %d", got)
	}

	// Second tick: still unhealthy, but now ArgoCD is rejecting the token —
	// a different problem, never having gone healthy in between.
	argo.result = UnhealthyResultWithCode(
		CodeArgoAuthFailed,
		ReasonCredentials,
	)
	p.check()

	counts := codesInStore(store)
	if counts[CodeArgoAuthFailed] != 1 {
		t.Fatalf("expected the reclassified alert %q to be raised, got %d", CodeArgoAuthFailed, counts[CodeArgoAuthFailed])
	}
	if counts[CodeArgoRepoBroken] != 0 {
		t.Fatalf("expected the stale %q alert to be resolved once the problem changed, got %d still open", CodeArgoRepoBroken, counts[CodeArgoRepoBroken])
	}

	// Third tick: same reclassified reason again — must NOT re-add or
	// re-resolve (no change this time).
	p.check()
	if got := codesInStore(store)[CodeArgoAuthFailed]; got != 1 {
		t.Fatalf("expected no duplicate add on a repeated identical reclassification, got %d", got)
	}

	// Recovery still resolves the CURRENT (reclassified) alert.
	argo.result = HealthyResult()
	p.check()
	if got := len(store.List()); got != 0 {
		t.Fatalf("expected all alerts resolved after recovery, got %d remaining: %+v", got, store.List())
	}
}

// TestPoller_TitlesAreExact pins the five alert titles as LITERALS.
//
// The titles are display content and nothing branches on them any more — but
// "nothing branches on it" is not the same as "nobody should notice it
// changed". These are sentences a person reads on the bell, so a rewrite
// should be a deliberate edit here, seen in a diff, and not a side effect of
// something else.
//
// Written out as literals on purpose: comparing TitleArgoRepoBroken with
// TitleArgoRepoBroken compares a symbol with itself and passes whatever it
// says.
func TestPoller_TitlesAreExact(t *testing.T) {
	exact := []struct{ name, got, want string }{
		{"TitleGitConnectionBroken", TitleGitConnectionBroken, "Sharko can't reach your Git connection"},
		{"TitleArgoRepoBroken", TitleArgoRepoBroken, "ArgoCD can't sync the repo"},
		{"TitleArgoAuthFailed", TitleArgoAuthFailed, "ArgoCD rejected Sharko's token"},
		{"TitleArgoUnreachable", TitleArgoUnreachable, "Sharko can't reach ArgoCD"},
		{"TitleArgoForbidden", TitleArgoForbidden, "ArgoCD refused Sharko's token permission"},
	}
	for _, tc := range exact {
		if tc.got != tc.want {
			t.Errorf("the alert title %s drifted:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}
}
