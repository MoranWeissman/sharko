package api

// connection_credential_check_test.go — W3-3: the background credential
// check and its per-cluster store.
//
// Reuses the connection-comparison fixtures (comparisonFixture, the
// sentinel, the EKS mint counter) so these tests exercise the SAME core the
// endpoint tests exercise — that shared core is the design's whole point.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
)

// wantDriftedSentence pins the product owner's exact drifted wording. If
// this literal and the constant in connection_credential_check.go ever
// disagree, the test fails — nobody paraphrases it later.
const wantDriftedSentence = "This connection's stored details no longer match its configured credentials source. Nothing was changed. An admin can review and repair it from the connection page."

// wantInlineSentence is the ESTABLISHED inline-kubeconfig limited-scope
// sentence from internal/connectioncompare/mode.go — the store must reuse
// it, never write a new one.
const wantInlineSentence = "This cluster was registered with a pasted kubeconfig, and those credentials are only stored in the connection itself. Sharko has no second copy to check the connection details against, so it checks the labels and the plain connection facts only."

// seedSyncedLiveSecret builds the live connection Secret EXACTLY as a real
// write would (same argosecrets builder the comparison's expected side
// uses), creates it in the fixture's fake clientset, and clears the fake's
// recorded actions so a later read-only assertion doesn't count the seeding.
func seedSyncedLiveSecret(t *testing.T, srv *Server, argoClient *fake.Clientset) {
	t.Helper()
	specLabels := map[string]string{"datadog": "enabled"}
	models.ApplyConnectivityCheckLabel(specLabels, srv.clusterRecon.ConnectivityCheckEnabled(context.Background()))
	built, err := argosecrets.BuildClusterSecret(argosecrets.ClusterSecretSpec{
		Name:   comparisonCluster,
		Server: "https://" + comparisonCluster + ".invalid",
		Token:  comparisonSentinel,
		CAData: base64.StdEncoding.EncodeToString([]byte("not-a-real-ca-just-test-bytes")),
		Labels: specLabels,
	}, "argocd")
	if err != nil {
		t.Fatalf("building the live secret through the canonical builder: %v", err)
	}
	// The builder fills StringData; a real API server converts that to Data
	// on write, but the fake clientset does not — do the conversion here so
	// the seeded Secret looks exactly like one read back from a cluster.
	built.Data = make(map[string][]byte, len(built.StringData))
	for k, v := range built.StringData {
		built.Data[k] = []byte(v)
	}
	built.StringData = nil
	ctx := context.Background()
	if _, err := argoClient.CoreV1().Secrets("argocd").Create(ctx, built, metav1.CreateOptions{}); err != nil {
		// Upsert: a test that drifts the secret and later restores it calls
		// this twice.
		if _, updErr := argoClient.CoreV1().Secrets("argocd").Update(ctx, built, metav1.UpdateOptions{}); updErr != nil {
			t.Fatalf("seeding the live secret: create %v / update %v", err, updErr)
		}
	}
	argoClient.ClearActions()
}

// driftLiveSecret rewrites the live Secret's credential blob to a different
// made-up value, so the full-scope comparison reports a real (sensitive)
// difference.
func driftLiveSecret(t *testing.T, argoClient *fake.Clientset) {
	t.Helper()
	ctx := context.Background()
	live, err := argoClient.CoreV1().Secrets("argocd").Get(ctx, comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the live secret to drift it: %v", err)
	}
	live.Data["config"] = []byte(`{"bearerToken":"a-rotated-made-up-value","tlsClientConfig":{}}`)
	if _, err := argoClient.CoreV1().Secrets("argocd").Update(ctx, live, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("drifting the live secret: %v", err)
	}
}

func newTestCheckLoop(srv *Server) *ConnectionCredentialCheckLoop {
	// The interval never fires in these tests — every pass is an explicit
	// runOnce call, so no test depends on wall-clock timing.
	return NewConnectionCredentialCheckLoop(srv, time.Hour)
}

func countAuditEvents(srv *Server, event string) int {
	n := 0
	for _, e := range srv.AuditLog().List(0) {
		if e.Event == event {
			n++
		}
	}
	return n
}

// TestCredentialCheckFromView_MappingRule pins the mapping rule written in
// credentialCheckFromView's comment: drifted requires something the
// comparison REPORTED; a clean limited-scope pass is not_compared with the
// comparison's own sentence — never drifted, never clear; a failed check
// stays check_failed.
func TestCredentialCheckFromView_MappingRule(t *testing.T) {
	cases := []struct {
		name       string
		view       connectionComparisonView
		wantStatus string
		wantDetail string
	}{
		{
			name:       "synced at full scope is clear",
			view:       connectionComparisonView{Status: string(connectioncompare.StatusSynced), Scope: "full"},
			wantStatus: credentialCheckClear,
			wantDetail: "",
		},
		{
			name:       "limited with nothing wrong is not_compared with the established sentence",
			view:       connectionComparisonView{Status: string(connectioncompare.StatusLimited), Scope: "limited", LimitReason: connectioncompare.LimitReasonEKSNoStoredCredential},
			wantStatus: credentialCheckNotCompared,
			wantDetail: connectioncompare.LimitReasonEKSNoStoredCredential,
		},
		{
			name:       "out_of_sync is drifted at any scope — the comparison reported a real difference",
			view:       connectionComparisonView{Status: string(connectioncompare.StatusOutOfSync), Scope: "limited"},
			wantStatus: credentialCheckDrifted,
			wantDetail: wantDriftedSentence,
		},
		{
			name:       "missing is drifted — the connection is not there, which the comparison saw",
			view:       connectionComparisonView{Status: string(connectioncompare.StatusMissing), Scope: "full"},
			wantStatus: credentialCheckDrifted,
			wantDetail: wantDriftedSentence,
		},
		{
			name:       "ownership_conflict is drifted — another tool holds the connection",
			view:       connectionComparisonView{Status: string(connectioncompare.StatusOwnershipConflict), Scope: "ownership_conflict"},
			wantStatus: credentialCheckDrifted,
			wantDetail: wantDriftedSentence,
		},
		{
			name:       "check_failed stays check_failed with the safe classified sentence",
			view:       connectionComparisonView{Status: string(connectioncompare.StatusCheckFailed), FailureReason: failBackendRead},
			wantStatus: credentialCheckFailed,
			wantDetail: failBackendRead,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, detail := credentialCheckFromView(tc.view)
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
			if detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", detail, tc.wantDetail)
			}
		})
	}
}

// TestConnectionCredentialCheckLoop_FullScope_ClearThenDrifted is the
// headline behavior: a background pass over a matching full-scope connection
// stores clear; a live difference on the next pass stores drifted with the
// product owner's exact sentence.
func TestConnectionCredentialCheckLoop_FullScope_ClearThenDrifted(t *testing.T) {
	srv, _, argoClient := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	seedSyncedLiveSecret(t, srv, argoClient)
	loop := newTestCheckLoop(srv)

	loop.runOnce(context.Background())
	rec, ok := srv.connCredChecks.get(comparisonCluster)
	if !ok {
		t.Fatal("the background pass recorded nothing for the cluster")
	}
	if rec.Status != credentialCheckClear {
		t.Fatalf("status = %q, want clear (detail: %q)", rec.Status, rec.Detail)
	}
	if rec.Detail != "" {
		t.Errorf("a clear record must carry no sentence, got %q", rec.Detail)
	}
	if rec.CheckedAt == "" {
		t.Error("checked-at must be set")
	}
	if _, err := time.Parse(time.RFC3339, rec.CheckedAt); err != nil {
		t.Errorf("checked-at %q is not RFC3339: %v", rec.CheckedAt, err)
	}

	driftLiveSecret(t, argoClient)
	loop.runOnce(context.Background())
	rec, _ = srv.connCredChecks.get(comparisonCluster)
	if rec.Status != credentialCheckDrifted {
		t.Fatalf("status after a live difference = %q, want drifted", rec.Status)
	}
	if rec.Detail != wantDriftedSentence {
		t.Errorf("drifted sentence = %q,\nwant exactly %q", rec.Detail, wantDriftedSentence)
	}
}

// TestConnectionCredentialCheckLoop_EKS_NotComparedAndZeroMint: a background
// pass over an EKS connection stores not_compared with the ESTABLISHED EKS
// sentence, and mints ZERO sign-in tokens.
func TestConnectionCredentialCheckLoop_EKS_NotComparedAndZeroMint(t *testing.T) {
	mint := &eksAPIMintCounter{}
	backend := eksBackendForAPI(t, mint)
	live := liveConnectionSecret(map[string]string{"datadog": "enabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"bearerToken":"a-token-minted-at-some-earlier-moment","tlsClientConfig":{}}`,
	}, nil)
	srv, _, _ := comparisonFixture(t, eksManagedYAML, live, backend)

	newTestCheckLoop(srv).runOnce(context.Background())

	if mint.calls != 0 {
		t.Fatalf(`the background credential check minted %d EKS sign-in token(s); it must mint ZERO.

WHY ZERO IS RIGHT: the write path mints exactly once, when a human asks
Sharko to write a connection. A check never does — the mode policy has
already ruled the EKS credential blob is not comparable (the backend stores
cluster details, not a reusable credential), so a token minted here would be
a real, working credential created on a timer and thrown away, every
interval, for every EKS cluster. If this is above zero, the background pass
now reaches GetCredentials (likely via ConnectionCredentialSpecForWrite,
which is the write path's builder). Find it and take it out; do not raise
the expected count.`, mint.calls)
	}

	rec, ok := srv.connCredChecks.get(comparisonCluster)
	if !ok {
		t.Fatal("the background pass recorded nothing for the EKS cluster")
	}
	if rec.Status != credentialCheckNotCompared {
		t.Fatalf("EKS status = %q, want not_compared — never drifted, never clear (detail: %q)", rec.Status, rec.Detail)
	}
	if rec.Detail != connectioncompare.LimitReasonEKSNoStoredCredential {
		t.Errorf("EKS detail = %q,\nwant the established sentence %q", rec.Detail, connectioncompare.LimitReasonEKSNoStoredCredential)
	}
}

// TestConnectionCredentialCheckLoop_InlineKubeconfig_NotCompared: same rule
// for a pasted-kubeconfig connection — the live Secret is the only copy, so
// there is nothing to compare the credential half against.
func TestConnectionCredentialCheckLoop_InlineKubeconfig_NotCompared(t *testing.T) {
	inlineYAML := "clusters:\n- name: " + comparisonCluster + "\n  credsSource: inline-kubeconfig\n  labels:\n    datadog: enabled\n"
	live := liveConnectionSecret(map[string]string{"datadog": "enabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"bearerToken":"the-pasted-only-copy","tlsClientConfig":{}}`,
	}, nil)
	srv, _, _ := comparisonFixture(t, inlineYAML, live, comparisonFakeVault{})

	newTestCheckLoop(srv).runOnce(context.Background())

	rec, ok := srv.connCredChecks.get(comparisonCluster)
	if !ok {
		t.Fatal("the background pass recorded nothing for the inline cluster")
	}
	if rec.Status != credentialCheckNotCompared {
		t.Fatalf("inline status = %q, want not_compared (detail: %q)", rec.Status, rec.Detail)
	}
	if rec.Detail != wantInlineSentence {
		t.Errorf("inline detail = %q,\nwant the established sentence %q", rec.Detail, wantInlineSentence)
	}
}

// TestConnectionCredentialCheckLoop_BackendUnreachable_CheckFailed: a
// backend hiccup is check_failed with the fixed safe sentence — never read
// as drift, never as health, and the backend's own error text appears
// nowhere.
func TestConnectionCredentialCheckLoop_BackendUnreachable_CheckFailed(t *testing.T) {
	const rawBackendText = "dial tcp 10.9.8.7:8200 refused zz9-raw-provider-text-must-not-escape"
	live := liveConnectionSecret(map[string]string{"datadog": "enabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"bearerToken":"whatever","tlsClientConfig":{}}`,
	}, nil)
	srv, _, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{failWith: errors.New(rawBackendText)})

	newTestCheckLoop(srv).runOnce(context.Background())

	rec, ok := srv.connCredChecks.get(comparisonCluster)
	if !ok {
		t.Fatal("the background pass recorded nothing")
	}
	if rec.Status != credentialCheckFailed {
		t.Fatalf("status = %q, want check_failed — a backend hiccup is never drift and never health (detail: %q)", rec.Status, rec.Detail)
	}
	if rec.Detail != failBackendRead {
		t.Errorf("detail = %q, want the fixed safe sentence %q", rec.Detail, failBackendRead)
	}

	// Sweep every stored surface for the raw provider text.
	blob, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshalling the record: %v", err)
	}
	for _, fragment := range []string{rawBackendText, "10.9.8.7", "zz9-raw-provider-text-must-not-escape"} {
		if strings.Contains(string(blob), fragment) {
			t.Errorf("the stored record carries raw provider error text %q", fragment)
		}
	}
	for _, e := range srv.AuditLog().List(0) {
		entryBlob, _ := json.Marshal(e)
		if strings.Contains(string(entryBlob), "zz9-raw-provider-text-must-not-escape") {
			t.Errorf("an audit entry carries raw provider error text: %s", entryBlob)
		}
	}
}

// TestConnectionCredentialCheckStore_TransitionOnlyAudit: one audit entry
// when drift appears, one when it clears — never repeated while unchanged,
// and a failed check in between neither clears nor re-announces the episode.
func TestConnectionCredentialCheckStore_TransitionOnlyAudit(t *testing.T) {
	workingVault := comparisonFakeVault{}
	srv, _, argoClient := comparisonFixture(t, backendManagedYAML, nil, workingVault)
	seedSyncedLiveSecret(t, srv, argoClient)
	loop := newTestCheckLoop(srv)
	ctx := context.Background()

	const detected = "connection_credential_drift_detected"
	const cleared = "connection_credential_drift_cleared"

	// Clear passes announce nothing.
	loop.runOnce(ctx)
	loop.runOnce(ctx)
	if n := countAuditEvents(srv, detected); n != 0 {
		t.Fatalf("clear passes wrote %d drift-detected entries, want 0", n)
	}

	// Drift appears: exactly ONE entry, however many passes see it.
	driftLiveSecret(t, argoClient)
	loop.runOnce(ctx)
	loop.runOnce(ctx)
	if n := countAuditEvents(srv, detected); n != 1 {
		t.Fatalf("unchanged drift wrote %d drift-detected entries across two passes, want exactly 1", n)
	}
	if n := countAuditEvents(srv, cleared); n != 0 {
		t.Fatalf("drift wrote %d drift-cleared entries, want 0", n)
	}

	// A backend failure mid-episode: the check goes check_failed, and the
	// episode stays open — no cleared entry, and no second detected entry
	// when the backend comes back and the same drift is still there.
	installCredProvider(srv, comparisonFakeVault{failWith: errors.New("backend down")}, nil, nil)
	loop.runOnce(ctx)
	loop.runOnce(ctx)
	if rec, _ := srv.connCredChecks.get(comparisonCluster); rec.Status != credentialCheckFailed {
		t.Fatalf("status during the backend outage = %q, want check_failed", rec.Status)
	}
	if n := countAuditEvents(srv, cleared); n != 0 {
		t.Fatalf("a failed check wrote %d drift-cleared entries, want 0 — a check that could not look clears nothing", n)
	}
	// RULING (f): a genuinely failed check is VISIBLE, with a failure-shaped
	// title — and transition-only, like every other episode here, so a
	// backend that is down for an hour does not write four entries a minute.
	if n := countAuditEvents(srv, "connection_credential_check_failed"); n != 1 {
		t.Fatalf("two failing passes wrote %d check-failed entries, want exactly 1 (it used to write ZERO — the one real failure was invisible)", n)
	}
	installCredProvider(srv, workingVault, nil, nil)
	loop.runOnce(ctx)
	if n := countAuditEvents(srv, detected); n != 1 {
		t.Fatalf("a flapping backend re-announced the same drift: %d detected entries, want still exactly 1", n)
	}
	if n := countAuditEvents(srv, "connection_credential_check_recovered"); n != 1 {
		t.Fatalf("the check completing again wrote %d recovered entries, want exactly 1", n)
	}

	// Drift cleared: exactly one cleared entry, then silence.
	seedSyncedLiveSecret(t, srv, argoClient)
	loop.runOnce(ctx)
	if n := countAuditEvents(srv, cleared); n != 1 {
		t.Fatalf("drift clearing wrote %d drift-cleared entries, want exactly 1", n)
	}
	loop.runOnce(ctx)
	if n := countAuditEvents(srv, cleared); n != 1 {
		t.Fatalf("an unchanged clear wrote another drift-cleared entry (total %d), want still 1", n)
	}
	if n := countAuditEvents(srv, detected); n != 1 {
		t.Fatalf("final detected count = %d, want 1", n)
	}

	// And the background loop never writes the per-check entry the human
	// endpoint writes — Recent activity must not flood every interval.
	if n := countAuditEvents(srv, "secret_resource_read"); n != 0 {
		t.Errorf("the background loop wrote %d per-check (secret_resource_read) audit entries, want 0 — that entry belongs to the human-driven endpoint only", n)
	}

	// RULING (f): every entry this surface wrote says the same thing three
	// ways. Detection is a SUCCESS (the check ran and found something), a
	// real failure is a failure, and all of them are read-only so nothing
	// changed and nothing was going to.
	for _, e := range srv.AuditLog().List(0) {
		switch e.Event {
		case detected, cleared, "connection_credential_check_recovered":
			if e.Result != "success" {
				t.Errorf("%s recorded result %q — a check that ran to completion is a success; the drift state supplies the warning", e.Event, e.Result)
			}
		case "connection_credential_check_failed":
			if e.Result != "failure" {
				t.Errorf("%s recorded result %q, want failure", e.Event, e.Result)
			}
		default:
			continue
		}
		if e.Changes != audit.ChangesNotApplicable {
			t.Errorf("%s recorded changes %q — a read-only check neither changed anything nor failed to", e.Event, e.Changes)
		}
	}
}

// TestConnectionCredentialCheckLoop_NeverWrites: a full background pass
// performs no Kubernetes write of any kind.
func TestConnectionCredentialCheckLoop_NeverWrites(t *testing.T) {
	srv, _, argoClient := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	seedSyncedLiveSecret(t, srv, argoClient) // clears the fake's actions after seeding

	newTestCheckLoop(srv).runOnce(context.Background())

	for _, action := range argoClient.Actions() {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Errorf("the background credential check performed a %q on %s — detection is read-only, always",
				action.GetVerb(), action.GetResource().Resource)
		}
	}
}

// TestManualComparisonEndpoint_UpdatesCredentialCheckStore: a human's
// Check-again click feeds the same store the loop feeds, so the fleet view
// never lags a fresh manual check.
func TestManualComparisonEndpoint_UpdatesCredentialCheckStore(t *testing.T) {
	srv, router, argoClient := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	seedSyncedLiveSecret(t, srv, argoClient)

	if _, ok := srv.connCredChecks.get(comparisonCluster); ok {
		t.Fatal("the store had a record before any check ran")
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	rec, ok := srv.connCredChecks.get(comparisonCluster)
	if !ok {
		t.Fatal("a manual check did not update the shared store")
	}
	if rec.Status != credentialCheckClear {
		t.Errorf("status after a manual check of a matching connection = %q, want clear (detail: %q)", rec.Status, rec.Detail)
	}

	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if rec.CheckedAt != view.CheckedAt {
		t.Errorf("store checked-at %q != the response's checked_at %q — the two surfaces must tell the same time", rec.CheckedAt, view.CheckedAt)
	}
}

// TestManagedSecretsRow_CredentialCheckFields_NoLeak: the fleet row carries
// the status word, the fixed sentence and the timestamp — and NOTHING
// derived from the credential value, even while the store's drift was
// detected on a connection whose real stored token is the sentinel.
func TestManagedSecretsRow_CredentialCheckFields_NoLeak(t *testing.T) {
	srv, router, argoClient := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	seedSyncedLiveSecret(t, srv, argoClient)
	driftLiveSecret(t, argoClient)

	newTestCheckLoop(srv).runOnce(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()

	// The row says what a person needs.
	var resp managedSecretsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	var row *connectionSecretRow
	for i := range resp.ClusterConnectionSecrets {
		if resp.ClusterConnectionSecrets[i].Cluster == comparisonCluster {
			row = &resp.ClusterConnectionSecrets[i]
		}
	}
	if row == nil {
		t.Fatalf("no connection row for %s in %+v", comparisonCluster, resp.ClusterConnectionSecrets)
	}
	if row.CredentialCheck != credentialCheckDrifted {
		t.Fatalf("credential_check = %q, want drifted", row.CredentialCheck)
	}
	if row.CredentialCheckDetail != wantDriftedSentence {
		t.Errorf("credential_check_detail = %q,\nwant exactly %q", row.CredentialCheckDetail, wantDriftedSentence)
	}
	if row.CredentialCheckedAt == "" {
		t.Error("credential_checked_at must be set")
	}

	// And nothing else. The sentinel really is the stored token, so every
	// derived form of it is a real leak if it appears.
	for _, form := range sentinelForms() {
		if strings.Contains(body, form) {
			t.Errorf("the fleet response carries a derived form of the credential value: %q", form)
		}
	}
	for _, form := range sentinelLengthForms() {
		if strings.Contains(body, form) {
			t.Errorf("the fleet response carries the credential's length: %q", form)
		}
	}
	if strings.Contains(body, strconv.Quote(strconv.Itoa(len(comparisonSentinel)))) {
		t.Errorf("the fleet response carries the credential's byte length as a string value")
	}

	// credential_check is a bare status WORD — never an object that could
	// grow expected/live sides.
	var raw struct {
		Rows []map[string]json.RawMessage `json:"cluster_connection_secrets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding raw: %v", err)
	}
	for _, r := range raw.Rows {
		if cc, ok := r["credential_check"]; ok {
			var word string
			if err := json.Unmarshal(cc, &word); err != nil {
				t.Errorf("credential_check is not a plain string: %s", cc)
			}
		}
		for _, banned := range []string{"credential_check_expected", "credential_check_live", "expected", "live"} {
			if _, ok := r[banned]; ok {
				t.Errorf("a connection row carries a %q key — the fleet surface ships no field detail", banned)
			}
		}
	}
}

// TestConnectionCredentialCheckLoop_MissingSecret_IsDrifted: a managed
// cluster whose connection Secret is gone entirely is drift a person must
// hear about — the comparison saw the absence; nothing was invented.
func TestConnectionCredentialCheckLoop_MissingSecret_IsDrifted(t *testing.T) {
	srv, _, _ := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})

	newTestCheckLoop(srv).runOnce(context.Background())

	rec, ok := srv.connCredChecks.get(comparisonCluster)
	if !ok {
		t.Fatal("the background pass recorded nothing")
	}
	if rec.Status != credentialCheckDrifted {
		t.Fatalf("status for a missing connection Secret = %q, want drifted (detail: %q)", rec.Status, rec.Detail)
	}
	if rec.Detail != wantDriftedSentence {
		t.Errorf("detail = %q, want the fixed sentence", rec.Detail)
	}
}
