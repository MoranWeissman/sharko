package clusterreconciler

// live_vault_resolution_test.go — R2-1: the write path resolves the
// cluster-credentials backend at USE time, never at construction.
//
// The bug these tests pin against coming back: serve.go used to hand the
// reconciler the provider VALUE it had at boot (`Vault: credProvider`), while
// the check path read the api.Server's live snapshot. Configure a backend
// through the connections API after boot and the check saw it, but every
// write — the background pass and the repair — kept the boot generation
// until a restart. Deps.Vault is now a resolver, and these tests prove the
// resolver semantics: a swap is seen by the next write, and "no backend
// right now" refuses instead of panicking or falling back to anything stale.
//
// The full end-to-end proof — backend swapped through the api.Server's real
// publish mechanism, with the check, the repair-spec route, and the
// background write all watched — lives in
// internal/api/live_credentials_provider_test.go, because it needs the
// api.Server snapshot this package must not import.

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// theExistingCredentialsFailureSentence is the signed-off message createOne
// already records when a credential fetch fails — pinned by exact full text.
// Resolver-returns-nil must produce THIS sentence, not a new one.
const theExistingCredentialsFailureSentence = "Sharko couldn't fetch this cluster's credentials from the secrets backend. " +
	"Sharko could not read this cluster's sign-in details from the configured credentials source. " +
	"The server log for this request id says which step failed."

func TestConnectionCredentialSpecForWrite_NoBackendConfigured_Refuses(t *testing.T) {
	t.Parallel()

	// Both "no resolver wired at all" and "resolver answers nil" mean the
	// same thing: no backend right now. Neither may panic, and neither may
	// return a usable-looking spec.
	cases := map[string]*Reconciler{
		"resolver answers nil": {deps: Deps{Vault: func() Vault { return nil }}},
		"no resolver wired":    {deps: Deps{}},
	}

	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			spec, err := r.ConnectionCredentialSpecForWrite(models.ManagedClusterEntry{Name: "prod-eu"})
			if err == nil {
				t.Fatal("no backend configured returned no error; the caller would go on to write a spec with no credential in it")
			}
			if !errors.Is(err, ErrNoCredentialsBackend) {
				t.Errorf("error = %v, want ErrNoCredentialsBackend — the caller decides what to say, and it decides by TYPE", err)
			}
			if spec.Server != "" || spec.Token != "" || spec.CAData != "" {
				t.Errorf("a refusal came back with credential fields filled in: server set = %v, token set = %v, ca set = %v",
					spec.Server != "", spec.Token != "", spec.CAData != "")
			}
		})
	}
}

func TestPollOnce_ResolverSaysNoBackend_SkipsAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	k8sClient := fake.NewSimpleClientset()
	fg := &fakeGit{files: map[string][]byte{
		DefaultManagedClustersPath: envelopedManagedClusters("prod-eu"),
	}}

	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider { return fg },
		ArgoClient:  k8sClient,
		Vault:       func() Vault { return nil }, // no backend right now
		AuditFn:     (&auditCollector{}).Add,
	})

	r.pollOnce(ctx) // must not panic

	if got := secretsListUnfiltered(t, k8sClient, DefaultArgoCDNamespace); len(got) != 0 {
		t.Fatalf("a pass with no backend wrote %d Secret(s); it must write nothing", len(got))
	}
}

// TestPollOnce_BackendDisappearsMidPass_FailsClosedWithTheExistingSentence
// covers the window the top-of-pass gate cannot: the gate saw a backend, then
// the configuration changed before this cluster's write resolved it again.
// The write must refuse — no panic, no write, no stale provider — and the
// cluster's record must carry the EXISTING credentials-failure sentence, not
// new wording (R2-1 criterion 3).
func TestPollOnce_BackendDisappearsMidPass_FailsClosedWithTheExistingSentence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	working := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://prod-eu.example.com", CAData: []byte("fake-ca"), Token: "fake-token"},
		},
	}
	// First resolution (the pass gate) sees a backend; every later one
	// (the write inside createOne) sees none.
	resolutions := 0
	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider {
			return &fakeGit{files: map[string][]byte{
				DefaultManagedClustersPath: envelopedManagedClusters("prod-eu"),
			}}
		},
		ArgoClient: fake.NewSimpleClientset(),
		Vault: func() Vault {
			resolutions++
			if resolutions == 1 {
				return working
			}
			return nil
		},
		AuditFn: (&auditCollector{}).Add,
	})

	r.pollOnce(ctx) // must not panic

	k8sClient := r.deps.ArgoClient.(*fake.Clientset)
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{}); err == nil {
		t.Fatal("the Secret was written although the backend was gone at write time — the write must refuse, not fall back")
	}

	rec, ok := r.LastReconcile("prod-eu")
	if !ok {
		t.Fatal("no reconcile record for prod-eu — the refusal must be recorded, not silent")
	}
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want %q", rec.Outcome, OutcomeFailed)
	}
	if rec.Message != theExistingCredentialsFailureSentence {
		t.Fatalf("message = %q\nwant the existing signed-off sentence = %q\n\nResolver-returns-nil must speak with the sentence the credential-failure path already uses — no new wording.",
			rec.Message, theExistingCredentialsFailureSentence)
	}
}

// TestPollOnce_BackendSwap_NextPassWritesWithTheNewBackend is the
// package-level half of R2-1 criterion 1: swap what the resolver answers
// between two passes and the next write is built from the NEW backend's
// credentials — no reconstruction, no restart. (The api-level test drives the
// same swap through the api.Server's real publish mechanism.)
func TestPollOnce_BackendSwap_NextPassWritesWithTheNewBackend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	backendA := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://backend-a.invalid", CAData: []byte("fake-ca"), Token: "made-up-token-a"},
	}}
	backendB := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://backend-b.invalid", CAData: []byte("fake-ca"), Token: "made-up-token-b"},
	}}

	current := Vault(backendA)
	k8sClient := fake.NewSimpleClientset()
	r := New(Deps{
		GitProvider: func() gitprovider.GitProvider {
			return &fakeGit{files: map[string][]byte{
				DefaultManagedClustersPath: envelopedManagedClusters("prod-eu"),
			}}
		},
		ArgoClient: k8sClient,
		Vault:      func() Vault { return current },
		AuditFn:    (&auditCollector{}).Add,
	})

	r.pollOnce(ctx)
	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("first pass did not create the Secret: %v", err)
	}
	if got := secret.StringData["server"]; got != "https://backend-a.invalid" {
		t.Fatalf("first write server = %q, want backend A's", got)
	}

	// The operator swaps the backend; the Secret is gone (that is what the
	// next pass exists to fix). The write it performs must be built from B.
	if err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Delete(ctx, "prod-eu", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	current = backendB

	r.pollOnce(ctx)
	secret, err = k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("second pass did not recreate the Secret: %v", err)
	}
	if got := secret.StringData["server"]; got != "https://backend-b.invalid" {
		t.Fatalf("second write server = %q, want backend B's %q — the write is still on an old provider generation",
			got, "https://backend-b.invalid")
	}
}
