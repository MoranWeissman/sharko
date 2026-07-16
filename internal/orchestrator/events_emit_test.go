package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/events"
)

// spyEmitter is a test double satisfying orchestrator.EventEmitter. It records
// every emitted (reason, message, eventType) tuple for assertion.
type spyEmitter struct {
	events []spyEvent
}

type spyEvent struct {
	reason, message, eventType string
}

func (s *spyEmitter) Emit(reason, message, eventType string) {
	s.events = append(s.events, spyEvent{reason: reason, message: message, eventType: eventType})
}

// TestCommitChangesWithMeta_PROpenFailure_EmitsWarning proves that a PR-open
// failure emits exactly one Warning event with ReasonPROpenFailed, and that the
// message contains no secret material.
func TestCommitChangesWithMeta_PROpenFailure_EmitsWarning(t *testing.T) {
	git := newMockGitProvider()
	git.prErr = errors.New("git provider rejected the PR (403)")

	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	spy := &spyEmitter{}
	orch.SetEventEmitter(spy)

	// AddAddon funnels through commitChangesWithMeta -> CreatePullRequest,
	// which our mock fails via prErr.
	_, err := orch.AddAddon(context.Background(), AddAddonRequest{
		Name:    "brand-new-addon",
		Chart:   "brand-new-addon",
		RepoURL: "https://charts.example.com",
		Version: "1.0.0",
	})
	if err == nil {
		t.Fatal("expected an error from AddAddon when the PR fails to open")
	}

	if len(spy.events) != 1 {
		t.Fatalf("expected exactly 1 emitted event, got %d: %+v", len(spy.events), spy.events)
	}
	ev := spy.events[0]
	if ev.reason != events.ReasonPROpenFailed {
		t.Errorf("expected reason %q, got %q", events.ReasonPROpenFailed, ev.reason)
	}
	if ev.eventType != "Warning" {
		t.Errorf("expected Warning event, got %q", ev.eventType)
	}
	assertNoSecretMaterial(t, ev.message)
}

// TestCommitChangesWithMeta_Success_NoEvent proves the happy path emits NO
// event — no per-operation spam on success.
func TestCommitChangesWithMeta_Success_NoEvent(t *testing.T) {
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	spy := &spyEmitter{}
	orch.SetEventEmitter(spy)

	_, err := orch.AddAddon(context.Background(), AddAddonRequest{
		Name:    "brand-new-addon",
		Chart:   "brand-new-addon",
		RepoURL: "https://charts.example.com",
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error on happy path: %v", err)
	}

	if len(spy.events) != 0 {
		t.Errorf("expected NO events on success, got %d: %+v", len(spy.events), spy.events)
	}
}

// TestEmitWarning_NilEmitter proves the nil-emitter path is safe (out-of-cluster
// / dev mode — no emitter wired). Must not panic.
func TestEmitWarning_NilEmitter(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	// No SetEventEmitter call — eventEmitter is nil.
	orch.emitWarning(events.ReasonPROpenFailed, "should not panic")
}

// assertNoSecretMaterial checks a message contains no obvious secret-shaped
// tokens. This is a lightweight guard; the real enforcement is security review.
func assertNoSecretMaterial(t *testing.T, message string) {
	t.Helper()
	banned := []string{
		"sharko_",        // API key prefix
		"BEGIN ",         // PEM blocks
		"eyJ",            // JWT / base64 header
		"AKIA",           // AWS access key id prefix
		"kubeconfig",     // kubeconfig material
		"704909879244",   // the forbidden real account id
	}
	for _, b := range banned {
		if strings.Contains(message, b) {
			t.Errorf("event message contains banned secret-shaped token %q: %q", b, message)
		}
	}
}
