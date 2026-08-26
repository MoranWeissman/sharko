package api

// bf1_freshness_leak_test.go — BF1's own proof, with its own sentinel.
//
// # What is being proved, and why an existing test was not enough
//
// Two separate holes meet in this file.
//
// The first is the log's. A repository address can carry its token in three
// places — as the username, as the password, or as a query parameter — and the
// log sink only ever asked about the first two, because both of those end at
// an "@" and the sink's whole test was "does this contain an @". The third
// walked past a stripper written to remove exactly it. credsafe had the full
// rule the whole time; the sink held a second copy of it that was one
// character wide.
//
// The second is GET /api/v1/catalog/freshness. The engine-pin snapshot stored
// err.Error() verbatim and the handler returned it in the response body. What
// feeds that check is a closure wired up in cmd/sharko/serve.go, and today's
// closure happens to hand back safe text. The standing rule does not care:
// no raw provider, Git or Kubernetes error text reaches a user-facing surface
// even when today's trace says it cannot, because the feeder changes and
// nobody comes back to recheck this line.
//
// # How it is proved
//
// A sentinel that appears nowhere else in this repository is planted in all
// three carrier shapes, pushed through the REAL freshness pass and the REAL
// router, and the output is swept for the sentinel in every shape a value has
// genuinely escaped software wearing: re-encoded, hashed "for safety",
// truncated to a "harmless" fragment, reported as a length, or masked to a
// string of stars that is exactly as long as the secret.
//
// The sweep machinery is leakFormsFor / findLeakIn from init_status_leak_test.
// go — the same matcher pointed at a different sentinel, not a second copy of
// it. The positive control below is this file's own, and it runs before any
// absence here is believed: a sweep that cannot find a secret it was handed
// proves nothing at all when it reports finding none.
//
// The log is captured through captureSlog, which builds the handler chain
// `sharko serve` installs. A harness that builds its own logger is not the
// pipeline production runs, and this package has been burned by exactly that.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/helm"
)

// bf1Sentinel stands in for the access token. It appears nowhere else in this
// repository, so finding it anywhere is proof of a leak rather than a
// coincidence.
const bf1Sentinel = "K4RP-bf1-query-token-sentinel-9w2f7t-never-leaves-the-server-a1c6"

// The three shapes a repository address carries a credential in. The third is
// the one this story is about.
const (
	bf1TokenAsUsername = "https://" + bf1Sentinel + "@charts.example/org/addons"
	bf1TokenAsPassword = "https://x-access-token:" + bf1Sentinel + "@charts.example/org/addons"
	bf1TokenAsQuery    = "https://charts.example/org/addons?access_token=" + bf1Sentinel
)

// bf1SafeAddress is what credsafe leaves of all three: host and path, which is
// all anybody needed in order to recognise which repository is meant.
const bf1SafeAddress = "https://charts.example/org/addons"

func bf1Carriers() map[string]string {
	return map[string]string{
		"the token in the username position": bf1TokenAsUsername,
		"the token in the password position": bf1TokenAsPassword,
		"the token as a query parameter":     bf1TokenAsQuery,
	}
}

func bf1Forms(tokenisedURL string) map[string]string {
	return leakFormsFor(bf1Sentinel, tokenisedURL)
}

func assertNoBF1Leak(t *testing.T, where, tokenisedURL, text string) {
	t.Helper()
	for _, name := range findLeakIn(text, bf1Forms(tokenisedURL)) {
		t.Errorf("%s carries %s of the repository token.\n\nthe text was:\n%s", where, name, text)
	}
}

// TestBF1Sweep_FindsAPlantedSentinel is the positive control, and it must be
// believed before any absence in this file is.
//
// Every other test here asserts that something is NOT present, and "not
// present" is also what a broken sweep reports. So each form is planted in
// turn and the finder is required to name it.
func TestBF1Sweep_FindsAPlantedSentinel(t *testing.T) {
	forms := bf1Forms(bf1TokenAsQuery)
	if len(forms) < 20 {
		t.Fatalf("the sweep only looks for %d forms — it has been hollowed out", len(forms))
	}
	planted := 0
	for name, form := range forms {
		text := "some ordinary looking output " + form + " and some more text"
		found := findLeakIn(text, forms)
		if len(found) == 0 {
			t.Errorf("the sweep was handed %s and found nothing — every absence it reports is worthless", name)
			continue
		}
		planted++
	}
	if planted != len(forms) {
		t.Fatalf("the sweep found only %d of its %d own planted forms", planted, len(forms))
	}
	// And it must be quiet on text that carries nothing, or it would find a
	// leak in every string and prove nothing that way instead.
	if found := findLeakIn("an ordinary log line about a repository at "+bf1SafeAddress, forms); len(found) > 0 {
		t.Fatalf("the sweep reports %v in text that carries no part of the token — it fires on everything", found)
	}
}

// bf1FailingLister fails every version fetch with an error whose words embed
// the whole tokenised address, which is what a Helm client, a Git library and
// net/url all really do.
type bf1FailingLister struct{ repoURL string }

func (f *bf1FailingLister) ListVersions(_ context.Context, repo, chart string) ([]helm.ChartVersion, error) {
	return nil, fmt.Errorf("Get %q: dial tcp: lookup charts.example: no such host", repo+"/"+chart)
}

// TestBF1_FreshnessPass_NeverLogsTheRepositoryToken drives a real freshness
// pass over an approved addon whose address carries a token, in each of the
// three shapes, and sweeps the log the production handler chain wrote.
func TestBF1_FreshnessPass_NeverLogsTheRepositoryToken(t *testing.T) {
	for name, repoURL := range bf1Carriers() {
		t.Run(name, func(t *testing.T) {
			lister := &bf1FailingLister{repoURL: repoURL}

			// The fixture must really fail and the failure must really
			// carry the token. Without this half every assertion below
			// would pass while proving nothing.
			_, fetchErr := lister.ListVersions(context.Background(), repoURL, "addons")
			if fetchErr == nil {
				t.Fatal("the fixture must FAIL — there is nothing to prove otherwise")
			}
			if !strings.Contains(fetchErr.Error(), bf1Sentinel) {
				t.Fatalf("the underlying error does NOT carry the token, so this test proves nothing.\n\ngot: %v", fetchErr)
			}

			sched := catalog.NewFreshnessScheduler(nil, lister, nil, time.Hour).
				WithApprovedAddons(func(context.Context) ([]catalog.ApprovedAddon, error) {
					return []catalog.ApprovedAddon{{Name: "addons", RepoURL: repoURL, Chart: "addons"}}, nil
				})

			logs := captureSlog(t, func() { sched.RefreshForTest() })

			// The pass must actually have run and actually have logged the
			// failure, or the sweep swept nothing.
			if !strings.Contains(logs, `"msg":"[freshness] version fetch failed"`) {
				t.Fatalf("the version-fetch failure was never logged, so the sweep below swept nothing:\n%s", logs)
			}
			assertNoBF1Leak(t, "the freshness pass log", repoURL, logs)

			// And the replacement must be PRESENT. An absence on its own is
			// also what a handler that returned early produces.
			if !strings.Contains(logs, bf1SafeAddress) {
				t.Errorf("the log no longer names the repository at all — an operator cannot tell which one failed:\n%s", logs)
			}

			// The snapshot the UI reads must be clean too, in both the
			// sentence a person sees and the stored error field.
			snap, ok := sched.VersionSnapshot("addons")
			if !ok {
				t.Fatal("no snapshot was recorded, so the pass did not reach the code under test")
			}
			assertNoBF1Leak(t, "the snapshot's plain-English reason", repoURL, snap.NoDataReason)
			assertNoBF1Leak(t, "the snapshot's stored error", repoURL, snap.Err)
			if snap.Err == "" {
				t.Error("the snapshot records no failure at all — the fixture did not fail where this test thinks it did")
			}
		})
	}
}

// TestBF1_CatalogFreshnessEndpoint_NeverReturnsRawErrorText is the API half.
// The engine-pin check fails with an error carrying the token, and the real
// GET /api/v1/catalog/freshness is driven through the real router.
func TestBF1_CatalogFreshnessEndpoint_NeverReturnsRawErrorText(t *testing.T) {
	for name, repoURL := range bf1Carriers() {
		t.Run(name, func(t *testing.T) {
			pinErr := fmt.Errorf("reading the engine pin from %s: unexpected status 401", repoURL)
			if !strings.Contains(pinErr.Error(), bf1Sentinel) {
				t.Fatalf("the fixture error does not carry the token, so this test proves nothing: %v", pinErr)
			}

			srv := newTestServer()
			srv.SetCatalog(testCatalog(t))
			sched := catalog.NewFreshnessScheduler(nil, nil, func(context.Context) (*catalog.EnginePinStatus, error) {
				return nil, pinErr
			}, time.Hour)

			logs := captureSlog(t, func() { sched.RefreshForTest() })
			srv.SetFreshness(sched)

			// The check must really have failed, or there is no error text
			// for the endpoint to have carried.
			snap, ok := sched.EnginePinSnapshot()
			if !ok {
				t.Fatal("no engine-pin snapshot was recorded — the pass did not reach the code under test")
			}
			if snap.Err == "" {
				t.Fatal("the engine-pin snapshot records no failure, so the endpoint has nothing to leak and this test proves nothing")
			}

			router := NewRouter(srv, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/freshness", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("GET /api/v1/catalog/freshness returned %d, so the body below is not the one operators see.\n\nbody: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()

			// The body must really contain the engine-pin section, or the
			// sweep is looking at a response that never carried the field.
			var decoded catalogFreshnessResponse
			if err := json.Unmarshal([]byte(body), &decoded); err != nil {
				t.Fatalf("decoding the response: %v\n\nbody: %s", err, body)
			}
			if decoded.EnginePin == nil {
				t.Fatalf("the response carries no engine_pin section, so the field under test was never rendered.\n\nbody: %s", body)
			}
			if decoded.EnginePin.Err == "" {
				t.Fatalf("the response's engine_pin.error is empty, so this sweep is proving nothing about it.\n\nbody: %s", body)
			}

			assertNoBF1Leak(t, "the GET /api/v1/catalog/freshness body", repoURL, body)
			assertNoBF1Leak(t, "the freshness pass log", repoURL, logs)
		})
	}
}

// TestBF1_TheProductionLogChainStripsAQueryToken is the narrowest statement of
// the defect, made against the chain `sharko serve` installs rather than
// against a handler assembled for the test.
//
// It is here, in the package whose call sites hand raw addresses to slog under
// keys nobody would call sensitive — "repoURL", "url", "repo_url" — rather than
// only next to the sink, because next to the sink is where the last version of
// this was proved and the pipeline was not the one shipping.
func TestBF1_TheProductionLogChainStripsAQueryToken(t *testing.T) {
	for _, key := range []string{"repoURL", "url", "repo_url", "repo", "registry"} {
		for name, repoURL := range bf1Carriers() {
			t.Run(key+"/"+name, func(t *testing.T) {
				logs := captureSlog(t, func() {
					slogInfoForTest(key, repoURL)
				})
				if !strings.Contains(logs, `"msg":"bf1 line"`) {
					t.Fatalf("the log line was never written, so this test swept nothing:\n%s", logs)
				}
				assertNoBF1Leak(t, "a log line under key "+key, repoURL, logs)
				if !strings.Contains(logs, bf1SafeAddress) {
					t.Errorf("the log line no longer names the repository at all under key %q:\n%s", key, logs)
				}
			})
		}
	}
}

// slogInfoForTest writes one attribute through the default logger, which
// captureSlog has pointed at the production handler chain.
func slogInfoForTest(key, value string) {
	slog.Info("bf1 line", key, value)
}
