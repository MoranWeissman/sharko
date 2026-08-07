package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

// orphaned_secret_demo_test.go — leftover-secrets S1. The seeded "redis-ha"
// orphan (addon_values_reconciler.go's newDemoAddonValuesReconciler) has to
// actually show up through the real interface the System page reads, and a
// demo delete has to genuinely change what the next read reports — same
// "not a stub" bar the Refresh/Sync tests in managed_secrets_demo_test.go
// hold this reconciler to.

// orphanedSecretsBody decodes just the orphaned_secrets slice of GET
// /system/managed-secrets — a minimal, test-only re-declaration (same
// convention as managedSecretsBody in managed_secrets_demo_test.go).
type orphanedSecretsBody struct {
	OrphanedSecrets []struct {
		Cluster         string `json:"cluster"`
		SecretNamespace string `json:"secret_namespace"`
		SecretName      string `json:"secret_name"`
		Addon           string `json:"addon"`
		State           string `json:"state"`
		LastChecked     string `json:"last_checked"`
	} `json:"orphaned_secrets"`
}

func getOrphanedSecrets(t *testing.T, router http.Handler, token string) orphanedSecretsBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("GET managed-secrets status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body orphanedSecretsBody
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode managed-secrets response: %v; body = %s", err, rw.Body.String())
	}
	return body
}

// TestBigEstate_OrphanedSecret_SeededAndVisible is the "the interface
// actually reports one" half: the seeded redis-ha leftover secret must
// appear in orphaned_secrets with a real addon name and a last-checked
// timestamp — not a placeholder.
func TestBigEstate_OrphanedSecret_SeededAndVisible(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	body := getOrphanedSecrets(t, router, token)
	if len(body.OrphanedSecrets) == 0 {
		t.Fatal("orphaned_secrets is empty on the big estate — leftover-secrets S1 seed regression")
	}

	row := body.OrphanedSecrets[0]
	if row.Addon == "" {
		t.Error("seeded orphan has no addon name")
	}
	if row.SecretName == "" || row.SecretNamespace == "" {
		t.Errorf("seeded orphan is missing name/namespace: %+v", row)
	}
	if row.State != "orphaned" {
		t.Errorf("seeded orphan state = %q, want orphaned", row.State)
	}
	if row.LastChecked == "" {
		t.Error("seeded orphan has no last_checked timestamp")
	}
}

// TestBigEstate_OrphanedSecret_DeleteRoundTrips is the "not a stub" half:
// deleting the seeded orphan through the real DELETE endpoint must make it
// disappear from the very next read, and must leave an audit entry with the
// same event/resource/detail shape the real reconciler's delete path uses.
func TestBigEstate_OrphanedSecret_DeleteRoundTrips(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	before := getOrphanedSecrets(t, router, token)
	if len(before.OrphanedSecrets) == 0 {
		t.Fatal("no seeded orphan to delete")
	}
	target := before.OrphanedSecrets[0]

	path := "/api/v1/clusters/" + target.Cluster + "/orphaned-secrets/" + target.SecretNamespace + "/" + target.SecretName
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("DELETE %s status = %d, want 200; body = %s", path, rw.Code, rw.Body.String())
	}

	after := getOrphanedSecrets(t, router, token)
	for _, row := range after.OrphanedSecrets {
		if row.Cluster == target.Cluster && row.SecretNamespace == target.SecretNamespace && row.SecretName == target.SecretName {
			t.Fatalf("deleted orphan %+v is still reported after delete", target)
		}
	}

	entries := srv.AuditLog().List(0)
	found := false
	wantResource := "cluster:" + target.Cluster
	wantDetail := "deleted leftover secret " + target.SecretNamespace + "/" + target.SecretName
	for _, e := range entries {
		if e.Event == "orphaned_secret_deleted" && e.Resource == wantResource && e.Detail == wantDetail {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no orphaned_secret_deleted audit entry with resource=%q detail=%q", wantResource, wantDetail)
	}
}
