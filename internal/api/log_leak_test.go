package api

// log_leak_test.go — the proof for B9.
//
// # What is being proved
//
// Five stories closed this leak on response bodies. This one is about the LOG.
// A log feels less exposed than an API reply and is not: it is shipped to
// whatever collector the operator runs, kept for months, and read by more
// people than the API.
//
// Two lines carried it, and between them they covered the whole application:
//
//   - internal/api/router.go writeServerError — `slog.Error("server error",
//     ..., "error", err)`. Its response body has been sanitised for a long
//     time and seventeen handlers rely on that. Its LOG line carried the raw
//     error for every error class in the codebase. It is the shared sink.
//
//   - internal/argocd/client.go doGet — `slog.Error("argocd call failed",
//     ..., "body", string(body))`. The whole ArgoCD response body, verbatim.
//
// The carrier is the one every story this round has been about. A Git
// repository address is commonly written with the password inside it —
//
//	https://x-access-token:<token>@host/org/repo.git
//
// — and providers quote the address they failed on inside their error text
// AND inside their HTTP response bodies. net/url's own parse error quotes in
// full the string it refused.
//
// # How it is proved
//
// The sweep machinery from init_status_leak_test.go is reused, not rewritten:
// the same sentinel, the same tokenised URL, the same initLeakForms /
// findInitLeak / assertNoInitLeak, and the same positive control
// (TestInitLeakSweep_FindsAPlantedSentinel) that proves the finder can find a
// planted secret before any absence here is believed.
//
// The token is planted in the two real carriers — a raw provider error and a
// raw HTTP response body — and each is pushed down its REAL production path
// through the REAL router or the REAL ArgoCD client. Then the captured log is
// swept.
//
// captureSlog now builds the SAME handler chain `sharko serve` installs
// (logging.NewHandler). Before B9 it built a bare JSON handler with no
// redaction in it, so every "the log must not carry this" assertion in this
// package was being made against a pipeline nobody runs.
//
// # And the safe replacement must be PRESENT
//
// Every test here asserts the absence of the token AND the presence of the
// classification that replaced it. An absence on its own is also what a
// short-circuit produces: a handler that returned early, a fixture that never
// failed, a log line that was never emitted. The presence assertion is what
// turns "found nothing" into "reached the code and it did the right thing".

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// --- 1. the shared sink: writeServerError's log line ------------------------

// TestLogLeak_WriteServerError_NeverLogsTheRepositoryToken drives the real GET
// /api/v1/clusters against a connection whose repository URL net/url refuses.
//
// That handler is one of the seventeen that answer a failed connection lookup
// through writeServerError rather than through the connection gate — see
// connection_gate_guard_test.go, where it is listed `notRouted:
// writeServerError`. Its 502 body was already safe. Its log line was not.
func TestLogLeak_WriteServerError_NeverLogsTheRepositoryToken(t *testing.T) {
	srv := serverWithUnreadableRepoURL(t, initLeakUnparseableRepoURL, "an-argocd-token")

	// The fixture must really fail, and the underlying error must really
	// carry the token. Without this half every assertion below would pass
	// while proving nothing.
	_, buildErr := srv.connSvc.GetActiveGitProvider()
	if buildErr == nil {
		t.Fatal("the fixture must FAIL to build a Git provider — there is nothing to prove otherwise")
	}
	if !strings.Contains(buildErr.Error(), initLeakSentinel) {
		t.Fatalf(`the underlying error does NOT carry the token, so this test proves nothing.

got: %v`, buildErr)
	}

	router := NewRouter(srv, nil)
	var body string
	var status int
	logs := captureSlog(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		status = w.Code
		body = w.Body.String()
	})

	// The request must actually have failed on the connection lookup. A 200
	// here would mean the handler never reached writeServerError and this
	// test is green for the wrong reason.
	if status < 500 {
		t.Fatalf("GET /api/v1/clusters returned %d, so it never reached writeServerError — this test proves nothing about that line.\n\nbody: %s", status, body)
	}

	assertNoInitLeak(t, "the GET /api/v1/clusters response body", body)
	assertNoInitLeak(t, "the log output for GET /api/v1/clusters", logs)

	// The log line must still be there, and it must still say what failed.
	// A fix that stopped logging altogether would pass every sweep above and
	// leave an operator with nothing.
	if !strings.Contains(logs, `"msg":"server error"`) {
		t.Fatalf(`the "server error" log line was never emitted, so the sweep above swept nothing.

log output:
%s`, logs)
	}
	for _, want := range []string{
		`"op":"get_active_git_provider"`, // which operation failed
		`"status":503`,                   // and how it was answered
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log line no longer carries %s — debugging got worse, which is not an acceptable price for the fix.\n\nlog output:\n%s", want, logs)
		}
	}

	// And Sharko's own classification IS present where the error's words used
	// to be. This is the short-circuit guard: if the record never reached the
	// sink, this fails loudly instead of passing quietly.
	if !strings.Contains(logs, `"error":"`) {
		t.Fatalf("the log line has no error field at all — the record did not reach the sink, so nothing here was tested.\n\nlog output:\n%s", logs)
	}
	if !strings.Contains(logs, "chain=") {
		t.Errorf(`the log line's error field carries no type chain, so credsafe.LogClass never ran on it.

log output:
%s`, logs)
	}
}

// TestLogLeak_WriteServerErrorLogsAClassification pins the exact replacement
// as a literal typed out here, not as the constant the code assigned.
//
// The error is built the way production builds it: url.Parse refusing a
// repository URL with a token inside it. That is a real *url.Error carrying
// the real string.
func TestLogLeak_WriteServerErrorLogsAClassification(t *testing.T) {
	_, parseErr := url.Parse(initLeakUnparseableRepoURL)
	if parseErr == nil {
		t.Fatal("url.Parse accepted the fixture URL — the whole premise of this test is that it refuses it")
	}
	if !strings.Contains(parseErr.Error(), initLeakSentinel) {
		t.Fatalf("net/url's error does not quote the URL it refused, so there is no leak to prove: %v", parseErr)
	}
	wrapped := fmt.Errorf("building the gitea provider: %w", parseErr)

	logs := captureSlog(t, func() {
		w := httptest.NewRecorder()
		writeServerError(w, http.StatusBadGateway, "list_clusters", wrapped)
	})

	assertNoInitLeak(t, "the writeServerError log line", logs)

	// The literal is typed out here rather than compared against
	// credsafe.LogClass(wrapped): comparing a function's output with the same
	// function's output cannot fail, so it would pin nothing.
	const wantClass = `"error":"unclassified chain=*url.Error>*errors.errorString"`
	if !strings.Contains(logs, wantClass) {
		t.Errorf("the log line does not carry\n  %s\n\nlog output:\n%s", wantClass, logs)
	}
}

// --- 2. the raw HTTP response body ------------------------------------------

// TestLogLeak_ArgocdResponseBody_NeverReachesTheLog stands up a real HTTP
// server that answers like a broken ArgoCD — a 500 whose body quotes a
// repository URL with the token inside it, which is exactly what ArgoCD does
// when a repo it is syncing cannot be reached — and drives the REAL
// argocd.Client against it.
func TestLogLeak_ArgocdResponseBody_NeverReachesTheLog(t *testing.T) {
	responseBody := fmt.Sprintf(
		`{"error":"rpc error: code = Unknown desc = failed to list refs for %s: authentication required","code":2}`,
		initLeakRepoURL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer upstream.Close()

	// Sanity: the body really does carry the token. Without this the sweep
	// below would be searching for something that was never in play.
	if !strings.Contains(responseBody, initLeakSentinel) {
		t.Fatal("the planted response body does not carry the token — this test proves nothing")
	}

	client := argocd.NewClient(upstream.URL, "an-argocd-token", false)

	var callErr error
	logs := captureSlog(t, func() {
		_, callErr = client.ListApplications(context.Background())
	})

	if callErr == nil {
		t.Fatal("the ArgoCD call succeeded against a server that returns 500 — the failing branch under test never ran")
	}

	assertNoInitLeak(t, "the log output for a failed ArgoCD call", logs)
	assertNoInitLeak(t, "the error the ArgoCD client returned", callErr.Error())

	// The line must still be emitted, and must still say which call failed
	// and how. Removing the body must not remove the triage.
	if !strings.Contains(logs, `"msg":"argocd call failed"`) {
		t.Fatalf(`the "argocd call failed" log line was never emitted, so the sweep above swept nothing.

log output:
%s`, logs)
	}
	if !strings.Contains(logs, `"status":500`) {
		t.Errorf("the log line no longer carries the status code — debugging got worse.\n\nlog output:\n%s", logs)
	}
	if !strings.Contains(logs, `"endpoint":"/api/v1/applications`) {
		t.Errorf("the log line no longer names the ArgoCD endpoint — an operator cannot tell which call failed.\n\nlog output:\n%s", logs)
	}
	// And the body field is gone entirely, not merely shortened.
	if strings.Contains(logs, `"body":`) {
		t.Errorf("the log line still has a body field. Truncating or masking a response body is not a fix — the token can be anywhere in it.\n\nlog output:\n%s", logs)
	}
}

// TestLogLeak_ArgocdTransportError_IsClassifiedNotQuoted covers the other
// ArgoCD line: the one that fires when the request never got an answer at all.
// It hands slog the transport error itself, so the sink is what protects it.
func TestLogLeak_ArgocdTransportError_IsClassifiedNotQuoted(t *testing.T) {
	// A URL with the token inside it and a host nothing can dial. The
	// transport's error quotes the URL it tried, token and all.
	client := argocd.NewClient(
		"https://x-access-token:"+initLeakSentinel+"@127.0.0.1:1", "a-token", false)

	var callErr error
	logs := captureSlog(t, func() {
		_, callErr = client.ListApplications(context.Background())
	})
	if callErr == nil {
		t.Fatal("the dial to a closed port succeeded — the transport-failure branch never ran")
	}

	assertNoInitLeak(t, "the log output for a failed ArgoCD dial", logs)

	if !strings.Contains(logs, `"msg":"argocd call got no answer"`) {
		t.Fatalf("the log line was never emitted, so the sweep swept nothing.\n\nlog output:\n%s", logs)
	}
	if !strings.Contains(logs, "chain=") {
		t.Errorf(`the log line's error field carries no type chain, so credsafe.LogClass never ran on it.

log output:
%s`, logs)
	}
}

// --- 3. LogClass itself -----------------------------------------------------

// TestLogClass_NeverRepeatsTheErrorsWords covers the classifier directly,
// including the shape that matters most: an error whose words ARE the token.
func TestLogClass_NeverRepeatsTheErrorsWords(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a plain error carrying the token",
			err:  fmt.Errorf("failed to clone %s", initLeakRepoURL),
			want: "unclassified chain=*errors.errorString",
		},
		{
			name: "wrapped twice, as a provider wraps it",
			err: fmt.Errorf("gitea provider: %w",
				fmt.Errorf("parsing repo url: %w", fmt.Errorf("bad url %s", initLeakRepoURL))),
			want: "unclassified chain=*errors.errorString",
		},
		{
			name: "a context deadline",
			err:  fmt.Errorf("calling argocd: %w", context.DeadlineExceeded),
			want: "deadline-exceeded timeout chain=context.deadlineExceededError",
		},
		{
			name: "nothing at all",
			err:  nil,
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := credsafe.LogClass(tc.err)
			if got != tc.want {
				t.Errorf("LogClass = %q, want exactly %q", got, tc.want)
			}
			if strings.Contains(got, initLeakSentinel) {
				t.Errorf("LogClass repeated the token: %q", got)
			}
			for _, form := range findInitLeak(got) {
				t.Errorf("LogClass output carries %s of the token: %q", form, got)
			}
		})
	}
}

// TestLogClass_SaysWhatItRecognises is the honesty half. Making the log safe
// must not make it useless: a classification that said "unclassified" for
// everything would pass every sweep in this file and help nobody.
func TestLogClass_SaysWhatItRecognises(t *testing.T) {
	got := credsafe.LogClass(fmt.Errorf("reading the secret: %w", credsafe.ErrNotFound))
	if !strings.Contains(got, "not-found") {
		t.Errorf("LogClass = %q — a missing credential must still be recognisable in the log", got)
	}
	// And a MARKED credentials-backend error says so as well as saying what
	// kind. The two facts are independent: a bare ErrNotFound is not marked,
	// which is why the assertion above does not also demand the mark.
	marked := credsafe.LogClass(credsafe.Mark(fmt.Errorf("etcdserver: request failed while resolving %s", initLeakSentinel)))
	if !strings.Contains(marked, "credentials-backend") {
		t.Errorf("LogClass = %q — a credentials-backend failure must still say so", marked)
	}
	for _, form := range findInitLeak(marked) {
		t.Errorf("LogClass output for a marked credentials error carries %s of the token: %q", form, marked)
	}

	// Two different failures must not collapse into one answer.
	deadline := credsafe.LogClass(fmt.Errorf("x: %w", context.DeadlineExceeded))
	canceled := credsafe.LogClass(fmt.Errorf("x: %w", context.Canceled))
	if deadline == canceled {
		t.Errorf("a timeout and a cancellation both classify as %q — these need different fixes and must stay different answers", deadline)
	}
}
