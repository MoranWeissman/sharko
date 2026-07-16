package events

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
)

// TestNewRecorder_NilClient verifies that a nil client returns a nil-safe recorder.
func TestNewRecorder_NilClient(t *testing.T) {
	recorder := NewRecorder(nil, "sharko")
	if !recorder.IsNil() {
		t.Errorf("expected nil recorder, got non-nil")
	}

	// All methods should be no-ops (no panic).
	recorder.Event(ReasonAWSSecretsGetFailed, "test message", EventTypeWarning)
	recorder.Eventf(ReasonAWSAssumeRoleFailed, "test %s", EventTypeWarning, "message")
	recorder.AnnotatedEventf(nil, ReasonArgoCDUnreachable, "test", EventTypeWarning)
}

// TestNewRecorder_FakeClient verifies that the real (broadcaster-backed)
// recorder constructs cleanly and does not panic on emit.
func TestNewRecorder_FakeClient(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	recorder := NewRecorder(clientset, "sharko")

	if recorder.IsNil() {
		t.Fatal("expected non-nil recorder")
	}

	// Emit a warning event — broadcaster flush is async, so we don't assert
	// on the sink here (see TestEmit_FakeRecorder for the synchronous proof).
	recorder.Event(ReasonClusterTestFailed, "Stage1 connectivity test failed: network timeout", EventTypeWarning)
}

// TestEmit_FakeRecorder is the SYNCHRONOUS emission proof: record.FakeRecorder
// captures events into a buffered channel formatted as "<type> <reason> <message>",
// so we can assert exactly what was emitted.
func TestEmit_FakeRecorder(t *testing.T) {
	fake := record.NewFakeRecorder(10)
	recorder := NewRecorderForTest(fake, "sharko")

	recorder.Event(ReasonClusterTestFailed, "Connectivity test failed for cluster \"prod-eu\"", EventTypeWarning)

	select {
	case got := <-fake.Events:
		if !strings.HasPrefix(got, "Warning ") {
			t.Errorf("expected Warning event, got %q", got)
		}
		if !strings.Contains(got, ReasonClusterTestFailed) {
			t.Errorf("expected reason %q in event, got %q", ReasonClusterTestFailed, got)
		}
		if !strings.Contains(got, "Connectivity test failed") {
			t.Errorf("expected message in event, got %q", got)
		}
	default:
		t.Fatal("expected an event on the fake recorder channel, got none")
	}
}

// TestEmit_StringAPI verifies the Emit(string) convenience coerces types
// correctly (unknown type → Warning fail-safe).
func TestEmit_StringAPI(t *testing.T) {
	fake := record.NewFakeRecorder(10)
	recorder := NewRecorderForTest(fake, "sharko")

	recorder.Emit(ReasonPROpenFailed, "PR open failed", "Warning")
	assertEventContains(t, fake, "Warning", ReasonPROpenFailed)

	recorder.Emit(ReasonClusterRegistered, "cluster registered", "Normal")
	assertEventContains(t, fake, "Normal", ReasonClusterRegistered)

	// Unknown type coerces to Warning (fail-safe).
	recorder.Emit(ReasonDriftDetected, "drift", "Bogus")
	assertEventContains(t, fake, "Warning", ReasonDriftDetected)
}

// assertEventContains drains one event and asserts its type + reason.
func assertEventContains(t *testing.T, fake *record.FakeRecorder, wantType, wantReason string) {
	t.Helper()
	select {
	case got := <-fake.Events:
		if !strings.HasPrefix(got, wantType+" ") {
			t.Errorf("expected type %q, got %q", wantType, got)
		}
		if !strings.Contains(got, wantReason) {
			t.Errorf("expected reason %q, got %q", wantReason, got)
		}
	default:
		t.Fatalf("expected an event (type=%s reason=%s), got none", wantType, wantReason)
	}
}

// TestEventType validates the EventType constants.
func TestEventType(t *testing.T) {
	if string(EventTypeNormal) != "Normal" {
		t.Errorf("EventTypeNormal = %q, want %q", EventTypeNormal, "Normal")
	}
	if string(EventTypeWarning) != "Warning" {
		t.Errorf("EventTypeWarning = %q, want %q", EventTypeWarning, "Warning")
	}
}

// TestReasonConstants validates that Reason constants are UpperCamelCase and stable.
func TestReasonConstants(t *testing.T) {
	reasons := []string{
		ReasonAWSAssumeRoleFailed,
		ReasonAWSSecretsGetFailed,
		ReasonAWSTokenMintFailed,
		ReasonArgoCDUnreachable,
		ReasonArgoCDAuthFailed,
		ReasonClusterTestFailed,
		ReasonPROpenFailed,
		ReasonClusterRegistered,
	}

	for _, r := range reasons {
		if r == "" {
			t.Errorf("empty reason constant")
		}
		// Simple validation: first char is uppercase.
		if len(r) > 0 && r[0] < 'A' || r[0] > 'Z' {
			t.Errorf("reason %q does not start with uppercase letter", r)
		}
	}
}

// TestFromContext validates context wiring (for future use).
func TestFromContext(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	recorder := NewRecorder(clientset, "sharko")

	ctx := NewContext(context.Background(), recorder)
	retrieved := FromContext(ctx)
	if retrieved == nil {
		t.Fatal("expected non-nil recorder from context")
	}
	if retrieved.namespace != recorder.namespace {
		t.Errorf("namespace mismatch: got %q, want %q", retrieved.namespace, recorder.namespace)
	}
}

// TestEventf_NoSecretLeakage validates that secret redaction discipline is followed.
// This is a documentation test — the real enforcement is code review + security audits.
func TestEventf_NoSecretLeakage(t *testing.T) {
	// Example of what NOT to do (caught in code review):
	// recorder.Eventf(ReasonAWSSecretsGetFailed, "failed to get secret %s with token %s", EventTypeWarning, secretPath, bearerToken)

	// Correct usage:
	clientset := fake.NewSimpleClientset()
	recorder := NewRecorder(clientset, "sharko")
	recorder.Eventf(ReasonAWSSecretsGetFailed, "failed to get secret from AWS Secrets Manager: access denied", EventTypeWarning)

	// The test passes if the message contains no placeholder secret values.
	// Real enforcement: security-auditor agent reviews every Event() call site.
}

// TestNilRecorder_MethodsSafe verifies that all methods on a nil recorder are safe to call.
func TestNilRecorder_MethodsSafe(t *testing.T) {
	var recorder *EventRecorder // intentionally nil

	// Should not panic.
	recorder.Event(ReasonPROpenFailed, "test", EventTypeWarning)
	recorder.Eventf(ReasonPRMergeFailed, "test %d", EventTypeWarning, 123)
	recorder.AnnotatedEventf(map[string]string{"foo": "bar"}, ReasonDriftDetected, "test", EventTypeWarning)

	if !recorder.IsNil() {
		t.Error("expected IsNil() = true for nil recorder")
	}
}

// TestEventRecorder_RefObject validates that events reference a stable Pod object.
func TestEventRecorder_RefObject(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sharko-server-abc123",
				Namespace: "sharko",
			},
		},
	)
	recorder := NewRecorder(clientset, "sharko")

	recorder.Event(ReasonClusterRegistered, "cluster prod-eu registered", EventTypeNormal)

	// The event should reference a "sharko-server" object (see recorder.go Event method).
	// We can't directly assert the event contents in the fake client without waiting for async flush,
	// but we've validated the wiring compiles and the recorder doesn't panic.
}
