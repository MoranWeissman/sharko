package credsafe

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// marksecretvalue_test.go — S2 added a SECOND mark to this package, and this
// file is the proof that adding it did not weaken the first one.
//
// The risk of a second sentence is precise and worth naming: Message's doc
// comment says the sentence must not vary with the CAUSE, because a sentence
// that varied would be a channel back to the cause. These two vary with the
// OPERATION instead. So the property to pin is that WITHIN each mark the
// sentence is invariant no matter what the backend said — which is what
// TestMarkSecretValue_SameSentenceWhateverTheCause below checks.

// backendSentinel stands in for credential material a misbehaving backend SDK
// put into its own error text. Fake.
const backendSentinel = "CANARY-be7714aa-backend-said-this-0f21c9d3"

func TestMarkSecretValue_ErrorSaysTheFixedSentence(t *testing.T) {
	raw := fmt.Errorf("AccessDeniedException: signature %s rejected", backendSentinel)
	marked := MarkSecretValue(raw)

	if got := marked.Error(); got != SecretValueMessage {
		t.Fatalf("Error() = %q, want the fixed secret-value sentence", got)
	}
	// The shapes a boundary that forgot to ask would use.
	for name, got := range map[string]string{
		"Error()":         marked.Error(),
		"%v":              fmt.Sprintf("%v", marked),
		"%s":              fmt.Sprintf("%s", marked),
		"Sentence(err)":   Sentence(marked),
		"wrapped %w then": fmt.Errorf("pushing addon secret: %w", marked).Error(),
	} {
		if strings.Contains(got, backendSentinel) {
			t.Errorf("%s leaked the backend's words: %q", name, got)
		}
	}
}

func TestMarkSecretValue_IsStillACredentialsBackendFailure(t *testing.T) {
	marked := MarkSecretValue(errors.New("boom"))
	if !Is(marked) {
		t.Error("Is() must answer yes for a secret-value mark too — every existing guard and boundary asks this question and must keep working")
	}
}

func TestMarkSecretValue_SentenceIsTheSecretValueOne(t *testing.T) {
	if got := Sentence(MarkSecretValue(errors.New("boom"))); got != SecretValueMessage {
		t.Errorf("Sentence() = %q, want SecretValueMessage", got)
	}
	if got := Sentence(Mark(errors.New("boom"))); got != Message {
		t.Errorf("Sentence() = %q, want the credentials Message — the original mark must be unchanged", got)
	}
	// The two must not be the same string, or the whole reason for the second
	// mark is gone.
	if Message == SecretValueMessage {
		t.Error("the two sentences are identical — then one of them is telling an operator to look at the wrong thing")
	}
}

func TestMarkSecretValue_SameSentenceWhateverTheCause(t *testing.T) {
	causes := []error{
		errors.New("AccessDeniedException"),
		errors.New("ResourceNotFoundException"),
		errors.New("dial tcp 10.0.0.1:443: connection refused"),
		fmt.Errorf("nested: %w", errors.New("x509: certificate signed by unknown authority")),
	}
	var seen string
	for i, cause := range causes {
		got := MarkSecretValue(cause).Error()
		if i == 0 {
			seen = got
			continue
		}
		if got != seen {
			t.Errorf("the sentence changed with the cause (%v) — that makes it a channel back to the cause: %q vs %q", cause, got, seen)
		}
	}
}

func TestMarkSecretValue_CauseStillReachable(t *testing.T) {
	sentinelErr := errors.New("the real reason")
	marked := MarkSecretValue(fmt.Errorf("wrapping: %w", sentinelErr))

	if !errors.Is(marked, sentinelErr) {
		t.Error("errors.Is must still reach the real cause through the mark")
	}
	if got := Cause(marked).Error(); !strings.Contains(got, "the real reason") {
		t.Errorf("Cause() = %q, want the real error underneath (classification needs it)", got)
	}
}

func TestMarkSecretValue_MarkingTwiceIsMarkingOnce(t *testing.T) {
	once := MarkSecretValue(errors.New("boom"))
	twice := MarkSecretValue(once)
	if once != twice {
		t.Error("MarkSecretValue must be idempotent")
	}
	// And the FIRST boundary to speak wins, either way round — a later
	// wrapper must not be able to relabel somebody else's failure.
	if got := Mark(MarkSecretValue(errors.New("boom"))).Error(); got != SecretValueMessage {
		t.Errorf("Mark over a secret-value mark changed the sentence to %q — the first boundary should keep the last word", got)
	}
	if got := MarkSecretValue(Mark(errors.New("boom"))).Error(); got != Message {
		t.Errorf("MarkSecretValue over a credentials mark changed the sentence to %q", got)
	}
}

// TestMarks_OneOrdinaryWrapCannotRelabelSomebodyElsesFailure drives the case
// the two assertions above CANNOT reach: a mark with a plain fmt.Errorf wrap
// sitting on top of it, which is what every real chain looks like.
//
// The guard used to be a type assertion on the OUTERMOST error while findMarked
// walked the whole chain, so MarkSecretValue(Mark(x)) was a no-op but
// MarkSecretValue(fmt.Errorf("...: %w", Mark(x))) relabelled — a cluster
// sign-in failure told to the operator as an addon secret-value failure,
// pointing them at the wrong thing. Nothing leaked either way, which is
// exactly why nothing else in the tree noticed.
//
// Both directions are driven, and both directions of the wrap depth, so a fix
// that only handles one nesting cannot pass.
func TestMarks_OneOrdinaryWrapCannotRelabelSomebodyElsesFailure(t *testing.T) {
	const sentinel = "backend-said-this-and-it-must-never-be-read"

	cases := []struct {
		name string
		// build produces the chain that reaches the second boundary.
		build func() error
		// relabel is the second boundary, which must NOT change the sentence.
		relabel func(error) error
		want    string
	}{
		{
			name:    "credentials mark, one wrap, then a secret-value boundary",
			build:   func() error { return fmt.Errorf("listing cluster secrets: %w", Mark(errors.New(sentinel))) },
			relabel: MarkSecretValue,
			want:    Message,
		},
		{
			name:    "secret-value mark, one wrap, then a credentials boundary",
			build:   func() error { return fmt.Errorf("reading addon secret: %w", MarkSecretValue(errors.New(sentinel))) },
			relabel: Mark,
			want:    SecretValueMessage,
		},
		{
			name: "credentials mark, two wraps, then a secret-value boundary",
			build: func() error {
				return fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", Mark(errors.New(sentinel))))
			},
			relabel: MarkSecretValue,
			want:    Message,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.relabel(tc.build())

			// 1. The first boundary keeps the last word about WHICH failure
			//    this is.
			if got.Error() != tc.want {
				t.Errorf("the second boundary relabelled the failure.\n got %q\nwant %q", got.Error(), tc.want)
			}
			// 2. And the boundary still gets the last word about the WORDS:
			//    the re-wrap must strip Sharko's own prefixes, so the sentence
			//    stands on its own. A fix that simply returned the wrapper
			//    unchanged would fail here.
			if got.Error() != tc.want {
				t.Errorf("Sharko's own prefixes reached the boundary sentence: %q", got.Error())
			}
			// 3. The backend's words are gone from the rendered sentence, and
			//    the real cause is still reachable for classification.
			if strings.Contains(got.Error(), sentinel) {
				t.Errorf("the backend's own words reached the boundary sentence: %q", got.Error())
			}
			if !Is(got) {
				t.Error("the chain stopped answering yes to Is — every existing guard keys on that")
			}
			if cause := Cause(got); cause == nil || !strings.Contains(cause.Error(), sentinel) {
				t.Error("the real cause is no longer reachable through Cause")
			}
		})
	}
}

func TestMarkSecretValue_NilInNilOut(t *testing.T) {
	if MarkSecretValue(nil) != nil {
		t.Error("nil in, nil out")
	}
}

// TestBothSentences_ExplainWhichThingToLookAt is the counterweight test: a
// fixed sentence that says nothing useful trades one defect for another.
func TestBothSentences_ExplainWhichThingToLookAt(t *testing.T) {
	cases := map[string]struct {
		sentence string
		mustName string
	}{
		"credentials":  {Message, "sign-in details"},
		"secret value": {SecretValueMessage, "secret value"},
	}
	for name, c := range cases {
		if !strings.Contains(c.sentence, c.mustName) {
			t.Errorf("the %s sentence does not name what failed (%q): %q", name, c.mustName, c.sentence)
		}
		if !strings.Contains(c.sentence, "server log") {
			t.Errorf("the %s sentence does not tell the operator where the detail is: %q", name, c.sentence)
		}
		if !strings.HasSuffix(strings.TrimSpace(c.sentence), ".") {
			t.Errorf("the %s sentence is not a finished sentence: %q", name, c.sentence)
		}
	}
}
