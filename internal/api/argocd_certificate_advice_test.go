package api

// argocd_certificate_advice_test.go — L16 (epic video-leftovers, 2026-10-18).
//
// The two ArgoCD certificate sentences used to give different advice. The
// connection test said "Give ArgoCD a trusted certificate, or for a test
// setup, install ArgoCD with server.insecure or tick Skip TLS certificate
// verification." The list paths said "Add the certificate authority to the
// ArgoCD connection in Settings", and the connection has no such field. Both
// now give one advice. Every expected sentence here is a literal, and the old
// words are banned.

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/verify"
)

func TestArgoCDCertificateSentences_GiveOneAdvice(t *testing.T) {
	const advice = "Give ArgoCD a trusted certificate, or, for a test setup only, tick Skip TLS certificate verification."
	certErr := &argocd.UnreachableError{Verb: "GET", Endpoint: "/api/v1/clusters", Code: argocd.UnreachableTLSUntrusted}

	got := map[string]string{
		"connection test":   renderConnectionFailure(connectionKindArgoCD, verify.ERR_TLS),
		"list clusters":     argoListFailureSentence(argoReadClusters, certErr),
		"list applications": argoListFailureSentence(argoReadApplications, certErr),
	}
	want := map[string]string{
		"connection test":   "ArgoCD uses a certificate Sharko does not trust. Give ArgoCD a trusted certificate, or, for a test setup only, tick Skip TLS certificate verification.",
		"list clusters":     "Sharko reached ArgoCD but could not verify its certificate, so it did not list clusters. Give ArgoCD a trusted certificate, or, for a test setup only, tick Skip TLS certificate verification. Then try again.",
		"list applications": "Sharko reached ArgoCD but could not verify its certificate, so it did not list applications. Give ArgoCD a trusted certificate, or, for a test setup only, tick Skip TLS certificate verification. Then try again.",
	}
	for name, sentence := range got {
		if sentence != want[name] {
			t.Errorf("%s:\n got: %q\nwant: %q", name, sentence, want[name])
		}
		if !strings.Contains(sentence, advice) {
			t.Errorf("%s does not give the one advice: %q", name, sentence)
		}
		for _, old := range []string{
			"install ArgoCD with server.insecure",
			"server.insecure",
			"Add the certificate authority",
			"certificate authority to the ArgoCD connection",
		} {
			if strings.Contains(sentence, old) {
				t.Errorf("%s still says the old %q: %q", name, old, sentence)
			}
		}
	}

	// The catalog the browser gets holds the same words.
	entry, ok := connectionFailureEntry("connectionFailureArgoCDTLS")
	if !ok || entry.Sentence != want["connection test"] {
		t.Errorf("catalog entry = %q (found %v), want %q", entry.Sentence, ok, want["connection test"])
	}
}
