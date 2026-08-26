package main

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

// Story B11 — a bad SHARKO_TRUSTED_PROXIES stops the server at startup.
//
// The rule is not "warn and carry on". A list Sharko cannot read is a list it
// cannot enforce, and carrying on would mean running with the rate limiter
// keyed on something nobody chose. So `sharko serve` refuses to start.
//
// These tests drive the real serve command, not the parser.

// serveEnv puts the command in the state each test below needs, and restores
// the default logger the command replaces.
//
// KUBERNETES_SERVICE_HOST makes platform.Detect report Kubernetes mode, where
// building the config store is the first thing that can fail. Leaving
// SHARKO_ENCRYPTION_KEY empty makes it fail, with a distinctive error. Which
// of the two errors comes back is what tells us which step ran first.
func serveEnv(t *testing.T, trustedProxies string) {
	t.Helper()
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })

	t.Setenv(api.TrustedProxiesEnv, trustedProxies)
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("SHARKO_ENCRYPTION_KEY", "")
}

// encryptionKeyFailure is the error the config-store step returns in
// Kubernetes mode with no encryption key. It is the marker for "startup got
// past the trusted-proxy check and into real work".
const encryptionKeyFailure = "SHARKO_ENCRYPTION_KEY is required"

func TestServe_InvalidTrustedProxiesFailsStartup(t *testing.T) {
	for _, raw := range []string{"garbage", "10.0.0.0/33", "*", "0.0.0.0/0"} {
		t.Run(raw, func(t *testing.T) {
			serveEnv(t, raw)

			err := serveCmd.RunE(serveCmd, nil)
			if err == nil {
				t.Fatalf("sharko serve started with an unreadable %s — it must refuse", api.TrustedProxiesEnv)
			}
			if !strings.Contains(err.Error(), api.TrustedProxiesEnv) {
				t.Errorf("the startup error does not name the setting, so an operator cannot tell what to fix: %v", err)
			}
			if strings.Contains(err.Error(), raw) {
				t.Errorf("the startup error repeats the configured value (%q): %v", raw, err)
			}
		})
	}
}

// TestServe_TrustedProxiesParsedBeforeTheConfigStore makes the ORDERING real.
//
// It used to be prose. A comment on this file claimed the check "runs BEFORE
// the server does anything else: the command returns the error without opening
// a config store, contacting a cluster, or binding the port" — and nothing
// observed it. Moving the ParseTrustedProxies block below the config-store
// construction in serve.go left both ./cmd/sharko/ and ./internal/api/
// reporting ok. The code was ordered correctly and the guard protecting that
// order was decorative, so a future reorder would ship an install that opens
// the config store — and can create the initial-admin Secret — before refusing
// to start.
//
// The order is observable as soon as BOTH steps would fail: whichever error
// comes back is the step that ran first. Here the list is unreadable AND the
// encryption key is missing, so the answer must be the trusted-proxy error.
// TestServe_ValidTrustedProxiesGetsPastTheCheck is the other half — it proves
// the encryption-key failure is genuinely reachable, so this test is
// discriminating and not just observing the only error there is.
func TestServe_TrustedProxiesParsedBeforeTheConfigStore(t *testing.T) {
	serveEnv(t, "garbage")

	err := serveCmd.RunE(serveCmd, nil)
	if err == nil {
		t.Fatal("sharko serve started with an unreadable trusted-proxy list")
	}
	if strings.Contains(err.Error(), encryptionKeyFailure) {
		t.Fatalf("startup opened the config store BEFORE checking %s — a server with an "+
			"unenforceable proxy list got as far as touching cluster state. Error: %v",
			api.TrustedProxiesEnv, err)
	}
	if !strings.Contains(err.Error(), api.TrustedProxiesEnv) {
		t.Fatalf("expected the trusted-proxy startup error, got: %v", err)
	}
}

// TestServe_ValidTrustedProxiesGetsPastTheCheck earns its name now: it drives
// the real serve command, not the parser.
//
// It used to call ParseTrustedProxies and Count() and assert on those, which
// duplicated TestParseTrustedProxies_Valid and proved nothing about startup —
// the name promised a positive control on the startup path that the body did
// not provide.
//
// The command cannot be allowed to finish (it would bind a port and serve), so
// it is stopped at the first step after the check: the config store, failing
// on the missing encryption key. Getting THAT error is the proof — a readable
// list did not stop startup, and the next step really did run.
func TestServe_ValidTrustedProxiesGetsPastTheCheck(t *testing.T) {
	serveEnv(t, "10.0.0.1, 2001:db8::1")

	err := serveCmd.RunE(serveCmd, nil)
	if err == nil {
		t.Fatal("serve returned no error at all; this test must never let a server start listening")
	}
	if strings.Contains(err.Error(), api.TrustedProxiesEnv) {
		t.Fatalf("a plain list of two proxy addresses was refused at startup: %v", err)
	}
	if !strings.Contains(err.Error(), encryptionKeyFailure) {
		t.Fatalf("expected startup to get past the trusted-proxy check and stop at the config "+
			"store, got: %v", err)
	}
}
