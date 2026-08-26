package api

// cluster_comparison_leak_test.go — the proof for B7 and B8, and the guard
// that stops the next field being added the same way.
//
// # What is being proved
//
// GET /api/v1/clusters/{name}/comparison copies ArgoCD's own strings into its
// JSON body. Not on an error path — on the ordinary, successful 200 that the
// cluster detail screen calls every time it opens. Three of those strings
// carried credential material:
//
//   - argocd_source_repo_url was spec.source.repoURL, verbatim. A Git
//     repository address is routinely registered with the token inside it
//     (https://x-access-token:<token>@host/org/repo.git), and ArgoCD stores
//     whatever it was given.
//   - argocd_operation_message was operationState.message, verbatim, capped at
//     4000 characters. ArgoCD's message quotes the repository it was syncing
//     from and quotes provider errors word for word.
//   - argocd_connection_message was the cluster's connectionMessage, which is
//     the credentials layer's own words.
//
// and two more of the same shape were found by widening the search:
// connectivity_detail (models.Cluster, set from the same operationState.message
// via buildCheckFailDetail) and git_repo_url (a chart repository address, which
// is a repository address whoever wrote it).
//
// Nothing had to go wrong for any of them to travel.
//
// # How it is proved
//
// The sentinel, the sweep and the positive control are B4's, reused from
// init_status_leak_test.go — same package, same forms, same
// TestInitLeakSweep_FindsAPlantedSentinel proving the finder finds a planted
// secret BEFORE anything trusts an absence. Writing a third sweep would mean
// three of them drifting apart.
//
// The sentinel is planted in the ArgoCD data itself — inside a repository URL,
// inside an operation message, inside a connection message, inside the status
// words — and the REAL endpoint is driven through the REAL router against a
// real Gitea provider and a real ArgoCD client, both pointed at local servers.
// The response body and every captured log line are then swept.
//
// # Why each row also asserts a PRESENCE
//
// Every leak assertion is an absence, and a handler that refused early — a 400
// on a missing cluster, a 403 on a role, a 502 because the connection would not
// build — produces the same absence while the line under test never runs. So
// the test also requires the fixed sentences to be IN the body. A short-circuit
// then fails loudly as "never reached the code under test" instead of passing
// as "no leak found".

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// comparisonLeakCluster is the cluster the endpoint is driven for.
const comparisonLeakCluster = "leak-cluster"

// comparisonLeakTokenURL is the threat in its real shape, reusing B4's
// sentinel so the sweep already knows every form of it.
const comparisonLeakTokenURL = initLeakRepoURL

// comparisonLeakSafeURL is what SafeRepoURL leaves of it.
const comparisonLeakSafeURL = initLeakRepoURLSafe

// --- the fixtures -----------------------------------------------------------

// comparisonLeakGitServer serves just enough Gitea for the real GiteaProvider
// to read the two configuration files the comparison needs. The chart
// repository URL in the catalog carries the sentinel too, because a chart
// repository address is a repository address.
func comparisonLeakGitServer(t *testing.T) *httptest.Server {
	t.Helper()
	managed := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: ` + comparisonLeakCluster + `
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
      repoURL: ` + comparisonLeakTokenURL + `
      chart: keda
      version: "2.13.0"
      namespace: keda
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

// comparisonLeakArgocdServer is an ArgoCD whose every free-form string carries
// the sentinel: the repository the addon syncs from, the destination server,
// the operation message, the cluster's connectionMessage, and — so the
// allow-listing of ArgoCD's status words is exercised too — the sync and health
// words themselves.
func comparisonLeakArgocdServer(t *testing.T) *httptest.Server {
	t.Helper()
	app := func(name, phase string) map[string]interface{} {
		return map[string]interface{}{
			"metadata": map[string]interface{}{"name": name, "namespace": "argocd"},
			"spec": map[string]interface{}{
				"project": "default",
				"source": map[string]interface{}{
					"repoURL":        comparisonLeakTokenURL,
					"targetRevision": "2.13.0",
					"path":           "charts/keda",
				},
				"destination": map[string]interface{}{
					"server":    comparisonLeakTokenURL,
					"namespace": "keda",
				},
			},
			"status": map[string]interface{}{
				"sync":   map[string]interface{}{"status": "OutOfSync " + initLeakSentinel},
				"health": map[string]interface{}{"status": "Degraded " + initLeakSentinel},
				"operationState": map[string]interface{}{
					"phase": phase,
					"message": "one or more synchronization tasks completed unsuccessfully, " +
						"reason: failed to fetch " + comparisonLeakTokenURL,
				},
			},
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/applications"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					app("keda-"+comparisonLeakCluster, "Failed"),
					app("connectivity-check-"+comparisonLeakCluster, "Failed"),
				},
			})
		case strings.HasSuffix(r.URL.Path, "/api/v1/clusters"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{{
					"name":   comparisonLeakCluster,
					"server": "https://" + comparisonLeakCluster + ".example",
					"info": map[string]interface{}{
						"connectionState": map[string]interface{}{
							"status":  "Failed",
							"message": "failed to load initial state: " + comparisonLeakTokenURL,
						},
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

// comparisonLeakServer wires both local servers into a real connection saved
// through the real config store, so the handler builds a real Gitea provider
// and a real ArgoCD client the way it does in production.
func comparisonLeakServer(t *testing.T) *Server {
	t.Helper()
	git := comparisonLeakGitServer(t)
	argo := comparisonLeakArgocdServer(t)

	store := config.NewFileStore(t.TempDir() + "/comparison-leak-config.yaml")
	if err := store.SaveConnection(models.Connection{
		Name: "comparison-leak",
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
	if err := store.SetActiveConnection("comparison-leak"); err != nil {
		t.Fatalf("activating the test connection: %v", err)
	}

	srv := newTestServer()
	srv.connSvc = service.NewConnectionService(store)
	return srv
}

// --- the sweep --------------------------------------------------------------

// TestClusterComparisonLeak_NeverShowsTheRepositoryToken drives the real
// endpoint and sweeps the whole 200 body and every log line.
func TestClusterComparisonLeak_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := comparisonLeakServer(t)
	router := NewRouter(srv, nil)

	var body string
	logs := captureSlog(t, func() {
		req := withRole(httptest.NewRequest(http.MethodGet,
			"/api/v1/clusters/"+comparisonLeakCluster+"/comparison", nil), "admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 — anything else means the endpoint refused before the code under test ran. got %d, body %s",
				w.Code, w.Body.String())
		}
		body = w.Body.String()
	})

	assertNoInitLeak(t, "the cluster-comparison 200 body", body)
	assertNoInitLeak(t, "the log output for the cluster-comparison endpoint", logs)

	// --- the presence half: prove the code under test actually ran ---------

	var resp models.ClusterComparisonResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decoding the comparison body: %v\n\n%s", err, body)
	}
	if len(resp.AddonComparisons) == 0 {
		t.Fatal("no addon comparisons came back — the comparison never ran and every absence above is an absence of nothing")
	}

	var keda *models.AddonComparisonStatus
	for i := range resp.AddonComparisons {
		if strings.HasPrefix(resp.AddonComparisons[i].AddonName, "keda") {
			keda = &resp.AddonComparisons[i]
		}
	}
	if keda == nil {
		t.Fatal("the keda comparison is missing — the ArgoCD application was never matched, so the fields under test were never set")
	}
	if !keda.ArgocdDeployed {
		t.Fatal("keda came back as not deployed — the ArgoCD branch that sets every field under test never ran")
	}

	// B7: the repository is still named, with the credential gone. Blanking it
	// would pass every sweep above and take the operator's answer with it.
	if keda.ArgocdSourceRepoURL != comparisonLeakSafeURL {
		t.Errorf("argocd_source_repo_url is\n  %q\nwant exactly\n  %q", keda.ArgocdSourceRepoURL, comparisonLeakSafeURL)
	}
	if keda.GitRepoURL != comparisonLeakSafeURL {
		t.Errorf("git_repo_url is\n  %q\nwant exactly\n  %q", keda.GitRepoURL, comparisonLeakSafeURL)
	}
	if keda.ArgocdDestinationServer != comparisonLeakSafeURL {
		t.Errorf("argocd_destination_server is\n  %q\nwant exactly\n  %q", keda.ArgocdDestinationServer, comparisonLeakSafeURL)
	}

	// B8: the operation message is Sharko's own sentence, pinned by a literal
	// typed here rather than by the constant the server assigned.
	const wantSyncSentence = "ArgoCD could not finish syncing this addon. Sharko does not repeat ArgoCD's own message here: that message quotes whatever ArgoCD was working on, which includes the repository address with its access token inside it. Open this application in ArgoCD to read the full error."
	if !strings.HasPrefix(keda.ArgocdOperationMessage, wantSyncSentence) {
		t.Errorf("argocd_operation_message is\n  %q\nwant it to start with exactly\n  %q", keda.ArgocdOperationMessage, wantSyncSentence)
	}
	if !strings.Contains(keda.ArgocdOperationMessage, "repo="+comparisonLeakSafeURL) {
		t.Errorf("argocd_operation_message no longer names the repository: %q", keda.ArgocdOperationMessage)
	}
	// And ArgoCD's own words are gone from it.
	for _, gone := range []string{"completed unsuccessfully", "failed to fetch"} {
		if strings.Contains(keda.ArgocdOperationMessage, gone) {
			t.Errorf("argocd_operation_message still carries ArgoCD's own words (%q): %q", gone, keda.ArgocdOperationMessage)
		}
	}

	const wantShort = "ArgoCD could not finish syncing this addon — open it in ArgoCD for the full error."
	if len(keda.Issues) != 1 || keda.Issues[0] != wantShort {
		t.Errorf("issues is\n  %q\nwant exactly one entry\n  %q", keda.Issues, wantShort)
	}

	// The status words ArgoCD sent are not ArgoCD's words any more.
	if keda.ArgocdSyncStatus != credsafe.Unrecognised {
		t.Errorf("argocd_sync_status is %q — an ArgoCD word Sharko does not know must not be echoed", keda.ArgocdSyncStatus)
	}
	if keda.ArgocdHealthStatus != credsafe.Unrecognised {
		t.Errorf("argocd_health_status is %q — an ArgoCD word Sharko does not know must not be echoed", keda.ArgocdHealthStatus)
	}

	// The cluster connection message is Sharko's sentence, not ArgoCD's.
	const wantConnSentence = "ArgoCD cannot reach this cluster. Sharko does not repeat ArgoCD's own message here, because it quotes the credentials layer's words in full. Open the cluster in ArgoCD to read the connection error."
	if resp.ArgocdConnectionMessage != wantConnSentence {
		t.Errorf("argocd_connection_message is\n  %q\nwant exactly\n  %q", resp.ArgocdConnectionMessage, wantConnSentence)
	}

	// And the connectivity detail, which lands on the embedded cluster.
	const wantCheckSentence = "Sharko's connectivity-check application is failing in ArgoCD. Sharko does not repeat ArgoCD's own message here, because that message quotes the repository address and the credentials layer's words in full. Open the connectivity-check application in ArgoCD to read the full error."
	if !strings.HasPrefix(resp.Cluster.ConnectivityDetail, wantCheckSentence) {
		t.Errorf("connectivity_detail is\n  %q\nwant it to start with exactly\n  %q", resp.Cluster.ConnectivityDetail, wantCheckSentence)
	}
}

// TestClusterComparisonLeak_SweepGoesRedWhenNothingIsPlanted is the control on
// the control.
//
// If the fixtures above stopped carrying the sentinel — somebody "tidied" a
// constant, a fixture drifted — every assertion in the sweep would still pass,
// having proved nothing. So the fixtures are checked for the sentinel here, and
// the finder is checked against a body with nothing planted in it.
func TestClusterComparisonLeak_SweepGoesRedWhenNothingIsPlanted(t *testing.T) {
	if !strings.Contains(comparisonLeakTokenURL, initLeakSentinel) {
		t.Fatal("the tokenised URL the fixtures plant no longer carries the sentinel — the sweep is proving nothing")
	}
	if strings.Contains(comparisonLeakSafeURL, initLeakSentinel) {
		t.Fatal("the safe URL carries the sentinel, so every assertion that the safe URL is present would also be a leak")
	}
	// A body with nothing planted in it must come back clean, or the sweep
	// fires on everything and a green run means nothing.
	clean := `{"argocd_operation_message":"` + credsafe.ArgocdSyncFailureMessage + `"}`
	if found := findInitLeak(clean); len(found) != 0 {
		t.Errorf("the sweep fired on a clean body, naming %v", found)
	}
	// And a body with the raw value planted must come back dirty. This is the
	// shape the break test (a) reproduces by hand.
	dirty := `{"argocd_source_repo_url":"` + comparisonLeakTokenURL + `"}`
	if found := findInitLeak(dirty); len(found) == 0 {
		t.Error("the sweep did NOT find the raw repository URL planted in a response body — it cannot prove anything about the real one")
	}
}

// --- the guard --------------------------------------------------------------

// providerSourcedField is one field on a response model, named, with what
// Sharko does about it.
type providerSourcedField struct {
	// field is the Go field name on the struct.
	field string
	// routed is true when the value goes through internal/credsafe on its way
	// onto the response; false means "safe as it stands, and why" is in reason.
	routed bool
	// reason must always be filled in. For a routed field it names the helper;
	// for an unrouted one it is the argument for why the raw value cannot
	// carry credential material.
	reason string
}

// comparisonGuard is the LIST. Every field on the two cluster-comparison
// response structs is named here exactly once, and each one is either routed
// through internal/credsafe or has a written reason why it is safe as it
// stands.
//
// This is the only thing that stops the next field being added the way
// argocd_source_repo_url was: somebody adds a string to the struct, copies an
// ArgoCD value into it, and nothing anywhere notices. The guard notices,
// because a field that is not in this list fails the test by name.
//
// It fails in BOTH directions on purpose. A field on the struct that is missing
// here fails as "unrouted new field". An entry here naming a field that no
// longer exists fails as "stale entry" — otherwise the list rots into a
// comforting fiction while the structs move on underneath it.
//
// B10 widened it past the comparison endpoint. It is still ONE list, because
// three lists drift apart: the observability overview's structs, the
// Dashboard's attention row and the addon detail's ApplicationSet status all
// carry ArgoCD-sourced values onto ordinary 200s, and every one of them is
// named here now.
var comparisonGuard = map[string][]providerSourcedField{
	"AddonComparisonStatus": {
		{field: "AddonName", reason: "Sharko's own addon name, read from its own catalog file — not a provider value"},
		{field: "GitConfigured", reason: "bool"},
		{field: "GitChart", reason: "a Helm chart NAME from Sharko's catalog — a bare identifier, no URL and no free-form text"},
		{field: "GitRepoURL", routed: true, reason: "credsafe.SafeRepoURL — a chart repository address is a repository address, whoever wrote it"},
		{field: "GitVersion", reason: "a chart version from Sharko's own catalog file"},
		{field: "GitNamespace", reason: "a Kubernetes namespace name from Sharko's own catalog file"},
		{field: "GitEnabled", reason: "bool"},
		{field: "EnvironmentVersion", reason: "a chart version from Sharko's own config"},
		{field: "CustomVersion", reason: "a chart version from Sharko's own config"},
		{field: "HasVersionOverride", reason: "bool"},
		{field: "ArgocdDeployed", reason: "bool"},
		{field: "ArgocdApplicationName", reason: "ArgoCD's metadata.name — a Kubernetes object name, so RFC 1123 characters only; it cannot hold a userinfo section and it is not free-form text"},
		{field: "ArgocdSyncStatus", routed: true, reason: "credsafe.SafeSyncStatus — echoed only when it is a value Sharko knows"},
		{field: "ArgocdHealthStatus", routed: true, reason: "credsafe.SafeHealthStatus — echoed only when it is a value Sharko knows"},
		{field: "ArgocdDeployedVersion", reason: "ArgoCD's spec.source.targetRevision — a Git ref or chart version; it is not a URL and it is not error text"},
		{field: "ArgocdNamespace", reason: "ArgoCD's spec.destination.namespace — a Kubernetes namespace name"},
		{field: "ArgocdSourceRepoURL", routed: true, reason: "credsafe.SafeRepoURL — this is B7"},
		{field: "ArgocdSourcePath", reason: "a path INSIDE the repository; the credential lives in the URL's userinfo, which a path has no room for"},
		{field: "ArgocdDestinationServer", routed: true, reason: "credsafe.SafeRepoURL — a URL on a response is a URL, and the response copy is not the value anything matches on"},
		{field: "ArgocdOperationState", routed: true, reason: "credsafe.SafeOperationPhase — echoed only when it is a value Sharko knows"},
		{field: "ArgocdOperationMessage", routed: true, reason: "credsafe.SafeOperationDetail — this is B8; ArgoCD's free-form message never travels"},
		{field: "Status", reason: "Sharko's own classification word, from a fixed set in classifyAddonApp"},
		{field: "Issues", routed: true, reason: "credsafe.ArgocdSyncFailureShort — it used to carry the first line of ArgoCD's operation message"},
		{field: "LastSyncTime", reason: "an RFC 3339 timestamp"},
		{field: "CreatedAt", reason: "an RFC 3339 timestamp"},
	},
	"ClusterComparisonResponse": {
		{field: "Cluster", reason: "models.Cluster, guarded separately below"},
		{field: "GitTotalAddons", reason: "int"},
		{field: "GitEnabledAddons", reason: "int"},
		{field: "GitDisabledAddons", reason: "int"},
		{field: "ArgocdTotalApplications", reason: "int"},
		{field: "ArgocdHealthyApplications", reason: "int"},
		{field: "ArgocdSyncedApplications", reason: "int"},
		{field: "ArgocdDegradedApplications", reason: "int"},
		{field: "ArgocdOutOfSyncApplications", reason: "int"},
		{field: "AddonComparisons", reason: "[]AddonComparisonStatus, guarded above"},
		{field: "TotalHealthy", reason: "int"},
		{field: "TotalWithIssues", reason: "int"},
		{field: "TotalMissingInArgocd", reason: "int"},
		{field: "TotalUntrackedInArgocd", reason: "int"},
		{field: "TotalDisabledInGit", reason: "int"},
		{field: "ClusterConnectionState", routed: true, reason: "credsafe.SafeConnectionState — echoed only when it is a value Sharko knows"},
		{field: "ArgocdConnectionStatus", routed: true, reason: "credsafe.SafeConnectionState — same value, same helper"},
		{field: "ArgocdConnectionMessage", routed: true, reason: "credsafe.ArgocdClusterConnectionFailureMessage — ArgoCD's own connectionMessage never travels"},
	},

	// --- B10: GET /observability/overview ---------------------------------

	"ObservabilityOverviewResponse": {
		{field: "ControlPlane", reason: "ControlPlaneInfo, guarded below"},
		{field: "RecentSyncs", reason: "[]SyncActivityEntry, guarded below"},
		{field: "AddonHealth", reason: "[]AddonHealthDetail, guarded below"},
		{field: "AddonGroups", reason: "[]AddonGroupHealth, guarded below"},
		{field: "ResourceAlerts", reason: "[]ResourceAlert, guarded below"},
	},
	"ControlPlaneInfo": {
		{field: "ArgocdVersion", reason: "a version string from ArgoCD's own /version endpoint — a semver, not a URL and not error text"},
		{field: "HelmVersion", reason: "a version string from the same /version endpoint"},
		{field: "KubectlVersion", reason: "a version string from the same /version endpoint"},
		{field: "TotalApps", reason: "int"},
		{field: "TotalClusters", reason: "int"},
		{field: "ConfiguredClusters", reason: "int"},
		{field: "ConfiguredClustersAvailable", reason: "bool"},
		{field: "ConnectedClusters", reason: "int"},
		{field: "TotalAppSets", reason: "int"},
		{field: "HealthSummary", reason: "a count per ArgoCD health word. It is a MAP KEYED by the raw word, not a value Sharko writes — reported as an adjacent finding rather than changed here, because rewriting the keys changes what every chart counts"},
	},
	"SyncActivityEntry": {
		{field: "Timestamp", reason: "an RFC 3339 timestamp from an ArgoCD history entry"},
		{field: "Duration", reason: "Sharko's own formatted duration"},
		{field: "DurationSecs", reason: "float"},
		{field: "AppName", reason: "ArgoCD's metadata.name — a Kubernetes object name, RFC 1123 characters only"},
		{field: "AddonName", reason: "Sharko's own split of the app name"},
		{field: "ClusterName", reason: "Sharko's own split of the app name"},
		{field: "Revision", reason: "an ArgoCD history entry's revision — a Git SHA or a chart version; it is not a URL and it is not error text"},
		{field: "Status", reason: "the literal \"Succeeded\", written by buildRecentSyncs — a completed history entry is a completed deploy"},
		{field: "Action", reason: "Sharko's own word, models.SyncActionInstalled or models.SyncActionUpdated"},
	},
	"AddonHealthDetail": {
		{field: "AddonName", reason: "Sharko's own split of the ArgoCD app name"},
		{field: "TotalClusters", reason: "int"},
		{field: "HealthyClusters", reason: "int"},
		{field: "DegradedClusters", reason: "int"},
		{field: "LastDeployTime", reason: "an RFC 3339 timestamp"},
		{field: "AvgSyncDuration", reason: "Sharko's own formatted duration"},
		{field: "AvgSyncSecs", reason: "float"},
		{field: "Clusters", reason: "[]AddonClusterHealth, guarded below"},
	},
	"AddonClusterHealth": {
		{field: "ClusterName", reason: "Sharko's own split of the ArgoCD app name"},
		{field: "Health", routed: true, reason: "credsafe.SafeHealthStatus — echoed only when it is a value Sharko knows"},
		{field: "HealthSince", reason: "an RFC 3339 timestamp"},
		{field: "ReconciledAt", reason: "an RFC 3339 timestamp"},
		{field: "LastDeployTime", reason: "an RFC 3339 timestamp"},
		{field: "LastSyncDuration", reason: "Sharko's own formatted duration"},
		{field: "ResourceCount", reason: "int"},
		{field: "HealthyResources", reason: "int"},
		{field: "Resources", routed: true, reason: "service.safeAppResources — this is B10's first named site; it used to be app.Resources itself"},
	},
	"AppResource": {
		{field: "Group", reason: "a Kubernetes API group — a DNS subdomain, so it cannot hold a userinfo section"},
		{field: "Kind", reason: "a Kubernetes kind — an identifier, not free-form text"},
		{field: "Namespace", reason: "a Kubernetes namespace name, RFC 1123 characters only"},
		{field: "Name", reason: "a Kubernetes object name, RFC 1123 characters only"},
		{field: "Status", routed: true, reason: "credsafe.SafeSyncStatus — a resource's status is ArgoCD's SyncStatusCode"},
		{field: "Health", routed: true, reason: "credsafe.SafeHealthStatus — a resource's health is ArgoCD's HealthStatusCode"},
		{field: "Message", routed: true, reason: "credsafe.SafeReportedDetail with credsafe.ArgocdResourceMessage — this is B10; ArgoCD's health-assessment text never travels"},
	},
	"AddonGroupHealth": {
		{field: "AddonName", reason: "Sharko's own split of the ArgoCD app name"},
		{field: "TotalApps", reason: "int"},
		{field: "HealthCounts", reason: "a count per ArgoCD health word, keyed by the raw word — same adjacent finding as ControlPlaneInfo.HealthSummary"},
		{field: "ChildApps", reason: "[]ChildAppHealth, guarded below"},
	},
	"ChildAppHealth": {
		{field: "AppName", reason: "ArgoCD's metadata.name — a Kubernetes object name"},
		{field: "ClusterName", reason: "Sharko's own split of the app name"},
		{field: "Health", routed: true, reason: "credsafe.SafeHealthStatus — echoed only when it is a value Sharko knows"},
		{field: "SyncStatus", routed: true, reason: "credsafe.SafeSyncStatus — echoed only when it is a value Sharko knows"},
		{field: "ReconciledAt", reason: "an RFC 3339 timestamp"},
		{field: "ResourceSummary", reason: "ResourceSummary, guarded below"},
		{field: "MissingLimits", reason: "container names Sharko read out of its OWN values file in git, not out of an ArgoCD object"},
	},
	"ResourceSummary": {
		{field: "TotalPods", reason: "int"},
		{field: "RunningPods", reason: "int"},
		{field: "TotalContainers", reason: "int"},
		{field: "HasMissingLimits", reason: "bool"},
	},
	"ResourceAlert": {
		{field: "AppName", reason: "ArgoCD's metadata.name — a Kubernetes object name"},
		{field: "ClusterName", reason: "Sharko's own split of the app name"},
		{field: "AddonName", reason: "Sharko's own addon name, read from its own catalog file"},
		{field: "AlertType", reason: "Sharko's own word, the literal \"missing_limits\""},
		{field: "Details", reason: "a fixed sentence written in checkMissingResources — a literal, never a provider value"},
	},

	// --- B10: GET /dashboard/attention ------------------------------------

	"AttentionItem": {
		{field: "AppName", reason: "ArgoCD's metadata.name — a Kubernetes object name"},
		{field: "AddonName", reason: "Sharko's own split of the app name"},
		{field: "Cluster", reason: "Sharko's own split of the app name"},
		{field: "Health", routed: true, reason: "credsafe.SafeHealthStatus — echoed only when it is a value Sharko knows"},
		{field: "Sync", routed: true, reason: "credsafe.SafeSyncStatus — echoed only when it is a value Sharko knows"},
		{field: "Error", routed: true, reason: "credsafe.SafeReportedDetail with credsafe.ArgocdAppConditionMessage — this is B10's second named site; the browser renders this string"},
		{field: "ErrorType", routed: true, reason: "credsafe.SafeConditionType — ArgoCD's ApplicationConditionType is a closed enum, so the type travels and the message does not"},
	},

	// --- B10: GET /addons/{name} ------------------------------------------

	"ApplicationSetStatusInfo": {
		{field: "Name", reason: "ArgoCD's metadata.name — a Kubernetes object name"},
		{field: "Conditions", reason: "[]ApplicationSetCondition, guarded below"},
		{field: "GeneratedApps", reason: "int"},
	},
	"ApplicationSetCondition": {
		{field: "Type", routed: true, reason: "credsafe.SafeAppSetConditionType — a closed enum"},
		{field: "Status", routed: true, reason: "credsafe.SafeConditionStatus — the True/False/Unknown triple"},
		{field: "Message", routed: true, reason: "credsafe.SafeReportedDetail with credsafe.ArgocdAppSetConditionMessage — B10 widening; an ApplicationSet generator holds the repository it templates from"},
	},

	// --- B11: GET /addons/list --------------------------------------------
	//
	// These two are the RESPONSE copies. The on-disk structs they are copied
	// from (models.AddonCatalogEntry, models.AddonSource) carry yaml tags and
	// are marshalled back into the operator's repository, so they are guarded
	// separately by catalogFileFieldGuard below — never by routing.

	"AddonCatalogEntryView": {
		{field: "Name", reason: "Sharko's own addon name, the key of the entry in its own catalog file"},
		{field: "RepoURL", routed: true, reason: "credsafe.SafeRepoURL — this is B11; a chart repository address is routinely written with the access token inside it"},
		{field: "Chart", reason: "a Helm chart NAME from the catalog file — a bare identifier, no URL and no free-form text"},
		{field: "Version", reason: "a chart version from the catalog file"},
		{field: "Namespace", reason: "a Kubernetes namespace name from the catalog file"},
		{field: "SelfHeal", reason: "*bool"},
		{field: "SyncOptions", reason: "ArgoCD sync-option words the operator chose from a fixed set, e.g. CreateNamespace=true — no URL, no free-form text"},
		{field: "AdditionalSources", reason: "[]AddonSourceView, guarded below — the nested repository addresses are routed there"},
		{field: "IgnoreDifferences", reason: "ArgoCD resource selectors the operator wrote: group, kind and JSON pointers into a manifest. Not a URL and not a credential store"},
		{field: "ExtraHelmValues", reason: "Helm parameter name/value pairs from the catalog file. They are the operator's own non-secret chart settings — a real secret goes in the Secrets block below, which holds provider PATHS and never values"},
		{field: "Secrets", reason: "[]AddonSecretRef — secretName, namespace, and a key→provider-path map. It is the PUSH DEFINITION, never the secret value: the value is fetched from the provider at reconcile time and is never in the catalog file at all"},
	},
	"AddonSourceView": {
		{field: "RepoURL", routed: true, reason: "credsafe.SafeRepoURL — B11; an extra chart source carries its own repository address, written the same way"},
		{field: "Path", reason: "a path INSIDE the repository; the credential lives in the URL's userinfo, which a path has no room for"},
		{field: "Chart", reason: "a Helm chart NAME"},
		{field: "Version", reason: "a chart version"},
		{field: "Parameters", reason: "Helm parameter name/value pairs the operator wrote — same argument as ExtraHelmValues above"},
		{field: "ValueFiles", reason: "repo-relative values-file paths; a path has no userinfo section"},
	},

	// --- B11: GET /catalog/addons and /catalog/addons/{name} --------------
	//
	// catalog.CatalogAddon was ALREADY the read-only view — json tags only,
	// with config.AddonCatalogEntry as the separate on-disk shape. What was
	// missing was the routing, and it could not go in BuildCatalogView,
	// because the orchestrator's write paths and the version/changelog
	// fetchers read the deployment fields off that same view. So the
	// stripping is a boundary call in the two handlers instead.

	"CatalogAddon": {
		{field: "Name", reason: "Sharko's own addon name, the key of the entry in catalog.yaml"},
		{field: "Origin", reason: "Sharko's own word, the curated/internal pair"},
		{field: "RepoURL", routed: true, reason: "credsafe.SafeRepoURL via CatalogAddon.SafeForResponse — this is B11"},
		{field: "Chart", reason: "a Helm chart NAME from catalog.yaml"},
		{field: "Version", reason: "a chart version from catalog.yaml"},
		{field: "Namespace", reason: "a Kubernetes namespace name from catalog.yaml"},
		{field: "Settings", reason: "config.AddonSettings — seven deployment switches: booleans, a namespace name and ArgoCD sync-option words. No URL and no free-form text"},
		{field: "AdditionalSources", routed: true, reason: "credsafe.SafeRepoURL on every element's RepoURL, in SafeForResponse — an extra chart source carries its own repository address"},
		{field: "ExtraHelmValues", reason: "Helm parameter name/value pairs from catalog.yaml — the operator's own non-secret chart settings"},
		{field: "Deployable", reason: "bool"},
		{field: "MissingFields", reason: "Sharko's own field NAMES, from the fixed list in missingDeploymentFields"},
		{field: "UnsupportedFields", reason: "Sharko's own field PATHS, from unsupportedRepoURLFields — the path only, never the value that was refused"},
		{field: "Secrets", reason: "[]SecretRequirement — a name, a description, a when-needed word, and an optional push definition of provider PATHS. Never a secret value"},
		{field: "Description", reason: "text from the curated list Sharko ships, not from the operator's file"},
		{field: "DocsURL", reason: "a docs link from the curated list Sharko ships — a public URL in a file in this repository, with no userinfo section in it"},
		{field: "Homepage", reason: "same curated list, same argument as DocsURL"},
		{field: "SourceURL", reason: "same curated list, same argument as DocsURL"},
		{field: "Maintainers", reason: "names from the curated list Sharko ships"},
		{field: "License", reason: "a licence identifier from the curated list"},
		{field: "Category", reason: "a category word from the curated list"},
		{field: "CuratedBy", reason: "names from the curated list"},
		{field: "SecurityScore", reason: "a score value from the curated list"},
		{field: "SecurityTier", reason: "a tier word from the curated list"},
		{field: "GitHubStars", reason: "int"},
		{field: "MinKubernetesVersion", reason: "a version string from the curated list"},
		{field: "Deprecated", reason: "bool"},
		{field: "SupersededBy", reason: "an addon name from the curated list"},
		{field: "RequiredValues", reason: "values-key names and descriptions from the curated list"},
		{field: "Quirks", reason: "text from the curated list"},
		{field: "Verified", reason: "bool — the signature-verification outcome"},
		{field: "SignatureIdentity", reason: "the Sigstore certificate identity a catalog source was signed with — matched against the anchored trust policy, so it is a value Sharko already vouched for"},
	},
	// B12 — one row of GET /api/v1/marketplace/sources.
	"catalogSourceRecord": {
		{field: "URL", routed: true, reason: "credsafe.SafeRepoURL in recordFromSnapshot. The env var that configures these is documented as being for tokened/private URLs, so a credential here is the expected shape rather than an accident — and this row goes out on a 200 to any signed-in caller, down to a viewer"},
		{field: "Status", reason: "one of three words Sharko wrote: ok, stale, failed"},
		{field: "LastFetched", reason: "*time.Time — a timestamp Sharko recorded"},
		{field: "EntryCount", reason: "int"},
		{field: "Verified", reason: "bool — the signature-verification outcome"},
		{field: "Issuer", reason: "the Sigstore certificate identity the source was signed with, matched against the anchored trust policy, so it is a value Sharko already vouched for"},
	},
	// models.AddonCatalogItem — what GET /addons/{name} returns, and what
	// ui/src/views/AddonDetail.tsx renders. Built in exactly two places, both
	// in internal/service/addon.go.
	"AddonCatalogItem": {
		{field: "AddonName", reason: "an addon name from the operator's own catalog"},
		{field: "Chart", reason: "a chart name"},
		{field: "RepoURL", routed: true, reason: "credsafe.SafeRepoURL at both construction sites, internal/service/addon.go:301 and :477 — there is no third site"},
		{field: "Namespace", reason: "a namespace name"},
		{field: "Version", reason: "a version string"},
		{field: "TotalClusters", reason: "int"},
		{field: "EnabledClusters", reason: "int"},
		{field: "HealthyApplications", reason: "int"},
		{field: "DegradedApplications", reason: "int"},
		{field: "MissingApplications", reason: "int"},
		{field: "DeployedClusterCount", reason: "int"},
		{field: "TotalTargetClusterCount", reason: "int"},
		{field: "Applications", reason: "per-cluster deployment rows — cluster names and Sharko's own health words, no URL"},
		// If AdditionalSources is ever added here, this list fails until
		// somebody decides what the response does with each element's
		// repoURL. The browser is already written to render it.
	},

	// B14 — the two version endpoints. Both hand back a chart repository
	// address on an ordinary 200, and one of them also hands back a
	// free-form sentence the browser renders verbatim.
	"catalogVersionsResponse": {
		{field: "Addon", reason: "Sharko's own addon name, the key the caller asked by"},
		{field: "Chart", reason: "a Helm chart NAME from the catalog — a bare identifier, no URL"},
		{field: "Repo", routed: true, reason: "credsafe.SafeRepoURL — this is B14; catalog.yaml is routinely written with the token inside the address"},
		{field: "Versions", reason: "chart version strings and dates, built from the repo index — no URL travels onto catalogVersionEntry"},
		{field: "LatestStable", reason: "a chart version string"},
		{field: "CachedAt", reason: "an RFC 3339 timestamp"},
		{field: "VersionCheckUnknown", reason: "bool"},
		{field: "NoDataReason", routed: true, reason: "credsafe.SafeRepoURLPhrase — the sentence names the repository and the browser renders it verbatim, so the address in it is the same stripped one, never the raw entry"},
	},
	"catalogVersionEntry": {
		{field: "Version", reason: "a chart version string from the repo index"},
		{field: "AppVersion", reason: "an application version string from the repo index"},
		{field: "Created", reason: "a timestamp string from the repo index"},
		{field: "Prerelease", reason: "bool, computed by Sharko from the version string"},
	},
	"models.AvailableVersionsResponse": {
		{field: "AddonName", reason: "Sharko's own addon name, the key the caller asked by"},
		{field: "Chart", reason: "a Helm chart NAME from Sharko's catalog — a bare identifier, no URL"},
		{field: "RepoURL", routed: true, reason: "credsafe.SafeRepoURL — this is B14; it was copied raw from the catalog entry onto a 200 body four lines below an error-path fix"},
		{field: "CurrentVersion", reason: "a chart version from Sharko's own catalog file"},
		{field: "Versions", reason: "chart version strings from the repo index — no URL travels onto models.AvailableVersion"},
	},
	"models.AvailableVersion": {
		{field: "Version", reason: "a chart version string from the repo index"},
		{field: "AppVersion", reason: "an application version string from the repo index"},
	},
}

// catalogFileFieldGuard is the OTHER half of B11, and it is the half that
// matters more.
//
// The three structs above are response copies. The structs below are the FILE:
// they carry yaml tags as well as json ones, Sharko reads the operator's
// catalog into them, changes one field and marshals the whole thing back out.
// A credential stripped on one of these is not a leak fixed, it is the
// operator's stored password deleted from their own repository on the next
// ordinary write.
//
// So this list says, for every field of the on-disk structs, whether the
// response copy carries it and how. It fails when a field is added to the file
// shape and nobody decided what the response should do with it — which is the
// exact way repoURL came to be handed out in the first place.
var catalogFileFieldGuard = map[string][]providerSourcedField{
	// models.AddonCatalogEntry — addons-catalog.yaml, the v3 shape. Copied
	// onto models.AddonCatalogEntryView by NewAddonCatalogEntryView.
	"models.AddonCatalogEntry": {
		{field: "Name", reason: "carried verbatim onto AddonCatalogEntryView.Name"},
		{field: "RepoURL", routed: true, reason: "carried onto AddonCatalogEntryView.RepoURL through credsafe.SafeRepoURL. The value HERE is left alone — it is what internal/helm dials and what the catalog writers marshal back"},
		{field: "Chart", reason: "carried verbatim"},
		{field: "Version", reason: "carried verbatim"},
		{field: "Namespace", reason: "carried verbatim"},
		{field: "SelfHeal", reason: "carried verbatim"},
		{field: "SyncOptions", reason: "carried verbatim (copied slice)"},
		{field: "AdditionalSources", routed: true, reason: "converted element by element through NewAddonSourceView, which routes each RepoURL"},
		{field: "IgnoreDifferences", reason: "carried verbatim (copied slice)"},
		{field: "ExtraHelmValues", reason: "carried verbatim (copied map)"},
		{field: "Secrets", reason: "carried verbatim (copied slice) — provider paths, never values"},
	},
	// models.AddonSource — the extra-source shape inside both catalog files.
	"models.AddonSource": {
		{field: "RepoURL", routed: true, reason: "carried onto AddonSourceView.RepoURL through credsafe.SafeRepoURL; the stored value is untouched"},
		{field: "Path", reason: "carried verbatim"},
		{field: "Chart", reason: "carried verbatim"},
		{field: "Version", reason: "carried verbatim"},
		{field: "Parameters", reason: "carried verbatim (copied map)"},
		{field: "ValueFiles", reason: "carried verbatim (copied slice)"},
	},
	// config.AddonCatalogEntry — catalog.yaml, the v4 shape. Copied onto
	// catalog.CatalogAddon by BuildCatalogView, then stripped at the response
	// boundary by CatalogAddon.SafeForResponse.
	"config.AddonCatalogEntry": {
		{field: "RepoURL", routed: true, reason: "reaches CatalogAddon.RepoURL, stripped at the response boundary by SafeForResponse. The value HERE feeds the orchestrator's write paths and the chart fetchers and is never touched"},
		{field: "Chart", reason: "carried verbatim onto CatalogAddon.Chart"},
		{field: "Version", reason: "carried verbatim"},
		{field: "Namespace", reason: "carried verbatim"},
		{field: "Settings", reason: "carried verbatim — deployment switches, no URL and no free-form text"},
		{field: "Secrets", reason: "converted by entrySecrets — provider paths, never values"},
		{field: "AdditionalSources", routed: true, reason: "reaches CatalogAddon.AdditionalSources, every element's RepoURL stripped by SafeForResponse"},
		{field: "ExtraHelmValues", reason: "carried verbatim (copied map)"},
	},
}

// clusterGuard covers the ArgoCD-sourced fields of the models.Cluster embedded
// in the comparison response. models.Cluster is a big struct that mostly holds
// Sharko's own registry data, so this list names only the fields whose value
// comes from ArgoCD, and the test below checks each one still exists.
var clusterGuard = []providerSourcedField{
	{field: "ConnectivityDetail", routed: true, reason: "credsafe.SafeOperationDetail via buildCheckFailDetail — it used to be ArgoCD's operationState.message, or an application condition's message"},
	{field: "ConnectivityStatus", reason: "Sharko's own verdict word, from a fixed set in computeConnectivityVerdictAt"},
	{field: "ConnectionStatus", routed: true, reason: "credsafe.SafeConnectionState in GetClusterComparison"},
	{field: "ServerURL", reason: "ArgoCD's cluster.server, a Kubernetes API server address. ArgoCD keeps a cluster's credentials in the Secret's config JSON, never in this field. It is NOT routed on purpose: it is the value clusterHasHealthyAddon matches destination servers against, and rewriting one side of a comparison breaks the match. Reported as an adjacent finding rather than changed here."},
	{field: "DerivedHealthStatus", reason: "Sharko's own word, from a fixed set in computeDerivedHealth"},
}

// TestComparisonGuard_EveryProviderSourcedFieldIsNamed is the guard.
func TestComparisonGuard_EveryProviderSourcedFieldIsNamed(t *testing.T) {
	structs := map[string]reflect.Type{
		"AddonComparisonStatus":     reflect.TypeOf(models.AddonComparisonStatus{}),
		"ClusterComparisonResponse": reflect.TypeOf(models.ClusterComparisonResponse{}),

		// B10 — the observability overview, top to bottom.
		"ObservabilityOverviewResponse": reflect.TypeOf(models.ObservabilityOverviewResponse{}),
		"ControlPlaneInfo":              reflect.TypeOf(models.ControlPlaneInfo{}),
		"SyncActivityEntry":             reflect.TypeOf(models.SyncActivityEntry{}),
		"AddonHealthDetail":             reflect.TypeOf(models.AddonHealthDetail{}),
		"AddonClusterHealth":            reflect.TypeOf(models.AddonClusterHealth{}),
		"AppResource":                   reflect.TypeOf(models.AppResource{}),
		"AddonGroupHealth":              reflect.TypeOf(models.AddonGroupHealth{}),
		"ChildAppHealth":                reflect.TypeOf(models.ChildAppHealth{}),
		"ResourceSummary":               reflect.TypeOf(models.ResourceSummary{}),
		"ResourceAlert":                 reflect.TypeOf(models.ResourceAlert{}),

		// B10 — the Dashboard's needs-attention row. It lives in this
		// package, and it was declared inside the handler until B10 —
		// reflection cannot see a type that only exists in a function body,
		// which is exactly why the guard never caught its `error` field.
		"AttentionItem": reflect.TypeOf(AttentionItem{}),

		// B10 — the addon detail's ApplicationSet status.
		"ApplicationSetStatusInfo": reflect.TypeOf(models.ApplicationSetStatusInfo{}),
		"ApplicationSetCondition":  reflect.TypeOf(models.ApplicationSetCondition{}),

		// B11 — the response copies of a catalog entry.
		"AddonCatalogEntryView": reflect.TypeOf(models.AddonCatalogEntryView{}),
		"AddonSourceView":       reflect.TypeOf(models.AddonSourceView{}),
		"CatalogAddon":          reflect.TypeOf(catalog.CatalogAddon{}),

		// B12 — the catalog sources page. It lives in this package, and its
		// url field was handed out verbatim on an ordinary 200 while a doc
		// comment right above it explained why that was fine.
		"catalogSourceRecord": reflect.TypeOf(catalogSourceRecord{}),

		// B12/B13 — the addon detail page's payload. It is here for a
		// field it does NOT have: the browser at
		// ui/src/views/AddonDetail.tsx renders
		// addon.additionalSources[].repoURL, and the TypeScript type
		// declares it, but this Go struct has no AdditionalSources at all,
		// so the block renders nothing today and no guard covered it.
		// Listing the struct is what makes the day somebody adds that field
		// a failing test rather than a silent leak in the browser.
		"AddonCatalogItem": reflect.TypeOf(models.AddonCatalogItem{}),

		// B14 — the two version endpoints. GET /upgrade/{addon}/versions
		// returned the tokenised repo address on a 200 while the fix four
		// lines above it covered only the error path, and
		// GET /marketplace/addons/{name}/versions returned it twice: once
		// as a field and once inside a sentence the browser prints as it
		// stands.
		"catalogVersionsResponse":          reflect.TypeOf(catalogVersionsResponse{}),
		"catalogVersionEntry":              reflect.TypeOf(catalogVersionEntry{}),
		"models.AvailableVersionsResponse": reflect.TypeOf(models.AvailableVersionsResponse{}),
		"models.AvailableVersion":          reflect.TypeOf(models.AvailableVersion{}),
	}

	// It must not be able to pass by being empty.
	if len(comparisonGuard) != len(structs) {
		t.Fatalf("the guard covers %d structs, want %d — it has been hollowed out", len(comparisonGuard), len(structs))
	}

	routed := 0
	for name, typ := range structs {
		listed := map[string]providerSourcedField{}
		for _, f := range comparisonGuard[name] {
			if _, dup := listed[f.field]; dup {
				t.Errorf("%s.%s is listed twice in the guard", name, f.field)
			}
			if strings.TrimSpace(f.reason) == "" {
				t.Errorf("%s.%s is listed with no reason — an entry with nothing written in it classifies nothing", name, f.field)
			}
			listed[f.field] = f
			if f.routed {
				routed++
			}
		}

		onStruct := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			fieldName := typ.Field(i).Name
			onStruct[fieldName] = true
			if _, ok := listed[fieldName]; !ok {
				t.Errorf(`%s.%s is on the response but is NOT in the guard.

Add it to comparisonGuard and say which it is:
  - routed: true, with the internal/credsafe helper it goes through, or
  - routed: false, with the reason its raw value cannot carry credential material.

A provider-sourced string added without either is how argocd_source_repo_url
came to hand out the repository's access token on a 200 response.`, name, fieldName)
			}
		}
		for field := range listed {
			if !onStruct[field] {
				t.Errorf("the guard lists %s.%s, which is no longer on the struct — a stale entry is a classification of nothing", name, field)
			}
		}
	}

	// models.Cluster: only the ArgoCD-sourced fields are enumerated, so the
	// check is one-directional — every named field must still exist.
	clusterType := reflect.TypeOf(models.Cluster{})
	for _, f := range clusterGuard {
		if strings.TrimSpace(f.reason) == "" {
			t.Errorf("models.Cluster.%s is listed with no reason", f.field)
		}
		if _, ok := clusterType.FieldByName(f.field); !ok {
			t.Errorf("the guard lists models.Cluster.%s, which is no longer on the struct — a stale entry is a classification of nothing", f.field)
		}
		if f.routed {
			routed++
		}
	}

	// And it must not be able to pass vacuously the other way either: if every
	// entry drifted to routed:false the list would still be complete and would
	// still prove nothing.
	// The floor is the EXACT number routed today, not a round number under it.
	// It was 24 while 27 were really routed, so three entries could have been
	// flipped to "safe as it stands" and the list would still have passed. B11
	// took the room out; B12 added catalogSourceRecord.URL and AddonCatalogItem.RepoURL
	// and re-measured
	// rather than assuming, which is the only way this number stays exact.
	// Removing any single routing fails here.
	// B14 re-measured rather than assuming: it added three routed fields —
	// catalogVersionsResponse.Repo, catalogVersionsResponse.NoDataReason and
	// models.AvailableVersionsResponse.RepoURL — so 33 became 36. Removing
	// any single routing fails here.
	const wantRoutedAtLeast = 36
	if routed < wantRoutedAtLeast {
		t.Errorf("only %d guarded fields are routed through internal/credsafe, want at least %d — entries have been flipped to 'safe as it stands' without the routing being removed from the code",
			routed, wantRoutedAtLeast)
	}
}

// TestCatalogFileFieldGuard_EveryOnDiskFieldIsAccountedFor is B11's half of the
// guard: the FILE shapes, not the response shapes.
//
// It fails in both directions the same way the response guard does — an
// unlisted field on the struct, and a listed field that no longer exists.
func TestCatalogFileFieldGuard_EveryOnDiskFieldIsAccountedFor(t *testing.T) {
	structs := map[string]reflect.Type{
		"models.AddonCatalogEntry": reflect.TypeOf(models.AddonCatalogEntry{}),
		"models.AddonSource":       reflect.TypeOf(models.AddonSource{}),
		"config.AddonCatalogEntry": reflect.TypeOf(config.AddonCatalogEntry{}),
	}
	if len(catalogFileFieldGuard) != len(structs) {
		t.Fatalf("the file-shape guard covers %d structs, want %d — it has been hollowed out",
			len(catalogFileFieldGuard), len(structs))
	}

	routed := 0
	for name, typ := range structs {
		listed := map[string]bool{}
		for _, f := range catalogFileFieldGuard[name] {
			if listed[f.field] {
				t.Errorf("%s.%s is listed twice", name, f.field)
			}
			if strings.TrimSpace(f.reason) == "" {
				t.Errorf("%s.%s is listed with no reason", name, f.field)
			}
			listed[f.field] = true
			if f.routed {
				routed++
			}
		}
		onStruct := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			fieldName := typ.Field(i).Name
			onStruct[fieldName] = true
			if !listed[fieldName] {
				t.Errorf(`%s.%s is written to the operator's catalog file but is NOT in catalogFileFieldGuard.

Add it and say what the RESPONSE copy does with it:
  - routed: true, with the internal/credsafe helper the copy goes through, or
  - routed: false, with the reason the raw value cannot carry credential material.

And whatever you decide, do not strip it HERE: this struct is marshalled back
into the repository, so a value removed here is the operator's own data thrown
away.`, name, fieldName)
			}
		}
		for field := range listed {
			if !onStruct[field] {
				t.Errorf("the file-shape guard lists %s.%s, which is no longer on the struct — a stale entry is a classification of nothing", name, field)
			}
		}
	}

	// Exact, for the same reason as the response guard above: the two
	// repository addresses on the v3 shape, the one on the extra-source shape,
	// and the two on the v4 shape.
	const wantRoutedAtLeast = 5
	if routed < wantRoutedAtLeast {
		t.Errorf("only %d file-shape fields are marked as routed onto the response copy, want at least %d — entries have been flipped to 'safe as it stands' without the routing being removed from the code",
			routed, wantRoutedAtLeast)
	}
}

// TestCatalogResponseCopy_IsNotTheOnDiskShape pins the asymmetry B11 exists
// for: the struct that goes on a response must NOT be the struct that goes back
// into the operator's repository.
//
// A yaml tag on a response copy is the warning sign — it means something could
// marshal that stripped value into a file. The check is written against the tag
// itself rather than against a list of type names, so a new response copy that
// picks up yaml tags fails here without anybody remembering to add it.
func TestCatalogResponseCopy_IsNotTheOnDiskShape(t *testing.T) {
	responseCopies := map[string]reflect.Type{
		"models.AddonCatalogEntryView": reflect.TypeOf(models.AddonCatalogEntryView{}),
		"models.AddonSourceView":       reflect.TypeOf(models.AddonSourceView{}),
	}
	for name, typ := range responseCopies {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if _, hasYAML := f.Tag.Lookup("yaml"); hasYAML {
				t.Errorf(`%s.%s carries a yaml tag.

This type exists so the sanitised copy can never be written to disk. A yaml tag
means something could marshal it into the operator's catalog file, which would
save the stripped repository address over their stored credential.`, name, f.Name)
			}
		}
	}

	// And the shapes it is a copy OF must still carry theirs, or the separation
	// is between two things that are the same and this test proves nothing.
	for name, typ := range map[string]reflect.Type{
		"models.AddonCatalogEntry": reflect.TypeOf(models.AddonCatalogEntry{}),
		"models.AddonSource":       reflect.TypeOf(models.AddonSource{}),
		"config.AddonCatalogEntry": reflect.TypeOf(config.AddonCatalogEntry{}),
	} {
		found := false
		for i := 0; i < typ.NumField(); i++ {
			if _, hasYAML := typ.Field(i).Tag.Lookup("yaml"); hasYAML {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no yaml tags any more — it is no longer the on-disk shape, so the separation this file guards has stopped meaning anything", name)
		}
	}
}

// TestComparisonGuard_SentencesAreNotEachOther. Making the three fields safe
// must not make them the same. An operator sent to the addon when the CLUSTER
// connection is what is broken has been given a worse answer than the leak was.
func TestComparisonGuard_SentencesAreNotEachOther(t *testing.T) {
	sentences := map[string]string{
		"the addon sync failure":     credsafe.ArgocdSyncFailureMessage,
		"the short badge line":       credsafe.ArgocdSyncFailureShort,
		"the cluster connection":     credsafe.ArgocdClusterConnectionFailureMessage,
		"the connectivity check":     credsafe.ArgocdCheckFailureMessage,
		"the Git connection (B1)":    credsafe.NoActiveGitConnectionMessage,
		"the ArgoCD connection (B1)": credsafe.NoActiveArgocdConnectionMessage,
	}
	seen := map[string]string{}
	for name, s := range sentences {
		if s == "" {
			t.Errorf("%s is empty — an empty sentence passes every leak sweep and helps nobody", name)
		}
		if other, dup := seen[s]; dup {
			t.Errorf("%s says exactly what %s says — these send an operator to different screens and must stay different answers", name, other)
		}
		seen[s] = name
		if !strings.Contains(s, "ArgoCD") && !strings.Contains(s, "Sharko") {
			t.Errorf("%s names neither ArgoCD nor Sharko, so it tells the reader nothing about where to go: %q", name, s)
		}
	}
}

// TestSafeOperationDetail_RefusesAnUnknownSentence holds the line named in
// credsafe's own doc: the helper takes a sentence parameter only so two callers
// can share the fact formatting, and it must not become a way to print
// arbitrary text under a safe-sounding function name.
func TestSafeOperationDetail_RefusesAnUnknownSentence(t *testing.T) {
	got := credsafe.SafeOperationDetail("here is the raw error: "+initLeakRepoURL, credsafe.OperationFacts{
		Phase: "Failed",
	})
	assertNoInitLeak(t, "SafeOperationDetail handed an arbitrary sentence", got)
	if !strings.HasPrefix(got, credsafe.ArgocdSyncFailureMessage) {
		t.Errorf("an unknown sentence must fall back to the most conservative one, got %q", got)
	}
	_ = fmt.Sprintf // keep fmt used if the assertions above are edited
}
