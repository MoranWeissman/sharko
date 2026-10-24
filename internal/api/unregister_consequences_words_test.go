package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// EP3-DRYRUN-FIXES call 11 (findings 10 and 28): the unregister-consequences
// words. Proper singular and plural, never owned by "", the CLI command
// instead of an HTTP call, and only files that are really in the repo.

func getConsequences(t *testing.T, gp *takeoverFakeGP, secrets []*corev1.Secret) (UnregisterConsequencesResponse, string) {
	t.Helper()
	_, router, _ := takeoverTestServer(t, gp, secrets, fakeAppSetReader{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodGet, "/api/v1/clusters/prod-eu/unregister/consequences", "operator", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("consequences: got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp UnregisterConsequencesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp, w.Body.String()
}

func consequenceByID(resp UnregisterConsequencesResponse, id string) *UnregisterConsequence {
	for i := range resp.Consequences {
		if resp.Consequences[i].ID == id {
			return &resp.Consequences[i]
		}
	}
	return nil
}

// bannedUnregisterWording is wording the page must never show again.
var bannedUnregisterWording = []string{
	"thing(s)",
	"Application(s)",
	`owned by ""`,
	`owned by \"\"`,
	"send DELETE",
	"cannot be undone",
	"stays exactly as it is",
}

func assertNoBannedUnregisterWording(t *testing.T, body string) {
	t.Helper()
	for _, bad := range bannedUnregisterWording {
		if strings.Contains(body, bad) {
			t.Errorf("the consequences page still says %q: %s", bad, body)
		}
	}
}

func TestUnregisterConsequences_ConfirmationNamesTheCLICommand(t *testing.T) {
	resp, body := getConsequences(t, newTakeoverFakeGP(), []*corev1.Secret{takenOverSecret()})
	if want := "Nothing here has happened yet. To go ahead, run: sharko remove-cluster prod-eu --yes. To take the cluster out of Sharko but keep its ArgoCD connection, add --cleanup=git."; resp.ConfirmationRequired != want {
		t.Errorf("confirmation_required:\n got %q\nwant %q", resp.ConfirmationRequired, want)
	}
	if want := `DELETE /api/v1/clusters/prod-eu with "yes": true in the body. Add "cleanup": "git" to keep its ArgoCD connection.`; resp.ConfirmationAPI != want {
		t.Errorf("confirmation_api:\n got %q\nwant %q", resp.ConfirmationAPI, want)
	}
	c := consequenceByID(resp, "argocd-connection")
	// The exact sentences, written out here. Comparing to the constants let
	// a changed constant pass; this does not.
	wantDetail := "Sharko owns this cluster's connection, so a full removal deletes it from ArgoCD once the pull request is merged. Nothing changes on the connection before that."
	wantMeans := "ArgoCD stops being able to reach the cluster. Anything ArgoCD is deploying there goes to an unknown state, and other tools that rely on that same connection lose it too. " +
		"If you only want the cluster out of Sharko, keep the connection (in the CLI: --cleanup=git). ArgoCD keeps it, and Sharko only takes its own label off it."
	if c == nil {
		t.Fatal("no argocd-connection consequence")
	}
	if c.Detail != wantDetail {
		t.Errorf("argocd-connection detail:\n got %q\nwant %q", c.Detail, wantDetail)
	}
	if c.WhatItMeans != wantMeans {
		t.Errorf("argocd-connection what_it_means:\n got %q\nwant %q", c.WhatItMeans, wantMeans)
	}
	assertNoBannedUnregisterWording(t, body)
}

func TestUnregisterConsequences_ConnectionWithNoOwnerLabel(t *testing.T) {
	plain := brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)
	resp, body := getConsequences(t, newTakeoverFakeGP(), []*corev1.Secret{plain})
	c := consequenceByID(resp, "argocd-connection")
	if c == nil || c.Detail != "This cluster's connection has no owner label, so it does not belong to Sharko." {
		t.Fatalf("argocd-connection consequence: %+v", c)
	}
	assertNoBannedUnregisterWording(t, body)
}

func TestUnregisterConsequences_NamesOnlyFilesThatExist(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no files of its own", nil,
			"A pull request removes this cluster from the fleet record. It has no other files of its own in the repo."},
		{"only the addon-assignment file", map[string]string{orchestrator.V4ClustersDir + "/prod-eu.yaml": "addons: {}\n"},
			"A pull request removes this cluster from the fleet record and deletes its addon-assignment file."},
		{"one values file, no addon-assignment file", map[string]string{orchestrator.V4ClusterValuesDir + "/prod-eu/logging.yaml": "a: 1\n"},
			"A pull request removes this cluster from the fleet record and deletes its 1 per-cluster Helm values file."},
		{"both", map[string]string{
			orchestrator.V4ClustersDir + "/prod-eu.yaml":                 "addons: {}\n",
			orchestrator.V4ClusterValuesDir + "/prod-eu/logging.yaml":    "a: 1\n",
			orchestrator.V4ClusterValuesDir + "/prod-eu/monitoring.yaml": "a: 1\n",
			orchestrator.V4ClusterValuesDir + "/other/logging.yaml":      "a: 1\n",
		}, "A pull request removes this cluster from the fleet record and deletes its addon-assignment file and its 2 per-cluster Helm values files."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gp := newTakeoverFakeGP()
			for p, b := range tc.files {
				gp.files[p] = []byte(b)
			}
			resp, _ := getConsequences(t, gp, []*corev1.Secret{takenOverSecret()})
			c := consequenceByID(resp, "git-record")
			if c == nil || c.Detail != tc.want {
				t.Fatalf("git-record:\n got %+v\nwant %q", c, tc.want)
			}
		})
	}
}

func TestUnregisterConsequences_V3ValuesFileOnlyWhenThere(t *testing.T) {
	gp := newTakeoverFakeGP()
	delete(gp.files, orchestrator.BootstrapRootAppPath) // v3
	resp, _ := getConsequences(t, gp, []*corev1.Secret{takenOverSecret()})
	if c := consequenceByID(resp, "git-record"); c == nil || c.Detail != "A pull request removes this cluster from the fleet record. It has no other files of its own in the repo." {
		t.Fatalf("v3, no values file: %+v", c)
	}
	gp.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("a: 1\n")
	resp, _ = getConsequences(t, gp, []*corev1.Secret{takenOverSecret()})
	if c := consequenceByID(resp, "git-record"); c == nil || c.Detail != "A pull request removes this cluster from the fleet record and deletes its values file." {
		t.Fatalf("v3, values file there: %+v", c)
	}
}

func TestUnregisterConsequences_GitUnreadable_GeneralWording(t *testing.T) {
	if got, want := unregisterGitRecordDetail(true, clusterRepoFiles{}), "A pull request removes this cluster from the fleet record and deletes any files of its own it has in the repo."; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnregisterConsequences_SingularAndPlural(t *testing.T) {
	if got, want := unregisterRunningAppsTitle(1), "1 ArgoCD Application is deployed there right now"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got, want := unregisterRunningAppsTitle(3), "3 ArgoCD Applications are deployed there right now"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got, want := unregisterSummary("prod-eu", 1), "Unregistering prod-eu has 1 thing worth reading carefully first. Nothing has been changed yet."; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got, want := unregisterSummary("prod-eu", 2), "Unregistering prod-eu has 2 things worth reading carefully first. Nothing has been changed yet."; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got, want := unregisterForeignConnectionDetail("argocd-cli"), `This cluster's connection is marked as owned by "argocd-cli", not by Sharko.`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
