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

	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ep4, serious 6. The secrets store test used to say "Sharko can't reach your
// secrets store. Check the connection settings and try again." when AWS had
// answered and turned Sharko's login down (an access key switched off). That
// was false: AWS was reached. These tests pin the two new sentences word for
// word, and ban the old one for both cases.
//
// ep4 round R. R1: the refused sentence used to send people to Settings →
// Secrets Provider, which cannot change Sharko's AWS login; it now says where
// the login really is. R2: a third case, Sharko has no AWS login at all, which
// used to come out as "AWS did not answer".

const (
	wantAWSLoginRefusedSentence = "AWS answered, but it turned down Sharko's login, so Sharko could not read your secrets store. " +
		"The login may be wrong or switched off, or it may not be allowed to read these secrets. " +
		"Check the AWS login Sharko runs with. " +
		"If it is an access key, the key is in the Kubernetes Secret given to Sharko: update that Secret, then restart Sharko. " +
		"The docs page \"Give Sharko an AWS login\" shows how."
	wantAWSUnreachableSentence = "Sharko could not get an answer from AWS, so it could not read your secrets store. " +
		"Check that this server can reach AWS over the network, and check the region in Settings → Secrets Provider. " +
		"The docs page \"Give Sharko an AWS login\" shows how to set up Sharko's AWS login."
	wantAWSNoLoginSentence = "Sharko has no AWS login, so it could not read your secrets store. " +
		"On EKS, give Sharko's service account an AWS role (the IRSA annotation), or use EKS Pod Identity. " +
		"Anywhere else, give Sharko an AWS access key. " +
		"Then restart Sharko. " +
		"The docs page \"Give Sharko an AWS login\" shows how."

	// The old refused-login words (ep4 round R, R1). Settings → Secrets
	// Provider cannot change Sharko's AWS login, so no AWS login sentence may
	// send people there for it again.
	oldAWSLoginInSettingsWords = "Check Sharko's AWS login in Settings → Secrets Provider"

	// The old docs title (ep4 round S, S5). It told EKS users the page was
	// not for them, so no sentence may name it again.
	oldAWSLoginDocsTitle = "when it does not run on EKS"

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

// awsNoLoginErr is the same, for Sharko having no AWS login at all: the SDK's
// last place to look for a login is the EC2 instance metadata service, and
// that call does not connect. Its error carries a RequestSendError, the same
// type as "AWS never answered" — only the IMDS operation error around it says
// what really happened.
func awsNoLoginErr() error {
	return credsafe.Mark(fmt.Errorf("listing secrets with prefix %q: %w", "clusters/",
		&smithy.OperationError{ServiceID: "Secrets Manager", OperationName: "ListSecrets",
			Err: fmt.Errorf("get identity: get credentials: %w",
				&smithy.OperationError{ServiceID: imds.ServiceID, OperationName: "GetMetadata",
					Err: &smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(awsOwnWords)}}})}))
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
		{"Sharko has no AWS login", awsNoLoginErr(), "connectionFailureVaultAWSNoLogin", wantAWSNoLoginSentence},
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
				oldAWSLoginInSettingsWords,
				"ec2imds",
				"GetMetadata",
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
		noLogin := renderConnectionFailure(kind, connectionFailureCodeAWSNoLogin)
		if noLogin != wantAWSNoLoginSentence {
			t.Errorf("%s no login:\n got: %q\nwant: %q", kind, noLogin, wantAWSNoLoginSentence)
		}
		// Nothing was sent to AWS, so the no-login sentence says neither
		// "turned down" nor "no answer".
		for _, banned := range []string{"turned down", "could not get an answer", "can't reach", "could not reach"} {
			if strings.Contains(noLogin, banned) {
				t.Errorf("%s no-login sentence says %q, but nothing was sent to AWS", kind, banned)
			}
		}
		// Settings → Secrets Provider holds the region, so only the
		// unreachable sentence (which asks to check the region) names it.
		// It cannot change Sharko's AWS login, so the two login sentences
		// must not send people there.
		if !strings.Contains(unreachable, "Settings → Secrets Provider") {
			t.Errorf("%s unreachable sentence does not point at Settings → Secrets Provider for the region: %q", kind, unreachable)
		}
		for name, s := range map[string]string{"refused": refused, "no login": noLogin} {
			if strings.Contains(s, "Settings → Secrets Provider") {
				t.Errorf("%s %s sentence sends people to Settings → Secrets Provider, which cannot change the AWS login: %q", kind, name, s)
			}
			if !strings.Contains(s, "restart Sharko") {
				t.Errorf("%s %s sentence does not say to restart Sharko: %q", kind, name, s)
			}
		}
		for name, s := range map[string]string{"refused": refused, "unreachable": unreachable, "no login": noLogin} {
			if strings.Contains(s, "Settings → Connections") {
				t.Errorf("%s %s sentence points at Settings → Connections: %q", kind, name, s)
			}
			if strings.Contains(s, oldAWSLoginInSettingsWords) {
				t.Errorf("%s %s sentence still carries the old words %q", kind, name, oldAWSLoginInSettingsWords)
			}
			if strings.Contains(s, oldAWSLoginDocsTitle) {
				t.Errorf("%s %s sentence still names the old docs title: %q", kind, name, s)
			}
			if !strings.Contains(s, `"Give Sharko an AWS login"`) {
				t.Errorf("%s %s sentence does not name the docs page: %q", kind, name, s)
			}
		}
	}
}

// Only the secrets store kinds get the AWS sentences. A Git or ArgoCD test
// that happens to see an AWS-shaped error keeps its own words.
func TestAWSFailureSentence_OnlyForSecretsStores(t *testing.T) {
	for _, kind := range []connectionKind{connectionKindGit, connectionKindArgoCD, connectionKindOther} {
		for _, err := range []error{awsRefusedErr(), awsUnreachableErr(), awsNoLoginErr()} {
			code := connectionFailureCodeFor(kind, err)
			if code == connectionFailureCodeAWSLoginRefused || code == connectionFailureCodeAWSUnreachable || code == connectionFailureCodeAWSNoLogin {
				t.Errorf("%s got the AWS code %s", kind, code)
			}
		}
	}
}
