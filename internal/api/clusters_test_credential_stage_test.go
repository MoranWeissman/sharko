package api

// clusters_test_credential_stage_test.go — the Go half of a two-language pin.
//
// POST /clusters/{name}/test answers with a `stage` naming the step that
// failed. One of those values, "credentials", is not just diagnostic text: the
// browser ROUTES on it. When the adopt dialog sees it, it keeps the cluster
// selected and adoptable, because a failed credential lookup is the normal
// case for adoption (internal/orchestrator/adopt.go treats every one of them
// that way) and nothing is known against a cluster that was never contacted.
//
// The browser used to work that out by lower-casing the server's
// error_message and hunting for "secret", "not found", "credential",
// "unavailable" and "no credentials available". Then the credentials hotfix
// made every credentials-backend failure carry ONE fixed sentence, which
// contains none of those phrases — so the search answered "no" for every
// credentials failure there is, and the credentials-optional contract stopped
// working with nothing failing anywhere.
//
// Routing now reads the typed stage. This file makes a Go-side rename of that
// value loud instead of silent, in two ways:
//
//   - the constant is compared with a WRITTEN-OUT literal, not with itself;
//   - the browser file that routes on it is read, and must carry the same
//     value.
//
// It is the same shape as the banned-wording pair that already spans the two
// languages (internal/api/connection_messages_round6_test.go and
// ui/src/__tests__/bannedWordingSweep.test.ts): a half in each, each half
// naming the literal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// browserCredentialStageDeclaration is the exact line the browser must carry.
// Written out rather than assembled from verifyStageCredentials: building it
// from the Go constant would make both sides move together and prove nothing.
const browserCredentialStageDeclaration = "export const VERIFY_STAGE_CREDENTIALS = 'credentials'"

// browserCredentialStageFile is the browser file that declares it. Named
// rather than searched for, so a move shows up here as a failure to find the
// file instead of as a sweep over nothing.
const browserCredentialStageFile = "ui/src/services/api.ts"

func TestTestCluster_CredentialFailureStageIsPinned(t *testing.T) {
	if verifyStageCredentials != "credentials" {
		t.Fatalf(`the credential-failure stage is %q, and the browser routes on "credentials".

Renaming this value changes which screen an operator sees when a cluster's
sign-in details cannot be read: the adopt dialog stops keeping that cluster
selected and shows it as a failed verification instead, on a path the server
would have adopted happily. If the rename is deliberate, change
VERIFY_STAGE_CREDENTIALS in %s and this literal together.`,
			verifyStageCredentials, browserCredentialStageFile)
	}
}

func TestTestCluster_BrowserCarriesTheSameCredentialStage(t *testing.T) {
	path := filepath.Join(repoRootForSweep(t), browserCredentialStageFile)
	body, err := os.ReadFile(path) //nolint:gosec // a fixed path under the repo root
	if err != nil {
		t.Fatalf(`could not read %s: %v

This guard reads the browser file that routes on the credential-failure
stage. If the file moved, point browserCredentialStageFile at its new home —
leaving it here would mean this test passes having read nothing.`,
			browserCredentialStageFile, err)
	}
	if !strings.Contains(string(body), browserCredentialStageDeclaration) {
		t.Errorf(`%s no longer declares:

  %s

The browser decides whether an unverifiable cluster stays adoptable by
comparing the response's stage against that constant. If the two sides stop
agreeing on the value, the comparison quietly never matches again — which is
exactly how the previous version of this contract died.`,
			browserCredentialStageFile, browserCredentialStageDeclaration)
	}
}
