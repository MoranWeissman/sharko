package config

// connection_gitnative_argocd_address_test.go — BF8. The Git-declared ArgoCD
// address goes through the same one rule the API save door uses.
//
// This door matters more than it looks. A declared address is committed to the
// operator's GitOps repository and rendered into the Deployment's environment,
// so a credential written into it is a credential in Git, read by everyone who
// can read the repo.

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestDeclaredArgocdAddress_WithACredentialIsNotApplied(t *testing.T) {
	const saved = "https://argocd.example"

	for _, carrier := range []struct{ name, url string }{
		{"password slot", "https://x-access-token:tok@argocd.other"},
		{"username slot", "https://tok@argocd.other"},
		{"query parameter", "https://argocd.other?access_token=tok"},
		{"fragment", "https://argocd.other#tok"},
	} {
		t.Run(carrier.name, func(t *testing.T) {
			t.Setenv(envConnArgocdServerURL, carrier.url)

			conn := &models.Connection{Argocd: models.ArgocdConfig{ServerURL: saved}}
			MergeConnectionFromEnv(conn)

			if conn.Argocd.ServerURL != saved {
				t.Errorf("a declared address with a credential in it was applied: %q", conn.Argocd.ServerURL)
			}
		})
	}
}

func TestDeclaredArgocdAddress_AnOrdinaryOneStillWins(t *testing.T) {
	// The positive control for the test above: without it, a merge that had
	// simply stopped working would look like a refusal and both tests would
	// agree with a broken door.
	t.Setenv(envConnArgocdServerURL, "https://argocd.declared")

	conn := &models.Connection{Argocd: models.ArgocdConfig{ServerURL: "https://argocd.saved"}}
	if !MergeConnectionFromEnv(conn) {
		t.Fatal("the merge reported no change, so this test proves nothing about the refusal case")
	}
	if conn.Argocd.ServerURL != "https://argocd.declared" {
		t.Errorf("the declared address did not win: %q", conn.Argocd.ServerURL)
	}
}

func TestDeclaredArgocdAddress_ARefusalNeverStopsTheRestOfTheMerge(t *testing.T) {
	// A boot that already works must keep working. The one bad field is
	// dropped; everything else the operator declared still lands.
	t.Setenv(envConnArgocdServerURL, "https://tok@argocd.other")
	t.Setenv(envConnArgocdNamespace, "argocd-prod")

	conn := &models.Connection{Argocd: models.ArgocdConfig{
		ServerURL: "https://argocd.saved",
		Namespace: "argocd",
		Token:     "the-saved-token",
	}}
	MergeConnectionFromEnv(conn)

	if conn.Argocd.ServerURL != "https://argocd.saved" {
		t.Errorf("the bad address was applied anyway: %q", conn.Argocd.ServerURL)
	}
	if conn.Argocd.Namespace != "argocd-prod" {
		t.Errorf("the refusal stopped the rest of the merge; namespace is %q", conn.Argocd.Namespace)
	}
	if conn.Argocd.Token != "the-saved-token" {
		t.Errorf("the saved ArgoCD token was disturbed: %q", conn.Argocd.Token)
	}
}
