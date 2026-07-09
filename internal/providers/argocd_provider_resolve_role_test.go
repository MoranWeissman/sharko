package providers

import (
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

// TestResolveRoleARN_AWSAuthConfig_WithRole covers the awsAuthConfig shape
// carrying a role — the connection doctor's (V2-cleanup-88.4) "cross-account
// role assumable" check uses this to find the role WITHOUT minting a token.
func TestResolveRoleARN_AWSAuthConfig_WithRole(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": {
			"clusterName": "my-eks-cluster",
			"roleARN": "arn:aws:iam::123456789012:role/example"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecretWithRegion(
		"prod-eks", "prod-eks",
		"https://abc.eks.amazonaws.com", "us-east-1",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	roleARN, region, ok, err := provider.ResolveRoleARN("prod-eks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true (AWS auth shape is present)")
	}
	if roleARN != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("roleARN = %q, want the parsed role", roleARN)
	}
	if region != "us-east-1" {
		t.Errorf("region = %q, want %q (from the Secret's region label)", region, "us-east-1")
	}
}

// TestResolveRoleARN_AWSAuthConfig_NoRole covers awsAuthConfig with no role
// (Sharko would mint with its own base identity) — ok is still true (an AWS
// auth shape IS in play) but roleARN is empty.
func TestResolveRoleARN_AWSAuthConfig_NoRole(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": { "clusterName": "my-eks-cluster" },
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecretWithRegion(
		"prod-eks", "prod-eks",
		"https://abc.eks.amazonaws.com", "us-east-1",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	roleARN, _, ok, err := provider.ResolveRoleARN("prod-eks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true (AWS auth shape is present, even with no role)")
	}
	if roleARN != "" {
		t.Errorf("roleARN = %q, want empty (no role in awsAuthConfig)", roleARN)
	}
}

// TestResolveRoleARN_ExecProviderConfig_KnownAWSCommand_WithRole covers the
// execProviderConfig shape with a known AWS authenticator command and a
// --role-arn flag.
func TestResolveRoleARN_ExecProviderConfig_KnownAWSCommand_WithRole(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "aws-iam-authenticator",
			"args": ["token", "-i", "my-eks-cluster", "--role-arn", "arn:aws:iam::123456789012:role/example"]
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecretWithRegion(
		"prod-eks", "prod-eks",
		"https://abc.eks.amazonaws.com", "eu-west-1",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	roleARN, region, ok, err := provider.ResolveRoleARN("prod-eks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if roleARN != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("roleARN = %q, want the parsed role", roleARN)
	}
	if region != "eu-west-1" {
		t.Errorf("region = %q, want %q", region, "eu-west-1")
	}
}

// TestResolveRoleARN_ExecProviderConfig_UnknownCommand covers the
// not-AWS-shaped exec-plugin case — ok is false, there is nothing to assume.
func TestResolveRoleARN_ExecProviderConfig_UnknownCommand(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "gcloud",
			"args": ["container", "clusters", "get-credentials"]
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"gke-cluster", "gke-cluster",
		"https://34.1.2.3",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	roleARN, _, ok, err := provider.ResolveRoleARN("gke-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false (not an AWS auth shape)")
	}
	if roleARN != "" {
		t.Errorf("roleARN = %q, want empty", roleARN)
	}
}

// TestResolveRoleARN_BearerToken_NotOK covers the plain bearerToken shape —
// no AWS auth shape at all, so ok is false.
func TestResolveRoleARN_BearerToken_NotOK(t *testing.T) {
	configJSON := `{
		"bearerToken": "eyJhbGciOi-fake-token",
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eu", "prod-eu",
		"https://api.cluster-1.example.com:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	roleARN, region, ok, err := provider.ResolveRoleARN("prod-eu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false (bearerToken shape has no AWS role)")
	}
	if roleARN != "" || region != "" {
		t.Errorf("roleARN = %q, region = %q, want both empty", roleARN, region)
	}
}

// TestResolveRoleARN_ClusterNotFound covers the not-found path — the doctor
// must not crash or misreport when the cluster secret doesn't exist.
func TestResolveRoleARN_ClusterNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, _, ok, err := provider.ResolveRoleARN("does-not-exist")
	if err == nil {
		t.Fatal("expected an error for an unknown cluster, got nil")
	}
	if ok {
		t.Fatal("ok = true, want false on error")
	}
}
