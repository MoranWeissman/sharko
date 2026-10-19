//go:build unix

package remoteclient

// LO-12 — every kubeconfig that reaches remoteclient (stored ones, pasted
// ones, the ones the providers build) goes through providers.BuildRESTConfig,
// so a field that would make client-go open a file or run a command inside
// the Sharko pod is refused here too, before any file is opened.
//
// The "file" is a named pipe: opening it for reading blocks until a writer
// shows up, and none does. A call that returns on its own never opened it.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/credsafe"
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

func TestNewClientFromKubeconfig_RefusesPodLocalReferences(t *testing.T) {
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
			var err error
			assertNeverOpened(t, trap, func() { _, err = NewClientFromKubeconfig([]byte(kc)) })
			if !errors.Is(err, providers.ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("NewClientFromKubeconfig: want ErrUnsupportedKubeconfigAuth, got %v", err)
			}
			// CheckDestinationTLS answers nil for a kubeconfig it cannot
			// build (NewClientFromKubeconfig is the one that refuses), but it
			// must not open the file either.
			assertNeverOpened(t, trap, func() { _ = CheckDestinationTLS([]byte(kc)) })
		})
	}
}

// realCertPair makes a self-signed cert and key, base64 encoded, so the
// transport client-go builds from an inline-cert kubeconfig can load them.
func realCertPair(t *testing.T) (certB64, keyB64 string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lo12-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return base64.StdEncoding.EncodeToString(certPEM), base64.StdEncoding.EncodeToString(keyPEM)
}

func TestNewClientFromKubeconfig_AcceptsInlineTokenAndInlineCert(t *testing.T) {
	cert, key := realCertPair(t)
	ca := "    certificate-authority-data: " + cert + "\n"
	for name, kc := range map[string]string{
		"bearer token": trapKubeconfig("    token: inline-bearer-token\n", ca),
		"inline cert":  trapKubeconfig("    client-certificate-data: "+cert+"\n    client-key-data: "+key+"\n", ca),
	} {
		if _, err := NewClientFromKubeconfig([]byte(kc)); err != nil {
			t.Errorf("%s: %v (cause: %v)", name, err, credsafe.Cause(err))
		}
	}
}
