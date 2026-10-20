package api

// docs_kind_walkthrough_test.go — pins the local kind walkthrough and the
// ArgoCD token steps that the episode 2 redo found missing. A new user ran
// the docs from zero and got stuck on three gaps; these sentences close them,
// so a later edit may not quietly take them out.
//
// What each pin protects:
//   - first-run.md must tell the user to create the sharko account
//     (accounts.sharko: apiKey) and to log in with the argocd tool's own port
//     forward. A plain kubectl port-forward drops on a kind hub.
//   - try-on-your-laptop.md must name the node image, use the internal
//     kubeconfig, and save it as a Secret named after the cluster with the key
//     the k8s-secrets provider reads (KubernetesSecretProvider: secret name =
//     cluster name, key "kubeconfig", namespace "sharko").

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDocForKindPins(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRootForSweep(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("cannot read %s: %v", rel, err)
	}
	return string(b)
}

func TestDocsEp2RedoPins(t *testing.T) {
	firstRun := readDocForKindPins(t, "docs/site/getting-started/first-run.md")
	kind := readDocForKindPins(t, "docs/site/getting-started/try-on-your-laptop.md")
	expired := readDocForKindPins(t, "docs/site/operator/argocd-account-token-expired.md")

	must := []struct{ name, doc, text string }{
		{"first-run creates the account", firstRun, "accounts.sharko: apiKey"},
		{"first-run logs in with the tool's own port forward", firstRun, "--port-forward --port-forward-namespace argocd"},
		{"first-run warns about a plain port forward", firstRun, "Do not start your own `kubectl port-forward` for this."},
		{"first-run reads the password without printing it", firstRun, "so it is never printed"},
		{"kind page names the node image", kind, "kindest/node:v1.31.0"},
		{"kind page uses the internal kubeconfig", kind, "kind get kubeconfig --internal --name spoke-1"},
		{"kind page saves the Secret in the shape the provider reads", kind, "--from-file=kubeconfig=spoke-1.kubeconfig"},
		{"kind page uses the sharko namespace", kind, "--namespace sharko --context kind-hub"},
		{"token page points to the login steps", expired, "first-run.md#make-the-argocd-token-first"},
	}
	for _, m := range must {
		if !strings.Contains(m.doc, m.text) {
			t.Errorf("%s: missing the exact text %q", m.name, m.text)
		}
	}

	// The node image must be the Kubernetes version the docs say is tested.
	inst := readDocForKindPins(t, "docs/site/operator/installation.md")
	if !strings.Contains(inst, "Tested on 1.31") {
		t.Errorf("operator installation page no longer says Kubernetes 1.31 is tested; the kind page image must match whatever it says now")
	}

	// The plain port forward login is the thing that failed; it may not come
	// back as advice.
	for name, doc := range map[string]string{"first-run": firstRun, "kind": kind, "token page": expired} {
		if strings.Contains(doc, "argocd login localhost") {
			t.Errorf("%s: tells the user to log in through a plain kubectl port forward (argocd login localhost...), which drops on a kind hub", name)
		}
	}
}
