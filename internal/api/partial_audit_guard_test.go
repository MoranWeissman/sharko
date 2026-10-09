package api

// partial_audit_guard_test.go — R2-8.
//
// A fan-out operation's item can come back three ways, not two: success,
// failed, and "partial" — it stopped halfway and NOTHING was rolled back, so
// the pull request is merged and the Secrets are written and real things
// changed. Every handler that folded those three into a two-answer audit line
// got it wrong, and the two that shipped were wrong in opposite directions:
//
//   - batch register folded partial into FAILURE, so an all-partial batch read
//     as "it all failed, nothing changed" while pull requests had merged (R2-7);
//   - adopt folded partial into SUCCESS, so an all-partial adoption read as
//     "Cluster adopted · success · changes applied" — full completion claimed
//     for work that did not complete (R2-8).
//
// This file closes the CLASS. Three layers, each one a LIST rather than a
// count, so it fails when a new site appears AND when a listed entry goes
// stale. A count-based floor answers "did I see enough?", which passes quietly
// the day somebody restructures the file, and gets HAPPIER as the bug spreads.
//
//	Layer 1  every fan-out decider, driven across every shape of result the
//	         orchestrator can produce, checked in BOTH directions.
//	Layer 2  every function in internal/api that writes an audit Result must
//	         be listed here and classified.
//	Layer 3  every function in internal/api that can see a "partial" must be
//	         listed here and classified, and the ones that leave the answer to
//	         the middleware must still answer 207 for it to derive "partial".

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/lifecycleevents"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// ───────────────────────────── Layer 1 ──────────────────────────────────
//
// The deciders themselves, driven through the REAL production functions.

// adoptShape is one adoption's worth of per-cluster answers.
type adoptShape struct {
	what      string
	succeeded int
	partial   int
	hardFail  int
	other     int
}

// result builds the AdoptClustersResult the orchestrator would return for
// this shape, with the same status strings adopt.go and adopt_v4.go write.
func (s adoptShape) result() *orchestrator.AdoptClustersResult {
	res := &orchestrator.AdoptClustersResult{}
	add := func(n int, status string) {
		for i := 0; i < n; i++ {
			res.Results = append(res.Results, orchestrator.AdoptClusterResult{
				Name:   fmt.Sprintf("%s-%d", status, len(res.Results)),
				Status: status,
			})
		}
	}
	add(s.succeeded, "success")
	add(s.partial, "partial")
	add(s.hardFail, "failed")
	// "skipped" is declared on AdoptClusterResult.Status and no code path
	// produces it today. It stands in here for ANY status a future change
	// invents: the point of the Other bucket is that an unrecognised answer
	// must never be read as a clean success.
	add(s.other, "skipped")
	return res
}

// everyAdoptShape is every combination of outcomes an adoption of up to three
// clusters can come back as, including the unrecognised-status one.
// Enumerated rather than hand-listed so nobody has to remember the awkward
// combination — the same reason everyBatchShape enumerates.
func everyAdoptShape() []adoptShape {
	var shapes []adoptShape
	for total := 1; total <= 3; total++ {
		for succeeded := 0; succeeded <= total; succeeded++ {
			for partial := 0; partial <= total-succeeded; partial++ {
				for hardFail := 0; hardFail <= total-succeeded-partial; hardFail++ {
					other := total - succeeded - partial - hardFail
					shapes = append(shapes, adoptShape{
						what: fmt.Sprintf("%d adopted, %d partial, %d failed outright, %d unrecognised",
							succeeded, partial, hardFail, other),
						succeeded: succeeded,
						partial:   partial,
						hardFail:  hardFail,
						other:     other,
					})
				}
			}
		}
	}
	return shapes
}

// TestAdopt_AuditSaysWhatActuallyHappened is the R2-8 guard. It fails on the
// shipped defect — a partial folded into success — and it fails just as hard
// on an over-correction that calls an all-failed adoption a partial.
func TestAdopt_AuditSaysWhatActuallyHappened(t *testing.T) {
	for _, shape := range everyAdoptShape() {
		t.Run(shape.what, func(t *testing.T) {
			res := shape.result()
			gotResult, gotChanges := adoptAuditOutcome(res, false)

			// The entry as it would be written, level included — the
			// middleware derives the level from the result.
			entry := audit.Entry{
				Event:   lifecycleevents.ClusterAdopted,
				Level:   levelFromResult(gotResult),
				Result:  gotResult,
				Changes: gotChanges,
			}
			if bad := validateLifecycleEntry(entry); len(bad) > 0 {
				t.Fatalf("the entry this adoption produces contradicts itself: %v", bad)
			}

			everythingFinished := shape.succeeded == shape.succeeded+shape.partial+shape.hardFail+shape.other
			anythingLanded := shape.succeeded > 0 || shape.partial > 0 || shape.other > 0

			// ── The direction that shipped: false success ────────────────
			if !everythingFinished && gotResult == "success" {
				t.Errorf("R2-8 IS BACK: %s, so the adoption did NOT finish for every cluster — "+
					"and the audit trail says %q. It claims full completion for work that did not complete, "+
					"which is the direction that hides the problem.", shape.what, gotResult)
			}
			if !everythingFinished && gotResult == "success" && (gotChanges == audit.ChangesApplied || gotChanges == audit.ChangesMayBeApplied) {
				t.Errorf("R2-8 IS BACK: %s, recorded as success with changes applied.", shape.what)
			}

			// ── And the mirror image, R2-7's shape, must not appear here ─
			if anythingLanded {
				if gotResult == "failure" {
					t.Errorf("%s — real work landed, so the audit trail must not say %q", shape.what, gotResult)
				}
				if gotChanges == audit.ChangesNone {
					t.Errorf("%s — real work landed and nothing was rolled back, "+
						"yet the audit trail claims nothing changed", shape.what)
				}
			}

			// ── The honest all-failed case still says so ─────────────────
			if !anythingLanded {
				if gotResult != "failure" {
					t.Errorf("%s — nothing landed anywhere, so the audit trail must say failure, got %q",
						shape.what, gotResult)
				}
				if gotChanges != audit.ChangesNone {
					t.Errorf("%s — nothing landed anywhere, so changes must be none, got %q",
						shape.what, gotChanges)
				}
			}

			// ── The exact wording, so neither end can drift ──────────────
			var wantResult string
			var wantChanges audit.ChangeResult
			switch {
			case everythingFinished:
				wantResult, wantChanges = "success", audit.ChangesApplied
			case !anythingLanded:
				wantResult, wantChanges = "failure", audit.ChangesNone
			case shape.partial > 0 || shape.other > 0:
				// R2-9, the product owner's ruling on B2: a cluster that
				// stopped part-way may or may not have left changes behind,
				// and the audit trail says exactly that rather than
				// picking one of the two certainties.
				wantResult, wantChanges = "partial", audit.ChangesMayBeApplied
			default:
				// Some finished, some failed outright, none part-way — at
				// least one cluster completed every step, so changes were
				// definitely applied.
				wantResult, wantChanges = "partial", audit.ChangesApplied
			}
			if gotResult != wantResult || gotChanges != wantChanges {
				t.Errorf("%s → %q · %q, want %q · %q", shape.what, gotResult, gotChanges, wantResult, wantChanges)
			}
		})
	}
}

// TestAdopt_DryRunNeverClaimsAWrite pins the other false claim on the same
// decision: a dry run plans and writes nothing, so it may never be recorded
// as having applied changes. It used to be — every dry run was written down
// as "Cluster adopted · success · changes applied".
func TestAdopt_DryRunNeverClaimsAWrite(t *testing.T) {
	for _, shape := range everyAdoptShape() {
		res := shape.result()
		_, changes := adoptAuditOutcome(res, true)
		if changes != audit.ChangesNotApplicable {
			t.Errorf("dry run (%s) recorded changes %q — a preview writes nothing, "+
				"so the only true answer is not_applicable", shape.what, changes)
		}
	}
}

// TestAdopt_HTTPStatusIsUnchanged pins the response code across every shape.
//
// POST /api/v1/clusters/adopt is a stable endpoint. R2-8 corrected the audit
// trail and NOTHING else: 207 only when at least one cluster failed and at
// least one did not; everything else — including an all-partial adoption and
// an all-failed one — still answers 200, exactly as before.
//
// That this differs from batch registration, which answers 207 for any
// cluster that did not fully succeed, is a known inconsistency and an open
// product-owner question. Changing it is a major version bump.
func TestAdopt_HTTPStatusIsUnchanged(t *testing.T) {
	for _, shape := range everyAdoptShape() {
		// The pre-R2-8 rule, written out independently of the production
		// code: anything not "failed" counted as a success for the status.
		hasFailure := shape.hardFail > 0
		hasSuccess := shape.succeeded+shape.partial+shape.other > 0
		want := http.StatusOK
		if hasFailure && hasSuccess {
			want = http.StatusMultiStatus
		}
		if got := adoptHTTPStatus(shape.result()); got != want {
			t.Errorf("%s → HTTP %d, want %d. The status code was NOT this story's to change — "+
				"POST /api/v1/clusters/adopt is stable and a status change is a major version bump.",
				shape.what, got, want)
		}
	}
}

// TestFanOutDeciders_AllAgreeOnTheRule drives every fan-out audit decider in
// the codebase over the same counts and requires the same answer from each.
//
// A LIST, by name. The two handlers each wrote their own version of one rule
// and that is exactly how they ended up wrong in opposite directions. If a
// third fan-out endpoint appears and writes a third version, adding it here
// is what proves it agrees; leaving it out is caught by Layer 2 below.
func TestFanOutDeciders_AllAgreeOnTheRule(t *testing.T) {
	deciders := []struct {
		name   string
		decide func(fanoutCounts) (string, audit.ChangeResult)
	}{
		{
			name: "batchAuditOutcome (internal/api/clusters_batch.go)",
			decide: func(c fanoutCounts) (string, audit.ChangeResult) {
				// Built with per-cluster Results, because that is what the
				// counting reads now — the older counters are still set,
				// exactly as RegisterClusterBatch sets them, so this is the
				// shape the handler really sees.
				br := &orchestrator.BatchResult{
					Total:     c.Total,
					Succeeded: c.Succeeded,
					Failed:    c.Partial + c.HardFailed,
					Partial:   c.Partial,
				}
				add := func(n int, status string) {
					for i := 0; i < n; i++ {
						br.Results = append(br.Results, orchestrator.RegisterClusterResult{Status: status})
					}
				}
				add(c.Succeeded, "success")
				add(c.Partial, "partial")
				add(c.HardFailed, "failed")
				return batchAuditOutcome(br)
			},
		},
		{
			name: "adoptAuditOutcome (internal/api/clusters_adopt.go)",
			decide: func(c fanoutCounts) (string, audit.ChangeResult) {
				shape := adoptShape{succeeded: c.Succeeded, partial: c.Partial, hardFail: c.HardFailed}
				return adoptAuditOutcome(shape.result(), false)
			},
		},
	}

	for total := 1; total <= 3; total++ {
		for succeeded := 0; succeeded <= total; succeeded++ {
			for partial := 0; partial <= total-succeeded; partial++ {
				c := fanoutCounts{
					Total:      total,
					Succeeded:  succeeded,
					Partial:    partial,
					HardFailed: total - succeeded - partial,
				}
				wantResult, wantChanges := fanoutAuditOutcome(c)
				for _, d := range deciders {
					gotResult, gotChanges := d.decide(c)
					if gotResult != wantResult || gotChanges != wantChanges {
						t.Errorf("%s disagrees with the shared rule on %d succeeded / %d partial / %d failed: "+
							"got %q · %q, the rule says %q · %q. One rule, one place — that is the whole point.",
							d.name, succeeded, partial, c.HardFailed, gotResult, gotChanges, wantResult, wantChanges)
					}
				}
			}
		}
	}
}

// ───────────────────────────── Layer 2 ──────────────────────────────────
//
// Every function in internal/api that decides an audit Result, listed and
// classified. A new one that folds a partial into success or failure has to
// be added here, and adding it means saying which of the three it is.

// auditResultSite is one function that writes an audit entry's Result.
type auditResultSite struct {
	file string
	// how the Result is arrived at, in plain words. Written out per site so
	// adding a new one forces the author to say what it does.
	how string
}

// auditResultWriters is the LIST. Key is the function name.
//
// Anything in internal/api that puts a Result on an audit.Fields or an
// audit.Entry must appear here. A missing entry fails by name; a stale entry
// fails by name too, so this cannot rot into a decoration.
var auditResultWriters = map[string]auditResultSite{
	"handleBatchRegisterClusters": {
		file: "clusters_batch.go",
		how:  "FAN-OUT. Result comes from batchAuditOutcome → fanoutAuditOutcome. Driven by Layer 1.",
	},
	"handleAdoptClusters": {
		file: "clusters_adopt.go",
		how:  "FAN-OUT. Result comes from adoptAuditOutcome → fanoutAuditOutcome. Driven by Layer 1.",
	},
	"record": {
		file: "connection_credential_check.go",
		how: "SINGLE DEFINITE OUTCOME, read-only. One connection's credential check: it ran " +
			"or it did not, and it writes nothing either way. No item list, so no partial to fold.",
	},
	"repairAudit": {
		file: "connection_repair.go",
		how: "SINGLE DEFINITE OUTCOME, already three-way by a different axis " +
			"(refused / failed / completed), each with its own event name. " +
			"One connection, no item list, so no partial to fold.",
	},
	"auditRepairCredentialFailure": {
		file: "connection_repair.go",
		how:  "SINGLE DEFINITE OUTCOME. One classified credential-backend failure. Always a failure.",
	},
	"runInitOperation": {
		file: "init.go",
		how: "SINGLE DEFINITE OUTCOME. Every failing step returns before this line, so the " +
			"entry is only written when init finished. Orchestrator.InitRepo, which is the " +
			"only thing on this endpoint's side that produces a \"partial\", has no caller.",
	},
	"handleGitWebhook": {
		file: "webhooks.go",
		how:  "SINGLE DEFINITE OUTCOME. Verified webhook accepted; failures return earlier.",
	},
	"auditSecretResourceRead": {
		file: "secret_resource.go",
		how:  "SINGLE DEFINITE OUTCOME. A read either returned the object or it did not.",
	},
	"emitSecretLeakAuditBlock": {
		file: "secret_leak_audit.go",
		how:  "SINGLE DEFINITE OUTCOME. The write was blocked. Always \"blocked\".",
	},
	"handleLogin": {
		file: "router.go",
		how:  "SINGLE DEFINITE OUTCOME. One login attempt: it succeeded or it did not.",
	},
	"handleLogout": {
		file: "router.go",
		how:  "SINGLE DEFINITE OUTCOME. One session ended.",
	},
	"auditMiddleware": {
		file: "audit_middleware.go",
		how: "THE FALLBACK. When a handler sets no Result of its own, this derives one from " +
			"the HTTP status via resultFromStatus — which maps 207 to \"partial\". Every " +
			"handler relying on that is listed in Layer 3 as via-207.",
	},
}

// TestAuditResultWriters_AreAllListed fails when a function in internal/api
// starts writing an audit Result without being classified here, and when a
// listed entry no longer exists.
func TestAuditResultWriters_AreAllListed(t *testing.T) {
	files := parseAPIPackage(t)

	found := map[string]string{}
	for _, pf := range files {
		for _, d := range pf.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok || !isAuditEntryOrFieldsType(cl.Type) {
					return true
				}
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Result" {
						found[fd.Name.Name] = filepath.Base(pf.name)
					}
				}
				return true
			})
		}
	}

	for name, file := range found {
		site, listed := auditResultWriters[name]
		if !listed {
			t.Errorf("%s (%s) decides an audit Result and is NOT in auditResultWriters.\n"+
				"Add it, and say which it is: a fan-out that must call fanoutAuditOutcome, "+
				"or a single definite outcome with no item list and therefore no partial to fold.\n"+
				"A fan-out that folds a partial into success or failure is the R2-7/R2-8 defect.", name, file)
			continue
		}
		if site.file != file {
			t.Errorf("%s is listed as living in %s but is in %s — the list has gone stale",
				name, site.file, file)
		}
	}
	for name, site := range auditResultWriters {
		if _, ok := found[name]; !ok {
			t.Errorf("auditResultWriters lists %s (%s) but no such audit-Result writer exists any more. "+
				"Remove the entry — a list that keeps dead names stops being read.", name, site.file)
		}
	}
}

// ───────────────────────────── Layer 3 ──────────────────────────────────
//
// Every function that can SEE a "partial", listed and classified — including
// the ones whose audit result the middleware derives from the HTTP status,
// which only says "partial" if the handler really answers 207.

// partialSite is one function that can see a "partial" value.
type partialSite struct {
	file string
	// via207 marks a handler that writes a partial-status body and leaves the
	// audit result to the middleware. That is only honest while it really
	// answers 207, so the guard checks the function still reaches for
	// http.StatusMultiStatus.
	via207 bool
	how    string
}

// partialAwareFunctions is the LIST of every function in internal/api that
// mentions "partial" — directly or through one of the package's
// partial-valued constants.
//
// LIMITATION, written down rather than left to be discovered: for a via-207
// entry this checks that the function still reaches for
// http.StatusMultiStatus. It does NOT prove the 207 is on the partial branch
// specifically. What it does catch is the shape that shipped in adopt — a
// handler that can answer partial while never producing a 207 at all — and a
// brand-new handler of that shape, which cannot be added without failing the
// "not listed" check first.
var partialAwareFunctions = map[string]partialSite{
	// ── The fan-out deciders and their helpers ──────────────────────────
	"fanoutAuditOutcome": {file: "fanout_audit.go",
		how: "THE RULE ITSELF. Driven by Layer 1."},
	"adoptAuditResource": {file: "clusters_adopt.go",
		how: "Names the clusters the adoption touched. A partial counts as touched — its PR exists."},

	// ── Handlers that leave the answer to the middleware, via 207 ───────
	"handleRegisterCluster": {file: "clusters_write.go", via207: true,
		how: "Single cluster. A partial body answers 207, which resultFromStatus maps to \"partial\"."},
	"handleDeregisterCluster": {file: "clusters_write.go", via207: true,
		how: "Single cluster removal. Partial → 207 → \"partial\"."},
	"handleUpdateClusterAddons": {file: "clusters_write.go", via207: true,
		how: "Single cluster addon update. Partial → 207 → \"partial\"."},
	"handleEnableAddon": {file: "addon_ops.go", via207: true,
		how: "One addon on one cluster. Partial → 207 → \"partial\"."},
	"handleDisableAddon": {file: "addon_ops.go", via207: true,
		how: "One addon on one cluster. Partial → 207 → \"partial\"."},
	"handleUnadoptCluster": {file: "clusters_adopt.go", via207: true,
		how: "Single cluster unadopt. Partial → 207 → \"partial\"."},
	"handleClusterTakeover": {file: "clusters_takeover.go", via207: true,
		how: "Both partial exits write 207 directly, which resultFromStatus maps to \"partial\"."},

	// ── The middleware's own mapping ────────────────────────────────────
	"resultFromStatus": {file: "audit_middleware.go",
		how: "Maps 207 → \"partial\". This mapping is what makes every via-207 entry above honest."},
	"levelFromResult": {file: "audit_middleware.go",
		how: "Maps \"partial\" → warn. Reads the result, never decides it."},

	// ── Not audit outcomes at all ───────────────────────────────────────
	"doctorOverallStatus": {file: "clusters_doctor.go",
		how: "READ-ONLY. A diagnostic summary in the response body. The doctor endpoint's audit " +
			"entry sets no Result and always answers 200 — nothing was written, so there is no " +
			"completion to claim."},
	"classifyBootstrapApp": {file: "init_status.go",
		how: "READ-ONLY probe. \"partial\" is a repo STATE on the wire, not an outcome."},
	"handleInitStatus": {file: "init_status.go",
		how: "READ-ONLY probe. Returns the repo state; writes nothing and decides no audit Result."},
	"runInitOperation": {file: "init.go",
		how: "Reads RepoStatePartial to choose between repair and refusal. Its audit entry is " +
			"written only after init finished — see auditResultWriters."},
	"connectionSyncQualifier": {file: "connection_canonical.go",
		how: "READ-ONLY. Describes how much of a connection was verified. Not an outcome."},
	"verificationScopeForComparison": {file: "connection_reconciliation.go",
		how: "READ-ONLY. Same verification scope, on the comparison path."},
}

// TestPartialAwareFunctions_AreAllListed fails when a function in internal/api
// starts being able to see a "partial" without being classified here, and
// when a listed entry no longer exists.
func TestPartialAwareFunctions_AreAllListed(t *testing.T) {
	files := parseAPIPackage(t)

	// Package-level constants whose value is "partial" — found rather than
	// hand-listed, so a new one cannot smuggle a site past this guard.
	partialConsts := map[string]bool{}
	for _, pf := range files {
		for _, d := range pf.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, v := range vs.Values {
					if isPartialLiteral(v) && i < len(vs.Names) {
						partialConsts[vs.Names[i].Name] = true
					}
				}
			}
		}
	}
	if len(partialConsts) == 0 {
		t.Fatal("no partial-valued constant found in internal/api — this guard is reading nothing. " +
			"Either the package changed shape or the scan is broken; do not treat this as a pass.")
	}

	found := map[string]string{}
	has207 := map[string]bool{}
	for _, pf := range files {
		for _, d := range pf.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			sees := false
			multiStatus := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if isPartialLiteral(x) {
						sees = true
					}
				case *ast.Ident:
					if partialConsts[x.Name] {
						sees = true
					}
				case *ast.SelectorExpr:
					if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "http" && x.Sel.Name == "StatusMultiStatus" {
						multiStatus = true
					}
				}
				return true
			})
			if sees {
				found[fd.Name.Name] = filepath.Base(pf.name)
				has207[fd.Name.Name] = multiStatus
			}
		}
	}

	for name, file := range found {
		site, listed := partialAwareFunctions[name]
		if !listed {
			t.Errorf("%s (%s) can see a \"partial\" and is NOT in partialAwareFunctions.\n"+
				"Add it, and say plainly which it is:\n"+
				"  - a fan-out audit decision → it must call fanoutAuditOutcome, never fold partial "+
				"into success (R2-8) or into failure (R2-7);\n"+
				"  - a handler leaving the answer to the middleware → mark via207, and it really "+
				"must answer 207, or the middleware records a partial as a full success;\n"+
				"  - not an audit outcome at all → say why.", name, file)
			continue
		}
		if site.file != file {
			t.Errorf("%s is listed as living in %s but is in %s — the list has gone stale",
				name, site.file, file)
		}
		if site.via207 && !has207[name] {
			t.Errorf("%s is listed as leaving its audit result to the middleware on the strength of "+
				"answering 207, but it no longer reaches for http.StatusMultiStatus. "+
				"Without the 207 the middleware records a partial as a full success — the R2-8 defect.", name)
		}
	}
	for name, site := range partialAwareFunctions {
		if _, ok := found[name]; !ok {
			t.Errorf("partialAwareFunctions lists %s (%s) but no such function sees a \"partial\" any more. "+
				"Remove the entry — a list that keeps dead names stops being read.", name, site.file)
		}
	}
}

// TestMiddlewareStillMapsPartial is the one assertion that makes every
// via-207 classification above mean something. If 207 ever stopped mapping to
// "partial", each of those handlers would silently record a half-finished
// operation as a full success and every entry above would still look correct.
func TestMiddlewareStillMapsPartial(t *testing.T) {
	if got := resultFromStatus(http.StatusMultiStatus); got != "partial" {
		t.Fatalf("resultFromStatus(207) = %q, want \"partial\". Every handler in "+
			"partialAwareFunctions marked via207 depends on this mapping.", got)
	}
	if got := resultFromStatus(http.StatusOK); got != "success" {
		t.Fatalf("resultFromStatus(200) = %q, want \"success\"", got)
	}
}

// ───────────────────────────── shared parsing ───────────────────────────

type parsedAPIFile struct {
	name string
	file *ast.File
}

// parseAPIPackage parses every non-test .go file in internal/api.
func parseAPIPackage(t *testing.T) []parsedAPIFile {
	t.Helper()
	dir := apiPackageDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var out []parsedAPIFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		out = append(out, parsedAPIFile{name: path, file: f})
	}
	if len(out) == 0 {
		t.Fatal("no source files parsed from internal/api — the guard is covering nothing")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func apiPackageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}

func isPartialLiteral(e ast.Expr) bool {
	bl, ok := e.(*ast.BasicLit)
	return ok && bl.Kind == token.STRING && bl.Value == `"partial"`
}

// isAuditEntryOrFieldsType reports whether a composite literal's type is
// audit.Entry or audit.Fields — the two shapes that carry a Result.
func isAuditEntryOrFieldsType(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "audit" {
		return false
	}
	return sel.Sel.Name == "Entry" || sel.Sel.Name == "Fields"
}
