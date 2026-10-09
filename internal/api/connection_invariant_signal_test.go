package api

// connection_invariant_signal_test.go — the fail-closed invariant guard in
// connection_canonical.go must SAY when it fires.
//
// # What was wrong
//
// The guard that keeps "Connection synced" impossible unless the whole
// connection was verified used to downgrade the answer in complete silence:
// no log line, no metric, no audit entry. Its own comment calls the state it
// catches "a bug by definition", and yet a wrong classification upstream would
// have produced no signal anywhere on the server. Worse, two of the four
// headlines the downgraded state can then pick read like good news — "Not
// checked yet" and "Configuration matches; credential content not compared" —
// so the bug would have been masked by reassurance instead of surfaced.
//
// # What is pinned here
//
//   - it fires and it says so;
//   - the line carries only Sharko's own closed-set enum values, proven with a
//     planted sentinel driven through the fields an attacker or a misbehaving
//     backend could reach;
//   - a normal comparison logs NOTHING, which is what makes "it cannot spam"
//     a checked fact rather than an argument.
//
// No metric is raised here on purpose. internal/metrics has a contract
// registry with a floor on how many metric families exist, and a separate
// story owns that area; adding a family from this file would trip it with an
// error that does not mention this file at all.

// captureSlog lives in cred_error_sentinel_test.go — same package, same job.
// It captures at Debug level, so a line emitted at ANY level is seen; that is
// what makes the "logs nothing on a normal answer" assertions below real.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
)

func TestConnectionInvariant_DowngradeIsAnnounced(t *testing.T) {
	// The incoherent input the guard exists for: synced at a narrower scope
	// than full. Compare cannot produce it today — the guard is defensive.
	logged := captureSlog(t, func() {
		st := connectionCanonicalStateFor(reconView(func(v *connectionComparisonView) {
			v.Status = string(connectioncompare.StatusSynced)
			v.Scope = string(connectioncompare.ScopeLimited)
		}))
		if st.SyncState != syncStateUnknown {
			t.Fatalf("the guard did not downgrade the state: got %q", st.SyncState)
		}
	})

	if logged == "" {
		t.Fatal("the fail-closed invariant fired and logged NOTHING — a state Sharko calls a bug by definition produced no signal at all")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(logged)), &entry); err != nil {
		t.Fatalf("the guard logged something that is not one record: %v\n%s", err, logged)
	}
	if entry["level"] != "WARN" {
		t.Errorf("the downgrade was logged at %v — it should be a warning: nothing is broken for the reader, Sharko is broken", entry["level"])
	}

	// The four fields, by name and by value. A reader of this line must be
	// able to see WHICH combination was incoherent without reproducing it.
	for field, want := range map[string]string{
		"sync_state":         syncStateSynced,
		"verification_scope": verificationScopePartial,
		"managed_scope":      managedScopeFullConnection,
		"management_mode":    managementModeSharkoManaged,
	} {
		if got, ok := entry[field]; !ok {
			t.Errorf("the log line does not name %s at all", field)
		} else if got != want {
			t.Errorf("the log line reports %s=%v, want %q", field, got, want)
		}
	}
}

// TestConnectionInvariant_TheLineCarriesNothingButEnums is the positive
// control. It plants a sentinel in every field of the view that carries text
// from outside Sharko — a git branch name, a commit, a path, a cluster name,
// an ownership marker read off a live Secret — and proves none of it reaches
// the log line.
//
// The sweep is proven to WORK first: the same sentinel is checked against a
// string that does contain it, so a silent pass cannot be mistaken for a
// clean result.
func TestConnectionInvariant_TheLineCarriesNothingButEnums(t *testing.T) {
	const sentinel = "CANARY-1f0a77c2-must-never-be-logged"

	// Prove the search itself can fail before trusting its silence.
	if !strings.Contains("prefix "+sentinel+" suffix", sentinel) {
		t.Fatal("the sentinel search does not work, so its silence would prove nothing")
	}

	logged := captureSlog(t, func() {
		connectionCanonicalStateFor(reconView(func(v *connectionComparisonView) {
			v.Status = string(connectioncompare.StatusSynced)
			v.Scope = string(connectioncompare.ScopeLimited)
			v.Cluster = sentinel
			v.Branch = sentinel
			v.ComparedCommit = sentinel
			v.ComparedPath = sentinel
			v.CredentialSourceType = sentinel
			v.liveOwnershipMarker = sentinel
			v.FailureReason = sentinel
			v.LimitReason = sentinel
		}))
	})

	if logged == "" {
		t.Fatal("nothing was logged at all — the guard's signal is gone, so this test is proving nothing")
	}
	if strings.Contains(logged, sentinel) {
		t.Errorf("the invariant guard's log line carries text from outside Sharko's own enums:\n%s", logged)
	}
}

// TestConnectionInvariant_SilentOnEveryNormalAnswer is the anti-spam half.
// Every state a real comparison can produce must go through
// connectionCanonicalStateFor without a word.
func TestConnectionInvariant_SilentOnEveryNormalAnswer(t *testing.T) {
	cases := map[string]func(*connectionComparisonView){
		"synced at full scope": func(v *connectionComparisonView) {},
		"out of sync":          func(v *connectionComparisonView) { v.Status = string(connectioncompare.StatusOutOfSync) },
		"missing": func(v *connectionComparisonView) {
			v.Status = string(connectioncompare.StatusMissing)
			v.liveSecretFound = false
		},
		"check failed":       func(v *connectionComparisonView) { v.Status = string(connectioncompare.StatusCheckFailed) },
		"ownership conflict": func(v *connectionComparisonView) { v.Status = string(connectioncompare.StatusOwnershipConflict) },
		"pasted credential, nothing rebuildable": func(v *connectionComparisonView) {
			v.Status = string(connectioncompare.StatusOutOfSync)
			v.Scope = string(connectioncompare.ScopeLimited)
			v.CredentialSourceType = models.CredsSourceInlineKubeconfig
		},
	}

	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			logged := captureSlog(t, func() { connectionCanonicalStateFor(reconView(mut)) })
			if logged != "" {
				t.Errorf("a normal answer produced a log line — the guard would spam every request:\n%s", logged)
			}
		})
	}
}
