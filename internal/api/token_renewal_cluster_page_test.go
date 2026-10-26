package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// ep4 round S, S2: while an EKS token renewal is failing but ArgoCD still
// reports its connection as Successful, the cluster page must not be told
// that ArgoCD's connection failed. The old token still works, so ArgoCD's
// status stays "Successful" and the renewal sentence travels in its own
// field. Before this fix the handler forced ArgoCD's status to "Failed",
// which put "Cluster Unreachable — ArgoCD Connection Failed" and "may be
// stale" on the page.
const wantRenewalProblem = "Sharko could not get a new sign-in token for this cluster, so ArgoCD will lose its connection to it soon. Check that Sharko can reach your secrets provider and AWS, then click Refresh EKS connection."

func TestClusterComparison_FailingRenewalDoesNotSayArgoCDFailed(t *testing.T) {
	const name = "prod-eu" // argoListFailingSrv(200) reports it as Successful
	recon := startFailingRenewal(t, name, refusingEKSBackend{})

	argo := argoListFailingSrv(t, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n- name: prod-us\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)
	srv.clusterRecon = recon

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+name+"/comparison", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}
	var resp models.ClusterComparisonResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ArgocdConnectionStatus != "Successful" {
		t.Errorf("argocd_connection_status = %q, want Successful: ArgoCD still says it works, and the old token still does", resp.ArgocdConnectionStatus)
	}
	if resp.ArgocdConnectionMessage != "" {
		t.Errorf("argocd_connection_message = %q, want none: ArgoCD reported no failure", resp.ArgocdConnectionMessage)
	}
	if resp.TokenRenewalProblem != wantRenewalProblem {
		t.Errorf("token_renewal_problem = %q, want %q", resp.TokenRenewalProblem, wantRenewalProblem)
	}
	if !strings.Contains(w.Body.String(), `"token_renewal_problem":`) {
		t.Errorf("no token_renewal_problem field on the wire: %s", w.Body.String())
	}
}

func TestApplyTokenRenewalProblem_OnlyWhileArgoCDStillWorks(t *testing.T) {
	const name = "eks-old"
	recon := startFailingRenewal(t, name, refusingEKSBackend{})

	// ArgoCD says it works: the renewal sentence goes in its own field and
	// ArgoCD's own status is left as it is.
	ok := &models.ClusterComparisonResponse{Cluster: models.Cluster{Name: name}, ArgocdConnectionStatus: "Successful"}
	applyTokenRenewalProblem(ok, recon)
	if ok.TokenRenewalProblem != wantRenewalProblem || ok.ArgocdConnectionStatus != "Successful" || ok.ArgocdConnectionMessage != "" {
		t.Fatalf("ArgoCD still works: got %+v", ok)
	}

	// ArgoCD itself reports a failure: its report wins, untouched, and no
	// renewal field is added.
	const argoSentence = "ArgoCD cannot reach this cluster."
	failed := &models.ClusterComparisonResponse{Cluster: models.Cluster{Name: name}, ArgocdConnectionStatus: "Failed", ArgocdConnectionMessage: argoSentence}
	applyTokenRenewalProblem(failed, recon)
	if failed.TokenRenewalProblem != "" || failed.ArgocdConnectionStatus != "Failed" || failed.ArgocdConnectionMessage != argoSentence {
		t.Fatalf("ArgoCD reports a failure: got %+v", failed)
	}

	// No renewal trouble, no reconciler, no response: nothing changes.
	fine := &models.ClusterComparisonResponse{Cluster: models.Cluster{Name: "fine"}, ArgocdConnectionStatus: "Successful"}
	applyTokenRenewalProblem(fine, recon)
	applyTokenRenewalProblem(fine, nil)
	applyTokenRenewalProblem(nil, recon)
	if fine.TokenRenewalProblem != "" || fine.ArgocdConnectionStatus != "Successful" {
		t.Fatalf("no trouble: got %+v", fine)
	}
}
