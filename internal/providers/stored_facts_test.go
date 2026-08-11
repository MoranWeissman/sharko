package providers

// stored_facts_test.go — proof that the read-only route never mints a sign-in
// token, and that the STS minting function itself carries no credential
// material in its logs.
//
// The two halves matter separately:
//
//   - The mint is a real credential-creating side effect. A read-only check that
//     triggers it is paying a real risk for a value it throws away.
//   - The mint's own log lines used to carry the presigned URL in full and the
//     token's length. The URL IS the credential.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// eksMetadataSentinel is a made-up value that appears nowhere else in this
// repository. It is placed where REAL EKS metadata lives — inside the caData of
// a structured-EKS payload — so the read-only path really does handle it.
const eksMetadataSentinel = "pq8w3n6eks-metadata-sentinel-never-log-this"

// mintCounter is a token-mint stand-in that counts how many times it was
// called. A read-only comparison path must leave it at zero.
type mintCounter struct {
	calls int
}

func (m *mintCounter) fn(_ context.Context, _, _, _ string) (string, error) {
	m.calls++
	return "k8s-aws-v1.fake-non-secret-test-token", nil
}

// structuredEKSPayloadWithSentinel builds a structured-EKS secret payload whose
// caData carries the sentinel — the real shape, in the real field.
func structuredEKSPayloadWithSentinel() string {
	caData := base64.StdEncoding.EncodeToString(
		[]byte("-----BEGIN CERTIFICATE-----\n" + eksMetadataSentinel + "\n-----END CERTIFICATE-----\n"))
	return `{
		"clusterName": "prod-eu",
		"host": "https://abc123.gr7.eu-west-1.eks.example.com",
		"caData": "` + caData + `",
		"region": "eu-west-1",
		"roleArn": "arn:aws:iam::000000000000:role/test-role"
	}`
}

// TestStoredConnectionFacts_EKSPayload_NeverMintsAToken is the headline proof
// for Point 2.
//
// The counter must be ZERO. Not low, not "only once" — zero. The read-only
// comparison has already decided, in the mode policy, that an EKS credential
// blob cannot be compared, so a token minted here would be created and thrown
// away: a real, usable, short-lived AWS sign-in credential brought into
// existence by a read.
func TestStoredConnectionFacts_EKSPayload_NeverMintsAToken(t *testing.T) {
	mint := &mintCounter{}
	p := &AWSSecretsManagerProvider{
		client:     &fakeSMClient{value: structuredEKSPayloadWithSentinel()},
		eksTokenFn: mint.fn,
	}

	facts, err := p.StoredConnectionFacts("prod-eu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mint.calls != 0 {
		t.Fatalf(`the read-only stored-facts read minted %d sign-in token(s); it must mint ZERO.

WHY ZERO AND NOT "JUST ONE": a minted EKS token is a real credential that can sign in as Sharko for as long as it lives. The comparison cannot compare it (a fresh one differs every time with nothing having drifted), so a token minted here is created and immediately discarded — all of the risk, none of the use. A read must not create credentials.

If this count is above zero, something on this path now reaches GetCredentials / buildFromStructured. Find it and take it out; do not raise the expected count.`, mint.calls)
	}

	if !facts.CredentialMintedPerFetch {
		t.Error("an EKS metadata payload must report CredentialMintedPerFetch, so the caller knows there is no stored credential to compare")
	}
	if facts.Token != "" {
		t.Error("no credential may be reported for a payload that holds none")
	}
	if facts.Server == "" {
		t.Error("the stable metadata (the API address) should still come back — that is the whole point of this read")
	}
	if !strings.Contains(string(facts.CAData), eksMetadataSentinel) {
		t.Fatal("sanity check failed: the fixture's caData should carry the sentinel, otherwise the leak tests below prove nothing")
	}
}

// TestStoredConnectionFacts_EKSPayload_LeaksNothingIntoLogs sweeps the log
// output of the real EKS metadata path for every form the sentinel could leak
// in.
func TestStoredConnectionFacts_EKSPayload_LeaksNothingIntoLogs(t *testing.T) {
	mint := &mintCounter{}
	p := &AWSSecretsManagerProvider{
		client:     &fakeSMClient{value: structuredEKSPayloadWithSentinel()},
		eksTokenFn: mint.fn,
	}

	var err error
	logs := captureLogs(t, func() {
		_, err = p.StoredConnectionFacts("prod-eu")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertNoEKSSentinel(t, "stored-facts log output", logs)
}

// TestStoredConnectionFacts_RawKubeconfig_ReportsTheStoredCredential proves the
// other branch: a raw kubeconfig holds a FIXED credential, so there is nothing
// to mint and the stored credential is reported as it stands — which is what
// makes a real comparison of data.config possible for that source.
func TestStoredConnectionFacts_RawKubeconfig_ReportsTheStoredCredential(t *testing.T) {
	kubeconfigYAML := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://stored-cluster.invalid:6443
    insecure-skip-tls-verify: true
  name: stored-cluster
contexts:
- context:
    cluster: stored-cluster
    user: stored-user
  name: stored-context
current-context: stored-context
users:
- name: stored-user
  user:
    token: ` + eksMetadataSentinel + `
`
	mint := &mintCounter{}
	p := &AWSSecretsManagerProvider{
		client:     &fakeSMClient{value: kubeconfigYAML},
		eksTokenFn: mint.fn,
	}

	var facts *StoredConnectionFacts
	var err error
	logs := captureLogs(t, func() {
		facts, err = p.StoredConnectionFacts("stored-cluster")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mint.calls != 0 {
		t.Errorf("a stored kubeconfig has nothing to mint, but the mint was called %d time(s)", mint.calls)
	}
	if facts.CredentialMintedPerFetch {
		t.Error("a stored kubeconfig's credential is fixed, so CredentialMintedPerFetch must be false")
	}
	if facts.Token != eksMetadataSentinel {
		t.Errorf("the stored credential must come back to the caller (that is the destination, not a leak), got %q", facts.Token)
	}
	assertNoEKSSentinel(t, "stored-facts log output (raw kubeconfig)", logs)
}

// TestStoredConnectionFacts_BackendError_LeaksNothing: the backend fails with
// the sentinel in its own error text — the nastiest realistic case, a provider
// SDK that puts credential material into its message. Neither the returned
// error nor the logs may carry it onward... and where they must, the caller is
// the one that refuses to pass it through (see the API handler). Here we prove
// the provider does not LOG it.
func TestStoredConnectionFacts_BackendError_LeaksNothingIntoLogs(t *testing.T) {
	p := &AWSSecretsManagerProvider{
		client: &fakeSMErrClient{err: errors.New("backend blew up while handling " + eksMetadataSentinel)},
	}

	var err error
	logs := captureLogs(t, func() {
		_, err = p.StoredConnectionFacts("prod-eu")
	})
	if err == nil {
		t.Fatal("expected an error from a failing backend")
	}
	assertNoEKSSentinel(t, "stored-facts log output (backend error)", logs)
}

// TestStoredFactsRouter_RefusesRatherThanFallingBackToTheMint pins the router's
// structural guarantee: a backend that cannot answer read-only is REFUSED, not
// asked through GetCredentials. Falling back would put the mint back on the
// comparison's path through the back door.
func TestStoredFactsRouter_RefusesRatherThanFallingBackToTheMint(t *testing.T) {
	mint := &mintCounter{}
	backend := &mintingOnlyBackend{mint: mint}
	r := &ClusterCredsRouter{Backend: backend}

	_, err := r.StoredFactsIndependentOfArgoCDSecret("prod-eu", "eks-token")
	if !errors.Is(err, ErrNoIndependentCredentialSource) {
		t.Fatalf("err = %v, want ErrNoIndependentCredentialSource — a backend with no read-only capability must be refused, never fetched from", err)
	}
	if mint.calls != 0 {
		t.Fatalf("the router fell back to a credential fetch and minted %d token(s); it must refuse instead", mint.calls)
	}
	if backend.getCredentialsCalls != 0 {
		t.Fatalf("the router called GetCredentials %d time(s); that is the route the mint lives on and the comparison must never take it", backend.getCredentialsCalls)
	}
}

// TestStoredFactsRouter_RefusesEveryNonBackendSource: inline, empty and
// anything unrecognised are all refused, so the comparison can never end up
// reading the live Secret back as its own expectation.
func TestStoredFactsRouter_RefusesEveryNonBackendSource(t *testing.T) {
	mint := &mintCounter{}
	facts := &factsBackend{mint: mint}
	r := &ClusterCredsRouter{Backend: facts}

	for _, source := range []string{"", "inline-kubeconfig", "something-new", "EKS-TOKEN", " eks-token"} {
		_, err := r.StoredFactsIndependentOfArgoCDSecret("prod-eu", source)
		if !errors.Is(err, ErrNoIndependentCredentialSource) {
			t.Errorf("credsSource=%q: err = %v, want ErrNoIndependentCredentialSource", source, err)
		}
	}
	if facts.calls != 0 {
		t.Errorf("the backend was read %d time(s) for a source that has no independent copy", facts.calls)
	}
	if mint.calls != 0 {
		t.Errorf("a refused source minted %d token(s)", mint.calls)
	}
}

// TestStoredFactsRouter_ArgoCDBackendIsRefused: when the backend IS the ArgoCD
// cluster-Secret reader, "the backend" and "the live Secret" are the same place,
// so reading it would compare the Secret with itself.
func TestStoredFactsRouter_ArgoCDBackendIsRefused(t *testing.T) {
	r := &ClusterCredsRouter{Backend: &ArgoCDProvider{}}
	for _, source := range []string{"secret-kubeconfig", "eks-token"} {
		if _, err := r.StoredFactsIndependentOfArgoCDSecret("prod-eu", source); !errors.Is(err, ErrNoIndependentCredentialSource) {
			t.Errorf("credsSource=%q: err = %v, want ErrNoIndependentCredentialSource", source, err)
		}
	}
}

// TestCanReadStoredFacts_AgreesWithTheRefusalOnEveryBackendShape is the
// anti-drift guard for Point 1.
//
// The comparison asks CanReadStoredFactsIndependentOfArgoCDSecret to decide what
// it may CLAIM (full scope, a full repair, "in sync"), and it calls
// StoredFactsIndependentOfArgoCDSecret to actually DO the read. If those two ever
// disagree, the endpoint says it checked something it never read — which is the
// bug this whole pass exists to close.
//
// So this walks every backend shape there is and asserts the yes/no answer and
// the real read land on the same side, for both backend-stored sources.
func TestCanReadStoredFacts_AgreesWithTheRefusalOnEveryBackendShape(t *testing.T) {
	shapes := []struct {
		name    string
		router  *ClusterCredsRouter
		wantCan bool
	}{
		{"nil router", nil, false},
		{"no backend at all", &ClusterCredsRouter{}, false},
		{"the backend IS the ArgoCD cluster-Secret reader", &ClusterCredsRouter{Backend: &ArgoCDProvider{}}, false},
		{"a backend that can only mint, with no read-only capability", &ClusterCredsRouter{Backend: &mintingOnlyBackend{mint: &mintCounter{}}}, false},
		{"a backend that answers read-only", &ClusterCredsRouter{Backend: &factsBackend{mint: &mintCounter{}}}, true},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			gotCan := shape.router.CanReadStoredFactsIndependentOfArgoCDSecret()
			if gotCan != shape.wantCan {
				t.Errorf("CanReadStoredFactsIndependentOfArgoCDSecret() = %v, want %v", gotCan, shape.wantCan)
			}

			for _, source := range []string{models.CredsSourceSecretKubeconfig, models.CredsSourceEKSToken} {
				_, err := shape.router.StoredFactsIndependentOfArgoCDSecret("prod-eu", source)
				refused := errors.Is(err, ErrNoIndependentCredentialSource)
				if refused == gotCan {
					t.Errorf(`credsSource=%q: the ANSWER and the READ disagree.

CanReadStoredFactsIndependentOfArgoCDSecret said %v, and StoredFactsIndependentOfArgoCDSecret %s.

WHY THIS MATTERS: the connection comparison uses the first one to decide what it may claim — full scope, a full-connection repair offer, "in sync" — and the second one to actually read. When they disagree the endpoint claims it checked the credential half of a connection it never read, and step 3 inherits a repair offer it must not have.

BOTH must go through storedFactsReader and nothing else. If you added a condition to one of them, move it into storedFactsReader so the other one gets it too.`,
						source, gotCan, map[bool]string{true: "refused", false: "went ahead with the read"}[refused])
				}
			}
		})
	}
}

// TestGetEKSToken_LogsCarryNoPresignedURLAndNoLength is the log-leak guard on
// the minting function itself.
//
// Two things used to be logged here and both are gone:
//
//   - the FULL presigned STS URL, at Debug. That URL IS the credential — anyone
//     holding it can sign in as Sharko for its lifetime.
//   - the token's LENGTH, at Info, so on by default in production. A length
//     narrows a guess.
//
// The function is not called here (it would need real AWS credentials). Instead
// the log-line shapes are asserted against the source, which is the thing that
// would regress.
func TestGetEKSToken_LogsCarryNoPresignedURLAndNoLength(t *testing.T) {
	body := readOwnSource(t, "aws_auth.go")

	// Only the LOG calls are searched. req.URL legitimately appears elsewhere
	// in this file — it is the thing being encoded into the token — so a
	// whole-file grep for it would fire on the encoding line and teach everyone
	// to ignore this test.
	logCalls := slogCallsIn(body)
	if len(logCalls) == 0 {
		t.Fatal("found no slog calls in aws_auth.go — this test can no longer see what it is guarding, so fix the extraction rather than deleting the test")
	}

	banned := []struct {
		fragment string
		why      string
	}{
		{`fullURL`, "the presigned STS URL IS the credential; logging it hands out a working sign-in"},
		{`req.URL`, "any log argument carrying the presigned URL is the credential in full"},
		{`tokenLength`, "a credential's length narrows a guess at it, and this line runs at Info"},
		{`len(token)`, "the token's length must not reach a log line in any labelled shape"},
		{`len(req.URL)`, "the presigned URL's length narrows a guess at what is inside it"},
		{`tokenPrefix`, "a prefix of a minted token is a prefix of a credential"},
		{`token[`, "a slice of a minted token is part of a credential"},
		{`sha256`, "a hash of a credential is not a safe summary of it — a guessable value is recovered from one"},
		{`", token)`, "the whole token must never be a log argument"},
	}
	for _, call := range logCalls {
		call = withoutBoolTests(call)
		for _, b := range banned {
			if strings.Contains(call, b.fragment) {
				t.Errorf("a log call in aws_auth.go contains %q — %s\n\nthe call: %s", b.fragment, b.why, call)
			}
		}
	}

	// And the useful, value-free diagnostic is still there: was the
	// cluster-name header attached. That is a bool, it carries no part of the
	// URL, and it is the setting that stops a token for one cluster being
	// replayed against another.
	if !strings.Contains(body, `"hasClusterHeader"`) {
		t.Error("the hasClusterHeader bool was removed; it is the one diagnostic here that is genuinely useful and carries no credential material")
	}
}

// withoutBoolTests strips strings.Contains(...) tests out of a log call before
// the banned-fragment sweep.
//
// A bool ABOUT the URL is fine and is the one diagnostic worth keeping:
// strings.Contains(req.URL, "x-k8s-aws-id") logs yes-or-no, not any part of the
// URL. Passing req.URL itself as a value is the leak. Without this, the sweep
// cannot tell those two apart and would force the useful bool out along with the
// leak.
func withoutBoolTests(call string) string {
	for {
		start := strings.Index(call, "strings.Contains(")
		if start < 0 {
			return call
		}
		open := start + len("strings.Contains") // index of '('
		depth, end := 0, -1
		for j := open; j < len(call); j++ {
			switch call[j] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = j
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return call
		}
		call = call[:start] + "<bool test>" + call[end+1:]
	}
}

// slogCallsIn extracts the text of every slog.* call in a Go source file, from
// the "slog." up to the balancing close paren, so a check can look at what is
// LOGGED rather than at everything the file mentions.
func slogCallsIn(body string) []string {
	var calls []string
	for i := 0; i < len(body); {
		start := strings.Index(body[i:], "slog.")
		if start < 0 {
			break
		}
		start += i
		open := strings.Index(body[start:], "(")
		if open < 0 {
			break
		}
		open += start
		depth, end := 0, -1
		for j := open; j < len(body); j++ {
			switch body[j] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = j
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			break
		}
		calls = append(calls, body[start:end+1])
		i = end + 1
	}
	return calls
}

// TestGetEKSToken_StillReturnsATokenShapedValue guards the contract the log
// removal must not have touched: callers still get a k8s-aws-v1 token back.
// getEKSToken itself needs real AWS credentials, so this checks the encoding
// step the function uses, the same way aws_auth_test.go's existing format tests
// do.
func TestGetEKSToken_StillReturnsATokenShapedValue(t *testing.T) {
	token := tokenFromURL("https://sts.eu-west-1.amazonaws.com/?Action=GetCallerIdentity")
	if !strings.HasPrefix(token, v1Prefix) {
		t.Errorf("token = %q, want the %q prefix", token, v1Prefix)
	}
}

// --- helpers ---------------------------------------------------------------

// mintingOnlyBackend is a cluster-credentials backend that can ONLY mint — it
// has no read-only capability at all. The router must refuse it.
type mintingOnlyBackend struct {
	mint                *mintCounter
	getCredentialsCalls int
}

func (b *mintingOnlyBackend) GetCredentials(name string) (*Kubeconfig, error) {
	b.getCredentialsCalls++
	token, err := b.mint.fn(context.Background(), name, "eu-west-1", "")
	if err != nil {
		return nil, err
	}
	return &Kubeconfig{Server: "https://" + name + ".invalid", Token: token}, nil
}
func (b *mintingOnlyBackend) ListClusters() ([]ClusterInfo, error)     { return nil, nil }
func (b *mintingOnlyBackend) SearchSecrets(_ string) ([]string, error) { return nil, nil }
func (b *mintingOnlyBackend) HealthCheck(_ context.Context) error      { return nil }

// factsBackend answers the read-only capability and counts the reads.
type factsBackend struct {
	mint  *mintCounter
	calls int
}

func (b *factsBackend) StoredConnectionFacts(_ string) (*StoredConnectionFacts, error) {
	b.calls++
	return &StoredConnectionFacts{Server: "https://stored.invalid", CredentialMintedPerFetch: true}, nil
}
func (b *factsBackend) GetCredentials(name string) (*Kubeconfig, error) {
	token, err := b.mint.fn(context.Background(), name, "eu-west-1", "")
	if err != nil {
		return nil, err
	}
	return &Kubeconfig{Server: "https://" + name + ".invalid", Token: token}, nil
}
func (b *factsBackend) ListClusters() ([]ClusterInfo, error)     { return nil, nil }
func (b *factsBackend) SearchSecrets(_ string) ([]string, error) { return nil, nil }
func (b *factsBackend) HealthCheck(_ context.Context) error      { return nil }

// readOwnSource reads a file from this package's own directory. Used by the
// log-shape guards, which assert on source rather than on a call that would
// need real AWS credentials.
func readOwnSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

// assertNoEKSSentinel fails when text carries the EKS metadata sentinel in any
// form a leak could take: raw, base64 spellings, SHA-256 hex and base64, prefix
// and suffix fragments, its byte length in labelled shapes, and masks whose
// length tracks it.
func assertNoEKSSentinel(t *testing.T, where, text string) {
	t.Helper()
	sum := sha256.Sum256([]byte(eksMetadataSentinel))
	n := len(eksMetadataSentinel)

	forms := []string{
		eksMetadataSentinel,
		base64.StdEncoding.EncodeToString([]byte(eksMetadataSentinel)),
		base64.RawStdEncoding.EncodeToString([]byte(eksMetadataSentinel)),
		base64.URLEncoding.EncodeToString([]byte(eksMetadataSentinel)),
		base64.RawURLEncoding.EncodeToString([]byte(eksMetadataSentinel)),
		hex.EncodeToString(sum[:]),
		base64.StdEncoding.EncodeToString(sum[:]),
		eksMetadataSentinel[:8],
		eksMetadataSentinel[:16],
		eksMetadataSentinel[n-8:],
		eksMetadataSentinel[n-16:],
	}
	for _, f := range forms {
		if strings.Contains(text, f) {
			t.Errorf("%s carries a form of the secret value (%q)", where, f)
		}
	}

	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n),
		fmt.Sprintf(`"length": %d`, n),
		fmt.Sprintf(`"len":%d`, n),
		fmt.Sprintf(`"len": %d`, n),
		fmt.Sprintf(`"bytes":%d`, n),
		fmt.Sprintf(`"size":%d`, n),
		fmt.Sprintf(`"tokenLength":%d`, n),
		fmt.Sprintf("length=%d", n),
		fmt.Sprintf("len=%d", n),
		fmt.Sprintf("bytes=%d", n),
	} {
		if strings.Contains(text, shape) {
			t.Errorf("%s carries the secret's byte length (%q) — a length narrows a guess", where, shape)
		}
	}

	for _, ch := range []string{"*", "•", "x", "●"} {
		for _, l := range []int{n - 1, n, n + 1} {
			mask := strings.Repeat(ch, l)
			if strings.Contains(text, mask) {
				t.Errorf("%s carries a mask whose length tracks the secret (%d of %q)", where, l, ch)
			}
		}
	}
}
