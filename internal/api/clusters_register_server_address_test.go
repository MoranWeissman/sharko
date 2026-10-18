package api

// clusters_register_server_address_test.go — LO-9 R11. A cluster address
// with a user name, password, query or fragment in it is refused with 400
// and a plain sentence that does not repeat those parts.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

func TestRegisterCluster_ServerAddressWithCredentialAnswers400(t *testing.T) {
	fake := &recordingCredProvider{kc: &providers.Kubeconfig{Server: "https://alice:s3cret@kube.example:6443?token=tok3n"}}
	srv := newRegisterBackendTestServer(t, fake)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":         "prod-eu",
		"creds_source": "secret-kubeconfig",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	const want = "The cluster address in these credentials has a user name, password, query or fragment in it. Sharko does not register a cluster with an address like that, because a password or token can hide in those parts. Use the address without them: https://kube.example:6443"
	if got := decodeError(t, w); got != want {
		t.Errorf("error:\n got: %q\nwant: %q", got, want)
	}
	for _, part := range []string{"alice", "s3cret", "tok3n"} {
		if strings.Contains(w.Body.String(), part) {
			t.Errorf("the answer repeats %q: %s", part, w.Body.String())
		}
	}
}
