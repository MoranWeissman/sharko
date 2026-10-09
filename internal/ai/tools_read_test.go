package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// fakeGPWithClusters is a GitProvider that returns fixed YAML for the two
// config files the read tools need.  All other methods fail with errFakeProvider
// (defined in tools_write_authz_test.go in the same package).
type fakeGPWithClusters struct {
	managedClusters []byte
	catalog         []byte
}

func (f fakeGPWithClusters) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	switch path {
	case "configuration/managed-clusters.yaml":
		return f.managedClusters, nil
	case "configuration/addons-catalog.yaml":
		return f.catalog, nil
	}
	return nil, errFakeProvider
}
func (f fakeGPWithClusters) ListDirectory(_ context.Context, _ string, _ string) ([]string, error) {
	return nil, errFakeProvider
}
func (f fakeGPWithClusters) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, errFakeProvider
}
func (f fakeGPWithClusters) TestConnection(_ context.Context) error { return errFakeProvider }
func (f fakeGPWithClusters) CreateBranch(_ context.Context, _ string, _ string) error {
	return errFakeProvider
}
func (f fakeGPWithClusters) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _ string, _ string) error {
	return errFakeProvider
}
func (f fakeGPWithClusters) BatchCreateFiles(_ context.Context, _ map[string][]byte, _ string, _ string) error {
	return errFakeProvider
}
func (f fakeGPWithClusters) DeleteFile(_ context.Context, _ string, _ string, _ string) error {
	return errFakeProvider
}
func (f fakeGPWithClusters) CreatePullRequest(_ context.Context, _ string, _ string, _ string, _ string) (*gitprovider.PullRequest, error) {
	return nil, errFakeProvider
}
func (f fakeGPWithClusters) MergePullRequest(_ context.Context, _ int) error {
	return errFakeProvider
}
func (f fakeGPWithClusters) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "", errFakeProvider
}
func (f fakeGPWithClusters) DeleteBranch(_ context.Context, _ string) error { return errFakeProvider }

// argocdAppJSON returns a minimal ArgoCD application JSON body with the given
// health, sync, and operationMessage values.
func argocdAppJSON(name, health, sync, opMsg string) []byte {
	type opState struct {
		Phase   string `json:"phase"`
		Message string `json:"message"`
	}
	type syncBlock struct {
		Status string `json:"status"`
	}
	type healthBlock struct {
		Status string `json:"status"`
	}
	type statusBlock struct {
		Sync           syncBlock   `json:"sync"`
		Health         healthBlock `json:"health"`
		OperationState *opState    `json:"operationState,omitempty"`
	}
	type metaBlock struct {
		Name string `json:"name"`
	}
	type specBlock struct {
		Project string `json:"project"`
	}
	type app struct {
		Metadata metaBlock   `json:"metadata"`
		Spec     specBlock   `json:"spec"`
		Status   statusBlock `json:"status"`
	}

	a := app{}
	a.Metadata.Name = name
	a.Spec.Project = "default"
	a.Status.Sync.Status = sync
	a.Status.Health.Status = health
	if opMsg != "" {
		a.Status.OperationState = &opState{Phase: "Failed", Message: opMsg}
	}
	b, _ := json.Marshal(a)
	return b
}

// argocdAppsListJSON returns a minimal ArgoCD applications list JSON body.
func argocdAppsListJSON(apps ...[]byte) []byte {
	var items []json.RawMessage
	for _, a := range apps {
		items = append(items, json.RawMessage(a))
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	out, _ := json.Marshal(map[string]interface{}{"items": items})
	return out
}

// newTestClusterAndCatalogGP returns a fakeGPWithClusters whose
// managed-clusters YAML has cluster "prod-eu" with keda=enabled, and a
// catalog YAML with keda at 2.13.0.
func newTestClusterAndCatalogGP() fakeGPWithClusters {
	mc := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        keda: enabled
`)
	cat := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addons-catalog
spec:
  applicationsets:
    - name: keda
      version: "2.13.0"
      chart: keda
      repoURL: https://kedacore.github.io/charts
      namespace: keda
`)
	return fakeGPWithClusters{managedClusters: mc, catalog: cat}
}

// newToolExecutorWithArgocd creates a ToolExecutor backed by the given httptest
// server as the ArgoCD endpoint and the given git provider.
func newToolExecutorWithArgocd(srv *httptest.Server, gp gitprovider.GitProvider) *ToolExecutor {
	ac := argocd.NewClient(srv.URL, "test-token", true)
	return &ToolExecutor{
		parser:              config.NewParser(),
		gp:                  gp,
		ac:                  ac,
		managedClustersPath: "configuration/managed-clusters.yaml",
	}
}

// ------- getAddonOnCluster tests -------

// TestGetAddonOnCluster_OperationMessageIsSharkosOwnSentence used to be
// TestGetAddonOnCluster_OperationMessageIncluded, and it used to require that
// ArgoCD's own operationState.message came out in the assistant's context
// verbatim (V2-cleanup-39). That assertion was the leak's keeper on the AI
// surface: the message quotes the repository ArgoCD was syncing from, token
// and all, and the assistant's context becomes the assistant's answer.
//
// It is inverted, not deleted. The same fixture still drives the same path; the
// line is still there and still labelled; what it carries is now Sharko's own
// sentence, and ArgoCD's words must be absent (B8).
func TestGetAddonOnCluster_OperationMessageIsSharkosOwnSentence(t *testing.T) {
	const argocdsOwnWords = "one or more tasks failed: CRD name too long"
	const wantMsg = "ArgoCD could not finish syncing this addon. Sharko does not repeat ArgoCD's own message here: that message quotes whatever ArgoCD was working on, which includes the repository address with its access token inside it. Open this application in ArgoCD to read the full error."

	appBody := argocdAppJSON("keda-prod-eu", "Healthy", "OutOfSync", argocdsOwnWords)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(appBody)
	}))
	defer srv.Close()

	e := newToolExecutorWithArgocd(srv, newTestClusterAndCatalogGP())
	out, err := e.getAddonOnCluster(context.Background(), "keda", "prod-eu")
	if err != nil {
		t.Fatalf("getAddonOnCluster returned error: %v", err)
	}
	if strings.Contains(out, argocdsOwnWords) {
		t.Errorf("the assistant's context carries ArgoCD's own message\ngot: %s", out)
	}
	if !strings.Contains(out, wantMsg) {
		t.Errorf("output does not carry Sharko's own sentence\ngot: %s", out)
	}
	if !strings.Contains(out, "Operation Message:") {
		t.Errorf("output missing 'Operation Message:' label\ngot: %s", out)
	}
}

// TestGetAddonOnCluster_EmptyOperationMessageOmitted asserts that when the
// ArgoCD app has an empty OperationMessage, no "Operation Message:" line
// appears in the output (V2-cleanup-39).
func TestGetAddonOnCluster_EmptyOperationMessageOmitted(t *testing.T) {
	appBody := argocdAppJSON("keda-prod-eu", "Healthy", "Synced", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(appBody)
	}))
	defer srv.Close()

	e := newToolExecutorWithArgocd(srv, newTestClusterAndCatalogGP())
	out, err := e.getAddonOnCluster(context.Background(), "keda", "prod-eu")
	if err != nil {
		t.Fatalf("getAddonOnCluster returned error: %v", err)
	}
	if strings.Contains(out, "Operation Message:") {
		t.Errorf("output should NOT contain 'Operation Message:' when message is empty\ngot: %s", out)
	}
}

// ------- getUnhealthyAddons tests -------

// TestGetUnhealthyAddons_OperationMessageIsSharkosOwnSentence is the same
// inversion for the unhealthy-addons tool (was
// TestGetUnhealthyAddons_OperationMessageIncluded, V2-cleanup-39). See the
// note on the addon-detail one above.
func TestGetUnhealthyAddons_OperationMessageIsSharkosOwnSentence(t *testing.T) {
	const argocdsOwnWords = "sync failed: pod quota exceeded"
	const wantMsg = "ArgoCD could not finish syncing this addon. Sharko does not repeat ArgoCD's own message here: that message quotes whatever ArgoCD was working on, which includes the repository address with its access token inside it. Open this application in ArgoCD to read the full error."

	unhealthyApp := argocdAppJSON("keda-prod-eu", "Degraded", "OutOfSync", argocdsOwnWords)
	listBody := argocdAppsListJSON(unhealthyApp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(listBody)
	}))
	defer srv.Close()

	e := newToolExecutorWithArgocd(srv, newTestClusterAndCatalogGP())
	out, err := e.getUnhealthyAddons(context.Background())
	if err != nil {
		t.Fatalf("getUnhealthyAddons returned error: %v", err)
	}
	if strings.Contains(out, argocdsOwnWords) {
		t.Errorf("the assistant's context carries ArgoCD's own message\ngot: %s", out)
	}
	if !strings.Contains(out, wantMsg) {
		t.Errorf("output does not carry Sharko's own sentence\ngot: %s", out)
	}
	if !strings.Contains(out, "message:") {
		t.Errorf("output missing 'message:' label\ngot: %s", out)
	}
}

// TestGetUnhealthyAddons_AllHealthy asserts the happy path: when all apps are
// Healthy the function returns "All addons are healthy." unchanged (V2-cleanup-39).
func TestGetUnhealthyAddons_AllHealthy(t *testing.T) {
	healthyApp := argocdAppJSON("cert-manager-prod-eu", "Healthy", "Synced", "")
	listBody := argocdAppsListJSON(healthyApp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(listBody)
	}))
	defer srv.Close()

	e := newToolExecutorWithArgocd(srv, newTestClusterAndCatalogGP())
	out, err := e.getUnhealthyAddons(context.Background())
	if err != nil {
		t.Fatalf("getUnhealthyAddons returned error: %v", err)
	}
	if out != "All addons are healthy." {
		t.Errorf("expected 'All addons are healthy.', got %q", out)
	}
}
