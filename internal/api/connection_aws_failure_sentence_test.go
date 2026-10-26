package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ep4, serious 6. The secrets store test used to say "Sharko can't reach your
// secrets store. Check the connection settings and try again." when AWS had
// answered and turned Sharko's login down (an access key switched off). That
// was false: AWS was reached. These tests pin the two new sentences word for
// word, and ban the old one for both cases.

const (
	wantAWSLoginRefusedSentence = "AWS answered, but it turned down Sharko's login, so Sharko could not read your secrets store. " +
		"The access key may be wrong or switched off, or it may not be allowed to read these secrets. " +
		"Check Sharko's AWS login in Settings → Secrets Provider. " +
		"The docs page \"Give Sharko an AWS login when it does not run on EKS\" shows how to set it up."
	wantAWSUnreachableSentence = "Sharko could not get an answer from AWS, so it could not read your secrets store. " +
		"Check that this server can reach AWS over the network, and check the region in Settings → Secrets Provider. " +
		"The docs page \"Give Sharko an AWS login when it does not run on EKS\" shows how to set up Sharko's AWS login."

	// What AWS itself said. It must never reach the answer.
	awsOwnWords = "The security token included in the request is invalid. acct-words-zz9"
)

// awsRefusedErr is shaped like the AWS SM provider's ListClusters error for a
// switched-off access key: AWS's answer, inside the SDK's operation error,
// wrapped by the provider and marked as a credentials-backend failure.
func awsRefusedErr() error {
	return credsafe.Mark(fmt.Errorf("listing secrets with prefix %q: %w", "clusters/",
		&smithy.OperationError{ServiceID: "Secrets Manager", OperationName: "ListSecrets",
			Err: &smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: awsOwnWords}}))
}

// awsUnreachableErr is the same, for a request that never got an answer.
func awsUnreachableErr() error {
	return credsafe.Mark(fmt.Errorf("listing secrets with prefix %q: %w", "clusters/",
		&smithy.OperationError{ServiceID: "Secrets Manager", OperationName: "ListSecrets",
			Err: &smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(awsOwnWords)}}}))
}

func TestAWSFailureSentence_ProviderTestTellsTheTwoApart(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		wantID string
		want   string
	}{
		{"AWS turned the login down", awsRefusedErr(), "connectionFailureVaultAWSLoginRefused", wantAWSLoginRefusedSentence},
		{"AWS never answered", awsUnreachableErr(), "connectionFailureVaultAWSUnreachable", wantAWSUnreachableSentence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newIsolatedTestServer(t)
			installCredProvider(srv, failingCredProvider{err: tc.err}, nil, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/test", nil)
			w := httptest.NewRecorder()
			NewRouter(srv, nil).ServeHTTP(w, req)

			var body map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("answer is not JSON: %v (%s)", err, w.Body.String())
			}
			if got, _ := body["message"].(string); got != tc.want {
				t.Errorf("message:\n got: %q\nwant: %q", got, tc.want)
			}
			if got, _ := body["message_id"].(string); got != tc.wantID {
				t.Errorf("message_id = %q, want %q", got, tc.wantID)
			}
			raw := w.Body.String()
			for _, banned := range []string{
				"Sharko can't reach your secrets store. Check the connection settings and try again.",
				awsOwnWords,
				"acct-words-zz9",
				"UnrecognizedClientException",
				"Settings → Connections",
			} {
				if strings.Contains(raw, banned) {
					t.Errorf("the answer carries %q: %s", banned, raw)
				}
			}
		})
	}
}

// The refused sentence must not say Sharko could not reach anything: AWS was
// reached. And each sentence points at the right page and the docs page.
func TestAWSFailureSentence_Wording(t *testing.T) {
	for _, kind := range []connectionKind{connectionKindVault, connectionKindSecrets} {
		refused := renderConnectionFailure(kind, connectionFailureCodeAWSLoginRefused)
		if refused != wantAWSLoginRefusedSentence {
			t.Errorf("%s refused:\n got: %q\nwant: %q", kind, refused, wantAWSLoginRefusedSentence)
		}
		for _, banned := range []string{"can't reach", "could not reach", "Check the connection settings"} {
			if strings.Contains(refused, banned) {
				t.Errorf("%s refused sentence says %q, but AWS was reached", kind, banned)
			}
		}
		unreachable := renderConnectionFailure(kind, connectionFailureCodeAWSUnreachable)
		if unreachable != wantAWSUnreachableSentence {
			t.Errorf("%s unreachable:\n got: %q\nwant: %q", kind, unreachable, wantAWSUnreachableSentence)
		}
		for _, s := range []string{refused, unreachable} {
			if !strings.Contains(s, "Settings → Secrets Provider") {
				t.Errorf("%s sentence does not point at Settings → Secrets Provider: %q", kind, s)
			}
			if strings.Contains(s, "Settings → Connections") {
				t.Errorf("%s sentence points at Settings → Connections: %q", kind, s)
			}
		}
	}
}

// Only the secrets store kinds get the AWS sentences. A Git or ArgoCD test
// that happens to see an AWS-shaped error keeps its own words.
func TestAWSFailureSentence_OnlyForSecretsStores(t *testing.T) {
	for _, kind := range []connectionKind{connectionKindGit, connectionKindArgoCD, connectionKindOther} {
		code := connectionFailureCodeFor(kind, awsRefusedErr())
		if code == connectionFailureCodeAWSLoginRefused || code == connectionFailureCodeAWSUnreachable {
			t.Errorf("%s got the AWS code %s", kind, code)
		}
	}
}
