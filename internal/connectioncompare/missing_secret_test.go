package connectioncompare

// missing_secret_test.go — B13 item 3b: what Compare says when there is no
// connection Secret.
//
// It used to say ONE thing, whatever the mode: "This cluster has no
// connection Secret yet. The reconciler will create it on its next pass."
// The reconciliation endpoint overrode it per mode, so the connection page
// read correctly — but the comparison endpoint shipped the raw sentence, and
// for a self-managed connection, a legacy pasted-credential one, or one whose
// credentials source Sharko does not understand, that is a promise the
// product cannot keep. A person waited for a Secret that was never coming.
//
// The creation promise is the thing worth guarding, so it is guarded three
// times: by pinning each sentence exactly, by a rule that nothing outside a
// genuinely rebuildable connection may promise creation, and — the one that
// was missing — by driving Classify and SecretMissingReason TOGETHER over
// real inputs.
//
// WHY THE THIRD ONE EXISTS. The first two tests here used to walk a
// hand-written list of Mode values and build a Policy by hand. Both passed
// while the product shipped two sentences that contradicted each other on the
// same connection: SecretMissingReason keyed on the mode alone, and Classify
// hands out ModeBackendStoredCredentials and ModeEKSToken from TWO places —
// the readable-backend path, and the path taken when Sharko cannot read the
// credentials source at all. A hand-built Policy never visits the second one.
// The defect lived precisely in the join, so the join is what gets tested.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// missingSecretRequest is a Compare request that reaches the "there is
// nothing there" exit under the given policy.
func missingSecretRequest(policy Policy) Request {
	return Request{
		ClusterName:        "prod-eu",
		Namespace:          "argocd",
		Policy:             policy,
		Live:               nil,
		LiveFound:          false,
		DesiredAddonLabels: map[string]string{"addons.sharko.dev/datadog": "true"},
		AddonLabelsKnown:   true,
	}
}

// promisesCreation reports whether a sentence tells the reader Sharko is going
// to build the Secret for them. It is the one thing that must never be said to
// somebody Sharko will not build a Secret for.
//
// It lists the RETIRED phrasings as well as the current one on purpose. This
// helper is a safety rule, not a style rule: if a sentence comes back written
// the old way, the rule still has to recognise it as a promise. That matters —
// when the wording was corrected on 2026-08-20 this helper still only knew
// "Sharko will create", so it stopped seeing the promise it exists to police
// and the composed test failed loudly. A rule that recognises only today's
// wording is a rule that switches itself off the next time somebody rewrites a
// sentence.
func promisesCreation(sentence string) bool {
	low := strings.ToLower(sentence)
	return strings.Contains(low, "sharko will create") ||
		strings.Contains(low, "will create it") ||
		strings.Contains(low, "reconciler will create") ||
		strings.Contains(low, "sharko automatically creates") ||
		strings.Contains(low, "automatically creates it")
}

// refusesCreation is the OTHER direction, and it is the one nobody was
// checking (B3a).
//
// A promise that Sharko will build the Secret is a false success claim when it
// will not. A statement that Sharko will NOT build it is the same kind of lie
// in reverse: the person stops waiting, goes looking for something to do by
// hand, and Sharko builds the connection behind them — or, worse, they conclude
// the product is stuck and start deleting things.
//
// Sharko genuinely refuses in exactly one situation: the cluster's Git record
// says connectionManagedBy: user, which is the only field the create path
// partitions on. Every other connection with no Secret goes into the create
// set and is attempted, whether or not the attempt can succeed.
//
// The retired phrasings are listed alongside the current ones for the same
// reason promisesCreation lists its own: a rule that recognises only today's
// wording switches itself off the next time somebody rewrites a sentence.
func refusesCreation(sentence string) bool {
	low := strings.ToLower(sentence)
	return strings.Contains(low, "sharko does not create it") ||
		strings.Contains(low, "will not create one") ||
		strings.Contains(low, "will not create it") ||
		strings.Contains(low, "does not create one")
}

func TestCompare_MissingSecretSentenceIsModeAware(t *testing.T) {
	cases := []struct {
		name   string
		policy Policy
		want   string
	}{
		{"backend_stored_credentials/readable", Policy{Mode: ModeBackendStoredCredentials, Scope: ScopeFull, RepairScope: RepairScopeFullConnection},
			"This cluster has no connection Secret right now. Sharko automatically creates it from Git and the configured credentials source."},
		{"eks_token/readable", Policy{Mode: ModeEKSToken, Scope: ScopeLimited, RepairScope: RepairScopeFullConnection},
			"This cluster has no connection Secret right now. Sharko automatically creates it from Git and the configured credentials source."},

		// The two rows the mode-only version got wrong. Same modes, but
		// Sharko cannot read the backend, so it will not be creating
		// anything and must not say it will.
		{"backend_stored_credentials/unreadable", Policy{Mode: ModeBackendStoredCredentials, Scope: ScopeLimited, RepairScope: RepairScopeAddonLabelsOnly},
			"This cluster has no connection Secret right now. Its credentials are kept in a secrets backend, and Sharko cannot read that backend at the moment, so it cannot create the Secret. Fix the secrets backend connection first, then check again."},
		{"eks_token/unreadable", Policy{Mode: ModeEKSToken, Scope: ScopeLimited, RepairScope: RepairScopeAddonLabelsOnly},
			"This cluster has no connection Secret right now. Its credentials are kept in a secrets backend, and Sharko cannot read that backend at the moment, so it cannot create the Secret. Fix the secrets backend connection first, then check again."},

		{"self_managed", Policy{Mode: ModeSelfManaged, Scope: ScopeAddonLabelsOnly, RepairScope: RepairScopeAddonLabelsOnly},
			"You maintain this cluster's connection Secret yourself and it has not been created yet. Sharko does not create it."},

		// B3b. This row used to expect the self-managed sentence above, and
		// that was the wrong answer twice over: adopted is not recorded in
		// Git, so the create path does not partition it out, and the
		// annotation that makes a connection adopted lives on the Secret that
		// is gone. It now expects the fall-through sentence, which refuses
		// nothing and promises nothing — the only safe answer for a mode
		// Classify cannot actually produce here.
		{"adopted", Policy{Mode: ModeAdopted, Scope: ScopeAddonLabelsOnly, RepairScope: RepairScopeAddonLabelsOnly},
			"This cluster has no connection Secret right now. Sharko still tries to build it from Git and the credentials backend, but this cluster's record does not name a credentials source Sharko understands, so the attempt may not find any credentials to use. Record a supported credentials source for this cluster."},
		// ModeForeignOwned is deliberately NOT a row here: Compare answers an
		// ownership conflict before it ever reaches the missing exit, so this
		// table cannot drive it. Its fall-through sentence is checked directly
		// in TestClassify_AdoptedAndForeignNeedALiveSecret.

		{"inline_kubeconfig", Policy{Mode: ModeInlineKubeconfig, Scope: ScopeLimited, RepairScope: RepairScopeAddonLabelsOnly},
			"This cluster's connection Secret is gone, and its credential existed only in that Secret — Sharko cannot restore it from Git. Store a fresh credential in a supported credentials provider and move the cluster onto it."},
		{"unknown_source", Policy{Mode: ModeUnknownSource, Scope: ScopeLimited, RepairScope: RepairScopeAddonLabelsOnly},
			"This cluster has no connection Secret right now. Sharko still tries to build it from Git and the credentials backend, but this cluster's record does not name a credentials source Sharko understands, so the attempt may not find any credentials to use. Record a supported credentials source for this cluster."},
		{"empty_mode", Policy{Mode: Mode(""), Scope: ScopeLimited, RepairScope: RepairScopeAddonLabelsOnly},
			"This cluster has no connection Secret right now. Sharko still tries to build it from Git and the credentials backend, but this cluster's record does not name a credentials source Sharko understands, so the attempt may not find any credentials to use. Record a supported credentials source for this cluster."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Compare(missingSecretRequest(tc.policy))
			if res.Status != StatusMissing {
				t.Fatalf("status = %q, want %q", res.Status, StatusMissing)
			}
			if res.LimitReason != tc.want {
				t.Errorf("limit reason =\n  %q\nwant\n  %q", res.LimitReason, tc.want)
			}
		})
	}

	// None of the five may name Sharko's own plumbing (product ruling,
	// 2026-08-19: information text speaks in the user's terms). The durable
	// one used to end by naming the reconciler and a pass of it, which sat
	// next to the plan sentence on the same page and read like two different
	// products. Banning the words as well as pinning the sentences means a
	// rewrite cannot quietly bring the vocabulary back.
	for _, sentence := range []string{
		LimitReasonSecretMissingDurable,
		LimitReasonSecretMissingSelfManaged,
		LimitReasonSecretMissingLegacyInline,
		LimitReasonSecretMissingUnknownSource,
		LimitReasonSecretMissingBackendUnreadable,
	} {
		for _, machinery := range []string{"reconciler", "next pass", "loop", "tick", "controller"} {
			if strings.Contains(strings.ToLower(sentence), machinery) {
				t.Errorf("a missing-Secret sentence names Sharko's machinery (%q) — banned:\n  %q", machinery, sentence)
			}
		}
	}

	// The durable sentence is the one that promises creation, and it has to
	// read the same way the plan sentence beside it on the page does — a
	// reader sees both at once. The shape is "Sharko automatically <does the
	// thing>." (product owner, 2026-08-20).
	const automaticOpening = "Sharko automatically "
	if !strings.Contains(LimitReasonSecretMissingDurable, automaticOpening) {
		t.Errorf("the durable missing-Secret sentence no longer reads like the plan sentence it sits beside:\n  %q",
			LimitReasonSecretMissingDurable)
	}
}

// TestClassifyThenSecretMissingReason_ComposedOverRealInputs is the test that
// was missing, and it is the one that fails when the promise is keyed on the
// mode alone.
//
// It never names a Mode. It builds the inputs Classify really receives —
// recorded credentials source, recorded connectionManagedBy, whether the
// secrets backend can be read, and the live-Secret facts — runs Classify, and
// hands its Policy to SecretMissingReason. Then it checks the promise against
// a rule computed from the INPUTS, not from the Policy, so the assertion
// cannot agree with the code by sharing its reasoning:
//
//	Sharko may promise to create the Secret only when the connection is
//	Sharko's to manage, the recorded source is one Sharko understands, and
//	Sharko can read that source right now.
func TestClassifyThenSecretMissingReason_ComposedOverRealInputs(t *testing.T) {
	credsSources := []string{
		models.CredsSourceSecretKubeconfig,
		models.CredsSourceEKSToken,
		models.CredsSourceInlineKubeconfig,
		"something-a-newer-sharko-wrote",
		"",
	}
	managedBy := []string{"", models.ConnectionManagedBySharko, models.ConnectionManagedByUser}

	// The live Secret is MISSING in every case here — that is the whole
	// situation. LiveManagedBy and LiveAdopted are still varied because
	// Classify reads them, and a missing Secret must not let a stale
	// ownership fact change the answer.
	for _, source := range credsSources {
		for _, mb := range managedBy {
			for _, backendReadable := range []bool{true, false} {
				for _, liveManagedBy := range []string{"", argosecrets.ManagedByValue, "some-other-tool"} {
					for _, adopted := range []bool{true, false} {
						in := ClassifyInput{
							CredsSource:                  source,
							ConnectionManagedBy:          mb,
							BackendCanProvideStoredFacts: backendReadable,
							LiveSecretFound:              false,
							LiveManagedBy:                liveManagedBy,
							LiveAdopted:                  adopted,
						}
						name := fmt.Sprintf("source=%q/managedBy=%q/backendReadable=%v/liveManagedBy=%q/adopted=%v",
							source, mb, backendReadable, liveManagedBy, adopted)
						t.Run(name, func(t *testing.T) {
							policy := Classify(in)
							got := SecretMissingReason(policy)

							sharkosToManage := !models.IsUserManagedConnection(mb)
							understoodSource := source == models.CredsSourceSecretKubeconfig ||
								source == models.CredsSourceEKSToken
							mayPromise := sharkosToManage && understoodSource && backendReadable

							if promisesCreation(got) != mayPromise {
								if mayPromise {
									t.Fatalf("Sharko can rebuild this connection, but the sentence promises nothing:\n  %q", got)
								}
								t.Fatalf("the page promises Sharko will create the Secret, and it will not "+
									"(classified as %q, repair scope %q):\n  %q", policy.Mode, policy.RepairScope, got)
							}

							// B3a — the same rule the other way up, and this
							// is the half that was missing.
							//
							// The rule is computed from the INPUTS again, and
							// it is deliberately ONE input: the create path
							// partitions on connectionManagedBy and on
							// nothing else. It does not read the recorded
							// credentials source, so a sentence that refuses
							// on the strength of that source is claiming a
							// behaviour no code performs.
							mayRefuse := !sharkosToManage
							if refusesCreation(got) != mayRefuse {
								if mayRefuse {
									t.Fatalf("this connection is the person's to maintain and Sharko never "+
										"creates it, but the sentence does not say so:\n  %q", got)
								}
								t.Fatalf("the page says Sharko will not create the Secret, and Sharko does try "+
									"(recorded source %q is not consulted by the create path; classified as %q):\n  %q",
									source, policy.Mode, got)
							}

							// A missing-Secret sentence is never empty:
							// silence is its own kind of wrong answer.
							if got == "" {
								t.Fatal("no sentence at all for a connection with no Secret")
							}
						})
					}
				}
			}
		}
	}
}

// TestClassifyThenSecretMissingReason_UnreadableBackendNamesTheRealProblem is
// the two rows the defect was actually about, spelled out so a future reader
// sees the situation rather than a cell in a matrix: the cluster's record
// names a credentials source Sharko understands, the Secret has been deleted,
// and the provider is unconfigured or unreachable. The reconciler pass the old
// sentence pointed at cannot run until the backend is fixed, and the old
// sentence never mentioned the backend.
func TestClassifyThenSecretMissingReason_UnreadableBackendNamesTheRealProblem(t *testing.T) {
	for _, source := range []string{models.CredsSourceSecretKubeconfig, models.CredsSourceEKSToken} {
		t.Run(source, func(t *testing.T) {
			policy := Classify(ClassifyInput{
				CredsSource:                  source,
				BackendCanProvideStoredFacts: false,
				LiveSecretFound:              false,
			})
			got := SecretMissingReason(policy)

			if got != LimitReasonSecretMissingBackendUnreadable {
				t.Fatalf("missing-Secret sentence =\n  %q\nwant\n  %q", got, LimitReasonSecretMissingBackendUnreadable)
			}
			// The two sentences a person sees at the same moment must not
			// contradict each other. The limit reason on this policy says
			// Sharko cannot read the credentials; the missing-Secret
			// sentence must not say Sharko is about to build from them.
			if promisesCreation(policy.LimitReason) {
				t.Fatalf("the limit reason itself promises creation: %q", policy.LimitReason)
			}
			if !strings.Contains(strings.ToLower(got), "backend") {
				t.Fatalf("the sentence does not name the thing to fix: %q", got)
			}
		})
	}
}

// TestCompare_MissingSecretSentenceSurvivesAPresentSecret guards the other
// direction: a Secret that IS there must not pick up a missing-Secret
// sentence from anywhere.
func TestCompare_MissingSecretSentenceSurvivesAPresentSecret(t *testing.T) {
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-eu", Namespace: "argocd"},
		Type:       corev1.SecretTypeOpaque,
	}
	req := missingSecretRequest(Policy{
		Mode: ModeSelfManaged, Scope: ScopeAddonLabelsOnly,
		RepairScope: RepairScopeAddonLabelsOnly,
		LimitReason: "the self-managed limit sentence",
	})
	req.Live = live
	req.LiveFound = true

	res := Compare(req)
	if res.Status == StatusMissing {
		t.Fatalf("a Secret that exists was reported missing")
	}
	for _, sentence := range []string{
		LimitReasonSecretMissingDurable,
		LimitReasonSecretMissingSelfManaged,
		LimitReasonSecretMissingLegacyInline,
		LimitReasonSecretMissingUnknownSource,
		LimitReasonSecretMissingBackendUnreadable,
	} {
		if res.LimitReason == sentence {
			t.Errorf("a present Secret carries the missing-Secret sentence %q", sentence)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// The join guard (B3a, B3b)
//
// Both defects this file now covers were JOIN defects. Classify put every
// connection in the right mode; SecretMissingReason then handed one of those
// modes a sentence about a different mode. Nothing failed, because every piece
// was right on its own and no test asked whether the pieces fit.
//
// So the guards below are about the join and nothing else, and they are LISTS
// rather than counts. A count fails when a mode is added and stays green when
// a mode is renamed out from under it; a list fails both ways and says which
// entry is the problem.
// ─────────────────────────────────────────────────────────────────────────────

// modesReachableWithNoLiveSecret is the hand-typed list of the modes Classify
// can actually produce for a connection whose Secret is gone — which is the
// only situation SecretMissingReason is ever asked about.
var modesReachableWithNoLiveSecret = []Mode{
	ModeSelfManaged,
	ModeInlineKubeconfig,
	ModeBackendStoredCredentials,
	ModeEKSToken,
	ModeUnknownSource,
}

// modesOnlyReachableWithALiveSecret is the rest. Both of these need a live
// Secret to exist at all — ownership is a label on the Secret, and adoption is
// an annotation on it — so neither can be the mode of a connection that has no
// Secret. ModeAdopted sharing the self-managed sentence was B3b.
var modesOnlyReachableWithALiveSecret = []Mode{
	ModeAdopted,
	ModeForeignOwned,
}

// missingSecretSentences is the hand-typed list of every sentence
// SecretMissingReason may return.
var missingSecretSentences = []string{
	LimitReasonSecretMissingDurable,
	LimitReasonSecretMissingSelfManaged,
	LimitReasonSecretMissingLegacyInline,
	LimitReasonSecretMissingUnknownSource,
	LimitReasonSecretMissingBackendUnreadable,
}

// declaredModes reads mode.go and returns the VALUE of every constant declared
// with type Mode.
//
// It reads the source rather than a hand-kept slice on purpose: a hand-kept
// slice is a second list somebody has to remember to update, and the whole
// point of this guard is to fail when somebody forgets. Adding an eighth mode
// makes this function return eight values, and the test below then has an
// unlisted one to complain about.
func declaredModes(t *testing.T) []Mode {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "mode.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing mode.go: %v", err)
	}
	var out []Mode
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Mode" {
			return true
		}
		for _, v := range spec.Values {
			lit, isLit := v.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				continue
			}
			text, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil {
				continue
			}
			out = append(out, Mode(text))
		}
		return true
	})
	return out
}

func modeSetOf(modes []Mode) map[Mode]bool {
	set := make(map[Mode]bool, len(modes))
	for _, m := range modes {
		set[m] = true
	}
	return set
}

func sortedModeNames(set map[Mode]bool) []string {
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, string(m))
	}
	sort.Strings(out)
	return out
}

// classifyInputsWithNoLiveSecret is every combination of inputs Classify can
// be handed for a connection that has no Secret. Shared by the guards below so
// they all reason over the same ground.
func classifyInputsWithNoLiveSecret() []ClassifyInput {
	var out []ClassifyInput
	for _, source := range []string{
		models.CredsSourceSecretKubeconfig,
		models.CredsSourceEKSToken,
		models.CredsSourceInlineKubeconfig,
		"something-a-newer-sharko-wrote",
		"",
	} {
		for _, mb := range []string{"", models.ConnectionManagedBySharko, models.ConnectionManagedByUser} {
			for _, backendReadable := range []bool{true, false} {
				for _, liveManagedBy := range []string{"", argosecrets.ManagedByValue, "some-other-tool"} {
					for _, adopted := range []bool{true, false} {
						out = append(out, ClassifyInput{
							CredsSource:                  source,
							ConnectionManagedBy:          mb,
							BackendCanProvideStoredFacts: backendReadable,
							LiveSecretFound:              false,
							LiveManagedBy:                liveManagedBy,
							LiveAdopted:                  adopted,
						})
					}
				}
			}
		}
	}
	return out
}

// TestSecretMissingReason_JoinIsExhaustiveBothWays is the guard the two
// defects needed: every mode that can reach this function has a sentence, and
// every sentence is reachable from some mode.
//
// It fails in four separate ways on purpose — a new mode nobody listed, a
// listed mode that no longer exists, a mode Classify produces here that the
// reachable list does not name, and a sentence no input can produce any more.
// And it refuses to pass on empty evidence: each half asserts it actually
// looked at something before it agrees.
func TestSecretMissingReason_JoinIsExhaustiveBothWays(t *testing.T) {
	// ── 1. Every mode the package declares is on exactly one of the two lists.
	declared := declaredModes(t)
	if len(declared) == 0 {
		t.Fatal("no Mode constants were found in mode.go — this guard would pass vacuously")
	}
	listed := modeSetOf(append(append([]Mode{}, modesReachableWithNoLiveSecret...), modesOnlyReachableWithALiveSecret...))
	if len(listed) != len(modesReachableWithNoLiveSecret)+len(modesOnlyReachableWithALiveSecret) {
		t.Fatal("a mode is named on both lists, or twice on one — the lists must partition the modes")
	}
	declaredSet := modeSetOf(declared)
	for _, m := range declared {
		if !listed[m] {
			t.Errorf("mode %q is declared in mode.go and is on neither list in this file.\n"+
				"Decide whether a connection with NO Secret can be classified as this mode. If it can, add it to\n"+
				"modesReachableWithNoLiveSecret AND give it a case in SecretMissingReason. If it cannot, add it to\n"+
				"modesOnlyReachableWithALiveSecret. Leaving it out is how ModeAdopted kept the wrong sentence.", m)
		}
	}
	for m := range listed {
		if !declaredSet[m] {
			t.Errorf("mode %q is listed in this file but is not declared in mode.go any more — the list has gone stale", m)
		}
	}

	// ── 2. The modes Classify really produces with no live Secret.
	produced := map[Mode]bool{}
	inputs := classifyInputsWithNoLiveSecret()
	if len(inputs) == 0 {
		t.Fatal("no classify inputs were built — this guard would pass vacuously")
	}
	sentencesProduced := map[string]bool{}
	for _, in := range inputs {
		policy := Classify(in)
		produced[policy.Mode] = true
		sentencesProduced[SecretMissingReason(policy)] = true
	}
	wantReachable := modeSetOf(modesReachableWithNoLiveSecret)
	for m := range produced {
		if !wantReachable[m] {
			t.Errorf("Classify produced mode %q for a connection with no live Secret, and modesReachableWithNoLiveSecret\n"+
				"does not name it. Either the classification changed or the list is wrong — and SecretMissingReason\n"+
				"has almost certainly never been asked what to say about it.", m)
		}
	}
	for m := range wantReachable {
		if !produced[m] {
			t.Errorf("modesReachableWithNoLiveSecret names %q, but no input to Classify produces it for a connection\n"+
				"with no live Secret. The entry is stale, or the inputs no longer cover the case that produced it.", m)
		}
	}
	t.Logf("drove %d classify inputs, producing modes %v", len(inputs), sortedModeNames(produced))

	// ── 3. Every sentence is reachable, and nothing else is returned.
	wantSentences := map[string]bool{}
	for _, s := range missingSecretSentences {
		if s == "" {
			t.Fatal("missingSecretSentences holds an empty string")
		}
		wantSentences[s] = true
	}
	if len(wantSentences) != len(missingSecretSentences) {
		t.Fatal("two entries in missingSecretSentences are the same sentence — one of them is not doing any work")
	}
	for s := range sentencesProduced {
		if !wantSentences[s] {
			t.Errorf("SecretMissingReason returned a sentence that missingSecretSentences does not name:\n  %q", s)
		}
	}
	for s := range wantSentences {
		if !sentencesProduced[s] {
			t.Errorf("no connection can reach this sentence any more — it is dead text still shipping in the catalog\n"+
				"and in ui/src/generated/connection-sentences.ts:\n  %q", s)
		}
	}

	// ── 4. Every declared mode gets a real sentence, reachable or not.
	for _, m := range declared {
		got := SecretMissingReason(Policy{Mode: m, Scope: ScopeLimited, RepairScope: RepairScopeAddonLabelsOnly})
		if got == "" {
			t.Errorf("mode %q has no missing-Secret sentence at all — silence is its own wrong answer", m)
			continue
		}
		if !wantSentences[got] {
			t.Errorf("mode %q produced a sentence that is not on the list:\n  %q", m, got)
		}
	}
}

// TestClassify_AdoptedAndForeignNeedALiveSecret is the fact B3b turned on, so
// it is asserted rather than assumed.
//
// If this ever stops holding, SecretMissingReason starts being asked about
// those two modes for real — and the guard above will already have made sure
// they are on the right list and have a truthful sentence.
func TestClassify_AdoptedAndForeignNeedALiveSecret(t *testing.T) {
	forbidden := modeSetOf(modesOnlyReachableWithALiveSecret)
	checked := 0
	for _, in := range classifyInputsWithNoLiveSecret() {
		policy := Classify(in)
		checked++
		if forbidden[policy.Mode] {
			t.Fatalf("Classify returned %q for a connection with no live Secret (inputs: source=%q managedBy=%q "+
				"backendReadable=%v liveManagedBy=%q adopted=%v)",
				policy.Mode, in.CredsSource, in.ConnectionManagedBy, in.BackendCanProvideStoredFacts,
				in.LiveManagedBy, in.LiveAdopted)
		}
	}
	if checked == 0 {
		t.Fatal("nothing was checked")
	}

	// And the fall-through answer for those two modes must be safe: it may
	// neither promise creation nor refuse it, because Sharko's behaviour for
	// such a connection is not knowable from the mode.
	for _, m := range modesOnlyReachableWithALiveSecret {
		got := SecretMissingReason(Policy{Mode: m})
		if promisesCreation(got) {
			t.Errorf("the fall-through sentence for %q promises Sharko will build the connection:\n  %q", m, got)
		}
		if refusesCreation(got) {
			t.Errorf("the fall-through sentence for %q says Sharko will not build the connection:\n  %q", m, got)
		}
	}
}

// TestSecretMissingReason_UnknownSourceDoesNotRefuse is B3a spelled out as a
// situation rather than a cell in a matrix.
//
// A cluster registered before Sharko recorded where credentials are kept —
// which is every cluster on every install upgraded from an older Sharko — has
// an empty credsSource. It is not user-managed, so the create path takes it
// like any other cluster: it looks the credentials up under the cluster's own
// key and builds the connection if it finds them. The old sentence told that
// person Sharko would not create anything.
func TestSecretMissingReason_UnknownSourceDoesNotRefuse(t *testing.T) {
	for _, source := range []string{"", "something-a-newer-sharko-wrote"} {
		t.Run("source="+source, func(t *testing.T) {
			policy := Classify(ClassifyInput{
				CredsSource:                  source,
				ConnectionManagedBy:          models.ConnectionManagedBySharko,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              false,
			})
			if policy.Mode != ModeUnknownSource {
				t.Fatalf("mode = %q, want %q", policy.Mode, ModeUnknownSource)
			}
			got := SecretMissingReason(policy)

			// Exact text, compared against a literal typed here — never
			// against the constant the code assigned, which is a comparison
			// that cannot fail.
			const want = "This cluster has no connection Secret right now. Sharko still tries to build it from Git and the credentials backend, but this cluster's record does not name a credentials source Sharko understands, so the attempt may not find any credentials to use. Record a supported credentials source for this cluster."
			if got != want {
				t.Errorf("sentence =\n  %q\nwant\n  %q", got, want)
			}
			if refusesCreation(got) {
				t.Error("the sentence still refuses on the strength of a recorded source the create path never reads")
			}
			if promisesCreation(got) {
				t.Error("the sentence promises creation, which Sharko cannot guarantee without a source it can read")
			}
			// It still has to tell the person what to do.
			if !strings.Contains(strings.ToLower(got), "record a supported credentials source") {
				t.Errorf("the sentence no longer says what to do about it:\n  %q", got)
			}
		})
	}
}

// TestSecretMissingReason_OnlySelfManagedRefuses pins the one true refusal, by
// exact text typed here.
func TestSecretMissingReason_OnlySelfManagedRefuses(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:         models.CredsSourceSecretKubeconfig,
		ConnectionManagedBy: models.ConnectionManagedByUser,
		LiveSecretFound:     false,
	})
	if policy.Mode != ModeSelfManaged {
		t.Fatalf("mode = %q, want %q", policy.Mode, ModeSelfManaged)
	}
	const want = "You maintain this cluster's connection Secret yourself and it has not been created yet. Sharko does not create it."
	got := SecretMissingReason(policy)
	if got != want {
		t.Fatalf("sentence =\n  %q\nwant\n  %q", got, want)
	}
	if !refusesCreation(got) {
		t.Fatal("the one connection Sharko really never builds no longer says so")
	}

	// Every other sentence must not refuse.
	for _, other := range []string{
		LimitReasonSecretMissingDurable,
		LimitReasonSecretMissingLegacyInline,
		LimitReasonSecretMissingUnknownSource,
		LimitReasonSecretMissingBackendUnreadable,
	} {
		if refusesCreation(other) {
			t.Errorf("a sentence for a connection Sharko does attempt says it will not:\n  %q", other)
		}
	}
}
