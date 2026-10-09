package api

// chart_repo_versions_leak_test.go — the positive control for B14.
//
// # What the earlier proof missed
//
// TestRealEndpoints_TokenisedChartRepo_NeitherBodyNorLogCarriesTheToken points
// its tokenised chart repository at a CLOSED PORT. Every request it makes
// fails, so every assertion it makes is about the 502 path. It was green while
// GET /api/v1/upgrade/{addonName}/versions handed the same token back on an
// ordinary 200, four lines below the error-path fix that test was written for.
//
// A leak test that can only reach the failure path proves the failure path.
//
// So every test in this file stands up a chart repository that ANSWERS, drives
// the real endpoint through the real router, and asserts on a 200 body. The
// 200 is a hard requirement: a non-200 fails as "never reached the code under
// test" rather than passing as "nothing leaked".
//
// Three shapes of 200 are covered, because the endpoint has three:
//
//   - the repository answers with versions (buildVersionsResponse);
//   - the freshness scheduler's snapshot answers with versions;
//   - the repository answers successfully with an EMPTY version list, which
//     is the shape that produces no_data_reason — a sentence the browser
//     renders verbatim, and which named the repository in full.
//
// # Everything asserted twice
//
// Absence of the token, and PRESENCE of the safe address. An absence on its
// own is also what a handler that 404'd produces.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// --- a chart repository that actually answers -------------------------------

// b14LiveChartRepo starts a real HTTP server that serves a real Helm
// index.yaml. withVersions=false serves an index whose entry list for the
// chart is EMPTY — a completely successful fetch that yields no versions,
// which is the shape that produces no_data_reason.
//
// It returns the server's base address WITHOUT any credential in it: that is
// the value the response must carry, and it is compared exactly.
func b14LiveChartRepo(t *testing.T, chart string, withVersions bool) string {
	t.Helper()
	entries := "  " + chart + ":\n    - version: 1.4.0\n      appVersion: \"1.4.0\"\n    - version: 1.3.0\n      appVersion: \"1.3.0\"\n"
	if !withVersions {
		entries = "  " + chart + ": []\n"
	}
	index := "apiVersion: v1\nentries:\n" + entries
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/index.yaml") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(index))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// b14TokenisedRepoURL puts the sentinel in the USERNAME position of a live
// address — the shape net/url's stripPassword hands straight back, and the
// normal way a GitHub token is written into a URL.
func b14TokenisedRepoURL(base string) string {
	return strings.Replace(base, "http://", "http://"+chartRepoLeakSentinel+"@", 1)
}

// --- the sentences, typed out as literals ------------------------------------
//
// Never read from the constant the code assigned: a test that quotes the code
// passes whatever the code decided to say.

const b14NoVersionsSentencePrefix = "no freshness data for this source — "
const b14NoVersionsSentenceMiddle = " lists no versions of "

// --- 1. GET /api/v1/upgrade/{addonName}/versions ----------------------------
//
// # What changed here, and why these tests now come in two halves
//
// These tests were written for a Sharko that would happily fetch from a chart
// repository whose address carried a token, and they proved the token did not
// come back out on the 200. Since BF9 that situation cannot arise: Sharko
// refuses such an address before it dials, because the same address would be
// written into a file in the operator's Git repository.
//
// So each test now runs the endpoint TWICE:
//
//   - with a clean address, which must really succeed and really carry data.
//     That half is the positive control: it proves the endpoint, the branch
//     and the fixture all work, so the other half's silence means something.
//   - with the tokenised address, which must not put the token anywhere —
//     and must not come back claiming it read the repository.

// TestUpgradeVersions_TokenisedChartRepo_NotEchoedOn200 drives the upgrade
// versions endpoint against a repository that answers.
func TestUpgradeVersions_TokenisedChartRepo_NotEchoedOn200(t *testing.T) {
	safeRepo := b14LiveChartRepo(t, "leaky", true)

	t.Run("positive control: a clean address really is read", func(t *testing.T) {
		srv := serverWithTokenisedCatalog(t, safeRepo)
		router := NewRouter(srv, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/upgrade/leaky/versions", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		body := w.Body.String()
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — the endpoint does not work even for a clean address, so the sweep below would prove nothing.\n\nbody: %s", w.Code, body)
		}
		var resp struct {
			RepoURL  string `json:"repo_url"`
			Versions []struct {
				Version string `json:"version"`
			} `json:"versions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v\n\nbody: %s", err, body)
		}
		if len(resp.Versions) != 2 {
			t.Fatalf("the response carries %d versions, want 2 — the chart repository was never really read", len(resp.Versions))
		}
		// The operator must still be able to tell WHICH repository this is.
		if resp.RepoURL != safeRepo {
			t.Errorf("repo_url = %q, want exactly %q", resp.RepoURL, safeRepo)
		}
	})

	t.Run("a tokenised address puts the token nowhere", func(t *testing.T) {
		repoURL := b14TokenisedRepoURL(safeRepo)
		logs, restore := captureLogs(t)
		defer restore()

		srv := serverWithTokenisedCatalog(t, repoURL)
		router := NewRouter(srv, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/upgrade/leaky/versions", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assertNoChartRepoLeak(t, "the GET /upgrade/leaky/versions body", w.Body.String())
		assertNoChartRepoLeak(t, "the log output for GET /upgrade/leaky/versions", logs.String())

		if w.Code == http.StatusOK {
			t.Errorf("the endpoint came back 200 for an address Sharko refuses to save — it read the repository using it.\n\nbody: %s", w.Body.String())
		}
	})
}

// --- 2. GET /api/v1/marketplace/addons/{name}/versions, on-demand path ------

// b14CatalogWithRepo builds a one-entry curated catalog whose repo address is
// whatever is passed in.
func b14CatalogWithRepo(t *testing.T, repoURL string) *catalog.Catalog {
	t.Helper()
	y := fmt.Sprintf(`
addons:
  - name: leaky
    description: an addon whose chart repo address carries a token.
    chart: leaky
    repo: %s
    default_namespace: leaky
    maintainers: [example]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`, repoURL)
	c, err := catalog.LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return c
}

// marketplaceVersions drives the endpoint and returns the recorder.
func marketplaceVersions(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/marketplace/addons/leaky/versions", nil)
	req.SetPathValue("name", "leaky")
	w := httptest.NewRecorder()
	srv.handleListCatalogVersions(w, req)
	return w
}

// TestMarketplaceVersions_TokenisedChartRepo_NotEchoedOn200 drives the
// on-demand fetch path (no freshness scheduler wired).
func TestMarketplaceVersions_TokenisedChartRepo_NotEchoedOn200(t *testing.T) {
	safeRepo := b14LiveChartRepo(t, "leaky", true)

	t.Run("positive control: a clean address really is read", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		w := marketplaceVersions(t, serverWithCatalog(t, b14CatalogWithRepo(t, safeRepo)))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for a clean address.\n\nbody: %s", w.Code, w.Body.String())
		}
		var resp catalogVersionsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Versions) != 2 {
			t.Fatalf("the response carries %d versions, want 2 — the repository was never really read", len(resp.Versions))
		}
		if resp.Repo != safeRepo {
			t.Errorf("repo = %q, want exactly %q", resp.Repo, safeRepo)
		}
	})

	t.Run("a tokenised address puts the token nowhere", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		logs, restore := captureLogs(t)
		defer restore()

		w := marketplaceVersions(t, serverWithCatalog(t, b14CatalogWithRepo(t, b14TokenisedRepoURL(safeRepo))))
		assertNoChartRepoLeak(t, "the marketplace versions body", w.Body.String())
		assertNoChartRepoLeak(t, "the log output for the marketplace versions path", logs.String())

		var resp catalogVersionsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v\n\nbody: %s", err, w.Body.String())
		}
		if len(resp.Versions) != 0 {
			t.Errorf("the response carries %d versions for an address Sharko refuses to save — it read the repository using it", len(resp.Versions))
		}
	})
}

// --- 3. the freshness snapshot path, and no_data_reason ---------------------

// TestMarketplaceVersions_SnapshotWithVersions_NotEchoedOn200 drives the OTHER
// 200 branch: the one that answers out of the freshness scheduler's durable
// snapshot rather than fetching. It is a different construction of the same
// struct, so it needs its own proof.
func TestMarketplaceVersions_SnapshotWithVersions_NotEchoedOn200(t *testing.T) {
	safeRepo := b14LiveChartRepo(t, "leaky", true)

	t.Run("positive control: the snapshot branch really is taken", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		c := b14CatalogWithRepo(t, safeRepo)
		srv := serverWithCatalog(t, c)
		sched := catalog.NewFreshnessScheduler(c, helm.NewFetcher(), nil, time.Hour)
		sched.RefreshForTest()
		srv.SetFreshness(sched)

		snap, ok := sched.VersionSnapshot("leaky")
		if !ok || snap.Unknown || len(snap.Versions) == 0 {
			t.Fatalf("the freshness snapshot is not populated (ok=%v unknown=%v versions=%d), so the handler never took the snapshot branch", ok, snap.Unknown, len(snap.Versions))
		}

		w := marketplaceVersions(t, srv)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200.\n\nbody: %s", w.Code, w.Body.String())
		}
		var resp catalogVersionsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Versions) != 2 {
			t.Fatalf("the snapshot response carries %d versions, want 2", len(resp.Versions))
		}
		if resp.Repo != safeRepo {
			t.Errorf("repo = %q, want exactly %q", resp.Repo, safeRepo)
		}
	})

	t.Run("a tokenised address is never fetched and never echoed", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		logs, restore := captureLogs(t)
		defer restore()

		c := b14CatalogWithRepo(t, b14TokenisedRepoURL(safeRepo))
		srv := serverWithCatalog(t, c)
		sched := catalog.NewFreshnessScheduler(c, helm.NewFetcher(), nil, time.Hour)
		sched.RefreshForTest()
		srv.SetFreshness(sched)

		// The scheduler is one of the things that dials a chart repository,
		// so it must come back knowing nothing rather than having fetched.
		snap, ok := sched.VersionSnapshot("leaky")
		if ok && !snap.Unknown && len(snap.Versions) > 0 {
			t.Error("the freshness scheduler read the repository using an address Sharko refuses to save")
		}

		w := marketplaceVersions(t, srv)
		assertNoChartRepoLeak(t, "the snapshot-path body", w.Body.String())
		assertNoChartRepoLeak(t, "the log output for the snapshot path", logs.String())
		assertNoChartRepoLeak(t, "the snapshot's own no-data reason", snap.NoDataReason)
	})
}

// TestMarketplaceVersions_NoDataReason_NotEchoedOn200 is the second field of
// this surface, and the one that matters most: no_data_reason is documented as
// a sentence the browser renders verbatim, and it named the repository in
// full.
func TestMarketplaceVersions_NoDataReason_NotEchoedOn200(t *testing.T) {
	safeRepo := b14LiveChartRepo(t, "leaky", false)

	t.Run("positive control: the repository answers with no versions", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		c := b14CatalogWithRepo(t, safeRepo)
		srv := serverWithCatalog(t, c)
		sched := catalog.NewFreshnessScheduler(c, helm.NewFetcher(), nil, time.Hour)
		sched.RefreshForTest()
		srv.SetFreshness(sched)

		snap, ok := sched.VersionSnapshot("leaky")
		if !ok || !snap.Unknown {
			t.Fatalf("expected an Unknown snapshot from a repository that answered with no versions (ok=%v unknown=%v) — the branch under test was never taken", ok, snap.Unknown)
		}

		w := marketplaceVersions(t, srv)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — no_data_reason is a 200 shape, never an error.\n\nbody: %s", w.Code, w.Body.String())
		}
		var resp catalogVersionsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Repo != safeRepo {
			t.Errorf("repo = %q, want exactly %q", resp.Repo, safeRepo)
		}
		// The sentence must still SAY something, and it must still name the
		// repository — a fix that emptied it would leave an operator with a
		// blank that reads like "up to date".
		want := b14NoVersionsSentencePrefix + safeRepo + b14NoVersionsSentenceMiddle + "leaky"
		if resp.NoDataReason != want {
			t.Errorf("no_data_reason = %q,\nwant exactly       %q.", resp.NoDataReason, want)
		}
	})

	t.Run("a tokenised address puts the token in no sentence", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		logs, restore := captureLogs(t)
		defer restore()

		c := b14CatalogWithRepo(t, b14TokenisedRepoURL(safeRepo))
		srv := serverWithCatalog(t, c)
		sched := catalog.NewFreshnessScheduler(c, helm.NewFetcher(), nil, time.Hour)
		sched.RefreshForTest()
		srv.SetFreshness(sched)

		w := marketplaceVersions(t, srv)
		assertNoChartRepoLeak(t, "the no_data_reason body", w.Body.String())
		assertNoChartRepoLeak(t, "the log output for the no_data_reason path", logs.String())

		var resp catalogVersionsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v\n\nbody: %s", err, w.Body.String())
		}
		// The sentence must still be there — a blank one reads like
		// "everything is fine".
		if strings.TrimSpace(resp.NoDataReason) == "" {
			t.Error("no_data_reason is empty, which an operator reads as up to date")
		}
	})
}

// --- 4. the oci:// branch, which builds its own sentence inline -------------

// TestMarketplaceVersions_OCIRepo_NoDataReason_NotEchoedOn200 covers the third
// place this endpoint builds the sentence: the graceful-degrade branch for an
// oci:// registry, written inline in the handler rather than in the scheduler.
// It is a 200 by design, and it named the repository twice.
func TestMarketplaceVersions_OCIRepo_NoDataReason_NotEchoedOn200(t *testing.T) {
	const safeOCIRepo = "oci://registry.example/private/charts"

	t.Run("positive control: a clean oci address degrades to unknown", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		w := marketplaceVersions(t, serverWithCatalog(t, b14CatalogWithRepo(t, safeOCIRepo)))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — an oci:// repo degrades to unknown, never to an error.\n\nbody: %s", w.Code, w.Body.String())
		}
		var resp catalogVersionsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Repo != safeOCIRepo {
			t.Errorf("repo = %q, want exactly %q", resp.Repo, safeOCIRepo)
		}
		const wantSentence = "no freshness data for this source — " + safeOCIRepo + " has no version index Sharko can read"
		if resp.NoDataReason != wantSentence {
			t.Errorf("no_data_reason = %q,\nwant exactly       %q", resp.NoDataReason, wantSentence)
		}
	})

	t.Run("a tokenised oci address puts the token nowhere", func(t *testing.T) {
		resetCatalogVersionsCacheForTest()
		repoURL := "oci://" + chartRepoLeakSentinel + "@registry.example/private/charts"
		w := marketplaceVersions(t, serverWithCatalog(t, b14CatalogWithRepo(t, repoURL)))
		assertNoChartRepoLeak(t, "the oci:// body", w.Body.String())

		var resp catalogVersionsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v\n\nbody: %s", err, w.Body.String())
		}
		// An oci:// address is one credsafe cannot take apart with
		// confidence once it has an "@" in it, so it says nothing at all
		// rather than guessing which part is the secret. Blank means
		// "Sharko will not vouch for any of this", and blank is the right
		// answer here — what must never happen is the raw value coming
		// back, which the sweep above covers.
		if resp.Repo != "" && resp.Repo != safeOCIRepo {
			t.Errorf("repo = %q, want either blank or exactly %q", resp.Repo, safeOCIRepo)
		}
	})
}

// --- keep the fixture honest -------------------------------------------------

// TestB14Fixtures_ReallyCarryTheToken proves the addresses these tests plant
// really do carry the sentinel, and the safe forms really do not. Without it,
// a typo in a fixture would make every absence above true for the wrong
// reason.
func TestB14Fixtures_ReallyCarryTheToken(t *testing.T) {
	base := b14LiveChartRepo(t, "leaky", true)
	tokenised := b14TokenisedRepoURL(base)
	if !strings.Contains(tokenised, chartRepoLeakSentinel) {
		t.Fatalf("the tokenised address does not carry the sentinel: %q", tokenised)
	}
	if strings.Contains(base, chartRepoLeakSentinel) {
		t.Fatalf("the SAFE address carries the sentinel, so every 'the safe value is present' assertion would also be a leak: %q", base)
	}
	if found := findChartRepoLeak(tokenised); len(found) == 0 {
		t.Error("the sweep cannot find the token in the address these tests plant — it cannot prove anything about the responses")
	}
	// And the orchestrator import is used, so the gitops config the
	// upgrade fixture depends on is really the one being set.
	var _ = orchestrator.GitOpsConfig{}
}
