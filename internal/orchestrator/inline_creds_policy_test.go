package orchestrator

import (
	"context"
	"testing"
)

// V2-cleanup-89.6 — admin-level kill switch for inline credential paste.
//
// These tests pin RegisterCluster's allow_inline_credentials gate:
//
//  1. When the gate function is wired and returns false, an inline-kubeconfig
//     registration that actually supplies kubeconfig bytes is rejected with
//     *InlineCredentialsDisabledError (single AND batch — batch calls
//     RegisterCluster per cluster, so no separate wiring is needed there).
//  2. A connection-only registration (inline creds source, but NO kubeconfig
//     supplied) is NOT blocked, even when the gate returns false — the
//     setting governs pasted bytes, not the source label.
//  3. When the gate function returns true, or is not wired at all (nil,
//     matching pre-89.6 behavior), registration proceeds normally.

func alwaysDisallow(context.Context) bool { return false }
func alwaysAllow(context.Context) bool    { return true }

func TestRegisterCluster_InlineCredentialsDisabled_RejectsPastedKubeconfig(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetAllowInlineCredentialsFn(alwaysDisallow)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
	})
	if err == nil {
		t.Fatal("expected an error when allow_inline_credentials is false and kubeconfig is supplied")
	}
	if !IsInlineCredentialsDisabled(err) {
		t.Errorf("expected *InlineCredentialsDisabledError, got %T: %v", err, err)
	}
	wantMsg := "inline credential paste is disabled on this server — point at your secret store instead, or ask your admin to enable allow_inline_credentials"
	if err.Error() != wantMsg {
		t.Errorf("error message = %q, want %q", err.Error(), wantMsg)
	}
}

func TestRegisterCluster_InlineCredentialsDisabled_ExplicitCredsSource(t *testing.T) {
	// Same gate, reached via the explicit creds_source field instead of the
	// legacy Provider="kubeconfig" derivation — pins that the gate keys off
	// the RESOLVED source, not the raw request shape.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetAllowInlineCredentialsFn(alwaysDisallow)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "kind-sharko-2",
		CredsSource: CredsSourceInlineKubeconfig,
		Kubeconfig:  v125TestBearerKubeconfig,
	})
	if !IsInlineCredentialsDisabled(err) {
		t.Errorf("expected *InlineCredentialsDisabledError, got %T: %v", err, err)
	}
}

func TestRegisterCluster_InlineCredentialsDisabled_ConnectionOnlyStaysValid(t *testing.T) {
	// No kubeconfig supplied at all — connection-only registration. Even
	// though the effective creds source derives to inline-kubeconfig (no
	// Provider, no creds_source, no kubeconfig — see ResolveCredsSource),
	// the gate must NOT fire because there are no pasted bytes to forbid.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetAllowInlineCredentialsFn(alwaysDisallow)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "connection-only",
	})
	if err != nil {
		t.Fatalf("expected connection-only registration to succeed even with allow_inline_credentials=false, got: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
}

func TestRegisterCluster_InlineCredentialsAllowed_Succeeds(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetAllowInlineCredentialsFn(alwaysAllow)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko-allowed",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
	})
	if err != nil {
		t.Fatalf("expected success when allow_inline_credentials is true, got: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
}

func TestRegisterCluster_InlineCredentialsGate_NilFnDefaultsToAllowed(t *testing.T) {
	// No SetAllowInlineCredentialsFn call at all — matches every pre-89.6
	// caller and every other orchestrator test in this package. Must behave
	// exactly as before: inline kubeconfig registration succeeds.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko-default",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
	})
	if err != nil {
		t.Fatalf("expected success with no gate function wired, got: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
}

func TestRegisterClusterBatch_InlineCredentialsDisabled_RejectsOnlyPastedMembers(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetAllowInlineCredentialsFn(alwaysDisallow)

	requests := []RegisterClusterRequest{
		// Pasted inline kubeconfig — must be rejected.
		{Name: "pasted", Provider: "kubeconfig", Kubeconfig: v125TestBearerKubeconfig},
		// Connection-only (no kubeconfig, no backend creds either) — must
		// still succeed; the gate never fires for it.
		{Name: "connection-only-batch"},
	}

	result := orch.RegisterClusterBatch(context.Background(), requests)

	if result.Total != 2 {
		t.Fatalf("expected total=2, got %d", result.Total)
	}
	if result.Failed != 1 {
		t.Errorf("expected failed=1 (the pasted-kubeconfig member), got %d", result.Failed)
	}
	if result.Succeeded != 1 {
		t.Errorf("expected succeeded=1 (the connection-only member), got %d", result.Succeeded)
	}

	var pastedResult, connOnlyResult *RegisterClusterResult
	for i := range result.Results {
		switch result.Results[i].Cluster.Name {
		case "pasted":
			pastedResult = &result.Results[i]
		case "connection-only-batch":
			connOnlyResult = &result.Results[i]
		}
	}
	if pastedResult == nil || pastedResult.Status != "failed" {
		t.Fatalf("expected 'pasted' to fail, got %+v", pastedResult)
	}
	if pastedResult.Error == "" {
		t.Error("expected an error message on the failed batch member")
	}
	if connOnlyResult == nil || connOnlyResult.Status != "success" {
		t.Fatalf("expected 'connection-only-batch' to succeed, got %+v", connOnlyResult)
	}
}
