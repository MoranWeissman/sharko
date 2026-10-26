package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/smithy-go"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ep4 round S, S3. With a name prefix and the least-privilege policy from
// the docs (Sharko may read only names under the prefix), a secret that is
// really missing gives "not found" for "<prefix><name>" and "access denied"
// for the plain name. Before, that access denied was taken as the cause, and
// the connection page told the person AWS had turned down Sharko's login and
// to update the AWS login Secret. The login was fine: the secret was just
// missing. Now every surface says the secret is not there and names what
// Sharko tried. Every value here is fake.

// The exact sentences, written out by hand — never built from the format
// constants, so a change to the words fails here.
const (
	wantSecretNotFoundNotAllowed = `Sharko could not find this cluster's secret in your secrets store. ` +
		`It looked for "sharko/prod-eu", and it is not there. ` +
		`Sharko is not allowed to read "prod-eu", so it could not look there. ` +
		`Check the secret's name, or set this cluster's secret path to the exact name.`
	wantSecretNotFoundBoth = `Sharko could not find this cluster's secret in your secrets store. ` +
		`It looked for "sharko/prod-eu" and "prod-eu", and none of them is there. ` +
		`Check the secret's name, or set this cluster's secret path to the exact name.`
	wantSecretNotFoundOne = `Sharko could not find this cluster's secret in your secrets store. ` +
		`It looked for "prod-eu", and it is not there. ` +
		`Check the secret's name, or set this cluster's secret path to the exact name.`
)

// fakeAWSWordsS3 stands for words AWS sent back; they must never reach an
// answer.
const fakeAWSWordsS3 = "FAKE-AWS-S3-WORDS-not-allowed-on-this-resource"

func awsDeniedS3() error {
	return &smithy.GenericAPIError{Code: "AccessDeniedException", Message: fakeAWSWordsS3}
}

// providerErrFor runs the REAL AWS provider's read-only lookup and returns
// its error, so the test drives the provider's own decision.
func providerErrFor(t *testing.T, prefix string, errs map[string]error) error {
	t.Helper()
	p := providers.NewAWSSecretsManagerProviderWithPerNameReadsForTest(prefix, errs)
	_, err := p.StoredConnectionFacts(comparisonCluster)
	if err == nil {
		t.Fatal("the fixture must fail")
	}
	return err
}

// assertNotTheLoginSentence bans the old wording for this case.
func assertNotTheLoginSentence(t *testing.T, where, body string) {
	t.Helper()
	for _, banned := range []string{
		wantAWSLoginRefusedSentence,
		"turned down Sharko's login",
		"update that Secret, then restart Sharko",
		fakeAWSWordsS3,
		"AccessDeniedException",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("%s still says %q: %s", where, banned, body)
		}
	}
}

func TestSecretNotFoundSentence_ExactWords(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		errs   map[string]error
		want   string
	}{
		{"not under the prefix, plain name not allowed", "sharko/", map[string]error{comparisonCluster: awsDeniedS3()}, wantSecretNotFoundNotAllowed},
		{"not there under either name", "sharko/", nil, wantSecretNotFoundBoth},
		{"no prefix, not there", "", nil, wantSecretNotFoundOne},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := secretNotFoundSentence(providerErrFor(t, tc.prefix, tc.errs))
			if !ok {
				t.Fatal("no not-found sentence for a missing secret")
			}
			if got != tc.want {
				t.Errorf("sentence:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}

	// Anything that is not "the secret is not there" gets no such sentence.
	for _, err := range []error{
		nil,
		errors.New("zz9-some-other-failure"),
		credsafe.MarkNotFound(errors.New("not found, but no names")),
		providerErrFor(t, "", map[string]error{comparisonCluster: awsDeniedS3()}),
		providerErrFor(t, "sharko/", map[string]error{"sharko/" + comparisonCluster: awsDeniedS3()}),
	} {
		if s, ok := secretNotFoundSentence(err); ok {
			t.Errorf("secretNotFoundSentence(%v) = %q, want none", err, s)
		}
	}
}

func TestConnectionCheck_MissingSecretUnderThePrefixIsNotALoginProblem(t *testing.T) {
	err := providerErrFor(t, "sharko/", map[string]error{comparisonCluster: awsDeniedS3()})
	if got := providers.ClassifyAWSFailure(err); got != providers.AWSFailureNone {
		t.Fatalf("ClassifyAWSFailure = %v, want none: the login is fine", got)
	}

	live := expectedLiveSecretForFixture(t)
	live.Annotations = map[string]string{
		clusterreconciler.AnnotationRevision:  reconTestCommit,
		clusterreconciler.AnnotationWrittenAt: "2026-08-18T09:00:00Z",
	}
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{failWith: err})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var cmp connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &cmp); err != nil {
		t.Fatal(err)
	}
	if cmp.Status != "check_failed" {
		t.Fatalf("status = %q, want check_failed", cmp.Status)
	}
	if cmp.FailureReason != wantSecretNotFoundNotAllowed {
		t.Errorf("failure_reason:\n got: %q\nwant: %q", cmp.FailureReason, wantSecretNotFoundNotAllowed)
	}
	assertNotTheLoginSentence(t, "the comparison answer", w.Body.String())

	w = httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	out := decodeRecon(t, w)
	if out.Sync.Reason != wantSecretNotFoundNotAllowed {
		t.Errorf("sync.reason:\n got: %q\nwant: %q", out.Sync.Reason, wantSecretNotFoundNotAllowed)
	}
	assertNotTheLoginSentence(t, "the connection page answer", w.Body.String())
}

func TestTestCluster_MissingSecretUnderThePrefixSaysNotFound(t *testing.T) {
	provider := providers.NewAWSSecretsManagerProviderWithPerNameReadsForTest("sharko/", map[string]error{comparisonCluster: awsDeniedS3()})
	_, body := testClusterSuggestions(t, provider, comparisonCluster)
	var resp testClusterResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ErrorMessage != wantSecretNotFoundNotAllowed {
		t.Errorf("error_message:\n got: %q\nwant: %q", resp.ErrorMessage, wantSecretNotFoundNotAllowed)
	}
	if len(resp.Steps) == 0 || resp.Steps[0].Detail != wantSecretNotFoundNotAllowed {
		t.Errorf("first step = %+v, want the not-found sentence", resp.Steps)
	}
	assertNotTheLoginSentence(t, "the test answer", body)
}

func TestDiagnose_MissingSecretUnderThePrefixSaysNotFound(t *testing.T) {
	srv := newIsolatedTestServer(t)
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(diagnoseCredLookupManagedClusters),
	}})
	installCredProvider(srv, providers.NewAWSSecretsManagerProviderWithPerNameReadsForTest("sharko/", map[string]error{
		"no-override": awsDeniedS3(),
	}), nil, nil)

	router := NewRouter(srv, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/clusters/no-override/diagnose", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body=%s)", w.Code, w.Body.String())
	}
	var resp errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	const want = `Sharko could not find this cluster's secret in your secrets store. ` +
		`It looked for "sharko/no-override", and it is not there. ` +
		`Sharko is not allowed to read "no-override", so it could not look there. ` +
		`Check the secret's name, or set this cluster's secret path to the exact name.`
	if resp.Error != want {
		t.Errorf("error:\n got: %q\nwant: %q", resp.Error, want)
	}
	assertNotTheLoginSentence(t, "the diagnose answer", w.Body.String())
}
