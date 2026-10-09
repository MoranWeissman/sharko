package argocd

// write_refused_leak_test.go — the proof for BF6.
//
// # What is being proved
//
// Every write Sharko makes to ArgoCD — a sync, a terminate, a cluster
// registration, a label update, a project, an application, a repository —
// used to turn a non-2xx answer into this error:
//
//	unexpected status %d from %s %s: %s
//
// where the last %s was ArgoCD's response payload, copied in whole. ArgoCD
// quotes whatever it was working on inside that payload, and for a repository
// that is the address with the access token inside it. The read path dropped
// the payload and wrote down why; the write path kept it, and the error
// travelled from there into 502 bodies a browser renders.
//
// # How it is proved
//
// A sentinel that appears nowhere else in this repository is planted inside a
// repository address, in ALL FOUR shapes a credential is carried in a URL —
// the password slot, the username slot, a query parameter and the fragment.
// Each of those goes inside a realistic ArgoCD error payload, and a real HTTP
// server answers every write method with it. Then the error the real client
// hands back is swept, in every spelling a value escapes in: raw, four base64
// forms, three hashes, several substrings, and its length in a dozen labelled
// shapes.
//
// # The sweep is proved to work FIRST
//
// Every assertion here is an ABSENCE, and an absence is also what a broken
// sweep reports. TestWriteRefusedSweep_FindsAPlantedSentinel plants each form
// and requires the finder to name it, and each method's test asserts the
// planted payload really carries the sentinel before believing anything.
//
// # And the diagnostics must still be PRESENT
//
// An error that says nothing would pass every sweep and leave an operator with
// no way to tell which call failed. So each test also demands the verb, the
// endpoint, the status code and the stable code.

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// writeLeakSentinel stands in for the access token inside a repository
// address. It appears nowhere else in this repository, so finding it anywhere
// is proof of a leak rather than a coincidence.
const writeLeakSentinel = "R4TZ-argocd-write-token-sentinel-8k2v6q-never-leaves-the-server-c1f7"

// writeLeakCarriers are the FOUR places a credential is carried inside a URL.
// All four are real shapes an operator's repository address is written in, and
// a fix that only covered the password slot would be a fix for one of them.
func writeLeakCarriers() []struct{ name, url string } {
	return []struct{ name, url string }{
		{"password slot", "https://x-access-token:" + writeLeakSentinel + "@github.example/sharko-org/addons.git"},
		{"username slot", "https://" + writeLeakSentinel + "@github.example/sharko-org/addons.git"},
		{"query parameter", "https://github.example/sharko-org/addons.git?access_token=" + writeLeakSentinel},
		{"fragment", "https://github.example/sharko-org/addons.git#" + writeLeakSentinel},
	}
}

// argocdErrorPayload is what a real ArgoCD answers with when a write fails on
// something to do with a repository: a gRPC-gateway error envelope quoting, in
// full, the address it was working on.
func argocdErrorPayload(repoURL string) string {
	return fmt.Sprintf(
		`{"error":"rpc error: code = Unknown desc = Failed to load target state: failed to generate manifest for source 1 of 1: `+
			`rpc error: code = Unknown desc = failed to list refs for %s: authentication required: `+
			`Get \"%s/info/refs?service=git-upload-pack\": authentication required","code":2,`+
			`"message":"Failed to load target state: repository %s is not accessible"}`,
		repoURL, repoURL, repoURL)
}

// --- the sweep -------------------------------------------------------------

// writeLeakForms is every shape the sentinel could come out wearing. Each one
// is a way a value has genuinely escaped from software before: re-encoded,
// hashed "for safety", truncated to a "harmless" fragment, or reported as a
// length or a mask whose width is the length.
func writeLeakForms(tokenisedURL string) map[string]string {
	s := writeLeakSentinel
	n := len(s)
	sha256Sum := sha256.Sum256([]byte(s))
	sha1Sum := sha1.Sum([]byte(s))
	md5Sum := md5.Sum([]byte(s))

	forms := map[string]string{
		"the token itself":     s,
		"base64 (std)":         base64.StdEncoding.EncodeToString([]byte(s)),
		"base64 (raw std)":     base64.RawStdEncoding.EncodeToString([]byte(s)),
		"base64 (url)":         base64.URLEncoding.EncodeToString([]byte(s)),
		"base64 (raw url)":     base64.RawURLEncoding.EncodeToString([]byte(s)),
		"SHA-256 hex":          hex.EncodeToString(sha256Sum[:]),
		"SHA-256 base64":       base64.StdEncoding.EncodeToString(sha256Sum[:]),
		"SHA-1 hex":            hex.EncodeToString(sha1Sum[:]),
		"MD5 hex":              hex.EncodeToString(md5Sum[:]),
		"first 12 characters":  s[:12],
		"first 24 characters":  s[:24],
		"last 12 characters":   s[n-12:],
		"middle 16 characters": s[n/2-8 : n/2+8],
	}
	if tokenisedURL != "" {
		forms["the whole tokenised URL"] = tokenisedURL
	}
	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n),
		fmt.Sprintf(`"length": %d`, n),
		fmt.Sprintf(`"len":%d`, n),
		fmt.Sprintf(`"len": %d`, n),
		fmt.Sprintf(`"size":%d`, n),
		fmt.Sprintf(`"size": %d`, n),
		fmt.Sprintf("length=%d", n),
		fmt.Sprintf("len=%d", n),
		fmt.Sprintf("size=%d", n),
	} {
		forms["its byte length, written "+shape] = shape
	}
	for _, ch := range []string{"*", "•", "x", "●", "#"} {
		for _, l := range []int{n - 1, n, n + 1} {
			forms[fmt.Sprintf("a mask of %d %q", l, ch)] = strings.Repeat(ch, l)
		}
	}
	return forms
}

// findWriteLeak returns the names of every form of the sentinel present in
// text. Split out from the assertion so the positive control can prove the
// finder actually finds things.
func findWriteLeak(text, tokenisedURL string) []string {
	var found []string
	for name, form := range writeLeakForms(tokenisedURL) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// assertNoWriteLeak fails, naming each form, when text carries the sentinel.
func assertNoWriteLeak(t *testing.T, where, text, tokenisedURL string) {
	t.Helper()
	for _, name := range findWriteLeak(text, tokenisedURL) {
		t.Errorf("%s carries %s of the repository token.\n\nthe text was:\n%s", where, name, text)
	}
}

// TestWriteRefusedSweep_FindsAPlantedSentinel is the POSITIVE CONTROL, and
// nothing in this file may be believed before it passes.
//
// Every other assertion here is an absence, and an absence is exactly what a
// sweep looking in the wrong place also reports. So each form is planted in
// turn and the finder is required to name it.
func TestWriteRefusedSweep_FindsAPlantedSentinel(t *testing.T) {
	for _, carrier := range writeLeakCarriers() {
		t.Run(carrier.name, func(t *testing.T) {
			forms := writeLeakForms(carrier.url)
			if len(forms) < 20 {
				t.Fatalf("the sweep only looks for %d forms — it has been hollowed out", len(forms))
			}
			for name, form := range forms {
				planted := "an ordinary looking error string " + form + " and some more text"
				if found := findWriteLeak(planted, carrier.url); len(found) == 0 {
					t.Errorf("the sweep did NOT find a planted %s (%q).\n\nA sweep that cannot find a secret somebody put there proves nothing about the ones it says are absent.", name, form)
				}
			}
			// And it stays silent on text that is genuinely clean, so a green
			// run elsewhere is a real result rather than a sweep that fires on
			// everything.
			clean := credsafe.ArgocdWriteRefusedMessage
			if found := findWriteLeak(clean, carrier.url); len(found) != 0 {
				t.Errorf("the sweep fired on Sharko's own safe sentence, naming %v — every other assertion in this file would be noise", found)
			}
		})
	}
}

// TestWriteLeakPayload_ReallyCarriesTheToken is the second half of the
// control: the fixture itself. If the payload builder ever stopped embedding
// the sentinel, every method test below would sweep for something that was
// never in play and pass.
func TestWriteLeakPayload_ReallyCarriesTheToken(t *testing.T) {
	for _, carrier := range writeLeakCarriers() {
		payload := argocdErrorPayload(carrier.url)
		if !strings.Contains(payload, writeLeakSentinel) {
			t.Fatalf("the simulated ArgoCD payload for the %s does not carry the token — every test in this file would prove nothing", carrier.name)
		}
		if !strings.Contains(payload, carrier.url) {
			t.Fatalf("the simulated ArgoCD payload for the %s does not carry the whole tokenised address", carrier.name)
		}
	}
}

// --- every write method, every carrier, every status ------------------------

// writeMethodCall is one of the nine write methods, named and callable. It is
// a LIST rather than a count: a method that stops being covered has to go
// missing from here by name, and a new one that is not added shows up in
// TestWriteMethods_EveryExportedWriteIsCovered.
type writeMethodCall struct {
	name string
	call func(c *Client) error
}

func writeMethodCalls() []writeMethodCall {
	return []writeMethodCall{
		{"TerminateOperation", func(c *Client) error {
			return c.TerminateOperation(context.Background(), "keda-prod-eu")
		}},
		{"SyncApplication", func(c *Client) error {
			return c.SyncApplication(context.Background(), "keda-prod-eu")
		}},
		{"RegisterCluster", func(c *Client) error {
			return c.RegisterCluster(context.Background(), "prod-eu", "https://k8s.example", []byte("ca"), "tok", map[string]string{"keda": "true"})
		}},
		{"DeleteCluster", func(c *Client) error {
			return c.DeleteCluster(context.Background(), "https://k8s.example")
		}},
		{"UpdateClusterLabels", func(c *Client) error {
			return c.UpdateClusterLabels(context.Background(), "https://k8s.example", map[string]string{"keda": "true"})
		}},
		{"CreateProject", func(c *Client) error {
			return c.CreateProject(context.Background(), []byte(`{"metadata":{"name":"sharko-addons"}}`))
		}},
		{"CreateApplication", func(c *Client) error {
			return c.CreateApplication(context.Background(), []byte(`{"metadata":{"name":"sharko-engine"}}`))
		}},
		{"AddRepository", func(c *Client) error {
			return c.AddRepository(context.Background(), "https://github.example/sharko-org/addons.git", "x-access-token", "a-git-token")
		}},
		{"RefreshApplication", func(c *Client) error {
			_, err := c.RefreshApplication(context.Background(), "keda-prod-eu", false)
			return err
		}},
	}
}

// TestWriteMethods_NeverReturnArgocdsOwnReply is the whole story: every write
// method, against a server that answers every request with a tokenised ArgoCD
// error payload, at every status class that matters.
func TestWriteMethods_NeverReturnArgocdsOwnReply(t *testing.T) {
	statuses := []int{
		http.StatusBadRequest,          // 400 — the terminate race
		http.StatusUnauthorized,        // 401 — sentinel branch
		http.StatusForbidden,           // 403 — sentinel branch
		http.StatusNotFound,            // 404
		http.StatusConflict,            // 409 — an ordinary 4xx
		http.StatusInternalServerError, // 500 — the branch that used to leak
		http.StatusBadGateway,          // 502
	}

	for _, carrier := range writeLeakCarriers() {
		payload := argocdErrorPayload(carrier.url)
		for _, status := range statuses {
			for _, m := range writeMethodCalls() {
				t.Run(fmt.Sprintf("%s/%s/%d", m.name, carrier.name, status), func(t *testing.T) {
					ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(status)
						_, _ = w.Write([]byte(payload))
					}))
					defer ts.Close()

					err := m.call(NewClient(ts.URL, "an-argocd-token", false))
					if err == nil {
						t.Fatalf("%s succeeded against a server answering %d — the failing branch never ran", m.name, status)
					}

					// The error's own words, and every ordinary way somebody
					// prints an error. All four have to be clean, because all
					// four are one fmt.Sprintf away from a response body.
					assertNoWriteLeak(t, m.name+" error text", err.Error(), carrier.url)
					assertNoWriteLeak(t, m.name+" printed with %v", fmt.Sprintf("%v", err), carrier.url)
					assertNoWriteLeak(t, m.name+" printed with %+v", fmt.Sprintf("%+v", err), carrier.url)
					assertNoWriteLeak(t, m.name+" printed with %s", fmt.Sprintf("%s", err), carrier.url)

					// The log classifier is what the log sink puts in place of
					// the error's words, so it is on the same footing.
					assertNoWriteLeak(t, m.name+" credsafe.LogClass", credsafe.LogClass(err), carrier.url)
				})
			}
		}
	}
}

// TestWriteRefused_StillSaysWhichCallFailedAndHow is the other half of the
// trade. Dropping ArgoCD's reply is only acceptable if an operator can still
// tell which call was refused and how, so this demands every one of Sharko's
// own facts by name.
func TestWriteRefused_StillSaysWhichCallFailedAndHow(t *testing.T) {
	for _, tc := range []struct {
		status   int
		wantCode WriteRefusalCode
		wantVerb string
	}{
		{http.StatusBadRequest, WriteRefusalRejected, http.MethodPost},
		{http.StatusUnauthorized, WriteRefusalTokenInvalid, http.MethodPost},
		{http.StatusForbidden, WriteRefusalPermissionDenied, http.MethodPost},
		{http.StatusNotFound, WriteRefusalNotFound, http.MethodPost},
		{http.StatusConflict, WriteRefusalRejected, http.MethodPost},
		{http.StatusInternalServerError, WriteRefusalUpstreamFailure, http.MethodPost},
		{http.StatusServiceUnavailable, WriteRefusalUpstreamFailure, http.MethodPost},
	} {
		t.Run(fmt.Sprintf("%d", tc.status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(argocdErrorPayload(writeLeakCarriers()[0].url)))
			}))
			defer ts.Close()

			err := NewClient(ts.URL, "tok", false).SyncApplication(context.Background(), "keda-prod-eu")
			if err == nil {
				t.Fatal("the sync succeeded against a failing server")
			}

			var refused *WriteRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("a failed write did not come back as *WriteRefusedError; got %T: %v", err, err)
			}
			if refused.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", refused.Code, tc.wantCode)
			}
			if refused.Verb != tc.wantVerb {
				t.Errorf("verb = %q, want %q", refused.Verb, tc.wantVerb)
			}
			if refused.Status != tc.status {
				t.Errorf("status = %d, want %d", refused.Status, tc.status)
			}
			if refused.Endpoint != "/api/v1/applications/keda-prod-eu/sync" {
				t.Errorf("endpoint = %q, want the sync path Sharko built", refused.Endpoint)
			}

			// The sentence is typed out here by hand rather than compared
			// against the constant the code assigned: comparing a value with
			// itself cannot fail, so it would pin nothing.
			const wantSentence = "ArgoCD refused this change. Sharko does not repeat ArgoCD's own reply here"
			if !strings.Contains(err.Error(), wantSentence) {
				t.Errorf("the error no longer opens with Sharko's fixed sentence.\n\ngot: %v", err)
			}
			// And the retired format string must not come back.
			if strings.Contains(err.Error(), "unexpected status") {
				t.Errorf("the retired \"unexpected status …\" wording is back, and with it whatever ArgoCD wrote after it.\n\ngot: %v", err)
			}
			for _, want := range []string{
				string(tc.wantCode),
				tc.wantVerb,
				"/api/v1/applications/keda-prod-eu/sync",
				fmt.Sprintf("status=%d", tc.status),
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error no longer carries %q — an operator cannot tell which call failed and how.\n\ngot: %v", want, err)
				}
			}
		})
	}
}

// TestWriteRefused_KeepsTheTwoSentinels proves the 401/403 classification
// callers already branch on survived the change, and that it is decided from
// the code rather than from any text.
func TestWriteRefused_KeepsTheTwoSentinels(t *testing.T) {
	for _, tc := range []struct {
		status   int
		sentinel error
		other    error
	}{
		{http.StatusUnauthorized, ErrTokenInvalid, ErrPermissionDenied},
		{http.StatusForbidden, ErrPermissionDenied, ErrTokenInvalid},
	} {
		t.Run(fmt.Sprintf("%d", tc.status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(argocdErrorPayload(writeLeakCarriers()[0].url)))
			}))
			defer ts.Close()

			err := NewClient(ts.URL, "tok", false).SyncApplication(context.Background(), "keda-prod-eu")
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("a %d no longer matches its sentinel with errors.Is; got %v", tc.status, err)
			}
			if errors.Is(err, tc.other) {
				t.Errorf("a %d ALSO matched the other sentinel — the two classes have collapsed into one", tc.status)
			}
		})
	}
}

// --- the terminate race, decided by type ------------------------------------

// TestTerminateOperation_NoOperationIsATypeNotAPhrase is the replacement for
// the two copies of isBenignTerminateError.
//
// Both used to lowercase the error and search it for "no operation is in
// progress". The payload here says something completely different from that
// phrase on purpose: if the classification still depended on ArgoCD's wording,
// this test would fail.
func TestTerminateOperation_NoOperationIsATypeNotAPhrase(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantBenign bool
	}{
		{
			name:       "a 400 whose wording is nothing like the old phrase",
			status:     http.StatusBadRequest,
			body:       `{"error":"there is currently nothing running that could be cancelled","code":3}`,
			wantBenign: true,
		},
		{
			name:       "the wording the old matcher was written for",
			status:     http.StatusBadRequest,
			body:       `{"error":"Unable to terminate operation. No operation is in progress","code":3}`,
			wantBenign: true,
		},
		{
			name:       "a 500 that happens to contain the old phrase",
			status:     http.StatusInternalServerError,
			body:       `{"error":"internal error while checking whether no operation is in progress","code":2}`,
			wantBenign: false,
		},
		{
			name:       "a 403",
			status:     http.StatusForbidden,
			body:       `{"error":"permission denied","code":7}`,
			wantBenign: false,
		},
		{
			name:       "a 404",
			status:     http.StatusNotFound,
			body:       `{"error":"application not found","code":5}`,
			wantBenign: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()

			err := NewClient(ts.URL, "tok", false).TerminateOperation(context.Background(), "keda-prod-eu")
			if err == nil {
				t.Fatal("the terminate succeeded against a failing server")
			}
			if got := errors.Is(err, ErrNoOperationInProgress); got != tc.wantBenign {
				t.Errorf("errors.Is(err, ErrNoOperationInProgress) = %v, want %v\n\ngot: %v", got, tc.wantBenign, err)
			}
			// Whichever way it went, ArgoCD's own wording is not in the error.
			if strings.Contains(strings.ToLower(err.Error()), "no operation is in progress") {
				t.Errorf("ArgoCD's own phrase came back inside the error: %v", err)
			}
		})
	}

	// A successful terminate is not benign-anything; it is a success.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	if err := NewClient(ts.URL, "tok", false).TerminateOperation(context.Background(), "keda-prod-eu"); err != nil {
		t.Errorf("a 200 terminate returned an error: %v", err)
	}
}

// TestSafeWriteFailure_TellsRefusedApartFromUnreachable covers the boundary
// helper the API handlers use, including the case where ArgoCD never answered
// at all and the transport error quotes the address it tried.
func TestSafeWriteFailure_TellsRefusedApartFromUnreachable(t *testing.T) {
	// 1. ArgoCD answered.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(argocdErrorPayload(writeLeakCarriers()[0].url)))
	}))
	defer ts.Close()
	refusedErr := NewClient(ts.URL, "tok", false).SyncApplication(context.Background(), "keda-prod-eu")
	if refusedErr == nil {
		t.Fatal("the sync succeeded against a failing server")
	}
	refusedSentence := SafeWriteFailure(refusedErr)
	if !strings.Contains(refusedSentence, "ArgoCD refused this change.") {
		t.Errorf("a refusal did not produce the refusal sentence: %q", refusedSentence)
	}
	assertNoWriteLeak(t, "SafeWriteFailure on a refusal", refusedSentence, writeLeakCarriers()[0].url)

	// 2. Sharko never got an answer, and the address it dialled carries the
	// token — which is what the transport error quotes.
	//
	// The token sits in the USERNAME slot here, not the password one. Go's
	// HTTP client already blanks a password before it builds *url.Error, so a
	// password-slot fixture would be cleaned by the standard library rather
	// than by Sharko and this half would prove nothing. The username survives
	// stdlib redaction, which is exactly why the username slot is one of the
	// four carriers this file sweeps for.
	unreachable := NewClient("https://"+writeLeakSentinel+"@127.0.0.1:1", "tok", false)
	dialErr := unreachable.SyncApplication(context.Background(), "keda-prod-eu")
	if dialErr == nil {
		t.Fatal("the dial to a closed port succeeded — the unreachable branch never ran")
	}
	// Positive control (BF8). Sharko's own error no longer quotes the address
	// it dialled, so the old check — "the raw error must contain the
	// sentinel" — cannot be made against it any more. It is made against a
	// bare http.Client instead, doing the same dial to the same closed port.
	// That proves the leak is still THERE to be stopped: if a future Go
	// release stops quoting the address, or the dial stops failing, this line
	// fails loudly rather than letting the rest of the test pass on nothing.
	if _, rawErr := (&http.Client{}).Get("https://" + writeLeakSentinel + "@127.0.0.1:1/api/v1/applications"); rawErr == nil {
		t.Fatal("a bare http.Client dial to a closed port succeeded — this test can prove nothing")
	} else if !strings.Contains(rawErr.Error(), writeLeakSentinel) {
		t.Fatal("a bare http.Client no longer quotes the address it tried, so there is nothing for this half to prove")
	}
	if strings.Contains(dialErr.Error(), writeLeakSentinel) {
		t.Errorf("Sharko's own transport error quotes the address it dialled: %q", dialErr.Error())
	}
	dialSentence := SafeWriteFailure(dialErr)
	if !strings.Contains(dialSentence, "Sharko could not get an answer from ArgoCD") {
		t.Errorf("an unreachable ArgoCD did not produce the unreachable sentence: %q", dialSentence)
	}
	assertNoWriteLeak(t, "SafeWriteFailure on an unreachable ArgoCD", dialSentence, "")
}
