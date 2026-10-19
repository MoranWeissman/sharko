//go:build unix

package orchestrator

// LO-12 — a pasted kubeconfig that points at a file (or a command) inside the
// Sharko pod is refused before anything is written, and the file is never
// opened. The "file" is a named pipe: opening it for reading blocks until a
// writer shows up, and none does, so a call that returns on its own never
// opened it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/providers"
)

func newTrapFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pod-serviceaccount-token")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	return p
}

func assertNeverOpened(t *testing.T, path string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// Something is stuck opening the FIFO. Keep opening the write end
		// until the call finishes (client-go can open the same path more
		// than once), then fail.
		for {
			if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				_ = w.Close()
			}
			select {
			case <-done:
				t.Fatalf("the kubeconfig made Sharko open %s inside its own pod", path)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
}

func trapKubeconfig(userLines, clusterLines string) string {
	return "apiVersion: v1\nkind: Config\ncurrent-context: c\n" +
		"clusters:\n- name: k\n  cluster:\n    server: https://10.0.0.9:6443\n" + clusterLines +
		"contexts:\n- name: c\n  context:\n    cluster: k\n    user: u\n" +
		"users:\n- name: u\n  user:\n" + userLines
}

func TestRegisterCluster_Kubeconfig_RefusesPodLocalReferences(t *testing.T) {
	trap := newTrapFile(t)
	cases := map[string]string{
		"tokenFile":                     trapKubeconfig("    tokenFile: "+trap+"\n", ""),
		"token and tokenFile":           trapKubeconfig("    token: inline-token\n    tokenFile: "+trap+"\n", ""),
		"client-certificate by path":    trapKubeconfig("    token: inline-token\n    client-certificate: "+trap+"\n    client-key: "+trap+"\n", ""),
		"client-key by path":            trapKubeconfig("    client-certificate-data: dGVzdC1jZXJ0\n    client-key: "+trap+"\n", ""),
		"certificate-authority by path": trapKubeconfig("    token: inline-token\n", "    certificate-authority: "+trap+"\n"),
		"exec":                          trapKubeconfig("    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: "+trap+"\n", ""),
		"auth-provider":                 trapKubeconfig("    auth-provider:\n      name: oidc\n      config:\n        idp-issuer-url: https://issuer.example\n        client-id: sharko\n        idp-certificate-authority: "+trap+"\n", ""),
	}
	for name, kc := range cases {
		t.Run(name, func(t *testing.T) {
			argocd := newMockArgocd()
			git := newMockGitProvider()
			asm := newMockArgoSecretManager()
			orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
			orch.SetArgoSecretManager(asm, "")
			seeded := len(git.files) // the mock starts with the addon catalog

			var err error
			assertNeverOpened(t, trap, func() {
				_, err = orch.RegisterCluster(context.Background(), RegisterClusterRequest{
					Name:       "prod",
					Provider:   "kubeconfig",
					Kubeconfig: kc,
					Addons:     map[string]bool{"monitoring": true},
				})
			})
			if !errors.Is(err, providers.ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("want ErrUnsupportedKubeconfigAuth, got %v", err)
			}
			if len(asm.ensured) != 0 {
				t.Errorf("a refused kubeconfig still wrote the ArgoCD Secret: %d Ensure call(s)", len(asm.ensured))
			}
			if len(git.prs) != 0 || len(git.files) != seeded {
				t.Errorf("a refused kubeconfig still wrote to Git: %d PR(s), %d file(s)", len(git.prs), len(git.files)-seeded)
			}
		})
	}
}

// LO-13 K1 — the same refused fields in a kubeconfig STORED in a secrets
// backend. Before, the backend lookup failed, register treated that like
// "nothing stored" and quietly went on as a register with no credentials.
// Now it stops before anything is written, and the error says why.
//
// The sentences are written out in full on purpose, so a change to the words
// a user sees fails here.
const (
	wantStoredPrefix         = `the kubeconfig stored for cluster "prod" cannot be used: unsupported kubeconfig auth method: `
	wantTokenFileSentence    = "kubeconfig reads its bearer token from a file path (tokenFile); Sharko does not read a kubeconfig's files from its own disk, so put the token itself in the kubeconfig (token) or generate one with `kubectl create token <serviceaccount> --duration=24h`"
	wantCertFileSentence     = "kubeconfig references client certificate/key by file path; embed the data inline first (`kubectl config view --flatten --minify`) or use a bearer token"
	wantCAFileSentence       = "kubeconfig references the cluster certificate authority by file path (certificate-authority); embed the data inline first (`kubectl config view --flatten --minify`)"
	wantExecSentence         = "exec-plugin auth (e.g. aws-iam-authenticator, gke-gcloud-auth-plugin) is not supported; use a bearer token (`kubectl create token <serviceaccount> --duration=24h`) or a client-certificate kubeconfig with inline data"
	wantAuthProviderSentence = "auth-provider sign-in (e.g. oidc, gcp, azure) is not supported; use a bearer token (`kubectl create token <serviceaccount> --duration=24h`) or a client-certificate kubeconfig with inline data"
)

func TestRegisterCluster_StoredKubeconfig_RefusedStopsTheRegister(t *testing.T) {
	trap := newTrapFile(t)
	cases := []struct{ name, kc, want string }{
		{"tokenFile", trapKubeconfig("    tokenFile: "+trap+"\n", ""), wantTokenFileSentence},
		{"token and tokenFile", trapKubeconfig("    token: inline-token\n    tokenFile: "+trap+"\n", ""), wantTokenFileSentence},
		{"client-certificate by path", trapKubeconfig("    token: inline-token\n    client-certificate: "+trap+"\n    client-key: "+trap+"\n", ""), wantCertFileSentence},
		{"client-key by path", trapKubeconfig("    client-certificate-data: dGVzdC1jZXJ0\n    client-key: "+trap+"\n", ""), wantCertFileSentence},
		{"certificate-authority by path", trapKubeconfig("    token: inline-token\n", "    certificate-authority: "+trap+"\n"), wantCAFileSentence},
		{"exec", trapKubeconfig("    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: "+trap+"\n", ""), wantExecSentence},
		{"auth-provider", trapKubeconfig("    auth-provider:\n      name: oidc\n      config:\n        idp-issuer-url: https://issuer.example\n        client-id: sharko\n        idp-certificate-authority: "+trap+"\n", ""), wantAuthProviderSentence},
	}
	noMint := func(context.Context, string, string, string) (string, error) {
		return "", errors.New("a raw kubeconfig must never mint a token")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argocd := newMockArgocd()
			git := newMockGitProvider()
			asm := newMockArgoSecretManager()
			backend := providers.NewAWSSecretsManagerProviderForTest(tc.kc, noMint)
			orch := New(nil, backend, argocd, git, defaultGitOps(), defaultPaths(), nil)
			orch.SetArgoSecretManager(asm, "")
			seeded := len(git.files)

			var err error
			assertNeverOpened(t, trap, func() {
				_, err = orch.RegisterCluster(context.Background(), RegisterClusterRequest{
					Name:        "prod",
					CredsSource: CredsSourceSecretKubeconfig,
					Addons:      map[string]bool{"monitoring": true},
				})
			})
			if !errors.Is(err, providers.ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("want ErrUnsupportedKubeconfigAuth, got %v", err)
			}
			if got := err.Error(); got != wantStoredPrefix+tc.want {
				t.Errorf("error:\n got: %s\nwant: %s", got, wantStoredPrefix+tc.want)
			}
			if len(asm.ensured) != 0 {
				t.Errorf("a refused stored kubeconfig still wrote the ArgoCD Secret: %d Ensure call(s)", len(asm.ensured))
			}
			if len(git.prs) != 0 || len(git.files) != seeded {
				t.Errorf("a refused stored kubeconfig still wrote to Git: %d PR(s), %d file(s)", len(git.prs), len(git.files)-seeded)
			}

			// The batch path shows the same sentence in its per-cluster
			// result (it goes through credsafe.Sentence, which must leave
			// Sharko's own refusal text alone).
			batch := orch.RegisterClusterBatch(context.Background(), []RegisterClusterRequest{{
				Name:        "prod",
				CredsSource: CredsSourceSecretKubeconfig,
			}})
			if batch.Failed != 1 || len(batch.Results) != 1 || batch.Results[0].Error != wantStoredPrefix+tc.want {
				t.Errorf("batch result: %+v", batch.Results)
			}
		})
	}
}
