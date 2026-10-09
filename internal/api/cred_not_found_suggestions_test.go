package api

// cred_not_found_suggestions_test.go — "did you mean one of these?" is decided
// by a TYPE, never by words.
//
// # The product behaviour being protected
//
// When an operator tests a cluster and the secret Sharko looked for is not
// there, the response carries a list of similarly-named secrets so they can fix
// a typo in one click. That is real, useful behaviour and it must not regress.
//
// # Why the old way was wrong in both directions
//
// It used to be strings.Contains(err.Error(), "not found") in the handler.
//
//   - It could not survive a backend rephrasing itself. The day AWS or client-go
//     worded a missing secret differently, the suggestions silently stopped
//     appearing and nobody would have noticed.
//   - Worse, it fired on failures that are NOT about absence. An AccessDenied
//     whose message mentions something not being found got the same treatment as
//     a genuinely missing secret, so the operator was sent to fix a typo that was
//     never there while the real problem — a missing IAM permission — went
//     unmentioned.
//
// Now the PROVIDER says "this is missing" with credsafe.MarkNotFound, at the
// point it actually knows: the AWS SDK returned ResourceNotFoundException, or
// the Kubernetes read was a real apierrors.IsNotFound. The handler asks with
// credsafe.IsNotFound, which is errors.Is against a marker. No words anywhere.
//
// These tests drive the REAL providers and the REAL handler, and they cover both
// halves: the missing case still offers suggestions, and each other kind of
// failure explicitly does not.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// testClusterSuggestions drives POST /clusters/{name}/test through the real
// router and returns what the response offered.
func testClusterSuggestions(t *testing.T, provider providers.ClusterCredentialsProvider, name string) (suggestions []string, body string) {
	t.Helper()
	srv := newTestServer()
	srv.publishProviders(provider, nil, nil)
	router := NewRouter(srv, nil)

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+name+"/test", nil), "admin")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	body = w.Body.String()

	var resp struct {
		Suggestions []string `json:"suggestions"`
		Reachable   bool     `json:"reachable"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decoding the response: %v\nbody: %s", err, body)
	}
	if resp.Reachable {
		t.Fatalf("the cluster came back reachable, so the failure path never ran; body: %s", body)
	}
	return resp.Suggestions, body
}

// awsMissing is what the AWS SDK returns when the secret genuinely is not there.
func awsMissing() error {
	return &types.ResourceNotFoundException{
		Message: aws.String("Secrets Manager can't find the specified secret."),
	}
}

// TestSuggestions_OfferedWhenTheSecretIsGenuinelyMissing_AWS is end-state
// property 5 for the AWS Secrets Manager backend: the product behaviour still
// works after the substring check was removed.
func TestSuggestions_OfferedWhenTheSecretIsGenuinelyMissing_AWS(t *testing.T) {
	provider := providers.NewAWSSecretsManagerProviderWithFailingReadForTest(
		awsMissing(),
		[]string{"clusters/prod-eu-west", "clusters/prod-eu-central", "unrelated-thing"},
	)

	// The provider's own answer first: this is the decision everything rests on.
	_, err := provider.GetCredentials("prod-eu")
	if err == nil {
		t.Fatal("the fixture must fail")
	}
	if !credsafe.IsNotFound(err) {
		t.Fatalf(`the provider did not mark a ResourceNotFoundException as "the credentials are not there" (%v).

That marker is what drives the suggestion list now. Without it the operator gets no help finding their typo.`, err)
	}

	suggestions, body := testClusterSuggestions(t, provider, "prod-eu")
	if len(suggestions) == 0 {
		t.Fatalf(`no secret-name suggestions were offered for a genuinely missing secret.

This is a product regression: the operator mistyped a name and Sharko knows which names exist, so it must offer them. body: %s`, body)
	}
	for _, want := range []string{"clusters/prod-eu-west", "clusters/prod-eu-central"} {
		if !sliceHas(suggestions, want) {
			t.Errorf("the suggestions do not include %q; got %v", want, suggestions)
		}
	}
}

// TestSuggestions_OfferedWhenTheSecretIsGenuinelyMissing_K8s is the same
// property for the Kubernetes Secrets backend, where "missing" is
// apierrors.IsNotFound rather than an AWS SDK type.
func TestSuggestions_OfferedWhenTheSecretIsGenuinelyMissing_K8s(t *testing.T) {
	provider := providers.NewKubernetesSecretProviderWithFailingReadForTest(
		"sharko",
		apierrors.NewNotFound(corev1.Resource("secrets"), "prod-eu"),
		[]string{"prod-eu-west", "prod-eu-central"},
	)

	_, err := provider.GetCredentials("prod-eu")
	if err == nil {
		t.Fatal("the fixture must fail")
	}
	if !credsafe.IsNotFound(err) {
		t.Fatalf(`the provider did not mark a Kubernetes NotFound as "the credentials are not there" (%v)`, err)
	}

	suggestions, body := testClusterSuggestions(t, provider, "prod-eu")
	if len(suggestions) == 0 {
		t.Fatalf("no secret-name suggestions were offered for a genuinely missing Secret; body: %s", body)
	}
}

// TestSuggestions_NotOfferedForAnythingThatIsNotAbsence is end-state property 6,
// and it is the half the old substring check got wrong.
//
// Each of these is a real failure that is NOT "the secret is missing". Offering
// a list of names for any of them tells the operator to go hunting for a typo
// while the actual problem — a missing IAM permission, a rate limit, an
// unreachable API server — goes unmentioned.
//
// The AccessDenied case is deliberately worded to CONTAIN the words "not found",
// because that is exactly what the old check could not tell apart.
func TestSuggestions_NotOfferedForAnythingThatIsNotAbsence(t *testing.T) {
	existing := []string{"clusters/prod-eu-west", "clusters/prod-eu-central"}

	for _, tc := range []struct {
		name    string
		readErr error
	}{
		{
			// The trap case. The old strings.Contains(err.Error(), "not found")
			// fired on this and offered suggestions for a permissions problem.
			name: "an access denial whose message happens to contain the words",
			readErr: errors.New("AccessDeniedException: User is not authorized to perform " +
				"secretsmanager:GetSecretValue — the required permission was not found on the role"),
		},
		{
			name:    "a plain access denial",
			readErr: errors.New("AccessDeniedException: User is not authorized to perform secretsmanager:GetSecretValue"),
		},
		{
			name:    "a throttle",
			readErr: errors.New("ThrottlingException: Rate exceeded"),
		},
		{
			name:    "a timeout",
			readErr: fmt.Errorf("operation error Secrets Manager: GetSecretValue: %w", errors.New("context deadline exceeded")),
		},
		{
			name:    "the backend being unreachable",
			readErr: errors.New("dial tcp 10.0.0.1:443: connect: connection refused"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := providers.NewAWSSecretsManagerProviderWithFailingReadForTest(tc.readErr, existing)

			// The provider must not claim absence.
			_, err := provider.GetCredentials("prod-eu")
			if err == nil {
				t.Fatal("the fixture must fail")
			}
			if credsafe.IsNotFound(err) {
				t.Fatalf(`the provider marked this as "the credentials are not there" and it is not (%v).

Only a real ResourceNotFoundException means absence. "Sharko was not allowed to look" and "Sharko could not reach the backend" are different answers and the operator needs the difference.`, tc.readErr)
			}

			// And the handler must not offer suggestions.
			suggestions, body := testClusterSuggestions(t, provider, "prod-eu")
			if len(suggestions) > 0 {
				t.Errorf(`secret-name suggestions were offered for a failure that is not about absence: %v

This sends the operator to hunt for a typo that does not exist while the real problem goes unmentioned. body: %s`, suggestions, body)
			}
		})
	}
}

// TestSuggestions_NotOfferedForAnythingThatIsNotAbsence_K8s is the same negative
// half on the Kubernetes backend: Forbidden and a broken API server are not
// absence, even though the Secret really cannot be read.
func TestSuggestions_NotOfferedForAnythingThatIsNotAbsence_K8s(t *testing.T) {
	existing := []string{"prod-eu-west", "prod-eu-central"}

	for _, tc := range []struct {
		name   string
		getErr error
	}{
		{"a Forbidden", apierrors.NewForbidden(corev1.Resource("secrets"), "prod-eu", errors.New("no RBAC for secrets"))},
		{"a Unauthorized", apierrors.NewUnauthorized("the server has asked for the client to provide credentials")},
		{"a server timeout", apierrors.NewTimeoutError("request did not complete", 1)},
		{"an unreachable API server", errors.New("dial tcp 10.0.0.1:443: connect: connection refused")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := providers.NewKubernetesSecretProviderWithFailingReadForTest("sharko", tc.getErr, existing)

			_, err := provider.GetCredentials("prod-eu")
			if err == nil {
				t.Fatal("the fixture must fail")
			}
			if credsafe.IsNotFound(err) {
				t.Fatalf(`the provider marked %s as "the credentials are not there" and it is not (%v)`, tc.name, tc.getErr)
			}

			suggestions, body := testClusterSuggestions(t, provider, "prod-eu")
			if len(suggestions) > 0 {
				t.Errorf("secret-name suggestions were offered for %s: %v\nbody: %s", tc.name, suggestions, body)
			}
		})
	}
}

// TestSuggestions_TheHandlerNeverReadsTheErrorsWords is the structural guard on
// the fix, so the substring check cannot come back quietly.
//
// It reads the handler's own source. A comment may MENTION the old check (the
// one above the converted call site explains what changed and why), so the
// search is for a real call, not for the words.
func TestSuggestions_TheHandlerNeverReadsTheErrorsWords(t *testing.T) {
	body := readAPISource(t, "clusters_discover.go")

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue // a comment explaining the history is fine
		}
		if strings.Contains(trimmed, `strings.Contains(err.Error()`) {
			t.Errorf(`the cluster-test handler reads an error's WORDS again:

	%s

On a credential path there is nothing to read: a marked error's Error() is the one fixed safe sentence. Whatever this needs to know, the provider must say with a type — see credsafe.MarkNotFound / credsafe.IsNotFound.`, trimmed)
		}
	}

	// And the type check is really there. Without this, deleting the whole
	// branch would pass the sweep above.
	if !strings.Contains(body, "credsafe.IsNotFound(err)") {
		t.Error(`the credsafe.IsNotFound check is gone from the cluster-test handler, so no suggestions can ever be offered.

That is a product regression, not a fix. The operator needs the list of names when the secret really is missing.`)
	}
}

func sliceHas(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// readAPISource reads a file from this package's own directory. Used by the
// structural guard above, which asserts on the source rather than trying to
// provoke every possible shape of failure through the handler.
func readAPISource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}
