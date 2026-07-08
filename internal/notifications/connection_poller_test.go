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
