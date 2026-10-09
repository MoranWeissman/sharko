package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// V2-cleanup-55.1 — regression tests for the raw-cluster-name
// credential-fetch bug class, pinned at the Diagnose endpoint (the live
// repro: cluster "moran" stored secret_path=sharko-smoke-target-1-kubeconfig
// in managed-clusters.yaml, but POST /clusters/moran/diagnose fetched AWS SM
// secret "moran" and 502'd while the register/Test path resolved correctly).
//
// Contract pinned here:
//  1. When the stored record has a secretPath override, the credentials
//     provider receives the secretPath — never the raw cluster name.
//  2. When no secretPath is stored, the provider receives the plain name
//     (byte-identical to the pre-resolver behavior).

const diagnoseCredLookupManagedClusters = `clusters:
  - name: moran
    secretPath: sharko-smoke-target-1-kubeconfig
    labels: {}
  - name: no-override
    labels: {}
`

// newDiagnoseCredLookupServer wires an isolated Server whose active Git
// provider serves a managed-clusters.yaml with a secretPath override, and
// whose credProvider records every lookup then fails (the assertion is the
// lookup key, not a live cluster connection).
func newDiagnoseCredLookupServer(t *testing.T) (*Server, *recordingCredProvider) {
	t.Helper()
	srv := newIsolatedTestServer(t)
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(diagnoseCredLookupManagedClusters),
	}})
	fake := &recordingCredProvider{err: errors.New("stub credentials backend — lookup key is the assertion")}
	installCredProvider(srv, fake, nil, nil)
	return srv, fake
}

func postDiagnose(t *testing.T, srv *Server, cluster string) int {
	t.Helper()
	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+cluster+"/diagnose", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code
}

func TestDiagnose_StoredSecretPathWins_ProviderNeverSeesRawName(t *testing.T) {
	srv, fake := newDiagnoseCredLookupServer(t)

	code := postDiagnose(t, srv, "moran")

	// The fake provider errors, so the handler must 502 — but only AFTER
	// asking the provider for the RESOLVED key.
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (fake provider always errors)", code)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("GetCredentials calls = %v, want exactly one lookup", fake.calls)
	}
	if fake.calls[0] != "sharko-smoke-target-1-kubeconfig" {
		t.Errorf("GetCredentials received %q, want the stored secret_path %q (raw cluster name must never reach the provider)",
			fake.calls[0], "sharko-smoke-target-1-kubeconfig")
	}
}

func TestDiagnose_NoSecretPath_ProviderReceivesPlainName(t *testing.T) {
	srv, fake := newDiagnoseCredLookupServer(t)

	if code := postDiagnose(t, srv, "no-override"); code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (fake provider always errors)", code)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "no-override" {
		t.Errorf("GetCredentials calls = %v, want exactly [no-override] (fallback must stay the plain name)", fake.calls)
	}
}

func TestDiagnose_ClusterNotInManagedClusters_FallsBackToName(t *testing.T) {
	srv, fake := newDiagnoseCredLookupServer(t)

	if code := postDiagnose(t, srv, "ghost"); code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (fake provider always errors)", code)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "ghost" {
		t.Errorf("GetCredentials calls = %v, want exactly [ghost] (unknown cluster falls back to its name)", fake.calls)
	}
}
