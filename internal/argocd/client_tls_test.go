package argocd

// client_tls_test.go — task #152 adversarial finding: Sharko used to skip TLS
// certificate verification on its own connection to ArgoCD, always, with no
// way to turn it on. These tests pin the fixed contract:
//
//   1. verification is ON by default — a self-signed ArgoCD is refused, and
//      the refusal names the exact setting that turns it off deliberately;
//   2. insecure=true (the operator's explicit opt-in) still connects;
//   3. the discovery probe verifies by default too, mirrors the opt-in, and
//      sends no credential;
//   4. plain-http endpoints (the common in-cluster case) are unaffected.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tlsTestHandler answers the two endpoints these tests touch and records
// whether any request arrived carrying an Authorization header.
func tlsTestHandler(sawAuthHeader *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			*sawAuthHeader = true
		}
		switch r.URL.Path {
		case "/api/v1/version", "/api/v1/clusters":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// A client built with insecure=false must refuse a server whose certificate
// cannot be verified (httptest's TLS server uses a self-signed certificate),
// and the error must carry the canned sentence naming the fix — not a bare
// handshake failure that sends the operator hunting in the wrong place.
func TestClient_RefusesUnverifiableCertificateByDefault(t *testing.T) {
	sawAuth := false
	ts := httptest.NewTLSServer(tlsTestHandler(&sawAuth))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", false)
	err := c.TestConnection(context.Background())
	if err == nil {
		t.Fatalf("expected a TLS verification error against a self-signed server, got nil")
	}
	if !errors.Is(err, ErrTLSCertificateNotTrusted) {
		t.Fatalf("expected errors.Is(err, ErrTLSCertificateNotTrusted), got: %v", err)
	}
	// The message must tell the operator exactly what to set.
	for _, want := range []string{
		"self-signed",
		"connection.argocd.insecure=true",
		"SHARKO_CONN_ARGOCD_INSECURE",
		"Skip TLS certificate verification",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message must mention %q; got: %v", want, err)
		}
	}
	// The refusal happens during the handshake — the token must never have
	// reached the server.
	if sawAuth {
		t.Errorf("the bearer token was sent to a server whose certificate did not verify")
	}
}

// insecure=true is the operator's explicit opt-in — it must still connect to
// a self-signed server.
func TestClient_InsecureOptInConnects(t *testing.T) {
	sawAuth := false
	ts := httptest.NewTLSServer(tlsTestHandler(&sawAuth))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", true)
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("insecure=true must connect to a self-signed server, got: %v", err)
	}
}

// Plain http (the common in-cluster case) has no certificate to verify —
// insecure=false must not affect it.
func TestClient_PlainHTTPUnaffected(t *testing.T) {
	sawAuth := false
	ts := httptest.NewServer(tlsTestHandler(&sawAuth))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", false)
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("plain-http endpoint must work with insecure=false, got: %v", err)
	}
}

// The discovery probe must verify certificates by default (so discovery never
// reports an https URL the real client would refuse), must honor the explicit
// opt-in, and must never send a credential.
func TestProbeArgoCD_TLSStanceAndNoCredential(t *testing.T) {
	sawAuth := false
	tlsSrv := httptest.NewTLSServer(tlsTestHandler(&sawAuth))
	defer tlsSrv.Close()

	if probeArgoCD(tlsSrv.URL, false) {
		t.Errorf("probe with insecure=false must refuse a self-signed server")
	}
	if !probeArgoCD(tlsSrv.URL, true) {
		t.Errorf("probe with insecure=true (explicit opt-in) must accept the self-signed server")
	}

	plainSrv := httptest.NewServer(tlsTestHandler(&sawAuth))
	defer plainSrv.Close()
	if !probeArgoCD(plainSrv.URL, false) {
		t.Errorf("probe must succeed against a plain-http ArgoCD without any opt-in")
	}

	if sawAuth {
		t.Errorf("the discovery probe must never send an Authorization header")
	}
}
