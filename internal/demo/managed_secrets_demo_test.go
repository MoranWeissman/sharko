package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/api"
)

// managed_secrets_demo_test.go pins the behaviour the maintainer's ask
// (managed secrets page has something to look at) actually depends on:
// the big generated estate produces real addon-values rows with a full
// state spread including a genuinely never-checked row, the connection
// rows show more than "unknown", seeded audit entries join onto both kinds
// of row, and the per-row Refresh/Sync actions change what the next read
// reports.

// managedSecretsBody mirrors the JSON shape of
// internal/api/system_managed_secrets.go's response — a minimal local
// re-declaration (test-only) rather than importing the unexported types.
type managedSecretsBody struct {
	ClusterConnectionSecrets []struct {
		Cluster            string `json:"cluster"`
		State              string `json:"state"`
		LastChecked        string `json:"last_checked"`
		LastRepaired       string `json:"last_repaired"`
		LastRepairedDetail string `json:"last_repaired_detail"`
	} `json:"cluster_connection_secrets"`
	AddonValuesSecrets []struct {
		Cluster            string `json:"cluster"`
		Addon              string `json:"addon"`
		State              string `json:"state"`
		LastChecked        string `json:"last_checked"`
		LastRepaired       string `json:"last_repaired"`
		LastRepairedDetail string `json:"last_repaired_detail"`
	} `json:"addon_values_secrets"`
	Engines struct {
		ClusterConnection struct {
			Wired           bool   `json:"wired"`
			IntervalSeconds int    `json:"interval_seconds"`
			LastRun         string `json:"last_run"`
		} `json:"cluster_connection"`
		AddonValues struct {
			Wired           bool   `json:"wired"`
			IntervalSeconds int    `json:"interval_seconds"`
			LastRun         string `json:"last_run"`
		} `json:"addon_values"`
	} `json:"engines"`
}

// getManagedSecrets performs the authenticated GET and decodes the body.
func getManagedSecrets(t *testing.T, router http.Handler, token string) managedSecretsBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("GET managed-secrets status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body managedSecretsBody
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode managed-secrets response: %v; body = %s", err, rw.Body.String())
	}
	return body
}

// TestBigEstate_ManagedSecrets_FullStateSpread is S1-S5: on the big
// generated estate, the Managed Secrets page must show real addon-values
// rows (not zero), a full state spread on both tables (not 50x unknown /
// all in-sync), at least one addon-values row that has never been checked,
// and both engines reporting wired with a recent run.
func TestBigEstate_ManagedSecrets_FullStateSpread(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	body := getManagedSecrets(t, router, token)

	// --- Engines: both wired with a recent run ---
	if !body.Engines.ClusterConnection.Wired {
		t.Error("engines.cluster_connection.wired = false, want true on the big estate")
	}
	if body.Engines.ClusterConnection.LastRun == "" {
		t.Error("engines.cluster_connection.last_run is empty, want a recent timestamp")
	}
	if !body.Engines.AddonValues.Wired {
		t.Error("engines.addon_values.wired = false, want true on the big estate")
	}
	if body.Engines.AddonValues.LastRun == "" {
		t.Error("engines.addon_values.last_run is empty, want a recent timestamp")
	}

	// --- Addon values secrets: real rows, full state spread, one never
	// checked ---
	if len(body.AddonValuesSecrets) == 0 {
		t.Fatal("addon_values_secrets is empty on the big estate — S1/S2 regression")
	}

	avStates := map[string]int{}
	neverChecked := 0
	for _, row := range body.AddonValuesSecrets {
		avStates[row.State]++
		if row.LastChecked == "" {
			neverChecked++
		}
	}
	t.Logf("addon_values_secrets: %d rows, states=%v, never_checked=%d", len(body.AddonValuesSecrets), avStates, neverChecked)

	for _, want := range []string{"in_sync", "out_of_sync", "missing"} {
		if avStates[want] == 0 {
			t.Errorf("addon_values_secrets has no row in state %q — want the full spread, states=%v", want, avStates)
		}
	}
	if neverChecked == 0 {
		t.Error("addon_values_secrets has no row with an empty last_checked — want at least one genuinely never-checked row (S2)")
	}

	// --- Cluster connection secrets: more than 50x unknown ---
	if len(body.ClusterConnectionSecrets) == 0 {
		t.Fatal("cluster_connection_secrets is empty on the big estate")
	}
	connStates := map[string]int{}
	for _, row := range body.ClusterConnectionSecrets {
		connStates[row.State]++
	}
	t.Logf("cluster_connection_secrets: %d rows, states=%v", len(body.ClusterConnectionSecrets), connStates)

	if connStates["in_sync"] == 0 {
		t.Errorf("cluster_connection_secrets has no in_sync row — want the majority in sync, states=%v", connStates)
	}
	if connStates["unknown"] == len(body.ClusterConnectionSecrets) {
		t.Errorf("cluster_connection_secrets is 100%% unknown — want a real spread (S4 regression), states=%v", connStates)
	}
	if connStates["missing"] == 0 {
		t.Errorf("cluster_connection_secrets has no missing row — want the self-managed exemplar to show missing, states=%v", connStates)
	}

	// --- Repair history joins (S5): at least one row of each kind carries
	// a last_repaired stamp with its canned detail. ---
	var connRepaired, avRepaired int
	for _, row := range body.ClusterConnectionSecrets {
		if row.LastRepaired != "" {
			connRepaired++
			if row.LastRepairedDetail == "" {
				t.Errorf("cluster %q has last_repaired but no last_repaired_detail", row.Cluster)
			}
		}
	}
	for _, row := range body.AddonValuesSecrets {
		if row.LastRepaired != "" {
			avRepaired++
			if row.LastRepairedDetail == "" {
				t.Errorf("cluster %q addon %q has last_repaired but no last_repaired_detail", row.Cluster, row.Addon)
			}
		}
	}
	if connRepaired == 0 {
		t.Error("no cluster_connection_secrets row carries last_repaired — S5 audit seeding regression")
	}
	if avRepaired == 0 {
		t.Error("no addon_values_secrets row carries last_repaired — S5 audit seeding regression")
	}
}

// TestBigEstate_SmallEstateAddonSecretDefsUnchanged pins S1's other half:
// the small hand-written estate (plain `make demo`, DefaultScaleConfig)
// must keep showing exactly the datadog/vault rows it always has — no new
// rows from the additional generated-estate-only definitions.
func TestSmallEstate_ManagedSecrets_Unchanged(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, DefaultScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	body := getManagedSecrets(t, router, token)

	for _, row := range body.AddonValuesSecrets {
		if row.Addon != "datadog" && row.Addon != "vault" {
			t.Errorf("small estate produced an addon_values_secrets row for addon %q — want only datadog/vault", row.Addon)
		}
	}

	// The small estate never wires a cluster reconciler or a secret
	// reconciler at all (step 10/11 are gated behind estate != nil), so
	// both engines must report unwired, exactly as before this change.
	if body.Engines.ClusterConnection.Wired {
		t.Error("small estate: engines.cluster_connection.wired = true, want false (unchanged behaviour)")
	}
	if body.Engines.AddonValues.Wired {
		t.Error("small estate: engines.addon_values.wired = true, want false (unchanged behaviour)")
	}
}

// TestBigEstate_RefreshAndSync_ChangeTheRow is S3: clicking Refresh then
// Sync on an out-of-sync addon-values row must change what the very next
// read reports — outcome flips to in_sync and a fresh last_repaired
// appears.
func TestBigEstate_RefreshAndSync_ChangeTheRow(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	body := getManagedSecrets(t, router, token)

	var target *struct {
		Cluster            string `json:"cluster"`
		Addon              string `json:"addon"`
		State              string `json:"state"`
		LastChecked        string `json:"last_checked"`
		LastRepaired       string `json:"last_repaired"`
		LastRepairedDetail string `json:"last_repaired_detail"`
	}
	for i := range body.AddonValuesSecrets {
		if body.AddonValuesSecrets[i].State == "out_of_sync" {
			target = &body.AddonValuesSecrets[i]
			break
		}
	}
	if target == nil {
		t.Fatal("no out_of_sync addon_values_secrets row found to exercise Refresh/Sync against")
	}

	post := func(path string) map[string]string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil))
		req.Header.Set("Authorization", "Bearer "+token)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("POST %s status = %d, want 200; body = %s", path, rw.Code, rw.Body.String())
		}
		var resp map[string]string
		if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode POST %s response: %v; body = %s", path, err, rw.Body.String())
		}
		return resp
	}

	refreshPath := fmt.Sprintf("/api/v1/clusters/%s/addons/%s/secret/refresh", target.Cluster, target.Addon)
	refreshResp := post(refreshPath)
	if refreshResp["outcome"] != "out_of_sync" {
		t.Errorf("Refresh outcome = %q, want out_of_sync (a check must not fix anything)", refreshResp["outcome"])
	}

	syncPath := fmt.Sprintf("/api/v1/clusters/%s/addons/%s/secret/sync", target.Cluster, target.Addon)
	syncResp := post(syncPath)
	if syncResp["outcome"] != "updated" {
		t.Errorf("Sync outcome = %q, want updated", syncResp["outcome"])
	}

	after := getManagedSecrets(t, router, token)
	var found bool
	for _, row := range after.AddonValuesSecrets {
		if row.Cluster != target.Cluster || row.Addon != target.Addon {
			continue
		}
		found = true
		if row.State != "in_sync" {
			t.Errorf("after Sync, row state = %q, want in_sync", row.State)
		}
		if row.LastRepaired == "" {
			t.Error("after Sync, last_repaired is still empty — want a fresh repair stamp")
		}
		if row.LastChecked == target.LastChecked {
			t.Error("after Refresh+Sync, last_checked did not change")
		}
	}
	if !found {
		t.Fatalf("row for cluster %q addon %q not found after Sync", target.Cluster, target.Addon)
	}
}

// TestBigEstate_ConnectionReconcileTrigger_RefreshesRows is S3's other
// half: POST /clusters/{name}/reconcile must not 503 in demo mode (it did,
// before this change — no reconcilerTrigger was ever wired for a generated
// estate), and must visibly change the row's last_checked.
func TestBigEstate_ConnectionReconcileTrigger_RefreshesRows(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	before := getManagedSecrets(t, router, token)
	if len(before.ClusterConnectionSecrets) == 0 {
		t.Fatal("no cluster_connection_secrets rows to check")
	}
	target := before.ClusterConnectionSecrets[0]

	// last_checked is RFC3339 (second precision) — sleep past a full
	// second so the trigger's fresh timestamp is guaranteed to render
	// differently, rather than the test flaking on sub-second timing.
	time.Sleep(1100 * time.Millisecond)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/clusters/%s/reconcile", target.Cluster), bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusAccepted {
		t.Fatalf("POST reconcile status = %d, want 202; body = %s", rw.Code, rw.Body.String())
	}

	after := getManagedSecrets(t, router, token)
	var found bool
	for _, row := range after.ClusterConnectionSecrets {
		if row.Cluster != target.Cluster {
			continue
		}
		found = true
		if row.LastChecked == target.LastChecked {
			t.Errorf("cluster %q last_checked did not change after a reconcile trigger", target.Cluster)
		}
	}
	if !found {
		t.Fatalf("cluster %q not found after reconcile trigger", target.Cluster)
	}
}
