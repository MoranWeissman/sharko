//go:build unix

package providers

// LO-12 — a kubeconfig must never make Sharko open a file or run a command
// inside its own pod.
//
// The proof that no file is read uses a named pipe (FIFO) as the "file" the
// kubeconfig points at. Opening a FIFO for reading blocks until somebody opens
// it for writing, and nobody does. So if any code path opens the path, the
// call under test never returns, assertNeverOpened sees the timeout, unblocks
// the reader, and fails the test. If the call returns on its own, the path was
// never opened — not "opened and closed quickly", never opened at all.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// The refusal sentences, written out in full on purpose (not read from the
// constants) so a change to the words a user sees fails here.
const (
	wantExecRefusal         = "unsupported kubeconfig auth method: exec-plugin auth (e.g. aws-iam-authenticator, gke-gcloud-auth-plugin) is not supported; use a bearer token (`kubectl create token <serviceaccount> --duration=24h`) or a client-certificate kubeconfig with inline data"
	wantAuthProviderRefusal = "unsupported kubeconfig auth method: auth-provider sign-in (e.g. oidc, gcp, azure) is not supported; use a bearer token (`kubectl create token <serviceaccount> --duration=24h`) or a client-certificate kubeconfig with inline data"
	wantTokenFileRefusal    = "unsupported kubeconfig auth method: kubeconfig reads its bearer token from a file path (tokenFile); Sharko does not read a kubeconfig's files from its own disk, so put the token itself in the kubeconfig (token) or generate one with `kubectl create token <serviceaccount> --duration=24h`"
	wantCertFileRefusal     = "unsupported kubeconfig auth method: kubeconfig references client certificate/key by file path; embed the data inline first (`kubectl config view --flatten --minify`) or use a bearer token"
	wantCAFileRefusal       = "unsupported kubeconfig auth method: kubeconfig references the cluster certificate authority by file path (certificate-authority); embed the data inline first (`kubectl config view --flatten --minify`)"
)

// newTrapFile makes a FIFO in a temp dir. Any attempt to open it for reading
// blocks, which is what assertNeverOpened detects.
func newTrapFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pod-serviceaccount-token")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	return p
}

// assertNeverOpened runs fn and fails the test if fn opens path. fn must not
// call t.Fatal (it runs on another goroutine); return values out instead.
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

// trapKubeconfig builds a one-context kubeconfig with extra lines on the
// selected user and the selected cluster. Lines are indented four spaces.
func trapKubeconfig(userLines, clusterLines string) string {
	return "apiVersion: v1\nkind: Config\ncurrent-context: c\n" +
		"clusters:\n- name: k\n  cluster:\n    server: https://10.0.0.9:6443\n" + clusterLines +
		"contexts:\n- name: c\n  context:\n    cluster: k\n    user: u\n" +
		"users:\n- name: u\n  user:\n" + userLines
}

type localRefCase struct {
	name  string
	kc    string
	want  string
	field string // the kubeconfig key the refusal names
}

// localRefCases is every refused field. trap is the FIFO path the field
// points at (where the field takes a path).
func localRefCases(trap string) []localRefCase {
	return []localRefCase{
		{"tokenFile", trapKubeconfig("    tokenFile: "+trap+"\n", ""), wantTokenFileRefusal, RefusedFieldTokenFile},
		// client-go re-reads tokenFile on every request even when token is
		// set too, so token + tokenFile is refused as well.
		{"token and tokenFile", trapKubeconfig("    token: inline-token\n    tokenFile: "+trap+"\n", ""), wantTokenFileRefusal, RefusedFieldTokenFile},
		{"client-certificate and client-key by path, with a token", trapKubeconfig("    token: inline-token\n    client-certificate: "+trap+"\n    client-key: "+trap+"\n", ""), wantCertFileRefusal, RefusedFieldClientCertificate},
		{"client-key by path with inline cert data", trapKubeconfig("    client-certificate-data: dGVzdC1jZXJ0\n    client-key: "+trap+"\n", ""), wantCertFileRefusal, RefusedFieldClientKey},
		{"certificate-authority by path", trapKubeconfig("    token: inline-token\n", "    certificate-authority: "+trap+"\n"), wantCAFileRefusal, RefusedFieldCertAuthority},
		{"exec", trapKubeconfig("    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: "+trap+"\n", ""), wantExecRefusal, RefusedFieldExec},
		{"auth-provider", trapKubeconfig("    auth-provider:\n      name: oidc\n      config:\n        idp-issuer-url: https://issuer.example\n        client-id: sharko\n        idp-certificate-authority: "+trap+"\n", ""), wantAuthProviderRefusal, RefusedFieldAuthProvider},
	}
}

// LO-13 K5: the tokenFile sentence used to say Sharko "never reads files from
// its own disk". That is not true — Sharko reads its own ServiceAccount token
// and its own config files. The sentence now says it does not read a
// KUBECONFIG's files. The old words must not come back.
func TestRefusalSentences_OldWordingIsGone(t *testing.T) {
	banned := []string{
		"never reads files from its own disk",
		"`kubectl config view --flatten`",
	}
	for _, s := range []string{refuseExecSentence, refuseAuthProviderSentence, refuseTokenFileSentence, refuseCertFileSentence, refuseCAFileSentence} {
		for _, b := range banned {
			if strings.Contains(s, b) {
				t.Errorf("refusal sentence still says %q: %s", b, s)
			}
		}
	}
}

// storedRefusalLog is the fixed text of the log line a stored-kubeconfig
// refusal writes (LO-13 K1).
const storedRefusalLog = "[provider] stored kubeconfig refused: it would make Sharko read a file or run a program inside its own pod"

// captureWarnLog sends slog's default logger to a buffer for one test.
func captureWarnLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return &buf
}

// assertStoredRefusal checks what GetCredentials hands back for a refused
// stored kubeconfig: a refusal, not a "not found"; the public text is still
// the fixed credentials sentence; the refusal and its field are reachable by
// type under the mark; and one log line names the field and nothing from the
// kubeconfig.
func assertStoredRefusal(t *testing.T, err error, tc localRefCase, trap, backend string, logs *bytes.Buffer) {
	t.Helper()
	if !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
		t.Fatalf("GetCredentials: want ErrUnsupportedKubeconfigAuth, got %v", err)
	}
	if credsafe.IsNotFound(err) {
		t.Errorf("GetCredentials: a refused kubeconfig must not be marked not found")
	}
	if err.Error() != credsafe.Message {
		t.Errorf("GetCredentials public sentence: got %q", err.Error())
	}
	refusal, ok := AsKubeconfigRefusal(err)
	if !ok {
		t.Fatalf("GetCredentials: refusal not reachable under the mark: %v", err)
	}
	if refusal.Field() != tc.field {
		t.Errorf("refused field: got %q, want %q", refusal.Field(), tc.field)
	}
	if refusal.Error() != tc.want {
		t.Errorf("refusal:\n got: %s\nwant: %s", refusal.Error(), tc.want)
	}
	line := logs.String()
	want := `"msg":"` + storedRefusalLog + `","backend":"` + backend + `","secretName":"prod","field":"` + tc.field + `"`
	if !strings.Contains(line, want) {
		t.Errorf("log line:\n got: %s\nwant to contain: %s", line, want)
	}
	if strings.Contains(line, trap) || strings.Contains(line, "inline-token") {
		t.Errorf("log line carries kubeconfig contents: %s", line)
	}
}

const inlineBearer = "    token: inline-bearer-token\n"
const inlineCertPair = "    client-certificate-data: dGVzdC1jZXJ0\n    client-key-data: dGVzdC1rZXk=\n"
const inlineCA = "    certificate-authority-data: dGVzdC1jYS1ieXRlcw==\n"

func TestBuildRESTConfig_RefusesPodLocalReferences(t *testing.T) {
	trap := newTrapFile(t)
	for _, tc := range localRefCases(trap) {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			assertNeverOpened(t, trap, func() { _, err = BuildRESTConfig([]byte(tc.kc)) })
			if !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("want ErrUnsupportedKubeconfigAuth, got %v", err)
			}
			if err.Error() != tc.want {
				t.Errorf("sentence:\n got: %s\nwant: %s", err.Error(), tc.want)
			}
			if r, ok := AsKubeconfigRefusal(err); !ok || r.Field() != tc.field {
				t.Errorf("refusal type: got %#v, want field %q", r, tc.field)
			}
		})
	}
}

func TestParseInlineKubeconfig_RefusesPodLocalReferences(t *testing.T) {
	trap := newTrapFile(t)
	for _, tc := range localRefCases(trap) {
		t.Run(tc.name, func(t *testing.T) {
			var kc *Kubeconfig
			var err error
			assertNeverOpened(t, trap, func() { kc, err = ParseInlineKubeconfig(tc.kc) })
			if kc != nil {
				t.Fatalf("want no credentials, got %+v", kc)
			}
			if !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("want ErrUnsupportedKubeconfigAuth, got %v", err)
			}
			if err.Error() != tc.want {
				t.Errorf("sentence:\n got: %s\nwant: %s", err.Error(), tc.want)
			}
		})
	}
}

// A kubeconfig often carries more than one context. client-go only uses the
// one current-context selects, so a file reference on a user no context
// selects is never opened, and the kubeconfig still works.
func TestBuildRESTConfig_IgnoresEntriesNoContextSelects(t *testing.T) {
	trap := newTrapFile(t)
	kc := trapKubeconfig(inlineBearer, inlineCA) +
		"- name: unused\n  user:\n    tokenFile: " + trap + "\n"
	var err error
	assertNeverOpened(t, trap, func() { _, err = BuildRESTConfig([]byte(kc)) })
	if err != nil {
		t.Fatalf("an unused user must not block the kubeconfig, got %v", err)
	}
}

func TestBuildRESTConfig_AcceptsInlineTokenAndInlineCert(t *testing.T) {
	cfg, err := BuildRESTConfig([]byte(trapKubeconfig(inlineBearer, inlineCA)))
	if err != nil {
		t.Fatalf("bearer token kubeconfig: %v", err)
	}
	if cfg.BearerToken != "inline-bearer-token" || cfg.BearerTokenFile != "" || string(cfg.CAData) != "test-ca-bytes" {
		t.Errorf("bearer: got token %q, tokenFile %q, CA %q", cfg.BearerToken, cfg.BearerTokenFile, cfg.CAData)
	}

	cfg, err = BuildRESTConfig([]byte(trapKubeconfig(inlineCertPair, inlineCA)))
	if err != nil {
		t.Fatalf("inline cert kubeconfig: %v", err)
	}
	if string(cfg.CertData) != "test-cert" || string(cfg.KeyData) != "test-key" || cfg.CertFile != "" || cfg.KeyFile != "" || cfg.CAFile != "" {
		t.Errorf("cert: got %+v", cfg.TLSClientConfig)
	}
}

func TestParseInlineKubeconfig_AcceptsInlineTokenAndInlineCert(t *testing.T) {
	kc, err := ParseInlineKubeconfig(trapKubeconfig(inlineBearer, inlineCA))
	if err != nil || kc.Token != "inline-bearer-token" {
		t.Fatalf("bearer: kc=%+v err=%v", kc, err)
	}
	kc, err = ParseInlineKubeconfig(trapKubeconfig(inlineCertPair, inlineCA))
	if err != nil || string(kc.CertData) != "test-cert" || string(kc.KeyData) != "test-key" {
		t.Fatalf("cert: kc=%+v err=%v", kc, err)
	}
}

// --- stored kubeconfigs: the Kubernetes Secrets provider ---

func k8sProviderHolding(kubeconfig string) *KubernetesSecretProvider {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "sharko"},
		Data:       map[string][]byte{"kubeconfig": []byte(kubeconfig)},
	})
	return newKubernetesSecretProviderWithClient(client, "sharko")
}

func TestKubernetesSecretProvider_RefusesPodLocalReferences(t *testing.T) {
	trap := newTrapFile(t)
	for _, tc := range localRefCases(trap) {
		t.Run(tc.name, func(t *testing.T) {
			p := k8sProviderHolding(tc.kc)

			// The parse step itself: the refusal and its exact sentence.
			var err error
			assertNeverOpened(t, trap, func() { _, err = p.fetchK8sSecret("prod") })
			if !errors.Is(err, ErrUnsupportedKubeconfigAuth) || !strings.HasSuffix(err.Error(), tc.want) {
				t.Fatalf("fetchK8sSecret: got %v", err)
			}

			// The public fetch: a refusal, kept apart from "not found".
			logs := captureWarnLog(t)
			var kc *Kubeconfig
			assertNeverOpened(t, trap, func() { kc, err = p.GetCredentials("prod") })
			if kc != nil {
				t.Fatalf("GetCredentials: want no credentials, got %+v", kc)
			}
			assertStoredRefusal(t, err, tc, trap, "kubernetes-secrets", logs)

			// The read-only comparison path keeps the cause under the mark,
			// and what a person sees stays the one fixed credentials sentence.
			var facts *StoredConnectionFacts
			assertNeverOpened(t, trap, func() { facts, err = p.StoredConnectionFacts("prod") })
			if facts != nil || !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("StoredConnectionFacts: want a refusal, got facts=%+v err=%v", facts, err)
			}
			if err.Error() != credsafe.Message {
				t.Errorf("public sentence: got %q", err.Error())
			}
			if cause := credsafe.Cause(err).Error(); !strings.HasSuffix(cause, tc.want) {
				t.Errorf("cause:\n got: %s\nwant suffix: %s", cause, tc.want)
			}
		})
	}
}

func TestKubernetesSecretProvider_AcceptsInlineTokenAndInlineCert(t *testing.T) {
	kc, err := k8sProviderHolding(trapKubeconfig(inlineBearer, inlineCA)).GetCredentials("prod")
	if err != nil || kc.Token != "inline-bearer-token" {
		t.Fatalf("bearer: kc=%+v err=%v", kc, err)
	}
	kc, err = k8sProviderHolding(trapKubeconfig(inlineCertPair, inlineCA)).GetCredentials("prod")
	if err != nil || string(kc.CertData) != "test-cert" || string(kc.KeyData) != "test-key" {
		t.Fatalf("cert: kc=%+v err=%v", kc, err)
	}
}

// --- stored kubeconfigs: the AWS Secrets Manager provider (raw kubeconfig) ---

func awsProviderHolding(kubeconfig string) *AWSSecretsManagerProvider {
	return NewAWSSecretsManagerProviderForTest(kubeconfig, func(context.Context, string, string, string) (string, error) {
		return "", errors.New("a raw kubeconfig must never mint a token")
	})
}

func TestAWSSecretsManagerProvider_RefusesPodLocalReferences(t *testing.T) {
	trap := newTrapFile(t)
	for _, tc := range localRefCases(trap) {
		t.Run(tc.name, func(t *testing.T) {
			p := awsProviderHolding(tc.kc)

			// The parse step itself: the refusal and its exact sentence.
			var err error
			assertNeverOpened(t, trap, func() { _, err = p.buildFromRawKubeconfig([]byte(tc.kc), "prod") })
			if !errors.Is(err, ErrUnsupportedKubeconfigAuth) || !strings.HasSuffix(err.Error(), tc.want) {
				t.Fatalf("buildFromRawKubeconfig: got %v", err)
			}

			// The public fetch: a refusal, kept apart from "not found".
			logs := captureWarnLog(t)
			var kc *Kubeconfig
			assertNeverOpened(t, trap, func() { kc, err = p.GetCredentials("prod") })
			if kc != nil {
				t.Fatalf("GetCredentials: want no credentials, got %+v", kc)
			}
			assertStoredRefusal(t, err, tc, trap, "aws-secrets-manager", logs)

			// The read-only comparison path.
			var facts *StoredConnectionFacts
			assertNeverOpened(t, trap, func() { facts, err = p.StoredConnectionFacts("prod") })
			if facts != nil || !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("StoredConnectionFacts: want a refusal, got facts=%+v err=%v", facts, err)
			}
		})
	}
}

func TestAWSSecretsManagerProvider_AcceptsInlineTokenAndInlineCert(t *testing.T) {
	kc, err := awsProviderHolding(trapKubeconfig(inlineBearer, inlineCA)).GetCredentials("prod")
	if err != nil || kc.Token != "inline-bearer-token" {
		t.Fatalf("bearer: kc=%+v err=%v", kc, err)
	}
	kc, err = awsProviderHolding(trapKubeconfig(inlineCertPair, inlineCA)).GetCredentials("prod")
	if err != nil || string(kc.CertData) != "test-cert" || string(kc.KeyData) != "test-key" {
		t.Fatalf("cert: kc=%+v err=%v", kc, err)
	}
}

// --- the ArgoCD provider's synthesized kubeconfigs ---

// The ArgoCD provider writes values from the ArgoCD cluster Secret into a
// kubeconfig template. A value with a line break in it can add a field to that
// kubeconfig. The same check refuses the result before client-go reads it.
func TestArgoCDProvider_RefusesFileFieldsAddedThroughSecretValues(t *testing.T) {
	trap := newTrapFile(t)
	cases := []struct {
		name, server, configJSON, want string
	}{
		{"tokenFile through bearerToken", "https://10.0.0.9:6443", `{"bearerToken":"abc\n    tokenFile: ` + trap + `","tlsClientConfig":{"insecure":true}}`, wantTokenFileRefusal},
		{"certificate-authority through server", "https://10.0.0.9:6443\n    certificate-authority: " + trap, `{"bearerToken":"abc","tlsClientConfig":{}}`, wantCAFileRefusal},
		{"certificate-authority through server, client-certificate Secret", "https://10.0.0.9:6443\n    certificate-authority: " + trap, `{"tlsClientConfig":{"certData":"dGVzdC1jZXJ0","keyData":"dGVzdC1rZXk="}}`, wantCAFileRefusal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(argoCDSecret("prod", "prod", tc.server, tc.configJSON))
			p := newArgoCDProviderWithClient(client, "argocd")
			var kc *Kubeconfig
			var err error
			assertNeverOpened(t, trap, func() { kc, err = p.GetCredentials("prod") })
			if kc != nil || !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
				t.Fatalf("want a refusal, got kc=%+v err=%v", kc, err)
			}
			if cause := credsafe.Cause(err).Error(); !strings.HasSuffix(cause, tc.want) {
				t.Errorf("cause:\n got: %s\nwant suffix: %s", cause, tc.want)
			}
		})
	}
}
