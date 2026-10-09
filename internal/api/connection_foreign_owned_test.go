package api

// connection_foreign_owned_test.go — the state-model proof for a connection
// another tool has marked as its own.
//
// # What went wrong, and why nothing caught it
//
// connectioncompare.Compare threw away the policy's own ownership sentence and
// substituted a hand-written literal of its own. That literal was
// byte-identical to this package's modeStatementForeignOwned — by accident,
// not by any shared symbol — and that accidental equality was LOAD-BEARING:
// it was the only reason the page's de-duplication fired and the ownership row
// rendered its short fact instead of repeating the mode statement. One
// character of drift in either literal, in either package, and the page
// silently printed the same sentence twice.
//
// No test could catch it. The one sweep that reads both fields
// (TestConnectionReconciliation_NoConditionRepeatsTheReason) asserts a
// condition never EQUALS sync.reason — so pulling the two sentences apart made
// it pass MORE easily. A guard that gets weaker as the bug gets worse is worse
// than no guard, because it reads like coverage.
//
// # The ruling
//
// Presentation structure must follow typed facts, never equality between human
// sentences. So: no ownership condition row at all (the mode statement states
// the boundary and plan.action offers the take-over — a third sentence adds no
// fact and no decision), and both hand-written literals are gone.
//
// The tests below are the break tests for that ruling. Tests 1 and 2 are the
// state-model proof: they assert that rendering does not depend on what any
// sentence SAYS. Both failed before the change.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
)

// --- the two tuples, built through the REAL classifier and comparison --------

// foreignOwnedTuple runs the real Classify + Compare and maps the result onto
// the view exactly as the handler does, so these tests cannot pass on a
// fixture the server could never produce.
//
// checkFailure empty gives TUPLE A (an ordinary foreign-owned connection);
// non-empty gives TUPLE B (the same connection, plus a failure the caller
// already hit — Compare's failure exit fires BEFORE its ownership exit).
func foreignOwnedTuple(checkFailure connectioncompare.CheckFailure) connectionReconciliationView {
	policy := connectioncompare.Classify(connectioncompare.ClassifyInput{
		CredsSource:                  models.CredsSourceSecretKubeconfig,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                "some-other-tool",
	})
	res := connectioncompare.Compare(connectioncompare.Request{
		ClusterName:      comparisonCluster,
		Namespace:        "argocd",
		Policy:           policy,
		Live:             foreignOwnedLiveSecret(),
		LiveFound:        true,
		AddonLabelsKnown: true,
		CheckFailure:     checkFailure,
	})
	v := reconView(func(v *connectionComparisonView) {})
	v.policy = policy
	v = finishView(v, res)
	return buildRecon(v, false)
}

// countSentence counts how many times one exact sentence appears anywhere a
// person reads on this page.
func countSentence(out connectionReconciliationView, sentence string) int {
	if sentence == "" {
		return 0
	}
	n := 0
	for _, text := range []string{
		out.ModeStatement, out.Sync.Reason, out.Sync.Headline, out.Sync.Qualifier,
		out.Plan.Automatic, out.Plan.RequiresApproval,
	} {
		if text == sentence {
			n++
		}
	}
	for _, c := range out.Conditions {
		if c.Detail == sentence {
			n++
		}
	}
	return n
}

func foreignConditionRows(out connectionReconciliationView) []string {
	ids := make([]string, 0, len(out.Conditions))
	for _, c := range out.Conditions {
		ids = append(ids, c.ID+"/"+c.Status)
	}
	return ids
}

// --- BREAK TEST 1 + 2 — the state-model proof --------------------------------

// TestConnectionReconciliation_ForeignRenderingIgnoresSentenceText is break
// tests 1 and 2 together, because they are one property:
//
//	Changing what a sentence SAYS must not change which rows render.
//
// It holds the typed state fixed and varies only the WORDS the comparison
// hands over — including the two cases that matter most: a sentence that is
// exactly the mode statement (the accidental byte-identity that used to exist)
// and one that differs from it by a single word (the drift that used to break
// the page). If any of those changes the rendered conditions, rendering is
// keyed on text and this fails.
//
// IT FAILED BEFORE THE CHANGE. With limit_reason equal to the mode statement
// the ownership row rendered its short fact; with any other value the same
// typed state rendered the mode statement instead — two different pages from
// one state, decided purely by whether two strings matched.
func TestConnectionReconciliation_ForeignRenderingIgnoresSentenceText(t *testing.T) {
	// The same words, one word apart, and nothing alike — every shape the
	// drift could take.
	sentenceVariants := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"exactly the mode statement", modeStatementForeignOwned},
		{"the mode statement, one word changed", strings.Replace(modeStatementForeignOwned, "marked", "reported", 1)},
		{"the mode statement plus a trailing word", modeStatementForeignOwned + " Really."},
		{"nothing like the mode statement", "A completely unrelated sentence."},
	}

	// Held fixed inside each group: the typed state. Varied across the group:
	// only the words.
	for _, status := range []connectioncompare.Status{
		connectioncompare.StatusOwnershipConflict,
		connectioncompare.StatusCheckFailed,
	} {
		for _, failure := range []string{failBackendRead, failGitRead, failLiveRead, "an unclassified failure"} {
			var baselineIDs []string
			var baselineConds []connectionReconciliationCondition
			var baselineName string

			for _, variant := range sentenceVariants {
				out := buildRecon(reconView(func(v *connectionComparisonView) {
					v.Status = string(status)
					v.Scope = string(connectioncompare.ScopeOwnershipConflict)
					v.OwnershipMode = string(connectioncompare.ModeForeignOwned)
					v.LimitReason = variant.text
					v.FailureReason = failure
					v.RepairAvailable = false
					v.RepairScope = string(connectioncompare.RepairScopeNone)
				}), false)

				// BREAK TEST 2 — no sentence, in any shape, may bring the
				// ownership row into existence or take it away.
				for _, c := range out.Conditions {
					if c.ID == conditionOwnership {
						t.Errorf("status=%s failure=%.20q limit=%q produced an ownership condition row (detail %q).\n"+
							"The foreign-owned page states the ownership boundary once, in mode_statement, and offers "+
							"the decision once, in plan.action. There is no third place for it.",
							status, failure, variant.name, c.Detail)
					}
				}

				ids := foreignConditionRows(out)
				if baselineIDs == nil {
					baselineIDs, baselineConds, baselineName = ids, out.Conditions, variant.name
					continue
				}

				// BREAK TEST 1 — same typed state, different words: the rows
				// must be byte-for-byte the same.
				if strings.Join(ids, ",") != strings.Join(baselineIDs, ",") {
					t.Errorf("status=%s failure=%.20q: the rendered rows CHANGED when only a sentence's words changed.\n"+
						"  with %s: %v\n  with %s: %v\n"+
						"Rendering is keyed on sentence text, which is exactly what the ruling forbids.",
						status, failure, baselineName, baselineIDs, variant.name, ids)
					continue
				}
				for i := range out.Conditions {
					if out.Conditions[i] != baselineConds[i] {
						t.Errorf("status=%s failure=%.20q: condition %d changed when only a sentence's words changed.\n"+
							"  with %s: %+v\n  with %s: %+v",
							status, failure, i, baselineName, baselineConds[i], variant.name, out.Conditions[i])
					}
				}
			}
		}
	}
}

// --- BREAK TEST 3 — tuple A --------------------------------------------------

// TestConnectionReconciliation_TupleA_ForeignOwnedSaysItOnce pins the ordinary
// foreign-owned connection: the boundary is stated exactly once, there is no
// ownership row, and the take-over action survives.
func TestConnectionReconciliation_TupleA_ForeignOwnedSaysItOnce(t *testing.T) {
	out := foreignOwnedTuple("")

	if out.ManagementMode != managementModeForeignOwned {
		t.Fatalf("management_mode = %q, want %q", out.ManagementMode, managementModeForeignOwned)
	}
	if out.Sync.State != syncStateBlocked {
		t.Errorf("sync.state = %q, want %q", out.Sync.State, syncStateBlocked)
	}
	if out.ManagedScope != managedScopeNone {
		t.Errorf("managed_scope = %q, want %q", out.ManagedScope, managedScopeNone)
	}

	if n := countSentence(out, modeStatementForeignOwned); n != 1 {
		t.Errorf("the management-mode statement appears %d time(s) on the page, want exactly 1.\n"+
			"conditions: %+v\nsync.reason: %q", n, out.Conditions, out.Sync.Reason)
	}
	for _, c := range out.Conditions {
		if c.ID == conditionOwnership {
			t.Errorf("tuple A produced an ownership condition row: %+v", c)
		}
	}
	// The action is the decision, and it must survive the row's removal.
	if out.Plan.Action != planActionTakeOver {
		t.Errorf("plan.action = %q, want %q", out.Plan.Action, planActionTakeOver)
	}
	// And no prose beside the action repeating what the action already offers.
	if out.Plan.RequiresApproval != "" {
		t.Errorf("plan.requires_approval = %q, want empty — the take-over action carries the decision", out.Plan.RequiresApproval)
	}
}

// --- BREAK TEST 4 — tuple B --------------------------------------------------

// TestConnectionReconciliation_TupleB_ForeignOwnedPlusFailedRead pins the tuple
// that was in no matrix and tested nowhere: the same foreign-owned connection,
// plus a credentials-backend read that failed.
//
// Compare's FAILURE exit fires before its ownership exit, so the answer is
// check_failed wearing a foreign mode. The page used to swallow that entirely —
// it printed the mode statement a SECOND time as the ownership row and never
// mentioned the read that actually failed.
func TestConnectionReconciliation_TupleB_ForeignOwnedPlusFailedRead(t *testing.T) {
	out := foreignOwnedTuple(connectioncompare.CheckFailureBackendRead)

	if out.ManagementMode != managementModeForeignOwned {
		t.Fatalf("management_mode = %q, want %q", out.ManagementMode, managementModeForeignOwned)
	}
	if out.Sync.State != syncStateUnknown {
		t.Errorf("sync.state = %q, want %q — a check that could not finish is never blocked or synced",
			out.Sync.State, syncStateUnknown)
	}

	// Exactly one management-mode statement, same sentence as tuple A.
	if n := countSentence(out, modeStatementForeignOwned); n != 1 {
		t.Errorf("the management-mode statement appears %d time(s) on the page, want exactly 1.\n"+
			"conditions: %+v", n, out.Conditions)
	}
	if out.ModeStatement != modeStatementForeignOwned {
		t.Errorf("mode_statement = %q, want the same sentence tuple A carries", out.ModeStatement)
	}

	// Exactly one backend-read failure, and it stays in sync.reason.
	if out.Sync.Reason != failBackendRead {
		t.Errorf("sync.reason = %q, want the backend-read failure unchanged", out.Sync.Reason)
	}
	if n := countSentence(out, failBackendRead); n != 1 {
		t.Errorf("the backend-read failure appears %d time(s) on the page, want exactly 1", n)
	}

	// No ownership row, and the failed step IS named as a condition.
	namedTheFailedStep := false
	for _, c := range out.Conditions {
		if c.ID == conditionOwnership {
			t.Errorf("tuple B produced an ownership condition row: %+v", c)
		}
		if c.ID == conditionCredentialReference && c.Status == conditionStatusBlocked {
			namedTheFailedStep = true
		}
	}
	if !namedTheFailedStep {
		t.Errorf("tuple B names no blocked credential_reference condition, so the page never says which step failed.\n"+
			"conditions: %+v", out.Conditions)
	}
	// The take-over action survives a failed read too.
	if out.Plan.Action != planActionTakeOver {
		t.Errorf("plan.action = %q, want %q", out.Plan.Action, planActionTakeOver)
	}
}

// --- BREAK TEST 7 — the pattern cannot come back -----------------------------

// saidOnceAllowedFallbacks is the COMPLETE list of condition rows still allowed
// to decide what they render by comparing two sentences.
//
// It is a LIST, never a count. A count would pass while one site was swapped
// for another, and would tell whoever hit it nothing about what to do.
//
// Every entry is a KNOWN DEFECT waiting on a product ruling, not an approved
// pattern. Each one genuinely varies — driving the builder across the whole
// cross product shows each rendering both its fallback fact AND a pass-through
// sentence — so converting them changes what a reader sees, and that is a
// product decision rather than an implementer's. Adding to this list is not a
// way to make a new one legal; it is a way to record a new one that has been
// ruled on.
var saidOnceAllowedFallbacks = map[string]string{
	"condCredentialRefUnknownSource": "credential_reference, unknown recorded source — varies with status",
	"condCredentialRefUnreadable":    "credential_reference, backend not readable right now — varies with status",
	"condComparisonPartial":          "comparison, limited scope — varies when a difference rides with a limited status",
	"condApprovalNoRepair":           "approval, repair door withheld — varies with status",
}

// TestConnectionReconciliation_NoTextEqualityDeduplication is break test 7: it
// stops the pattern coming back.
//
// It reads the source rather than the behaviour, because that is where the
// defect lives. Two things fail it:
//
//  1. a saidOnce call whose fallback is not on the list above — including a new
//     one on the foreign-owned path, which is how this whole defect started;
//  2. ANY comparison of a mode statement's text anywhere in the package, which
//     is the specific move that made two packages' literals load-bearing.
func TestConnectionReconciliation_NoTextEqualityDeduplication(t *testing.T) {
	root := repoRootForSweep(t)
	fset := token.NewFileSet()
	path := filepath.Join(root, "internal/api/connection_reconciliation.go")
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing connection_reconciliation.go: %v", err)
	}

	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "saidOnce" {
			return true
		}
		line := fset.Position(call.Pos()).Line
		if len(call.Args) != 3 {
			t.Errorf("connection_reconciliation.go:%d: saidOnce called with %d arguments, want 3", line, len(call.Args))
			return true
		}
		fallback, ok := call.Args[2].(*ast.Ident)
		if !ok {
			t.Errorf("connection_reconciliation.go:%d: saidOnce's fallback is not a named constant", line)
			return true
		}
		found = append(found, fallback.Name)
		if _, allowed := saidOnceAllowedFallbacks[fallback.Name]; !allowed {
			t.Errorf("connection_reconciliation.go:%d: a NEW text-equality de-duplication decides the %s row.\n"+
				"Presentation structure must follow typed facts, never equality between human sentences.\n"+
				"Drive this row from typed state. If it genuinely cannot be, it needs a product ruling and an\n"+
				"entry in saidOnceAllowedFallbacks — the list is a record of decisions, not a way to make this legal.",
				line, fallback.Name)
		}
		return true
	})

	// The declarations must stay true, or the list becomes where coverage goes
	// to die.
	seen := map[string]bool{}
	for _, f := range found {
		seen[f] = true
	}
	var stale []string
	for name := range saidOnceAllowedFallbacks {
		if !seen[name] {
			stale = append(stale, "  "+name+" — "+saidOnceAllowedFallbacks[name])
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("saidOnceAllowedFallbacks names %d row(s) that no longer compare sentences.\n"+
			"They were fixed — remove them from the list so it keeps naming only what is still outstanding:\n%s",
			len(stale), strings.Join(stale, "\n"))
	}

	// 2. No mode statement's TEXT may be compared, anywhere in this package.
	// This is the exact move that made a literal in internal/connectioncompare
	// load-bearing for a rendering decision in internal/api.
	files, err := parser.ParseDir(fset, filepath.Join(root, "internal/api"), func(fi fs.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing internal/api: %v", err)
	}
	for _, pkg := range files {
		for name, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				bin, ok := n.(*ast.BinaryExpr)
				if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
					return true
				}
				for _, side := range []ast.Expr{bin.X, bin.Y} {
					id, ok := side.(*ast.Ident)
					if !ok || !strings.HasPrefix(id.Name, "modeStatement") {
						continue
					}
					t.Errorf("%s:%d: %s's TEXT is compared to decide something.\n"+
						"A mode statement is prose for a person to read, never an identifier, routing key or\n"+
						"de-duplication signal. Key the decision on the typed management mode instead.",
						filepath.Base(name), fset.Position(bin.Pos()).Line, id.Name)
				}
				return true
			})
		}
	}
}
