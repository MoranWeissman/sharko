package secrets

import (
	"strings"
	"testing"
)

// failure_sentence_test.go pins P1-B B2 for the values engine: every shape
// LastError() can carry maps to a safe, canned sentence — never the raw
// error text, and never a machinery word this project's UI voice rule
// forbids in visible text.

func TestFailureSentence_EmptyStaysEmpty(t *testing.T) {
	if got := FailureSentence(""); got != "" {
		t.Errorf("FailureSentence(\"\") = %q, want empty", got)
	}
}

func TestFailureSentence_MappedNeverEqualsRaw(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			"plan-level: no catalog readable",
			`no addon catalog could be read on branch "main", so no addon secrets can be pushed: catalog.yaml: not found; configuration/addons-catalog.yaml: not found`,
		},
		{
			"plan-level: managed-clusters read failure",
			`could not read configuration/managed-clusters.yaml: dial tcp: i/o timeout`,
		},
		{
			"per-item: incomplete catalog secret definition",
			`cluster=prod-eu addon=datadog secret=datadog-secrets: the secret definition in the catalog has no secret name and no namespace — fill that in and Sharko can push it`,
		},
		{
			"per-item: credentials failure",
			`cluster=prod-eu addon=datadog secret=datadog-secrets: getting credentials: AccessDenied: User is not authorized`,
		},
		{
			"per-item: cluster connect failure",
			`cluster=prod-eu addon=datadog secret=datadog-secrets: connecting to cluster: dial tcp: connection refused`,
		},
		{
			"per-item: provider fetch failure",
			`cluster=prod-eu addon=datadog secret=datadog-secrets: fetching "secrets/datadog/api-key" from provider: AccessDeniedException`,
		},
		{
			"per-item: existing secret read failure",
			`cluster=prod-eu addon=datadog secret=datadog-secrets: checking existing secret: etcdserver: request timed out`,
		},
		{
			"per-item: create failure",
			`cluster=prod-eu addon=datadog secret=datadog-secrets: creating secret: secrets "datadog-secrets" already exists`,
		},
		{
			"per-item: update failure",
			`cluster=prod-eu addon=datadog secret=datadog-secrets: updating secret: Patch "...": conflict`,
		},
		{
			"unrecognized message shape (defensive default)",
			"some future error shape %!s(MISSING) this function has never seen",
		},
	}

	forbidden := []string{"%!", "err=", "converge", "*errors.errorString", "*url.Error"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureSentence(tc.raw)
			if got == "" {
				t.Fatalf("FailureSentence(%q) = empty, want a non-empty canned sentence", tc.raw)
			}
			if got == tc.raw {
				t.Errorf("FailureSentence(%q) returned the raw message unchanged", tc.raw)
			}
			for _, bad := range forbidden {
				if strings.Contains(got, bad) {
					t.Errorf("FailureSentence(%q) = %q, contains forbidden substring %q", tc.raw, got, bad)
				}
			}
		})
	}
}
