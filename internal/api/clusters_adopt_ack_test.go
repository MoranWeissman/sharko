package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/takeover"
)

// adoptFakeArgocd is an ArgoCD that knows the clusters it is given.
type adoptFakeArgocd struct {
	discoverFakeArgocd
	clusters []models.ArgocdCluster
}

func (a *adoptFakeArgocd) ListClusters(context.Context) ([]models.ArgocdCluster, error) {
	return a.clusters, nil
}

func adoptAckTestRouter(t *testing.T) (http.Handler, *takeoverFakeGP) {
	t.Helper()
	gp := newTakeoverFakeGP()
	srv, router, _ := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)}, nil)
	srv.connSvc.SetArgocdClientOverride(&adoptFakeArgocd{clusters: []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.invalid"},
	}})
	return router, gp
}

// A real adopt with a warning nobody acknowledged answers 409, lists the
// warning with its id, and writes nothing (episode 3 dry run, finding 13).
func TestAdopt_409_WhenAWarningIsNotAcknowledged(t *testing.T) {
	router, gp := adoptAckTestRouter(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/adopt", "admin",
		orchestrator.AdoptClustersRequest{Clusters: []string{"prod-eu"}}))

	if w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	var body adoptNeedsAcknowledgementResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "unacknowledged_findings" {
		t.Errorf("code = %q, want unacknowledged_findings", body.Code)
	}
	if len(body.UnacknowledgedFindings) == 0 || !containsStr(body.UnacknowledgedFindings, takeover.FindingSecretOwner) {
		t.Errorf("unacknowledged_findings = %v, want it to name %s", body.UnacknowledgedFindings, takeover.FindingSecretOwner)
	}
	if len(body.Results) != 1 || body.Results[0].Name != "prod-eu" || len(body.Results[0].Warnings) == 0 {
		t.Fatalf("results must carry the cluster's warnings: %s", w.Body.String())
	}
	for _, wn := range body.Results[0].Warnings {
		if wn.ID == "" || wn.Message == "" {
			t.Errorf("every warning needs an id and a message: %+v", wn)
		}
	}
	if len(body.Warnings) == 0 {
		t.Errorf("the flat warnings list is empty: %s", w.Body.String())
	}
	if gp.wroteAnything() {
		t.Fatalf("an unacknowledged adopt still touched git: branches=%v written=%v prs=%d", gp.branches, gp.written, gp.prs)
	}
}

// Acknowledging every warning id lets the same adopt go through.
func TestAdopt_AcknowledgedWarningsGoThrough(t *testing.T) {
	router, gp := adoptAckTestRouter(t)

	// First, find out which ids there are, the way the UI does: a preview.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/adopt", "admin",
		orchestrator.AdoptClustersRequest{Clusters: []string{"prod-eu"}, DryRun: true}))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: got %d (body=%s)", w.Code, w.Body.String())
	}
	var preview orchestrator.AdoptClustersResult
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(preview.Results) != 1 || len(preview.Results[0].Warnings) == 0 {
		t.Fatalf("the preview must list the warnings to read: %s", w.Body.String())
	}
	if gp.wroteAnything() {
		t.Fatal("a preview touched git")
	}
	var ids []string
	for _, wn := range preview.Results[0].Warnings {
		ids = append(ids, wn.ID)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/adopt", "admin",
		orchestrator.AdoptClustersRequest{Clusters: []string{"prod-eu"}, AcknowledgedFindings: ids}))
	if w.Code != http.StatusOK {
		t.Fatalf("acknowledged adopt: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if gp.prs != 1 {
		t.Errorf("expected one pull request, got %d", gp.prs)
	}
}
