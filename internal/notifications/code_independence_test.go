package notifications

// code_independence_test.go — the server half of the product owner's two
// browser break tests.
//
// The browser ones are: change the title, prove both connection panels still
// receive their details; change the identifier, prove the wrong panel cannot.
// Neither can be written until the server actually guarantees that a
// notification's identity is its Code and nothing else. These prove that
// guarantee, by running the real ConnectionPoller against the real Store with
// deliberately WRONG titles.
//
// "Deliberately wrong" is the point. Every title below is nonsense a product
// owner would never ship. If any of these tests starts depending on the real
// wording, it stops proving anything.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// pollerFor builds a poller wired to one store with fixed probes, driving one
// check() synchronously. Start()'s goroutine and ticker are not involved —
// this is about what one evaluation does, not about timing.
func pollerFor(store *Store, git, argo func(ctx context.Context) HealthResult) *ConnectionPoller {
	return NewConnectionPoller(store, time.Minute, git, argo)
}

func healthyProbe(context.Context) HealthResult { return HealthyResult() }

// findByCode returns the single notification carrying code, or nil.
func findByCode(store *Store, code Code) *Notification {
	for _, n := range store.List() {
		if n.Code == code {
			found := n
			return &found
		}
	}
	return nil
}

// TestCode_TheProbeCannotChooseTheWords replaces what used to be
// TestCode_SurvivesAnyTitle.
//
// That test drove the poller with nonsense titles and asserted the identifier
// survived them. It could only be written because a probe could pass a title at
// all — caller-composed prose that landed on a persisted, user-visible field
// with nothing checking it. The title parameter is gone, so the stronger claim
// is now provable: a probe supplies two enums, and the words come from the
// server's own table keyed by the code.
//
// The titles are compared against LITERALS, not against the Title* constants,
// because comparing a constant with itself proves nothing about what it says.
func TestCode_TheProbeCannotChooseTheWords(t *testing.T) {
	cases := []struct {
		code      Code
		wantTitle string
	}{
		{CodeGitConnectionBroken, "Sharko can't reach your Git connection"},
		{CodeArgoRepoBroken, "ArgoCD can't sync the repo"},
		{CodeArgoAuthFailed, "ArgoCD rejected Sharko's token"},
		{CodeArgoUnreachable, "Sharko can't reach ArgoCD"},
		{CodeArgoForbidden, "ArgoCD refused Sharko's token permission"},
	}

	for _, tc := range cases {
		t.Run(tc.code.String(), func(t *testing.T) {
			store := NewStore(10, nil)
			p := pollerFor(store,
				func(context.Context) HealthResult {
					return UnhealthyResultWithCode(tc.code, ReasonUnreachable)
				},
				func(context.Context) HealthResult { return UndeterminedResult() },
			)
			p.check()

			got := findByCode(store, tc.code)
			if got == nil {
				t.Fatalf("no notification carries the identifier %q after a break was raised under it.\nStore holds: %v",
					tc.code, store.List())
			}
			if got.Title != tc.wantTitle {
				t.Errorf("the title for %s is not the one the server owns:\n got %q\nwant %q", tc.code, got.Title, tc.wantTitle)
			}
			if got.Type != TypeConnection {
				t.Errorf("the type for %s is %q, want %q — the type is derived from the code, not taken from a caller",
					tc.code, got.Type, TypeConnection)
			}
		})
	}
}

// TestCode_RecoveryClearsTheAlertEvenWhenTheWordingChanged is the case that
// used to break. The store resolved a recovered alert by re-typing the same
// sentence it was raised with, so a title that differed by one character left
// the alert on the bell forever with the connection healthy.
func TestCode_RecoveryClearsTheAlertEvenWhenTheWordingChanged(t *testing.T) {
	store := NewStore(10, nil)

	brokenWithOneWording := func(context.Context) HealthResult {
		return UnhealthyResultWithCode(CodeGitConnectionBroken, ReasonUnreachable)
	}
	p := pollerFor(store, brokenWithOneWording, func(context.Context) HealthResult { return UndeterminedResult() })
	p.check()

	if findByCode(store, "git_connection_broken") == nil {
		t.Fatal("the break was never raised, so this test cannot prove anything about clearing it")
	}

	// The connection recovers. Under the old title-keyed code this only
	// cleared if the resolve string matched the raise string exactly.
	p.gitHealthFn = healthyProbe
	p.check()

	if got := findByCode(store, "git_connection_broken"); got != nil {
		t.Errorf("the alert is still on the bell after the connection recovered: %+v", *got)
	}
	if len(store.List()) != 0 {
		t.Errorf("the store should be empty after the only alert resolved, it holds %v", store.List())
	}
}

// TestCode_ReclassificationSwapsTheAlert covers unhealthy → unhealthy under a
// DIFFERENT identifier: the stale alert must go, the new one must arrive, and
// the bell must never show two open alerts for one connection.
func TestCode_ReclassificationSwapsTheAlert(t *testing.T) {
	store := NewStore(10, nil)

	current := UnhealthyResultWithCode(CodeArgoRepoBroken, ReasonNotSynced)
	p := pollerFor(store,
		func(context.Context) HealthResult { return UndeterminedResult() },
		func(context.Context) HealthResult { return current },
	)
	p.check()

	if findByCode(store, "argocd_repo_broken") == nil {
		t.Fatal("the first classification was never raised")
	}

	// Same connection, still broken, but now a different KIND of problem.
	current = UnhealthyResultWithCode(CodeArgoAuthFailed, ReasonCredentials)
	p.check()

	if got := findByCode(store, "argocd_repo_broken"); got != nil {
		t.Errorf("the stale alert is still open after reclassification: %+v", *got)
	}
	if findByCode(store, "argocd_auth_failed") == nil {
		t.Errorf("the new classification was never raised. Store holds: %v", store.List())
	}
	if len(store.List()) != 1 {
		t.Errorf("one connection must never show two open alerts, the store holds %v", store.List())
	}
}

// TestCode_RepeatedChecksDoNotReNag is the other side of the same coin. A
// probe that keeps reporting the same problem tick after tick is still the same
// problem, and must not stack alerts on the bell or resurrect one the user has
// marked read.
//
// This used to drive the poller with a different wording on every tick, to
// prove the wording was not what made an alert "new". A probe cannot supply a
// wording at all now, so the wording cannot make anything look new — which is
// the same guarantee, obtained structurally rather than tested for.
func TestCode_RepeatedChecksDoNotReNag(t *testing.T) {
	store := NewStore(10, nil)

	p := pollerFor(store,
		func(context.Context) HealthResult {
			return UnhealthyResultWithCode(CodeGitConnectionBroken, ReasonUnreachable)
		},
		func(context.Context) HealthResult { return UndeterminedResult() },
	)
	p.check()
	store.MarkAllRead()

	for i := 0; i < 3; i++ {
		p.check()
	}

	all := store.List()
	if len(all) != 1 {
		t.Fatalf("re-raising the same problem stacked alerts on the bell — the store holds %d:\n%v", len(all), all)
	}
	if !all[0].Read {
		t.Error("an alert the user marked read came back unread on a later tick")
	}
}

// TestCode_TheIDCarriesNoWording proves the notification's ID — the key the
// browser posts back to mark an alert read — is built from the identifier.
//
// The old ID interpolated the title, so rewording a sentence silently changed
// an identifier the browser was holding. The wanted value is a LITERAL: a test
// that built the expected ID the same way the code does would agree with the
// code however wrong both were.
func TestCode_TheIDCarriesNoWording(t *testing.T) {
	store := NewStore(10, nil)

	p := pollerFor(store,
		func(context.Context) HealthResult {
			return UnhealthyResultWithCode(CodeGitConnectionBroken, ReasonUnreachable)
		},
		func(context.Context) HealthResult { return UndeterminedResult() },
	)
	p.check()

	got := findByCode(store, "git_connection_broken")
	if got == nil {
		t.Fatal("nothing was raised")
	}
	if got.ID != "connection-git_connection_broken" {
		t.Errorf("the ID is not built from the identifier: got %q, want %q", got.ID, "connection-git_connection_broken")
	}
	// And no part of the sentence a person reads is in it.
	for _, word := range []string{"Sharko", "reach", "your"} {
		if strings.Contains(got.ID, word) {
			t.Errorf("the notification ID contains %q from its title: %q. The ID is a key the browser posts back — no wording may reach it.",
				word, got.ID)
		}
	}
}

// TestCode_TheIDIsStableAcrossOccurrences proves the same problem raised
// twice keeps one ID, which is what lets the store deduplicate the re-raise
// on every tick now that dedup keys on the ID.
func TestCode_TheIDIsStableAcrossOccurrences(t *testing.T) {
	store := NewStore(10, nil)
	p := pollerFor(store,
		func(context.Context) HealthResult {
			return UnhealthyResultWithCode(CodeGitConnectionBroken, ReasonUnreachable)
		},
		func(context.Context) HealthResult { return UndeterminedResult() },
	)

	p.check()
	first := findByCode(store, "git_connection_broken")
	if first == nil {
		t.Fatal("nothing was raised on the first check")
	}

	// Recover, then break again — a genuinely new occurrence.
	p.gitHealthFn = healthyProbe
	p.check()
	p.gitHealthFn = func(context.Context) HealthResult {
		return UnhealthyResultWithCode(CodeGitConnectionBroken, ReasonTimeout)
	}
	p.check()

	second := findByCode(store, "git_connection_broken")
	if second == nil {
		t.Fatal("the second break was never raised — a recovered alert must be able to fire again")
	}
	if second.ID != first.ID {
		t.Errorf("the same problem got two different IDs across occurrences: %q then %q", first.ID, second.ID)
	}
}

// TestCode_UndeclaredCodeNeverReachesTheStore is the server side of "unknown
// identifiers degrade visibly and safely". The browser owns the rendering; the
// server owns never emitting an identifier outside the declared set.
func TestCode_UndeclaredCodeNeverReachesTheStore(t *testing.T) {
	store := NewStore(10, nil)

	store.Add(Notification{
		ID:          "made-up",
		Code:        "a_code_nobody_declared",
		Type:        TypeConnection,
		Title:       "Something happened",
		Description: "Something happened, at length.",
		Timestamp:   time.Now(),
	})
	if len(store.List()) != 0 {
		t.Errorf("the store accepted a notification with an undeclared code: %v", store.List())
	}

	// And a notification with no code at all, which is the same failure by a
	// different route: a new kind added without an identifier.
	store.Add(Notification{
		ID:          "no-code",
		Type:        TypeConnection,
		Title:       "Something else happened",
		Description: "At length.",
		Timestamp:   time.Now(),
	})
	if len(store.List()) != 0 {
		t.Errorf("the store accepted a notification with no code at all: %v", store.List())
	}

	// Not passing vacuously — a declared code goes in.
	store.Add(Notification{
		ID:        "real",
		Code:      CodeGitConnectionBroken,
		Type:      TypeConnection,
		Title:     "A real one",
		Timestamp: time.Now(),
	})
	if len(store.List()) != 1 {
		t.Errorf("the store refused a notification with a declared code — the refusal is too broad: %v", store.List())
	}
}
