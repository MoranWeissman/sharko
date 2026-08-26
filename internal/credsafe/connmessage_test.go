package credsafe

// connmessage_test.go — the two sentences pinned by EXACT TEXT.
//
// # Why the wanted text is typed out here in full
//
// Every string below is a literal typed into this file, never a reference to
// the constant the production code assigned. A test that compares a constant
// to itself cannot fail — it goes green through a rewrite, a truncation, an
// empty string, and through somebody pasting an error's words into the
// sentence. A story earlier the same day shipped exactly that test, and only a
// deliberate break exposed it.
//
// So: change the sentence in connmessage.go and this test goes red. That is
// the whole point. If the change is wanted, change the literal here too — and
// in doing so, read the new sentence once, on purpose.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const wantNoActiveGitConnectionMessage = "Sharko has no usable Git connection. Open Settings and check the active connection: the Git provider, the repository it points at, and the access token."

const wantNoActiveArgocdConnectionMessage = "Sharko has no usable ArgoCD connection. Open Settings and check the active connection: the ArgoCD server address and the ArgoCD token."

func TestNoActiveConnectionMessages_ExactText(t *testing.T) {
	if NoActiveGitConnectionMessage != wantNoActiveGitConnectionMessage {
		t.Errorf("NoActiveGitConnectionMessage is\n  %q\nwant exactly\n  %q",
			NoActiveGitConnectionMessage, wantNoActiveGitConnectionMessage)
	}
	if NoActiveArgocdConnectionMessage != wantNoActiveArgocdConnectionMessage {
		t.Errorf("NoActiveArgocdConnectionMessage is\n  %q\nwant exactly\n  %q",
			NoActiveArgocdConnectionMessage, wantNoActiveArgocdConnectionMessage)
	}
}

// TestNoActiveConnectionErrors_SayExactlyTheSentence: the error form and the
// string form must not be able to drift apart, and Error() must be the whole
// of what the error says — no prefix, no cause appended.
func TestNoActiveConnectionErrors_SayExactlyTheSentence(t *testing.T) {
	if got := ErrNoActiveGitConnection.Error(); got != wantNoActiveGitConnectionMessage {
		t.Errorf("ErrNoActiveGitConnection.Error() is\n  %q\nwant exactly\n  %q", got, wantNoActiveGitConnectionMessage)
	}
	if got := ErrNoActiveArgocdConnection.Error(); got != wantNoActiveArgocdConnectionMessage {
		t.Errorf("ErrNoActiveArgocdConnection.Error() is\n  %q\nwant exactly\n  %q", got, wantNoActiveArgocdConnectionMessage)
	}
}

// TestNoActiveConnectionErrors_HaveNothingUnderneath is the property that
// makes them safe to return.
//
// The four methods in internal/remediation and the two in internal/api that
// return these used to wrap the real failure with %w. The real failure, on the
// Git side, can be net/url quoting a repository URL with the access token
// inside it. So an error with a reachable cause is one fmt.Errorf away from
// putting that token back on the wire. These have no cause at all.
func TestNoActiveConnectionErrors_HaveNothingUnderneath(t *testing.T) {
	for name, err := range map[string]error{
		"ErrNoActiveGitConnection":    ErrNoActiveGitConnection,
		"ErrNoActiveArgocdConnection": ErrNoActiveArgocdConnection,
	} {
		if u, ok := err.(interface{ Unwrap() error }); ok && u.Unwrap() != nil {
			t.Errorf("%s has %v underneath it", name, u.Unwrap())
		}
		if u, ok := err.(interface{ Unwrap() []error }); ok && len(u.Unwrap()) > 0 {
			t.Errorf("%s has causes underneath it", name)
		}
		// And printing it every way a careless caller might still says only
		// the sentence.
		for _, format := range []string{"%v", "%s"} {
			got := fmt.Sprintf("wrapping: "+format, err)
			if !strings.HasSuffix(got, err.Error()) {
				t.Errorf("%s printed with %q came out as %q — something else got into it", name, format, got)
			}
		}
		// And the %w wrap a caller would reach for: the wrapper's own words
		// are its own, but the sentence is the whole of this error's
		// contribution — there is nothing else in the chain to print.
		if got := fmt.Errorf("wrapping: %w", err).Error(); got != "wrapping: "+err.Error() {
			t.Errorf("%s wrapped with %%w came out as %q — something else got into it", name, got)
		}
	}
}

// TestNoActiveConnectionMessages_AreTwoDifferentAnswers. Making the messages
// safe must not make them the same: an operator sent to the ArgoCD token when
// the repository URL is what is broken has been given a worse answer than the
// leak was.
func TestNoActiveConnectionMessages_AreTwoDifferentAnswers(t *testing.T) {
	if NoActiveGitConnectionMessage == NoActiveArgocdConnectionMessage {
		t.Fatal("the two sentences have collapsed into one — the operator can no longer tell which half of the connection to go and fix")
	}
	if !strings.Contains(NoActiveGitConnectionMessage, "Git") {
		t.Error("the Git sentence does not say Git")
	}
	if !strings.Contains(NoActiveArgocdConnectionMessage, "ArgoCD") {
		t.Error("the ArgoCD sentence does not say ArgoCD")
	}
	if strings.Contains(NoActiveGitConnectionMessage, "ArgoCD") {
		t.Error("the Git sentence talks about ArgoCD — it will send the operator to the wrong field")
	}
	// Each must be long enough to actually tell somebody what to do. An empty
	// or one-word sentence would pass every leak sweep in this change and help
	// nobody, which is the other way to fail this story.
	for name, s := range map[string]string{
		"NoActiveGitConnectionMessage":    NoActiveGitConnectionMessage,
		"NoActiveArgocdConnectionMessage": NoActiveArgocdConnectionMessage,
	} {
		if len(s) < 60 {
			t.Errorf("%s is too short to be useful to an operator: %q", name, s)
		}
		if !strings.Contains(s, "Settings") {
			t.Errorf("%s does not point anywhere — it must name the screen to go to: %q", name, s)
		}
	}
}

// TestNoActiveConnectionMessages_CarryNoRetiredWording. The prefixes these
// replaced are banned wording now. A sentence that quoted one would put the
// retired phrase back into every 502 body in the set.
func TestNoActiveConnectionMessages_CarryNoRetiredWording(t *testing.T) {
	for name, s := range map[string]string{
		"NoActiveGitConnectionMessage":    NoActiveGitConnectionMessage,
		"NoActiveArgocdConnectionMessage": NoActiveArgocdConnectionMessage,
	} {
		lowered := strings.ToLower(s)
		for _, retired := range []string{"no active git connection:", "no active argocd connection:"} {
			if strings.Contains(lowered, retired) {
				t.Errorf("%s contains the retired prefix %q", name, retired)
			}
		}
	}
}

// TestNoActiveConnectionErrors_AreNotEachOther guards the copy-paste failure:
// two constants pointing at one string, so half the endpoints in the set start
// naming the wrong half of the connection.
func TestNoActiveConnectionErrors_AreNotEachOther(t *testing.T) {
	if errors.Is(ErrNoActiveGitConnection, ErrNoActiveArgocdConnection) {
		t.Fatal("the two sentinel errors are the same value — every ArgoCD failure would answer with the Git sentence or the reverse")
	}
}
