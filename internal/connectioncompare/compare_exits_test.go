package connectioncompare

// compare_exits_test.go — R3-17: every exit from Compare answers with a defined
// RepairScope, and a future exit is covered by construction rather than by
// somebody remembering.
//
// # Why there are two halves
//
// Round 2 fixed three of Compare's exits and stopped, because the test written
// alongside it drove real inputs at the exits somebody had thought of. The two
// exits nobody thought of kept returning "" — a value the contract does not
// define — and RepairAvailable was false only because false is the zero value.
//
// A test built the same way would miss the next one the same way. So:
//
//   - TestCompare_EveryReachableExitAnswersWithADefinedRepairScope drives real
//     inputs at every exit that CAN be reached and checks the answer.
//   - TestCompare_EveryReturnInCompareSetsRepairScopeExplicitly reads the source
//     of Compare and fails if ANY return in it is not preceded by an explicit
//     assignment to res.RepairScope. That one cannot be dodged by adding an
//     exit, which is the whole point.
//
// The second half exists because one exit — the expectedSides build failure —
// cannot be reached through the public API at all today: it fires only when
// BuildClusterSecret fails, which happens only when json.MarshalIndent fails on
// a struct of plain strings. It still has to answer correctly the day something
// upstream makes it reachable, and reading the source is the only way to hold it
// to that now.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// definedRepairScopes is every value the contract defines. "" is deliberately
// NOT in here: it is the zero value of the string type, not an answer.
var definedRepairScopes = map[RepairScope]bool{
	RepairScopeNone:            true,
	RepairScopeAddonLabelsOnly: true,
	RepairScopeFullConnection:  true,
}

func TestCompare_EveryReachableExitAnswersWithADefinedRepairScope(t *testing.T) {
	ownedPolicy := func() Policy {
		return Classify(ClassifyInput{
			CredsSource:                  models.CredsSourceSecretKubeconfig,
			BackendCanProvideStoredFacts: true,
			LiveSecretFound:              true,
			LiveManagedBy:                argosecrets.ManagedByValue,
		})
	}

	tests := []struct {
		// exit names the exit in Compare, in words rather than by line number.
		exit  string
		build func() Request
		// wantScope is the exact scope this exit must answer with.
		wantScope RepairScope
	}{
		{
			exit: "the caller already hit a failure",
			build: func() Request {
				return Request{
					ClusterName:      testCluster,
					Namespace:        testNamespace,
					Policy:           ownedPolicy(),
					CheckFailure:     "The credentials source did not answer.",
					LiveFound:        true,
					Live:             &corev1.Secret{},
					AddonLabelsKnown: true,
				}
			},
			wantScope: RepairScopeNone,
		},
		{
			exit: "another tool owns the connection",
			build: func() Request {
				return Request{
					ClusterName: testCluster,
					Namespace:   testNamespace,
					Policy: Classify(ClassifyInput{
						CredsSource:                  models.CredsSourceSecretKubeconfig,
						BackendCanProvideStoredFacts: true,
						LiveSecretFound:              true,
						LiveManagedBy:                "another-tool",
					}),
					LiveFound:        true,
					Live:             &corev1.Secret{},
					AddonLabelsKnown: true,
				}
			},
			wantScope: RepairScopeNone,
		},
		{
			exit: "there is no connection Secret at all",
			build: func() Request {
				return Request{
					ClusterName: testCluster,
					Namespace:   testNamespace,
					Policy: Classify(ClassifyInput{
						CredsSource:                  models.CredsSourceSecretKubeconfig,
						BackendCanProvideStoredFacts: true,
						LiveSecretFound:              false,
					}),
					LiveFound:        false,
					Live:             nil,
					AddonLabelsKnown: true,
				}
			},
			wantScope: RepairScopeNone,
		},
		{
			// One of the two exits round 2 missed.
			exit: "Sharko does not know which addons should be on",
			build: func() Request {
				return Request{
					ClusterName:      testCluster,
					Namespace:        testNamespace,
					Policy:           ownedPolicy(),
					LiveFound:        true,
					Live:             &corev1.Secret{},
					AddonLabelsKnown: false,
				}
			},
			wantScope: RepairScopeNone,
		},
		{
			exit: "the comparison ran to the end (owned, full scope)",
			build: func() Request {
				spec := argosecrets.ClusterSecretSpec{
					Name:   testCluster,
					Server: "https://ran-to-the-end.invalid",
					Token:  "made-up-not-a-real-token",
					Labels: map[string]string{"addon-foo": "enabled"},
				}
				req, _ := ownedRequest(t, spec, spec.Labels)
				return req
			},
			wantScope: RepairScopeFullConnection,
		},
		{
			exit: "the comparison ran to the end (guest, labels only)",
			build: func() Request {
				spec := argosecrets.ClusterSecretSpec{
					Name:   testCluster,
					Server: "https://guest.invalid",
					Token:  "made-up-not-a-real-token",
					Labels: map[string]string{"addon-foo": "enabled"},
				}
				req, _ := ownedRequest(t, spec, spec.Labels)
				req.Policy = Classify(ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: true,
					LiveSecretFound:              true,
					ConnectionManagedBy:          models.ConnectionManagedByUser,
				})
				if req.Policy.RepairScope != RepairScopeAddonLabelsOnly {
					t.Fatalf("fixture: a user-managed connection should be labels-only, got %q", req.Policy.RepairScope)
				}
				return req
			},
			wantScope: RepairScopeAddonLabelsOnly,
		},
	}

	for _, tt := range tests {
		t.Run(tt.exit, func(t *testing.T) {
			res := Compare(tt.build())

			if !definedRepairScopes[res.RepairScope] {
				t.Fatalf(`the exit for %q answered RepairScope = %q, which the contract does not define.

RepairScope is a string type whose "none" is the explicit constant %q, not the zero value. An exit that returns without setting it leaves "" on the wire, and no reader knows what that means.`,
					tt.exit, res.RepairScope, RepairScopeNone)
			}
			if res.RepairScope != tt.wantScope {
				t.Errorf("the exit for %q answered RepairScope = %q, want %q", tt.exit, res.RepairScope, tt.wantScope)
			}
			// RepairAvailable must AGREE with the scope, and must be set on
			// purpose rather than left at its zero value. "none" and "a repair
			// is available" cannot both be true.
			if wantAvailable := tt.wantScope != RepairScopeNone; res.RepairAvailable != wantAvailable {
				t.Errorf("the exit for %q answered RepairAvailable = %v with RepairScope = %q; those disagree",
					tt.exit, res.RepairAvailable, res.RepairScope)
			}
		})
	}
}

// TestCompare_EveryReturnInCompareSetsRepairScopeExplicitly reads Compare's own
// source and fails if any return inside it is not preceded, in the same block,
// by an explicit assignment to res.RepairScope.
//
// This is the half that covers an exit nobody has written yet. A new early
// return added without setting the scope fails here even though no fixture
// exists for it — which is what "covered by construction" means, as opposed to
// covered because somebody remembered to add a case.
func TestCompare_EveryReturnInCompareSetsRepairScopeExplicitly(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "compare.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing compare.go: %v", err)
	}

	var compareFn *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if isFunc && fn.Name.Name == "Compare" && fn.Recv == nil {
			compareFn = fn
			break
		}
	}
	if compareFn == nil {
		t.Fatal("could not find func Compare in compare.go — this test needs updating if it moved")
	}

	// Walk the statement lists. For every block that contains a return, every
	// return in it must have an explicit res.RepairScope assignment somewhere
	// before it in that same list, or in an enclosing list before the block.
	returns := 0
	var walkBlock func(stmts []ast.Stmt, scopeSetByAncestor bool)
	walkBlock = func(stmts []ast.Stmt, scopeSetByAncestor bool) {
		scopeSet := scopeSetByAncestor
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.AssignStmt:
				for _, lhs := range s.Lhs {
					if isResRepairScope(lhs) {
						scopeSet = true
					}
				}
			case *ast.IfStmt:
				walkBlock(s.Body.List, scopeSet)
				if s.Else != nil {
					if elseBlock, isBlock := s.Else.(*ast.BlockStmt); isBlock {
						walkBlock(elseBlock.List, scopeSet)
					}
				}
			case *ast.SwitchStmt:
				for _, c := range s.Body.List {
					if clause, isClause := c.(*ast.CaseClause); isClause {
						walkBlock(clause.Body, scopeSet)
					}
				}
			case *ast.ForStmt:
				walkBlock(s.Body.List, scopeSet)
			case *ast.RangeStmt:
				walkBlock(s.Body.List, scopeSet)
			case *ast.BlockStmt:
				walkBlock(s.List, scopeSet)
			case *ast.ReturnStmt:
				returns++
				if !scopeSet {
					t.Errorf(`%s: this return in Compare is not preceded by an explicit res.RepairScope assignment.

Every exit must set RepairScope, because "none" is the constant %q and not the zero value of the type. Leaving it unset puts "" on the wire, which the contract does not define — and RepairAvailable being false by luck (false is its zero value) is exactly the thing this rule exists to stop.`,
						fset.Position(s.Pos()), RepairScopeNone)
				}
			}
		}
	}
	walkBlock(compareFn.Body.List, false)

	if returns == 0 {
		t.Fatal("found no returns in Compare — the walk is not doing anything, so this test proves nothing")
	}
	// Compare has one exit per honesty step plus the full run. If this number
	// moves, an exit was added or removed and the behaviour table above needs a
	// case for it.
	const knownReturns = 6
	if returns != knownReturns {
		t.Errorf(`Compare now has %d return(s); this test knew about %d.

That is not a failure by itself — it means an exit was added or removed. Add (or drop) the matching case in TestCompare_EveryReachableExitAnswersWithADefinedRepairScope and update this number, so the new exit has a real fixture and not just the source-level check.`,
			returns, knownReturns)
	}
}

// isResRepairScope reports whether an assignment target is res.RepairScope.
func isResRepairScope(expr ast.Expr) bool {
	sel, isSelector := expr.(*ast.SelectorExpr)
	if !isSelector || sel.Sel.Name != "RepairScope" {
		return false
	}
	ident, isIdent := sel.X.(*ast.Ident)
	return isIdent && ident.Name == "res"
}
