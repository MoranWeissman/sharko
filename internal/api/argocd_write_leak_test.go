package api

// argocd_write_leak_test.go — BF6's proof at the boundary a person actually
// sees: the restart-sync endpoint.
//
// # What is being proved
//
// POST /clusters/{name}/addons/{addon}/restart-sync makes two ArgoCD writes —
// a terminate and a sync — and pasted whatever came back into its 502 body:
//
//	"failed to terminate operation: " + err.Error()
//	"failed to sync application: " + err.Error()
//
// The error carried ArgoCD's response payload whole, and ArgoCD quotes the
// repository it was working on inside that payload — token and all. So a
// failing sync handed the repository's access token to any signed-in caller,
// on an ordinary error response the browser renders.
//
// # How it is proved
//
// A sentinel that appears nowhere else in this repository goes inside a
// repository address, in all FOUR shapes a credential is carried in a URL, and
// that address goes inside a realistic ArgoCD error payload. A real HTTP
// server answers the terminate and the sync with it, and the REAL router
// serves the REAL handler. Then three surfaces are swept: the response body,
// every captured log line, and every audit record the request produced.
//
// The sweep machinery is the one init_status_leak_test.go already owns —
// leakFormsFor and findLeakIn — pointed at this file's sentinel, so there is
// one list of shapes rather than two that drift.
//
// # The sweep is proved to work first
//
// TestArgocdWriteLeakSweep_FindsAPlantedSentinel plants every form and demands
// the finder name it, and every case checks the planted payload really carries
// the sentinel before believing any absence.
//
// # Two surfaces are NOT swept here, and here is why
//
// Kubernetes events and notifications. Neither can receive this error: the
// only code that writes Kubernetes events is internal/clusterreconciler, the
// only code that writes notifications is internal/notifications, and neither
// calls an ArgoCD write method. That is not an opinion — it is asserted
// mechanically by TestArgocdWriteCallers_AreExactlyTheAuditedList in
// internal/argocd, which resolves every caller through the type checker and
// fails if either package ever becomes one.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
)

// argoWriteLeakSentinel stands in for the access token inside a repository
// address. It appears nowhere else in this repository.
const argoWriteLeakSentinel = "P8QM-restart-sync-token-sentinel-5x1n7t-never-leaves-the-server-d2a6"

// argoWriteLeakCarriers are the FOUR slots a credential is carried in inside a
// URL. A fix that only covered the password slot would be a fix for one of
// them.
func argoWriteLeakCarriers() []struct{ name, url string } {
	return []struct{ name, url string }{
		{"password slot", "https://x-access-token:" + argoWriteLeakSentinel + "@github.example/sharko-org/addons.git"},
		{"username slot", "https://" + argoWriteLeakSentinel + "@github.example/sharko-org/addons.git"},
		{"query parameter", "https://github.example/sharko-org/addons.git?access_token=" + argoWriteLeakSentinel},
		{"fragment", "https://github.example/sharko-org/addons.git#" + argoWriteLeakSentinel},
	}
}

// argoWriteLeakPayload is what a real ArgoCD answers with when a write fails
// on something to do with a repository.
func argoWriteLeakPayload(repoURL string) string {
	return fmt.Sprintf(
		`{"error":"rpc error: code = Unknown desc = Failed to load target state: `+
			`failed to list refs for %s: authentication required","code":2,`+
			`"message":"repository %s is not accessible"}`,
		repoURL, repoURL)
}

// findArgoWriteLeak reuses the shape list init_status_leak_test.go owns, over
// this file's sentinel.
func findArgoWriteLeak(text, tokenisedURL string) []string {
	return findLeakIn(text, leakFormsFor(argoWriteLeakSentinel, tokenisedURL))
}

func assertNoArgoWriteLeak(t *testing.T, where, text, tokenisedURL string) {
	t.Helper()
	for _, name := range findArgoWriteLeak(text, tokenisedURL) {
		t.Errorf("%s carries %s of the repository token.\n\nthe text was:\n%s", where, name, text)
	}
}

// TestArgocdWriteLeakSweep_FindsAPlantedSentinel is the POSITIVE CONTROL.
// Nothing below may be believed before it passes: every other assertion here
// is an absence, and an absence is what a broken sweep reports too.
func TestArgocdWriteLeakSweep_FindsAPlantedSentinel(t *testing.T) {
	for _, carrier := range argoWriteLeakCarriers() {
		t.Run(carrier.name, func(t *testing.T) {
			forms := leakFormsFor(argoWriteLeakSentinel, carrier.url)
			if len(forms) < 20 {
				t.Fatalf("the sweep only looks for %d forms — it has been hollowed out", len(forms))
			}
			for name, form := range forms {
				planted := "an ordinary looking response body " + form + " and some more text"
				if found := findArgoWriteLeak(planted, carrier.url); len(found) == 0 {
					t.Errorf("the sweep did NOT find a planted %s (%q).\n\nA sweep that cannot find a secret somebody put there proves nothing about the ones it says are absent.", name, form)
				}
			}
			if found := findArgoWriteLeak("Sharko could not start a new sync for this addon.", carrier.url); len(found) != 0 {
				t.Errorf("the sweep fired on clean text, naming %v — every other assertion here would be noise", found)
			}
		})
	}
}

// argocdWriteFailureServer answers GetApplication with a running operation and
// answers BOTH writes with the given status and the tokenised payload.
func argocdWriteFailureServer(t *testing.T, appName, payload string, writeStatus int) (*httptest.Server, *bool, *bool) {
	t.Helper()
	terminated, synced := false, false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/applications/"+appName:
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{
				"metadata":{"name":%q,"namespace":"argocd"},
				"spec":{"project":"default","source":{"repoURL":"https://github.example/sharko-org/addons.git"}},
				"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Degraded"},
					"operationState":{"phase":"Running","startedAt":"2026-06-10T11:50:00Z"}}
			}`, appName)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/applications/"+appName+"/operation":
			terminated = true
			w.WriteHeader(writeStatus)
			_, _ = w.Write([]byte(payload))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applications/"+appName+"/sync":
			synced = true
			w.WriteHeader(writeStatus)
			_, _ = w.Write([]byte(payload))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(payload))
		}
	}))
	return ts, &terminated, &synced
}

// TestRestartSync_NeverHandsOutArgocdsReply drives the real handler for every
// carrier shape and sweeps all three surfaces a person or a collector can read.
func TestRestartSync_NeverHandsOutArgocdsReply(t *testing.T) {
	const appName = "keda-prod-eu"

	for _, carrier := range argoWriteLeakCarriers() {
		payload := argoWriteLeakPayload(carrier.url)
		if !strings.Contains(payload, argoWriteLeakSentinel) {
			t.Fatalf("the simulated ArgoCD payload for the %s does not carry the token — this test would prove nothing", carrier.name)
		}

		for _, writeStatus := range []int{http.StatusInternalServerError, http.StatusConflict, http.StatusBadGateway} {
			t.Run(fmt.Sprintf("%s/%d", carrier.name, writeStatus), func(t *testing.T) {
				ts, terminated, _ := argocdWriteFailureServer(t, appName, payload, writeStatus)
				defer ts.Close()

				srv := newTestServerWithArgocd(t, ts.URL, "test-token")
				router := NewRouter(srv, nil)

				var body string
				var status int
				logs := captureSlog(t, func() {
					req := httptest.NewRequest(http.MethodPost,
						"/api/v1/clusters/prod-eu/addons/keda/restart-sync", nil)
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					status = w.Code
					body = w.Body.String()
				})

				// The request must really have reached the failing write. A
				// 200 here would mean the branch under test never ran and the
				// sweeps below swept nothing.
				if !*terminated {
					t.Fatalf("ArgoCD's terminate endpoint was never called — the branch under test never ran.\n\nstatus %d body %s", status, body)
				}
				if status != http.StatusBadGateway {
					t.Fatalf("expected 502 from a failed ArgoCD write, got %d.\n\nbody: %s", status, body)
				}

				assertNoArgoWriteLeak(t, "the restart-sync response body", body, carrier.url)
				assertNoArgoWriteLeak(t, "the log output for restart-sync", logs, carrier.url)

				entries := srv.AuditLog().List(100)
				if len(entries) == 0 {
					t.Fatal("the request produced no audit record at all, so the audit sweep swept nothing")
				}
				recorded, marshalErr := json.Marshal(entries)
				if marshalErr != nil {
					t.Fatalf("marshalling audit entries: %v", marshalErr)
				}
				assertNoArgoWriteLeak(t, "the audit records for restart-sync", string(recorded), carrier.url)

				// And the replacement is PRESENT. An absence on its own is
				// also what an early return produces.
				const wantAction = "Sharko could not stop the sync that was already running."
				const wantSentence = "ArgoCD refused this change. Sharko does not repeat ArgoCD's own reply here"
				if !strings.Contains(body, wantAction) {
					t.Errorf("the 502 body does not say, in Sharko's own words, what it could not do.\n\nbody: %s", body)
				}
				if !strings.Contains(body, wantSentence) {
					t.Errorf("the 502 body does not carry Sharko's fixed sentence.\n\nbody: %s", body)
				}
				if !strings.Contains(body, fmt.Sprintf("status=%d", writeStatus)) {
					t.Errorf("the 502 body no longer says which status ArgoCD answered with — debugging got worse, which is not an acceptable price.\n\nbody: %s", body)
				}
				if !strings.Contains(body, "/api/v1/applications/"+appName+"/operation") {
					t.Errorf("the 502 body no longer names the call that failed.\n\nbody: %s", body)
				}
				// The retired prefix must not come back, and with it whatever
				// error text followed it.
				if strings.Contains(body, "failed to terminate operation:") ||
					strings.Contains(body, "failed to sync application:") {
					t.Errorf("a retired prefix is back in the body, and with it err.Error().\n\nbody: %s", body)
				}
			})
		}
	}
}

// TestRestartSync_BenignTerminateIsDecidedByType replaces the old
// TestIsBenignTerminateError.
//
// The old test pinned a helper that lowercased the error and searched it for
// "no operation is in progress". Both copies of that helper are gone. What is
// pinned now is the behaviour end to end: a 400 on the terminate is tolerated
// and the sync still fires, and it is tolerated even when ArgoCD's wording has
// nothing in common with the phrase the old matcher was written for.
func TestRestartSync_BenignTerminateIsDecidedByType(t *testing.T) {
	const appName = "keda-prod-eu"

	for _, tc := range []struct {
		name            string
		terminateStatus int
		terminateBody   string
		wantCode        int
		wantSynced      bool
	}{
		{
			name:            "a 400 worded nothing like the old phrase is still tolerated",
			terminateStatus: http.StatusBadRequest,
			terminateBody:   `{"error":"there is currently nothing running that could be cancelled","code":3}`,
			wantCode:        http.StatusOK,
			wantSynced:      true,
		},
		{
			name:            "a 400 in the wording the old matcher was written for",
			terminateStatus: http.StatusBadRequest,
			terminateBody:   `{"error":"Unable to terminate operation. No operation is in progress","code":3}`,
			wantCode:        http.StatusOK,
			wantSynced:      true,
		},
		{
			name:            "a 500 that happens to contain the old phrase is NOT tolerated",
			terminateStatus: http.StatusInternalServerError,
			terminateBody:   `{"error":"internal error while checking whether no operation is in progress","code":2}`,
			wantCode:        http.StatusBadGateway,
			wantSynced:      false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terminated, synced := false, false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/applications/"+appName:
					w.WriteHeader(http.StatusOK)
					_, _ = fmt.Fprintf(w, `{
						"metadata":{"name":%q,"namespace":"argocd"},
						"spec":{"project":"default","source":{"repoURL":"https://github.example/sharko-org/addons.git"}},
						"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Healthy"},
							"operationState":{"phase":"Running","startedAt":"2026-06-10T11:50:00Z"}}
					}`, appName)
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/applications/"+appName+"/operation":
					terminated = true
					w.WriteHeader(tc.terminateStatus)
					_, _ = w.Write([]byte(tc.terminateBody))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applications/"+appName+"/sync":
					synced = true
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()

			srv := newTestServerWithArgocd(t, ts.URL, "test-token")
			router := NewRouter(srv, nil)

			req := httptest.NewRequest(http.MethodPost,
				"/api/v1/clusters/prod-eu/addons/keda/restart-sync", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if !terminated {
				t.Fatal("the terminate was never attempted — this case proves nothing")
			}
			if w.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d.\n\nbody: %s", tc.wantCode, w.Code, w.Body.String())
			}
			if synced != tc.wantSynced {
				t.Errorf("SyncApplication called = %v, want %v", synced, tc.wantSynced)
			}
			// Whichever way it went, ArgoCD's wording is not in the response.
			if strings.Contains(strings.ToLower(w.Body.String()), "no operation is in progress") {
				t.Errorf("ArgoCD's own phrase came back in the response body: %s", w.Body.String())
			}
		})
	}
}

// TestRestartSync_UnreadableApplicationSaysSoInSharkosWords covers the third
// site on this handler: the GetApplication failure, which used to append
// err.Error() to a 404 body.
func TestRestartSync_UnreadableApplicationSaysSoInSharkosWords(t *testing.T) {
	carrier := argoWriteLeakCarriers()[1] // the username slot
	payload := argoWriteLeakPayload(carrier.url)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(payload))
	}))
	defer ts.Close()

	srv := newTestServerWithArgocd(t, ts.URL, "test-token")
	router := NewRouter(srv, nil)

	var body string
	var status int
	logs := captureSlog(t, func() {
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/clusters/prod-eu/addons/keda/restart-sync", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		status = w.Code
		body = w.Body.String()
	})

	if status != http.StatusNotFound {
		t.Fatalf("expected 404 when ArgoCD does not have the application, got %d.\n\nbody: %s", status, body)
	}
	assertNoArgoWriteLeak(t, "the restart-sync 404 body", body, carrier.url)
	assertNoArgoWriteLeak(t, "the log output for the restart-sync 404", logs, carrier.url)

	// Decoded, not matched against the raw JSON: the sentence names the
	// application in quotes, and encoding/json escapes those.
	var decoded struct {
		Error string `json:"error"`
	}
	if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
		t.Fatalf("the 404 body is not JSON: %v\n\nbody: %s", decodeErr, body)
	}
	const want = `Sharko could not read application "keda-prod-eu" from ArgoCD.`
	if !strings.Contains(decoded.Error, want) {
		t.Errorf("the 404 body does not say, in Sharko's own words, what it could not do.\n\nbody: %s", body)
	}
	if strings.Contains(body, "not found in ArgoCD:") {
		t.Errorf("the retired prefix is back, and with it err.Error().\n\nbody: %s", body)
	}
	// The reason has to be somewhere — the log line, found by the request id.
	if !strings.Contains(logs, "restart-sync: could not read the application from ArgoCD") {
		t.Errorf("the failure was not logged at all, so an operator has nothing to go on.\n\nlog output:\n%s", logs)
	}
}

// TestRestartSync_WhenArgocdCannotBeReached_TheAddressIsNotQuoted is the case
// a break test found missing.
//
// Replacing argocd.SafeWriteFailure(err) with err.Error() in the two 502
// bodies left every other test in this file green, because a *WriteRefusedError
// already SAYS the safe sentence — there was nothing left to leak on the path
// those tests drive. The path they did not drive is the other one: ArgoCD
// never answers, so there is no WriteRefusedError at all, and what err.Error()
// carries is the transport's own text, which quotes the address Sharko dialled.
//
// The address here has the token in its USERNAME slot. Go's HTTP client blanks
// a password before it builds *url.Error, so a password-slot fixture would be
// cleaned by the standard library rather than by Sharko and would prove
// nothing. The username survives, which is exactly why it is one of the four
// carriers this file sweeps for.
func TestRestartSync_WhenArgocdCannotBeReached_TheAddressIsNotQuoted(t *testing.T) {
	const appName = "keda-prod-eu"

	// A server that answers the application read normally and then drops the
	// connection on the terminate, so the client gets a transport failure
	// rather than a status code.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/applications/"+appName {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{
				"metadata":{"name":%q,"namespace":"argocd"},
				"spec":{"project":"default","source":{"repoURL":"https://github.example/sharko-org/addons.git"}},
				"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Degraded"},
					"operationState":{"phase":"Running","startedAt":"2026-06-10T11:50:00Z"}}
			}`, appName)
			return
		}
		hijacker, canHijack := w.(http.Hijacker)
		if !canHijack {
			t.Errorf("the test server cannot hijack, so the transport-failure branch cannot be reached")
			return
		}
		conn, _, hijackErr := hijacker.Hijack()
		if hijackErr != nil {
			t.Errorf("hijacking: %v", hijackErr)
			return
		}
		_ = conn.Close()
	}))
	defer ts.Close()

	// The saved ArgoCD address carries the token. That is the value the
	// transport quotes back when the connection dies.
	hostPort := strings.TrimPrefix(ts.URL, "http://")
	tokenisedArgoURL := "http://" + argoWriteLeakSentinel + "@" + hostPort

	srv := newTestServerWithArgocd(t, tokenisedArgoURL, "test-token")
	router := NewRouter(srv, nil)

	var body string
	var status int
	logs := captureSlog(t, func() {
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/clusters/prod-eu/addons/keda/restart-sync", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		status = w.Code
		body = w.Body.String()
	})

	if status != http.StatusBadGateway {
		t.Fatalf("expected 502 when ArgoCD drops the connection, got %d.\n\nbody: %s", status, body)
	}

	assertNoArgoWriteLeak(t, "the restart-sync 502 body for an unreachable ArgoCD", body, "")
	assertNoArgoWriteLeak(t, "the log output for an unreachable ArgoCD", logs, "")

	// And Sharko says, in its own words, that it got no answer.
	const want = "Sharko could not get an answer from ArgoCD"
	if !strings.Contains(body, want) {
		t.Errorf("the 502 body does not say that Sharko never got an answer.\n\nbody: %s", body)
	}
}

// auditEntriesForTest keeps the audit type imported and the intent explicit:
// the sweep above reads whatever the audit log holds, whatever shape it is.
var _ = audit.Entry{}
