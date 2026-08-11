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

// TestMark_DoesNotChangeWhatErrorSays is the property the whole design rests on.
//
// Mark adds a marker and nothing else. That is what lets the fix be introduced
// without touching a single internal caller: %w chains still read the same,
// errors.As still finds typed provider errors, and the cluster-test handler's
// strings.Contains(err.Error(), "not found") check — which drives the secret-name
// suggestions an operator actually uses — still matches.
func TestMark_DoesNotChangeWhatErrorSays(t *testing.T) {
	original := errors.New(`secret for cluster "prod-eu" not found in AWS Secrets Manager`)
	marked := Mark(original)

	if marked.Error() != original.Error() {
		t.Errorf("Mark changed Error() from %q to %q — internal callers and existing substring checks would break", original.Error(), marked.Error())
	}
	if !strings.Contains(marked.Error(), "not found") {
		t.Error(`the "not found" substring is gone; the cluster-test handler keys its secret-name suggestions off it`)
	}
	if !errors.Is(marked, original) {
		t.Error("errors.Is can no longer reach the original error through the mark")
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
