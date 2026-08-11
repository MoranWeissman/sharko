package api

// cred_error_sentinel_test.go — the proof for this hotfix.
//
// # What is being proved
//
// A credentials backend's own error text can carry credential material. An AWS
// SDK error can wrap a presigned URL, a token fragment, or a credential a
// provider chain put into its message. So the question is not "does Sharko log
// a secret" — it is "does a credentials backend's ERROR TEXT get out".
//
// The test drives the REAL AWS Secrets Manager provider (not a hand-written
// double) down the REAL structured-EKS path, and makes the REAL token mint fail
// with an error whose message CONTAINS a unique fake sentinel. That is the case
// that matters: it is exactly the shape of a real SDK error carrying credential
// material.
//
// Then it sweeps for the sentinel in every place it could come out:
//
//	the API response body
//	the captured log output
//	the STORED audit entry, read back through auditLog.List
//	GET /api/v1/audit
//	GET /api/v1/audit/stream
//	the error sentence a handler returns
//	CLI output (the CLI prints response bodies verbatim — see the CLI test)
//	the generated OpenAPI files
//	the scraped /metrics
//	Kubernetes events
//
// ...and it sweeps for it in every FORM a leak could take: raw, four base64
// spellings, SHA-256 in hex and base64, prefix and suffix fragments, its byte
// length as a number in a dozen labelled shapes, and star/bullet masks at
// length−1, length and length+1.
//
// # Why the audit half needs its own proof
//
// The audit log is read-by-anyone: audit.list is a VIEWER-role action, GET
// /audit returns entries whole, and GET /audit/stream marshals each entry
// straight off the subscriber channel. Sanitizing on the way out would have had
// to be done twice and the stream would have been the one that got missed. So
// the fix is at audit.Log.Add, and the test proves it by reading the STORED
// entry back — if the text were only hidden at render time, the stored entry
// would still carry it and TestCredErrorSentinel_StoredAuditEntry would fail.
//
// It also proves the typed cause does not linger: audit.Entry.Cause carries the
// real error only as far as Add, and Add clears it. A cause left hanging on a
// stored entry is a leak waiting for the next person who prints it with %+v, so
// the assertion uses %+v and reflection rather than JSON, which would hide it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/record"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// credSentinel is a made-up value that appears NOWHERE else in this repository.
// It stands in for credential material an AWS SDK error put into its own
// message. A search for it is therefore meaningful: it cannot show up in a log
// line, a response body or a metric label by accident.
const credSentinel = "ZR4T-cred-error-sentinel-8h2v6q-never-leaves-the-process-c1d5"

// structuredEKSPayload is a real structured-EKS descriptor. The provider's own
// format sniff sends it down the token-mint branch — the branch this hotfix is
// about — with no help from the test.
const structuredEKSPayload = `{
	"clusterName": "prod-eu",
	"host": "https://abc123.gr7.eu-west-1.eks.example.com",
	"caData": "",
	"region": "eu-west-1",
	"roleArn": "arn:aws:iam::000000000000:role/test-role"
}`

// mintFailingWithSentinel is the REAL mint seam, failing with an error whose
// message carries the sentinel. This is what a misbehaving AWS SDK looks like
// from Sharko's side.
func mintFailingWithSentinel(_ context.Context, _, _, _ string) (string, error) {
	return "", fmt.Errorf("operation error STS: AssumeRole, https response error: api error AccessDenied: %s", credSentinel)
}

// backendErrCarryingSentinel is a credentials-BACKEND read failure whose own
// text carries credential material — the same class of thing as a mint failure,
// and the one that actually travels outward wearing its text.
func backendErrCarryingSentinel() error {
	return fmt.Errorf("etcdserver: request failed while resolving %s", credSentinel)
}

// credProviderWithFailingMint builds a REAL credentials provider whose backing
// reads fail with an error whose text carries the sentinel.
//
// Which provider this is, and why, is a real finding of this hotfix:
//
// The AWS Secrets Manager provider deliberately does NOT surface a mint failure
// outward. GetCredentials tries the prefixed secret name, then the exact name,
// and when both fail — for ANY reason, a failed mint included — it returns its
// OWN "secret for cluster %q not found ... set secret_path" sentence. So on that
// backend the mint error's text can only ever reach the LOG line. That is
// exactly fix #1 of this hotfix, and it is proven separately by
// TestCredErrorSentinel_AWSSMMintFailureNeverReachesLogsOrResponse plus the
// log-source guard in internal/providers.
//
// The ArgoCD provider is where a backend failure's text genuinely travels OUT of
// the process: it wraps the underlying error with %w into what GetCredentials
// returns, and that value used to flow into the API response, the audit log, and
// the reconcile record. So it is the provider the sentinel is pushed down, and
// its Error() really does carry the sentinel — which is the point. credsafe.Mark
// leaves Error() alone on purpose, so any boundary that forgets to ask for the
// safe sentence will show the sentinel and the test will say where.
func credProviderWithFailingMint() providers.ClusterCredentialsProvider {
	return providers.NewArgoCDProviderWithFailingBackendForTest(backendErrCarryingSentinel())
}

// credProviderAWSSMWithFailingMint is the AWS Secrets Manager arm — aws_sm.go,
// the file the brief names, with the real structured-EKS payload and the real
// failing mint.
func credProviderAWSSMWithFailingMint() providers.ClusterCredentialsProvider {
	return providers.NewAWSSecretsManagerProviderForTest(structuredEKSPayload, mintFailingWithSentinel)
}

// --- the sweep -------------------------------------------------------------

// assertNoCredSentinel fails when text carries the sentinel in ANY form a leak
// could take.
//
// The forms are not decoration. Each one is a way a value has actually escaped
// from software before: re-encoded, hashed "for safety", truncated to a
// "harmless" prefix, reported as a length, or masked with a run of stars whose
// length gives the length away.
func assertNoCredSentinel(t *testing.T, where, text string) {
	t.Helper()
	sum := sha256.Sum256([]byte(credSentinel))
	n := len(credSentinel)

	forms := map[string]string{
		"the value itself":     credSentinel,
		"base64 (std)":         base64.StdEncoding.EncodeToString([]byte(credSentinel)),
		"base64 (raw std)":     base64.RawStdEncoding.EncodeToString([]byte(credSentinel)),
		"base64 (url)":         base64.URLEncoding.EncodeToString([]byte(credSentinel)),
		"base64 (raw url)":     base64.RawURLEncoding.EncodeToString([]byte(credSentinel)),
		"SHA-256 hex":          hex.EncodeToString(sum[:]),
		"SHA-256 base64":       base64.StdEncoding.EncodeToString(sum[:]),
		"first 8 characters":   credSentinel[:8],
		"first 16 characters":  credSentinel[:16],
		"last 8 characters":    credSentinel[n-8:],
		"last 16 characters":   credSentinel[n-16:],
		"middle 16 characters": credSentinel[n/2-8 : n/2+8],
	}
	for name, form := range forms {
		if strings.Contains(text, form) {
			t.Errorf("%s carries %s of the credentials-backend error text (%q)\n\nthe text was:\n%s", where, name, form, text)
		}
	}

	// The byte length, in every labelled shape a well-meaning log line uses. A
	// length narrows a guess at what is inside.
	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n),
		fmt.Sprintf(`"length": %d`, n),
		fmt.Sprintf(`"len":%d`, n),
		fmt.Sprintf(`"len": %d`, n),
		fmt.Sprintf(`"bytes":%d`, n),
		fmt.Sprintf(`"bytes": %d`, n),
		fmt.Sprintf(`"size":%d`, n),
		fmt.Sprintf(`"size": %d`, n),
		fmt.Sprintf(`"errorLength":%d`, n),
		fmt.Sprintf("length=%d", n),
		fmt.Sprintf("len=%d", n),
		fmt.Sprintf("bytes=%d", n),
		fmt.Sprintf("size=%d", n),
	} {
		if strings.Contains(text, shape) {
			t.Errorf("%s carries the error text's byte length (%q) — a length narrows a guess", where, shape)
		}
	}

	// Variable-length masks. A run of stars one character shorter, exactly as
	// long as, or one longer than the value gives its length away just as surely
	// as a number does.
	for _, ch := range []string{"*", "•", "x", "●", "#"} {
		for _, l := range []int{n - 1, n, n + 1} {
			if strings.Contains(text, strings.Repeat(ch, l)) {
				t.Errorf("%s carries a mask whose length tracks the error text (%d of %q)", where, l, ch)
			}
		}
	}
}

// captureSlog swaps slog's default logger for a buffer at Debug level — the
// lowest, so every line the code under test emits at any level is captured —
// and restores it afterwards. Not safe under t.Parallel(); no test here calls it.
func captureSlog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	fn()
	return buf.String()
}

// sanityCheckFixture proves the fixture really does carry the sentinel. Without
// this, a typo in the payload or the mint stub would make every assertion below
// pass while proving nothing at all.
func sanityCheckFixture(t *testing.T) {
	t.Helper()
	_, err := credProviderWithFailingMint().GetCredentials("prod-eu")
	if err == nil {
		t.Fatal("sanity check failed: the fixture's mint must FAIL — there is nothing to prove otherwise")
	}
	if !strings.Contains(err.Error(), credSentinel) {
		t.Fatalf(`sanity check failed: the error the provider returns internally must CONTAIN the sentinel, otherwise every assertion below is vacuous.

got: %v

This is deliberate. credsafe.Mark does NOT change what Error() says — internal callers, %%w chains, errors.As on typed provider errors and the handler's own "not found" substring check all keep working. Only what crosses a PUBLIC boundary is swapped for the fixed sentence.`, err)
	}
	if !credsafe.Is(err) {
		t.Fatal(`sanity check failed: the error is not marked as a credentials-backend failure, so no boundary can recognise it.

The classification is by TYPE (errors.Is against a sentinel), never by reading the error's words. If this fails, credsafe.Mark is missing from the provider boundary.`)
	}
}

// TestCredErrorSentinel_FixtureIsReal is the sanity check on its own, so a
// broken fixture fails loudly with its own name rather than silently making
// every other test in this file meaningless.
func TestCredErrorSentinel_FixtureIsReal(t *testing.T) {
	sanityCheckFixture(t)
}

// TestCredErrorSentinel_AWSSMMintFailureNeverReachesLogsOrResponse covers the
// AWS Secrets Manager arm — aws_sm.go, the file the brief names.
//
// On this backend a failed mint is swallowed into the provider's own "secret not
// found" sentence, so the mint error's text can only get out through a LOG line.
// This asserts it does not, driving the real provider and the real failing mint,
// and it also asserts the whole request-level surface stays clean.
func TestCredErrorSentinel_AWSSMMintFailureNeverReachesLogsOrResponse(t *testing.T) {
	provider := credProviderAWSSMWithFailingMint()

	// Sanity: the mint really does fail carrying the sentinel, even though the
	// provider chooses not to pass that failure outward.
	if _, err := mintFailingWithSentinel(context.Background(), "prod-eu", "eu-west-1", ""); err == nil ||
		!strings.Contains(err.Error(), credSentinel) {
		t.Fatal("sanity check failed: the mint stub must fail carrying the sentinel")
	}

	srv := newTestServer()
	srv.publishProviders(provider, nil, nil)
	router := NewRouter(srv, nil)

	var body string
	logs := captureSlog(t, func() {
		req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-eu/test", nil), "admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		body = w.Body.String()
	})

	assertNoCredSentinel(t, "the AWS-SM path's response body", body)
	assertNoCredSentinel(t, "the AWS-SM path's captured log output", logs)

	// The step names survive — removing the error value from a log line is only
	// an improvement if the line still says which failure it was.
	if !strings.Contains(logs, `"step":"sts"`) {
		t.Errorf(`the mint-failure log line lost its "step":"sts" field, so it can no longer be told apart from the other GetCredentials failure.

logs:
%s`, logs)
	}
}

// --- 1. the API response and the logs --------------------------------------

// TestCredErrorSentinel_TestClusterResponseAndLogs drives POST
// /clusters/{name}/test, which is the endpoint an operator actually clicks, all
// the way through the real provider and the failing real mint.
func TestCredErrorSentinel_TestClusterResponseAndLogs(t *testing.T) {
	sanityCheckFixture(t)

	srv := newTestServer()
	srv.publishProviders(credProviderWithFailingMint(), nil, nil)
	router := NewRouter(srv, nil)

	var body string
	logs := captureSlog(t, func() {
		req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-eu/test", nil), "admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		body = w.Body.String()
	})

	assertNoCredSentinel(t, "the POST /clusters/{name}/test response body", body)
	assertNoCredSentinel(t, "the captured log output for POST /clusters/{name}/test", logs)

	// And the response still SAYS something. A fix that returned an empty
	// message would pass every sweep above and leave the operator with nothing.
	if !strings.Contains(body, credsafe.Message) {
		t.Errorf(`the response no longer carries the safe sentence, so the operator is told nothing at all.

want it to contain: %s
got body: %s`, credsafe.Message, body)
	}
}

// TestCredErrorSentinel_HandlerErrorSentence checks the sentence itself, at the
// helper every boundary calls — separately from any one endpoint, so a new
// endpoint that uses the helper inherits a checked sentence.
func TestCredErrorSentinel_HandlerErrorSentence(t *testing.T) {
	_, err := credProviderWithFailingMint().GetCredentials("prod-eu")
	if err == nil {
		t.Fatal("expected the mint to fail")
	}
	assertNoCredSentinel(t, "credsafe.Sentence's output", credsafe.Sentence(err))
	if credsafe.Sentence(err) != credsafe.Message {
		t.Errorf("credsafe.Sentence returned %q, want the one fixed sentence %q", credsafe.Sentence(err), credsafe.Message)
	}
}

// TestCredErrorSentinel_SentenceDoesNotVaryWithTheError pins the property that
// makes the sentence safe: it is the SAME for every underlying failure. A
// sentence that changed with the cause would be a channel back to the cause —
// a caller could learn about the credential by watching which sentence came back.
func TestCredErrorSentinel_SentenceDoesNotVaryWithTheError(t *testing.T) {
	causes := []error{
		errors.New("AccessDenied: " + credSentinel),
		errors.New("totally different wording, no sentinel"),
		errors.New(""),
		fmt.Errorf("wrapped: %w", errors.New(credSentinel)),
	}
	var sentences []string
	for _, c := range causes {
		sentences = append(sentences, credsafe.Sentence(credsafe.Mark(c)))
	}
	for i, got := range sentences {
		if got != sentences[0] {
			t.Errorf("sentence %d = %q but sentence 0 = %q — the sentence must not vary with the underlying error's content", i, got, sentences[0])
		}
	}
}

// --- 2. the audit log: stored, listed, and streamed ------------------------

// auditEntryFromFailedCredFetch builds the audit entry a credential-fetch
// failure produces, exactly the way the cluster reconciler does: Cause carries
// the real typed error, and Add is what decides what gets stored.
func auditEntryFromFailedCredFetch(t *testing.T) (*audit.Log, error) {
	t.Helper()
	_, credErr := credProviderWithFailingMint().GetCredentials("prod-eu")
	if credErr == nil {
		t.Fatal("expected the mint to fail")
	}
	log := audit.NewLog(10)
	log.Add(audit.Entry{
		Level:    "error",
		Event:    "cluster_secret_create",
		User:     "sharko",
		Action:   "get_credentials",
		Resource: "cluster:prod-eu",
		Source:   "reconciler",
		Result:   "failure",
		// The unsafe shape on purpose: a caller that passes the raw text in both
		// public fields. Add must fix BOTH, because both are public.
		Error:  credErr.Error(),
		Detail: "credential fetch failed: " + credErr.Error(),
		Cause:  credErr,
	})
	return log, credErr
}

// TestCredErrorSentinel_StoredAuditEntry is THE test that proves the fix is at
// the write side.
//
// It reads the entry back out of the ring with List. If the unsafe text were
// merely hidden by a read handler, it would still be sitting here and this test
// would fail. It also proves the typed cause is gone — using %+v and reflection,
// not JSON, because json:"-" would hide a lingering cause from a JSON check
// while leaving it there for the next person who prints the struct.
func TestCredErrorSentinel_StoredAuditEntry(t *testing.T) {
	sanityCheckFixture(t)
	log, _ := auditEntryFromFailedCredFetch(t)

	stored := log.List(0)
	if len(stored) != 1 {
		t.Fatalf("expected exactly one stored entry, got %d", len(stored))
	}
	e := stored[0]

	assertNoCredSentinel(t, "the STORED audit entry's Error field", e.Error)
	assertNoCredSentinel(t, "the STORED audit entry's Detail field", e.Detail)
	assertNoCredSentinel(t, "the STORED audit entry printed with %+v", fmt.Sprintf("%+v", e))

	if e.Error != credsafe.Message {
		t.Errorf("stored Error = %q, want the fixed safe sentence %q", e.Error, credsafe.Message)
	}
	if e.Detail != credsafe.Message {
		t.Errorf("stored Detail = %q, want the fixed safe sentence %q — Detail is just as public as Error", e.Detail, credsafe.Message)
	}

	// The typed cause must be gone, not just unserialized.
	if e.Cause != nil {
		t.Errorf(`the stored entry still carries a typed cause (%v).

A cause left on a stored entry is a leak waiting for the next person who logs it with %%+v or reflects over it. json:"-" hides it from ONE reader, not from all of them. audit.Add must clear it.`, e.Cause)
	}
	// Reflection, so a renamed or added error-typed field is caught too.
	v := reflect.ValueOf(e)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.Interface && !f.IsNil() {
			if errVal, ok := f.Interface().(error); ok {
				t.Errorf("stored audit entry field %q still holds a live error (%v) — clear it in audit.Add", v.Type().Field(i).Name, errVal)
			}
		}
	}
}

// TestCredErrorSentinel_AuditListEndpoint sweeps the real GET /api/v1/audit
// response — the surface the lowest role (viewer) can read.
func TestCredErrorSentinel_AuditListEndpoint(t *testing.T) {
	sanityCheckFixture(t)
	_, credErr := credProviderWithFailingMint().GetCredentials("prod-eu")

	srv := newTestServer()
	srv.AuditLog().Add(audit.Entry{
		Level: "error", Event: "cluster_secret_create", User: "sharko",
		Action: "get_credentials", Resource: "cluster:prod-eu", Source: "reconciler",
		Result: "failure",
		Error:  credErr.Error(), Detail: credErr.Error(), Cause: credErr,
	})
	router := NewRouter(srv, nil)

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil), "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertNoCredSentinel(t, "the GET /api/v1/audit response body", w.Body.String())
	if !strings.Contains(w.Body.String(), "cluster:prod-eu") {
		t.Errorf("the audit entry is not in the response at all, so this test proved nothing; body = %s", w.Body.String())
	}
}

// TestCredErrorSentinel_AuditStreamEndpoint sweeps GET /api/v1/audit/stream.
//
// This is the surface a read-side fix would have missed: the stream handler
// json.Marshals each entry straight off the subscriber channel. It is covered
// here because the entry that reaches the channel was already sanitized by Add.
func TestCredErrorSentinel_AuditStreamEndpoint(t *testing.T) {
	sanityCheckFixture(t)
	_, credErr := credProviderWithFailingMint().GetCredentials("prod-eu")

	srv := newTestServer()
	router := NewRouter(srv, nil)

	// A cancellable request so the streaming handler returns instead of hanging.
	ctx, cancel := context.WithCancel(context.Background())
	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/audit/stream", nil), "viewer").WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(w, req)
	}()

	// Give the handler a moment to subscribe, then push the entry through.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.AuditLog().Add(audit.Entry{
			Level: "error", Event: "cluster_secret_create", User: "sharko",
			Action: "get_credentials", Resource: "cluster:prod-eu", Source: "reconciler",
			Result: "failure",
			Error:  credErr.Error(), Detail: credErr.Error(), Cause: credErr,
		})
		if strings.Contains(w.Body.String(), "cluster:prod-eu") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	streamed := w.Body.String()
	assertNoCredSentinel(t, "the GET /api/v1/audit/stream output", streamed)
	if !strings.Contains(streamed, "cluster:prod-eu") {
		t.Errorf("nothing reached the stream, so this test proved nothing; body = %q", streamed)
	}
}

// TestCredErrorSentinel_AuditSanitizeIsNotBypassableByAnyField walks every
// string field on audit.Entry and pushes the sentinel through it, so the two
// PUBLIC error-carrying fields are proven sanitized and the others are proven
// to be what they say they are (names, ids, fixed vocabularies).
func TestCredErrorSentinel_AuditSanitizeIsNotBypassableByAnyField(t *testing.T) {
	_, credErr := credProviderWithFailingMint().GetCredentials("prod-eu")

	log := audit.NewLog(10)
	log.Add(audit.Entry{
		Error:  credErr.Error(),
		Detail: credErr.Error(),
		Cause:  credErr,
	})
	stored := log.List(0)[0]

	blob, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshalling the stored entry: %v", err)
	}
	assertNoCredSentinel(t, "the stored audit entry marshalled to JSON", string(blob))
	assertNoCredSentinel(t, "the stored audit entry printed with %+v", fmt.Sprintf("%+v", stored))
}

// --- 3. a git or Kubernetes error that WRAPS a credentials error -----------

// TestCredErrorSentinel_WrappedByAnotherError is the case the classification
// design exists for.
//
// A git or Kubernetes error that wraps a credentials error with %w must still
// produce the safe sentence — because the marker travels with the cause and
// errors.Is finds it through the wrap. A filter that matched on the error's
// WORDS would miss this the moment the wrapper reworded things, which is exactly
// how this bug class comes back.
func TestCredErrorSentinel_WrappedByAnotherError(t *testing.T) {
	_, credErr := credProviderWithFailingMint().GetCredentials("prod-eu")

	// A git-shaped wrapper and a Kubernetes-shaped wrapper, two layers deep.
	wrapped := fmt.Errorf("reading configuration/managed-clusters.yaml from git: %w",
		fmt.Errorf("secrets \"prod-eu\" could not be resolved: %w", credErr))

	if !credsafe.Is(wrapped) {
		t.Fatal(`a wrapper around a credentials error is no longer recognised as one.

The marker must travel through every %w hop, and the classification must find it with errors.Is. If this fails, either a wrapper dropped %w or the marker is not reachable.`)
	}
	assertNoCredSentinel(t, "the wrapped error's safe sentence", credsafe.Sentence(wrapped))

	log := audit.NewLog(10)
	log.Add(audit.Entry{Error: wrapped.Error(), Detail: wrapped.Error(), Cause: wrapped})
	stored := log.List(0)[0]
	assertNoCredSentinel(t, "a stored audit entry whose cause is a git error wrapping a credentials error", fmt.Sprintf("%+v", stored))
}

// TestCredErrorSentinel_UnrelatedErrorsKeepTheirText is the other half of the
// rule, and it is just as important.
//
// A git or Kubernetes error that does NOT wrap a credentials error keeps its
// full text. Blanket-redacting everything would gut the audit trail and the
// operator-facing diagnostics for no gain — those errors are a different risk.
// Without this test, "redact everything" would pass every other test in this
// file.
func TestCredErrorSentinel_UnrelatedErrorsKeepTheirText(t *testing.T) {
	gitErr := errors.New("git: reference not found: refs/heads/main")

	if credsafe.Is(gitErr) {
		t.Fatal("a plain git error must not be classified as a credentials-backend failure")
	}
	if got := credsafe.Sentence(gitErr); got != gitErr.Error() {
		t.Errorf("credsafe.Sentence(gitErr) = %q, want the error's own text %q — unrelated errors must not be redacted", got, gitErr.Error())
	}

	log := audit.NewLog(10)
	log.Add(audit.Entry{Error: gitErr.Error(), Detail: "detail worth keeping", Cause: gitErr})
	stored := log.List(0)[0]
	if stored.Error != gitErr.Error() {
		t.Errorf(`the stored audit entry lost an unrelated git error's text (got %q).

Blanket redaction would make the audit log useless. Only a credentials-backend failure — including one another error wraps — gets the fixed sentence.`, stored.Error)
	}
	if stored.Detail != "detail worth keeping" {
		t.Errorf("the stored entry lost an unrelated Detail (got %q)", stored.Detail)
	}
	// The cause is still cleared, safe or not — a live error on a stored entry
	// is a hazard regardless of what it says.
	if stored.Cause != nil {
		t.Errorf("audit.Add must clear Cause on every entry, safe or not; got %v", stored.Cause)
	}
}

// --- 4. metrics, OpenAPI, Kubernetes events --------------------------------

// TestCredErrorSentinel_MetricsScrape scrapes the real /metrics handler after
// driving the failing path, so a metric LABEL built from an error string would
// be caught. Metric labels are a classic accidental leak: they look like
// telemetry, and they are world-readable on an unauthenticated endpoint.
func TestCredErrorSentinel_MetricsScrape(t *testing.T) {
	sanityCheckFixture(t)

	srv := newTestServer()
	srv.publishProviders(credProviderWithFailingMint(), nil, nil)
	router := NewRouter(srv, nil)

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-eu/test", nil), "admin")
	router.ServeHTTP(httptest.NewRecorder(), req)

	mw := httptest.NewRecorder()
	router.ServeHTTP(mw, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if mw.Code != http.StatusOK {
		t.Fatalf("/metrics returned %d, want 200", mw.Code)
	}
	assertNoCredSentinel(t, "the scraped /metrics output", mw.Body.String())
}

// TestCredErrorSentinel_KubernetesEvents sweeps the events Sharko emits. Events
// are visible cluster-wide to anybody who can read the namespace, so an error
// string in an event message is a leak with a very wide audience.
func TestCredErrorSentinel_KubernetesEvents(t *testing.T) {
	sanityCheckFixture(t)

	fake := record.NewFakeRecorder(64)
	srv := newTestServer()
	srv.publishProviders(credProviderWithFailingMint(), nil, nil)
	srv.SetEventRecorder(events.NewRecorderForTest(fake, "sharko"))
	router := NewRouter(srv, nil)

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-eu/test", nil), "admin")
	router.ServeHTTP(httptest.NewRecorder(), req)

	var emitted []string
	for {
		select {
		case e := <-fake.Events:
			emitted = append(emitted, e)
			continue
		default:
		}
		break
	}
	assertNoCredSentinel(t, "the emitted Kubernetes events", strings.Join(emitted, "\n"))
}

// TestCredErrorSentinel_GeneratedOpenAPI sweeps the committed swagger files.
// A safe sentence added to a swagger @Description is fine; the sentinel showing
// up there would mean a generated example or a doc string captured real error
// text.
func TestCredErrorSentinel_GeneratedOpenAPI(t *testing.T) {
	for _, name := range []string{"docs.go", "swagger.json", "swagger.yaml"} {
		path := filepath.Join("..", "..", "docs", "swagger", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v — the generated OpenAPI files are part of what this hotfix must not leak into", path, err)
		}
		assertNoCredSentinel(t, "the generated OpenAPI file "+name, string(body))
	}
}

// TestCredErrorSentinel_NeverAppearsAnywhereInTheRepoButHere is the guard on the
// sentinel itself: it must be unique, or every sweep above is weaker than it
// looks. If somebody copies the constant into another file, this fails and says
// so.
func TestCredErrorSentinel_NeverAppearsAnywhereInTheRepoButHere(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable paths are not this test's business
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "_dist", ".worktrees", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 4<<20 {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Contains(body, []byte(credSentinel)) {
			return nil
		}
		if filepath.Base(path) == "cred_error_sentinel_test.go" {
			return nil
		}
		offenders = append(offenders, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf(`the sentinel appears in files other than its own test, so a search for it is no longer meaningful:

%s

Pick a fresh unique value rather than reusing this one.`, strings.Join(offenders, "\n"))
	}
}
