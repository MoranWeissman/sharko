package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/models"
)

// audit_blast_radius_test.go — P3-F1. Three things the audit trail was
// getting wrong, each pinned here:
//
//   1. an act that checked every cluster said "every" and never said how
//      many, so nobody reading the log could tell what it touched;
//   2. the "Refresh all" entry on the values engine carried no resource at
//      all — an entry about nothing;
//   3. the per-row "last repaired" join walked the whole audit ring once
//      per row.

func TestBlastRadiusSentences(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"clusters, unknown count", clusterCheckBlastRadius(0), "read-only check of every cluster's connection secret — nothing is written"},
		{"clusters, one", clusterCheckBlastRadius(1), "read-only check of 1 cluster's connection secret — nothing is written"},
		{"clusters, many", clusterCheckBlastRadius(12), "read-only check of all 12 clusters' connection secrets — nothing is written"},
		{"values, unknown count", valuesCheckBlastRadius(0), "read-only check of every addon-values secret — nothing is written"},
		{"values, one", valuesCheckBlastRadius(1), "read-only check of 1 addon-values secret — nothing is written"},
		{"values, many", valuesCheckBlastRadius(37), "read-only check of all 37 addon-values secrets — nothing is written"},
		{"values write pass says it writes", valuesReconcileBlastRadius(4), "full pass over all 4 addon-values secrets — values are pushed where they differ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestBlastRadiusNeverClaimsZero pins the one number that must never be
// printed. A count of 0 means "no pass has run on this server instance
// yet", never "there is nothing to check" — printing "all 0 clusters"
// would read as a claim that Sharko manages nothing.
func TestBlastRadiusNeverClaimsZero(t *testing.T) {
	for _, s := range []string{clusterCheckBlastRadius(0), valuesCheckBlastRadius(0), valuesReconcileBlastRadius(0)} {
		if strings.Contains(s, "0") {
			t.Errorf("an unknown count must not print a number; got %q", s)
		}
	}
}

// TestRefreshAllEntry_NamesTheSurfaceAndTheBlastRadius is the "Refresh all"
// pin: the entry the values engine leaves behind names what it acted on and
// how much of it.
func TestRefreshAllEntry_NamesTheSurfaceAndTheBlastRadius(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{knownItemCount: 37})
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets/check", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}

	e := findAuditEntry(t, srv, "addon_values_secret_check_triggered")
	if e.Resource != resourceAddonValuesSecrets {
		t.Errorf("resource = %q, want %q — an entry about nothing is not an audit entry", e.Resource, resourceAddonValuesSecrets)
	}
	if e.Detail != valuesCheckBlastRadius(37) {
		t.Errorf("detail = %q, want the real blast radius %q", e.Detail, valuesCheckBlastRadius(37))
	}
}

func TestReconcileTriggerEntry_NamesTheSurfaceAndSaysItWrites(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{knownItemCount: 4})
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets/reconcile", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}

	e := findAuditEntry(t, srv, "reconcile_triggered")
	if e.Resource != resourceAddonValuesSecrets {
		t.Errorf("resource = %q, want %q", e.Resource, resourceAddonValuesSecrets)
	}
	// The write pass and the check must not read the same in the log —
	// that difference is exactly what an auditor is looking for.
	if !strings.Contains(e.Detail, "pushed") {
		t.Errorf("the write pass's entry must say it writes; got %q", e.Detail)
	}
}

func findAuditEntry(t *testing.T, srv *Server, event string) audit.Entry {
	t.Helper()
	for _, e := range srv.AuditLog().List(0) {
		if e.Event == event {
			return e
		}
	}
	t.Fatalf("no audit entry with event %q; got %+v", event, srv.AuditLog().List(0))
	return audit.Entry{}
}

// ---------------------------------------------------------------------------
// The per-row repair join
// ---------------------------------------------------------------------------

// TestRepairIndex_MatchesTheOldPerRowScan pins that turning rows×ring into
// one map build did not change a single answer: for every row, the index
// returns exactly what a fresh newest-first scan of the whole ring would.
// The reference scan below is the OLD implementation, kept here on purpose
// as the thing the fast path is measured against.
func TestRepairIndex_MatchesTheOldPerRowScan(t *testing.T) {
	base := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	// Newest first, the order audit.Log.List returns.
	entries := []audit.Entry{
		{Event: "cluster_secret_managed_self_heal", Resource: "cluster:prod-eu", Result: "success", Timestamp: base},
		{Event: "addon_secret_updated", Resource: "cluster:prod-eu/addon:datadog", Result: "success", Timestamp: base.Add(-time.Minute)},
		{Event: "cluster_secret_create", Resource: "cluster:prod-eu", Result: "success", Timestamp: base.Add(-2 * time.Minute)},
		// A failure must never count as a repair.
		{Event: "cluster_secret_create", Resource: "cluster:staging-us", Result: "failure", Timestamp: base.Add(-3 * time.Minute)},
		// Neither must an event that isn't a write.
		{Event: "cluster_connection_secret_check_triggered", Resource: "cluster:staging-us", Result: "success", Timestamp: base.Add(-4 * time.Minute)},
		// Nor the new live-read entry.
		{Event: "secret_resource_read", Resource: "cluster:staging-us", Result: "success", Timestamp: base.Add(-5 * time.Minute)},
		{Event: "addon_secret_created", Resource: "cluster:staging-us/addon:grafana", Result: "success", Timestamp: base.Add(-6 * time.Minute)},
	}

	idx := buildRepairIndex(entries)

	for _, cluster := range []string{"prod-eu", "staging-us", "never-heard-of-it"} {
		wantAt, wantDetail, wantOK := referenceConnectionRepairScan(entries, cluster)
		got, gotOK := idx.lastConnectionSecretRepair(cluster)
		if gotOK != wantOK || (wantOK && (!got.At.Equal(wantAt) || got.Detail != wantDetail)) {
			t.Errorf("connection %q: index gave (%+v, %v), the old scan gave (%v, %q, %v)",
				cluster, got, gotOK, wantAt, wantDetail, wantOK)
		}
	}

	pairs := [][2]string{{"prod-eu", "datadog"}, {"staging-us", "grafana"}, {"prod-eu", "grafana"}}
	for _, p := range pairs {
		wantAt, wantDetail, wantOK := referenceValuesRepairScan(entries, p[0], p[1])
		got, gotOK := idx.lastAddonValuesSecretRepair(p[0], p[1])
		if gotOK != wantOK || (wantOK && (!got.At.Equal(wantAt) || got.Detail != wantDetail)) {
			t.Errorf("values %q/%q: index gave (%+v, %v), the old scan gave (%v, %q, %v)",
				p[0], p[1], got, gotOK, wantAt, wantDetail, wantOK)
		}
	}

	// Newest wins: prod-eu has two repairs in the ring.
	if got, _ := idx.lastConnectionSecretRepair("prod-eu"); got.Detail != "labels corrected" {
		t.Errorf("the most recent repair must win; got %q", got.Detail)
	}
}

// TestRepairIndex_KeepsTheTwoKindsApart pins the reason the index holds two
// maps instead of one: a values event that somehow lands on a
// connection-shaped resource must not fill in a connection row's answer.
func TestRepairIndex_KeepsTheTwoKindsApart(t *testing.T) {
	idx := buildRepairIndex([]audit.Entry{
		{Event: "addon_secret_created", Resource: "cluster:prod-eu", Result: "success", Timestamp: time.Now()},
	})
	if _, ok := idx.lastConnectionSecretRepair("prod-eu"); ok {
		t.Error("a values event must never answer a connection row's last-repaired question")
	}
}

// TestRepairIndex_WalksTheRingOnce is the performance pin, written as a
// correctness one: the whole page costs ONE pass over the ring however many
// rows it has. It is expressed as "the index is built once and every row is
// a map lookup" — buildRepairIndex is the only thing that touches entries.
func TestRepairIndex_WalksTheRingOnce(t *testing.T) {
	entries := make([]audit.Entry, 0, 1000)
	for i := 0; i < 1000; i++ {
		entries = append(entries, audit.Entry{
			Event:     "cluster_secret_create",
			Resource:  "cluster:c" + string(rune('a'+i%26)),
			Result:    "success",
			Timestamp: time.Now().Add(-time.Duration(i) * time.Second),
		})
	}
	idx := buildRepairIndex(entries)
	if len(idx.connection) != 26 {
		t.Fatalf("index holds %d connection resources, want 26 (one per distinct resource)", len(idx.connection))
	}
	for i := 0; i < 26; i++ {
		if _, ok := idx.lastConnectionSecretRepair("c" + string(rune('a'+i))); !ok {
			t.Fatalf("cluster c%c must resolve from the index", 'a'+i)
		}
	}
}

func referenceConnectionRepairScan(entries []audit.Entry, clusterName string) (time.Time, string, bool) {
	want := "cluster:" + clusterName
	for _, e := range entries {
		if e.Resource != want || e.Result != "success" {
			continue
		}
		if d, isRepair := connectionSecretRepairDetail[e.Event]; isRepair {
			return e.Timestamp, d, true
		}
	}
	return time.Time{}, "", false
}

func referenceValuesRepairScan(entries []audit.Entry, clusterName, addonName string) (time.Time, string, bool) {
	want := "cluster:" + clusterName + "/addon:" + addonName
	for _, e := range entries {
		if e.Resource != want || e.Result != "success" {
			continue
		}
		if d, isRepair := addonValuesSecretRepairDetail[e.Event]; isRepair {
			return e.Timestamp, d, true
		}
	}
	return time.Time{}, "", false
}

// TestBuildRowsUseTheIndex pins that the row builders actually read the
// prebuilt index — a "last repaired" that silently stopped being filled in
// would otherwise pass every test above.
func TestBuildRowsUseTheIndex(t *testing.T) {
	at := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	idx := buildRepairIndex([]audit.Entry{
		{Event: "cluster_secret_create", Resource: "cluster:prod-eu", Result: "success", Timestamp: at},
	})

	srv := newTestServer()
	rows := srv.buildConnectionSecretRows(t.Context(), []models.Cluster{{Name: "prod-eu", Managed: true}}, idx)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].LastRepaired != at.UTC().Format(time.RFC3339) {
		t.Errorf("last_repaired = %q, want %q", rows[0].LastRepaired, at.UTC().Format(time.RFC3339))
	}
	if rows[0].LastRepairedDetail != "secret created" {
		t.Errorf("last_repaired_detail = %q, want %q", rows[0].LastRepairedDetail, "secret created")
	}
}
