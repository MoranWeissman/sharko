package api

// system_managed_secrets_canonical_test.go — B5. The fleet list obeys the
// reconciliation contract.
//
// The defect these tests exist to prevent, in the product owner's words: the
// fleet rendered a green "Synced" with a quiet "Not compared" chip for a
// legacy pasted-credential connection, while that same connection's own page
// said "Verification incomplete". Two surfaces, two answers, one connection.
//
// So: every row's state is now a projection of the ONE canonical answer, the
// display words come from the server, and the row builder itself refuses to
// emit a synced row that was not fully verified — even if something upstream
// regresses.

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// fleetRowFor drives the REAL path: a finished comparison goes into the
// shared credential-check store through record() — the same call the
// background loop and every page open make — and the row builder reads it
// back. Nothing is hand-injected past the seam under test.
func fleetRowFor(t *testing.T, srv *Server, cluster models.Cluster, view *connectionComparisonView) connectionSecretRow {
	t.Helper()
	if view != nil {
		srv.connCredChecks.record(cluster.Name, *view)
	}
	rows := srv.buildConnectionSecretRows(context.Background(), []models.Cluster{cluster}, repairIndex{}, false)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 connection row, got %d", len(rows))
	}
	return rows[0]
}

func fleetCluster(name string) models.Cluster {
	return models.Cluster{Name: name, Managed: true, ConnectionStatus: "Successful"}
}

// fleetView builds a finished comparison for the fleet tests. Same baseline
// as the reconciliation tests use, so the two surfaces are provably fed the
// same shapes.
func fleetView(cluster string, mut func(*connectionComparisonView)) connectionComparisonView {
	v := reconView(mut)
	v.Cluster = cluster
	return v
}

// ============================================================================
// The four management modes. The required fleet behaviour, row by row.
// ============================================================================

func TestFleetRow_FourManagementModes(t *testing.T) {
	cases := []struct {
		name string
		// cluster is the git-side record the list already holds.
		cluster models.Cluster
		view    func(*connectionComparisonView)

		wantState        string
		wantSyncState    string
		wantVerification string
		wantMode         string
		wantHeadline     string
		wantQualifier    string
	}{
		{
			name:    "sharko-managed, fully compared and matching",
			cluster: fleetCluster("prod-eu"),
			view:    func(v *connectionComparisonView) {},

			wantState:        "in_sync",
			wantSyncState:    syncStateSynced,
			wantVerification: verificationScopeFull,
			wantMode:         managementModeSharkoManaged,
			wantHeadline:     headlineConnectionSynced,
		},
		{
			// The EKS row. ArgoCD may well report the connection as working
			// and the configuration may well match — but the credential
			// content was never compared, so the row is NOT synced and does
			// NOT carry the in_sync word the "Synced" filter counts.
			name:    "EKS, clean, credential content not compared",
			cluster: fleetCluster("spoke-eks"),
			view: func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.OwnershipMode = string(connectioncompare.ModeEKSToken)
				v.CredentialSourceType = models.CredsSourceEKSToken
				v.LimitReason = connectioncompare.LimitReasonEKSNoStoredCredential
				v.NotChecked = []connectionComparisonNotChecked{{Path: "data.config", Reason: "no stored credential"}}
			},

			wantState:        "unknown",
			wantSyncState:    syncStateUnknown,
			wantVerification: verificationScopePartial,
			wantMode:         managementModeSharkoManaged,
			wantHeadline:     headlineConfigurationMatchesEKS,
		},
		{
			// THE ROW THAT SHIPPED THE DEFECT. spoke-us: a legacy pasted
			// credential, clean in the part Sharko could look at. Row 12 of
			// the matrix, and the reversal the product owner called a
			// release blocker.
			name:    "legacy inline, present and clean",
			cluster: models.Cluster{Name: "spoke-us", Managed: true, ConnectionStatus: "Successful", CredsSource: models.CredsSourceInlineKubeconfig},
			view: func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.OwnershipMode = string(connectioncompare.ModeInlineKubeconfig)
				v.CredentialSourceType = models.CredsSourceInlineKubeconfig
				v.LimitReason = "the inline limit sentence"
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
			},

			wantState:        "unknown",
			wantSyncState:    syncStateUnknown,
			wantVerification: verificationScopePartial,
			wantMode:         managementModeLegacyInline,
			wantHeadline:     headlineVerificationIncomplete,
			wantQualifier:    qualifierLegacyInline,
		},
		{
			name:    "self-managed, every owned addon label compared and matching",
			cluster: models.Cluster{Name: "guest", Managed: true, ConnectionStatus: "Successful", ConnectionManagedBy: models.ConnectionManagedByUser},
			view: func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				v.OwnershipMode = string(connectioncompare.ModeSelfManaged)
				v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
			},

			wantState:        "in_sync",
			wantSyncState:    syncStateSynced,
			wantVerification: verificationScopeFull,
			wantMode:         managementModeSelfManaged,
			wantHeadline:     headlineAddonLabelsSynced,
			wantQualifier:    qualifierSelfManaged,
		},
		{
			name:    "foreign owned",
			cluster: fleetCluster("someone-elses"),
			view: func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOwnershipConflict)
				v.Scope = string(connectioncompare.ScopeOwnershipConflict)
				v.OwnershipMode = string(connectioncompare.ModeForeignOwned)
				v.LimitReason = modeStatementForeignOwned
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
			},

			wantState:        "foreign",
			wantSyncState:    syncStateBlocked,
			wantVerification: verificationScopeNone,
			wantMode:         managementModeForeignOwned,
			wantHeadline:     headlineBlocked,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer()
			v := fleetView(tc.cluster.Name, tc.view)
			row := fleetRowFor(t, srv, tc.cluster, &v)

			if row.State != tc.wantState {
				t.Errorf("state = %q, want %q", row.State, tc.wantState)
			}
			if row.SyncState != tc.wantSyncState {
				t.Errorf("sync_state = %q, want %q", row.SyncState, tc.wantSyncState)
			}
			if row.VerificationScope != tc.wantVerification {
				t.Errorf("verification_scope = %q, want %q", row.VerificationScope, tc.wantVerification)
			}
			if row.ManagementMode != tc.wantMode {
				t.Errorf("management_mode = %q, want %q", row.ManagementMode, tc.wantMode)
			}
			if row.Headline != tc.wantHeadline {
				t.Errorf("headline = %q, want %q", row.Headline, tc.wantHeadline)
			}
			if row.Qualifier != tc.wantQualifier {
				t.Errorf("qualifier = %q, want %q", row.Qualifier, tc.wantQualifier)
			}

			// THE INVARIANT, on the fleet row: the in_sync word the "Synced"
			// filter and the "Synced" count both key off may only ever ride
			// with a full verification of a scope Sharko actually owns.
			if row.State == "in_sync" && (row.VerificationScope != verificationScopeFull || row.ManagedScope == managedScopeNone) {
				t.Fatalf("the fleet row claims in_sync at verification_scope=%q managed_scope=%q", row.VerificationScope, row.ManagedScope)
			}
			if row.Headline == "Synced" {
				t.Fatal(`the fleet row rendered bare "Synced"`)
			}
			// Health is a separate fact and never rolls into the git state.
			if row.Health != healthStateConnected {
				t.Errorf("health = %q, want connected — health is independent of the git state", row.Health)
			}
		})
	}
}

// TestFleetRow_NotCheckedYetIsHonest pins the consequence of one canonical
// derivation, stated up front rather than discovered in a browser: a cluster
// no check has reached has no verdict, and the row says exactly that instead
// of the cheerful green it used to show off the reconciler's addon-label-only
// record.
func TestFleetRow_NotCheckedYetIsHonest(t *testing.T) {
	srv := newTestServer()
	row := fleetRowFor(t, srv, fleetCluster("never-looked-at"), nil)

	if row.State != "unknown" {
		t.Errorf("state = %q, want unknown", row.State)
	}
	if row.SyncState != syncStateUnknown {
		t.Errorf("sync_state = %q, want unknown", row.SyncState)
	}
	if row.VerificationScope != verificationScopeNone {
		t.Errorf("verification_scope = %q, want none", row.VerificationScope)
	}
	if row.Headline != headlineNotCheckedYet {
		t.Errorf("headline = %q, want %q", row.Headline, headlineNotCheckedYet)
	}
	if row.CredentialCheck != "" || row.CredentialCheckedAt != "" {
		t.Errorf("a never-checked row invented a credential-check outcome: %q / %q", row.CredentialCheck, row.CredentialCheckedAt)
	}

	// The management mode still comes from the git record — a git-declared
	// fact, not a comparison result.
	inline := models.Cluster{Name: "inline-never-checked", Managed: true, CredsSource: models.CredsSourceInlineKubeconfig}
	if got := fleetRowFor(t, srv, inline, nil).ManagementMode; got != managementModeLegacyInline {
		t.Errorf("management_mode = %q, want legacy_inline from the git record", got)
	}
	guest := models.Cluster{Name: "guest-never-checked", Managed: true, ConnectionManagedBy: models.ConnectionManagedByUser}
	if got := fleetRowFor(t, srv, guest, nil).ManagementMode; got != managementModeSelfManaged {
		t.Errorf("management_mode = %q, want self_managed from the git record", got)
	}
	// foreign_owned can never be claimed without reading the live Secret.
	for _, c := range []models.Cluster{inline, guest, fleetCluster("plain")} {
		if got := fleetRowFor(t, srv, c, nil).ManagementMode; got == managementModeForeignOwned {
			t.Errorf("%s: claimed foreign_owned with no comparison to back it", c.Name)
		}
	}
}

// ============================================================================
// The break test. This is what must fail when someone restores green Synced.
// ============================================================================

// TestFleetRow_SyncedWithoutFullVerificationIsStructurallyImpossible is the
// fail-closed guard's own test (dispatch task 4). It hands the row builder a
// canonical state that VIOLATES the invariant — exactly the shape a future
// upstream regression would produce, and exactly the shape the product
// owner's break test will try to restore — and proves the row still refuses
// to say synced in either vocabulary.
//
// Delete the guard in applyConnectionRowCanonical and this test fails.
func TestFleetRow_SyncedWithoutFullVerificationIsStructurallyImpossible(t *testing.T) {
	violations := []struct {
		name string
		st   connectionCanonicalState
	}{
		{
			"legacy inline claiming synced at partial scope — the exact defect",
			connectionCanonicalState{
				ManagementMode: managementModeLegacyInline, ManagedScope: managedScopeAddonLabels,
				SyncState: syncStateSynced, VerificationScope: verificationScopePartial,
				CheckedAt: "2026-08-19T10:00:00Z", Headline: headlineConnectionSynced,
			},
		},
		{
			"EKS claiming synced at partial scope",
			connectionCanonicalState{
				ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection,
				SyncState: syncStateSynced, VerificationScope: verificationScopePartial,
				CheckedAt: "2026-08-19T10:00:00Z", Headline: headlineConnectionSynced,
			},
		},
		{
			"synced with nothing verified at all",
			connectionCanonicalState{
				ManagementMode: managementModeSharkoManaged, ManagedScope: managedScopeFullConnection,
				SyncState: syncStateSynced, VerificationScope: verificationScopeNone,
				CheckedAt: "2026-08-19T10:00:00Z", Headline: headlineConnectionSynced,
			},
		},
		{
			"synced on a connection Sharko owns nothing on",
			connectionCanonicalState{
				ManagementMode: managementModeForeignOwned, ManagedScope: managedScopeNone,
				SyncState: syncStateSynced, VerificationScope: verificationScopeFull,
				CheckedAt: "2026-08-19T10:00:00Z", Headline: headlineConnectionSynced,
			},
		},
	}

	for _, tc := range violations {
		t.Run(tc.name, func(t *testing.T) {
			row := connectionSecretRow{Cluster: "c"}
			applyConnectionRowCanonical(&row, tc.st)

			if row.SyncState == syncStateSynced {
				t.Errorf("sync_state = synced at verification_scope=%q managed_scope=%q", row.VerificationScope, row.ManagedScope)
			}
			if row.State == "in_sync" {
				t.Errorf(`state = in_sync — this row would be rendered green and counted under "Synced"`)
			}
			if strings.Contains(row.Headline, "synced") || strings.Contains(row.Headline, "Synced") {
				t.Errorf("headline = %q — it still reads as synchronized", row.Headline)
			}
		})
	}
}

// TestFleetRow_LegacyInlineNeverCountsAsSynced walks the full comparison ->
// store -> row path for the real spoke-us shape and pins the two facts the
// ruling names: the row is not rendered synced, and it does not match the
// ?state=in_sync filter the "Synced" count is built from.
func TestFleetRow_LegacyInlineNeverCountsAsSynced(t *testing.T) {
	srv := newTestServer()
	cluster := models.Cluster{
		Name: "spoke-us", Managed: true,
		ConnectionStatus: "Successful",
		CredsSource:      models.CredsSourceInlineKubeconfig,
	}
	v := fleetView(cluster.Name, func(v *connectionComparisonView) {
		v.Status = string(connectioncompare.StatusLimited)
		v.Scope = string(connectioncompare.ScopeLimited)
		v.OwnershipMode = string(connectioncompare.ModeInlineKubeconfig)
		v.CredentialSourceType = models.CredsSourceInlineKubeconfig
		v.LimitReason = "the inline limit sentence"
		v.RepairScope = string(connectioncompare.RepairScopeAddonLabelsOnly)
	})
	row := fleetRowFor(t, srv, cluster, &v)

	if row.State == "in_sync" {
		t.Fatal("the legacy pasted-credential row is green again — the release blocker is back")
	}
	if row.CredentialCheck != credentialCheckNotCompared {
		t.Errorf("credential_check = %q, want not_compared", row.CredentialCheck)
	}
	// ArgoCD says the connection works. That is CORRECT beside an
	// unverifiable git state, and must not pull the git state green.
	if row.Health != healthStateConnected {
		t.Errorf("health = %q, want connected", row.Health)
	}

	// The merged Rows array, the ?state= filter and the counts all read the
	// same projected word, so they cannot disagree with the row.
	merged := buildManagedSecretRows([]connectionSecretRow{row}, nil, nil)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged row, got %d", len(merged))
	}
	if merged[0].State != row.State {
		t.Errorf("merged row state = %q, row state = %q — the two must be the same word", merged[0].State, row.State)
	}
	synced := filterManagedSecretRows(merged, managedSecretRowFilters{State: "in_sync"})
	if len(synced) != 0 {
		t.Fatalf(`the legacy pasted-credential row is counted under the "Synced" filter: %+v`, synced)
	}
}

// TestFleetRow_HealthIsSeparateAndSharedWithThePage pins the two independent
// questions the fleet must now answer, and that health uses the SAME mapping
// the connection page does — one function, so the two surfaces cannot phrase
// the same cluster's health differently.
func TestFleetRow_HealthIsSeparateAndSharedWithThePage(t *testing.T) {
	cases := []struct{ argoState, want string }{
		{"Successful", healthStateConnected},
		{"Failed", healthStateUnavailable},
		{"Unknown", healthStateNotChecked},
		{"", healthStateNotChecked},
		// Sharko's own marker for a git-listed cluster ArgoCD has no entry
		// for. The page answers not_checked for that too (its ArgoCD lookup
		// errors out), so the row must as well.
		{"missing", healthStateNotChecked},
	}
	srv := newTestServer()
	for _, tc := range cases {
		cluster := models.Cluster{Name: "c", Managed: true, ConnectionStatus: tc.argoState}
		row := fleetRowFor(t, srv, cluster, nil)
		if row.Health != tc.want {
			t.Errorf("ConnectionStatus %q -> health %q, want %q", tc.argoState, row.Health, tc.want)
		}
		if got := argoHealthWordFor(tc.argoState); got != row.Health {
			t.Errorf("the row did not use the shared mapping: %q vs %q", row.Health, got)
		}
	}
}

// ============================================================================
// Zero new I/O, and structurally zero-mint.
// ============================================================================

// exploderCredProvider fails the test if the fleet row path so much as looks
// at the credentials backend. Every method is a hard failure — including the
// ones that mint (GetCredentials on an EKS record is the mint path).
type exploderCredProvider struct{ t *testing.T }

func (p exploderCredProvider) GetCredentials(name string) (*providers.Kubeconfig, error) {
	p.t.Fatalf("the fleet row path called GetCredentials(%q) — that is a backend read, and on an EKS record it MINTS a sign-in token", name)
	return nil, nil
}

func (p exploderCredProvider) StoredConnectionFacts(name string) (*providers.StoredConnectionFacts, error) {
	p.t.Fatalf("the fleet row path called StoredConnectionFacts(%q) — a new backend read per row", name)
	return nil, nil
}

func (p exploderCredProvider) ListClusters() ([]providers.ClusterInfo, error) {
	p.t.Fatal("the fleet row path called ListClusters on the credentials backend")
	return nil, nil
}

func (p exploderCredProvider) SearchSecrets(_ string) ([]string, error) {
	p.t.Fatal("the fleet row path called SearchSecrets on the credentials backend")
	return nil, nil
}

func (p exploderCredProvider) HealthCheck(_ context.Context) error {
	p.t.Fatal("the fleet row path called HealthCheck on the credentials backend")
	return nil
}

// TestFleetRow_AddsNoIOAndCannotMint is the structural half of the promise
// this round makes: the fleet list stayed cheap and stayed zero-mint.
//
// The row builder gets a credentials provider that fails the test on every
// method, and a server with no git provider and no ArgoCD client configured
// at all — so a git read or an ArgoCD call would have to fail loudly rather
// than pass quietly. Fifty rows are built. The canonical fields are still
// filled in, because every one of them comes from a pure mapping over a
// comparison that already ran somewhere else.
func TestFleetRow_AddsNoIOAndCannotMint(t *testing.T) {
	srv := newTestServer()
	installCredProvider(srv, exploderCredProvider{t: t}, nil, nil)

	clusters := make([]models.Cluster, 0, 50)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		for i := 0; i < 10; i++ {
			c := models.Cluster{Name: name + string(rune('0'+i)), Managed: true, ConnectionStatus: "Successful"}
			// Half the rows carry a recorded EKS comparison — the shape that
			// mints if anything on this path ever reaches for a credential.
			if i%2 == 0 {
				c.CredsSource = models.CredsSourceEKSToken
				v := fleetView(c.Name, func(v *connectionComparisonView) {
					v.Status = string(connectioncompare.StatusLimited)
					v.Scope = string(connectioncompare.ScopeLimited)
					v.OwnershipMode = string(connectioncompare.ModeEKSToken)
					v.CredentialSourceType = models.CredsSourceEKSToken
					v.LimitReason = connectioncompare.LimitReasonEKSNoStoredCredential
				})
				srv.connCredChecks.record(c.Name, v)
			}
			clusters = append(clusters, c)
		}
	}

	rows := srv.buildConnectionSecretRows(context.Background(), clusters, repairIndex{}, false)
	if len(rows) != 50 {
		t.Fatalf("expected 50 rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.SyncState == "" || row.VerificationScope == "" || row.Headline == "" {
			t.Fatalf("row %q came back without a canonical answer: %+v", row.Cluster, row)
		}
		if row.SyncState == syncStateSynced && row.VerificationScope != verificationScopeFull {
			t.Fatalf("row %q claims synced at %q", row.Cluster, row.VerificationScope)
		}
	}
}
