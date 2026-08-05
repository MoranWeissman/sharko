package clusterreconciler

import (
	"strings"
	"testing"
)

// failure_sentence_test.go pins P1-B B2: every Failed record this package
// can produce maps to a safe, canned sentence — never the raw error text
// (or the raw pass-level abort reason) that produced it, and never a
// machinery word this project's UI voice rule forbids in visible text.

func TestFailureSentence_EmptyStaysEmpty(t *testing.T) {
	if got := FailureSentence(""); got != "" {
		t.Errorf("FailureSentence(\"\") = %q, want empty", got)
	}
}

// TestFailureSentence_MappedNeverEqualsRaw pins the core S8/#123 guarantee
// for every failure kind this package's call sites actually produce — one
// case per recordReconcile(OutcomeFailed, ...) call site in reconciler.go
// and check_pass.go, built from real, representative wrapped-error text.
func TestFailureSentence_MappedNeverEqualsRaw(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			"pollOnce git read abort",
			"reconciler pass aborted: git read failed: dial tcp: i/o timeout",
		},
		{
			"pollOnce schema validation abort",
			"reconciler pass aborted: schema validation failed: managed-clusters.yaml: unknown field \"foo\"",
		},
		{
			"checkOnce git_read abort (Kind-constant spelling)",
			"reconciler pass aborted: git_read failed: dial tcp: i/o timeout",
		},
		{
			"checkOnce schema_validation abort (Kind-constant spelling)",
			"reconciler pass aborted: schema_validation failed: unexpected EOF",
		},
		{
			"reconcileDiff listing secrets abort",
			"reconciler pass aborted: listing ArgoCD cluster secrets failed: Get \"https://argocd/api\": context deadline exceeded",
		},
		{
			"createOne pre-create Get failure",
			"Sharko couldn't check whether an ArgoCD cluster secret already exists for this cluster: etcdserver: request timed out",
		},
		{
			"createOne vault credentials failure",
			"Sharko couldn't fetch this cluster's credentials from the secrets backend: AccessDenied: User is not authorized",
		},
		{
			"createOne build payload failure",
			"Sharko couldn't build the ArgoCD cluster secret for this cluster: invalid kubeconfig: missing CA data",
		},
		{
			"createOne Secret Create failure",
			"Sharko couldn't create the ArgoCD cluster secret for this cluster: secrets \"prod-eu\" already exists",
		},
		{
			"syncSelfManaged label sync failure",
			"Sharko couldn't sync addon labels onto this cluster's self-managed ArgoCD secret: connection refused",
		},
		{
			"syncConnectivityCheckLabel failure",
			"Sharko couldn't converge the connectivity-check label on this cluster's ArgoCD secret: Patch \"...\": EOF",
		},
		{
			"selfHealManagedCluster write failure",
			"Sharko couldn't converge git-desired addon labels on this drifted managed-cluster Secret: Update \"...\": conflict",
		},
		{
			"selfHealManagedCluster post-write verify read failure",
			"Sharko wrote the self-heal but couldn't re-read the cluster Secret to confirm it converged: Get \"...\": not found",
		},
		{
			"selfHealManagedCluster residual drift after write",
			"self-heal did not fully converge this managed-cluster Secret's addon labels",
		},
		{
			"selfHealManagedCluster ownership lost after write",
			"self-heal left this managed-cluster Secret WITHOUT its Sharko ownership label — refusing to report it healed",
		},
		{
			"deleteOne orphan delete failure",
			"Sharko couldn't remove this orphaned ArgoCD cluster secret: Delete \"...\": timeout",
		},
		{
			"checkOneSecret read-by-name failure",
			"Sharko couldn't read this cluster's ArgoCD secret to check it: Get \"...\": unauthorized",
		},
		{
			"unrecognized message shape (defensive default)",
			"some future Message text %!s(MISSING) that this function has never seen before",
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
