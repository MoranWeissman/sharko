package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-62.2 — the per-cluster role_arn must SURVIVE registration:
// before this story the UI sent role_arn in the register POST but
// RegisterClusterRequest had no field, so Go JSON decoding silently dropped
// it and a discovery-registered cross-account cluster minted EKS tokens with
// the wrong identity. These tests pin (a) persistence of role_arn on the
// managed-clusters.yaml entry, (b) the registration-time credential fetch
// forwarding the role to a role-capable backend, (c) the empty-role_arn
// no-regression contract, and (d) the inline-source rejection.

// roleCapableCredProvider is mockCredProvider plus the
// providers.RoleARNCredentialsProvider capability.
type roleCapableCredProvider struct {
	mockCredProvider
	roleCalls []string
}

func (m *roleCapableCredProvider) GetCredentialsWithRoleARN(clusterName, roleARN string) (*providers.Kubeconfig, error) {
	m.roleCalls = append(m.roleCalls, roleARN)
	return m.GetCredentials(clusterName)
}

func TestRegisterCluster_RoleARN_PersistedOnGitRecord(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"cross-account": {Server: "https://cross-account.example.com"},
	}}
	orch := New(nil, creds, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "cross-account",
		CredsSource: CredsSourceEKSToken,
		RoleARN:     "arn:aws:iam::111122223333:role/example",
		Addons:      map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q (%s)", result.Status, result.Error)
	}

	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "roleArn: arn:aws:iam::111122223333:role/example") {
		t.Fatalf("managed-clusters.yaml must record roleArn, got:\n%s", mc)
	}
	// The creds source travels alongside so later fetches route the role
	// to the backend (roleArn without eks-token would never be read).
	if !strings.Contains(mc, "credsSource: eks-token") {
		t.Fatalf("managed-clusters.yaml must record credsSource: eks-token, got:\n%s", mc)
	}
}

// The registration-time fetch (which feeds Stage-1 verification) must mint
// with the request's role — the entry is not in managed-clusters.yaml yet,
// so the stored-record resolver cannot supply it.
func TestRegisterCluster_RoleARN_ForwardedToRegistrationFetch(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &roleCapableCredProvider{mockCredProvider: mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"cross-account": {Server: "https://cross-account.example.com"},
	}}}
	orch := New(nil, creds, argocd, git, defaultGitOps(), defaultPaths(), nil)

	if _, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "cross-account",
		CredsSource: CredsSourceEKSToken,
		RoleARN:     "arn:aws:iam::111122223333:role/example",
	}); err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if len(creds.roleCalls) != 1 || creds.roleCalls[0] != "arn:aws:iam::111122223333:role/example" {
		t.Fatalf("registration fetch roleCalls = %v, want the request's role_arn", creds.roleCalls)
	}
}

// Empty role_arn keeps today's record byte-identical: NO roleArn key is
// emitted (omitempty), and a role-capable backend is fetched through the
// plain GetCredentials path.
func TestRegisterCluster_EmptyRoleARN_NoRegression(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &roleCapableCredProvider{mockCredProvider: mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://prod-eu.example.com"},
	}}}
	orch := New(nil, creds, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "prod-eu",
		CredsSource: CredsSourceEKSToken,
		Addons:      map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q (%s)", result.Status, result.Error)
	}

	mc := string(git.files["configuration/managed-clusters.yaml"])
	if strings.Contains(mc, "roleArn") {
		t.Fatalf("empty role_arn must omit the roleArn key entirely, got:\n%s", mc)
	}
	if len(creds.roleCalls) != 0 {
		t.Fatalf("empty role_arn must use plain GetCredentials, got role calls: %v", creds.roleCalls)
	}
}

// role_arn contradicts an inline-kubeconfig registration (nothing is minted
// on the inline path) — the orchestrator rejects it as a caller error (400
// at the handler via IsInvalidCredsSource).
func TestRegisterCluster_RoleARN_RejectedForInlineSource(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-inline",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		RoleARN:    "arn:aws:iam::111122223333:role/example",
	})
	if err == nil {
		t.Fatal("want error for role_arn on an inline-kubeconfig registration")
	}
	if !IsInvalidCredsSource(err) {
		t.Fatalf("err = %v, want an *InvalidCredsSourceError (→ 400)", err)
	}
	if !strings.Contains(err.Error(), "role_arn") {
		t.Fatalf("err = %v, want a role_arn-specific message", err)
	}
}
