package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// captureRecord runs fn against a RedactHandler-wrapped JSON handler and
// returns the decoded JSON record. Single-record harness so each test can
// assert on a fresh emission.
func captureRecord(t *testing.T, fn func(*slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewRedactHandler(inner))
	fn(logger)

	raw := bytes.TrimSpace(buf.Bytes())
	if len(raw) == 0 {
		t.Fatalf("no log record emitted")
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decode emitted record: %v (raw=%q)", err, buf.String())
	}
	return rec
}

// ---------------------------------------------------------------------------
// Sensitive-key heuristic
// ---------------------------------------------------------------------------

func TestRedact_SensitiveKey_Token(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("auth", "token", "ghp_abcd1234567890")
	})
	if rec["token"] != redactedPlaceholder {
		t.Fatalf("expected token redacted, got %v", rec["token"])
	}
}

func TestRedact_SensitiveKey_Password(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("login", "password", "hunter2")
	})
	if rec["password"] != redactedPlaceholder {
		t.Fatalf("expected password redacted, got %v", rec["password"])
	}
}

func TestRedact_SensitiveKey_Kubeconfig(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("cluster", "kubeconfig", "apiVersion: v1\nkind: Config\n...")
	})
	if rec["kubeconfig"] != redactedPlaceholder {
		t.Fatalf("expected kubeconfig redacted, got %v", rec["kubeconfig"])
	}
}

func TestRedact_SensitiveKey_Secret(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("provider", "secret", "raw-secret-material")
	})
	if rec["secret"] != redactedPlaceholder {
		t.Fatalf("expected secret redacted, got %v", rec["secret"])
	}
}

func TestRedact_SensitiveKey_PAT(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("git", "pat", "github_pat_11ABC...")
	})
	if rec["pat"] != redactedPlaceholder {
		t.Fatalf("expected pat redacted, got %v", rec["pat"])
	}
}

func TestRedact_SensitiveKey_BearerToken(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("api", "bearer_token", "eyJ.foo.bar")
	})
	if rec["bearer_token"] != redactedPlaceholder {
		t.Fatalf("expected bearer_token redacted, got %v", rec["bearer_token"])
	}
}

func TestRedact_SensitiveKey_Authorization(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("http", "authorization", "Bearer abc")
	})
	if rec["authorization"] != redactedPlaceholder {
		t.Fatalf("expected authorization redacted, got %v", rec["authorization"])
	}
}

func TestRedact_SensitiveKey_APIKey(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("ai", "api_key", "sk-abc123")
	})
	if rec["api_key"] != redactedPlaceholder {
		t.Fatalf("expected api_key redacted, got %v", rec["api_key"])
	}
}

func TestRedact_SensitiveKey_PrivateKey(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("tls", "private_key", "-----BEGIN PRIVATE KEY-----...")
	})
	if rec["private_key"] != redactedPlaceholder {
		t.Fatalf("expected private_key redacted, got %v", rec["private_key"])
	}
}

func TestRedact_SensitiveKey_CertData(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("tls", "cert_data", "-----BEGIN CERTIFICATE-----...")
	})
	if rec["cert_data"] != redactedPlaceholder {
		t.Fatalf("expected cert_data redacted, got %v", rec["cert_data"])
	}
}

// ---- Suffix-matching dynamic keys ----

func TestRedact_SensitiveSuffix_DbPassword(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("connect", "db_password", "hunter2")
	})
	if rec["db_password"] != redactedPlaceholder {
		t.Fatalf("expected db_password redacted via suffix, got %v", rec["db_password"])
	}
}

func TestRedact_SensitiveSuffix_ArgocdToken(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("argocd", "argocd_token", "abc123")
	})
	if rec["argocd_token"] != redactedPlaceholder {
		t.Fatalf("expected argocd_token redacted via suffix, got %v", rec["argocd_token"])
	}
}

func TestRedact_SensitiveSuffix_WebhookSecret(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("hook", "webhook_secret", "shhh")
	})
	if rec["webhook_secret"] != redactedPlaceholder {
		t.Fatalf("expected webhook_secret redacted via suffix, got %v", rec["webhook_secret"])
	}
}

func TestRedact_SensitiveSuffix_SigningKey(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("sign", "signing_key", "MIIBIjAN...")
	})
	if rec["signing_key"] != redactedPlaceholder {
		t.Fatalf("expected signing_key redacted via suffix, got %v", rec["signing_key"])
	}
}

// ---- Case-insensitive matching ----

func TestRedact_SensitiveKey_CaseInsensitive(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("api", "API_KEY", "sk-XYZ")
	})
	if rec["API_KEY"] != redactedPlaceholder {
		t.Fatalf("expected API_KEY redacted (case-insensitive), got %v", rec["API_KEY"])
	}
}

func TestRedact_SensitiveSuffix_CaseInsensitive(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("git", "GitHub_TOKEN", "ghp_xyz")
	})
	if rec["GitHub_TOKEN"] != redactedPlaceholder {
		t.Fatalf("expected GitHub_TOKEN redacted (case-insensitive suffix), got %v", rec["GitHub_TOKEN"])
	}
}

// ---- Non-sensitive keys must not redact ----

func TestRedact_NonSensitiveKey_Preserved(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("cluster", "name", "prod-eu", "region", "us-east-1", "count", 42)
	})
	if rec["name"] != "prod-eu" {
		t.Fatalf("expected name preserved, got %v", rec["name"])
	}
	if rec["region"] != "us-east-1" {
		t.Fatalf("expected region preserved, got %v", rec["region"])
	}
	if rec["count"].(float64) != 42 {
		t.Fatalf("expected count preserved, got %v", rec["count"])
	}
}

// ---------------------------------------------------------------------------
// JWT-shape detection
// ---------------------------------------------------------------------------

func TestRedact_JWTShape_RealJWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature"
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("auth", "body", jwt)
	})
	if rec["body"] != redactedPlaceholder {
		t.Fatalf("expected JWT-shape value redacted via regex, got %v", rec["body"])
	}
}

func TestRedact_JWTShape_NotAJWT_Preserved(t *testing.T) {
	// Starts with eyJ but is not a 3-segment JWT (no dots).
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("info", "msg", "eyJsomethingsomethingsomething")
	})
	if rec["msg"] == redactedPlaceholder {
		t.Fatalf("expected non-JWT eyJ-prefix string preserved, got REDACTED")
	}
}

func TestRedact_JWTShape_InNonSensitiveKey(t *testing.T) {
	// A JWT pasted into a generic-named field MUST still be caught
	// by the value-shape detector.
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature"
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("response", "payload", jwt)
	})
	if rec["payload"] != redactedPlaceholder {
		t.Fatalf("expected JWT in non-sensitive key 'payload' redacted, got %v", rec["payload"])
	}
}

func TestRedact_JWTShape_SubstringInLogMessage_Preserved(t *testing.T) {
	// A log line that mentions a JWT inside a sentence (not as the
	// whole value) must not be redacted — regex is anchored.
	msg := "received token like eyJaaa.bbb.ccc from client"
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("event", "detail", msg)
	})
	if rec["detail"] == redactedPlaceholder {
		t.Fatalf("expected substring-shaped sentence preserved, got REDACTED")
	}
}

// ---------------------------------------------------------------------------
// Base64-blob detection
// ---------------------------------------------------------------------------

func TestRedact_Base64Blob_LongBlob(t *testing.T) {
	// 120-char pure base64 string.
	blob := strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", 4)[:120]
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("payload", "data", blob)
	})
	if rec["data"] != redactedPlaceholder {
		t.Fatalf("expected long base64 blob redacted, got %v", rec["data"])
	}
}

func TestRedact_Base64Blob_ShortNotRedactedByLength(t *testing.T) {
	// Pure base64 charset, but only 40 chars — below the 100-char
	// threshold, so the value detector must not fire. (A short token
	// is still caught when its KEY is sensitive — see the key tests.)
	shortB64 := "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NQ=="
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("info", "short_id", shortB64)
	})
	if rec["short_id"] == redactedPlaceholder {
		t.Fatalf("expected short base64-shaped string preserved (below 100-char threshold), got REDACTED")
	}
}

func TestRedact_Base64Blob_InNonSensitiveKey(t *testing.T) {
	// A long base64 blob in an innocuous-named field must still be
	// caught by the value detector.
	blob := strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", 4)[:120]
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("snapshot", "encoded", blob)
	})
	if rec["encoded"] != redactedPlaceholder {
		t.Fatalf("expected long base64 in non-sensitive key 'encoded' redacted, got %v", rec["encoded"])
	}
}

func TestRedact_Base64Blob_LongNonBase64_Preserved(t *testing.T) {
	// 200-char value but contains characters outside the base64
	// alphabet (spaces, punctuation) — must not be redacted.
	longNonB64 := strings.Repeat("hello world! this is a long sentence. ", 6)
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("info", "narrative", longNonB64)
	})
	if rec["narrative"] == redactedPlaceholder {
		t.Fatalf("expected long non-base64 string preserved, got REDACTED")
	}
}

// ---------------------------------------------------------------------------
// _unsafe_ opt-out
// ---------------------------------------------------------------------------

func TestRedact_UnsafePrefix_BypassesKeyHeuristic(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("debug", "_unsafe_token", "raw-token-for-dev-debug")
	})
	if rec["_unsafe_token"] != "raw-token-for-dev-debug" {
		t.Fatalf("expected _unsafe_token preserved, got %v", rec["_unsafe_token"])
	}
}

func TestRedact_UnsafePrefix_BypassesJWTRegex(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature"
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("debug", "_unsafe_jwt", jwt)
	})
	if rec["_unsafe_jwt"] != jwt {
		t.Fatalf("expected _unsafe_jwt preserved, got %v", rec["_unsafe_jwt"])
	}
}

func TestRedact_UnsafePrefix_BypassesBase64Detector(t *testing.T) {
	blob := strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", 4)[:120]
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("debug", "_unsafe_blob", blob)
	})
	if rec["_unsafe_blob"] != blob {
		t.Fatalf("expected _unsafe_blob preserved, got %v", rec["_unsafe_blob"])
	}
}

// ---------------------------------------------------------------------------
// Composition — multiple attrs, some redacted, some not
// ---------------------------------------------------------------------------

func TestRedact_Composition_OnlySensitiveChanged(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("login",
			"username", "alice",
			"password", "hunter2",
			"role", "admin",
			"count", 3,
		)
	})
	if rec["username"] != "alice" {
		t.Fatalf("expected username preserved, got %v", rec["username"])
	}
	if rec["password"] != redactedPlaceholder {
		t.Fatalf("expected password redacted, got %v", rec["password"])
	}
	if rec["role"] != "admin" {
		t.Fatalf("expected role preserved, got %v", rec["role"])
	}
	if rec["count"].(float64) != 3 {
		t.Fatalf("expected count preserved, got %v", rec["count"])
	}
}

// ---------------------------------------------------------------------------
// Nested groups
// ---------------------------------------------------------------------------

func TestRedact_NestedGroup_SensitiveKeyInside(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("creds_event",
			slog.Group("creds",
				slog.String("token", "ghp_abc"),
				slog.String("user", "alice"),
			),
		)
	})
	creds, ok := rec["creds"].(map[string]any)
	if !ok {
		t.Fatalf("expected creds group, got %T %v", rec["creds"], rec["creds"])
	}
	if creds["token"] != redactedPlaceholder {
		t.Fatalf("expected creds.token redacted, got %v", creds["token"])
	}
	if creds["user"] != "alice" {
		t.Fatalf("expected creds.user preserved, got %v", creds["user"])
	}
}

func TestRedact_DeeplyNestedGroup(t *testing.T) {
	// Group inside a group — recursion must still catch the inner secret.
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("payload",
			slog.Group("outer",
				slog.Group("inner",
					slog.String("password", "shhh"),
				),
			),
		)
	})
	outer := rec["outer"].(map[string]any)
	inner := outer["inner"].(map[string]any)
	if inner["password"] != redactedPlaceholder {
		t.Fatalf("expected outer.inner.password redacted, got %v", inner["password"])
	}
}

func TestRedact_NestedGroup_UnsafeBypass(t *testing.T) {
	// _unsafe_ inside a group still bypasses redaction.
	rec := captureRecord(t, func(l *slog.Logger) {
		l.Info("debug",
			slog.Group("dev",
				slog.String("_unsafe_token", "raw"),
				slog.String("token", "should-redact"),
			),
		)
	})
	dev := rec["dev"].(map[string]any)
	if dev["_unsafe_token"] != "raw" {
		t.Fatalf("expected _unsafe_token preserved in group, got %v", dev["_unsafe_token"])
	}
	if dev["token"] != redactedPlaceholder {
		t.Fatalf("expected token redacted in group, got %v", dev["token"])
	}
}

// ---------------------------------------------------------------------------
// WithAttrs — pre-attached attrs are redacted once at attachment time
// ---------------------------------------------------------------------------

func TestRedact_WithAttrs_RedactedAtAttachment(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewRedactHandler(inner)).With(
		"username", "alice",
		"token", "ghp_secret",
	)
	logger.Info("event")

	raw := bytes.TrimSpace(buf.Bytes())
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec["username"] != "alice" {
		t.Fatalf("expected username preserved via With, got %v", rec["username"])
	}
	if rec["token"] != redactedPlaceholder {
		t.Fatalf("expected token redacted via With, got %v", rec["token"])
	}
}

// ---------------------------------------------------------------------------
// NewRedactHandler edge cases
// ---------------------------------------------------------------------------

func TestNewRedactHandler_NilInner(t *testing.T) {
	if got := NewRedactHandler(nil); got != nil {
		t.Fatalf("expected nil when wrapping nil handler, got %v", got)
	}
}

func TestRedact_EmptyKey_NotMatched(t *testing.T) {
	// An empty-key attribute (used for group inlining) must not
	// trigger suffix-only matching.
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewRedactHandler(inner))
	// Use the Group("") inlining pattern with a non-sensitive child.
	logger.Info("event", slog.Group("", slog.String("region", "us-east-1")))

	raw := bytes.TrimSpace(buf.Bytes())
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec["region"] != "us-east-1" {
		t.Fatalf("expected region preserved with empty-key inlined group, got %v", rec["region"])
	}
}

// ---------------------------------------------------------------------------
// Enabled delegation
// ---------------------------------------------------------------------------

func TestRedactHandler_EnabledDelegates(t *testing.T) {
	inner := slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := NewRedactHandler(inner)
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatalf("expected Debug level filtered out by inner (Warn threshold)")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatalf("expected Error level allowed through")
	}
}
