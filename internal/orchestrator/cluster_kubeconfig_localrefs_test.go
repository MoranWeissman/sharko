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
