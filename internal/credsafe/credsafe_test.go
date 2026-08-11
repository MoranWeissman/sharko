package credsafe

// credsafe_test.go — the properties everything else in this hotfix relies on.
//
// Every boundary asks this package two questions: "did this come from a
// credentials backend?" and "what may I say about it?". If either answer is
// wrong, every boundary is wrong at once — so the answers are pinned here.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const sentinel = "VB6N-credsafe-unit-sentinel-2j8s5y-e7c1"

// TestMark_ErrorIsTheFixedSafeSentence is the property the whole design rests on.
//
// A marked error SAYS the fixed safe sentence and nothing else. So a boundary
// that forgets to ask cannot leak — the default answer is already the safe one.
// err.Error(), "%v", "%s", slog's error field, a log line somebody adds next
// year: all of them get the sentence.
//
// The original is not lost. errors.Is still reaches it, which is how everything
// that needs to classify keeps working.
func TestMark_ErrorIsTheFixedSafeSentence(t *testing.T) {
	original := errors.New(`AccessDenied while resolving ` + sentinel)
	marked := Mark(original)

	if marked.Error() != Message {
		t.Errorf(`Mark's Error() = %q, want the one fixed safe sentence %q.

This is the guard that makes every other boundary safe by default. If Error() passes the original text through, then every %%v, every log line and every string concatenation anybody adds later is a leak waiting to happen.`, marked.Error(), Message)
	}
	if strings.Contains(marked.Error(), sentinel) {
		t.Error("a marked error's Error() carries the original text")
	}
	if !errors.Is(marked, original) {
		t.Error("errors.Is can no longer reach the original error through the mark — the cause must stay reachable through Unwrap")
	}
}

// TestMark_EveryPrintingVerbGetsTheSafeSentence: %v and %s go through Error(),
// and those are what a careless log line or a string concatenation uses. This
// pins that none of them is a way round the sentence.
func TestMark_EveryPrintingVerbGetsTheSafeSentence(t *testing.T) {
	marked := Mark(errors.New("AccessDenied: " + sentinel))
	for _, format := range []string{"%v", "%s", "%v (wrapped: %v)"} {
		got := fmt.Sprintf(format, marked, marked)
		if strings.Contains(got, sentinel) {
			t.Errorf("printing a marked error with %q leaked the original text: %s", format, got)
		}
	}
	// And a %w wrap: the wrapper's own words are its own, but the marked
	// error's contribution is still the sentence.
	wrapped := fmt.Errorf("reading the cluster Secret: %w", marked)
	if strings.Contains(wrapped.Error(), sentinel) {
		t.Errorf("wrapping a marked error with %%w leaked the original text: %s", wrapped.Error())
	}
}

// TestCause_ReachesTheRealErrorForClassificationOnly.
//
// credsafe.Cause is what verify.ClassifyError and verify.AssumeRoleHint use to
// read the real AWS/Kubernetes message and pick one of a fixed set of
// Sharko-written outcomes. It must step past however many marks there are and
// hand back the untouched cause.
func TestCause_ReachesTheRealErrorForClassificationOnly(t *testing.T) {
	original := errors.New("operation error STS: AssumeRole, api error AccessDenied: " + sentinel)
	if got := Cause(Mark(original)); got != original {
		t.Errorf("Cause(marked) = %v, want the original error back", got)
	}
	if got := Cause(Mark(Mark(original))); got != original {
		t.Errorf("Cause(marked twice) = %v, want the original error back", got)
	}
	// An unmarked error comes back unchanged, so a caller does not need to know
	// whether a mark is there.
	if got := Cause(original); got != original {
		t.Errorf("Cause(an unmarked error) = %v, want it unchanged", got)
	}
	if Cause(nil) != nil {
		t.Error("Cause(nil) must be nil")
	}
}

// TestMark_TypedErrorsStayReachable: errors.As must still find a typed provider
// error through the mark, because the API layer dispatches on
// *ArgoCDProviderError's stable Code to pick UI copy.
func TestMark_TypedErrorsStayReachable(t *testing.T) {
	typed := &fakeTypedErr{code: "argocd_provider_iam_required"}
	marked := Mark(fmt.Errorf("wrapping: %w", typed))

	var got *fakeTypedErr
	if !errors.As(marked, &got) {
		t.Fatal("errors.As can no longer find the typed error through the mark — API dispatch on the stable Code would break")
	}
	if got.code != "argocd_provider_iam_required" {
		t.Errorf("code = %q, want the original", got.code)
	}
}

type fakeTypedErr struct{ code string }

func (e *fakeTypedErr) Error() string { return "typed: " + e.code }

// TestIs_FindsTheMarkerThroughAnyNumberOfWraps: the marker must survive a git or
// Kubernetes error wrapping a credentials error, at any depth. This is why the
// classification can be by type instead of by reading words.
func TestIs_FindsTheMarkerThroughAnyNumberOfWraps(t *testing.T) {
	err := Mark(errors.New("AccessDenied: " + sentinel))
	for i := 0; i < 5; i++ {
		err = fmt.Errorf("layer %d: %w", i, err)
		if !Is(err) {
			t.Fatalf("the marker was lost after %d wrap(s)", i+1)
		}
	}
}

// TestIs_NeverReadsTheErrorsWords is the anti-regression guard on the design.
//
// An error whose text is full of credential-sounding words but which was never
// marked must NOT be classified as a credentials failure. If this ever starts
// failing, somebody has replaced the type check with a substring check — which is
// exactly how this bug class comes back, because the substring stops matching the
// day a backend rephrases things.
func TestIs_NeverReadsTheErrorsWords(t *testing.T) {
	for _, text := range []string{
		"credential provider failure",
		"credentials backend error: AccessDenied",
		"failed to fetch the credential from the secrets backend",
		Message,
	} {
		if Is(errors.New(text)) {
			t.Errorf("an UNMARKED error was classified as a credentials failure from its words alone (%q).\n\nThe classification must be errors.Is against the marker, never a substring match.", text)
		}
	}
}

// TestSentence_IsFixedForCredentialsAndPassesEverythingElseThrough covers both
// halves of the rule in one place.
func TestSentence_IsFixedForCredentialsAndPassesEverythingElseThrough(t *testing.T) {
	if got := Sentence(nil); got != "" {
		t.Errorf("Sentence(nil) = %q, want empty", got)
	}

	credErr := Mark(errors.New("AccessDenied: " + sentinel))
	if got := Sentence(credErr); got != Message {
		t.Errorf("Sentence(a credentials error) = %q, want the fixed sentence %q", got, Message)
	}
	if strings.Contains(Sentence(credErr), sentinel) {
		t.Error("Sentence leaked the credentials error's text")
	}

	gitErr := errors.New("git: reference not found: refs/heads/main")
	if got := Sentence(gitErr); got != gitErr.Error() {
		t.Errorf("Sentence(a git error) = %q, want its own text %q — unrelated errors must not be redacted", got, gitErr.Error())
	}
}

// TestSentence_DoesNotVaryWithTheCause: a sentence that changed with the cause
// would be a channel back to the cause. A caller could learn about the credential
// by watching which sentence came back.
func TestSentence_DoesNotVaryWithTheCause(t *testing.T) {
	causes := []error{
		errors.New("AccessDenied: " + sentinel),
		errors.New("RegionDisabled"),
		errors.New(""),
		errors.New(strings.Repeat("x", 4096)),
	}
	for i, c := range causes {
		if got := Sentence(Mark(c)); got != Message {
			t.Errorf("cause %d produced %q, want the one fixed sentence %q", i, got, Message)
		}
	}
}

// TestMark_IsIdempotentAndNilSafe: marking twice is the same as marking once, and
// nil in means nil out — so a boundary can call Mark on whatever it has without
// checking first.
func TestMark_IsIdempotentAndNilSafe(t *testing.T) {
	if Mark(nil) != nil {
		t.Error("Mark(nil) must be nil so boundaries can call it unconditionally")
	}
	once := Mark(errors.New("boom"))
	twice := Mark(once)
	if once != twice {
		t.Error("Mark is not idempotent — marking an already-marked error should return it unchanged")
	}
	if !Is(twice) {
		t.Error("a twice-marked error is no longer recognised")
	}
}

// TestMessage_SaysSomethingUseful: an empty or contentless sentence would pass
// every leak sweep in this hotfix and leave the operator with nothing. The
// sentence has to point somewhere.
func TestMessage_SaysSomethingUseful(t *testing.T) {
	if len(Message) < 40 {
		t.Errorf("the safe sentence is too short to be useful to an operator: %q", Message)
	}
	if !strings.Contains(Message, "log") {
		t.Errorf("the safe sentence does not tell the operator where to look next: %q", Message)
	}
}

// --- the not-found marker --------------------------------------------------
//
// This is the second marker, and it exists for one product behaviour: the
// cluster-test page offers the operator a list of similarly-named secrets when
// the one it looked for is genuinely absent. That used to be decided by looking
// for the words "not found" in the error text. These tests pin the replacement.

// TestIsNotFound_SurvivesMarkAndWrapping is the exact combination the real code
// produces: the provider marks "this is missing", the boundary marks "this came
// from a credentials backend" on top, and something wraps the pair with %w. Both
// answers must survive all of it.
func TestIsNotFound_SurvivesMarkAndWrapping(t *testing.T) {
	missing := MarkNotFound(errors.New(`secret "prod-eu" does not exist`))
	if !IsNotFound(missing) {
		t.Fatal("MarkNotFound's own result is not recognised by IsNotFound")
	}

	// Mark on top — this is the order the providers use (MarkNotFound inside
	// getCredentials, Mark at the exported boundary).
	both := Mark(missing)
	if !Is(both) {
		t.Error("the credentials marker was lost when applied on top of the not-found marker")
	}
	if !IsNotFound(both) {
		t.Fatalf(`the not-found marker was lost under Mark.

Mark's wrapper returns a two-element Unwrap so the cause branch is walked. If this fails, that walk is broken and the suggestion flow silently stops working — the operator gets no help finding their typo.`)
	}
	// And Error() is still the safe sentence: being recognisable as "missing"
	// must not be a way to get the text back.
	if both.Error() != Message {
		t.Errorf("a marked not-found error says %q, want the fixed safe sentence", both.Error())
	}

	// Through arbitrary wrapping, in either marker order.
	for name, err := range map[string]error{
		"not-found inside, mark outside": Mark(MarkNotFound(errors.New("gone"))),
		"mark inside, not-found outside": MarkNotFound(Mark(errors.New("gone"))),
	} {
		wrapped := fmt.Errorf("layer one: %w", fmt.Errorf("layer two: %w", err))
		if !Is(wrapped) {
			t.Errorf("%s: the credentials marker was lost through two %%w hops", name)
		}
		if !IsNotFound(wrapped) {
			t.Errorf("%s: the not-found marker was lost through two %%w hops", name)
		}
	}
}

// TestIsNotFound_SaysNoForEveryOtherKindOfFailure is the half that a substring
// check got wrong, and it is the reason this marker exists at all.
//
// An AccessDenied whose message happens to contain the words "not found" used to
// be treated as a missing secret, which sent the operator to fix a typo that was
// never there. Nothing may be inferred from words — only from the marker.
func TestIsNotFound_SaysNoForEveryOtherKindOfFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"an access denial that happens to say the words", errors.New("AccessDenied: user is not authorized; secret not found in the allowed set")},
		{"a timeout", errors.New("context deadline exceeded")},
		{"a throttle", errors.New("ThrottlingException: rate exceeded")},
		{"a marked credentials failure that is not about absence", Mark(errors.New("AccessDenied: not found"))},
		{"the safe sentence itself", errors.New(Message)},
		{"nothing at all", nil},
	} {
		if IsNotFound(tc.err) {
			t.Errorf(`%s was classified as "the credentials are missing" (%v).

Only the provider may say that, with MarkNotFound, at the point it actually knows. Inferring it from words is how the suggestion flow starts firing on failures that are not about absence.`, tc.name, tc.err)
		}
	}
}

// TestMarkNotFound_IsIdempotentAndNilSafe mirrors Mark's own contract so a
// boundary can call it without checking first.
func TestMarkNotFound_IsIdempotentAndNilSafe(t *testing.T) {
	if MarkNotFound(nil) != nil {
		t.Error("MarkNotFound(nil) must be nil")
	}
	once := MarkNotFound(errors.New("gone"))
	twice := MarkNotFound(once)
	if once != twice {
		t.Error("MarkNotFound is not idempotent")
	}
	if !IsNotFound(twice) {
		t.Error("a twice-marked not-found error is no longer recognised")
	}
}

// TestMarkNotFound_KeepsTypedErrorsReachable: the ArgoCD provider marks an
// apierrors.NewNotFound, and existing callers use apierrors.IsNotFound on it.
// That must keep working through the marker.
func TestMarkNotFound_KeepsTypedErrorsReachable(t *testing.T) {
	typed := &fakeTypedErr{code: "some_typed_error"}
	err := Mark(MarkNotFound(fmt.Errorf("wrapping: %w", typed)))

	var got *fakeTypedErr
	if !errors.As(err, &got) {
		t.Fatal("errors.As can no longer find the typed error through both markers")
	}
	if got.code != "some_typed_error" {
		t.Errorf("code = %q, want the original", got.code)
	}
}
