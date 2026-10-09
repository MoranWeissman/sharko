package auth

import (
	"strings"
	"testing"
)

// TestMaybeLogBootstrapCredential_PasswordNotInStructuredLog is the
// defense-in-depth regression guard for V2-cleanup-1.
//
// Contract under test: the auto-generated bootstrap admin password MUST
// NEVER appear in the structured slog stream, regardless of what the
// RedactHandler wrapper does. The slog stream carries only the
// "bootstrap admin generated" audit event (username only); the credential
// itself is routed to os.Stdout via fmt.Fprintln (see store.go).
//
// If a future refactor re-introduces `"password", password` as a slog attr
// at the bootstrap call site, this test FAILS — the unique sentinel value
// would appear in the captured buffer even if the RedactHandler is wired
// (the test captures the raw default logger, not the wrapped chain).
func TestMaybeLogBootstrapCredential_PasswordNotInStructuredLog(t *testing.T) {
	t.Setenv(EnvBootstrapAdminPassword, "")

	const sentinel = "test-bootstrap-pw-do-not-leak"
	s := newBootstrapStore(t, map[string][]byte{
		"admin.password":        []byte("$2a$10$fakebcrypt"),
		"admin.initialPassword": []byte(sentinel),
	})

	buf, restore := captureSlog(t)
	defer restore()

	s.MaybeLogBootstrapCredential()

	out := buf.String()

	// LOAD-BEARING ASSERTION — the sentinel password MUST NOT appear in
	// captured structured-log output. If this assertion fires, the
	// V2-cleanup-1 contract was reverted.
	if strings.Contains(out, sentinel) {
		t.Fatalf("BOOTSTRAP PASSWORD LEAKED INTO STRUCTURED LOG: sentinel %q found in slog buffer:\n%s", sentinel, out)
	}

	// The audit event still fires — log scrapers can grep for the
	// timestamped "bootstrap admin generated" line without learning the
	// credential.
	if !strings.Contains(out, "bootstrap admin generated") {
		t.Fatalf("expected audit event 'bootstrap admin generated' in slog buffer (so scrapers can grep the timestamp), got:\n%s", out)
	}
}
