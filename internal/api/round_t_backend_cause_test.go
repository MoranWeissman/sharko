package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ep4 round T, T2 and T3. During break 2 (a wrong AWS key), Test connection
// said the general "could not read this cluster's sign-in details ... The
// server log for this request id says which step failed." sentence, and
// Diagnose connection's fix said to check that the credentials "still exist
// at the configured source" — a missing secret. Both were the wrong way: the
// cause was Sharko's AWS login. Now both say the same AWS sentence the
// Diagnostics page says, picked by the error's TYPES. Every value is fake.

// The old words these two answers must never say for an AWS login failure.
const (
	oldTestGeneralCredsSentence = "Sharko could not read this cluster's sign-in details from the configured credentials source."
	oldTestRequestIDWords       = "The server log for this request id says which step failed."
	oldDoctorMissingSecretFix   = "Check that the cluster is registered and its credentials still exist at the configured source (secret path or ArgoCD cluster Secret), then try again."
)

// roundTCluster is the cluster every case reads.
const roundTCluster = "spoke-1"

// smRead wraps err the way the AWS SDK does for a GetSecretValue call.
func smRead(err error) error {
	return &smithy.OperationError{ServiceID: "Secrets Manager", OperationName: "GetSecretValue", Err: err}
}

func roundTAWSCases() []struct {
	name string
	err  error
	want string
} {
	return []struct {
		name string
		err  error
		want string
	}{
		{"AWS turned the login down",
			smRead(&smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: awsOwnWords}),
			wantAWSLoginRefusedSentence},
		{"AWS never answered",
			smRead(&smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(awsOwnWords)}}),
			wantAWSUnreachableSentence},
		{"Sharko has no AWS login",
			smRead(fmt.Errorf("get identity: get credentials: %w",
				&smithy.OperationError{ServiceID: imds.ServiceID, OperationName: "GetMetadata",
					Err: &smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(awsOwnWords)}}})),
			wantAWSNoLoginSentence},
	}
}

// roundTProvider is the REAL AWS provider whose read of the cluster's secret
// fails with err, so the provider itself decides what the error carries.
func roundTProvider(err error) providers.ClusterCredentialsProvider {
	return providers.NewAWSSecretsManagerProviderWithPerNameReadsForTest("", map[string]error{roundTCluster: err})
}

func assertNoAWSWords(t *testing.T, where, body string) {
	t.Helper()
	for _, banned := range []string{awsOwnWords, "acct-words-zz9", "UnrecognizedClientException", "ec2imds", "GetMetadata", "GetSecretValue"} {
		if strings.Contains(body, banned) {
			t.Errorf("%s carries the store's own words %q: %s", where, banned, body)
		}
	}
}

func TestTestCluster_AWSLoginFailureSaysTheAWSCause(t *testing.T) {
	for _, tc := range roundTAWSCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, body := testClusterSuggestions(t, roundTProvider(tc.err), roundTCluster)
			var resp testClusterResponse
			if err := json.Unmarshal([]byte(body), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.ErrorMessage != tc.want {
				t.Errorf("error_message:\n got: %q\nwant: %q", resp.ErrorMessage, tc.want)
			}
			if len(resp.Steps) == 0 || resp.Steps[0].Name != "Fetch credentials" || resp.Steps[0].Detail != tc.want {
				t.Errorf("first step = %+v, want Fetch credentials with the AWS sentence", resp.Steps)
			}
			// The cluster was never asked: the answer says the login read
			// failed, so the UI and CLI do not call the cluster unreachable.
			if resp.Stage != verifyStageCredentials {
				t.Errorf("stage = %q, want %q", resp.Stage, verifyStageCredentials)
			}
			for _, banned := range []string{oldTestGeneralCredsSentence, oldTestRequestIDWords} {
				if strings.Contains(body, banned) {
					t.Errorf("the test answer still says %q: %s", banned, body)
				}
			}
			assertNoAWSWords(t, "the test answer", body)
		})
	}

	// Any other store failure keeps the general sentence.
	t.Run("some other store failure", func(t *testing.T) {
		_, body := testClusterSuggestions(t, &recordingCredProvider{err: credsafe.Mark(errors.New("zz9-other-store-words"))}, roundTCluster)
		var resp testClusterResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.ErrorMessage != credsafe.Message {
			t.Errorf("error_message = %q, want the general sentence %q", resp.ErrorMessage, credsafe.Message)
		}
		if strings.Contains(body, "zz9-other-store-words") {
			t.Errorf("the store's words leaked: %s", body)
		}
	})
}

func TestDoctor_AWSLoginFailureFixSaysTheAWSCause(t *testing.T) {
	const wantDetail = `Sharko could not read the login for cluster "spoke-1" from your secrets store.`
	for _, tc := range roundTAWSCases() {
		t.Run(tc.name, func(t *testing.T) {
			srv := newDoctorTestServer(t)
			installCredProvider(srv, roundTProvider(tc.err), nil, nil)

			check, creds := srv.doctorCheckCredentials(context.Background(), roundTCluster)
			if check.Status != doctorStatusFail || creds != nil {
				t.Fatalf("status %q creds %v, want fail and none", check.Status, creds)
			}
			if check.Detail != wantDetail {
				t.Errorf("detail:\n got: %q\nwant: %q", check.Detail, wantDetail)
			}
			if check.Fix != tc.want {
				t.Errorf("fix:\n got: %q\nwant: %q", check.Fix, tc.want)
			}
			whole := check.Detail + " " + check.Fix
			for _, banned := range []string{oldDoctorMissingSecretFix, "still exist at the configured source", oldTestGeneralCredsSentence, oldTestRequestIDWords} {
				if strings.Contains(whole, banned) {
					t.Errorf("check 1 still says %q: %s", banned, whole)
				}
			}
			assertNoAWSWords(t, "check 1", whole)
		})
	}

	t.Run("the secret is not there", func(t *testing.T) {
		srv := newDoctorTestServer(t)
		installCredProvider(srv, providers.NewAWSSecretsManagerProviderWithPerNameReadsForTest("", nil), nil, nil)
		check, _ := srv.doctorCheckCredentials(context.Background(), roundTCluster)
		const wantFix = `Sharko could not find this cluster's secret in your secrets store. ` +
			`It looked for "spoke-1", and it is not there. ` +
			`Check the secret's name, or set this cluster's secret path to the exact name.`
		if check.Detail != wantDetail || check.Fix != wantFix {
			t.Errorf("detail %q\nfix %q\nwant %q\n and %q", check.Detail, check.Fix, wantDetail, wantFix)
		}
	})

	t.Run("some other store failure keeps the old fix", func(t *testing.T) {
		srv := newDoctorTestServer(t)
		installCredProvider(srv, &recordingCredProvider{err: credsafe.Mark(errors.New("zz9-other-store-words"))}, nil, nil)
		check, _ := srv.doctorCheckCredentials(context.Background(), roundTCluster)
		if check.Fix != oldDoctorMissingSecretFix {
			t.Errorf("fix = %q, want the general fix", check.Fix)
		}
		if strings.Contains(check.Detail, "zz9-other-store-words") {
			t.Errorf("the store's words leaked: %s", check.Detail)
		}
	})

	// Through the real endpoint too, so the answer the UI reads is checked.
	t.Run("through POST /doctor", func(t *testing.T) {
		srv := newDoctorTestServer(t)
		withDoctorGitFiles(srv)
		installCredProvider(srv, roundTProvider(roundTAWSCases()[0].err), nil, nil)
		router := NewRouter(srv, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, operatorReq("/api/v1/clusters/"+roundTCluster+"/doctor"))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), jsonText(t, wantAWSLoginRefusedSentence)) {
			t.Errorf("the doctor answer does not carry the AWS sentence: %s", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "still exist at the configured source") {
			t.Errorf("the doctor answer still points at a missing secret: %s", w.Body.String())
		}
		assertNoAWSWords(t, "the doctor answer", w.Body.String())
	})
}

// jsonText is s as it appears inside a JSON string.
func jsonText(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Trim(string(b), `"`)
}
