package api

// plumbing_free_messages_test.go — the exact words of every 503 that B19
// reworded, pinned character for character.
//
// # Why the literal is typed out here
//
// A test that reads `if resyncFailNotRunning != resyncFailNotRunning` passes
// forever and proves nothing. That exact self-comparison was found in this
// tree, and only a break test exposed it — the constant was changed, the test
// stayed green. So every sentence below is TYPED OUT in this file. Change the
// production constant and this test fails, which is the whole point: the
// wording is a product decision, and a product decision should not move
// because somebody edited a string on the way past.
//
// # The old wording is banned too, by name
//
// Pinning only the new text lets the old text come back in a NEW constant
// beside the old one. A wrong sentence in this repo has already survived four
// review rounds because its only test asserted it was not empty. So each case
// below also names the exact words it replaced, and no shipped sentence in
// this package may contain them.

import (
	"strings"
	"testing"
)

func TestPlumbingFreeMessages_ExactText(t *testing.T) {
	cases := []struct {
		name  string
		got   string
		want  string
		outOf string // the wording it replaced, which must be gone
	}{
		{
			name:  "resyncFailNotRunning",
			got:   resyncFailNotRunning,
			want:  "The part of Sharko that manages cluster connections is not running on this server, so there is nothing to resync.",
			outOf: "the cluster reconciler is not running on this server (no in-cluster Kubernetes client or credentials provider configured) — there is nothing to resync",
		},
		{
			name:  "reconcileFailNotRunning",
			got:   reconcileFailNotRunning,
			want:  "The part of Sharko that manages cluster connections is not running on this server, so it cannot check this cluster's connection. On this deployment nothing is keeping the ArgoCD addon labels in step with Git.",
			outOf: "the cluster reconciler is not running on this server (no in-cluster Kubernetes client or credentials provider configured) — addon labels are not auto-synced to ArgoCD on this deployment",
		},
		{
			name:  "secretsEngineNotRunning",
			got:   secretsEngineNotRunning,
			want:  "The part of Sharko that delivers addon secrets to your clusters is not running on this server.",
			outOf: "secrets reconciler not configured",
		},
		{
			name:  "secretsEngineNotRunningHint",
			got:   secretsEngineNotRunningHint,
			want:  "Configure a secrets backend under Settings → Connections, or POST /api/v1/connections/ with the backend config. The engine that reads addon secrets from Git starts once a backend is configured.",
			outOf: "the git-backed secrets engine starts with it",
		},
	}

	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s reads:\n  %q\nand the product decision is:\n  %q", tc.name, tc.got, tc.want)
		}
		if strings.Contains(strings.ToLower(tc.got), strings.ToLower(tc.outOf)) {
			t.Errorf("%s still carries the wording it was meant to replace:\n  %q", tc.name, tc.outOf)
		}
		// The two rules this story is about, restated on the sentence
		// itself so a reader of THIS file sees why the words are what
		// they are.
		if strings.Contains(strings.ToLower(tc.got), "reconciler") {
			t.Errorf("%s names Sharko's own machinery to a person:\n  %q", tc.name, tc.got)
		}
		if violatesGitRule(tc.got) {
			t.Errorf("%s writes Git in lowercase prose:\n  %q", tc.name, tc.got)
		}
	}

	// The wire code beside the secrets 503 is an identifier a caller matches
	// on, not a sentence, and it deliberately keeps the old spelling. Pinned
	// so nobody "tidies" it along with the sentence and breaks every client
	// that switches on it.
	if got := secretsEngineNotConfiguredCode; got != "secrets_reconciler_not_configured" {
		t.Errorf("the wire code changed to %q — clients match on this string, it is not prose", got)
	}
}
