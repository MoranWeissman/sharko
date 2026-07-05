package providers

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// V2-cleanup-53.1 — pin that creds_source=eks-token needs NO separate wiring:
// an EKS-JSON payload fetched through the (restored) aws-sm cluster-creds arm
// is sniffed as structured and dispatched down the STS/token path, while a
// raw kubeconfig YAML payload is dispatched down the parse path. The STS call
// itself is seamed per-instance via eksTokenFn (the established per-instance
// test-seam convention), so no real AWS credentials are needed.

const testCAPEM = "-----BEGIN CERTIFICATE-----\nMIIB fake ca for tests\n-----END CERTIFICATE-----\n"

func testEKSJSONPayload() []byte {
	return []byte(`{
		"clusterName": "prod-eu",
		"host": "https://abc123.gr7.eu-west-1.eks.example.com",
		"caData": "` + base64.StdEncoding.EncodeToString([]byte(testCAPEM)) + `",
		"region": "eu-west-1",
		"roleArn": "arn:aws:iam::000000000000:role/EKSReadRole"
	}`)
}

const testRawKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://raw.kubeconfig.example.com
    insecure-skip-tls-verify: true
  name: raw
contexts:
- context:
    cluster: raw
    user: raw
  name: raw
current-context: raw
users:
- name: raw
  user:
    token: raw-token
`

// --- payload sniff (dispatch decision) -------------------------------------

func TestSniffStructuredEKSSecret_EKSJSON(t *testing.T) {
	structured, ok := sniffStructuredEKSSecret(testEKSJSONPayload())
	if !ok {
		t.Fatal("expected EKS-JSON payload to be sniffed as structured")
	}
	if structured.ClusterName != "prod-eu" {
		t.Errorf("clusterName = %q, want prod-eu", structured.ClusterName)
	}
	if structured.Region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", structured.Region)
	}
	if structured.RoleARN != "arn:aws:iam::000000000000:role/EKSReadRole" {
		t.Errorf("roleArn = %q, want the per-secret role", structured.RoleARN)
	}
}

func TestSniffStructuredEKSSecret_RawKubeconfigYAML(t *testing.T) {
	if _, ok := sniffStructuredEKSSecret([]byte(testRawKubeconfig)); ok {
		t.Fatal("raw kubeconfig YAML must NOT be sniffed as structured EKS-JSON")
	}
}

func TestSniffStructuredEKSSecret_JSONWithoutHost(t *testing.T) {
	// JSON that parses but has no host (e.g. an addon-secret value that
	// happens to be JSON) must fall through to the raw path.
	if _, ok := sniffStructuredEKSSecret([]byte(`{"apiKey": "not-a-cluster"}`)); ok {
		t.Fatal("JSON without a host must NOT be sniffed as structured EKS-JSON")
	}
}

// --- STS/token path through the aws-sm arm ----------------------------------

// An EKS-JSON payload must go down the STS/token path: eksTokenFn is invoked
// with the cluster name, region, and (per-secret) role ARN, and the minted
// token lands in the returned Kubeconfig.
func TestBuildFromStructured_EKSJSON_GoesDownSTSTokenPath(t *testing.T) {
	var gotCluster, gotRegion, gotRole string
	p := &AWSSecretsManagerProvider{
		roleARN: "arn:aws:iam::000000000000:role/ProviderDefault",
		eksTokenFn: func(_ context.Context, clusterName, region, roleARN string) (string, error) {
			gotCluster, gotRegion, gotRole = clusterName, region, roleARN
			return "k8s-aws-v1.fake-token", nil
		},
	}

	structured, ok := sniffStructuredEKSSecret(testEKSJSONPayload())
	if !ok {
		t.Fatal("test payload must sniff as structured")
	}

	kc, err := p.buildFromStructured(structured, "")
	if err != nil {
		t.Fatalf("buildFromStructured: %v", err)
	}

	if gotCluster != "prod-eu" || gotRegion != "eu-west-1" {
		t.Errorf("eksTokenFn called with (%q, %q), want (prod-eu, eu-west-1)", gotCluster, gotRegion)
	}
	// The per-secret roleArn must win over the provider-level default.
	if gotRole != "arn:aws:iam::000000000000:role/EKSReadRole" {
		t.Errorf("eksTokenFn roleARN = %q, want the per-secret role", gotRole)
	}
	if kc.Token != "k8s-aws-v1.fake-token" {
		t.Errorf("Kubeconfig.Token = %q, want the minted STS token", kc.Token)
	}
	if kc.Server != "https://abc123.gr7.eu-west-1.eks.example.com" {
		t.Errorf("Kubeconfig.Server = %q, want the EKS host", kc.Server)
	}
	if !strings.Contains(string(kc.Raw), "k8s-aws-v1.fake-token") {
		t.Error("synthesized kubeconfig YAML must embed the minted token")
	}
}

// When the secret omits roleArn, the provider-level default (RoleARN from the
// cluster-test config) must be used.
func TestBuildFromStructured_FallsBackToProviderRoleARN(t *testing.T) {
	var gotRole string
	p := &AWSSecretsManagerProvider{
		roleARN: "arn:aws:iam::000000000000:role/ProviderDefault",
		eksTokenFn: func(_ context.Context, _, _, roleARN string) (string, error) {
			gotRole = roleARN
			return "k8s-aws-v1.fake-token", nil
		},
	}

	structured := structuredEKSSecret{
		ClusterName: "prod-eu",
		Host:        "https://abc123.gr7.eu-west-1.eks.example.com",
		CAData:      base64.StdEncoding.EncodeToString([]byte(testCAPEM)),
		Region:      "eu-west-1",
		// RoleARN intentionally empty.
	}

	if _, err := p.buildFromStructured(structured, ""); err != nil {
		t.Fatalf("buildFromStructured: %v", err)
	}
	if gotRole != "arn:aws:iam::000000000000:role/ProviderDefault" {
		t.Errorf("eksTokenFn roleARN = %q, want the provider-level default", gotRole)
	}
}

// V2-cleanup-62.2 — the token-mint role-ARN precedence, pinned for all three
// sources plus the all-empty case:
//
//	structured-SM-secret roleArn  >  per-cluster registered role_arn  >
//	connection-level provider default  >  "" (no role assumed).
//
// The per-cluster value is the roleArn recorded on the cluster's
// managed-clusters.yaml entry at registration (e.g. the cross-account role a
// discovery-registered cluster was found with).
func TestBuildFromStructured_RoleARNPrecedence(t *testing.T) {
	const (
		secretRole   = "arn:aws:iam::111122223333:role/FromSecret"
		clusterRole  = "arn:aws:iam::111122223333:role/FromClusterEntry"
		providerRole = "arn:aws:iam::111122223333:role/ConnectionDefault"
	)

	tests := []struct {
		name         string
		secretRole   string
		clusterRole  string
		providerRole string
		wantRole     string
	}{
		{"SM-secret roleArn wins over per-cluster AND default", secretRole, clusterRole, providerRole, secretRole},
		{"per-cluster role wins over the connection default", "", clusterRole, providerRole, clusterRole},
		{"connection default when secret and cluster are empty", "", "", providerRole, providerRole},
		{"all empty: no role assumed (byte-identical legacy behavior)", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotRole string
			p := &AWSSecretsManagerProvider{
				roleARN: tt.providerRole,
				eksTokenFn: func(_ context.Context, _, _, roleARN string) (string, error) {
					gotRole = roleARN
					return "k8s-aws-v1.fake-token", nil
				},
			}
			structured := structuredEKSSecret{
				ClusterName: "prod-eu",
				Host:        "https://abc123.gr7.eu-west-1.eks.example.com",
				CAData:      base64.StdEncoding.EncodeToString([]byte(testCAPEM)),
				Region:      "eu-west-1",
				RoleARN:     tt.secretRole,
			}
			if _, err := p.buildFromStructured(structured, tt.clusterRole); err != nil {
				t.Fatalf("buildFromStructured: %v", err)
			}
			if gotRole != tt.wantRole {
				t.Errorf("eksTokenFn roleARN = %q, want %q", gotRole, tt.wantRole)
			}
		})
	}
}

// GetCredentialsWithRoleARN("", ...) delegation contract: GetCredentials must
// stay byte-identical to the with-role variant called with an empty role —
// GetCredentials(name) == GetCredentialsWithRoleARN(name, ""). Pinned at the
// capability seam so a future refactor cannot fork the two paths.
func TestAWSSMProvider_ImplementsRoleARNCapability(t *testing.T) {
	var p ClusterCredentialsProvider = &AWSSecretsManagerProvider{}
	if _, ok := p.(RoleARNCredentialsProvider); !ok {
		t.Fatal("AWSSecretsManagerProvider must implement RoleARNCredentialsProvider")
	}
}

// A raw kubeconfig payload must go down the parse path (no token minting).
func TestBuildFromRawKubeconfig_ParsePath(t *testing.T) {
	p := &AWSSecretsManagerProvider{
		eksTokenFn: func(_ context.Context, _, _, _ string) (string, error) {
			t.Fatal("raw kubeconfig payload must NOT mint an STS token")
			return "", nil
		},
	}

	kc, err := p.buildFromRawKubeconfig([]byte(testRawKubeconfig), "some-secret")
	if err != nil {
		t.Fatalf("buildFromRawKubeconfig: %v", err)
	}
	if kc.Server != "https://raw.kubeconfig.example.com" {
		t.Errorf("Server = %q, want the kubeconfig's server", kc.Server)
	}
	if kc.Token != "raw-token" {
		t.Errorf("Token = %q, want the kubeconfig's bearer token", kc.Token)
	}
}
