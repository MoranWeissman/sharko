package api

// lifecycle_event_semantics_test.go — ruling (f), 2026-08-19.
//
// "Title, outcome and change result must never contradict each other."
//
// Before this there was no test anywhere that asserted an audit event's
// OUTCOME. So the connection feed shipped ten contradiction classes at once:
// "Connection Secret created · failure" where no Secret existed afterwards,
// "Credential drift noticed · failure" on a check that had fully succeeded
// and whose own detail said "Nothing was changed", "Connection repaired ·
// rejected" on sixteen paths that never touched the connection, and "Cluster
// adopted · success" when every adoption failed.
//
// This file is a RULE ENGINE, not a list of expected strings. A list would
// only pin the rows somebody thought to write down; the rules reject the
// whole shape of the contradiction, so a new event that repeats the mistake
// fails without anybody remembering to add a row for it.
//
// Three layers:
//
//  1. the catalog — every connection-lifecycle event, classified by what its
//     TITLE claims;
//  2. validateLifecycleEntry — the rules, derived from the ruling's own
//     words;
//  3. the table — every (event × outcome × changes) row the writers can
//     actually produce, plus the rows a naive enumeration would invent that
//     the code cannot produce, pinned as negatives.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
)

// lifecycleEventKind is what an event's TITLE claims happened.
type lifecycleEventKind int

const (
	// kindCompletedWrite — a past-tense title claiming work landed
	// ("Connection Secret created", "Connection repaired"). It may ONLY ever
	// carry a success outcome: a past-tense claim beside a failure outcome
	// is the contradiction this whole ruling is about.
	kindCompletedWrite lifecycleEventKind = iota
	// kindFailureShaped — a title that says on its face the work did not
	// happen. It may only carry a non-success outcome; a "…_failed" event
	// recorded as a success would be the same lie mirrored.
	kindFailureShaped
	// kindRefusal — Sharko declined to act. The request completed and
	// deliberately wrote nothing.
	kindRefusal
	// kindReadOnly — a check, a comparison, a read. It never changes
	// anything and never fails to, so its change answer is always
	// not_applicable. It CAN succeed while reporting something worrying:
	// successfully detecting drift is a successful check with an attention
	// result, and the drift state itself supplies the warning.
	kindReadOnly
	// kindBatch — a fan-out over many items, which can honestly be success,
	// partial or failure. What it may not do is take its outcome from an
	// HTTP status that does not know what happened.
	kindBatch
	// kindRequestScoped — the up-front "somebody asked for this" entry. Its
	// outcome is whatever the request ended up as; it makes no claim about
	// work landing, so it is exempt from the past-tense rule.
	kindRequestScoped
)

// lifecycleEventCatalog classifies every connection-lifecycle event by what
// its title claims. An event the feed renders that is NOT in here is itself a
// finding — see TestLifecycleEvents_CatalogCoversEveryWriter.
var lifecycleEventCatalog = map[string]lifecycleEventKind{
	// Reconciler — the cluster-Secret lifecycle.
	clusterreconciler.EventClusterSecretCreate:          kindCompletedWrite,
	clusterreconciler.EventClusterSecretDelete:          kindCompletedWrite,
	clusterreconciler.EventClusterSecretUserLabelSync:   kindCompletedWrite,
	clusterreconciler.EventClusterSecretManagedSelfHeal: kindCompletedWrite,
	clusterreconciler.EventClusterConnectionRepair:      kindCompletedWrite,

	"cluster_secret_create_failed":            kindFailureShaped,
	"cluster_secret_delete_failed":            kindFailureShaped,
	"cluster_secret_user_label_sync_failed":   kindFailureShaped,
	"cluster_secret_managed_self_heal_failed": kindFailureShaped,
	"cluster_connection_repair_failed":        kindFailureShaped,

	// The API repair handler.
	"cluster_connection_repair_requested": kindRequestScoped,
	"cluster_connection_repair_refused":   kindRefusal,

	// The background credential check — read-only, always.
	"connection_credential_drift_detected":  kindReadOnly,
	"connection_credential_drift_cleared":   kindReadOnly,
	"connection_credential_check_failed":    kindReadOnly,
	"connection_credential_check_recovered": kindReadOnly,

	// Fan-out endpoints.
	"cluster_adopted":    kindBatch,
	"cluster_registered": kindBatch,
}

// validateLifecycleEntry applies the ruling's rules to one entry and returns
// every violation. The rules are the product owner's words, one function per
// sentence he wrote.
func validateLifecycleEntry(e audit.Entry) []string {
	var bad []string
	kind, known := lifecycleEventCatalog[e.Event]
	if !known {
		return []string{"event " + e.Event + " is not in the lifecycle catalog"}
	}

	// "title, outcome and change result must never contradict each other"
	switch kind {
	case kindCompletedWrite:
		// "a genuinely failed check must have a failure-shaped title" and
		// "a successful repair must not retain a failure title" — the same
		// rule from both ends: a past-tense title is a success or it is the
		// wrong title.
		if e.Result != "success" {
			bad = append(bad, "past-tense title "+e.Event+" carries outcome "+e.Result+
				" — a title claiming work landed may only ever be a success")
		}
		// A completed write says whether it wrote. "No changes made" is an
		// ACTION RESULT here, and a legitimate one.
		if e.Changes != audit.ChangesApplied && e.Changes != audit.ChangesNone {
			bad = append(bad, "completed write "+e.Event+" has changes "+string(e.Changes)+
				" — it must say applied or none, never not_applicable and never unset")
		}
	case kindFailureShaped:
		if e.Result == "success" {
			bad = append(bad, "failure-shaped title "+e.Event+" carries outcome success")
		}
		if e.Changes == audit.ChangesNotApplicable {
			bad = append(bad, "failure-shaped title "+e.Event+
				" claims not_applicable — an attempted write either landed something or it did not")
		}
	case kindRefusal:
		if e.Result == "success" {
			bad = append(bad, "refusal "+e.Event+" carries outcome success — Sharko declined to act")
		}
		if e.Changes != audit.ChangesNone {
			bad = append(bad, "refusal "+e.Event+" has changes "+string(e.Changes)+
				" — a refusal touches nothing")
		}
	case kindReadOnly:
		// "if the event model has only success/failure, a completed check
		// that correctly finds drift is SUCCESS — the drift state itself
		// supplies the warning."
		if e.Changes != audit.ChangesNotApplicable {
			bad = append(bad, "read-only event "+e.Event+" has changes "+string(e.Changes)+
				" — a check neither changed anything nor failed to")
		}
		if strings.HasSuffix(e.Event, "_failed") {
			if e.Result != "failure" {
				bad = append(bad, "check-failure event "+e.Event+" carries outcome "+e.Result)
			}
		} else if e.Result != "success" {
			bad = append(bad, "read-only event "+e.Event+" carries outcome "+e.Result+
				" — a check that ran to completion is a success, whatever it found")
		}
	case kindBatch:
		switch e.Result {
		case "success", "partial", "failure":
		default:
			bad = append(bad, "batch event "+e.Event+" carries outcome "+e.Result)
		}
		if e.Result == "failure" && e.Changes == audit.ChangesApplied {
			bad = append(bad, "batch event "+e.Event+" says every item failed yet claims changes were applied")
		}
		if e.Result == "success" && e.Changes == audit.ChangesNone {
			bad = append(bad, "batch event "+e.Event+" says success yet claims nothing changed")
		}
	}

	// The level must not disagree with the outcome either — it is part of
	// how a reader sees the result.
	if e.Result == "failure" && e.Level == "info" {
		bad = append(bad, e.Event+" records a failure at info level")
	}
	return bad
}

// TestLifecycleEvents_EveryProducibleCombination is the table the ruling
// asked for: every (event × outcome × changes) row the writers can actually
// produce, each one checked against the rules AND against what the code does.
//
// The rows were derived by reading every audit write site in
// internal/clusterreconciler and internal/api, not from a naive enumeration
// of the enums — which is why the negatives at the bottom exist.
func TestLifecycleEvents_EveryProducibleCombination(t *testing.T) {
	rows := []struct {
		what    string
		entry   audit.Entry
		wantBad bool
	}{
		// ── The reconciler's Secret lifecycle ────────────────────────────
		{"a Secret really was created", audit.Entry{Event: clusterreconciler.EventClusterSecretCreate, Level: "info", Result: "success", Changes: audit.ChangesApplied}, false},
		{"the create threw — no Secret exists (C1)", audit.Entry{Event: "cluster_secret_create_failed", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},
		{"a Secret really was deleted", audit.Entry{Event: clusterreconciler.EventClusterSecretDelete, Level: "info", Result: "success", Changes: audit.ChangesApplied}, false},
		{"the delete threw (C7)", audit.Entry{Event: "cluster_secret_delete_failed", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},
		{"the guest's addon labels really were synced", audit.Entry{Event: clusterreconciler.EventClusterSecretUserLabelSync, Level: "info", Result: "success", Changes: audit.ChangesApplied}, false},
		{"the guest label sync threw (C7)", audit.Entry{Event: "cluster_secret_user_label_sync_failed", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},
		{"self-heal converged the labels", audit.Entry{Event: clusterreconciler.EventClusterSecretManagedSelfHeal, Level: "info", Result: "success", Changes: audit.ChangesApplied}, false},
		{"self-heal threw before writing (C6)", audit.Entry{Event: "cluster_secret_managed_self_heal_failed", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},
		{"self-heal WROTE but did not converge — the subtle one (C6)", audit.Entry{Event: "cluster_secret_managed_self_heal_failed", Level: "error", Result: "failure", Changes: audit.ChangesApplied}, false},

		// ── Repair, from the reconciler ──────────────────────────────────
		{"the repair rewrote fields", audit.Entry{Event: clusterreconciler.EventClusterConnectionRepair, Level: "info", Result: "success", Changes: audit.ChangesApplied}, false},
		{"the repair ran and nothing needed changing (C3)", audit.Entry{Event: clusterreconciler.EventClusterConnectionRepair, Level: "info", Result: "success", Changes: audit.ChangesNone}, false},
		{"the repair threw", audit.Entry{Event: "cluster_connection_repair_failed", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},

		// ── Repair, from the API handler ─────────────────────────────────
		{"somebody asked for a repair", audit.Entry{Event: "cluster_connection_repair_requested", Level: "info", Result: "success"}, false},
		{"the repair was refused — 15 paths (C4/C5)", audit.Entry{Event: "cluster_connection_repair_refused", Level: "warn", Result: "rejected", Changes: audit.ChangesNone}, false},
		{"the repair's write path failed", audit.Entry{Event: "cluster_connection_repair_failed", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},

		// ── The background credential check (C2, the primary target) ─────
		{"drift DETECTED — a check that succeeded", audit.Entry{Event: "connection_credential_drift_detected", Level: "warn", Result: "success", Changes: audit.ChangesNotApplicable}, false},
		{"drift cleared", audit.Entry{Event: "connection_credential_drift_cleared", Level: "info", Result: "success", Changes: audit.ChangesNotApplicable}, false},
		{"the check itself did not finish", audit.Entry{Event: "connection_credential_check_failed", Level: "error", Result: "failure", Changes: audit.ChangesNotApplicable}, false},
		{"the check completes again", audit.Entry{Event: "connection_credential_check_recovered", Level: "info", Result: "success", Changes: audit.ChangesNotApplicable}, false},

		// ── Fan-out endpoints (C8/C9) ────────────────────────────────────
		{"every adoption succeeded", audit.Entry{Event: "cluster_adopted", Level: "info", Result: "success", Changes: audit.ChangesApplied}, false},
		{"some adoptions failed", audit.Entry{Event: "cluster_adopted", Level: "warn", Result: "partial", Changes: audit.ChangesApplied}, false},
		{"EVERY adoption failed (C8)", audit.Entry{Event: "cluster_adopted", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},
		{"every registration succeeded", audit.Entry{Event: "cluster_registered", Level: "info", Result: "success", Changes: audit.ChangesApplied}, false},
		{"some registrations failed", audit.Entry{Event: "cluster_registered", Level: "warn", Result: "partial", Changes: audit.ChangesApplied}, false},
		{"EVERY registration failed (C9)", audit.Entry{Event: "cluster_registered", Level: "error", Result: "failure", Changes: audit.ChangesNone}, false},

		// ── The rows the code CANNOT produce, pinned as negatives ────────
		// These are exactly the shapes that shipped. If any of them ever
		// validates again, the contradiction is back.
		{"NEGATIVE: Secret created · failure — the C1 shape", audit.Entry{Event: clusterreconciler.EventClusterSecretCreate, Level: "error", Result: "failure", Changes: audit.ChangesNone}, true},
		{"NEGATIVE: Connection repaired · failure", audit.Entry{Event: clusterreconciler.EventClusterConnectionRepair, Level: "error", Result: "failure", Changes: audit.ChangesNone}, true},
		{"NEGATIVE: drift detected · failure — the C2 shape", audit.Entry{Event: "connection_credential_drift_detected", Level: "warn", Result: "failure", Changes: audit.ChangesNotApplicable}, true},
		{"NEGATIVE: Connection repaired · rejected — the C4 shape", audit.Entry{Event: clusterreconciler.EventClusterConnectionRepair, Level: "warn", Result: "rejected", Changes: audit.ChangesNone}, true},
		{"NEGATIVE: a read-only check claiming it changed something", audit.Entry{Event: "connection_credential_drift_detected", Level: "warn", Result: "success", Changes: audit.ChangesApplied}, true},
		{"NEGATIVE: a refusal claiming it changed something", audit.Entry{Event: "cluster_connection_repair_refused", Level: "warn", Result: "rejected", Changes: audit.ChangesApplied}, true},
		{"NEGATIVE: a _failed event recorded as a success", audit.Entry{Event: "cluster_secret_create_failed", Level: "error", Result: "success", Changes: audit.ChangesNone}, true},
		{"NEGATIVE: all adoptions failed yet changes were applied", audit.Entry{Event: "cluster_adopted", Level: "error", Result: "failure", Changes: audit.ChangesApplied}, true},
		{"NEGATIVE: a failure logged at info level", audit.Entry{Event: "cluster_secret_create_failed", Level: "info", Result: "failure", Changes: audit.ChangesNone}, true},
	}

	for _, row := range rows {
		t.Run(row.what, func(t *testing.T) {
			bad := validateLifecycleEntry(row.entry)
			if row.wantBad && len(bad) == 0 {
				t.Fatalf("this contradiction was ACCEPTED — the rules no longer reject %q · %s · %s",
					row.entry.Event, row.entry.Result, row.entry.Changes)
			}
			if !row.wantBad && len(bad) > 0 {
				t.Fatalf("a shape the code really produces was rejected: %v", bad)
			}
		})
	}
}

// TestLifecycleEvents_TheBreakTestTheProductOwnerAskedForByName pairs a
// success-shaped (past-tense) title with a failure outcome and proves the
// suite rejects it.
//
// This is the assertion that would have caught the shipped defect. It is
// written as its own test, separate from the table, because the product
// owner named it specifically and a row buried in a table is easy to delete
// without anyone noticing what it was for.
func TestLifecycleEvents_TheBreakTestTheProductOwnerAskedForByName(t *testing.T) {
	successShapedTitles := []string{
		clusterreconciler.EventClusterSecretCreate,
		clusterreconciler.EventClusterSecretDelete,
		clusterreconciler.EventClusterSecretUserLabelSync,
		clusterreconciler.EventClusterSecretManagedSelfHeal,
		clusterreconciler.EventClusterConnectionRepair,
	}
	failureOutcomes := []string{"failure", "rejected", "partial"}

	for _, title := range successShapedTitles {
		for _, outcome := range failureOutcomes {
			entry := audit.Entry{Event: title, Level: "error", Result: outcome, Changes: audit.ChangesNone}
			if bad := validateLifecycleEntry(entry); len(bad) == 0 {
				t.Fatalf("the suite ACCEPTED %q · %s — a past-tense title paired with a non-success outcome is exactly the contradiction ruling (f) removed",
					title, outcome)
			}
		}
	}
}

// TestLifecycleEvents_CatalogCoversEveryWriter is the guard against the
// catalog going stale. Every lifecycle event name written anywhere in the Go
// tree must be classified here — otherwise a new writer can reintroduce a
// contradiction in an event the rules simply do not look at.
func TestLifecycleEvents_CatalogCoversEveryWriter(t *testing.T) {
	// The events this ruling governs, by prefix. Anything matching that is
	// NOT in the catalog is a gap.
	prefixes := []string{
		"cluster_secret_create", "cluster_secret_delete",
		"cluster_secret_user_label_sync", "cluster_secret_managed_self_heal",
		"cluster_connection_repair", "connection_credential_",
	}
	found := writtenEventNames(t)
	for _, name := range found {
		governed := false
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				governed = true
				break
			}
		}
		if !governed {
			continue
		}
		if _, ok := lifecycleEventCatalog[name]; !ok {
			t.Errorf("event %q is written by the code but not classified in lifecycleEventCatalog — the rules do not look at it, so it can carry any contradiction it likes", name)
		}
	}
	if len(found) < 20 {
		t.Fatalf("the writer scan found only %d event names — it has lost its reach", len(found))
	}
}

// writtenEventNames scans the Go tree for audit event names the code
// actually writes — both `Event: "literal"` and `Event: SomeConstant` where
// the constant's own declaration carries the literal. It is the input to the
// catalog-coverage guard above.
func writtenEventNames(t *testing.T) []string {
	t.Helper()
	root := repoRootForSweep(t)

	// ANY snake_case string literal in these packages. Deliberately broad:
	// event names reach audit.Entry three different ways — `Event: "x"`, a
	// named constant, and a plain argument to a helper — and a scanner that
	// only knew the first shape missed cluster_connection_repair_refused,
	// which is passed as an argument. The caller filters by the governed
	// prefixes, which are specific enough that a false positive would have
	// to be a string deliberately named like a lifecycle event.
	eventLiteral := regexp.MustCompile(`"([a-z][a-z0-9_]{4,})"`)

	seen := map[string]bool{}
	for _, dir := range []string{"internal/api", "internal/clusterreconciler"} {
		walkErr := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, m := range eventLiteral.FindAllStringSubmatch(string(body), -1) {
				seen[m[1]] = true
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", dir, walkErr)
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestLifecycleEvents_RealWritersProduceValidEntries closes the gap a table
// alone leaves: the table proves the RULES are right, this proves the CODE
// obeys them. It drives the real background credential check through its
// four transitions against real fixtures and validates every entry it
// writes.
//
// Without this, the table would be a well-argued document about code that
// might do something else entirely.
func TestLifecycleEvents_RealWritersProduceValidEntries(t *testing.T) {
	srv, _, argoClient := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	seedSyncedLiveSecret(t, srv, argoClient)
	loop := newTestCheckLoop(srv)
	ctx := context.Background()

	// Clean, then drifted, then a backend outage, then recovered — all four
	// transitions in one run.
	loop.runOnce(ctx)
	driftLiveSecret(t, argoClient)
	loop.runOnce(ctx)
	installCredProvider(srv, comparisonFakeVault{failWith: errors.New("backend down")}, nil, nil)
	loop.runOnce(ctx)
	installCredProvider(srv, comparisonFakeVault{}, nil, nil)
	loop.runOnce(ctx)
	seedSyncedLiveSecret(t, srv, argoClient)
	loop.runOnce(ctx)

	checked := 0
	for _, e := range srv.AuditLog().List(0) {
		if _, governed := lifecycleEventCatalog[e.Event]; !governed {
			continue
		}
		checked++
		if bad := validateLifecycleEntry(e); len(bad) > 0 {
			t.Errorf("the real writer produced a contradictory entry %+v: %v", e, bad)
		}
	}
	if checked < 3 {
		t.Fatalf("only %d governed entries were produced — this test is not exercising the writers", checked)
	}
	t.Logf("validated %d entries from the real credential-check writer", checked)
}
