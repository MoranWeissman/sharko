package orchestrator

// secret_failure_leak_test.go — the positive-control leak sweep for the addon
// secret path (S2), and the catalog completeness guard behind it.
//
// # How this differs from secrets_sentinel_test.go next door
//
// That file plants the sentinel in the secret VALUE and proves the value never
// escapes. It passed the whole time the leak was live, because the leak was
// never about the value: it was
//
//	Error: fmt.Sprintf("fetching key %q from %q: %v", key, providerPath, fetchErr)
//
// putting the BACKEND'S OWN ERROR TEXT on a `json:"error"` field. A backend
// error is exactly where a misbehaving SDK puts a fragment of a credential —
// a presigned URL, a token, whatever a credential chain wrote into its
// message.
//
// So this file plants the sentinel in the ERROR TEXT instead. It is the test
// that would have caught it.
//
// # It exercises the real entry point, and sweeps the real wire bytes
//
// Not createAddonSecrets. RegisterCluster — the orchestrator entry point
// behind POST /api/v1/clusters — and then json.Marshal over the
// *RegisterClusterResult it returns, which is the exact value
// handleRegisterCluster hands to writeJSON. So the bytes swept here are the
// bytes that go on the wire.
//
// WHAT THIS DOES NOT COVER, written down rather than implied: it stops at the
// orchestrator, so it does not exercise HTTP routing, auth or the middleware
// chain. Nothing in that chain rewrites the body, but this test does not prove
// that.

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// secretErrorSentinel is a fake, token-shaped value planted inside a secrets
// backend's error MESSAGE. Never a real credential.
const secretErrorSentinel = "CANARY-a41f77c2-secret-store-said-this-9db3e015"

// leakForms returns every shape the sentinel could plausibly wear if it made
// it out: itself, three base64 variants, base32, three hashes, and the
// leading/trailing/middle fragments that a truncating formatter would leave.
//
// Length-derived forms are handled separately by maskWidthLeaks below — a mask
// whose width tracks the secret discloses its length even though none of these
// strings appear.
func leakForms(secret string) map[string]string {
	sum256 := sha256.Sum256([]byte(secret))
	sum1 := sha1.Sum([]byte(secret)) //nolint:gosec // leak-detection only, not a security primitive
	sum5 := md5.Sum([]byte(secret))  //nolint:gosec // leak-detection only, not a security primitive
	forms := map[string]string{
		"raw":            secret,
		"base64-std":     base64.StdEncoding.EncodeToString([]byte(secret)),
		"base64-raw":     base64.RawStdEncoding.EncodeToString([]byte(secret)),
		"base64-url":     base64.URLEncoding.EncodeToString([]byte(secret)),
		"base32":         base32.StdEncoding.EncodeToString([]byte(secret)),
		"sha256-hex":     hex.EncodeToString(sum256[:]),
		"sha1-hex":       hex.EncodeToString(sum1[:]),
		"md5-hex":        hex.EncodeToString(sum5[:]),
		"prefix-16":      secret[:16],
		"suffix-16":      secret[len(secret)-16:],
		"middle-16":      secret[len(secret)/2-8 : len(secret)/2+8],
		"uppercased":     strings.ToUpper(secret),
		"lowercased":     strings.ToLower(secret),
		"quoted-escaped": strings.Trim(mustJSON(secret), `"`),
	}
	return forms
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// maskWidthLeaks reports any run of masking characters whose length equals the
// secret's length — a mask that tracks the secret discloses how long it is.
func maskWidthLeaks(haystack string, secretLen int) []string {
	var found []string
	for _, ch := range []string{"*", "•", "x", "X", "#", "."} {
		if strings.Contains(haystack, strings.Repeat(ch, secretLen)) {
			found = append(found, fmt.Sprintf("a run of %d %q characters — that width is the secret's length", secretLen, ch))
		}
	}
	return found
}

// sweep reports every leak form present in haystack.
func sweep(haystack, secret string) []string {
	var hits []string
	for name, form := range leakForms(secret) {
		if strings.Contains(haystack, form) {
			hits = append(hits, name)
		}
	}
	hits = append(hits, maskWidthLeaks(haystack, len(secret))...)
	return hits
}

// TestSweep_FindsTheSentinel is the POSITIVE CONTROL, and it runs first on
// purpose.
//
// A sweep that has quietly stopped working passes every leak test in this
// file while proving nothing. This one hands the sweep a string that DOES
// contain the sentinel — in several shapes — and fails if the sweep comes back
// empty. Only after that is "the sweep found nothing" evidence of anything.
func TestSweep_FindsTheSentinel(t *testing.T) {
	forms := leakForms(secretErrorSentinel)
	for name, form := range forms {
		haystack := `{"error":"the backend said: ` + form + `"}`
		if hits := sweep(haystack, secretErrorSentinel); len(hits) == 0 {
			t.Errorf("the sweep failed to find the %s form of the sentinel — it would report a leak as clean", name)
		}
	}
	// And the length-derived form, which no substring above would catch.
	masked := `{"error":"value ` + strings.Repeat("*", len(secretErrorSentinel)) + `"}`
	if hits := sweep(masked, secretErrorSentinel); len(hits) == 0 {
		t.Error("the sweep failed to notice a mask whose width is the secret's length")
	}
	// Control in the other direction: a clean string must come back clean, or
	// the sweep is just always saying yes.
	if hits := sweep(`{"error":"Sharko could not read this addon's secret value."}`, secretErrorSentinel); len(hits) != 0 {
		t.Errorf("the sweep reported a leak in a clean string: %v", hits)
	}
}

// TestRegisterCluster_BackendErrorTextNeverReachesTheResponse is the real
// proof. The secrets backend fails with the sentinel INSIDE its error message,
// and the sentinel must not appear anywhere in the serialized response.
func TestRegisterCluster_BackendErrorTextNeverReachesTheResponse(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCredsWithRaw(), argocd, git, defaultGitOps(), defaultPaths(), nil)

	// The shape a real leak takes: the backend's own words carry the secret.
	backendErr := fmt.Errorf("AccessDeniedException: request signed with %s was rejected", secretErrorSentinel)
	orch.SetSecretManagement(defaultSecretDefs(), &mockSecretFetcher{err: backendErr}, fakeClientFactory())

	var result *RegisterClusterResult
	var err error
	logs := captureOrchestratorLogs(t, func() {
		result, err = orch.RegisterCluster(context.Background(), RegisterClusterRequest{
			Name:   "prod-eu",
			Addons: map[string]bool{"datadog": true},
			Region: "eu-west-1",
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FailedSecrets) != 1 {
		t.Fatalf("expected the fetch failure recorded, got %d", len(result.FailedSecrets))
	}

	// The bytes handleRegisterCluster would write.
	body, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatalf("marshalling the result: %v", marshalErr)
	}
	if hits := sweep(string(body), secretErrorSentinel); len(hits) != 0 {
		t.Fatalf("the backend's error text reached the response body as %v:\n%s", hits, body)
	}
	// The whole error, not just the sentinel: no part of the backend's
	// sentence belongs on the wire.
	for _, word := range []string{"AccessDeniedException", "request signed with", "was rejected"} {
		if strings.Contains(string(body), word) {
			t.Errorf("the backend's own wording %q reached the response body:\n%s", word, body)
		}
	}

	// The log is allowed to be more detailed than the response — but not this
	// detailed. credsafe.Sentence is what stands between the two.
	if hits := sweep(logs, secretErrorSentinel); len(hits) != 0 {
		t.Errorf("the backend's error text reached the server log as %v:\n%s", hits, logs)
	}
}

// TestSecretFailure_SentenceStillExplainsTheCategory is the counterweight.
// Redacting is easy; redacting without turning every failure into "something
// went wrong" is the actual requirement. A fix that passes the sweep above and
// fails this one has traded one defect for another.
func TestSecretFailure_SentenceStillExplainsTheCategory(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCredsWithRaw(), argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(defaultSecretDefs(),
		&mockSecretFetcher{err: fmt.Errorf("AccessDeniedException: %s", secretErrorSentinel)},
		fakeClientFactory())

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "prod-eu", Addons: map[string]bool{"datadog": true}, Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := result.FailedSecrets[0]

	// 1. It says WHICH STEP failed — reading from the store, not writing to
	//    the cluster. Those are two different things to go and look at.
	if got.Code != SecretFailureFetch {
		t.Errorf("Code = %q, want the fetch code so a caller can tell the two steps apart", got.Code)
	}
	// 2. It names the addon, the key and the secret — all Sharko's own
	//    configuration out of the Git catalog, none of it backend output.
	if got.Addon != "datadog" {
		t.Errorf("Addon = %q, want the addon named so the operator knows which one to look at", got.Addon)
	}
	if got.Key == "" {
		t.Error("Key is empty — the operator cannot tell which key of the secret failed")
	}
	if got.Name == "" {
		t.Error("Name is empty — the operator cannot tell which secret failed")
	}
	// 3. The sentence is a whole sentence that points somewhere, not a shrug.
	sentence := got.Error.String()
	if !strings.Contains(sentence, "secrets store") {
		t.Errorf("the sentence does not say which system to go and look at: %q", sentence)
	}
	if !strings.HasSuffix(strings.TrimSpace(sentence), ".") || len(sentence) < 60 {
		t.Errorf("the sentence is not a complete, actionable sentence: %q", sentence)
	}
	// 4. And the provider path — the location of the secret inside the
	//    backend — is not in it.
	if strings.Contains(sentence, "secrets/datadog") || strings.Contains(string(mustJSONBytes(result)), "secrets/datadog") {
		t.Errorf("the provider path reached the response: %q", sentence)
	}
}

func mustJSONBytes(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestSecretFailureSentences_EveryDeclaredCodeHasOne parses the
// SecretFailureCode const block in secret_failure.go and fails BY NAME on any
// code with no catalog sentence.
//
// A LIST, not a count. A count answers "did I see enough?", which is the
// question that fails with a misleading message the day the file is
// restructured. This one names the code that has no sentence.
func TestSecretFailureSentences_EveryDeclaredCodeHasOne(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "secret_failure.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing secret_failure.go: %v", err)
	}

	var declared []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "SecretFailureCode" {
			return true
		}
		for _, name := range spec.Names {
			declared = append(declared, name.Name)
		}
		return true
	})

	// Anchor: if the parse stops finding the const block, this fails loudly
	// instead of passing over an empty list.
	for _, want := range []string{"SecretFailureFetch", "SecretFailureWrite"} {
		found := false
		for _, got := range declared {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected to find the declared code %s in secret_failure.go — either it was removed or this guard stopped seeing the const block", want)
		}
	}

	byName := map[string]SecretFailureCode{
		"SecretFailureFetch": SecretFailureFetch,
		"SecretFailureWrite": SecretFailureWrite,
	}
	for _, name := range declared {
		code, known := byName[name]
		if !known {
			t.Errorf("a new SecretFailureCode %s was declared but this guard does not know it — add it to byName AND give it a catalog sentence", name)
			continue
		}
		sentence, ok := secretFailureSentences[code]
		if !ok || strings.TrimSpace(sentence) == "" {
			t.Errorf("SecretFailureCode %s has no sentence in secretFailureSentences — a code cannot ship with a blank message", name)
		}
	}
}
