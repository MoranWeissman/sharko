package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
// connectionSecretRowBody mirrors internal/api's connectionSecretRow on the
// wire. Named (rather than anonymous inline) so a test can hold a pointer to
// one — see the failed-check lookup below.
type connectionSecretRowBody struct {
	Cluster            string `json:"cluster"`
	State              string `json:"state"`
	LastChecked        string `json:"last_checked"`
	LastRepaired       string `json:"last_repaired"`
	LastRepairedDetail string `json:"last_repaired_detail"`
	LastCheckError     string `json:"last_check_error"`
	// B5 — the canonical answer the row's state is a projection of.
	ManagementMode    string `json:"management_mode"`
	ManagedScope      string `json:"managed_scope"`
	SyncState         string `json:"sync_state"`
	VerificationScope string `json:"verification_scope"`
	Headline          string `json:"headline"`
	Qualifier         string `json:"qualifier"`
	Health            string `json:"health"`
}

type managedSecretsBody struct {
	ClusterConnectionSecrets []connectionSecretRowBody `json:"cluster_connection_secrets"`
	AddonValuesSecrets       []struct {
		Cluster            string `json:"cluster"`
		Addon              string `json:"addon"`
		State              string `json:"state"`
		LastChecked        string `json:"last_checked"`
		LastRepaired       string `json:"last_repaired"`
		LastRepairedDetail string `json:"last_repaired_detail"`
		LastCheckError     string `json:"last_check_error"`
	} `json:"addon_values_secrets"`
	Engines struct {
		ClusterConnection struct {
			Wired            bool   `json:"wired"`
			IntervalSeconds  int    `json:"interval_seconds"`
			LastRun          string `json:"last_run"`
			LastError        string `json:"last_error"`
			LastErrorCluster string `json:"last_error_cluster"`
			LastErrorAt      string `json:"last_error_at"`
		} `json:"cluster_connection"`
		AddonValues struct {
			Wired            bool   `json:"wired"`
			IntervalSeconds  int    `json:"interval_seconds"`
			LastRun          string `json:"last_run"`
			LastError        string `json:"last_error"`
			LastErrorCluster string `json:"last_error_cluster"`
			LastErrorAt      string `json:"last_error_at"`
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

	// "foreign" (P1-A) joins the spread: the walk has to be able to see a
	// secret Sharko refuses to touch, or the new state is invisible.
	for _, want := range []string{"in_sync", "out_of_sync", "missing", "foreign"} {
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

// TestBigEstate_ManagedSecrets_FailedChecksAreHonest is P1-B B3: the demo
// estate must show a failed check on BOTH engines — a connection row whose
// last check failed (state unknown, never out_of_sync, with a canned
// reason), an addon-values row likewise (closing queued row
// demo-seed-a-failing-check — the values side has always had the field,
// the demo never seeded it), and a non-empty engine-level error WITH a
// cluster and a time on the addon-values engine (the connection engine's
// half of this was already pinned by
// TestHandleGetManagedSecrets_ClusterConnectionEngine_LastErrorNamesClusterAndTime
// in internal/api, driven off this same demo-shaped seed).
func TestBigEstate_ManagedSecrets_FailedChecksAreHonest(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	body := getManagedSecrets(t, router, token)

	// --- Connection side: a failed check reads as unknown, with a reason,
	// never out_of_sync. ---
	var failedConnRow *connectionSecretRowBody
	for i := range body.ClusterConnectionSecrets {
		if body.ClusterConnectionSecrets[i].LastCheckError != "" {
			failedConnRow = &body.ClusterConnectionSecrets[i]
			break
		}
	}
	if failedConnRow == nil {
		t.Fatal("no cluster_connection_secrets row carries last_check_error — want at least one seeded failed check (P1-B B3)")
	}
	if failedConnRow.State != "unknown" {
		t.Errorf("cluster %q: state = %q, want unknown (a failed check is not drift)", failedConnRow.Cluster, failedConnRow.State)
	}
	if strings.Contains(failedConnRow.LastCheckError, "Kubernetes API timed out") {
		t.Errorf("connection last_check_error looks like the raw demo seed text, not a canned sentence: %q", failedConnRow.LastCheckError)
	}

	// --- Addon values side: at least one row now carries last_check_error
	// (previously never seeded — queued row demo-seed-a-failing-check). ---
	var failedValuesRow *struct {
		Cluster            string `json:"cluster"`
		Addon              string `json:"addon"`
		State              string `json:"state"`
		LastChecked        string `json:"last_checked"`
		LastRepaired       string `json:"last_repaired"`
		LastRepairedDetail string `json:"last_repaired_detail"`
		LastCheckError     string `json:"last_check_error"`
	}
	for i := range body.AddonValuesSecrets {
		if body.AddonValuesSecrets[i].LastCheckError != "" {
			failedValuesRow = &body.AddonValuesSecrets[i]
			break
		}
	}
	if failedValuesRow == nil {
		t.Fatal("no addon_values_secrets row carries last_check_error on the big estate — closes demo-seed-a-failing-check, but it's not seeded")
	}
	// P1-B symmetry fix: a failed check is not drift on EITHER kind of row
	// — the values side had the same #120 bug (a failed check collapsed
	// into out_of_sync) the connection side was fixed for; this row must
	// now read unknown, never out_of_sync.
	if failedValuesRow.State != "unknown" {
		t.Errorf("cluster %q addon %q: state = %q, want unknown (a failed check is not drift)", failedValuesRow.Cluster, failedValuesRow.Addon, failedValuesRow.State)
	}
	if strings.Contains(failedValuesRow.LastCheckError, "connection refused") || strings.Contains(failedValuesRow.LastCheckError, "dial tcp") {
		t.Errorf("addon-values last_check_error leaked raw text: %q", failedValuesRow.LastCheckError)
	}

	// --- Addon-values engine: a non-empty error with a cluster and a time,
	// so both halves of the top strip can be seen failing. ---
	if body.Engines.AddonValues.LastError == "" {
		t.Error("engines.addon_values.last_error is empty, want a seeded engine-level failure (P1-B B3)")
	}
	if body.Engines.AddonValues.LastErrorCluster == "" {
		t.Error("engines.addon_values.last_error_cluster is empty, want a named cluster")
	}
	if body.Engines.AddonValues.LastErrorAt == "" {
		t.Error("engines.addon_values.last_error_at is empty, want a timestamp")
	}
	if strings.Contains(body.Engines.AddonValues.LastError, "connection refused") {
		t.Errorf("engines.addon_values.last_error leaked raw text: %q", body.Engines.AddonValues.LastError)
	}

	// --- Refresh (the read-only check trigger) must not clear either
	// failure — a check that fails again stays failed. ---
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+failedConnRow.Cluster+"/reconcile", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusAccepted {
		t.Fatalf("POST reconcile status = %d, want 202; body = %s", rw.Code, rw.Body.String())
	}

	after := getManagedSecrets(t, router, token)
	var stillFailed bool
	for _, row := range after.ClusterConnectionSecrets {
		if row.Cluster == failedConnRow.Cluster && row.State == "unknown" && row.LastCheckError != "" {
			stillFailed = true
		}
	}
	if !stillFailed {
		t.Errorf("cluster %q no longer shows a failed check after Refresh — a check that fails again must stay failed", failedConnRow.Cluster)
	}

	var stillFailedValues bool
	for _, row := range after.AddonValuesSecrets {
		if row.Cluster == failedValuesRow.Cluster && row.Addon == failedValuesRow.Addon && row.LastCheckError != "" {
			if row.State != "unknown" {
				t.Errorf("cluster %q addon %q: state = %q after Refresh, want unknown to stay unknown", row.Cluster, row.Addon, row.State)
			}
			stillFailedValues = true
		}
	}
	if !stillFailedValues {
		t.Errorf("cluster %q addon %q no longer carries last_check_error after Refresh — a check that fails again must stay failed", failedValuesRow.Cluster, failedValuesRow.Addon)
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
		LastCheckError     string `json:"last_check_error"`
	}
	for i := range body.AddonValuesSecrets {
		if body.AddonValuesSecrets[i].State == "out_of_sync" && body.AddonValuesSecrets[i].LastCheckError == "" {
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

// TestBigEstate_ForeignRow_RefusesSyncAndStaysForeign is P1-A A4: the demo
// has to teach the same lesson the real thing does. A Refresh on a foreign
// row still reports foreign (a check looks, it never fixes), and a Sync is
// refused with the exact sentence the page shows on the disabled button.
func TestBigEstate_ForeignRow_RefusesSyncAndStaysForeign(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	body := getManagedSecrets(t, router, token)

	var cluster, addon string
	for _, row := range body.AddonValuesSecrets {
		if row.State == "foreign" {
			cluster, addon = row.Cluster, row.Addon
			break
		}
	}
	if cluster == "" {
		t.Fatal("no foreign addon_values_secrets row in the big estate — the walk cannot see the new state")
	}

	post := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil))
		req.Header.Set("Authorization", "Bearer "+token)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		return rw
	}

	rw := post(fmt.Sprintf("/api/v1/clusters/%s/addons/%s/secret/refresh", cluster, addon))
	if rw.Code != http.StatusOK {
		t.Fatalf("Refresh status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var refreshResp map[string]string
	if err := json.Unmarshal(rw.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("decode Refresh response: %v", err)
	}
	if refreshResp["outcome"] != "foreign" {
		t.Errorf("Refresh outcome = %q, want foreign", refreshResp["outcome"])
	}

	rw = post(fmt.Sprintf("/api/v1/clusters/%s/addons/%s/secret/sync", cluster, addon))
	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Sync status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "Someone else created this one — Sharko will not touch it.") {
		t.Errorf("Sync refusal body = %s, want the exact foreign sentence", rw.Body.String())
	}

	// And the row is unchanged: still foreign after both clicks.
	after := getManagedSecrets(t, router, token)
	for _, row := range after.AddonValuesSecrets {
		if row.Cluster == cluster && row.Addon == addon && row.State != "foreign" {
			t.Errorf("row state = %q after Refresh+Sync, want it to stay foreign", row.State)
		}
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

// TestBigEstate_CheckAll_HonorsAddonValuesEngineOffSwitch is M5's pin (code
// review): before this fix, the demo reconciler's CheckAll ignored the
// settings store entirely — flipping Settings -> Addon Values Engine off
// changed the REAL engine's "Check all now" behaviour but left
// `make demo-big`'s demo reconciler running exactly as before, so a walk
// through the demo taught the wrong lesson about the off switch. Turning
// the setting off (the same PUT the Settings page uses) must now make
// POST /api/v1/secrets/check refuse with a 409 — the same synchronous
// IsEnabled pre-check M6 added to handleCheckSecrets fires here too, since
// the demo reconciler now implements IsEnabled honestly (M5) instead of
// always reporting enabled. See
// TestHandleCheckSecrets_EngineDisabled_Returns409NoAuditEntry in
// internal/api for the same contract against a fake (non-demo) reconciler.
func TestBigEstate_CheckAll_HonorsAddonValuesEngineOffSwitch(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	setEngine := func(enabled bool) {
		t.Helper()
		body, marshalErr := json.Marshal(map[string]bool{"addon_values_engine_enabled": enabled})
		if marshalErr != nil {
			t.Fatalf("marshal settings body: %v", marshalErr)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/addon-values-engine-enabled", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("PUT addon-values-engine-enabled(%v) status = %d, want 200; body = %s", enabled, rw.Code, rw.Body.String())
		}
	}

	checkAll := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets/check", bytes.NewReader(nil))
		req.Header.Set("Authorization", "Bearer "+token)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		return rw
	}

	// Engine on (default) — Check all now proceeds normally.
	if rw := checkAll(); rw.Code != http.StatusAccepted {
		t.Fatalf("engine on: POST /secrets/check status = %d, want 202; body = %s", rw.Code, rw.Body.String())
	}

	// Engine off — the same demo reconciler that just accepted a check must
	// now refuse, honestly, instead of running the check anyway.
	setEngine(false)
	rw := checkAll()
	if rw.Code != http.StatusConflict {
		t.Fatalf("engine off: POST /secrets/check status = %d, want 409 — the demo reconciler's off switch is not wired (M5); body = %s", rw.Code, rw.Body.String())
	}

	// Turning it back on restores normal behaviour — proves this is the
	// live setting, not a one-way trip.
	setEngine(true)
	if rw := checkAll(); rw.Code != http.StatusAccepted {
		t.Fatalf("engine back on: POST /secrets/check status = %d, want 202; body = %s", rw.Code, rw.Body.String())
	}
}

// TestBigEstate_ManagedSecrets_ConnectionRowsCarryTheCanonicalAnswer is B5 on
// the demo estate: every connection row answers the two independent questions
// the fleet must now answer — does the live resource match the git-defined
// state Sharko manages, and does ArgoCD report that the connection works —
// and the invariant holds on every row.
//
// It also pins the ban: no row anywhere may render the bare word "Synced",
// and no row may render "Connection synced" for a connection whose data
// Sharko does not manage.
func TestBigEstate_ManagedSecrets_ConnectionRowsCarryTheCanonicalAnswer(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	body := getManagedSecrets(t, router, demoLoginToken(t, router))
	if len(body.ClusterConnectionSecrets) == 0 {
		t.Fatal("no connection rows on the big estate")
	}

	validSync := map[string]bool{"synced": true, "out_of_sync": true, "blocked": true, "unknown": true}
	validScope := map[string]bool{"full": true, "partial": true, "none": true}
	// Four health words since B13: "unknown" is the answer for a
	// self-managed connection whose Secret does not exist. Pinning three
	// here was stale — it could not fire, and if the estate ever produced
	// that row the message would have named the wrong rule.
	validHealth := map[string]bool{"connected": true, "unavailable": true, "not_checked": true, "unknown": true}
	modes := map[string]int{}
	headlines := map[string]int{}

	for _, row := range body.ClusterConnectionSecrets {
		modes[row.ManagementMode]++
		headlines[row.Headline]++

		if !validSync[row.SyncState] {
			t.Errorf("%s: sync_state = %q, not one of the four durable words", row.Cluster, row.SyncState)
		}
		if !validScope[row.VerificationScope] {
			t.Errorf("%s: verification_scope = %q, not one of full/partial/none", row.Cluster, row.VerificationScope)
		}
		if !validHealth[row.Health] {
			t.Errorf("%s: health = %q, not one of the four health words", row.Cluster, row.Health)
		}
		if row.Headline == "" {
			t.Errorf("%s: no headline — the browser would have to invent one", row.Cluster)
		}
		if row.Headline == "Synced" {
			t.Errorf(`%s: rendered the banned bare word "Synced"`, row.Cluster)
		}
		if row.ManagementMode != "sharko_managed" && row.Headline == "Connection synced" {
			t.Errorf("%s: %q claimed \"Connection synced\"", row.Cluster, row.ManagementMode)
		}
		// THE INVARIANT, on every demo row, in both vocabularies.
		if row.SyncState == "synced" && (row.VerificationScope != "full" || row.ManagedScope == "none") {
			t.Errorf("%s: synced at verification_scope=%q managed_scope=%q", row.Cluster, row.VerificationScope, row.ManagedScope)
		}
		if row.State == "in_sync" && row.SyncState != "synced" {
			t.Errorf("%s: state=in_sync but sync_state=%q — the two vocabularies disagree", row.Cluster, row.SyncState)
		}
	}

	t.Logf("connection rows: modes=%v headlines=%v", modes, headlines)
	// The demo estate is meant to SHOW the feature, so it must contain at
	// least one connection Sharko does not own, phrased as such.
	if modes["foreign_owned"] == 0 {
		t.Error("no foreign_owned row on the big estate — the blocked state is invisible in the demo")
	}
	if headlines["Blocked"] == 0 {
		t.Error(`no row renders "Blocked" — the foreign-owned exemplar is not phrased`)
	}
	if headlines["Connection synced"] == 0 {
		t.Error(`no row renders "Connection synced" — the majority should be cleanly in sync`)
	}
}
