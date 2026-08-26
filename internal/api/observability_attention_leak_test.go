package api

// observability_attention_leak_test.go — the proof for B10.
//
// # What is being proved
//
// Three more ordinary 200 responses copied ArgoCD's own free-form strings
// into their JSON bodies. None of them is an error path; each is a page a
// person opens.
//
//   - GET /api/v1/observability/overview put every models.AppResource
//     straight onto models.AddonClusterHealth, and AppResource.Message is
//     the text ArgoCD's health assessment wrote about one Kubernetes object.
//     It travelled for every resource of every addon on every cluster, every
//     time the page opened.
//   - GET /api/v1/dashboard/attention put an ArgoCD application condition's
//     message into `error`, and the browser renders that string. A
//     ComparisonError condition is the one that says the repository was not
//     accessible and then names the repository — token and all.
//   - GET /api/v1/addons/{name} put an ArgoCD ApplicationSet condition's
//     message into `message`. An ApplicationSet's generator holds the
//     repository it templates from.
//
// # How it is proved
//
// The sentinel, the sweep and the positive control are B4's, reused through
// init_status_leak_test.go — same forms, same
// TestInitLeakSweep_FindsAPlantedSentinel proving the finder finds a planted
// secret before anything trusts an absence. The sentinel is planted in the
// ArgoCD data itself and the REAL handlers are driven through the REAL
// router against local ArgoCD and Gitea servers.
//
// # Why each row also asserts a PRESENCE
//
// Every leak assertion is an absence, and a handler that refused early
// produces the same absence while the line under test never runs. So each
// endpoint also has to come back with the fixed sentence IN it, pinned by a
// literal typed in this file rather than by the constant the server used.
//
// And a second kind of presence: a SECOND application whose ArgoCD values
// are all ordinary, well-known words. Its assertions prove the pages stayed
// useful — the condition type, the sync word, the health word, the resource
// kind/name/namespace all still come back — because a fix that blanked every
// field would pass every leak sweep above and take the operator's answer
// with it.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// obsLeakCluster is the cluster both applications are deployed to.
const obsLeakCluster = "leak-cluster"

// obsLeakTokenURL is the threat in its real shape, reusing B4's sentinel so
// the sweep already knows every form of it.
const obsLeakTokenURL = initLeakRepoURL

// obsLeakSafeURL is what SafeRepoURL leaves of it.
const obsLeakSafeURL = initLeakRepoURLSafe

// --- the fixtures -----------------------------------------------------------

// obsLeakGitServer serves just enough Gitea for the real GiteaProvider to
// read the catalog the addon detail and the observability overview need.
// There is no engine pin file, so both read the v3 catalog path.
func obsLeakGitServer(t *testing.T) *httptest.Server {
	t.Helper()
	managed := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: ` + obsLeakCluster + `
      region: us-east-1
      labels:
        keda: enabled
`
	catalog := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: keda
      repoURL: ` + obsLeakTokenURL + `
      chart: keda
      version: "2.13.0"
      namespace: keda
    - name: cert-manager
      repoURL: https://charts.example/cert-manager
      chart: cert-manager
      version: "1.14.0"
      namespace: cert-manager
`
	files := map[string]string{
		"configuration/managed-clusters.yaml": managed,
		"configuration/addons-catalog.yaml":   catalog,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/v1/version") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
			return
		}
		for path, body := range files {
			if strings.HasSuffix(r.URL.Path, path) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// obsLeakArgocdServer serves two applications and one ApplicationSet.
//
// keda-<cluster> carries the sentinel in every free-form place ArgoCD has:
// the repository it syncs from, a resource's health message, an application
// condition's message, and — so the allow-lists are exercised too — the
// status words and the condition type themselves.
//
// cert-manager-<cluster> carries nothing but ordinary ArgoCD values, and is
// what proves the pages still say something useful.
func obsLeakArgocdServer(t *testing.T) *httptest.Server {
	t.Helper()

	dirty := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "keda-" + obsLeakCluster, "namespace": "argocd"},
		"spec": map[string]interface{}{
			"project": "default",
			"source": map[string]interface{}{
				"repoURL":        obsLeakTokenURL,
				"targetRevision": "2.13.0",
			},
			"destination": map[string]interface{}{"server": "https://" + obsLeakCluster + ".example", "namespace": "keda"},
		},
		"status": map[string]interface{}{
			"sync":   map[string]interface{}{"status": "OutOfSync " + initLeakSentinel},
			"health": map[string]interface{}{"status": "Degraded " + initLeakSentinel},
			"operationState": map[string]interface{}{
				"phase":   "Failed",
				"message": "failed to fetch " + obsLeakTokenURL,
			},
			"conditions": []map[string]interface{}{{
				"type":    "ComparisonError " + initLeakSentinel,
				"message": "rpc error: repository not accessible: authentication required " + obsLeakTokenURL,
			}},
			"resources": []map[string]interface{}{{
				"group":     "apps",
				"kind":      "Deployment",
				"namespace": "keda",
				"name":      "keda-operator",
				"status":    "OutOfSync " + initLeakSentinel,
				"health": map[string]interface{}{
					"status":  "Degraded " + initLeakSentinel,
					"message": "failed to pull chart from " + obsLeakTokenURL,
				},
			}},
		},
	}

	clean := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "cert-manager-" + obsLeakCluster, "namespace": "argocd"},
		"spec": map[string]interface{}{
			"project": "default",
			"source": map[string]interface{}{
				"repoURL":        "https://charts.example/cert-manager",
				"targetRevision": "1.14.0",
			},
			"destination": map[string]interface{}{"server": "https://" + obsLeakCluster + ".example", "namespace": "cert-manager"},
		},
		"status": map[string]interface{}{
			"sync":   map[string]interface{}{"status": "OutOfSync"},
			"health": map[string]interface{}{"status": "Degraded"},
			"conditions": []map[string]interface{}{{
				"type":    "ComparisonError",
				"message": "an ordinary comparison error with nothing secret in it",
			}},
			"resources": []map[string]interface{}{
				{
					"kind":      "Service",
					"namespace": "cert-manager",
					"name":      "cert-manager-webhook",
					"status":    "Synced",
					"health":    map[string]interface{}{"status": "Healthy"},
				},
				{
					"group":     "apps",
					"kind":      "Deployment",
					"namespace": "cert-manager",
					"name":      "cert-manager",
					"status":    "Synced",
					"health": map[string]interface{}{
						"status":  "Degraded",
						"message": "Deployment does not have minimum availability",
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/api/v1/applicationsets/"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"metadata": map[string]interface{}{"name": "keda"},
				"status": map[string]interface{}{
					"conditions": []map[string]interface{}{{
						"type":    "ErrorOccurred " + initLeakSentinel,
						"status":  "True " + initLeakSentinel,
						"message": "error generating params: failed to read " + obsLeakTokenURL,
					}},
					"applicationStatus": []map[string]interface{}{{"application": "keda-" + obsLeakCluster}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/api/v1/applications"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{dirty, clean},
			})
		case strings.HasSuffix(r.URL.Path, "/api/v1/clusters"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{{
					"name":   obsLeakCluster,
					"server": "https://" + obsLeakCluster + ".example",
					"info": map[string]interface{}{
						"connectionState": map[string]interface{}{"status": "Successful"},
					},
				}},
			})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// obsLeakServer wires both local servers into a real connection saved
// through the real config store, so every handler builds a real Gitea
// provider and a real ArgoCD client the way it does in production.
func obsLeakServer(t *testing.T) *Server {
	t.Helper()
	git := obsLeakGitServer(t)
	argo := obsLeakArgocdServer(t)

	store := config.NewFileStore(t.TempDir() + "/obs-leak-config.yaml")
	if err := store.SaveConnection(models.Connection{
		Name: "obs-leak",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  git.URL + "/sharko-org/addons.git",
			Owner:    "sharko-org",
			Repo:     "addons",
			Token:    "a-token-that-is-not-empty",
		},
		Argocd: models.ArgocdConfig{ServerURL: argo.URL, Token: "an-argocd-token"},
	}); err != nil {
		t.Fatalf("saving the test connection: %v", err)
	}
	if err := store.SetActiveConnection("obs-leak"); err != nil {
		t.Fatalf("activating the test connection: %v", err)
	}

	srv := newTestServer()
	srv.connSvc = service.NewConnectionService(store)
	return srv
}

// obsLeakGet drives one real endpoint through the real router and returns
// the body plus every log line the request produced.
func obsLeakGet(t *testing.T, srv *Server, path string) (body, logs string) {
	t.Helper()
	router := NewRouter(srv, nil)
	logs = captureSlog(t, func() {
		req := withRole(httptest.NewRequest(http.MethodGet, path, nil), "admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s expected 200 — anything else means the endpoint refused before the code under test ran. got %d, body %s",
				path, w.Code, w.Body.String())
		}
		body = w.Body.String()
	})
	return body, logs
}

// --- the sweeps -------------------------------------------------------------

// The three fixed sentences, typed out as literals here rather than
// referenced through the credsafe constants the server assigned. A test that
// compares the server's constant against itself passes whatever the constant
// says, including a rewrite that puts ArgoCD's words back.
const (
	wantResourceSentence  = "ArgoCD reported something about this resource — open the application in ArgoCD to read it. Sharko does not repeat the text here, because a health message quotes the Kubernetes object's own words in full."
	wantConditionSentence = "ArgoCD reported a problem with this application — open it in ArgoCD to read the full condition. Sharko does not repeat ArgoCD's own condition text here, because that text quotes the repository address and the credentials layer's words in full."
	wantAppSetSentence    = "ArgoCD reported a problem with this addon's ApplicationSet — open it in ArgoCD to read the full condition. Sharko does not repeat ArgoCD's own condition text here, because that text quotes the repository address the generator was reading."
)

// TestObservabilityOverviewLeak_NeverShowsTheRepositoryToken is B10's first
// named site: models.AppResource.Message on the observability overview.
func TestObservabilityOverviewLeak_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := obsLeakServer(t)
	body, logs := obsLeakGet(t, srv, "/api/v1/observability/overview")

	assertNoInitLeak(t, "the observability overview 200 body", body)
	assertNoInitLeak(t, "the log output for the observability overview endpoint", logs)

	// --- the presence half -------------------------------------------------

	var resp models.ObservabilityOverviewResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decoding the overview body: %v\n\n%s", err, body)
	}
	if len(resp.AddonHealth) == 0 {
		t.Fatal("no addon health came back — the overview never ran and every absence above is an absence of nothing")
	}

	keda := findObsCluster(t, resp, "keda")
	if len(keda.Resources) != 1 {
		t.Fatalf("keda came back with %d resources, want 1 — the resource branch under test never ran", len(keda.Resources))
	}
	r := keda.Resources[0]

	// The resource is still fully named. Blanking it would pass every sweep
	// above and take the operator's answer with it.
	if r.Kind != "Deployment" || r.Name != "keda-operator" || r.Namespace != "keda" || r.Group != "apps" {
		t.Errorf("the resource is no longer identifiable: group=%q kind=%q ns=%q name=%q", r.Group, r.Kind, r.Namespace, r.Name)
	}
	if !strings.HasPrefix(r.Message, wantResourceSentence) {
		t.Errorf("resource message is\n  %q\nwant it to start with exactly\n  %q", r.Message, wantResourceSentence)
	}
	for _, gone := range []string{"failed to pull chart"} {
		if strings.Contains(r.Message, gone) {
			t.Errorf("resource message still carries ArgoCD's own words (%q): %q", gone, r.Message)
		}
	}
	// The status words ArgoCD sent are not ArgoCD's words any more.
	if r.Status != credsafe.Unrecognised || r.Health != credsafe.Unrecognised {
		t.Errorf("resource status/health are %q/%q — an ArgoCD word Sharko does not know must not be echoed", r.Status, r.Health)
	}
	if keda.Health != credsafe.Unrecognised {
		t.Errorf("the per-cluster health is %q — an ArgoCD word Sharko does not know must not be echoed", keda.Health)
	}

	// --- and the page is still useful --------------------------------------

	cm := findObsCluster(t, resp, "cert-manager")
	if cm.Health != "Degraded" {
		t.Errorf("the ordinary application's health is %q, want %q — a known ArgoCD word must still travel", cm.Health, "Degraded")
	}
	if len(cm.Resources) != 2 {
		t.Fatalf("the ordinary application came back with %d resources, want 2", len(cm.Resources))
	}
	var healthy, degraded *models.AppResource
	for i := range cm.Resources {
		switch cm.Resources[i].Kind {
		case "Service":
			healthy = &cm.Resources[i]
		case "Deployment":
			degraded = &cm.Resources[i]
		}
	}
	if healthy == nil || degraded == nil {
		t.Fatalf("the ordinary application's resources are not the two that were planted: %+v", cm.Resources)
	}
	if healthy.Message != "" {
		t.Errorf("a resource ArgoCD said nothing about came back with %q — an empty message and a replaced one are two different facts", healthy.Message)
	}
	if healthy.Status != "Synced" || healthy.Health != "Healthy" {
		t.Errorf("the healthy resource's words are %q/%q, want Synced/Healthy", healthy.Status, healthy.Health)
	}
	if !strings.HasPrefix(degraded.Message, wantResourceSentence) {
		t.Errorf("the degraded resource's message is %q, want it to start with the fixed sentence", degraded.Message)
	}
	if strings.Contains(degraded.Message, "minimum availability") {
		t.Errorf("the degraded resource still carries ArgoCD's own words: %q", degraded.Message)
	}
}

// findObsCluster pulls one addon's single per-cluster health record out of
// the overview response, failing loudly rather than returning a zero value —
// a zero value would make every assertion below it meaningless.
func findObsCluster(t *testing.T, resp models.ObservabilityOverviewResponse, addon string) models.AddonClusterHealth {
	t.Helper()
	for _, d := range resp.AddonHealth {
		if d.AddonName != addon {
			continue
		}
		if len(d.Clusters) != 1 {
			t.Fatalf("addon %q came back with %d clusters, want 1", addon, len(d.Clusters))
		}
		return d.Clusters[0]
	}
	t.Fatalf("addon %q is missing from the overview — the application was never matched, so the fields under test were never set", addon)
	return models.AddonClusterHealth{}
}

// TestDashboardAttentionLeak_NeverShowsTheRepositoryToken is B10's second
// named site: an ArgoCD application condition's message on the Dashboard's
// needs-attention feed, which the browser renders.
func TestDashboardAttentionLeak_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := obsLeakServer(t)
	body, logs := obsLeakGet(t, srv, "/api/v1/dashboard/attention")

	assertNoInitLeak(t, "the dashboard attention 200 body", body)
	assertNoInitLeak(t, "the log output for the dashboard attention endpoint", logs)

	// --- the presence half -------------------------------------------------

	var items []AttentionItem
	if err := json.Unmarshal([]byte(body), &items); err != nil {
		t.Fatalf("decoding the attention body: %v\n\n%s", err, body)
	}
	if len(items) != 2 {
		t.Fatalf("the feed came back with %d items, want 2 — the loop under test never ran over both applications.\n\n%s", len(items), body)
	}

	var dirty, clean *AttentionItem
	for i := range items {
		switch items[i].AddonName {
		case "keda":
			dirty = &items[i]
		case "cert-manager":
			clean = &items[i]
		}
	}
	if dirty == nil || clean == nil {
		t.Fatalf("the feed is not the two applications that were planted: %+v", items)
	}

	if !strings.HasPrefix(dirty.Error, wantConditionSentence) {
		t.Errorf("error is\n  %q\nwant it to start with exactly\n  %q", dirty.Error, wantConditionSentence)
	}
	if !strings.Contains(dirty.Error, "repo="+obsLeakSafeURL) {
		t.Errorf("error no longer names the repository, so the operator lost the one fact that identifies it: %q", dirty.Error)
	}
	for _, gone := range []string{"rpc error", "repository not accessible", "authentication required"} {
		if strings.Contains(dirty.Error, gone) {
			t.Errorf("error still carries ArgoCD's own words (%q): %q", gone, dirty.Error)
		}
	}
	if dirty.ErrorType != credsafe.Unrecognised {
		t.Errorf("error_type is %q — an ArgoCD condition type Sharko does not know must not be echoed", dirty.ErrorType)
	}
	if dirty.Health != credsafe.Unrecognised || dirty.Sync != credsafe.Unrecognised {
		t.Errorf("health/sync are %q/%q — an ArgoCD word Sharko does not know must not be echoed", dirty.Health, dirty.Sync)
	}
	// The app is still named, which is how a person finds it.
	if dirty.AppName != "keda-"+obsLeakCluster || dirty.Cluster != obsLeakCluster {
		t.Errorf("the failing application is no longer identifiable: app=%q cluster=%q", dirty.AppName, dirty.Cluster)
	}

	// --- and the page is still useful --------------------------------------

	if clean.ErrorType != "ComparisonError" {
		t.Errorf("error_type is %q, want %q — a condition type is a closed set and must still travel, or the row says nothing about WHAT went wrong", clean.ErrorType, "ComparisonError")
	}
	if clean.Health != "Degraded" || clean.Sync != "OutOfSync" {
		t.Errorf("health/sync are %q/%q, want Degraded/OutOfSync — known ArgoCD words must still travel", clean.Health, clean.Sync)
	}
	if !strings.HasPrefix(clean.Error, wantConditionSentence) {
		t.Errorf("the ordinary application's error is %q, want it to start with the fixed sentence", clean.Error)
	}
	if strings.Contains(clean.Error, "an ordinary comparison error") {
		t.Errorf("the ordinary application still carries ArgoCD's own condition text: %q", clean.Error)
	}
}

// TestAddonDetailLeak_NeverShowsTheRepositoryToken is the site B10's brief
// did not name, found by widening the search to every field whose value comes
// straight from an ArgoCD object: an ApplicationSet condition's message.
func TestAddonDetailLeak_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := obsLeakServer(t)
	body, logs := obsLeakGet(t, srv, "/api/v1/addons/keda")

	assertNoInitLeak(t, "the addon detail 200 body", body)
	assertNoInitLeak(t, "the log output for the addon detail endpoint", logs)

	// --- the presence half -------------------------------------------------

	var resp models.AddonDetailResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decoding the addon detail body: %v\n\n%s", err, body)
	}
	if resp.ApplicationSet == nil {
		t.Fatal("no ApplicationSet came back — the enrich branch never ran and every absence above is an absence of nothing")
	}
	if len(resp.ApplicationSet.Conditions) != 1 {
		t.Fatalf("the ApplicationSet came back with %d conditions, want 1", len(resp.ApplicationSet.Conditions))
	}
	c := resp.ApplicationSet.Conditions[0]
	if c.Message != wantAppSetSentence {
		t.Errorf("the ApplicationSet condition message is\n  %q\nwant exactly\n  %q", c.Message, wantAppSetSentence)
	}
	if strings.Contains(c.Message, "error generating params") {
		t.Errorf("the ApplicationSet condition still carries ArgoCD's own words: %q", c.Message)
	}
	if c.Type != credsafe.Unrecognised || c.Status != credsafe.Unrecognised {
		t.Errorf("the condition type/status are %q/%q — an ArgoCD word Sharko does not know must not be echoed", c.Type, c.Status)
	}
	// The ApplicationSet is still named and still counted.
	if resp.ApplicationSet.Name != "keda" || resp.ApplicationSet.GeneratedApps != 1 {
		t.Errorf("the ApplicationSet is no longer identifiable: name=%q generated=%d", resp.ApplicationSet.Name, resp.ApplicationSet.GeneratedApps)
	}

	// --- the two the sweep found that the brief did not name ---------------
	//
	// The addon's own repo_url came straight off Sharko's catalog file, and
	// the AddonDetail page renders it as a clickable, copyable link. The
	// per-cluster rows echoed ArgoCD's sync and health words raw.
	if resp.Addon.RepoURL != obsLeakSafeURL {
		t.Errorf("addon repo_url is\n  %q\nwant exactly\n  %q", resp.Addon.RepoURL, obsLeakSafeURL)
	}
	if len(resp.Addon.Applications) != 1 {
		t.Fatalf("the addon came back with %d per-cluster rows, want 1", len(resp.Addon.Applications))
	}
	app := resp.Addon.Applications[0]
	if app.SyncStatus != credsafe.Unrecognised || app.HealthStatus != credsafe.Unrecognised {
		t.Errorf("the per-cluster sync/health are %q/%q — an ArgoCD word Sharko does not know must not be echoed", app.SyncStatus, app.HealthStatus)
	}
	if app.ApplicationName != "keda-"+obsLeakCluster || app.ConfiguredVersion != "2.13.0" {
		t.Errorf("the per-cluster row is no longer useful: app=%q version=%q", app.ApplicationName, app.ConfiguredVersion)
	}
	if app.Status != "sync_failing" {
		t.Errorf("status is %q, want %q — Sharko's own classification still reads the RAW ArgoCD values server-side", app.Status, "sync_failing")
	}
}

// TestObsLeakSweep_GoesRedWhenNothingIsPlanted is the control on the control.
//
// If the fixtures stopped carrying the sentinel — somebody "tidied" a
// constant, a fixture drifted — every assertion above would still pass,
// having proved nothing.
func TestObsLeakSweep_GoesRedWhenNothingIsPlanted(t *testing.T) {
	if !strings.Contains(obsLeakTokenURL, initLeakSentinel) {
		t.Fatal("the tokenised URL the fixtures plant no longer carries the sentinel — the sweeps are proving nothing")
	}
	if strings.Contains(obsLeakSafeURL, initLeakSentinel) {
		t.Fatal("the safe URL carries the sentinel, so every assertion that the safe URL is present would also be a leak")
	}
	clean := `{"error":"` + wantConditionSentence + `"}`
	if found := findInitLeak(clean); len(found) != 0 {
		t.Errorf("the sweep fired on a clean body, naming %v", found)
	}
	dirty := `{"message":"failed to pull chart from ` + obsLeakTokenURL + `"}`
	if found := findInitLeak(dirty); len(found) == 0 {
		t.Error("the sweep did NOT find the raw repository URL planted in a resource health message — it cannot prove anything about the real one")
	}
}

// TestB10Sentences_ArePinnedAndDistinct pins each new sentence against a
// literal typed here, and holds the same line
// TestComparisonGuard_SentencesAreNotEachOther holds for B7/B8: making three
// fields safe must not make them the same field. A reader sent to the
// ApplicationSet when a RESOURCE is what is broken has been given a worse
// answer than the leak was.
func TestB10Sentences_ArePinnedAndDistinct(t *testing.T) {
	pinned := map[string][2]string{
		"the resource health message":  {credsafe.ArgocdResourceMessage, wantResourceSentence},
		"the application condition":    {credsafe.ArgocdAppConditionMessage, wantConditionSentence},
		"the ApplicationSet condition": {credsafe.ArgocdAppSetConditionMessage, wantAppSetSentence},
	}
	seen := map[string]string{}
	for name, pair := range pinned {
		if pair[0] != pair[1] {
			t.Errorf("%s is\n  %q\nwant exactly\n  %q", name, pair[0], pair[1])
		}
		if other, dup := seen[pair[0]]; dup {
			t.Errorf("%s says exactly what %s says — these send a reader to different places and must stay different answers", name, other)
		}
		seen[pair[0]] = name
	}
	// Distinct from the B7/B8 sentences too.
	for name, s := range map[string]string{
		"the addon sync failure": credsafe.ArgocdSyncFailureMessage,
		"the cluster connection": credsafe.ArgocdClusterConnectionFailureMessage,
		"the connectivity check": credsafe.ArgocdCheckFailureMessage,
		"the short badge line":   credsafe.ArgocdSyncFailureShort,
	} {
		if other, dup := seen[s]; dup {
			t.Errorf("%s says exactly what %s says", name, other)
		}
		seen[s] = name
	}
}

// TestSafeReportedDetail_KeepsSilenceSilent holds the one thing that
// separates SafeReportedDetail from SafeOperationDetail: "ArgoCD said
// nothing" must not become a sentence about a problem.
func TestSafeReportedDetail_KeepsSilenceSilent(t *testing.T) {
	if got := credsafe.SafeReportedDetail(false, credsafe.ArgocdResourceMessage, credsafe.OperationFacts{
		HealthStatus: "Healthy",
	}); got != "" {
		t.Errorf("a resource ArgoCD said nothing about got %q, want the empty string", got)
	}
	got := credsafe.SafeReportedDetail(true, credsafe.ArgocdResourceMessage, credsafe.OperationFacts{
		HealthStatus: "Degraded",
	})
	if got != wantResourceSentence+" (health=Degraded)" {
		t.Errorf("got %q, want the fixed sentence plus the one fact that was reported", got)
	}
	// And it cannot be used to print arbitrary text either.
	arbitrary := credsafe.SafeReportedDetail(true, "here is the raw error: "+initLeakRepoURL, credsafe.OperationFacts{})
	assertNoInitLeak(t, "SafeReportedDetail handed an arbitrary sentence", arbitrary)
}

// TestResyncMessage_NeverRepeatsTheReconcilersRawText covers the leak this
// story's own widening found, on a third endpoint the brief did not name.
//
// POST /api/v1/clusters/{name}/resync built its 200 body's `message` from
// clusterreconciler.ResyncResult.Message, which is the reconciler's RAW
// per-cluster record text. Several of the call sites that write that record
// build it by appending a Kubernetes API or git-provider error straight onto
// a fixed English prefix, so the raw error reached the body. Every OTHER
// reader of that same record — Reconciler.LastError, applyLastReconcile,
// connectionSecretCheckError — maps it through FailureSentence first. This
// one path did not.
func TestResyncMessage_NeverRepeatsTheReconcilersRawText(t *testing.T) {
	raw := "Sharko couldn't converge Git-desired addon labels on this drifted managed-cluster Secret: " +
		"Get \"" + initLeakRepoURL + "\": dial tcp: i/o timeout"

	got := resyncMessage("prod-eu", clusterreconciler.ResyncResult{
		Outcome: clusterreconciler.OutcomeFailed,
		Message: raw,
	})
	assertNoInitLeak(t, "the resync 200 body's message", got)

	// The presence half: it still says which cluster, and it still says what
	// to do. A blanked message would pass the sweep above and help nobody.
	const wantMapped = "Sharko tried to fix this cluster's connection secret and the write failed. Click Refresh to try again."
	if !strings.Contains(got, wantMapped) {
		t.Errorf("the message is\n  %q\nwant it to contain exactly\n  %q", got, wantMapped)
	}
	if !strings.Contains(got, `"prod-eu"`) {
		t.Errorf("the message no longer names the cluster: %q", got)
	}
	if !strings.Contains(got, "The self-heal setting was not changed.") {
		t.Errorf("the message dropped the self-heal promise this action's contract requires: %q", got)
	}

	// A skipped record carries one of the reconciler's own fixed sentences
	// and is deliberately NOT mapped — mapping an already-safe sentence a
	// second time collapses it into the generic fallback.
	skipped := resyncMessage("prod-eu", clusterreconciler.ResyncResult{
		Outcome: clusterreconciler.OutcomeSkipped,
		Message: clusterreconciler.ManagedSecretNotCreatedMessage,
	})
	if !strings.Contains(skipped, clusterreconciler.ManagedSecretNotCreatedMessage) {
		t.Errorf("a skipped resync lost its own specific reason: %q", skipped)
	}
}
