// catalog_repo_url_door_test.go — the doors say the rule, early and in plain
// English.
//
// # Why this file exists
//
// A break test wrote off the early check in POST /api/v1/addons — the handler
// stopped looking at the request's repository address entirely — and the whole
// suite stayed green. Nothing was actually unsafe: the orchestrator door and
// the catalog writer both still refused, so no credential could have reached
// Git. But the operator's experience collapsed from "400, here is the rule" to
// a late gateway error about something else, and no test noticed.
//
// So the doors get their own proof. Each case is one door, driven end to end,
// asserting the status, the machine-readable code and the exact sentence.
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// doorSecret is the stand-in for an operator's token, planted in the address
// each door is handed.
const doorSecret = "P4ss-w0rd-the-operator-actually-stored-2h6d"

// doorSweptSecret is the same text written independently, so the fixture and
// the sweep do not share one constant.
const doorSweptSecret = "P4ss-w0rd-the-operator-actually-stored-2h6d"

// doorRuleSentence is typed out in full rather than read from the code. A test
// that quotes the code passes whatever the code decided to say.
const doorRuleSentence = "Catalog repository URLs in the technical preview must be ones Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL."

func TestDoorSentinelsAgree(t *testing.T) {
	if doorSecret != doorSweptSecret || doorSecret == "" {
		t.Fatalf("planted %q and swept %q disagree, or are empty", doorSecret, doorSweptSecret)
	}
	if credsafe.UnsupportedRepoURLMessage != doorRuleSentence {
		t.Errorf("the sentence the doors say has changed.\n  now:  %s\n  want: %s", credsafe.UnsupportedRepoURLMessage, doorRuleSentence)
	}
}

// doorAddresses is every shape a door has to turn away, including the two
// refused on the structural rule alone.
var doorAddresses = map[string]string{
	"userinfo with a password":                     "https://git-user:" + doorSecret + "@charts.example/org/charts",
	"userinfo without a password":                  "https://" + doorSecret + "@charts.example/org/charts",
	"a query string":                               "https://charts.example/org/charts?access_token=" + doorSecret,
	"a fragment":                                   "https://charts.example/org/charts#" + doorSecret,
	"an ordinary query string, refused on purpose": "https://charts.example/org/charts?ref=main",
}

func TestDoorAddressesCoverEveryShape(t *testing.T) {
	const want = 5
	if len(doorAddresses) != want {
		t.Fatalf("the address table has %d entries, want exactly %d", len(doorAddresses), want)
	}
}

// TestAddAddonDoor_RefusesAnUnsupportedRepoURL drives POST /api/v1/addons, the
// v3 add door.
//
// The isolated test server has no upstream connection wired, so anything that
// reaches the upstream step comes back 502/503. That is what makes this test
// meaningful: a 400 can only have come from the door, and a 502 means the door
// let the address through and the refusal happened later, if at all.
func TestAddAddonDoor_RefusesAnUnsupportedRepoURL(t *testing.T) {
	for name, addr := range doorAddresses {
		t.Run(name, func(t *testing.T) {
			srv := newIsolatedTestServer(t)
			router := NewRouter(srv, nil)

			body, _ := json.Marshal(map[string]string{
				"name":     "leaky",
				"chart":    "leaky",
				"repo_url": addr,
				"version":  "1.0.0",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/addons", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 — the door let the address through, so the operator finds out late and hears about something else.\n\nbody: %s", w.Code, w.Body.String())
			}

			raw := w.Body.String()
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &resp); err != nil {
				t.Fatalf("the refusal is not JSON: %v\n\nbody: %s", err, raw)
			}
			if got, _ := resp["code"].(string); got != CodeUnsupportedRepoURL {
				t.Errorf("code = %q, want %q — a client has to branch on the code, not on the English", got, CodeUnsupportedRepoURL)
			}
			msg, _ := resp["error"].(string)
			if !strings.Contains(msg, doorRuleSentence) {
				t.Errorf("the refusal does not state the rule.\ngot: %q", msg)
			}
			assertDoorSaysNothingAboutTheValue(t, "the POST /addons refusal", raw)
		})
	}
}

// TestAddAddonDoor_LetsACleanAddressPast is the positive control. Without it,
// a door that refused everything would pass every case above.
func TestAddAddonDoor_LetsACleanAddressPast(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]string{
		"name":     "leaky",
		"chart":    "leaky",
		"repo_url": "https://charts.example/org/charts",
		"version":  "1.0.0",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusBadRequest {
		t.Errorf("a perfectly ordinary address was turned away at the door: %s", w.Body.String())
	}
}

// TestValidateCatalogChartDoor_RefusesAnUnsupportedRepoURL drives
// GET /catalog/validate, the paste-a-URL door. It answers 200 with
// `valid: false` by design, so the proof is the body, not the status.
func TestValidateCatalogChartDoor_RefusesAnUnsupportedRepoURL(t *testing.T) {
	for name, addr := range doorAddresses {
		t.Run(name, func(t *testing.T) {
			srv := newIsolatedTestServer(t)
			req := httptest.NewRequest(http.MethodGet,
				"/api/v1/catalog/validate?chart=leaky&repo="+url.QueryEscape(addr), nil)
			w := httptest.NewRecorder()
			srv.handleValidateCatalogChart(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — this endpoint reports failure in the body", w.Code)
			}
			var resp catalogValidateResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v\n\nbody: %s", err, w.Body.String())
			}
			if resp.Valid {
				t.Fatal("the address came back valid — the door let it through")
			}
			if resp.ErrorCode != validateErrInvalidInput {
				t.Errorf("error_code = %q, want %q", resp.ErrorCode, validateErrInvalidInput)
			}
			if !strings.Contains(resp.Message, doorRuleSentence) {
				t.Errorf("the message does not state the rule.\ngot: %q", resp.Message)
			}
			assertDoorSaysNothingAboutTheValue(t, "the /catalog/validate body", w.Body.String())
		})
	}
}

// TestRepoChartsDoor_RefusesAnUnsupportedRepoURL is the same for
// GET /catalog/repo-charts.
func TestRepoChartsDoor_RefusesAnUnsupportedRepoURL(t *testing.T) {
	for name, addr := range doorAddresses {
		t.Run(name, func(t *testing.T) {
			srv := newIsolatedTestServer(t)
			req := httptest.NewRequest(http.MethodGet,
				"/api/v1/catalog/repo-charts?repo="+url.QueryEscape(addr), nil)
			w := httptest.NewRecorder()
			srv.handleListRepoCharts(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			var resp repoChartsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v\n\nbody: %s", err, w.Body.String())
			}
			if resp.Valid {
				t.Fatal("the address came back valid — the door let it through")
			}
			if !strings.Contains(resp.Message, doorRuleSentence) {
				t.Errorf("the message does not state the rule.\ngot: %q", resp.Message)
			}
			assertDoorSaysNothingAboutTheValue(t, "the /catalog/repo-charts body", w.Body.String())
		})
	}
}

// assertDoorSaysNothingAboutTheValue plants nothing of its own — the caller
// already planted the secret in the address the door was handed. It proves
// the sweep can see that secret, then requires the door's answer not to carry
// it.
func assertDoorSaysNothingAboutTheValue(t *testing.T, what, body string) {
	t.Helper()

	// Positive control: the sweep really can find this text when it is
	// present. Checked against the address the doors were handed, not
	// against the body, so it is a property of the sweep and not of the
	// thing being swept.
	planted := "https://git-user:" + doorSecret + "@charts.example/org/charts"
	if !strings.Contains(planted, doorSweptSecret) {
		t.Fatal("the sweep cannot find the secret in the address it was planted in — every check below would pass for the wrong reason")
	}
	if body == "" {
		t.Fatalf("%s is empty — there is nothing to sweep", what)
	}

	if strings.Contains(body, doorSweptSecret) {
		t.Errorf("%s carries the planted secret:\n%s", what, body)
	}
	if strings.Contains(body, doorSweptSecret[:8]) {
		t.Errorf("%s carries the first eight characters of the planted secret:\n%s", what, body)
	}
	if strings.Contains(body, "git-user") {
		t.Errorf("%s carries the user half of the address:\n%s", what, body)
	}
	if strings.Contains(body, "access_token") {
		t.Errorf("%s carries the query parameter the token was written into:\n%s", what, body)
	}
}
