package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/secrets"
)

// reconcilerWithKnownItems is the real engine's extra method on top of the
// stand-in: the Secrets it has a record for, with the catalog's definition.
type reconcilerWithKnownItems struct {
	*fakeSecretReconciler
	items []secrets.KnownItem
}

func (r *reconcilerWithKnownItems) KnownItems() []secrets.KnownItem { return r.items }

// Walk 3, serious finding: with a push definition in the catalog and the
// Secret delivered to both clusters (Created: 2), the Secrets page said
// "No addon Secrets yet." because the rows were built only from definitions
// registered in memory, and in a real install nothing registers any: the
// definitions live in the catalog in Git. A healthy delivered Secret must be
// listed.
func TestManagedSecrets_ListsSecretsTheEngineDelivered(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "spoke-1", "server": "https://spoke-1.example.com"},
		{"name": "spoke-2", "server": "https://spoke-2.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{
		managedYAML: []byte("clusters:\n- name: spoke-1\n- name: spoke-2\n"),
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	srv.SetSecretReconciler(&reconcilerWithKnownItems{
		fakeSecretReconciler: &fakeSecretReconciler{lastItemOutcome: "created", lastItemOutcomeOK: true},
		items: []secrets.KnownItem{
			{Cluster: "spoke-1", Addon: "podinfo", SecretName: "podinfo-walk3", Namespace: "podinfo",
				Keys: map[string]string{"token": "sharko-ep4/walk3-canary"}},
			{Cluster: "spoke-2", Addon: "podinfo", SecretName: "podinfo-walk3", Namespace: "podinfo"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.AddonValuesSecrets) != 2 {
		t.Fatalf("addon_values_secrets has %d rows, want the 2 Secrets Sharko delivered: %+v",
			len(body.AddonValuesSecrets), body.AddonValuesSecrets)
	}
	for _, row := range body.AddonValuesSecrets {
		if row.Addon != "podinfo" || row.SecretName != "podinfo-walk3" || row.SecretNamespace != "podinfo" {
			t.Errorf("row %+v is not the delivered podinfo-walk3 Secret", row)
		}
		if row.State != "in_sync" {
			t.Errorf("row %s state = %q, want in_sync for a Secret that was just created", row.Cluster, row.State)
		}
	}
}

// The detail view of one row reads where the Secret lives from the same
// place, so clicking a delivered row does not answer "no values secret
// defined".
func TestAddonSecretDefinitionFor_FindsWhatTheEngineKnows(t *testing.T) {
	srv := newIsolatedTestServer(t)
	srv.SetSecretReconciler(&reconcilerWithKnownItems{
		fakeSecretReconciler: &fakeSecretReconciler{},
		items: []secrets.KnownItem{
			{Cluster: "spoke-1", Addon: "podinfo", SecretName: "podinfo-walk3", Namespace: "podinfo"},
		},
	})
	def, ok := srv.addonSecretDefinitionFor("spoke-1", "podinfo")
	if !ok || def.SecretName != "podinfo-walk3" || def.Namespace != "podinfo" {
		t.Fatalf("got %+v ok=%v, want podinfo-walk3 in podinfo", def, ok)
	}
	if _, ok := srv.addonSecretDefinitionFor("spoke-2", "podinfo"); ok {
		t.Error("found a definition for a cluster the engine has no record for")
	}
}
