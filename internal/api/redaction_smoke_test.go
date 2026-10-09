package api

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/logging"
)

// sampleKubeconfig is a realistic-shape kubeconfig used to exercise the
// redaction pipeline. It contains:
//   - a server URL (NOT secret on its own)
//   - a base64 CA blob >100 chars (must be caught by the base64-blob detector)
//   - a JWT-shape bearer token (must be caught by the JWT detector)
//   - the literal "kubeconfig" string we will use as a slog key (must be
//     caught by the sensitive-key heuristic)
//
// The CA + token strings are synthetic but follow the exact shape the AWS
// SM provider and kubeconfig parser would surface. The whole YAML is also
// long enough that an accidental whole-blob slog leak would be obvious.
const sampleKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: prod-eu
  cluster:
    server: https://abc123.gr7.us-east-1.eks.amazonaws.com
    certificate-authority-data: ` +
	"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURUVENDQWpXZ0F3SUJBZ0lVQUxsWVhWMUNqMVJ4QjFqaGNkSFh3UU5qWnpVd0RRWUpLb1pJaHZjTkFRRUxCUUF3CmRGTjEx" +
	`
contexts:
- name: prod-eu
  context:
    cluster: prod-eu
    user: prod-eu-user
users:
- name: prod-eu-user
  user:
    token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6ZGVmYXVsdCJ9.signature_part_here_xyz
`

// sampleCABlob is a >100-char base64 string extracted from the kubeconfig.
const sampleCABlob = "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURUVENDQWpXZ0F3SUJBZ0lVQUxsWVhWMUNqMVJ4QjFqaGNkSFh3UU5qWnpVd0RRWUpLb1pJaHZjTkFRRUxCUUF3CmRGTjEx"

// sampleJWT is the bearer-token-shape value from the kubeconfig.
const sampleJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6ZGVmYXVsdCJ9.signature_part_here_xyz"

// TestRedactionSmoke_KubeconfigDoesNotLeak runs a series of plausible-shape
// slog calls that an accidental change could introduce in the cluster-
// register path, captures the emitted log lines through the redact-wrapped
// handler chain installed at slog init, and asserts:
//
//   - NO log line contains the whole kubeconfig YAML
//   - NO log line contains the bearer JWT verbatim
//   - NO log line contains the base64 CA blob verbatim
//   - Non-sensitive metadata (cluster name, region) IS visible
//
// This is the "audit guard": if a future refactor accidentally adds
// `slog.Info("registering", "kubeconfig", kubeconfigBytes)` or similar,
// this test will fire BEFORE the leak reaches production.
func TestRedactionSmoke_KubeconfigDoesNotLeak(t *testing.T) {
	// Install the same handler chain as cmd/sharko/serve.go: redact wraps
	// the base JSON handler. Capture into a buffer so we can introspect
	// the emitted bytes.
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(logging.NewRedactHandler(base)))

	ctx := logging.WithRequestID(context.Background(), "req-smoke-1")
	log := logging.LoggerFromContext(ctx)

	// Plausible-shape call sites that an accidental refactor might
	// introduce in the cluster-register code path. Each one would leak
	// credentials WITHOUT the redaction wrapper.
	log.Info("registering cluster",
		"cluster", "prod-eu",
		"region", "us-east-1",
		"kubeconfig", sampleKubeconfig, // key-name heuristic
	)
	log.Info("fetched credentials",
		"cluster", "prod-eu",
		"token", sampleJWT, // key-name heuristic
	)
	log.Info("auth payload",
		"cluster", "prod-eu",
		"body", sampleJWT, // value-shape (JWT regex) — innocuous key
	)
	log.Info("ca data",
		"cluster", "prod-eu",
		"encoded", sampleCABlob, // value-shape (base64 >100) — innocuous key
	)
	log.Info("authorization header",
		"cluster", "prod-eu",
		"authorization", "Bearer "+sampleJWT, // key-name heuristic
	)
	log.Debug("provider parsed structured JSON",
		"server", "https://abc123.gr7.us-east-1.eks.amazonaws.com",
		"private_key", sampleCABlob, // key-name heuristic
	)

	out := buf.String()

	// --- Leakage asserts ---

	// The full kubeconfig YAML must not appear anywhere. We check a
	// distinctive interior fragment (the "kind: Config" + "users:" pair)
	// to avoid being fooled by a stray newline/escaping difference.
	if strings.Contains(out, "kind: Config") && strings.Contains(out, "users:") &&
		strings.Contains(out, "certificate-authority-data") {
		t.Fatalf("leak: full kubeconfig YAML present in log output:\n%s", out)
	}

	// The bearer JWT must not appear verbatim in any record.
	if strings.Contains(out, sampleJWT) {
		t.Fatalf("leak: bearer JWT present verbatim in log output:\n%s", out)
	}

	// The base64 CA blob must not appear verbatim.
	if strings.Contains(out, sampleCABlob) {
		t.Fatalf("leak: base64 CA blob present verbatim in log output:\n%s", out)
	}

	// --- Visibility asserts (the wrapper must NOT scrub innocuous metadata) ---

	if !strings.Contains(out, "prod-eu") {
		t.Fatalf("expected non-sensitive cluster name preserved; missing from log output:\n%s", out)
	}
	if !strings.Contains(out, "us-east-1") {
		t.Fatalf("expected non-sensitive region preserved; missing from log output:\n%s", out)
	}
	// The redaction placeholder MUST be present — proves the wrapper
	// actually fired, not that all the values silently dropped.
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] placeholder in log output (wrapper not engaged?):\n%s", out)
	}
	// And the request_id from the contextual logger must thread through.
	if !strings.Contains(out, "req-smoke-1") {
		t.Fatalf("expected request_id req-smoke-1 in log output:\n%s", out)
	}
}

// TestRedactionSmoke_AuthorizationHeader covers the most common live-
// debugging mistake: dumping an HTTP request's Authorization header
// without thinking. The value detector (base64-shape) backstops the
// key detector here — even if a future rename calls the field something
// other than `authorization`, the bearer-token portion remains JWT-
// shaped and will be caught by value regex if used standalone.
func TestRedactionSmoke_AuthorizationHeader(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(logging.NewRedactHandler(base)))

	// Key-based redaction.
	slog.Info("incoming request", "authorization", "Bearer "+sampleJWT)
	// Value-shape backstop — different key, same JWT content.
	slog.Info("upstream response", "raw", sampleJWT)

	out := buf.String()
	if strings.Contains(out, sampleJWT) {
		t.Fatalf("leak: JWT visible despite redaction wrapper:\n%s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in output; wrapper appears bypassed:\n%s", out)
	}
}
