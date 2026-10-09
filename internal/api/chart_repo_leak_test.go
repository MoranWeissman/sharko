package api

// chart_repo_leak_test.go — the positive-control sweep for B12 and B13.
//
// Two paths, one shape of bug: a repository address that carries a token, and
// Sharko handing it back to whoever asked.
//
//	B12 — GET /api/v1/marketplace/sources echoed the CONFIGURED source URL on an
//	      ordinary 200. The env var that sets it is documented as being for
//	      "tokened/private URLs that must not be committed to Git", so a token
//	      there is the expected shape, not an accident.
//
//	B13 — the chart-version and upgrade responses formatted Go's own *url.Error
//	      into the body. net/url's stripPassword replaces the PASSWORD and KEEPS
//	      the USERNAME, and a token is normally written in the username
//	      position, so it came back whole.
//
// Every assertion below is an ABSENCE, and a broken sweep reports an absence
// too. So TestChartRepoLeakSweep_FindsAPlantedSentinel runs first and requires
// the finder to name a secret somebody deliberately put there. And each
// handler test also asserts the SAFE replacement is present, so a handler that
// short-circuited before reaching the code under test fails loudly as "never
// got there" rather than passing as "nothing leaked".

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// chartRepoLeakSentinel stands in for the access token. It appears nowhere
// else in this repository, so finding it anywhere is proof rather than
// coincidence.
const chartRepoLeakSentinel = "K4PZ-chart-repo-token-sentinel-8w1q6v-never-leaves-the-server-a7c2"

// chartRepoLeakForms is the same sweep the init-status file uses, pointed at
// this file's sentinel. Not a second sweep — leakFormsFor is the one list.
func chartRepoLeakForms() map[string]string {
	return leakFormsFor(chartRepoLeakSentinel, "")
}

// findChartRepoLeak reports every form of the sentinel present in text.
func findChartRepoLeak(text string) []string {
	return findLeakIn(text, chartRepoLeakForms())
}

// assertNoChartRepoLeak fails, naming each form, when text carries the token.
func assertNoChartRepoLeak(t *testing.T, where, text string) {
	t.Helper()
	for _, name := range findChartRepoLeak(text) {
		t.Errorf("%s carries %s of the repository token.\n\nthe text was:\n%s", where, name, text)
	}
}

// TestChartRepoLeakSweep_FindsAPlantedSentinel is the positive control.
//
// If this ever goes green while finding nothing, every other test in this file
// is decoration.
func TestChartRepoLeakSweep_FindsAPlantedSentinel(t *testing.T) {
	forms := chartRepoLeakForms()
	if len(forms) < 20 {
		t.Fatalf("the sweep only looks for %d forms — it has been hollowed out", len(forms))
	}
	for name, form := range forms {
		planted := "some ordinary looking response body " + form + " and some more text"
		if found := findChartRepoLeak(planted); len(found) == 0 {
			t.Errorf("the sweep did NOT find a planted %s (%q).\n\nA sweep that cannot find a secret somebody put there proves nothing about the ones it says are absent.", name, form)
		}
	}
	// And it stays quiet on text that is genuinely clean, so a green run
	// elsewhere is a real result rather than a sweep that fires on everything.
	if found := findChartRepoLeak(credsafe.ChartRepoListVersionsMessage); len(found) != 0 {
		t.Errorf("the sweep fired on the safe sentence, naming %v — every other assertion here would be noise", found)
	}
}

// --- B12: the catalog sources page ------------------------------------------

// b12TokenisedSourceURL is the threat in the shape the env var documents: a
// catalog source address with the token inside it.
const b12TokenisedSourceURL = "https://x-access-token:" + chartRepoLeakSentinel + "@catalog.example/private/catalog.yaml"

// b12SafeSourceURL is the credential-stripped form of the address. It used to
// be what the row showed; that allowance was withdrawn (BF14 revision 2): the
// documented private-catalog shape hides the token in the PATH, where the
// stripping can't see it, so even this form must never appear. Written out as
// a literal here on purpose — pinning against the constant the code assigned
// would pass whatever the code decided to say.
const b12SafeSourceURL = "https://catalog.example/private/catalog.yaml"

// TestListCatalogSources_TokenisedURL_NotEchoedOn200 drives the REAL handler
// with a REAL sources.Fetcher whose snapshot holds the tokenised address, and
// sweeps the whole serialized body.
func TestListCatalogSources_TokenisedURL_NotEchoedOn200(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)

	success := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, map[string]*sources.SourceSnapshot{
		b12TokenisedSourceURL: {
			URL:           b12TokenisedSourceURL,
			Status:        sources.StatusOK,
			LastSuccessAt: success,
			LastAttemptAt: success,
			Entries:       []catalog.CatalogEntry{{Name: "a", Chart: "a", Repo: "https://x"}},
		},
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/marketplace/sources", nil)
	rw := httptest.NewRecorder()
	s.handleListCatalogSources(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — this must leak on the HAPPY path, which is the whole point", rw.Code)
	}
	body := rw.Body.String()
	assertNoChartRepoLeak(t, "the GET /marketplace/sources body", body)

	// The credential-stripped form must be absent too (BF14 revision 2):
	// a path can BE the credential, so stripping the userinfo is not
	// enough and the whole address stays inside the process.
	if strings.Contains(body, b12SafeSourceURL) {
		t.Errorf("the body contains the credential-stripped address %q — the withdrawn allowance is back.\n\nbody:\n%s", b12SafeSourceURL, body)
	}

	var rows []catalogSourceRecord
	if err := json.Unmarshal(rw.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (embedded + the tokenised source)", len(rows))
	}
	if rows[1].URL != credsafe.RedactedSourceLabel {
		t.Errorf("row URL = %q, want exactly %q", rows[1].URL, credsafe.RedactedSourceLabel)
	}
}

// --- B13: Go's own error text -----------------------------------------------

// b13TokenisedRepoURL puts the token in the USERNAME position, which is the
// whole point: stripPassword masks the password half and hands this one back.
func b13TokenisedRepoURL(hostport string) string {
	return "https://" + chartRepoLeakSentinel + "@" + hostport
}

// closedHostPort returns an address nothing is listening on, so a real
// http.Client produces a real *url.Error the way production does.
func closedHostPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// TestGoURLErrorKeepsTheUsername is the measurement this whole story rests on.
//
// It is not taken on trust from a report: it asserts, against the real
// net/http client, that the token survives in the username position and is
// masked in the password position. If Go ever changes that, this test says so
// before anything downstream quietly stops being necessary.
func TestGoURLErrorKeepsTheUsername(t *testing.T) {
	dead := closedHostPort(t)
	client := &http.Client{Timeout: 2 * time.Second}

	_, err := client.Get(b13TokenisedRepoURL(dead) + "/index.yaml")
	if err == nil {
		t.Fatal("expected the request to a closed port to fail")
	}
	if !strings.Contains(err.Error(), chartRepoLeakSentinel) {
		t.Fatalf("Go's *url.Error no longer keeps a username-position token.\n\nThat would be good news, but this story's fixes are written on the assumption that it does — re-read them before relaxing anything.\n\ngot: %v", err)
	}

	_, perr := client.Get("https://user:" + chartRepoLeakSentinel + "@" + dead + "/index.yaml")
	if perr == nil {
		t.Fatal("expected the password-position request to fail too")
	}
	if strings.Contains(perr.Error(), chartRepoLeakSentinel) {
		t.Errorf("Go kept a PASSWORD-position token too: %v", perr)
	}
}

// realTokenisedURLError produces a real *url.Error whose text carries a
// username-position token — the exact error VALUE the gate under test has to
// stay safe against.
//
// It dials directly rather than going through helm.Fetcher. Since BF9 the
// fetcher refuses an address in this shape before it reaches the network, so
// it can no longer produce one — which is a good thing, and is proved
// separately by TestChartRepoGate_HelmNoLongerDialsATokenisedAddress below.
// The gate still has to be safe, because *url.Error is not only produced by
// the chart fetcher: any HTTP client in the tree handed an address like this
// makes the same value, and writeChartRepoError is what stands between such an
// error and a response body.
func realTokenisedURLError(t *testing.T, repoURL string) error {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimRight(repoURL, "/") + "/index.yaml")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected the request to a closed port to fail")
	}
	if !strings.Contains(err.Error(), chartRepoLeakSentinel) {
		t.Fatalf("the error under test does not carry the token, so this test would prove nothing: %v", err)
	}
	return err
}

// TestChartRepoGate_HelmNoLongerDialsATokenisedAddress records the behaviour
// change that made realTokenisedURLError dial for itself: Sharko's one chart
// fetcher refuses such an address instead of contacting it.
func TestChartRepoGate_HelmNoLongerDialsATokenisedAddress(t *testing.T) {
	repoURL := b13TokenisedRepoURL(closedHostPort(t))
	// Positive control: the address really does carry the sentinel.
	if !strings.Contains(repoURL, chartRepoLeakSentinel) {
		t.Fatal("the address under test does not carry the sentinel")
	}

	_, err := helm.NewFetcher().ListVersions(t.Context(), repoURL, "anything")
	if err == nil {
		t.Fatal("the fetcher accepted a tokenised address")
	}
	var unsupported *credsafe.UnsupportedRepoURLError
	if !errors.As(err, &unsupported) {
		t.Fatalf("the fetcher failed for some other reason — it should have refused the address outright: %v", err)
	}
	assertNoChartRepoLeak(t, "the fetcher's refusal", err.Error())
}

// captureLogs swaps in the REAL redacting handler over a buffer, so what the
// test sweeps is what a deployment would actually write.
func captureLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewRedactHandler(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)))
	return &buf, func() { slog.SetDefault(prev) }
}

// chartRepoGateCase is one routed operation: which op the handler passes and
// which sentence the caller must get back.
type chartRepoGateCase struct {
	name string
	op   chartRepoOp
	// want is typed out as a literal, never read from the constant the code
	// assigned — a test that quotes the code cannot catch the code changing.
	want string
}

// TestChartRepoGate_RealURLError_NeitherBodyNorLogCarriesTheToken drives the
// gate with a REAL *url.Error from a REAL helm fetch against a REAL closed
// port, then sweeps the response body AND the log.
func TestChartRepoGate_RealURLError_NeitherBodyNorLogCarriesTheToken(t *testing.T) {
	repoURL := b13TokenisedRepoURL(closedHostPort(t))
	err := realTokenisedURLError(t, repoURL)

	cases := []chartRepoGateCase{
		{"list versions", chartRepoListVersions, "Sharko could not read the list of versions from the chart repository."},
		{"fetch values", chartRepoFetchValues, "Sharko could not download the chart from its repository to read its values."},
		{"upgrade check", chartRepoUpgradeCheck, "Sharko could not work out what this upgrade changes, because it could not read the chart from its repository."},
		{"recommendations", chartRepoRecommendations, "Sharko could not suggest upgrade versions, because it could not read the list of versions from the chart repository."},
		{"ai summary", chartRepoAISummary, "Sharko could not get a written summary from the configured AI provider."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs, restore := captureLogs(t)
			defer restore()

			rw := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/upgrade/x/versions", nil)
			writeChartRepoError(rw, req, tc.op, err)

			body := rw.Body.String()
			assertNoChartRepoLeak(t, tc.name+" response body", body)
			assertNoChartRepoLeak(t, tc.name+" log output", logs.String())

			// The sentence must actually be there — otherwise a handler that
			// wrote nothing at all would pass the sweep.
			if !strings.Contains(body, tc.want) {
				t.Errorf("body does not carry the exact sentence.\nwant: %q\ngot:  %s", tc.want, body)
			}
			// A refused connection is 502 and must stay 502.
			if rw.Code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502 for a refused connection", rw.Code)
			}
			// And the log must still be useful: the type chain is what tells
			// an operator the address would not dial.
			if !strings.Contains(logs.String(), "url.Error") {
				t.Errorf("the log lost the error's type chain — an operator learns nothing from it now.\nlog:\n%s", logs.String())
			}
		})
	}
}

// TestChartRepoGate_KeepsThe502Versus504Distinction is the guard on the thing
// the previous story was afraid of breaking.
//
// The leak is fixed by never FORMATTING the error, not by unwrapping it, so
// the %w chain classifyUpstreamError walks is completely untouched. This pins
// that: a refused connection is 502, a timeout is 504, and they stay
// different.
func TestChartRepoGate_KeepsThe502Versus504Distinction(t *testing.T) {
	refused := realTokenisedURLError(t, b13TokenisedRepoURL(closedHostPort(t)))

	// A server that accepts and then never answers, so the client's own
	// deadline fires and the error reports Timeout() — the real 504 shape.
	block := make(chan struct{})
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); slowSrv.Close() }()

	host := strings.TrimPrefix(slowSrv.URL, "http://")
	// Dialled directly, for the same reason realTokenisedURLError is: since
	// BF9 the chart fetcher refuses a tokenised address before it reaches the
	// network, so it cannot produce this value any more. The gate still has
	// to keep the 502/504 distinction on a *url.Error that carries a token
	// from anywhere else.
	timedOut := func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet,
			"http://"+chartRepoLeakSentinel+"@"+host+"/index.yaml", nil)
		if rerr != nil {
			t.Fatalf("building the slow request: %v", rerr)
		}
		resp, e := http.DefaultClient.Do(req)
		if e == nil {
			resp.Body.Close()
			t.Fatal("expected the slow server to time the request out")
		}
		if !strings.Contains(e.Error(), chartRepoLeakSentinel) {
			t.Fatalf("the timeout error does not carry the token, so this case would prove nothing: %v", e)
		}
		return e
	}()

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"refused connection", refused, http.StatusBadGateway},
		{"timed out", timedOut, http.StatusGatewayTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs, restore := captureLogs(t)
			defer restore()
			rw := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/upgrade/x/versions", nil)
			writeChartRepoError(rw, req, chartRepoListVersions, tc.err)
			if rw.Code != tc.want {
				t.Errorf("status = %d, want %d — the 502/504 distinction an operator acts on has been degraded", rw.Code, tc.want)
			}
			assertNoChartRepoLeak(t, tc.name+" body", rw.Body.String())
			assertNoChartRepoLeak(t, tc.name+" log", logs.String())
		})
	}
}

// TestChartRepoSentences_AreExactAndDistinct pins each sentence by an exact
// literal typed here, and requires them to differ — five identical sentences
// would satisfy every other assertion in this file and tell an operator
// nothing about which step failed.
func TestChartRepoSentences_AreExactAndDistinct(t *testing.T) {
	want := map[chartRepoOp]string{
		chartRepoListVersions:    "Sharko could not read the list of versions from the chart repository.",
		chartRepoFetchValues:     "Sharko could not download the chart from its repository to read its values.",
		chartRepoUpgradeCheck:    "Sharko could not work out what this upgrade changes, because it could not read the chart from its repository.",
		chartRepoRecommendations: "Sharko could not suggest upgrade versions, because it could not read the list of versions from the chart repository.",
		chartRepoAISummary:       "Sharko could not get a written summary from the configured AI provider.",
	}
	if len(chartRepoMessages) != len(want) {
		t.Fatalf("chartRepoMessages has %d entries, this test pins %d — a new operation was added without a pinned sentence", len(chartRepoMessages), len(want))
	}
	seen := map[string]chartRepoOp{}
	for op, w := range want {
		got, ok := chartRepoMessages[op]
		if !ok {
			t.Errorf("operation %d has no sentence at all", op)
			continue
		}
		if got != w {
			t.Errorf("operation %d sentence changed.\nwant: %q\ngot:  %q", op, w, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("operations %d and %d say the same sentence — the caller cannot tell which step failed", prev, op)
		}
		seen[got] = op
		// A blank would pass a leak sweep perfectly.
		if strings.TrimSpace(got) == "" {
			t.Errorf("operation %d says nothing at all", op)
		}
		if !strings.HasSuffix(got, ".") {
			t.Errorf("operation %d is not a complete sentence: %q", op, got)
		}
	}
	// Every operation must have a log name too, or the log line says "op": "".
	if len(chartRepoOpNames) != len(want) {
		t.Errorf("chartRepoOpNames has %d entries, want %d", len(chartRepoOpNames), len(want))
	}
	for op := range want {
		if strings.TrimSpace(chartRepoOpNames[op]) == "" {
			t.Errorf("operation %d has no log name", op)
		}
	}
}

// TestSafeRepoURL_SchemelessAddressStillIdentifiesItself pins the second half
// of the small change: an address with no scheme, no "@", no "?" and no "#"
// has nowhere for a credential to be, so blanking it only cost the operator
// the ability to see which repository the row is about.
//
// BF12 changed two rows here. A scheme-less address is now read the same way
// as one with a scheme — as a network-path reference — so a credential in it
// is STRIPPED and the rest is shown, instead of the whole row going blank.
// Same treatment, same removal, and the operator can still see which
// repository the row is about. The sentinel check below is unchanged and is
// what actually proves nothing leaked.
func TestSafeRepoURL_SchemelessAddressStillIdentifiesItself(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"scheme-less, nothing to hide", "charts.example/org/repo", "charts.example/org/repo"},
		{"scheme-less with a port", "charts.example:8443/org/repo", "charts.example:8443/org/repo"},
		{"scp-style git address still says nothing", "git@host:org/repo.git", ""},
		{"scheme-less with a query is stripped, not blanked", "charts.example/repo?access_token=" + chartRepoLeakSentinel, "charts.example/repo"},
		{"scheme-less with a fragment is stripped, not blanked", "charts.example/repo#" + chartRepoLeakSentinel, "charts.example/repo"},
		{"token in the username is still removed", b13TokenisedRepoURL("host/org/repo"), "https://host/org/repo"},
		{"token in the password is still removed", "https://u:" + chartRepoLeakSentinel + "@host/org/repo", "https://host/org/repo"},
		{"token in the query is still removed", "https://host/org/repo?access_token=" + chartRepoLeakSentinel, "https://host/org/repo"},
		{"empty stays empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := credsafe.SafeRepoURL(tc.in)
			if got != tc.want {
				t.Errorf("SafeRepoURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			assertNoChartRepoLeak(t, fmt.Sprintf("SafeRepoURL(%q)", tc.name), got)
		})
	}
}

// --- B13 end-to-end: the REAL handlers, through the REAL router --------------
//
// The gate tests above prove the gate. They do NOT prove the handlers call it:
// putting the raw fmt.Sprintf back into addons_changelog.go leaves them all
// green, and only the routing guard notices. So these two drive the actual
// endpoints through NewRouter, with a catalog whose repo address carries the
// token in the USERNAME position and points at a port nothing is listening on
// — which is exactly the production failure.

// serverWithTokenisedCatalog wires a REAL server whose Git provider serves a
// REAL addons-catalog.yaml naming a chart repository with the token inside it.
func serverWithTokenisedCatalog(t *testing.T, repoURL string) *Server {
	t.Helper()
	srv := newTestServer()
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
	// The enveloped v3 shape, the same one addon_catalog_leak_test.go uses, so
	// the parser reads real bytes rather than a shape invented for this test.
	catalogYAML := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: leaky
      repoURL: ` + repoURL + `
      chart: leaky
      version: "1.0.0"
      namespace: leaky
`
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: map[string][]byte{
		"configuration/addons-catalog.yaml": []byte(catalogYAML),
	}})
	return srv
}

// TestRealEndpoints_TokenisedChartRepo_NeitherBodyNorLogCarriesTheToken is the
// end-to-end positive control for B13.
func TestRealEndpoints_TokenisedChartRepo_NeitherBodyNorLogCarriesTheToken(t *testing.T) {
	repoURL := b13TokenisedRepoURL(closedHostPort(t))

	// Since BF9 these end in a 422, not a 502. Nothing was contacted: Sharko
	// refused the address in the catalog entry before dialling, so reporting
	// a gateway error would send the operator to look at a network that was
	// never used. The refusal names the rule instead.
	for _, tc := range []struct {
		name   string
		path   string
		want   string
		status int
	}{
		{
			"addon changelog",
			"/api/v1/addons/leaky/changelog",
			"Sharko could not read the list of versions from the chart repository. Catalog repository URLs in the technical preview must be ones Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL.",
			http.StatusUnprocessableEntity,
		},
		{
			"upgrade versions",
			"/api/v1/upgrade/leaky/versions",
			"Sharko could not read the list of versions from the chart repository. Catalog repository URLs in the technical preview must be ones Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL.",
			http.StatusUnprocessableEntity,
		},
		{
			"upgrade recommendations",
			"/api/v1/upgrade/leaky/recommendations",
			"Sharko could not suggest upgrade versions, because it could not read the list of versions from the chart repository. Catalog repository URLs in the technical preview must be ones Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL.",
			http.StatusUnprocessableEntity,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs, restore := captureLogs(t)
			defer restore()

			srv := serverWithTokenisedCatalog(t, repoURL)
			router := NewRouter(srv, nil)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			body := w.Body.String()
			assertNoChartRepoLeak(t, tc.name+" response body", body)
			assertNoChartRepoLeak(t, tc.name+" log output", logs.String())

			// Without this, a handler that 401'd or 404'd before ever dialling
			// the repository would sail through the sweep having proved
			// nothing. The sentence's presence is what says "the code under
			// test actually ran".
			if !strings.Contains(body, tc.want) {
				t.Fatalf("the response does not carry the expected sentence, so this test never reached the code under test.\nwant: %q\nstatus: %d\nbody: %s", tc.want, w.Code, body)
			}
			if w.Code != tc.status {
				t.Errorf("status = %d, want %d", w.Code, tc.status)
			}
		})
	}
}
