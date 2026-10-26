package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/models"
)

// ep4 round R, R4: while an EKS token renewal is failing, the cluster is a
// problem on every surface, not only on the row's status word. Before this,
// the "Connected" count, the check verdict and the health pill could all still
// be green next to a row that said "Failed".
func TestTokenRenewalFailure_IsAProblemEverywhere(t *testing.T) {
	const name = "eks-old"
	recon := startFailingRenewal(t, name, refusingEKSBackend{})

	t.Run("list counts move the cluster from connected to failed", func(t *testing.T) {
		stats := &models.ClusterHealthStats{TotalInGit: 2, Connected: 2, Failed: 0}
		c := models.Cluster{Name: name, ConnectionStatus: "Successful"}
		if applyTokenRenewalTrouble(&c, recon) {
			moveRenewalTroubleInStats(stats)
		}
		if stats.Connected != 1 || stats.Failed != 1 {
			t.Fatalf("counts = %+v, want connected 1 and failed 1", stats)
		}
		// Already failed: nothing to move, the count already says failed.
		f := models.Cluster{Name: name, ConnectionStatus: "Failed"}
		if applyTokenRenewalTrouble(&f, recon) {
			t.Fatal("a cluster that was already failed was counted as moved")
		}
		// A cluster with no trouble moves nothing.
		ok := models.Cluster{Name: "fine", ConnectionStatus: "Successful"}
		if applyTokenRenewalTrouble(&ok, recon) {
			t.Fatal("a healthy cluster was counted as moved")
		}
		// Never below zero.
		empty := &models.ClusterHealthStats{}
		moveRenewalTroubleInStats(empty)
		moveRenewalTroubleInStats(nil)
		if empty.Connected != 0 || empty.Failed != 0 {
			t.Fatalf("empty counts changed to %+v", empty)
		}
	})

	t.Run("verdict and health pill are not green even with a healthy check app", func(t *testing.T) {
		c := models.Cluster{
			Name:                name,
			ConnectionStatus:    "Failed",
			ConnectivityStatus:  "verified_check",
			ConnectivityDetail:  "",
			DerivedHealthStatus: "healthy",
		}
		applyTokenRenewalVerdict(&c, recon)
		if c.ConnectivityStatus != "check_failed" {
			t.Errorf("connectivity status = %q, want check_failed", c.ConnectivityStatus)
		}
		if c.ConnectivityDetail != clusterreconciler.TokenRenewalFailedSentence {
			t.Errorf("connectivity detail = %q, want the renewal sentence", c.ConnectivityDetail)
		}
		if c.DerivedHealthStatus != derivedHealthUnknown {
			t.Errorf("derived health = %q, want %q", c.DerivedHealthStatus, derivedHealthUnknown)
		}

		// A cluster with no trouble keeps what it had.
		ok := models.Cluster{Name: "fine", ConnectivityStatus: "verified_check", DerivedHealthStatus: "healthy"}
		applyTokenRenewalVerdict(&ok, recon)
		if ok.ConnectivityStatus != "verified_check" || ok.DerivedHealthStatus != "healthy" {
			t.Fatalf("a healthy cluster was changed to %+v", ok)
		}
		// No reconciler at all (out of cluster): nothing changes, no panic.
		applyTokenRenewalVerdict(&ok, nil)
		if ok.ConnectivityStatus != "verified_check" {
			t.Fatal("no reconciler changed the verdict")
		}
	})

	// ep4 round S, S1: the old token still works, so the page must not say
	// "Unavailable". It says "connected" (true) and carries the renewal
	// sentence as a warning: the renewal failed and ArgoCD will lose the
	// connection when the current token runs out.
	t.Run("connection page says connected, warns about the renewal, and does not say ArgoCD cannot use it", func(t *testing.T) {
		sentence, failing := recon.TokenRenewalTrouble(name)
		if !failing {
			t.Fatal("renewal not marked as failing")
		}
		out := buildConnectionReconciliationView(connectionReconciliationFacts{
			view:                reconView(nil),
			healthState:         healthStateConnected,
			tokenRenewalTrouble: sentence,
		})
		if out.Health.State != healthStateConnected {
			t.Errorf("health state = %q, want %q: the old token still works, so ArgoCD's connection is not unavailable yet", out.Health.State, healthStateConnected)
		}
		if out.Health.State == healthStateUnavailable {
			t.Errorf("health state is %q while the old token still works", healthStateUnavailable)
		}
		if out.Health.Message != "" {
			t.Errorf("health message = %q, want none: ArgoCD reported no failure", out.Health.Message)
		}
		const wantWarning = "Sharko could not get a new sign-in token for this cluster, so ArgoCD will lose its connection to it soon. Check that Sharko can reach your secrets provider and AWS, then click Refresh EKS connection."
		if out.Health.Warning != wantWarning {
			t.Errorf("health warning = %q, want %q", out.Health.Warning, wantWarning)
		}
		raw, err := json.Marshal(out.Health)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(raw); got != `{"state":"connected","warning":`+mustJSON(t, wantWarning)+`}` {
			t.Errorf("health JSON = %s", got)
		}
		var found bool
		for _, c := range out.Conditions {
			if c.Detail == condArgoCDUnavailable {
				t.Errorf("condition %q says %q, which is not true while the old token still works", c.ID, c.Detail)
			}
			if c.ID == conditionArgoCDConnection {
				found = true
				if c.Detail != clusterreconciler.TokenRenewalFailedSentence {
					t.Errorf("ArgoCD condition detail = %q, want the renewal sentence", c.Detail)
				}
				if c.Status != conditionStatusAttention {
					t.Errorf("ArgoCD condition status = %q, want %q while the renewal is failing", c.Status, conditionStatusAttention)
				}
			}
		}
		if !found {
			t.Fatal("no ArgoCD connection condition in the answer")
		}

		// No trouble: connected stays connected with the usual sentence.
		out = buildConnectionReconciliationView(connectionReconciliationFacts{
			view:        reconView(nil),
			healthState: healthStateConnected,
		})
		if out.Health.State != healthStateConnected || out.Health.Message != "" || out.Health.Warning != "" {
			t.Fatalf("no trouble: health = %+v, want connected with no message and no warning", out.Health)
		}

		// ArgoCD already cannot use it: its own message wins, the renewal
		// sentence does not cover it up.
		out = buildConnectionReconciliationView(connectionReconciliationFacts{
			view:                reconView(nil),
			healthState:         healthStateUnavailable,
			healthMessage:       "the server has asked for the client to provide credentials",
			tokenRenewalTrouble: sentence,
		})
		if out.Health.State != healthStateUnavailable || out.Health.Message != "the server has asked for the client to provide credentials" {
			t.Fatalf("ArgoCD's own failure was covered up: %+v", out.Health)
		}
		if out.Health.Warning != "" {
			t.Fatalf("a real ArgoCD failure also got the renewal warning: %+v", out.Health)
		}
		for _, c := range out.Conditions {
			if c.ID == conditionArgoCDConnection && c.Detail != condArgoCDUnavailable {
				t.Fatalf("real ArgoCD failure: condition detail = %q, want %q", c.Detail, condArgoCDUnavailable)
			}
		}
	})
}

// The same rule through the real list handler: the cluster ArgoCD calls
// "Successful" is counted as failed, and its row is not green anywhere.
func TestListClusters_FailingTokenRenewalIsNotCountedConnected(t *testing.T) {
	const name = "prod-eu" // argoListFailingSrv(200) reports it as Successful
	recon := startFailingRenewal(t, name, refusingEKSBackend{})

	argo := argoListFailingSrv(t, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n- name: prod-us\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)
	srv.clusterRecon = recon

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}
	var resp models.ClustersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.HealthStats == nil || resp.HealthStats.Connected != 0 || resp.HealthStats.Failed != 1 {
		t.Fatalf("health stats = %+v, want 0 connected and 1 failed", resp.HealthStats)
	}
	var row *models.Cluster
	for i := range resp.Clusters {
		if resp.Clusters[i].Name == name {
			row = &resp.Clusters[i]
		}
	}
	if row == nil {
		t.Fatalf("no %s row in %+v", name, resp.Clusters)
	}
	if row.ConnectionStatus != "Failed" || row.ConnectivityStatus != "check_failed" ||
		row.ConnectivityDetail != clusterreconciler.TokenRenewalFailedSentence ||
		row.DerivedHealthStatus != derivedHealthUnknown {
		t.Fatalf("row = status %q, check %q (%q), health %q; want Failed, check_failed with the renewal sentence, unknown",
			row.ConnectionStatus, row.ConnectivityStatus, row.ConnectivityDetail, row.DerivedHealthStatus)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
