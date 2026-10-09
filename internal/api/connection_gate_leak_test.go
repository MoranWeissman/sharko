package api

// connection_gate_leak_test.go — B1's positive-control leak sweep.
//
// # What it proves
//
// A sentinel shaped like a real access token is planted INSIDE a repository
// URL, that URL is saved as the active connection through the real config
// store, and real handlers are driven through the real router. The response
// body and every captured log line are then swept for the sentinel raw, in
// four base64 spellings, in three hashes, in four substrings, as a byte length
// in twelve labelled shapes, and as five kinds of length-revealing mask.
//
// It reuses the sweep B4 built in init_status_leak_test.go rather than writing
// a second one — same package, same sentinel, same forms, same positive
// control (TestInitLeakSweep_FindsAPlantedSentinel, which proves the finder
// finds a planted secret BEFORE anything trusts an absence).
//
// # Why the "did the gate line even run?" check is not optional
//
// Every assertion here is an ABSENCE, and a handler that refused earlier — a
// 400 on a missing field, a 403 on a role — produces the same absence while
// the line under test never runs. B4 hit exactly that: a test passed green
// having never reached the code it was named after. So each row states which
// fixed sentence it expects, and a row whose response does not carry that
// sentence FAILS, loudly, as "this route never reached the gate" rather than
// quietly passing.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// gateLeakRoute is one real endpoint driven end to end.
type gateLeakRoute struct {
	name string
	// method and path are the real registered route.
	method, path string
	body         string
	role         string
	// wantSentence is which half of the connection this route refuses on. A
	// response that does not carry it means the route never reached the gate,
	// and the row is a false negative rather than a pass.
	wantSentence string
	// wantStatus is the code that route already returned before B1. The codes
	// are a contract the UI depends on; B1 changed the words, not the codes.
	wantStatus int
}

// gateLeakRoutes covers both halves of the connection, both status shapes, and
// a spread of files rather than a spread of lines in one file: an addon read, a
// PR list, an upgrade list, an observability read, a cluster history read, a
// cluster discovery, a sync restart, an addon write, an engine pin write and a
// migration preview.
func gateLeakRoutes() []gateLeakRoute {
	const git = credsafe.NoActiveGitConnectionMessage
	const argo = credsafe.NoActiveArgocdConnectionMessage
	return []gateLeakRoute{
		{name: "GET /addons/{name}/changelog (Git, 503)", method: http.MethodGet,
			path: "/api/v1/addons/cert-manager/changelog", role: "admin",
			wantSentence: git, wantStatus: http.StatusServiceUnavailable},
		{name: "GET /prs/merged (Git, 503)", method: http.MethodGet,
			path: "/api/v1/prs/merged", role: "admin",
			wantSentence: git, wantStatus: http.StatusServiceUnavailable},
		{name: "GET /upgrade/{addon}/versions (Git, 503)", method: http.MethodGet,
			path: "/api/v1/upgrade/cert-manager/versions", role: "admin",
			wantSentence: git, wantStatus: http.StatusServiceUnavailable},
		{name: "POST /engine/pin/upgrade (Git, 502)", method: http.MethodPost,
			path: "/api/v1/engine/pin/upgrade", body: `{"dry_run":true}`, role: "admin",
			wantSentence: git, wantStatus: http.StatusBadGateway},
		{name: "POST /migration/preview (Git, 502)", method: http.MethodPost,
			path: "/api/v1/migration/preview", body: `{}`, role: "admin",
			wantSentence: git, wantStatus: http.StatusBadGateway},
		{name: "GET /observability/overview (ArgoCD, 503)", method: http.MethodGet,
			path: "/api/v1/observability/overview", role: "admin",
			wantSentence: argo, wantStatus: http.StatusServiceUnavailable},
		{name: "GET /clusters/{name}/history (ArgoCD, 503)", method: http.MethodGet,
			path: "/api/v1/clusters/prod-eu/history", role: "admin",
			wantSentence: argo, wantStatus: http.StatusServiceUnavailable},
		{name: "POST /clusters/adopt (ArgoCD, 502)", method: http.MethodPost,
			path: "/api/v1/clusters/adopt", body: `{"clusters":["prod-eu"]}`, role: "admin",
			wantSentence: argo, wantStatus: http.StatusBadGateway},
		{name: "POST /clusters/{name}/addons/{addon}/restart-sync (ArgoCD, 502)", method: http.MethodPost,
			path: "/api/v1/clusters/prod-eu/addons/keda/restart-sync", role: "admin",
			wantSentence: argo, wantStatus: http.StatusBadGateway},
		{name: "POST /addons (ArgoCD, 502)", method: http.MethodPost,
			path: "/api/v1/addons", role: "admin",
			body:         `{"name":"fluent-bit","chart":"fluent-bit","repo_url":"https://fluent.github.io/helm-charts","version":"0.43.0"}`,
			wantSentence: argo, wantStatus: http.StatusBadGateway},
		{name: "POST /addons/unwrap-globals (ArgoCD, 502)", method: http.MethodPost,
			path: "/api/v1/addons/unwrap-globals", body: `{}`, role: "admin",
			wantSentence: argo, wantStatus: http.StatusBadGateway},
		{name: "PUT /default-addons (Git, 502)", method: http.MethodPut,
			path: "/api/v1/default-addons", body: `{"addons":["cert-manager"]}`, role: "admin",
			wantSentence: git, wantStatus: http.StatusBadGateway},
	}
}

// TestConnectionGateLeak_NoRouteEverShowsTheRepositoryToken is the sweep.
//
// The active connection holds a repository URL net/url refuses — the exact
// production shape: the parse error quotes the whole string, token and all,
// and that error is wrapped twice on the way out of GetActiveGitProvider.
// Every route below is then driven for real.
func TestConnectionGateLeak_NoRouteEverShowsTheRepositoryToken(t *testing.T) {
	// The fixture must genuinely carry the token, or every absence below is
	// an absence of nothing.
	probe := serverWithUnreadableRepoURL(t, initLeakUnparseableRepoURL, "")
	buildErr := func() error {
		_, err := probe.connSvc.GetActiveGitProvider()
		return err
	}()
	if buildErr == nil {
		t.Fatal("the fixture must FAIL to build a Git provider — there is nothing to prove otherwise")
	}
	if !strings.Contains(buildErr.Error(), initLeakSentinel) {
		t.Fatalf(`the underlying error does NOT carry the token, so this whole file proves nothing.

got: %v`, buildErr)
	}

	for _, route := range gateLeakRoutes() {
		t.Run(route.name, func(t *testing.T) {
			srv := serverWithUnreadableRepoURL(t, initLeakUnparseableRepoURL, "")
			router := NewRouter(srv, nil)

			var body string
			var status int
			logs := captureSlog(t, func() {
				var req *http.Request
				if route.body == "" {
					req = httptest.NewRequest(route.method, route.path, nil)
				} else {
					req = httptest.NewRequest(route.method, route.path, bytes.NewReader([]byte(route.body)))
					req.Header.Set("Content-Type", "application/json")
				}
				req = withRole(req, route.role)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				body = w.Body.String()
				status = w.Code
			})

			// 1. The route actually reached the gate. Without this, a 400 or a
			//    403 would sail through every assertion below.
			if !strings.Contains(body, route.wantSentence) {
				t.Fatalf(`this route never reached the connection gate, so it proves nothing.

status: %d
body:   %s

wanted the body to carry: %q`, status, body, route.wantSentence)
			}

			// 2. Nothing about the token, in any shape, anywhere.
			assertNoInitLeak(t, route.name+" response body", body)
			assertNoInitLeak(t, route.name+" log output", logs)

			// 3. No fragment of the underlying failure either. Dropping the
			//    token but keeping "gitea provider:" would be a half-fix that
			//    still tells a caller how Sharko builds its clients.
			for _, fragment := range []string{"gitea provider:", "parse repo_url", "net/url", "invalid control character"} {
				if strings.Contains(body, fragment) {
					t.Errorf("the body still carries a fragment of the underlying failure (%q): %s", fragment, body)
				}
				if strings.Contains(logs, fragment) {
					t.Errorf("the log still carries a fragment of the underlying failure (%q): %s", fragment, logs)
				}
			}

			// 4. The retired prefixes are gone. Keeping the prefix and
			//    dropping only the error would be a change nobody could see.
			lowered := strings.ToLower(body + "\n" + logs)
			for _, retired := range []string{"no active git connection:", "no active argocd connection:"} {
				if strings.Contains(lowered, retired) {
					t.Errorf("the retired prefix %q is back", retired)
				}
			}

			// 5. The status code the UI already depends on is unchanged.
			if status != route.wantStatus {
				t.Errorf("status = %d, want %d — B1 changes what Sharko says, not which code it says it under", status, route.wantStatus)
			}

			// 6. It answered with the right HALF. A Git failure answered with
			//    the ArgoCD sentence would send an operator to the wrong field.
			other := credsafe.NoActiveArgocdConnectionMessage
			if route.wantSentence == other {
				other = credsafe.NoActiveGitConnectionMessage
			}
			if strings.Contains(body, other) {
				t.Errorf("the response carries BOTH halves' sentences — one of them is wrong:\n%s", body)
			}
		})
	}
}

// TestConnectionGateLeak_ReturnedErrorCarriesNoCause covers the sites that
// RETURN the failure instead of writing it. Their value used to be an
// fmt.Errorf(..., %w) wrap, so one more fmt.Errorf anywhere downstream put the
// token back on the wire.
//
// The property asserted is stronger than "the text is clean": there is nothing
// UNDER these errors at all, so no future wrap can reach a cause that does not
// exist.
func TestConnectionGateLeak_ReturnedErrorCarriesNoCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"the Git half", credsafe.ErrNoActiveGitConnection, credsafe.NoActiveGitConnectionMessage},
		{"the ArgoCD half", credsafe.ErrNoActiveArgocdConnection, credsafe.NoActiveArgocdConnectionMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Error() != tc.want {
				t.Errorf("Error() = %q, want the fixed sentence %q", tc.err.Error(), tc.want)
			}
			if u, ok := tc.err.(interface{ Unwrap() error }); ok && u.Unwrap() != nil {
				t.Errorf("the error has something underneath it (%v) — one fmt.Errorf downstream and the cause is back on the wire", u.Unwrap())
			}
			if u, ok := tc.err.(interface{ Unwrap() []error }); ok && len(u.Unwrap()) > 0 {
				t.Error("the error has causes underneath it — one fmt.Errorf downstream and they are back on the wire")
			}
		})
	}
}

// TestConnectionGateLeak_EnginePinLiveReturnsTheFixedSentence drives the one
// production path that returns rather than writes: CheckEnginePinLive, whose
// value handleCheckEnginePin hands to writeError as err.Error(). Before B1 it
// wrapped the connection error with %w, so the token came out of a GET.
func TestConnectionGateLeak_EnginePinLiveReturnsTheFixedSentence(t *testing.T) {
	srv := serverWithUnreadableRepoURL(t, initLeakUnparseableRepoURL, "")

	var err error
	logs := captureSlog(t, func() {
		_, err = srv.CheckEnginePinLive(t.Context())
	})
	if err == nil {
		t.Fatal("CheckEnginePinLive must fail with an unreadable repository URL — there is nothing to prove otherwise")
	}
	if err.Error() != credsafe.NoActiveGitConnectionMessage {
		t.Errorf("CheckEnginePinLive returned\n  %q\nwant exactly\n  %q", err.Error(), credsafe.NoActiveGitConnectionMessage)
	}
	assertNoInitLeak(t, "the error CheckEnginePinLive returned", err.Error())
	assertNoInitLeak(t, "the log output while CheckEnginePinLive ran", logs)

	// And wrapping it the way a careless caller would still says nothing.
	wrapped := "reading the engine pin: " + err.Error()
	assertNoInitLeak(t, "a caller's own wrap of that error", wrapped)
}
